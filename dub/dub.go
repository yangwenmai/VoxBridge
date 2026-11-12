// Package dub prepares a pre-dubbed episode: sentences, translations and
// one synthesized clip per sentence, kept in a resumable manifest, then
// mixed with the original into one audio file and a bilingual SRT.
package dub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/yangwenmai/voxbridge/core"
	"github.com/yangwenmai/voxbridge/media"
	"github.com/yangwenmai/voxbridge/transcript"
)

const (
	translateBatch = 16
	originalFile   = "original.flac"
	AudioFile      = "dub.m4a"
	SRTFile        = "dub.srt"
	linesDir       = "lines"
)

type Options struct {
	Dir    string
	Target core.Lang
	// ForceASR recognizes the audio even when the platform has subtitles.
	ForceASR       bool
	OriginalVolume float64
	Providers      core.DubProviders
	Dubbing        core.Dubbing
	// Voices are the synthesizer's voices in assignment order.
	Voices      []string
	Concurrency int
	Log         io.Writer
	// FetchTranscript and Original are replaced in tests.
	FetchTranscript func(context.Context, core.TranscriptRef) ([]core.Line, error)
	Original        func(ctx context.Context, in core.AudioInput, path string) error
	Decode          func(ctx context.Context, path string) ([]int16, error)
	Mix             bool
}

type Result struct {
	Dir, Audio, SRT, Manifest string
	Lines, Dubbed, Skipped    int
}

// Run prepares or resumes the dub of src in o.Dir. Every finished batch of
// translations and every synthesized line is saved before the next request,
// so after an interrupt the next Run only asks for what is missing.
func Run(ctx context.Context, src core.Source, o Options) (Result, error) {
	if src.Kind != core.SourceVOD {
		return Result{}, errors.New("预先配音只适用于点播；直播请等实时同传")
	}
	if len(o.Voices) == 0 {
		return Result{}, errors.New("合成服务没有登记音色")
	}
	if o.FetchTranscript == nil {
		o.FetchTranscript = transcript.Fetch
	}
	if o.Original == nil {
		o.Original = cacheOriginal
	}
	if o.Decode == nil {
		o.Decode = decodeClip
	}
	if o.Concurrency < 1 {
		o.Concurrency = 1
	}
	log := o.Log
	if log == nil {
		log = io.Discard
	}
	if err := os.MkdirAll(filepath.Join(o.Dir, linesDir), 0o755); err != nil {
		return Result{}, err
	}
	res := Result{Dir: o.Dir, Manifest: filepath.Join(o.Dir, manifestFile)}

	original := filepath.Join(o.Dir, originalFile)
	if _, err := os.Stat(original); err != nil {
		fmt.Fprintln(log, "下载原声…")
		if err := o.Original(ctx, src.Audio, original); err != nil {
			return res, fmt.Errorf("下载原声失败: %w", err)
		}
	}

	m, err := loadManifest(o.Dir)
	if err != nil {
		return res, err
	}
	if m != nil {
		if m.Source.URL != src.URL || m.Target != o.Target {
			return res, fmt.Errorf("%s 里是另一个链接或目标语言的配音（%s → %s），换个 --out 目录", o.Dir, m.Source.URL, m.Target)
		}
		fmt.Fprintf(log, "接着上次的进度做（%s）\n", res.Manifest)
	} else {
		if m, err = newManifest(ctx, src, o, original, log); err != nil {
			return res, err
		}
		if err := saveManifest(o.Dir, m); err != nil {
			return res, err
		}
	}

	if err := translate(ctx, m, o, log); err != nil {
		return res, err
	}
	if err := synthesize(ctx, m, o, log); err != nil {
		return res, err
	}

	var cues []Cue
	var paths []string
	var dubbed []int
	for i, l := range m.Lines {
		res.Lines++
		if l.Skipped {
			res.Skipped++
			continue
		}
		if l.AudioFile == nil || l.DubMs == nil {
			continue
		}
		cues = append(cues, Cue{StartMs: l.StartMs, DubMs: *l.DubMs})
		paths = append(paths, filepath.Join(o.Dir, *l.AudioFile))
		dubbed = append(dubbed, i)
	}
	res.Dubbed = len(cues)
	slots := Align(cues)
	res.SRT = filepath.Join(o.Dir, SRTFile)
	if err := writeAtomic(res.SRT, []byte(srt(m.Lines, cues, slots, dubbed))); err != nil {
		return res, err
	}
	if !o.Mix {
		return res, nil
	}
	fmt.Fprintln(log, "混音…")
	res.Audio = filepath.Join(o.Dir, AudioFile)
	if err := mix(ctx, original, res.Audio, cues, slots, paths, o.OriginalVolume); err != nil {
		return res, err
	}
	return res, nil
}

func newManifest(ctx context.Context, src core.Source, o Options, original string, log io.Writer) (*core.DubManifest, error) {
	var lines []core.Line
	providers := o.Providers
	if src.Transcript != nil && !o.ForceASR {
		fmt.Fprintf(log, "用平台字幕（%s，%s）\n", src.Transcript.Language, src.Transcript.Kind)
		var err error
		lines, err = o.FetchTranscript(ctx, *src.Transcript)
		if err != nil {
			return nil, err
		}
		providers.Recognizer = ""
	} else {
		if o.Dubbing.Recognizer == nil {
			return nil, errors.New("这个链接没有平台字幕，所选厂商也不能识别语音")
		}
		fmt.Fprintln(log, "没有平台字幕，识别语音…")
		var err error
		lines, err = o.Dubbing.Recognizer.Transcribe(ctx, core.AudioInput{URL: original})
		if err != nil {
			return nil, err
		}
	}
	if len(lines) == 0 {
		return nil, errors.New("没有拿到任何句子")
	}
	sort.SliceStable(lines, func(a, b int) bool { return lines[a].StartMs < lines[b].StartMs })
	lang := src.Language
	if src.Transcript != nil && !o.ForceASR {
		lang = src.Transcript.Language
	}
	m := &core.DubManifest{
		Version:   core.DubManifestVersion,
		Source:    core.ManifestSource{URL: src.URL, Language: lang},
		Target:    o.Target,
		Providers: providers,
	}
	for _, l := range lines {
		m.Lines = append(m.Lines, core.DubLine{
			StartMs: l.StartMs, EndMs: l.EndMs, Speaker: l.Speaker,
			Source: l.Text, Skipped: unspoken(l.Text),
		})
	}
	m.Voices = assignVoices(m.Lines, o.Voices)
	fmt.Fprintf(log, "共 %d 句", len(m.Lines))
	if m.Voices != nil {
		fmt.Fprintf(log, "，%d 位说话人各用一个音色", len(m.Voices))
	}
	fmt.Fprintln(log)
	return m, nil
}

func translate(ctx context.Context, m *core.DubManifest, o Options, log io.Writer) error {
	var pending []int
	for i, l := range m.Lines {
		if !l.Skipped && l.Translation == "" {
			pending = append(pending, i)
		}
	}
	for start := 0; start < len(pending); start += translateBatch {
		batch := pending[start:min(start+translateBatch, len(pending))]
		texts := make([]string, len(batch))
		for k, i := range batch {
			texts[k] = m.Lines[i].Source
		}
		out, err := o.Dubbing.Translator.Translate(ctx, texts, o.Target)
		if err != nil {
			return err
		}
		if len(out) != len(batch) {
			return fmt.Errorf("翻译返回了 %d 条，请求了 %d 条", len(out), len(batch))
		}
		for k, i := range batch {
			t := strings.TrimSpace(out[k])
			m.Lines[i].Translation = t
			if unspoken(t) {
				m.Lines[i].Skipped = true
			}
		}
		if err := saveManifest(o.Dir, m); err != nil {
			return err
		}
		fmt.Fprintf(log, "翻译 %d/%d\n", start+len(batch), len(pending))
	}
	return nil
}

func synthesize(ctx context.Context, m *core.DubManifest, o Options, log io.Writer) error {
	var pending []int
	for i, l := range m.Lines {
		if l.Skipped {
			continue
		}
		if l.AudioFile != nil && l.DubMs != nil {
			if _, err := os.Stat(filepath.Join(o.Dir, *l.AudioFile)); err == nil {
				continue
			}
		}
		pending = append(pending, i)
	}
	if len(pending) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var mu sync.Mutex
	var firstErr error
	done := 0
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		mu.Unlock()
	}
	var wg sync.WaitGroup
	for w := 0; w < o.Concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				mu.Lock()
				l := m.Lines[i]
				mu.Unlock()
				voice := voiceFor(m, l, o.Voices[0])
				audio, err := o.Dubbing.Synthesizer.Synthesize(ctx, l.Translation, voice)
				if err != nil {
					fail(err)
					continue
				}
				name := filepath.Join(linesDir, fmt.Sprintf("%04d.%s", i, audio.Format))
				path := filepath.Join(o.Dir, name)
				if err := writeAtomic(path, audio.Data); err != nil {
					fail(err)
					continue
				}
				// Audio already paid for is recorded even if Ctrl-C lands now.
				pcm, err := o.Decode(context.WithoutCancel(ctx), path)
				if err != nil {
					fail(fmt.Errorf("第 %d 句的配音解不开: %w", i+1, err))
					continue
				}
				dubMs := int64(len(pcm)) * 1000 / SampleRate
				mu.Lock()
				m.Lines[i].Voice, m.Lines[i].AudioFile, m.Lines[i].DubMs = &voice, &name, &dubMs
				err = saveManifest(o.Dir, m)
				done++
				n := done
				mu.Unlock()
				if err != nil {
					fail(err)
					continue
				}
				if n%10 == 0 || n == len(pending) {
					fmt.Fprintf(log, "合成 %d/%d\n", n, len(pending))
				}
			}
		}()
	}
	for _, i := range pending {
		if ctx.Err() != nil {
			break
		}
		select {
		case jobs <- i:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

// cacheOriginal saves the source audio as 24 kHz mono flac.
func cacheOriginal(ctx context.Context, in core.AudioInput, path string) error {
	tmp := path + ".partial.flac"
	args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}, media.HeaderArgs(in.Headers)...)
	args = append(args, "-i", in.URL, "-vn", "-ac", "1", "-ar", strconv.Itoa(SampleRate), "-c:a", "flac", tmp)
	if _, err := media.Run(ctx, "ffmpeg", args...); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
