// Package openai adapts the OpenAI realtime translation endpoint.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/coder/websocket"
	"github.com/yangwenmai/voxbridge/core"
	"github.com/yangwenmai/voxbridge/provider/internal/wire"
)

const (
	ID      = "openai"
	KeyEnv  = "OPENAI_API_KEY"
	Model   = "gpt-realtime-translate"
	apiBase = "https://api.openai.com/v1"
	liveURL = "wss://api.openai.com/v1/realtime/translations?model=" + Model
	keyHint = "到 https://platform.openai.com/api-keys 创建或复制 API Key"
)

// targets are the 13 output languages; input language is detected.
var targets = []core.Lang{"es", "pt", "fr", "ja", "ru", "zh", "de", "ko", "hi", "id", "vi", "it", "en"}

var capabilities = core.Capabilities{
	Provider: ID,
	Services: []core.Service{{
		Capability:       core.CapInterpret,
		Model:            Model,
		Languages:        []core.LangRule{{From: []core.Lang{core.AnyLang}, To: targets}},
		Subtitles:        true,
		InputSampleRate:  24000,
		OutputSampleRate: 24000,
		Pricing: core.Pricing{
			Unit: "minute", Currency: "USD", PerHour: 2.04,
			Basis: "按输入音频时长 0.034 美元/分钟",
		},
	}},
}

type Provider struct{}

func (Provider) ID() string                      { return ID }
func (Provider) Name() string                    { return "OpenAI" }
func (Provider) KeyEnv() string                  { return KeyEnv }
func (Provider) Capabilities() core.Capabilities { return capabilities }

// Check reads the model, then opens a translation socket and waits for
// session.created. No audio is sent, and billing is by audio duration.
func (Provider) Check(ctx context.Context, key string) []core.CheckResult {
	r := core.CheckResult{Capability: core.CapInterpret, Model: Model}
	h := http.Header{}
	h.Set("Authorization", "Bearer "+key)
	reply, err := wire.Do(ctx, http.MethodGet, apiBase+"/models/"+Model, h, nil)
	if err != nil {
		r.Err = errors.New(core.Scrub(err.Error(), key))
		return []core.CheckResult{r}
	}
	if e := Classify(reply.Status, reply.Body, key); e != nil {
		r.Err = e
		return []core.CheckResult{r}
	}
	if err := openSession(ctx, h, key); err != nil {
		r.Err = err
		r.Scope = "模型可见，但翻译会话没建起来"
		return []core.CheckResult{r}
	}
	r.OK = true
	r.Scope = "模型可见，翻译会话已建立（session.created），未发送音频"
	return []core.CheckResult{r}
}

func openSession(ctx context.Context, h http.Header, key string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, reply, err := wire.Dial(ctx, liveURL, h)
	if conn == nil {
		if reply.Status != 0 {
			if e := Classify(reply.Status, reply.Body, key); e != nil {
				return e
			}
		}
		return errors.New(core.Scrub(err.Error(), key))
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	_, msg, err := conn.Read(ctx)
	if err != nil {
		return errors.New(core.Scrub(err.Error(), key))
	}
	var ev struct {
		Type  string `json:"type"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(msg, &ev) != nil {
		return fmt.Errorf("翻译会话返回的内容看不懂")
	}
	if ev.Type == "session.created" {
		return nil
	}
	if ev.Error != nil {
		return &core.ProviderError{Provider: ID, Service: "实时翻译", Kind: core.ErrBadRequest, Code: ev.Error.Code, Message: core.Scrub(ev.Error.Message, key)}
	}
	return fmt.Errorf("翻译会话先返回了 %s，而不是 session.created", ev.Type)
}

// Classify turns an OpenAI error reply into a ProviderError, or nil. The
// API echoes part of a wrong key in its message, so key is scrubbed out.
func Classify(status int, body []byte, key string) *core.ProviderError {
	if status < 400 {
		return nil
	}
	var r struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &r)
	e := &core.ProviderError{Provider: ID, Service: "实时翻译", HTTP: status, Code: r.Error.Code, Message: redactKeyEcho(core.Scrub(r.Error.Message, key))}
	switch {
	case status == 401 || r.Error.Code == "invalid_api_key":
		e.Kind, e.Hint = core.ErrBadKey, keyHint
	case status == 403:
		e.Kind, e.Hint = core.ErrNotEnabled, "这个项目或组织没有该模型的权限，检查 platform.openai.com 的项目权限和组织验证"
	case status == 404:
		e.Kind, e.Hint = core.ErrNotFound, "这个 Key 看不到 "+Model
	case status == 429:
		e.Kind = core.ErrRateLimited
		if r.Error.Code == "insufficient_quota" {
			e.Hint = "账户余额或额度不足"
		}
	case status >= 500:
		e.Kind = core.ErrServer
	default:
		e.Kind = core.ErrBadRequest
	}
	return e
}

var keyEcho = regexp.MustCompile(`(API key provided: )\S+?(\.\s|\.$|\s|$)`)

// redactKeyEcho hides the partly masked key OpenAI quotes back.
func redactKeyEcho(s string) string {
	return keyEcho.ReplaceAllString(s, "${1}[REDACTED]${2}")
}
