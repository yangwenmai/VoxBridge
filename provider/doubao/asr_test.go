package doubao

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yangwenmai/voxbridge/core"
	"github.com/yangwenmai/voxbridge/media"
	"github.com/yangwenmai/voxbridge/provider/internal/wire"
)

// Recorded 2026-11-13 against volc.seedasr.auc with 5 s of English.
func asrReply(t *testing.T, name string) wire.Reply {
	t.Helper()
	var rec struct {
		Status int               `json:"status"`
		Header map[string]string `json:"header"`
		Body   string            `json:"body"`
	}
	if err := json.Unmarshal(recorded(t, name), &rec); err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	for k, v := range rec.Header {
		h.Set(k, v)
	}
	return wire.Reply{Status: rec.Status, Header: h, Body: []byte(rec.Body)}
}

type asrServer struct {
	submits, queries []string // task ids
	submitReplies    []wire.Reply
	queryReplies     []wire.Reply
}

func (s *asrServer) client(t *testing.T, durationMs int64) *Client {
	c := NewClient(testKey)
	next := func(q *[]wire.Reply) wire.Reply {
		if len(*q) == 0 {
			t.Fatal("unexpected request")
		}
		r := (*q)[0]
		if len(*q) > 1 {
			*q = (*q)[1:]
		}
		return r
	}
	c.send = func(_ context.Context, _, url string, h http.Header, _ []byte) (wire.Reply, error) {
		id := h.Get("X-Api-Request-Id")
		if strings.HasSuffix(url, "/submit") {
			s.submits = append(s.submits, id)
			return next(&s.submitReplies), nil
		}
		s.queries = append(s.queries, id)
		return next(&s.queryReplies), nil
	}
	c.wait = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	c.probe = func(context.Context, string, map[string]string) (media.Probe, error) {
		return media.Probe{DurationMs: durationMs}, nil
	}
	c.encode = func(context.Context, core.AudioInput, int64, int64) ([]byte, error) { return []byte("mp3"), nil }
	return c
}

type memTasks struct{ state []byte }

func (m *memTasks) Load() ([]byte, error)   { return m.state, nil }
func (m *memTasks) Save(b []byte) error     { m.state = append([]byte(nil), b...); return nil }
func (m *memTasks) Progress(string, ...any) {}
func (m *memTasks) parsed(t *testing.T) asrState {
	var s asrState
	json.Unmarshal(m.state, &s)
	return s
}

func TestRecognizeSubmitsOnceAndPollsUntilDone(t *testing.T) {
	s := &asrServer{
		submitReplies: []wire.Reply{asrReply(t, "asr_submit_ok.json")},
		queryReplies:  []wire.Reply{asrReply(t, "asr_query_processing.json"), asrReply(t, "asr_query_processing.json"), asrReply(t, "asr_query_done.json")},
	}
	c := s.client(t, 5112)
	tasks := &memTasks{}
	lines, err := c.Transcribe(context.Background(), core.AudioInput{URL: "original.flac"}, tasks)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0].Text, "When I was 27 years old") || lines[0].StartMs != 210 || *lines[0].Speaker != "1" {
		t.Fatalf("lines = %+v", lines)
	}
	if len(s.submits) != 1 || len(s.queries) != 3 || s.queries[2] != s.submits[0] {
		t.Fatalf("submits %v, queries %v; queries must use the submitted task id", s.submits, s.queries)
	}
	if u := c.Usage(); u.Units[core.CapRecognize] != 5.112 {
		t.Fatalf("billed %v s", u.Units[core.CapRecognize])
	}
	if st := tasks.parsed(t); !st.Chunks[0].Done || st.Chunks[0].TaskID != s.submits[0] {
		t.Fatalf("saved state %+v", st)
	}
}

func TestInterruptedPollingResumesWithoutResubmitting(t *testing.T) {
	s := &asrServer{
		submitReplies: []wire.Reply{asrReply(t, "asr_submit_ok.json")},
		queryReplies:  []wire.Reply{asrReply(t, "asr_query_processing.json")},
	}
	c := s.client(t, 5112)
	ctx, cancel := context.WithCancel(context.Background())
	c.wait = func(context.Context, time.Duration) error { cancel(); return ctx.Err() }
	tasks := &memTasks{}
	if _, err := c.Transcribe(ctx, core.AudioInput{URL: "original.flac"}, tasks); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	taskID := s.submits[0]

	s2 := &asrServer{queryReplies: []wire.Reply{asrReply(t, "asr_query_processing.json"), asrReply(t, "asr_query_done.json")}}
	c2 := s2.client(t, 5112)
	lines, err := c2.Transcribe(context.Background(), core.AudioInput{URL: "original.flac"}, tasks)
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.submits) != 0 || len(lines) != 1 || s2.queries[0] != taskID {
		t.Fatalf("resume submitted %d times, queried %v (want %s)", len(s2.submits), s2.queries, taskID)
	}
	if u := c2.Usage(); u.Cost != 0 {
		t.Fatalf("resume billed %v", u.Cost)
	}

	// Once done, the sentences come from the saved state with no request.
	s3 := &asrServer{}
	if _, err := s3.client(t, 5112).Transcribe(context.Background(), core.AudioInput{URL: "original.flac"}, tasks); err != nil {
		t.Fatal(err)
	}
	if len(s3.submits)+len(s3.queries) != 0 {
		t.Fatalf("finished recognition sent %d requests", len(s3.submits)+len(s3.queries))
	}
}

func TestSavedButUnsentTaskIsCheckedBeforeSubmitting(t *testing.T) {
	saved := func() *memTasks {
		return &memTasks{state: mustJSON(asrState{Resource: asrRes, Chunks: []asrChunk{{DurationMs: 5112, TaskID: "task-1"}}})}
	}
	// The service has it: the last run was cut off after it was accepted.
	s := &asrServer{queryReplies: []wire.Reply{asrReply(t, "asr_query_processing.json"), asrReply(t, "asr_query_done.json")}}
	if _, err := s.client(t, 5112).Transcribe(context.Background(), core.AudioInput{}, saved()); err != nil {
		t.Fatal(err)
	}
	if len(s.submits) != 0 {
		t.Fatal("resubmitted a task the service already had")
	}
	// The service never got it: submit once, with the saved id.
	s = &asrServer{
		submitReplies: []wire.Reply{asrReply(t, "asr_submit_ok.json")},
		queryReplies:  []wire.Reply{asrReply(t, "asr_query_not_found.json"), asrReply(t, "asr_query_done.json")},
	}
	if _, err := s.client(t, 5112).Transcribe(context.Background(), core.AudioInput{}, saved()); err != nil {
		t.Fatal(err)
	}
	if len(s.submits) != 1 || s.submits[0] != "task-1" {
		t.Fatalf("submits = %v", s.submits)
	}
}

func TestPollingErrors(t *testing.T) {
	submitted := func() *memTasks {
		return &memTasks{state: mustJSON(asrState{Resource: asrRes, Chunks: []asrChunk{{DurationMs: 5112, TaskID: "task-1", Submitted: true}}})}
	}
	notGranted := wire.Reply{Status: 403, Body: []byte(`{}`), Header: http.Header{
		"X-Api-Status-Code": {"45000030"},
		"X-Api-Message":     {"[resource_id=volc.seedasr.auc] requested resource not granted"},
		"X-Tt-Logid":        {"log-1"},
	}}
	busy := wire.Reply{Status: 503, Body: []byte(`{}`), Header: http.Header{"X-Api-Status-Code": {"55000031"}, "X-Api-Message": {"server busy"}}}
	badAudio := wire.Reply{Status: 200, Body: []byte(`{}`), Header: http.Header{"X-Api-Status-Code": {"45000151"}, "X-Api-Message": {"invalid audio format"}}}
	cases := []struct {
		name    string
		replies []wire.Reply
		kind    core.ErrorKind
		queries int
	}{
		{"expired task", []wire.Reply{asrReply(t, "asr_query_not_found.json")}, core.ErrBadRequest, 1},
		{"not enabled", []wire.Reply{notGranted}, core.ErrNotEnabled, 1},
		{"busy past every retry", []wire.Reply{busy}, core.ErrRateLimited, 4},
		{"failed task", []wire.Reply{asrReply(t, "asr_query_processing.json"), badAudio}, core.ErrBadRequest, 2},
	}
	for _, tc := range cases {
		s := &asrServer{queryReplies: tc.replies}
		_, err := s.client(t, 5112).Transcribe(context.Background(), core.AudioInput{}, submitted())
		var pe *core.ProviderError
		if !errors.As(err, &pe) || pe.Kind != tc.kind {
			t.Fatalf("%s: err = %v", tc.name, err)
		}
		if len(s.submits) != 0 || len(s.queries) != tc.queries {
			t.Fatalf("%s: %d submits, %d queries", tc.name, len(s.submits), len(s.queries))
		}
	}
	// A busy query that recovers keeps polling.
	s := &asrServer{queryReplies: []wire.Reply{busy, asrReply(t, "asr_query_done.json")}}
	if _, err := s.client(t, 5112).Transcribe(context.Background(), core.AudioInput{}, submitted()); err != nil {
		t.Fatal(err)
	}
}

func TestLongAudioIsSplitWithOffsets(t *testing.T) {
	s := &asrServer{
		submitReplies: []wire.Reply{asrReply(t, "asr_submit_ok.json")},
		queryReplies:  []wire.Reply{asrReply(t, "asr_query_done.json")},
	}
	lines, err := s.client(t, ASRChunk.Milliseconds()+5112).Transcribe(context.Background(), core.AudioInput{}, &memTasks{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.submits) != 2 || s.submits[0] == s.submits[1] {
		t.Fatalf("submits = %v", s.submits)
	}
	if len(lines) != 2 || lines[1].StartMs != ASRChunk.Milliseconds()+210 {
		t.Fatalf("lines = %+v", lines)
	}
}
