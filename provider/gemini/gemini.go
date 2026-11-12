// Package gemini adapts the Gemini Live API live translation model.
package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/yangwenmai/voxbridge/core"
	"github.com/yangwenmai/voxbridge/provider/internal/wire"
)

const (
	ID      = "gemini"
	KeyEnv  = "GEMINI_API_KEY"
	Model   = "gemini-3.5-live-translate-preview"
	apiBase = "https://generativelanguage.googleapis.com/v1beta"
	liveURL = "wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent"
	keyHint = "到 https://aistudio.google.com/apikey 创建或复制 Gemini API Key"
)

// targets are the BCP-47 codes in the live translation language table.
var targets = []core.Lang{
	"af", "ak", "sq", "am", "ar", "hy", "az", "eu", "be", "bn", "bg", "my", "ca",
	"zh-Hans", "zh-Hant", "hr", "cs", "da", "nl", "en", "et", "fil", "fi", "fr",
	"gl", "ka", "de", "el", "gu", "ha", "he", "hi", "hu", "is", "id", "it", "ja",
	"jv", "kn", "kk", "km", "rw", "ko", "lo", "lv", "lt", "mk", "ms", "ml", "mr",
	"mn", "ne", "no", "nb", "fa", "pl", "pt-BR", "pt-PT", "pa", "ro", "ru", "sr",
	"sd", "si", "sk", "sl", "es", "su", "sw", "sv", "ta", "te", "th", "tr", "uk",
	"ur", "uz", "vi", "zu",
}

var capabilities = core.Capabilities{
	Provider: ID,
	Services: []core.Service{{
		Capability:       core.CapInterpret,
		Model:            Model,
		Languages:        []core.LangRule{{From: []core.Lang{core.AnyLang}, To: targets}},
		Subtitles:        true,
		InputSampleRate:  16000,
		OutputSampleRate: 24000,
		Pricing: core.Pricing{
			Unit: "audio token", Currency: "USD", PerHour: 2.21,
			Basis: "输入 3.50 美元/百万 token，输出 21.00 美元/百万 token，按官方 25 token/秒折合约 0.0368 美元/分钟",
		},
	}},
}

type Provider struct{}

func (Provider) ID() string                      { return ID }
func (Provider) Name() string                    { return "Gemini" }
func (Provider) KeyEnv() string                  { return KeyEnv }
func (Provider) Capabilities() core.Capabilities { return capabilities }

// Check reads the model and opens a Live session with a translation setup,
// then closes it before sending audio, which is all that is billed.
func (Provider) Check(ctx context.Context, key string) []core.CheckResult {
	r := core.CheckResult{Capability: core.CapInterpret, Model: Model}
	h := http.Header{}
	h.Set("x-goog-api-key", key)
	reply, err := wire.Do(ctx, http.MethodGet, apiBase+"/models/"+Model, h, nil)
	if err != nil {
		r.Err = errors.New(core.Scrub(err.Error(), key))
		return []core.CheckResult{r}
	}
	if e := Classify(reply.Status, reply.Body); e != nil {
		e.Message = core.Scrub(e.Message, key)
		r.Err = e
		return []core.CheckResult{r}
	}
	if err := openLive(ctx, h); err != nil {
		r.Err = errors.New(core.Scrub(err.Error(), key))
		r.Scope = "模型可见，但 Live 会话没建起来"
		return []core.CheckResult{r}
	}
	r.OK = true
	r.Scope = "模型可见，Live 翻译会话 setup 成功，未发送音频"
	return []core.CheckResult{r}
}

func openLive(ctx context.Context, h http.Header) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, reply, err := wire.Dial(ctx, liveURL, h)
	if conn == nil {
		if reply.Status != 0 {
			if e := Classify(reply.Status, reply.Body); e != nil {
				return e
			}
		}
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	setup, _ := json.Marshal(map[string]any{"setup": map[string]any{
		"model": "models/" + Model,
		"generationConfig": map[string]any{
			"responseModalities":       []string{"AUDIO"},
			"inputAudioTranscription":  map[string]any{},
			"outputAudioTranscription": map[string]any{},
			"translationConfig":        map[string]any{"targetLanguageCode": "zh-Hans"},
		},
	}})
	if err := conn.Write(ctx, websocket.MessageText, setup); err != nil {
		return err
	}
	_, msg, err := conn.Read(ctx)
	if err != nil {
		if reason := websocket.CloseStatus(err); reason != -1 {
			return &core.ProviderError{Provider: ID, Service: "Live", Kind: classifyText(err.Error()), Message: err.Error()}
		}
		return err
	}
	var resp struct {
		SetupComplete *struct{} `json:"setupComplete"`
	}
	if json.Unmarshal(msg, &resp) != nil || resp.SetupComplete == nil {
		return fmt.Errorf("Live 会话返回了意外内容: %.200s", msg)
	}
	return nil
}

// Classify turns a Google API error reply into a ProviderError, or nil.
func Classify(status int, body []byte) *core.ProviderError {
	if status < 400 {
		return nil
	}
	var r struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
			Details []struct {
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &r)
	e := &core.ProviderError{Provider: ID, Service: "Live 翻译", HTTP: status, Code: r.Error.Status, Message: r.Error.Message}
	reason := ""
	for _, d := range r.Error.Details {
		if d.Reason != "" {
			reason = d.Reason
		}
	}
	switch {
	case reason == "API_KEY_INVALID" || status == 401:
		e.Kind, e.Hint = core.ErrBadKey, keyHint
	case status == 404:
		e.Kind, e.Hint = core.ErrNotFound, "这个 Key 看不到 "+Model+"，模型可能改名或下线了"
	case status == 403 || r.Error.Status == "FAILED_PRECONDITION":
		e.Kind, e.Hint = core.ErrNotEnabled, "确认 Generative Language API 已启用、账号所在地区可用"
	case status == 429:
		e.Kind = core.ErrRateLimited
	case status >= 500:
		e.Kind = core.ErrServer
	default:
		e.Kind = core.ErrBadRequest
	}
	return e
}

func classifyText(s string) core.ErrorKind {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "api key"):
		return core.ErrBadKey
	case strings.Contains(s, "not found"), strings.Contains(s, "not supported"):
		return core.ErrNotFound
	case strings.Contains(s, "quota"), strings.Contains(s, "exhausted"):
		return core.ErrRateLimited
	}
	return core.ErrBadRequest
}
