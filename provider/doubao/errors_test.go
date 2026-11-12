package doubao

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
		// Recorded 2026-11-12 with a wrong key.
		{"translate wrong key", 500, `{"code":55000000,"message":"Invalid X-Api-Key"}`, core.ErrBadKey},
		{"tts wrong key", 401, `{"header":{"reqid":"E4B43824-8A3F-4885-88F7-EE02D8E1D997","code":45000010,"message":"Invalid X-Api-Key"}}`, core.ErrBadKey},
		{"interpretation socket wrong key", 401, `{"error":"Invalid X-Api-Key"}`, core.ErrBadKey},
		// Seen in CastMind: TTS 2.0 not enabled looks like a server fault.
		{"tts not enabled", 500, `{"header":{"code":55000000,"message":"resource ID is mismatched with speaker related resource"}}`, core.ErrNotEnabled},
		{"forbidden", 403, ``, core.ErrNotEnabled},
		{"concurrency", 200, `{"code":45000292,"message":"quota exceeded for types: concurrency"}`, core.ErrRateLimited},
		{"busy", 200, `{"code":55000031,"message":"server busy"}`, core.ErrRateLimited},
		{"gateway", 502, `<html>bad gateway</html>`, core.ErrServer},
		{"bad language pair", 200, `{"code":45000001,"message":"invalid params"}`, core.ErrBadRequest},
	}
	for _, c := range cases {
		code, msg := parseReply([]byte(c.body))
		e := Classify("test", c.status, code, msg, "logid-1")
		if e == nil {
			t.Errorf("%s: no error", c.name)
			continue
		}
		if e.Kind != c.want {
			t.Errorf("%s: kind %s, want %s", c.name, e.Kind, c.want)
		}
		if e.Kind.Retryable() != (c.want == core.ErrRateLimited || c.want == core.ErrServer) {
			t.Errorf("%s: retryable = %v", c.name, e.Kind.Retryable())
		}
		if e.LogID != "logid-1" {
			t.Errorf("%s: logid dropped", c.name)
		}
	}
}

func TestSuccessIsNotAnError(t *testing.T) {
	code, msg := parseReply([]byte(`{"code":20000000,"message":"ok","data":{"translation_list":[]}}`))
	if e := Classify("test", 200, code, msg, ""); e != nil {
		t.Fatal(e)
	}
}

func TestDecodeTTSJoinsChunks(t *testing.T) {
	// Shape documented for /api/v3/tts/unidirectional: audio slices, then a
	// final object with code 20000000.
	body := `{"code":0,"message":"","data":"SUQz"}` + "\n" + `{"code":0,"message":"","data":"AAEC"}` + "\n" + `{"code":20000000,"message":"OK","data":null}`
	audio, err := DecodeTTS([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if string(audio[:3]) != "ID3" || len(audio) != 6 {
		t.Fatalf("audio = %v", audio)
	}
}

func TestDecodeTTSMidStreamError(t *testing.T) {
	body := `{"code":0,"data":"SUQz"}{"code":45000292,"message":"quota exceeded for types: concurrency"}`
	_, err := DecodeTTS([]byte(body))
	pe, ok := err.(*core.ProviderError)
	if !ok || pe.Kind != core.ErrRateLimited {
		t.Fatalf("err = %v", err)
	}
}
