package openai

import (
	"strings"
	"testing"

	"github.com/yangwenmai/voxbridge/core"
)

func TestWrongKeyIsBadKeyAndNotEchoed(t *testing.T) {
	key := "sk-bogus-openai-key-123"
	// Recorded 2026-11-12 from GET /v1/models/gpt-realtime-translate.
	body := `{"error":{"message":"Incorrect API key provided: sk-bogus-openai-key-123. You can find your API key at https://platform.openai.com/account/api-keys.","type":"invalid_request_error","param":null,"code":"invalid_api_key"}}`
	e := Classify(401, []byte(body), key)
	if e.Kind != core.ErrBadKey {
		t.Fatalf("kind = %s", e.Kind)
	}
	if strings.Contains(e.Error(), "bogus") {
		t.Fatalf("key leaked: %s", e.Error())
	}
}

func TestMaskedKeyEchoIsHidden(t *testing.T) {
	body := `{"error":{"message":"Incorrect API key provided: sk-proj-****************wxyz. You can find your API key at https://platform.openai.com/account/api-keys.","code":"invalid_api_key"}}`
	e := Classify(401, []byte(body), "sk-proj-real-key-wxyz")
	if strings.Contains(e.Message, "wxyz") {
		t.Fatalf("key tail leaked: %s", e.Message)
	}
}

func TestQuota(t *testing.T) {
	body := `{"error":{"message":"You exceeded your current quota","type":"insufficient_quota","code":"insufficient_quota"}}`
	if e := Classify(429, []byte(body), "k"); e.Kind != core.ErrRateLimited || e.Hint == "" {
		t.Fatalf("got %+v", e)
	}
}
