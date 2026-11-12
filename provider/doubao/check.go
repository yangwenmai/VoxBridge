package doubao

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/coder/websocket"
	"github.com/yangwenmai/voxbridge/core"
	"github.com/yangwenmai/voxbridge/provider/internal/wire"
)

type Provider struct{}

func (Provider) ID() string                      { return ID }
func (Provider) Name() string                    { return "豆包语音（火山引擎）" }
func (Provider) KeyEnv() string                  { return KeyEnv }
func (Provider) Capabilities() core.Capabilities { return capabilities }

// Check translates "Hello.", synthesizes "你好。" and opens the
// interpretation socket without starting a session.
func (Provider) Check(ctx context.Context, key string) []core.CheckResult {
	return []core.CheckResult{checkAST(ctx, key), checkMT(ctx, key), checkTTS(ctx, key)}
}

func headers(key, resource string) http.Header {
	h := http.Header{}
	h.Set("X-Api-Key", key)
	h.Set("X-Api-Resource-Id", resource)
	h.Set("X-Api-Request-Id", uuid())
	h.Set("Content-Type", "application/json")
	return h
}

func checkMT(ctx context.Context, key string) core.CheckResult {
	r := core.CheckResult{Capability: core.CapTranslate, Model: mtRes, Cost: "10 token"}
	body, _ := json.Marshal(map[string]any{"target_language": "zh", "text_list": []string{"Hello."}})
	reply, err := wire.Do(ctx, http.MethodPost, mtURL, headers(key, mtRes), body)
	if err != nil {
		r.Err = scrub(err, key)
		return r
	}
	code, msg := parseReply(reply.Body)
	if e := Classify("机器翻译", reply.Status, code, core.Scrub(msg, key), reply.Header.Get("X-Tt-Logid")); e != nil {
		r.Err = e
		return r
	}
	var ok struct {
		Data struct {
			TranslationList []struct {
				Translation string `json:"translation"`
			} `json:"translation_list"`
		} `json:"data"`
	}
	if json.Unmarshal(reply.Body, &ok) != nil || len(ok.Data.TranslationList) != 1 {
		r.Err = fmt.Errorf("机器翻译返回的内容看不懂")
		return r
	}
	r.OK = true
	r.Scope = fmt.Sprintf("Hello. → %s", ok.Data.TranslationList[0].Translation)
	return r
}

func checkTTS(ctx context.Context, key string) core.CheckResult {
	voice := TTSVoices[0].ID
	r := core.CheckResult{Capability: core.CapSynthesize, Model: ttsRes, Cost: "3 字符"}
	body, _ := json.Marshal(map[string]any{"req_params": map[string]any{
		"text": "你好。", "speaker": voice,
		"audio_params": map[string]any{"format": "mp3", "sample_rate": 24000},
	}})
	reply, err := wire.Do(ctx, http.MethodPost, ttsURL, headers(key, ttsRes), body)
	if err != nil {
		r.Err = scrub(err, key)
		return r
	}
	logID := reply.Header.Get("X-Tt-Logid")
	if reply.Status >= 400 {
		code, msg := parseReply(reply.Body)
		r.Err = Classify("语音合成 2.0", reply.Status, code, core.Scrub(msg, key), logID)
		return r
	}
	audio, err := DecodeTTS(reply.Body)
	if pe, ok := err.(*core.ProviderError); ok {
		pe.LogID = logID
		pe.Message = core.Scrub(pe.Message, key)
	}
	if err != nil {
		r.Err = err
		return r
	}
	r.OK = true
	r.Scope = fmt.Sprintf("用 %s 合成「你好。」得到 %d 字节 mp3", voice, len(audio))
	return r
}

// DecodeTTS joins the base64 audio slices of a TTS 2.0 reply, which is a run
// of JSON objects back to back.
func DecodeTTS(body []byte) ([]byte, error) {
	dec := json.NewDecoder(strings.NewReader(string(body)))
	var audio []byte
	for {
		var chunk struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    string `json:"data"`
		}
		err := dec.Decode(&chunk)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("语音合成返回的内容看不懂: %w", err)
		}
		if e := Classify("语音合成 2.0", 200, chunk.Code, chunk.Message, ""); e != nil {
			return nil, e
		}
		if chunk.Data != "" {
			b, err := base64.StdEncoding.DecodeString(chunk.Data)
			if err != nil {
				return nil, fmt.Errorf("语音合成返回的音频看不懂: %w", err)
			}
			audio = append(audio, b...)
		}
	}
	if len(audio) == 0 {
		return nil, fmt.Errorf("语音合成没有返回音频")
	}
	return audio, nil
}

// checkAST only proves the socket accepts the key; the language pair and
// voice are checked when a session starts.
func checkAST(ctx context.Context, key string) core.CheckResult {
	r := core.CheckResult{Capability: core.CapInterpret, Model: astRes}
	h := headers(key, astRes)
	h.Del("Content-Type")
	h.Del("X-Api-Request-Id")
	h.Set("X-Api-Connect-Id", uuid())
	conn, reply, err := wire.Dial(ctx, astURL, h)
	if conn != nil {
		conn.Close(websocket.StatusNormalClosure, "")
		r.OK = true
		r.Scope = "建立了 WebSocket 连接，未发送音频，未验证语言对和音色"
		return r
	}
	if reply.Status == 0 {
		r.Err = scrub(err, key)
		return r
	}
	code, msg := parseReply(reply.Body)
	r.Err = Classify("同声传译 2.0", reply.Status, code, core.Scrub(msg, key), reply.Header.Get("X-Tt-Logid"))
	return r
}

func scrub(err error, key string) error {
	return fmt.Errorf("%s", core.Scrub(err.Error(), key))
}

func uuid() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
