package dub

// Cue is a dubbed sentence: where it starts in the original and how long
// its Chinese audio runs.
type Cue struct {
	StartMs int64
	DubMs   int64
}

// Slot is where a cue lands on the output timeline. ShiftMs is how far the
// original from this sentence onward is pushed back by inserted silence.
type Slot struct {
	OutStartMs int64
	ShiftMs    int64
}

// Align places each cue at max(its original start, the previous Chinese
// end). When the Chinese runs long, the original waits at the start of the
// next sentence: silence is inserted there, and everything after it moves
// later by the same amount, so each sentence's original and its Chinese
// start together. cues must be sorted by StartMs.
func Align(cues []Cue) []Slot {
	slots := make([]Slot, len(cues))
	var shift, prevEnd int64
	for i, c := range cues {
		start := max(c.StartMs+shift, prevEnd)
		shift = start - c.StartMs
		slots[i] = Slot{OutStartMs: start, ShiftMs: shift}
		prevEnd = start + c.DubMs
	}
	return slots
}

// OutMs maps a time in the original to the output timeline.
func OutMs(cues []Cue, slots []Slot, ms int64) int64 {
	var shift int64
	for i, c := range cues {
		if c.StartMs > ms {
			break
		}
		shift = slots[i].ShiftMs
	}
	return ms + shift
}
