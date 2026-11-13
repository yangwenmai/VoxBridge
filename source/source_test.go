package source

import (
	"context"
	"os"
	"testing"

	"github.com/yangwenmai/voxbridge/core"
	"github.com/yangwenmai/voxbridge/media"
)

// testdata/*.json are `yt-dlp -J -f bestaudio/best` outputs recorded with
// yt-dlp 2026.10.21, trimmed, with signed media URLs replaced.

func noProbe(t *testing.T) Prober {
	return func(context.Context, string, map[string]string) (media.Probe, error) {
		t.Fatal("ffprobe should not be needed")
		return media.Probe{}, nil
	}
}

func load(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestYouTubeLiveIsLive(t *testing.T) {
	src, err := FromInfo(context.Background(), "https://www.youtube.com/@aljazeeraenglish/live", load(t, "youtube_live"), noProbe(t))
	if err != nil {
		t.Fatal(err)
	}
	if src.Kind != core.SourceLive {
		t.Fatalf("kind = %s, want live", src.Kind)
	}
	if src.URL != "https://www.youtube.com/watch?v=gCNeDWCI0vo" {
		t.Errorf("url = %s", src.URL)
	}
	if src.Audio.URL == "" || src.Audio.Protocol != "m3u8_native" {
		t.Errorf("audio = %+v", src.Audio)
	}
	if src.Transcript != nil || src.Video != nil || src.DurationMs != 0 {
		t.Errorf("live source carries vod fields: %+v", src)
	}
}

func TestYouTubeVideoIsVODWithManualSubtitles(t *testing.T) {
	src, err := FromInfo(context.Background(), "https://www.youtube.com/watch?v=arj7oStGLkU", load(t, "youtube_vod_ted"), noProbe(t))
	if err != nil {
		t.Fatal(err)
	}
	if src.Kind != core.SourceVOD || src.DurationMs != 844000 {
		t.Fatalf("kind %s duration %d, want vod 844000", src.Kind, src.DurationMs)
	}
	if src.Audio.Codec != "opus" {
		t.Errorf("audio codec = %s, want the audio-only opus track", src.Audio.Codec)
	}
	if src.Video == nil || src.Video.Height != 1080 {
		t.Errorf("video = %+v, want 1080p", src.Video)
	}
	tr := src.Transcript
	if tr == nil || tr.Language != "en" || tr.Kind != core.TranscriptManual || tr.Format != "vtt" {
		t.Fatalf("transcript = %+v, want manual en vtt", tr)
	}
}

func TestAutoCaptionsOnlyInSpokenLanguage(t *testing.T) {
	i := info{Language: "en", AutomaticCaptions: map[string][]subtitle{
		"fr":      {{Ext: "vtt", URL: "u-fr"}},
		"en-orig": {{Ext: "vtt", URL: "u-orig-vtt"}, {Ext: "json3", URL: "u-orig"}},
		"en":      {{Ext: "vtt", URL: "u-en"}},
	}}
	tr := i.transcript()
	if tr == nil || tr.URL != "u-orig" || tr.Kind != core.TranscriptAuto || tr.Language != "en" {
		t.Fatalf("transcript = %+v, want the original-language json3", tr)
	}
	i.Language = ""
	if tr := i.transcript(); tr != nil {
		t.Fatalf("unknown spoken language picked %+v; auto tracks are mostly machine translations", tr)
	}
}

func TestLiveChatIsNotATranscript(t *testing.T) {
	i := info{Subtitles: map[string][]subtitle{"live_chat": {{Ext: "json", URL: "chat"}}}}
	if tr := i.transcript(); tr != nil {
		t.Fatalf("picked %+v", tr)
	}
}

func TestPodcastMP3UsesFFprobeForDurationAndTitle(t *testing.T) {
	probed := false
	probe := func(context.Context, string, map[string]string) (media.Probe, error) {
		probed = true
		// Recorded ffprobe output for the same file.
		return media.Probe{DurationMs: 10875024, Title: "#504 – Russia-Ukraine War: Putin, Zelenskyy, Drones and the Future of Warfare"}, nil
	}
	src, err := FromInfo(context.Background(), "https://media.blubrry.com/x.mp3", load(t, "podcast_mp3"), probe)
	if err != nil {
		t.Fatal(err)
	}
	if !probed || src.Kind != core.SourceVOD || src.DurationMs != 10875024 {
		t.Fatalf("probed %v kind %s duration %d", probed, src.Kind, src.DurationMs)
	}
	if src.Title != "#504 – Russia-Ukraine War: Putin, Zelenskyy, Drones and the Future of Warfare" {
		t.Errorf("title = %q, want the ID3 title rather than the file name", src.Title)
	}
	if src.Transcript != nil || src.Video != nil {
		t.Errorf("mp3 has transcript or video: %+v", src)
	}
}

func TestEndlessStreamWithoutMetadataIsLive(t *testing.T) {
	raw := []byte(`{"_type":"video","title":"radio","extractor":"generic","url":"https://radio.example/live.m3u8","protocol":"m3u8_native"}`)
	probe := func(context.Context, string, map[string]string) (media.Probe, error) { return media.Probe{}, nil }
	src, err := FromInfo(context.Background(), "https://radio.example/live.m3u8", raw, probe)
	if err != nil {
		t.Fatal(err)
	}
	if src.Kind != core.SourceLive {
		t.Fatalf("kind = %s, want live", src.Kind)
	}
}

func TestBilibiliVideoWithoutLiveStatusIsVOD(t *testing.T) {
	src, err := FromInfo(context.Background(), "https://www.bilibili.com/video/BV1GJ411x7h7", load(t, "bilibili_vod"), noProbe(t))
	if err != nil {
		t.Fatal(err)
	}
	if src.Kind != core.SourceVOD || src.DurationMs != 212393 {
		t.Fatalf("kind %s duration %d", src.Kind, src.DurationMs)
	}
	if src.Audio.Headers["Referer"] == "" {
		t.Errorf("bilibili audio needs its Referer header: %+v", src.Audio.Headers)
	}
}

func TestChannelIsRejected(t *testing.T) {
	_, err := FromInfo(context.Background(), "https://www.youtube.com/@TED", load(t, "youtube_channel"), noProbe(t))
	assertKind(t, err, ErrNotSingle)
}

func TestUpcomingLiveIsRejected(t *testing.T) {
	raw := []byte(`{"_type":"video","title":"soon","live_status":"is_upcoming","url":"https://x/a.m3u8"}`)
	_, err := FromInfo(context.Background(), "https://x", raw, noProbe(t))
	assertKind(t, err, ErrNotLiveYet)
}

func assertKind(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	se, ok := err.(*Error)
	if !ok || se.Kind != want {
		t.Fatalf("err = %v, want kind %s", err, want)
	}
}

func TestClassifyYtDlpErrors(t *testing.T) {
	// stderr recorded from yt-dlp 2026.10.21 for the URL in each comment.
	cases := []struct {
		stderr string
		want   ErrorKind
	}{
		// https://example.com/
		{"ERROR: Unsupported URL: https://example.com/", ErrUnsupported},
		// https://www.youtube.com/watch?v=aaaaaaaaaaa
		{"ERROR: [youtube] aaaaaaaaaaa: This video is unavailable", ErrUnavailable},
		// an ended YouTube live stream
		{"ERROR: [youtube] jfKfPfyJRdk: This live stream recording is not available.", ErrUnavailable},
		// a Spotify episode
		{"ERROR: [DRM] The requested site is known to use DRM protection. It will NOT be supported.\n       Please DO NOT open an issue, unless you have evidence that the video is not DRM protected", ErrDRM},
		// https://nonexistent.invalid/a.mp3
		{"ERROR: [generic] a: Unable to download webpage: HTTPSConnection(host='nonexistent.invalid', port=443): Failed to resolve 'nonexistent.invalid' ([Errno 8] nodename nor servname provided, or not known)", ErrNetwork},
	}
	for _, c := range cases {
		if got := ClassifyYtDlp(c.stderr); got != c.want {
			t.Errorf("%q: got %s, want %s", c.stderr, got, c.want)
		}
	}
}
