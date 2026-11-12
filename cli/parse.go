package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/yangwenmai/voxbridge/core"
	"github.com/yangwenmai/voxbridge/media"
	"github.com/yangwenmai/voxbridge/source"
)

func parse(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("parse", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "")
	sample := fs.String("sample", "", "")
	seconds := fs.Int("seconds", 10, "")
	url, rest := splitURL(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if url == "" && fs.NArg() > 0 {
		url = fs.Arg(0)
	}
	if url == "" {
		fmt.Fprintln(stderr, "用法: voxbridge parse <链接> [--json] [--sample out.wav]")
		return 2
	}

	src, err := source.Resolve(ctx, url)
	if err != nil {
		var se *source.Error
		if errors.As(err, &se) {
			fmt.Fprintf(stderr, "无法使用这个链接 [%s]：%v\n", se.Kind, se)
		} else {
			fmt.Fprintf(stderr, "无法使用这个链接：%v\n", err)
		}
		return 1
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		enc.Encode(src)
	} else {
		printSource(stdout, src)
	}

	if *sample != "" {
		if err := writeSample(ctx, src, *sample, time.Duration(*seconds)*time.Second); err != nil {
			fmt.Fprintf(stderr, "解码音频失败：%v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "已解码 %d 秒音频到 %s\n", *seconds, *sample)
	}
	return 0
}

// splitURL lets the link come before or after the flags.
func splitURL(args []string) (string, []string) {
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		return args[0], args[1:]
	}
	return "", args
}

func printSource(w io.Writer, s core.Source) {
	kind := "点播（可选实时同传或预先配音）"
	if s.Kind == core.SourceLive {
		kind = "直播（只能实时同传）"
	}
	fmt.Fprintf(w, "类型    %s\n标题    %s\n链接    %s\n", kind, s.Title, s.URL)
	if s.Language != "" {
		fmt.Fprintf(w, "语言    %s\n", s.Language)
	}
	if s.Kind == core.SourceVOD {
		fmt.Fprintf(w, "时长    %s\n", (time.Duration(s.DurationMs) * time.Millisecond).Round(time.Second))
	}
	fmt.Fprintf(w, "音频    %s %s\n", s.Audio.Protocol, s.Audio.Codec)
	if s.Video != nil {
		fmt.Fprintf(w, "画面    %dx%d\n", s.Video.Width, s.Video.Height)
	}
	if s.Transcript != nil {
		label := "人工字幕"
		if s.Transcript.Kind == core.TranscriptAuto {
			label = "平台自动字幕"
		}
		fmt.Fprintf(w, "字幕    %s %s（%s）\n", label, s.Transcript.Language, s.Transcript.Format)
	} else if s.Kind == core.SourceVOD {
		fmt.Fprintln(w, "字幕    无，配音时需要先做语音识别")
	}
}

func writeSample(ctx context.Context, s core.Source, path string, d time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, d+90*time.Second)
	defer cancel()
	r, err := media.DecodePCM(ctx, s.Audio.URL, s.Audio.Headers, core.InputSampleRate, d)
	if err != nil {
		return err
	}
	var pcm bytes.Buffer
	_, readErr := io.Copy(&pcm, r)
	if err := r.Close(); err != nil {
		return err
	}
	if readErr != nil {
		return readErr
	}
	if pcm.Len() == 0 {
		return fmt.Errorf("ffmpeg 没有输出音频")
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return media.WriteWAV(f, pcm.Bytes(), core.InputSampleRate)
}
