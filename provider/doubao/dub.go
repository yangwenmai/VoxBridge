package doubao

import (
	"context"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/yangwenmai/voxbridge/core"
	"github.com/yangwenmai/voxbridge/provider/internal/wire"
)

// List prices used to report the cost of a run.
const (
	mtInPerToken   = 1.8 / 1e6
	mtOutPerToken  = 5.4 / 1e6
	ttsPerChar     = 3.0 / 1e4
	ttsSpeechRate  = 10
	ttsSampleRate  = 24000
	ttsServiceName = "语音合成 2.0"
	mtServiceName  = "机器翻译"
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
