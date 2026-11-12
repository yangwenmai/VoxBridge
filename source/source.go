// Package source resolves a link into a core.Source with the system yt-dlp
// and ffprobe. It does not bypass DRM or sign in.
package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yangwenmai/voxbridge/core"
	"github.com/yangwenmai/voxbridge/media"
)

// ErrorKind tells the user why a link cannot be used.
type ErrorKind string

const (
	ErrUnsupported   ErrorKind = "unsupported"
	ErrDRM           ErrorKind = "drm"
	ErrLoginRequired ErrorKind = "login_required"
	ErrBotCheck      ErrorKind = "bot_check"
	ErrUnavailable   ErrorKind = "unavailable"
	ErrGeoBlocked    ErrorKind = "geo_blocked"
	ErrNotLiveYet    ErrorKind = "not_live_yet"
	ErrNotSingle     ErrorKind = "not_single_item"
	ErrNoAudio       ErrorKind = "no_audio"
	ErrNetwork       ErrorKind = "network"
	ErrUnknown       ErrorKind = "unknown"
)

var messages = map[ErrorKind]string{
	ErrUnsupported:   "这个链接里没认出音频或视频。换一个直接指向视频、直播或音频文件的链接",
	ErrDRM:           "这个来源有 DRM 保护，VoxBridge 不支持",
	ErrLoginRequired: "这个内容要登录或付费才能看（私享、会员、年龄限制），VoxBridge 不做登录抓取",
	ErrBotCheck:      "平台要求人机验证，暂时拿不到。过一会儿再试，或换网络",
	ErrUnavailable:   "内容不存在或已下架",
	ErrGeoBlocked:    "内容在你所在的地区不可用",
	ErrNotLiveYet:    "直播还没开始，开播后再试",
	ErrNotSingle:     "这是频道、播放列表或网页，不是单个视频。VoxBridge 一次只处理一个视频或直播",
	ErrNoAudio:       "解析到了内容，但里面没有音轨",
	ErrNetwork:       "网络不通，或对方服务器没有响应",
	ErrUnknown:       "解析失败",
}

type Error struct {
	Kind ErrorKind
	// Detail is the tool's own message, for bug reports.
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return messages[e.Kind]
	}
	return messages[e.Kind] + "（" + e.Detail + "）"
}

// ClassifyYtDlp maps yt-dlp's stderr to an ErrorKind.
func ClassifyYtDlp(stderr string) ErrorKind {
	s := strings.ToLower(stderr)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(s, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("[drm]", "drm protected", "drm protection"):
		return ErrDRM
	case has("not a bot", "confirm you're not a robot"):
		return ErrBotCheck
	case has("private video", "sign in to confirm your age", "members-only", "join this channel",
		"only available for registered users", "login required", "requires authentication",
		"account credentials", "use --cookies", "this video is only available to"):
		return ErrLoginRequired
	case has("not available in your country", "geo restrict", "geo-restrict", "not available from your location"):
		return ErrGeoBlocked
	case has("live event will begin", "premieres in", "this live event will", "is_upcoming", "scheduled to start"):
		return ErrNotLiveYet
	case has("unsupported url"):
		return ErrUnsupported
	case has("video unavailable", "this video is unavailable", "has been removed", "recording is not available",
		"http error 404", "http error 410", "does not exist", "no longer available"):
		return ErrUnavailable
	case has("unable to download webpage", "failed to resolve", "nodename nor servname", "name or service not known",
		"timed out", "connection refused", "connection reset", "network is unreachable", "temporary failure in name resolution"):
		return ErrNetwork
	}
	return ErrUnknown
}

// Resolve turns a link into a Source. Live means the stream has no end;
// everything with a known duration is vod.
func Resolve(ctx context.Context, url string) (core.Source, error) {
	if err := media.Require("yt-dlp", "ffprobe", "ffmpeg"); err != nil {
		return core.Source{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	// -I 0 keeps a pasted channel or playlist from listing every entry
	// before it is rejected.
	out, err := media.Run(ctx, "yt-dlp", "-J", "--no-playlist", "--flat-playlist", "-I", "0", "--no-warnings",
		"-f", "bestaudio/best", "--", url)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return core.Source{}, &Error{Kind: ErrNetwork, Detail: "yt-dlp 90 秒没有返回"}
		}
		var exit *media.ExitError
		if errors.As(err, &exit) {
			return core.Source{}, &Error{Kind: ClassifyYtDlp(exit.Stderr), Detail: firstError(exit.Stderr)}
		}
		return core.Source{}, err
	}
	return FromInfo(ctx, url, out, media.FFprobe)
}

// Prober reads duration and title of a media URL.
type Prober func(ctx context.Context, url string, headers map[string]string) (media.Probe, error)

// FromInfo builds a Source from `yt-dlp -J` output. probe is only called
// when yt-dlp cannot tell whether the stream ends.
func FromInfo(ctx context.Context, url string, ytdlpJSON []byte, probe Prober) (core.Source, error) {
	var i info
	if err := json.Unmarshal(ytdlpJSON, &i); err != nil {
		return core.Source{}, &Error{Kind: ErrUnknown, Detail: "yt-dlp 输出看不懂: " + err.Error()}
	}
	if i.Type == "playlist" || i.Type == "multi_video" || i.Type == "url" {
		return core.Source{}, &Error{Kind: ErrNotSingle}
	}
	audio, ok := i.audio()
	if !ok {
		return core.Source{}, &Error{Kind: ErrNoAudio}
	}
	src := core.Source{
		URL:      firstNonEmpty(i.WebpageURL, i.OriginalURL, url),
		Title:    i.Title,
		Language: core.Lang(i.Language),
		Audio:    audio,
	}
	durationMs := int64(i.Duration * 1000)
	state := i.liveState()
	if state == stateUpcoming {
		return core.Source{}, &Error{Kind: ErrNotLiveYet}
	}
	if state == stateUnknown && durationMs == 0 {
		p, err := probe(ctx, audio.URL, audio.Headers)
		if err != nil {
			var exit *media.ExitError
			if errors.As(err, &exit) {
				return core.Source{}, &Error{Kind: ErrUnavailable, Detail: firstError(exit.Stderr)}
			}
			return core.Source{}, err
		}
		durationMs = p.DurationMs
		if p.Title != "" && i.Extractor == "generic" {
			src.Title = p.Title
		}
	}
	if state == stateLive || (state == stateUnknown && durationMs == 0) {
		src.Kind = core.SourceLive
		return src, nil
	}
	src.Kind = core.SourceVOD
	src.DurationMs = durationMs
	src.Video = i.video()
	src.Transcript = i.transcript()
	return src, nil
}

func firstError(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ERROR:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "ERROR:"))
			if len(line) > 200 {
				line = line[:200] + "…"
			}
			return line
		}
	}
	return strings.TrimSpace(fmt.Sprintf("%.200s", stderr))
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
