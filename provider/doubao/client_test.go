package doubao

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yangwenmai/voxbridge/core"
	"github.com/yangwenmai/voxbridge/provider/internal/wire"
)

const testKey = "sk-test-0123456789"

type fakeServer struct {
	replies []wire.Reply
	sent    []http.Header
	bodies  []string
	waits   []time.Duration
}

func (f *fakeServer) client() *Client {
	c := NewClient(testKey)
	c.send = func(_ context.Context, _, _ string, h http.Header, body []byte) (wire.Reply, error) {
		f.sent = append(f.sent, h)
		f.bodies = append(f.bodies, string(body))
		r := f.replies[0]
		if len(f.replies) > 1 {
			f.replies = f.replies[1:]
		}
		if r.Header == nil {
			r.Header = http.Header{}
		}
		return r, nil
	}
	c.wait = func(_ context.Context, d time.Duration) error {
		f.waits = append(f.waits, d)
		return nil
	}
	return c
}

func recorded(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBusyIsRetriedThenSucceeds(t *testing.T) {
	f := &fakeServer{replies: []wire.Reply{
		{Status: 200, Body: []byte(`{"code":55000031,"message":"server busy"}`)},
		{Status: 503, Body: []byte(`upstream`)},
		{Status: 200, Body: recorded(t, "mt_ok.json")},
	}}
	c := f.client()
	out, err := c.Translate(context.Background(), []string{"Thank you.", "I gave out homework assignments."}, "zh")
	if err != nil {
		t.Fatal(err)
	}
	if out[0] != "谢谢。" || out[1] != "我布置了家庭作业。" {
		t.Fatalf("out = %q", out)
	}
	if len(f.waits) != 2 || f.waits[0] != time.Second || f.waits[1] != 3*time.Second {
		t.Fatalf("waits = %v, want 1s then 3s", f.waits)
	}
	u := c.Usage()
	if u.Requests[core.CapTranslate] != 3 {
		t.Fatalf("requests = %d, want 3 (retries count)", u.Requests[core.CapTranslate])
	}
	if u.Units[core.CapTranslate] != 65 {
		t.Fatalf("tokens = %v, want the recorded 29+36", u.Units[core.CapTranslate])
	}
}

func TestGivesUpAfterThreeRetries(t *testing.T) {
	f := &fakeServer{replies: []wire.Reply{{Status: 500, Body: []byte(`{"code":55000001,"message":"internal"}`), Header: http.Header{"X-Tt-Logid": {"log-9"}}}}}
	_, err := f.client().Translate(context.Background(), []string{"a"}, "zh")
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Kind != core.ErrServer || pe.LogID != "log-9" {
		t.Fatalf("err = %v", err)
	}
	if len(f.sent) != 4 {
		t.Fatalf("sent %d requests, want 1 + 3 retries", len(f.sent))
	}
}

func TestKeyNeverInErrors(t *testing.T) {
	f := &fakeServer{replies: []wire.Reply{{Status: 401, Body: []byte(`{"code":45000010,"message":"Invalid X-Api-Key ` + testKey + `"}`)}}}
	_, err := f.client().Synthesize(context.Background(), "你好", "v")
	if err == nil || strings.Contains(err.Error(), testKey) {
		t.Fatalf("err = %v", err)
	}
}

func TestTranslateSendsSixteenPerRequestWithRequestID(t *testing.T) {
	reply := func(n int) wire.Reply {
		list := make([]map[string]string, n)
		for i := range list {
			list[i] = map[string]string{"translation": "译"}
		}
		b, _ := json.Marshal(map[string]any{"code": 20000000, "data": map[string]any{"translation_list": list}})
		return wire.Reply{Status: 200, Body: b}
	}
	f := &fakeServer{replies: []wire.Reply{reply(16), reply(4)}}
	texts := make([]string, 20)
	for i := range texts {
		texts[i] = "x"
	}
	out, err := f.client().Translate(context.Background(), texts, "zh")
	if err != nil || len(out) != 20 {
		t.Fatalf("out %d, err %v", len(out), err)
	}
	if len(f.sent) != 2 || !strings.Contains(f.bodies[0], strings.Repeat(`"x",`, 15)+`"x"]`) {
		t.Fatalf("requests = %d, first body %s", len(f.sent), f.bodies[0])
	}
	if f.sent[0].Get("X-Api-Request-Id") == "" || f.sent[0].Get("X-Api-Request-Id") == f.sent[1].Get("X-Api-Request-Id") {
		t.Fatal("each request needs its own X-Api-Request-Id")
	}
}

func TestSynthesizeRecordedReply(t *testing.T) {
	f := &fakeServer{replies: []wire.Reply{{Status: 200, Body: recorded(t, "tts_ok.raw")}}}
	c := f.client()
	a, err := c.Synthesize(context.Background(), "谢谢。", "zh_female_vv_uranus_bigtts")
	if err != nil {
		t.Fatal(err)
	}
	if a.Format != "mp3" || string(a.Data[:3]) != "ID3" {
		t.Fatalf("audio %s %q", a.Format, a.Data[:3])
	}
	if !strings.Contains(f.bodies[0], `"speech_rate":10`) || !strings.Contains(f.bodies[0], `"sample_rate":24000`) {
		t.Fatalf("body %s", f.bodies[0])
	}
	if u := c.Usage(); u.Units[core.CapSynthesize] != 3 {
		t.Fatalf("characters = %v", u.Units[core.CapSynthesize])
	}
}
