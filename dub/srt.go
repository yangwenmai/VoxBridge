package dub

import (
	"fmt"
	"strings"

	"github.com/yangwenmai/voxbridge/core"
)

// srt writes the bilingual subtitles on the output timeline: the Chinese
// over the source for dubbed lines, the source alone for the rest.
func srt(lines []core.DubLine, cues []Cue, slots []Slot, dubbed []int) string {
	slotOf := map[int]int{}
	for k, i := range dubbed {
		slotOf[i] = k
	}
	var b strings.Builder
	n := 0
	for i, l := range lines {
		var start, end int64
		text := l.Source
		if k, ok := slotOf[i]; ok {
			start = slots[k].OutStartMs
			end = start + cues[k].DubMs
			if l.EndMs != nil {
				end = max(end, OutMs(cues, slots, *l.EndMs))
			}
			text = l.Translation + "\n" + l.Source
		} else {
			start = OutMs(cues, slots, l.StartMs)
			end = start + lineLength(lines, i)
			if l.EndMs != nil {
				end = OutMs(cues, slots, *l.EndMs)
			}
		}
		if strings.TrimSpace(text) == "" || end <= start {
			continue
		}
		if i+1 < len(lines) {
			end = min(end, nextStart(lines, cues, slots, slotOf, i+1))
		}
		n++
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", n, stamp(start), stamp(max(end, start+1)), text)
	}
	return b.String()
}

func nextStart(lines []core.DubLine, cues []Cue, slots []Slot, slotOf map[int]int, i int) int64 {
	if k, ok := slotOf[i]; ok {
		return slots[k].OutStartMs
	}
	return OutMs(cues, slots, lines[i].StartMs)
}

func stamp(ms int64) string {
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}
