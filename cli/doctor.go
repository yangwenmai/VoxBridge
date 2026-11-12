package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yangwenmai/voxbridge/config"
	"github.com/yangwenmai/voxbridge/core"
	"github.com/yangwenmai/voxbridge/media"
	"github.com/yangwenmai/voxbridge/provider/registry"
)

// Status marks are emoji, two columns wide in a terminal; detailIndent
// lines continuation text up under the text after a mark.
const (
	markOK       = "✅"
	markFail     = "❌"
	markOff      = "⚪"
	detailIndent = "     "
)

func doctor(ctx context.Context, stdout, stderr io.Writer) int {
	ok := true
	fmt.Fprintln(stdout, "工具")
	for _, t := range []struct{ name, flag string }{{"ffmpeg", "-version"}, {"ffprobe", "-version"}, {"yt-dlp", "--version"}} {
		if err := media.Require(t.name); err != nil {
			ok = false
			fmt.Fprintf(stdout, "  %s %s  %v\n", markFail, t.name, err)
			continue
		}
		out, _ := media.Run(ctx, t.name, t.flag)
		fmt.Fprintf(stdout, "  %s %s  %s\n", markOK, t.name, firstLine(string(out)))
	}

	path, err := config.Path()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	keys, err := config.Keys(registry.KeyEnv(), os.Getenv, path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	providers := registry.All()
	results := make([][]core.CheckResult, len(providers))
	var wg sync.WaitGroup
	for n, p := range providers {
		entry, has := keys[p.ID()]
		if !has {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
			defer cancel()
			results[n] = p.Check(cctx, entry.Key.Reveal())
		}()
	}
	wg.Wait()

	usable := 0
	for n, p := range providers {
		entry, has := keys[p.ID()]
		fmt.Fprintf(stdout, "\n%s\n", p.Name())
		if !has {
			fmt.Fprintf(stdout, "  %s 未配置。设置环境变量 %s，或写进 %s\n", markOff, p.KeyEnv(), path)
			continue
		}
		fmt.Fprintf(stdout, "  Key 来自 %s\n", describeOrigin(entry))
		caps := p.Capabilities()
		for _, r := range results[n] {
			label := capabilityLabel(r.Capability)
			if r.OK {
				usable++
				fmt.Fprintf(stdout, "  %s %s（%s）  %s%s\n", markOK, label, r.Model, r.Scope, costNote(r.Cost))
				if s, found := caps.Service(r.Capability); found {
					fmt.Fprintf(stdout, detailIndent+"%s\n", describeService(s))
				}
				continue
			}
			ok = false
			fmt.Fprintf(stdout, "  %s %s（%s）  %s\n", markFail, label, r.Model, describeErr(r.Err))
			if r.Scope != "" {
				fmt.Fprintf(stdout, detailIndent+"%s\n", r.Scope)
			}
		}
	}
	if usable == 0 {
		fmt.Fprintln(stdout, "\n没有可用的厂商能力。至少配置一家的 API Key。")
	}
	if !ok {
		return 1
	}
	return 0
}

func describeOrigin(e config.Entry) string {
	if e.Origin == config.FromEnv {
		return "环境变量 " + e.Source
	}
	return e.Source
}

func describeErr(err error) string {
	var pe *core.ProviderError
	if errors.As(err, &pe) {
		parts := []string{kindLabel(pe.Kind)}
		var detail []string
		if pe.HTTP != 0 {
			detail = append(detail, fmt.Sprintf("HTTP %d", pe.HTTP))
		}
		if pe.Code != "" {
			detail = append(detail, pe.Code)
		}
		if pe.Message != "" {
			detail = append(detail, pe.Message)
		}
		if pe.LogID != "" {
			detail = append(detail, "logid "+pe.LogID)
		}
		if len(detail) > 0 {
			parts = append(parts, "（"+strings.Join(detail, "，")+"）")
		}
		if pe.Hint != "" {
			parts = append(parts, "。"+pe.Hint)
		}
		return strings.Join(parts, "")
	}
	return "请求失败：" + err.Error()
}

func kindLabel(k core.ErrorKind) string {
	switch k {
	case core.ErrBadKey:
		return "Key 无效"
	case core.ErrNotEnabled:
		return "服务未开通或无权限"
	case core.ErrNotFound:
		return "模型不存在"
	case core.ErrRateLimited:
		return "被限流或额度不足"
	case core.ErrServer:
		return "服务端暂时出错，稍后再试"
	}
	return "请求被拒绝"
}

func capabilityLabel(c core.Capability) string {
	switch c {
	case core.CapInterpret:
		return "实时同传"
	case core.CapTranslate:
		return "机器翻译"
	case core.CapSynthesize:
		return "语音合成"
	case core.CapRecognize:
		return "语音识别"
	}
	return string(c)
}

func describeService(s core.Service) string {
	var targets int
	for _, r := range s.Languages {
		targets += len(r.To)
	}
	parts := []string{fmt.Sprintf("目标语言 %d 种", targets)}
	if len(s.Voices) > 0 {
		parts = append(parts, fmt.Sprintf("音色 %d 个", len(s.Voices)))
	}
	if s.Subtitles {
		parts = append(parts, "带字幕")
	}
	if s.InputSampleRate > 0 {
		parts = append(parts, fmt.Sprintf("输入 %d Hz", s.InputSampleRate))
	}
	if s.Pricing.PerHour > 0 {
		parts = append(parts, fmt.Sprintf("估算每小时约 %.2f %s", s.Pricing.PerHour, s.Pricing.Currency))
	}
	return strings.Join(parts, "，")
}

func costNote(cost string) string {
	if cost == "" {
		return ""
	}
	return "，计费约 " + cost
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
