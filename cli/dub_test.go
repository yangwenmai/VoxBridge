package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yangwenmai/voxbridge/core"
	"github.com/yangwenmai/voxbridge/media"
	"github.com/yangwenmai/voxbridge/source"
)

const podcastURL = "https://media.blubrry.com/x.mp3"

type countingDub struct{ asr, mt, tts int }

func (c *countingDub) Transcribe(context.Context, core.AudioInput) ([]core.Line, error) {
	c.asr++
	return nil, errors.New("stop after recognition")
}

func (c *countingDub) Translate(context.Context, []string, core.Lang) ([]string, error) {
	c.mt++
	return nil, errors.New("unexpected")
}

func (c *countingDub) Synthesize(context.Context, string, string) (core.Audio, error) {
	c.tts++
	return core.Audio{}, errors.New("unexpected")
}

// withPodcast resolves every link to the recorded podcast mp3, which has no
// platform subtitles, and counts the provider requests a dub makes.
func withPodcast(t *testing.T, tty bool, answer string) *countingDub {
	t.Helper()
	data, err := os.ReadFile("../source/testdata/podcast_mp3.json")
	if err != nil {
		t.Fatal(err)
	}
	src, err := source.FromInfo(context.Background(), podcastURL, data,
		func(context.Context, string, map[string]string) (media.Probe, error) {
			// Recorded ffprobe output for the same file: 3:01:15.
			return media.Probe{DurationMs: 10875024}, nil
		})
	if err != nil || src.Transcript != nil {
		t.Fatalf("source %+v, %v", src, err)
	}
	c := &countingDub{}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DOUBAO_API_KEY", "test-key")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	r, rt, df, in, tty0 := resolve, requireTools, dubbingFor, stdin, interactive
	t.Cleanup(func() { resolve, requireTools, dubbingFor, stdin, interactive = r, rt, df, in, tty0 })
	resolve = func(context.Context, string) (core.Source, error) { return src, nil }
	requireTools = func() error { return nil }
	dubbingFor = func(core.Dubber, string) core.Dubbing {
		return core.Dubbing{Recognizer: c, Translator: c, Synthesizer: c, Usage: func() core.Usage { return core.Usage{} }}
	}
	stdin = strings.NewReader(answer)
	interactive = func() bool { return tty }
	return c
}

func runDub(t *testing.T, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), append([]string{"dub", podcastURL}, args...), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestNoSubtitlesWithoutTTYAsksForASRFlag(t *testing.T) {
	c := withPodcast(t, false, "")
	code, _, stderr := runDub(t, "--out", t.TempDir())
	if code == 0 || c.asr+c.mt+c.tts != 0 {
		t.Fatalf("exit %d, requests %+v", code, c)
	}
	if !strings.Contains(stderr, "--asr") || !strings.Contains(stderr, "约 13.59 元") {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestNoSubtitlesDeclinedMakesNoRequest(t *testing.T) {
	for _, answer := range []string{"\n", "n\n", "no\n", ""} {
		c := withPodcast(t, true, answer)
		code, stdout, _ := runDub(t, "--out", t.TempDir())
		if code == 0 || c.asr+c.mt+c.tts != 0 {
			t.Fatalf("answer %q: exit %d, requests %+v", answer, code, c)
		}
		for _, want := range []string{"3:01:15", "豆包语音（火山引擎）", "volc.bigasr.auc_turbo", "识别约 13.59 元", "[y/N]", "已取消，没有发出任何请求。"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("answer %q: prompt lacks %q:\n%s", answer, want, stdout)
			}
		}
	}
}

// outWithOriginal is a work directory whose original audio is already
// cached, so a run goes straight to recognition.
func outWithOriginal(t *testing.T) string {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "original.flac"), []byte("flac"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestYesOrASRFlagRecognizes(t *testing.T) {
	c := withPodcast(t, true, "y\n")
	runDub(t, "--out", outWithOriginal(t))
	if c.asr != 1 {
		t.Fatalf("after yes: %d recognitions", c.asr)
	}

	c = withPodcast(t, false, "")
	_, stdout, _ := runDub(t, "--out", outWithOriginal(t), "--asr")
	if c.asr != 1 || strings.Contains(stdout, "[y/N]") {
		t.Fatalf("--asr: %d recognitions, prompt shown: %v", c.asr, strings.Contains(stdout, "[y/N]"))
	}
}

func TestRedirectedStdinIsNotInteractive(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if isTerminal(null) || isTerminal(r) {
		t.Fatal("/dev/null or a pipe counted as a terminal; it would be prompted instead of told to pass --asr")
	}
}
