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

	"github.com/yangwenmai/voxbridge/core"
	"github.com/yangwenmai/voxbridge/media"
	"github.com/yangwenmai/voxbridge/provider/internal/wire"
)

const (
	asrSubmitURL   = "https://" + host + "/api/v3/auc/bigmodel/submit"
	asrQueryURL    = "https://" + host + "/api/v3/auc/bigmodel/query"
	asrRes         = "volc.seedasr.auc"
	asrServiceName = "录音文件识别 2.0"
	asrPerSecond   = 0.8 / 3600
	asrSampleRate  = 16000
	asrBitrate     = "24k"
	asrUserID      = "voxbridge"

	asrDone       = 20000000
	asrProcessing = 20000001
	asrQueued     = 20000002
	asrSilent     = 20000003
	asrNotFound   = 45000092
)

// ASRChunk is the most audio sent in one task. The service takes up to 5
// hours by URL; audio inline as base64 is undocumented, so chunks stay
// near 10 MB. Speaker labels are per task and do not carry across chunks.
var ASRChunk = time.Hour

// PollInterval is the wait between task queries.
var PollInterval = 5 * time.Second

type asrState struct {
	Resource string     `json:"resource"`
	Chunks   []asrChunk `json:"chunks"`
}

type asrChunk struct {
	OffsetMs   int64  `json:"offsetMs"`
	DurationMs int64  `json:"durationMs"`
	TaskID     string `json:"taskId"`
	// Submitted is set once the service accepted the task, which is when
	// it bills; a saved but unsubmitted task is queried before resending.
	Submitted bool        `json:"submitted"`
	Done      bool        `json:"done"`
	Lines     []core.Line `json:"lines,omitempty"`
}

type asrResult struct {
	Result struct {
		Utterances []struct {
			StartTime int64  `json:"start_time"`
			EndTime   int64  `json:"end_time"`
			Text      string `json:"text"`
			Additions struct {
				Speaker string `json:"speaker"`
			} `json:"additions"`
		} `json:"utterances"`
	} `json:"result"`
}

// Transcribe submits the audio as recognition tasks and polls them. Task
// ids are saved in tasks before each submit, so a resumed run never pays
// for the same audio twice.
func (c *Client) Transcribe(ctx context.Context, in core.AudioInput, tasks core.RecognizeTasks) ([]core.Line, error) {
	st, resumed, err := c.loadState(ctx, in, tasks)
	if err != nil {
		return nil, err
	}
	for i := range st.Chunks {
		ch := &st.Chunks[i]
		if ch.Submitted {
			continue
		}
		if resumed {
			// The last run may have been cut off after the service took it.
			status, _, err := c.query(ctx, ch.TaskID)
			if err != nil {
				return nil, err
			}
			if status != asrNotFound {
				ch.Submitted = true
				if err := tasks.Save(mustJSON(st)); err != nil {
					return nil, err
				}
				continue
			}
		}
		tasks.Progress("上传第 %d/%d 段音频（%s）提交识别…", i+1, len(st.Chunks), (time.Duration(ch.DurationMs) * time.Millisecond).Round(time.Second))
		if err := c.submit(ctx, in, ch); err != nil {
			return nil, err
		}
		ch.Submitted = true
		c.bill(core.CapRecognize, float64(ch.DurationMs)/1000, float64(ch.DurationMs)/1000*asrPerSecond)
		if err := tasks.Save(mustJSON(st)); err != nil {
			return nil, err
		}
	}
	if resumed {
		tasks.Progress("接着等上次提交的识别任务，不重新提交")
	}

	start := time.Now()
	lastNote := start
	for {
		pending := 0
		for i := range st.Chunks {
			ch := &st.Chunks[i]
			if ch.Done {
				continue
			}
			status, body, err := c.query(ctx, ch.TaskID)
			if err != nil {
				return nil, err
			}
			switch status {
			case asrDone, asrSilent:
				if ch.Lines, err = parseUtterances(body, ch.OffsetMs); err != nil {
					return nil, err
				}
				ch.Done = true
				if err := tasks.Save(mustJSON(st)); err != nil {
					return nil, err
				}
			case asrProcessing, asrQueued:
				pending++
			case asrNotFound:
				return nil, &core.ProviderError{Provider: ID, Service: asrServiceName, Kind: core.ErrBadRequest,
					Code: strconv.Itoa(asrNotFound), Message: "服务端找不到已提交的识别任务 " + ch.TaskID,
					Hint: "多半已过期。删掉工作目录里的 asr-tasks.json 再跑会重新提交，并再次计费"}
			}
		}
		if pending == 0 {
			break
		}
		if time.Since(lastNote) >= 30*time.Second {
			lastNote = time.Now()
			tasks.Progress("识别中，已等 %s（%d/%d 段完成）", time.Since(start).Round(time.Second), len(st.Chunks)-pending, len(st.Chunks))
		}
		if err := c.wait(ctx, PollInterval); err != nil {
			return nil, err
		}
	}
	tasks.Progress("识别完成，等了 %s", time.Since(start).Round(time.Second))
	var lines []core.Line
	for _, ch := range st.Chunks {
		lines = append(lines, ch.Lines...)
	}
	return lines, nil
}

func (c *Client) loadState(ctx context.Context, in core.AudioInput, tasks core.RecognizeTasks) (*asrState, bool, error) {
	data, err := tasks.Load()
	if err != nil {
		return nil, false, err
	}
	if data != nil {
		var st asrState
		if err := json.Unmarshal(data, &st); err != nil || st.Resource != asrRes || len(st.Chunks) == 0 {
			return nil, false, fmt.Errorf("识别任务记录看不懂或不是 %s 的（%v）；删掉它会重新提交并再次计费", asrRes, err)
		}
		return &st, true, nil
	}
	probe, err := c.probe(ctx, in.URL, in.Headers)
	if err != nil {
		return nil, false, err
	}
	st := &asrState{Resource: asrRes}
	chunk := ASRChunk.Milliseconds()
	for off := int64(0); off < probe.DurationMs; off += chunk {
		st.Chunks = append(st.Chunks, asrChunk{OffsetMs: off, DurationMs: min(chunk, probe.DurationMs-off), TaskID: uuid()})
	}
	if len(st.Chunks) == 0 {
		return nil, false, fmt.Errorf("音频时长为 0")
	}
	return st, false, tasks.Save(mustJSON(st))
}

func (c *Client) submit(ctx context.Context, in core.AudioInput, ch *asrChunk) error {
	audio, err := c.encode(ctx, in, ch.OffsetMs, ch.DurationMs)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, call{
		cap: core.CapRecognize, service: asrServiceName, url: asrSubmitURL, resource: asrRes,
		header: taskHeader(ch.TaskID),
		body: map[string]any{
			"user":  map[string]any{"uid": asrUserID},
			"audio": map[string]any{"data": base64.StdEncoding.EncodeToString(audio), "format": "mp3"},
			"request": map[string]any{
				"model_name": "bigmodel", "enable_itn": true, "enable_punc": true,
				"enable_speaker_info": true, "show_utterances": true,
			},
		},
		check: checkASR(),
	})
	return err
}

// encodeChunk cuts one chunk as 16 kHz mono mp3.
func encodeChunk(ctx context.Context, in core.AudioInput, offsetMs, durationMs int64) ([]byte, error) {
	dir, err := os.MkdirTemp("", "voxbridge-asr-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "chunk.mp3")
	args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}, media.HeaderArgs(in.Headers)...)
	args = append(args, "-ss", ms(offsetMs), "-t", ms(durationMs), "-i", in.URL,
		"-vn", "-ac", "1", "-ar", strconv.Itoa(asrSampleRate), "-b:a", asrBitrate, path)
	if _, err := media.Run(ctx, "ffmpeg", args...); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// query returns the task status from the X-Api-Status-Code header.
func (c *Client) query(ctx context.Context, taskID string) (int, []byte, error) {
	reply, err := c.do(ctx, call{
		cap: core.CapRecognize, service: asrServiceName, url: asrQueryURL, resource: asrRes,
		header: taskHeader(taskID), body: map[string]any{},
		check: checkASR(asrProcessing, asrQueued, asrSilent, asrNotFound),
	})
	if err != nil {
		return 0, nil, err
	}
	status, _ := strconv.Atoi(reply.Header.Get("X-Api-Status-Code"))
	return status, reply.Body, nil
}

func taskHeader(id string) http.Header {
	h := http.Header{}
	h.Set("X-Api-Request-Id", id)
	h.Set("X-Api-Sequence", "-1")
	return h
}

// checkASR reads the status the recognizer puts in response headers;
// statuses in also are answers, not failures.
func checkASR(also ...int) func(wire.Reply) *core.ProviderError {
	return func(r wire.Reply) *core.ProviderError {
		status, _ := strconv.Atoi(r.Header.Get("X-Api-Status-Code"))
		if r.Status < 400 && status == asrDone {
			return nil
		}
		for _, s := range also {
			if r.Status < 400 && status == s {
				return nil
			}
		}
		code, msg := parseReply(r.Body)
		if status != 0 {
			code = status
		}
		if m := r.Header.Get("X-Api-Message"); m != "" {
			msg = m
		}
		if e := Classify(asrServiceName, r.Status, code, msg, r.Header.Get("X-Tt-Logid")); e != nil {
			return e
		}
		return &core.ProviderError{Provider: ID, Service: asrServiceName, Kind: core.ErrServer, HTTP: r.Status,
			Message: "没有返回识别状态", LogID: r.Header.Get("X-Tt-Logid")}
	}
}

func parseUtterances(body []byte, offsetMs int64) ([]core.Line, error) {
	var r asrResult
	if len(body) > 0 {
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, &core.ProviderError{Provider: ID, Service: asrServiceName, Kind: core.ErrServer, Message: "识别结果看不懂"}
		}
	}
	lines := make([]core.Line, 0, len(r.Result.Utterances))
	for _, u := range r.Result.Utterances {
		end := u.EndTime + offsetMs
		l := core.Line{StartMs: u.StartTime + offsetMs, EndMs: &end, Text: u.Text}
		if s := u.Additions.Speaker; s != "" {
			l.Speaker = &s
		}
		lines = append(lines, l)
	}
	return lines, nil
}

func ms(v int64) string { return strconv.FormatFloat(float64(v)/1000, 'f', 3, 64) }

func mustJSON(v any) []byte {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	return b
}
