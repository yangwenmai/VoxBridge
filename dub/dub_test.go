package dub

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/yangwenmai/voxbridge/core"
)

func TestAlignWaitsAtSentenceStart(t *testing.T) {
	cues := []Cue{
		{StartMs: 1000, DubMs: 2000}, // fits before the next sentence
		{StartMs: 4000, DubMs: 5000}, // runs 2 s past the next start
		{StartMs: 7000, DubMs: 1000}, // original waits 2 s here
		{StartMs: 9500, DubMs: 500},  // shift carries over
		{StartMs: 20000, DubMs: 100},
	}
	want := []Slot{
		{OutStartMs: 1000, ShiftMs: 0},
		{OutStartMs: 4000, ShiftMs: 0},
		{OutStartMs: 9000, ShiftMs: 2000},
		{OutStartMs: 11500, ShiftMs: 2000},
		{OutStartMs: 22000, ShiftMs: 2000},
	}
	got := Align(cues)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("slot %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i].OutStartMs < got[i-1].OutStartMs+cues[i-1].DubMs {
			t.Fatalf("Chinese %d starts before %d ends", i, i-1)
		}
	}
	if OutMs(cues, got, 8000) != 10000 || OutMs(cues, got, 500) != 500 {
		t.Fatal("original times map through the inserted silence")
	}
}

type fakeDub struct {
	mu          sync.Mutex
	translated  int
	synthesized int
	// stopAfter cancels the run once this many lines are synthesized.
	stopAfter int
	cancel    context.CancelFunc
	voices    []string
}

func (f *fakeDub) Translate(_ context.Context, texts []string, _ core.Lang) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.translated += len(texts)
	out := make([]string, len(texts))
	for i, t := range texts {
		out[i] = "译" + t
	}
	return out, nil
}

func (f *fakeDub) Synthesize(ctx context.Context, text, voice string) (core.Audio, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return core.Audio{}, err
	}
	f.synthesized++
	f.voices = append(f.voices, voice)
	if f.synthesized == f.stopAfter {
		f.cancel()
	}
	return core.Audio{Data: []byte(text), Format: "mp3"}, nil
}

func testOptions(dir string, f *fakeDub, lines []core.Line) Options {
	return Options{
		Dir: dir, Target: "zh", Concurrency: 2, Voices: []string{"v1", "v2"},
		Providers: core.DubProviders{Translator: "fake", Synthesizer: "fake"},
		Dubbing:   core.Dubbing{Translator: f, Synthesizer: f},
		FetchTranscript: func(context.Context, core.TranscriptRef) ([]core.Line, error) {
			return lines, nil
		},
		Original: func(_ context.Context, _ core.AudioInput, path string) error {
			return os.WriteFile(path, []byte("flac"), 0o644)
		},
		// Each character of a clip lasts 100 ms.
		Decode: func(_ context.Context, path string) ([]int16, error) {
			b, err := os.ReadFile(path)
			return make([]int16, utf8.RuneCount(b)*SampleRate/10), err
		},
	}
}

func episode(n int) []core.Line {
	lines := make([]core.Line, n)
	for i := range lines {
		lines[i] = core.Line{StartMs: int64(i) * 3000, Text: "sentence " + strings.Repeat("x", i%5)}
	}
	lines[3].Text = "[Music]"
	return lines
}

var vod = core.Source{Kind: core.SourceVOD, URL: "https://example.com/v", Language: "en",
	Transcript: &core.TranscriptRef{Language: "en", Kind: core.TranscriptManual, Format: "vtt"}}

func TestInterruptedRunResumesWithoutRepeatingRequests(t *testing.T) {
	dir := t.TempDir()
	lines := episode(40)
	f := &fakeDub{stopAfter: 15}
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	if _, err := Run(ctx, vod, testOptions(dir, f, lines)); err == nil {
		t.Fatal("interrupted run reported success")
	}
	if f.translated != 39 {
		t.Fatalf("translated %d lines, want 39 (one is a sound cue)", f.translated)
	}
	first := f.synthesized

	f2 := &fakeDub{}
	res, err := Run(context.Background(), vod, testOptions(dir, f2, lines))
	if err != nil {
		t.Fatal(err)
	}
	if f2.translated != 0 {
		t.Fatalf("resume translated %d lines again", f2.translated)
	}
	if first+f2.synthesized != 39 {
		t.Fatalf("synthesized %d + %d, want 39 in total", first, f2.synthesized)
	}
	if res.Dubbed != 39 || res.Skipped != 1 {
		t.Fatalf("result = %+v", res)
	}

	f3 := &fakeDub{}
	if _, err := Run(context.Background(), vod, testOptions(dir, f3, lines)); err != nil {
		t.Fatal(err)
	}
	if f3.translated+f3.synthesized != 0 {
		t.Fatalf("finished dub sent %d requests", f3.translated+f3.synthesized)
	}
	srt, _ := os.ReadFile(res.SRT)
	if !strings.Contains(string(srt), "译sentence x\nsentence x\n") {
		t.Fatalf("srt lacks the bilingual line:\n%s", srt)
	}
}

func TestResumeRefusesAnotherLink(t *testing.T) {
	dir := t.TempDir()
	if _, err := Run(context.Background(), vod, testOptions(dir, &fakeDub{}, episode(5))); err != nil {
		t.Fatal(err)
	}
	other := vod
	other.URL = "https://example.com/other"
	if _, err := Run(context.Background(), other, testOptions(dir, &fakeDub{}, episode(5))); err == nil {
		t.Fatal("reused a directory holding another link's dub")
	}
}

func TestVoicesByTalkTime(t *testing.T) {
	name := func(s string) *string { return &s }
	end := func(ms int64) *int64 { return &ms }
	lines := []core.Line{
		{StartMs: 0, EndMs: end(1000), Speaker: name("Host"), Text: "Hi."},
		{StartMs: 1000, EndMs: end(9000), Speaker: name("Guest"), Text: "Long answer."},
		{StartMs: 9000, EndMs: end(10000), Speaker: name("Host"), Text: "Thanks."},
	}
	dir := t.TempDir()
	f := &fakeDub{}
	if _, err := Run(context.Background(), vod, testOptions(dir, f, lines)); err != nil {
		t.Fatal(err)
	}
	m, _ := loadManifest(dir)
	if m.Voices["Guest"] != "v1" || m.Voices["Host"] != "v2" {
		t.Fatalf("voices = %v; the speaker who talks most gets the first voice", m.Voices)
	}

	// Under 80% labelled: everyone gets the first voice.
	lines[0].Speaker, lines[1].Speaker = nil, nil
	dir = t.TempDir()
	f = &fakeDub{}
	if _, err := Run(context.Background(), vod, testOptions(dir, f, lines)); err != nil {
		t.Fatal(err)
	}
	m, _ = loadManifest(dir)
	if m.Voices != nil {
		t.Fatalf("voices = %v, want none", m.Voices)
	}
	for _, v := range f.voices {
		if v != "v1" {
			t.Fatalf("voice %s used", v)
		}
	}
}

func TestLiveIsRefused(t *testing.T) {
	if _, err := Run(context.Background(), core.Source{Kind: core.SourceLive}, Options{}); err == nil {
		t.Fatal("live source accepted")
	}
}
