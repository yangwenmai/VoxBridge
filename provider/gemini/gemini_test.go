package gemini

import (
	"testing"

	"github.com/yangwenmai/voxbridge/core"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   core.ErrorKind
	}{
		// Recorded 2026-11-12 from GET models/gemini-3.5-live-translate-preview.
		{"wrong key", 400, `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID","domain":"googleapis.com"}]}}`, core.ErrBadKey},
		{"model gone", 404, `{"error":{"code":404,"message":"models/x is not found","status":"NOT_FOUND"}}`, core.ErrNotFound},
		{"region", 400, `{"error":{"code":400,"message":"User location is not supported for the API use.","status":"FAILED_PRECONDITION"}}`, core.ErrNotEnabled},
		{"quota", 429, `{"error":{"code":429,"message":"Resource has been exhausted","status":"RESOURCE_EXHAUSTED"}}`, core.ErrRateLimited},
	}
	for _, c := range cases {
		e := Classify(c.status, []byte(c.body))
		if e == nil || e.Kind != c.want {
			t.Errorf("%s: got %+v, want %s", c.name, e, c.want)
		}
	}
	if Classify(200, []byte(`{"name":"models/`+Model+`"}`)) != nil {
		t.Error("200 classified as error")
	}
}
