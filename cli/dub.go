package cli

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yangwenmai/voxbridge/config"
	"github.com/yangwenmai/voxbridge/core"
	"github.com/yangwenmai/voxbridge/dub"
	"github.com/yangwenmai/voxbridge/media"
	"github.com/yangwenmai/voxbridge/provider/registry"
	"github.com/yangwenmai/voxbridge/source"
)

const dubTarget core.Lang = "zh"

func dubCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dub", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "")
	asr := fs.Bool("asr", false, "")
	volume := fs.Float64("original-volume", 0.25, "")
	url, rest := splitURL(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if url == "" && fs.NArg() > 0 {
		url = fs.Arg(0)
	}
	if url == "" {
		fmt.Fprintln(stderr, "用法: voxbridge dub <链接> [--out 目录] [--asr] [--original-volume 0.25]")
		return 2
	}
	if *volume < 0 || *volume > 1 {
		fmt.Fprintln(stderr, "--original-volume 要在 0 到 1 之间")
		return 2
	}
	if err := requireTools(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
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

	fmt.Fprintln(stdout, "解析链接…")
	src, err := resolve(ctx, url)
	if err != nil {
		var se *source.Error
		if errors.As(err, &se) {
			fmt.Fprintf(stderr, "无法使用这个链接 [%s]：%v\n", se.Kind, se)
		} else {
			fmt.Fprintf(stderr, "无法使用这个链接：%v\n", err)
		}
		return 1
	}
	if src.Kind != core.SourceVOD {
		fmt.Fprintln(stderr, "这是直播。预先配音只适用于点播。")
		return 1
	}
	fmt.Fprintf(stdout, "%s（%s）\n", src.Title, clock(src.DurationMs))

	needASR := *asr || src.Transcript == nil
	from := src.Language
	if src.Transcript != nil && !*asr {
		from = src.Transcript.Language
	}
	p, svc, why := pickDubber(keys, from, needASR)
	if p == nil {
		fmt.Fprintln(stderr, why)
		return 1
	}
	dir := *out
	if dir == "" {
		sum := sha1.Sum([]byte(src.URL))
		dir = filepath.Join("voxbridge-dubs", hex.EncodeToString(sum[:])[:10])
	}
	hours := float64(src.DurationMs) / 3600000

	// Remote recognition uploads the whole episode and bills by the hour,
	// so it only runs on --asr or an explicit yes. A directory that already
	// holds a manifest has its sentences and needs no recognition.
	_, statErr := os.Stat(filepath.Join(dir, "manifest.json"))
	if src.Transcript == nil && !*asr && statErr != nil {
		asrCost := svc.asrPerHour * hours
		if !interactive() {
			fmt.Fprintf(stderr, "这个链接没有平台字幕，配音要先用%s的语音识别（%s）把整集音频上传识别，约 %.2f 元。\n"+
				"为避免意外扣费，不会自动识别。确认要识别，请加 --asr 重跑：\n  voxbridge dub %q --asr\n",
				p.Name(), svc.asrModel, asrCost, url)
			return 1
		}
		fmt.Fprintf(stdout, "\n这个链接没有平台字幕，配音要先把整集音频上传做语音识别。\n"+
			"  时长  %s\n  识别  %s·语音识别（%s）\n  预计  识别约 %.2f 元，整集合计约 %.2f 元（按公开价估算，记在你自己的账上）\n"+
			"要上传音频做语音识别吗？[y/N] ", clock(src.DurationMs), p.Name(), svc.asrModel, asrCost, svc.perHour*hours)
		if !confirmed(stdin) {
			fmt.Fprintln(stdout, "已取消，没有发出任何请求。")
			return 1
		}
	}

	dubber := p.(core.Dubber)
	d := dubbingFor(dubber, keys[p.ID()].Key.Reveal())
	if !needASR {
		d.Recognizer = nil
	}
	fmt.Fprintf(stdout, "用 %s：%s\n", p.Name(), strings.Join(svc.names, " + "))
	fmt.Fprintf(stdout, "预计费用约 %.1f 元（按公开价估算，记在你自己的账上；已做完的部分不会再计费）\n", svc.perHour*hours)
	fmt.Fprintf(stdout, "工作目录 %s\n", dir)

	providers := core.DubProviders{Translator: p.ID(), Synthesizer: p.ID()}
	if needASR {
		providers.Recognizer = p.ID()
	}
	res, err := dub.Run(ctx, src, dub.Options{
		Dir: dir, Target: dubTarget, ForceASR: *asr, OriginalVolume: *volume,
		Providers: providers, Dubbing: d, Voices: svc.voices, Concurrency: 2,
		Log: stdout, Mix: true,
	})
	printUsage(stdout, d.Usage())
	if err != nil {
		if ctx.Err() != nil {
			fmt.Fprintf(stderr, "已中断。做完的句子都存在 %s，再跑一次同样的命令会接着做。\n", filepath.Join(dir, "manifest.json"))
			return 130
		}
		fmt.Fprintf(stderr, "配音没做完：%s\n已做完的留在 %s，修好后再跑一次同样的命令会接着做。\n", describeErr(err), dir)
		return 1
	}
	fmt.Fprintf(stdout, "\n完成：%d 句，配了 %d 句，跳过 %d 句\n", res.Lines, res.Dubbed, res.Skipped)
	fmt.Fprintf(stdout, "  音频  %s\n  字幕  %s\n", res.Audio, res.SRT)
	fmt.Fprintf(stdout, "试听：afplay %q\n", res.Audio)
	return 0
}

type dubServices struct {
	names      []string
	voices     []string
	perHour    float64
	asrModel   string
	asrPerHour float64
}

// Replaced in tests.
var (
	resolve                = source.Resolve
	requireTools           = func() error { return media.Require("ffmpeg", "ffprobe", "yt-dlp") }
	dubbingFor             = func(d core.Dubber, key string) core.Dubbing { return d.Dubbing(key) }
	stdin        io.Reader = os.Stdin
	interactive            = func() bool { return isTerminal(os.Stdin) }
)

// confirmed reads one answer; only y or yes counts, so Enter means no.
func confirmed(r io.Reader) bool {
	line, _ := bufio.NewReader(r).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// pickDubber returns the first configured provider whose capability table
// covers every step this source needs.
func pickDubber(keys map[string]config.Entry, from core.Lang, needASR bool) (core.Provider, dubServices, string) {
	caps := []core.Capability{core.CapTranslate, core.CapSynthesize}
	if needASR {
		caps = append([]core.Capability{core.CapRecognize}, caps...)
	}
	var missing []string
	for _, p := range registry.All() {
		if _, ok := p.(core.Dubber); !ok {
			continue
		}
		if _, ok := keys[p.ID()]; !ok {
			missing = append(missing, p.KeyEnv())
			continue
		}
		var s dubServices
		ok := true
		for _, c := range caps {
			svc, found := p.Capabilities().Service(c)
			if !found {
				ok = false
				break
			}
			switch c {
			case core.CapRecognize:
				ok = from == "" || svc.Supports(baseLang(from), baseLang(from))
				s.asrModel, s.asrPerHour = svc.Model, svc.Pricing.PerHour
			case core.CapTranslate:
				ok = from == "" || svc.Supports(from, dubTarget)
			case core.CapSynthesize:
				ok = svc.Supports(core.AnyLang, dubTarget) || svc.Supports(dubTarget, dubTarget)
				for _, v := range svc.Voices {
					s.voices = append(s.voices, v.ID)
				}
			}
			if !ok {
				break
			}
			s.names = append(s.names, capabilityLabel(c)+"（"+svc.Model+"）")
			s.perHour += svc.Pricing.PerHour
		}
		if ok {
			return p, s, ""
		}
	}
	if len(missing) > 0 {
		return nil, dubServices{}, fmt.Sprintf("没有能做预先配音的厂商。设置 %s（或写进 config.json）后再试；voxbridge doctor 可以检查。", strings.Join(missing, " 或 "))
	}
	if needASR {
		return nil, dubServices{}, "这个链接没有平台字幕，已配置的厂商里没有能识别语音再翻译成中文的。"
	}
	return nil, dubServices{}, fmt.Sprintf("已配置的厂商不支持把 %s 翻译成中文。", from)
}

func baseLang(l core.Lang) core.Lang {
	b, _, _ := strings.Cut(string(l), "-")
	return core.Lang(b)
}

func printUsage(w io.Writer, u core.Usage) {
	if len(u.Requests) == 0 {
		fmt.Fprintln(w, "\n本次没有发出任何请求，费用 0 元")
		return
	}
	caps := make([]core.Capability, 0, len(u.Requests))
	for c := range u.Requests {
		caps = append(caps, c)
	}
	sort.Slice(caps, func(a, b int) bool { return caps[a] < caps[b] })
	fmt.Fprintln(w, "\n本次请求")
	for _, c := range caps {
		fmt.Fprintf(w, "  %s  %d 次请求，%s %s\n", capabilityLabel(c), u.Requests[c], trimFloat(u.Units[c]), u.UnitNames[c])
	}
	fmt.Fprintf(w, "  合计约 %.4f 元（按公开价估算）\n", u.Cost)
}

func trimFloat(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%.1f", f)
}

func clock(ms int64) string {
	s := ms / 1000
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
