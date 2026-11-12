package doubao

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/yangwenmai/voxbridge/core"
)

const consoleHint = "到火山引擎控制台「豆包语音 → 开通管理」确认该服务已开通，且 API Key 和它在同一个项目里；刚开通的等几分钟再试"

// reply is the error body shape. Translation puts code and message at the
// top level, TTS nests them in "header", the interpretation socket uses "error".
type reply struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Error   string `json:"error"`
	Header  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"header"`
}

func parseReply(body []byte) (code int, message string) {
	var r reply
	if json.NewDecoder(strings.NewReader(string(body))).Decode(&r) != nil {
		text := strings.TrimSpace(string(body))
		if len(text) > 200 {
			text = text[:200]
		}
		return 0, text
	}
	if r.Header != nil {
		return r.Header.Code, r.Header.Message
	}
	if r.Message == "" {
		r.Message = r.Error
	}
	return r.Code, r.Message
}

// Classify turns one Doubao reply into a ProviderError, or nil on success.
// status is the HTTP status; code and message come from the body.
func Classify(service string, status, code int, message, logID string) *core.ProviderError {
	if status < 400 && (code == 0 || code == success) {
		return nil
	}
	e := &core.ProviderError{Provider: ID, Service: service, HTTP: status, Message: message, LogID: logID}
	if code != 0 {
		e.Code = strconv.Itoa(code)
	}
	lower := strings.ToLower(message)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(lower, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("invalid x-api-key", "api key is invalid"):
		e.Kind = core.ErrBadKey
		e.Hint = "API Key 不对。到火山引擎控制台「豆包语音 → API Key 管理」复制新版控制台的 API Key"
	case status == 401 || status == 403 || has("mismatched", "access denied", "permission denied", "not granted"):
		e.Kind = core.ErrNotEnabled
		e.Hint = consoleHint
	case status == 429 || code == 55000031 || has("quota exceeded"):
		e.Kind = core.ErrRateLimited
	case status >= 500:
		e.Kind = core.ErrServer
	case code == 45000001:
		e.Kind = core.ErrBadRequest
		e.Hint = "参数不对，多半是账号不支持该语言对或音色"
	default:
		e.Kind = core.ErrBadRequest
	}
	return e
}
