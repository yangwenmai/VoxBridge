// Package media runs the system ffmpeg, ffprobe and yt-dlp. VoxBridge never
// bundles them.
package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MissingToolError means a required program is not on PATH.
type MissingToolError struct{ Tools []string }

func (e *MissingToolError) Error() string {
	return fmt.Sprintf("缺少 %s。macOS 用 `brew install %s` 安装，其他系统见 README", strings.Join(e.Tools, "、"), strings.Join(brewNames(e.Tools), " "))
}

func brewNames(tools []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tools {
		if t == "ffprobe" {
			t = "ffmpeg"
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// Require fails with a MissingToolError naming every absent tool.
func Require(tools ...string) error {
	var missing []string
	for _, t := range tools {
		if _, err := exec.LookPath(t); err != nil {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return &MissingToolError{Tools: missing}
	}
	return nil
}

// Run executes a tool and returns stdout. A non-zero exit returns an
// *ExitError carrying stderr.
func Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return stdout.Bytes(), &ExitError{Tool: name, Stderr: stderr.String()}
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

type ExitError struct {
	Tool   string
	Stderr string
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("%s failed: %s", e.Tool, strings.TrimSpace(e.Stderr))
}

// HeaderArgs renders HTTP headers for ffmpeg's -headers option.
func HeaderArgs(h map[string]string) []string {
	if len(h) == 0 {
		return nil
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + ": " + h[k] + "\r\n")
	}
	return []string{"-headers", b.String()}
}

// Probe is what ffprobe says about a media URL. DurationMs is 0 when the
// stream has no end, as with a live stream.
type Probe struct {
	DurationMs int64
	Title      string
}

// FFprobe reads container duration and title.
func FFprobe(ctx context.Context, url string, headers map[string]string) (Probe, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	args := append([]string{"-v", "error"}, HeaderArgs(headers)...)
	args = append(args, "-show_entries", "format=duration:format_tags=title", "-of", "default=noprint_wrappers=1", url)
	out, err := Run(ctx, "ffprobe", args...)
	if err != nil {
		return Probe{}, err
	}
	var p Probe
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "duration":
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				p.DurationMs = int64(f * 1000)
			}
		case "TAG:title":
			p.Title = v
		}
	}
	return p, nil
}

// DecodePCM streams mono 16-bit little-endian PCM at rate from url, stopping
// after limit when limit > 0. Close the reader to stop ffmpeg.
func DecodePCM(ctx context.Context, url string, headers map[string]string, rate int, limit time.Duration) (io.ReadCloser, error) {
	args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin"}, HeaderArgs(headers)...)
	args = append(args, "-i", url)
	if limit > 0 {
		args = append(args, "-t", strconv.FormatFloat(limit.Seconds(), 'f', 3, 64))
	}
	args = append(args, "-vn", "-ac", "1", "-ar", strconv.Itoa(rate), "-f", "s16le", "pipe:1")
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &pcmStream{ReadCloser: out, cmd: cmd, stderr: &stderr}, nil
}

type pcmStream struct {
	io.ReadCloser
	cmd    *exec.Cmd
	stderr *bytes.Buffer
}

func (s *pcmStream) Close() error {
	s.ReadCloser.Close()
	if err := s.cmd.Wait(); err != nil && s.stderr.Len() > 0 {
		return &ExitError{Tool: "ffmpeg", Stderr: s.stderr.String()}
	}
	return nil
}
