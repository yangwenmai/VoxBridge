// Package transcript turns platform subtitles into sentence lines.
package transcript

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yangwenmai/voxbridge/core"
)

// Fetch downloads and parses a platform subtitle track into sentences.
func Fetch(ctx context.Context, ref core.TranscriptRef) ([]core.Line, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref.URL, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载字幕失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载字幕失败: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	cues, err := Parse(ref.Format, data)
	if err != nil {
		return nil, err
	}
	return Sentences(cues), nil
}

// Parse reads vtt, srt or json3 into cues in time order.
func Parse(format string, data []byte) ([]core.Line, error) {
	switch format {
	case "vtt", "srt":
		return parseBlocks(string(data)), nil
	case "json3":
		return parseJSON3(data)
	}
	return nil, fmt.Errorf("不支持的字幕格式 %q", format)
}

var (
	voiceTag   = regexp.MustCompile(`<v(?:\.[^ >]*)?\s+([^>]+)>`)
	anyTag     = regexp.MustCompile(`<[^>]*>`)
	namePrefix = regexp.MustCompile(`^([A-Z][\w.'-]*(?: [A-Z][\w.'-]*){0,2}):\s+`)
	spaces     = regexp.MustCompile(`\s+`)
	// TED-style credit cues ("Transcriber: … Reviewer: …") are not speech.
	credit = regexp.MustCompile(`^(?:Transcriber|Translator|Reviewer):`)
)

func parseBlocks(s string) []core.Line {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	var cues []core.Line
	for _, block := range strings.Split(s, "\n\n") {
		rows := strings.Split(strings.TrimSpace(block), "\n")
		timing := -1
		for i, r := range rows {
			if strings.Contains(r, "-->") {
				timing = i
				break
			}
		}
		if timing < 0 {
			continue
		}
		from, to, ok := strings.Cut(rows[timing], "-->")
		if !ok {
			continue
		}
		start, err1 := parseTime(from)
		end, err2 := parseTime(strings.Fields(to)[0])
		if err1 != nil || err2 != nil {
			continue
		}
		raw := strings.Join(rows[timing+1:], " ")
		var speaker *string
		if m := voiceTag.FindStringSubmatch(raw); m != nil {
			name := strings.TrimSpace(m[1])
			speaker = &name
		}
		text := cleanText(anyTag.ReplaceAllString(raw, ""))
		if credit.MatchString(text) {
			continue
		}
		if speaker == nil {
			if m := namePrefix.FindStringSubmatch(text); m != nil {
				name := m[1]
				speaker = &name
				text = strings.TrimSpace(text[len(m[0]):])
			}
		}
		if text == "" {
			continue
		}
		e := end
		cues = append(cues, core.Line{StartMs: start, EndMs: &e, Speaker: speaker, Text: text})
	}
	return cues
}

func cleanText(s string) string {
	return strings.TrimSpace(spaces.ReplaceAllString(html.UnescapeString(s), " "))
}

// parseTime reads hh:mm:ss.mmm, mm:ss.mmm or the srt form with a comma.
func parseTime(s string) (int64, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", ".")
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("bad time %q", s)
	}
	var ms float64
	for _, p := range parts[:len(parts)-1] {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0, err
		}
		ms = ms*60 + float64(n)
	}
	sec, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil {
		return 0, err
	}
	return int64((ms*60+sec)*1000 + 0.5), nil
}

func parseJSON3(data []byte) ([]core.Line, error) {
	var doc struct {
		Events []struct {
			TStartMs    int64 `json:"tStartMs"`
			DDurationMs int64 `json:"dDurationMs"`
			AAppend     int   `json:"aAppend"`
			Segs        []struct {
				UTF8 string `json:"utf8"`
			} `json:"segs"`
		} `json:"events"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("json3 字幕看不懂: %w", err)
	}
	var cues []core.Line
	for _, ev := range doc.Events {
		if ev.AAppend == 1 || len(ev.Segs) == 0 {
			continue
		}
		var b strings.Builder
		for _, s := range ev.Segs {
			b.WriteString(s.UTF8)
		}
		text := cleanText(b.String())
		if text == "" {
			continue
		}
		end := ev.TStartMs + ev.DDurationMs
		cues = append(cues, core.Line{StartMs: ev.TStartMs, EndMs: &end, Text: text})
	}
	// Auto captions overlap: each event ends when the next one starts.
	for i := 0; i+1 < len(cues); i++ {
		if *cues[i].EndMs > cues[i+1].StartMs {
			e := cues[i+1].StartMs
			cues[i].EndMs = &e
		}
	}
	return cues, nil
}

const (
	maxGapMs           = 1500
	maxSentenceMs      = 20000
	unpunctuatedGapMs  = 700
	unpunctuatedMaxMs  = 8000
	maxSentenceRunes   = 400
	punctuatedCueShare = 0.1
)

var sentenceEnd = regexp.MustCompile(`[.?!。？！…]["'”’)\]]*$`)

// Sentences joins caption cues into sentences: a cue ending in terminal
// punctuation closes one, as do a speaker change and a long pause.
// Captions without punctuation (most auto captions) split on shorter
// pauses and a shorter maximum length instead.
func Sentences(cues []core.Line) []core.Line {
	punctuated := 0
	for _, c := range cues {
		if sentenceEnd.MatchString(c.Text) {
			punctuated++
		}
	}
	gap, maxLen := int64(maxGapMs), int64(maxSentenceMs)
	usePunct := len(cues) > 0 && float64(punctuated)/float64(len(cues)) >= punctuatedCueShare
	if !usePunct {
		gap, maxLen = unpunctuatedGapMs, unpunctuatedMaxMs
	}
	var out []core.Line
	var cur *core.Line
	flush := func() {
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}
	for _, c := range cues {
		if cur != nil {
			speakerChange := c.Speaker != nil && (cur.Speaker == nil || *cur.Speaker != *c.Speaker)
			paused := cur.EndMs != nil && c.StartMs-*cur.EndMs > gap
			long := c.StartMs-cur.StartMs > maxLen || len([]rune(cur.Text)) > maxSentenceRunes
			if speakerChange || paused || long {
				flush()
			}
		}
		if cur == nil {
			line := c
			cur = &line
		} else {
			cur.Text += " " + c.Text
			cur.EndMs = c.EndMs
		}
		if usePunct && sentenceEnd.MatchString(c.Text) {
			flush()
		}
	}
	flush()
	return out
}
