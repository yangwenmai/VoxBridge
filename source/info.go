package source

import (
	"sort"
	"strings"

	"github.com/yangwenmai/voxbridge/core"
)

// info is the part of `yt-dlp -J` output VoxBridge reads.
type info struct {
	Type        string  `json:"_type"`
	Title       string  `json:"title"`
	WebpageURL  string  `json:"webpage_url"`
	OriginalURL string  `json:"original_url"`
	Extractor   string  `json:"extractor"`
	IsLive      *bool   `json:"is_live"`
	LiveStatus  string  `json:"live_status"`
	Duration    float64 `json:"duration"`
	Language    string  `json:"language"`

	format
	RequestedFormats []format `json:"requested_formats"`
	Formats          []format `json:"formats"`

	Subtitles         map[string][]subtitle `json:"subtitles"`
	AutomaticCaptions map[string][]subtitle `json:"automatic_captions"`
}

type format struct {
	URL         string            `json:"url"`
	Protocol    string            `json:"protocol"`
	ACodec      string            `json:"acodec"`
	VCodec      string            `json:"vcodec"`
	Width       int               `json:"width"`
	Height      int               `json:"height"`
	HTTPHeaders map[string]string `json:"http_headers"`
}

type subtitle struct {
	Ext string `json:"ext"`
	URL string `json:"url"`
}

// liveState is what yt-dlp knows about liveness: live, vod, upcoming, or
// unknown when the extractor (e.g. a bare file or stream URL) says nothing.
type liveState int

const (
	stateUnknown liveState = iota
	stateLive
	stateVOD
	stateUpcoming
)

func (i info) liveState() liveState {
	switch i.LiveStatus {
	case "is_live":
		return stateLive
	case "is_upcoming":
		return stateUpcoming
	case "not_live", "was_live", "post_live":
		return stateVOD
	}
	if i.IsLive != nil {
		if *i.IsLive {
			return stateLive
		}
		return stateVOD
	}
	return stateUnknown
}

func hasAudio(f format) bool { return f.URL != "" && f.ACodec != "none" }
func hasVideo(f format) bool { return f.URL != "" && f.VCodec != "" && f.VCodec != "none" }

// audio picks the selected audio format: the audio half of a merged
// selection, or the single selected format.
func (i info) audio() (core.AudioInput, bool) {
	pick := i.format
	for _, f := range i.RequestedFormats {
		if hasAudio(f) && !hasVideo(f) {
			pick = f
		}
	}
	if !hasAudio(pick) {
		return core.AudioInput{}, false
	}
	return core.AudioInput{URL: pick.URL, Headers: pick.HTTPHeaders, Protocol: pick.Protocol, Codec: pick.ACodec}, true
}

// video picks the tallest picture up to 1080p.
func (i info) video() *core.VideoInput {
	var best *format
	for k := range i.Formats {
		f := &i.Formats[k]
		if !hasVideo(*f) || f.Height > 1080 {
			continue
		}
		if best == nil || f.Height > best.Height {
			best = f
		}
	}
	if best == nil {
		return nil
	}
	return &core.VideoInput{URL: best.URL, Headers: best.HTTPHeaders, Width: best.Width, Height: best.Height}
}

var subtitleFormats = []string{"vtt", "srt", "json3", "ttml", "srv3"}

// transcript prefers captions written for the spoken language, then the
// platform's recognition of the original audio. Machine-translated
// caption tracks are never picked.
func (i info) transcript() *core.TranscriptRef {
	lang := i.Language
	if ref := pickTrack(i.Subtitles, lang, core.TranscriptManual, false); ref != nil {
		return ref
	}
	if lang == "" {
		return nil
	}
	return pickTrack(i.AutomaticCaptions, lang, core.TranscriptAuto, true)
}

func pickTrack(tracks map[string][]subtitle, lang string, kind core.TranscriptKind, auto bool) *core.TranscriptRef {
	var candidates []string
	if auto {
		candidates = []string{lang + "-orig", lang}
	} else if lang != "" {
		base, _, _ := strings.Cut(lang, "-")
		var related []string
		for k := range tracks {
			if k != lang && strings.HasPrefix(k, base) && (len(k) == len(base) || k[len(base)] == '-') {
				related = append(related, k)
			}
		}
		sort.Strings(related)
		candidates = append([]string{lang}, related...)
	} else {
		// Unknown spoken language: only a single real track is unambiguous.
		var real []string
		for k := range tracks {
			if k != "live_chat" {
				real = append(real, k)
			}
		}
		if len(real) == 1 {
			candidates = real
		}
	}
	for _, key := range candidates {
		for _, ext := range subtitleFormats {
			for _, s := range tracks[key] {
				if s.Ext == ext && s.URL != "" {
					tag := strings.TrimSuffix(key, "-orig")
					return &core.TranscriptRef{Language: core.Lang(tag), Kind: kind, Format: ext, URL: s.URL}
				}
			}
		}
	}
	return nil
}
