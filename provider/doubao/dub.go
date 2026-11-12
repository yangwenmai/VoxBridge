package doubao

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/yangwenmai/voxbridge/core"
	"github.com/yangwenmai/voxbridge/media"
	"github.com/yangwenmai/voxbridge/provider/internal/wire"
)

// List prices used to report the cost of a run.
const (
	mtInPerToken   = 1.8 / 1e6
	mtOutPerToken  = 5.4 / 1e6
	ttsPerChar     = 3.0 / 1e4
	asrPerSecond   = 4.5 / 3600
	asrURL         = "https://" + host + "/api/v3/auc/bigmodel/recognize/flash"
	asrRes         = "volc.bigasr.auc_turbo"
	asrChunk       = 100 * time.Minute
	ttsSpeechRate  = 10
	ttsSampleRate  = 24000
	asrSampleRate  = 16000
	asrBitrate     = "32k"
	asrUserID      = "voxbridge"
	asrStatusOK    = "20000000"
	ttsServiceName = "语音合成 2.0"
	mtServiceName  = "机器翻译"
	asrServiceName = "录音文件识别极速版"
)

type mtUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// mtReply carries usage per translation; older replies had it at the top.
type mtReply struct {
	Data struct {
		TranslationList []struct {
			Translation string  `json:"translation"`
			Usage       mtUsage `json:"usage"`
		} `json:"translation_list"`
	} `json:"data"`
	Usage mtUsage `json:"usage"`
}

// Translate sends texts in batches of TranslateBatchLimit.
func (c *Client) Translate(ctx context.Context, texts []string, to core.Lang) ([]string, error) {
	out := make([]string, 0, len(texts))
	for start := 0; start < len(texts); start += TranslateBatchLimit {
		end := min(start+TranslateBatchLimit, len(texts))
		batch := texts[start:end]
		reply, err := c.do(ctx, call{
			cap: core.CapTranslate, service: mtServiceName, url: mtURL, resource: mtRes,
			body:  map[string]any{"target_language": string(to), "text_list": batch},
			check: checkJSON(mtServiceName),
		})
		if err != nil {
			return nil, err
		}
		var r mtReply
		if err := json.Unmarshal(reply.Body, &r); err != nil || len(r.Data.TranslationList) != len(batch) {
			return nil, &core.ProviderError{Provider: ID, Service: mtServiceName, Kind: core.ErrServer,
				Message: fmt.Sprintf("返回了 %d 条译文，请求了 %d 条", len(r.Data.TranslationList), len(batch)),
				LogID:   reply.Header.Get("X-Tt-Logid")}
		}
		in, outTok := r.Usage.PromptTokens, r.Usage.CompletionTokens
		for _, t := range r.Data.TranslationList {
			in += t.Usage.PromptTokens
			outTok += t.Usage.CompletionTokens
		}
		if in == 0 && outTok == 0 {
			for k, t := range batch {
				in += utf8.RuneCountInString(t)
				outTok += utf8.RuneCountInString(r.Data.TranslationList[k].Translation)
			}
		}
		c.bill(core.CapTranslate, float64(in+outTok), float64(in)*mtInPerToken+float64(outTok)*mtOutPerToken)
		for _, t := range r.Data.TranslationList {
			out = append(out, t.Translation)
		}
	}
	return out, nil
}

// Synthesize reads text in voice as 24 kHz mp3.
func (c *Client) Synthesize(ctx context.Context, text, voice string) (core.Audio, error) {
	var audio []byte
	_, err := c.do(ctx, call{
		cap: core.CapSynthesize, service: ttsServiceName, url: ttsURL, resource: ttsRes,
		body: map[string]any{"req_params": map[string]any{
			"text": text, "speaker": voice,
			"audio_params": map[string]any{"format": "mp3", "sample_rate": ttsSampleRate, "speech_rate": ttsSpeechRate},
		}},
		check: func(r wire.Reply) *core.ProviderError {
			if r.Status >= 400 {
				return checkJSON(ttsServiceName)(r)
			}
			a, err := DecodeTTS(r.Body)
			if pe, ok := err.(*core.ProviderError); ok {
				pe.Service = ttsServiceName
				return pe
			}
			if err != nil {
				return &core.ProviderError{Provider: ID, Service: ttsServiceName, Kind: core.ErrServer, Message: err.Error()}
			}
			audio = a
			return nil
		},
	})
	if err != nil {
		return core.Audio{}, err
	}
	c.bill(core.CapSynthesize, float64(utf8.RuneCountInString(text)), float64(utf8.RuneCountInString(text))*ttsPerChar)
	return core.Audio{Data: audio, Format: "mp3", SampleRate: ttsSampleRate}, nil
}

type asrReply struct {
	Result struct {
		Utterances []struct {
			StartTime int64  `json:"start_time"`
			EndTime   int64  `json:"end_time"`
			Text      string `json:"text"`
			Speaker   string `json:"speaker"`
			Additions struct {
				Speaker string `json:"speaker"`
			} `json:"additions"`
		} `json:"utterances"`
	} `json:"result"`
}

// Transcribe sends the audio in chunks of up to 100 minutes, the flash
// recognizer's limit being 2 hours and 100 MB per request.
func (c *Client) Transcribe(ctx context.Context, in core.AudioInput) ([]core.Line, error) {
	probe, err := media.FFprobe(ctx, in.URL, in.Headers)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "voxbridge-asr-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	var lines []core.Line
	for offset := time.Duration(0); offset < time.Duration(probe.DurationMs)*time.Millisecond; offset += asrChunk {
		path := filepath.Join(dir, fmt.Sprintf("chunk-%d.mp3", offset/asrChunk))
		args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}, media.HeaderArgs(in.Headers)...)
		args = append(args, "-ss", strconv.FormatFloat(offset.Seconds(), 'f', 3, 64), "-t", strconv.FormatFloat(asrChunk.Seconds(), 'f', 3, 64),
			"-i", in.URL, "-vn", "-ac", "1", "-ar", strconv.Itoa(asrSampleRate), "-b:a", asrBitrate, path)
		if _, err := media.Run(ctx, "ffmpeg", args...); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		chunk, err := c.recognize(ctx, data)
		if err != nil {
			return nil, err
		}
		chunkMs := min(asrChunk.Milliseconds(), probe.DurationMs-offset.Milliseconds())
		c.bill(core.CapRecognize, float64(chunkMs)/1000, float64(chunkMs)/1000*asrPerSecond)
		for _, l := range chunk {
			l.StartMs += offset.Milliseconds()
			if l.EndMs != nil {
				e := *l.EndMs + offset.Milliseconds()
				l.EndMs = &e
			}
			lines = append(lines, l)
		}
	}
	return lines, nil
}

func (c *Client) recognize(ctx context.Context, audio []byte) ([]core.Line, error) {
	h := http.Header{}
	h.Set("X-Api-Sequence", "-1")
	reply, err := c.do(ctx, call{
		cap: core.CapRecognize, service: asrServiceName, url: asrURL, resource: asrRes, header: h,
		body: map[string]any{
			"user":  map[string]any{"uid": asrUserID},
			"audio": map[string]any{"data": base64.StdEncoding.EncodeToString(audio)},
			"request": map[string]any{
				"model_name": "bigmodel", "enable_itn": true, "enable_punc": true,
				"enable_speaker_info": true, "show_utterances": true,
			},
		},
		check: checkASR,
	})
	if err != nil {
		return nil, err
	}
	var r asrReply
	if err := json.Unmarshal(reply.Body, &r); err != nil {
		return nil, &core.ProviderError{Provider: ID, Service: asrServiceName, Kind: core.ErrServer, Message: "识别结果看不懂", LogID: reply.Header.Get("X-Tt-Logid")}
	}
	lines := make([]core.Line, 0, len(r.Result.Utterances))
	for _, u := range r.Result.Utterances {
		end := u.EndTime
		l := core.Line{StartMs: u.StartTime, EndMs: &end, Text: u.Text}
		if s := firstNonEmpty(u.Speaker, u.Additions.Speaker); s != "" {
			l.Speaker = &s
		}
		lines = append(lines, l)
	}
	return lines, nil
}

// checkASR reads the status the flash recognizer puts in response headers.
func checkASR(r wire.Reply) *core.ProviderError {
	status := r.Header.Get("X-Api-Status-Code")
	if r.Status < 400 && status == asrStatusOK {
		return nil
	}
	code, msg := parseReply(r.Body)
	if status != "" {
		if n, err := strconv.Atoi(status); err == nil {
			code = n
		}
		if m := r.Header.Get("X-Api-Message"); m != "" {
			msg = m
		}
	}
	if e := Classify(asrServiceName, r.Status, code, msg, r.Header.Get("X-Tt-Logid")); e != nil {
		return e
	}
	return &core.ProviderError{Provider: ID, Service: asrServiceName, Kind: core.ErrServer, HTTP: r.Status, Message: "没有返回识别状态", LogID: r.Header.Get("X-Tt-Logid")}
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
