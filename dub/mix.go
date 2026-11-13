package dub

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"

	"github.com/yangwenmai/voxbridge/media"
)

// SampleRate is the rate of the cached original and of the mix.
const SampleRate = 24000

func msToSamples(ms int64) int64 { return ms * SampleRate / 1000 }

// decodeClip reads a dubbed line as 24 kHz mono samples.
func decodeClip(ctx context.Context, path string) ([]int16, error) {
	out, err := media.Run(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin",
		"-i", path, "-f", "s16le", "-ac", "1", "-ar", strconv.Itoa(SampleRate), "-")
	if err != nil {
		return nil, err
	}
	pcm := make([]int16, len(out)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(out[2*i:]))
	}
	return pcm, nil
}

type clip struct {
	start int64 // output sample
	path  string
}

// mixer sums the shifted original, scaled by volume, with the dubbed clips
// and writes s16le samples. Clips never overlap each other.
type mixer struct {
	ctx    context.Context
	w      *bufio.Writer
	volume float64
	clips  []clip
	pos    int64
	next   int
	cur    []int16
	curAt  int64
}

func (m *mixer) emit(orig []int16) error {
	acc := make([]float64, len(orig))
	for i, s := range orig {
		acc[i] = float64(s) * m.volume
	}
	end := m.pos + int64(len(orig))
	for {
		if m.cur == nil {
			if m.next >= len(m.clips) || m.clips[m.next].start >= end {
				break
			}
			pcm, err := decodeClip(m.ctx, m.clips[m.next].path)
			if err != nil {
				return err
			}
			m.cur, m.curAt = pcm, m.clips[m.next].start
			m.next++
		}
		from := max(m.curAt, m.pos)
		to := min(m.curAt+int64(len(m.cur)), end)
		for t := from; t < to; t++ {
			acc[t-m.pos] += float64(m.cur[t-m.curAt])
		}
		if m.curAt+int64(len(m.cur)) > end {
			break
		}
		m.cur = nil
	}
	var b [2]byte
	for _, v := range acc {
		binary.LittleEndian.PutUint16(b[:], uint16(int16(math.Max(-32768, math.Min(32767, v)))))
		if _, err := m.w.Write(b[:]); err != nil {
			return err
		}
	}
	m.pos = end
	return nil
}

func (m *mixer) silence(n int64) error {
	const chunk = SampleRate
	zeros := make([]int16, chunk)
	for n > 0 {
		k := min(n, chunk)
		if err := m.emit(zeros[:k]); err != nil {
			return err
		}
		n -= k
	}
	return nil
}

// tail plays out whatever clip is still sounding after the original ends.
func (m *mixer) tail() error {
	var last int64
	if m.cur != nil {
		last = m.curAt + int64(len(m.cur))
	}
	for _, c := range m.clips[m.next:] {
		last = max(last, c.start+1)
	}
	if last <= m.pos {
		return nil
	}
	if err := m.silence(last - m.pos); err != nil {
		return err
	}
	if m.cur != nil || m.next < len(m.clips) {
		return m.tail()
	}
	return nil
}

// mix writes the dubbed episode to out: the original from originalPath
// with silence inserted per slots, and each clip at its slot.
func mix(ctx context.Context, originalPath, out string, cues []Cue, slots []Slot, paths []string, volume float64) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	dec := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin",
		"-i", originalPath, "-f", "s16le", "-ac", "1", "-ar", strconv.Itoa(SampleRate), "-")
	src, err := dec.StdoutPipe()
	if err != nil {
		return err
	}
	tmp := out + ".partial.m4a"
	enc := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "s16le", "-ac", "1", "-ar", strconv.Itoa(SampleRate), "-i", "-",
		"-c:a", "aac", "-b:a", "96k", tmp)
	sink, err := enc.StdinPipe()
	if err != nil {
		return err
	}
	var encErr, decErr capture
	enc.Stderr, dec.Stderr = &encErr, &decErr
	if err := dec.Start(); err != nil {
		return err
	}
	if err := enc.Start(); err != nil {
		return err
	}

	m := &mixer{ctx: ctx, w: bufio.NewWriterSize(sink, 1<<16), volume: volume}
	for i, s := range slots {
		m.clips = append(m.clips, clip{start: msToSamples(s.OutStartMs), path: paths[i]})
	}
	r := bufio.NewReaderSize(src, 1<<16)
	var origPos int64 // samples of the original consumed
	copyUntil := func(limit int64) (bool, error) {
		buf := make([]byte, 2*SampleRate)
		for limit < 0 || origPos < limit {
			want := int64(len(buf))
			if limit >= 0 {
				want = min(want, 2*(limit-origPos))
			}
			n, err := io.ReadFull(r, buf[:want])
			n -= n % 2
			if n > 0 {
				pcm := make([]int16, n/2)
				for i := range pcm {
					pcm[i] = int16(binary.LittleEndian.Uint16(buf[2*i:]))
				}
				if err := m.emit(pcm); err != nil {
					return false, err
				}
				origPos += int64(n / 2)
			}
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return true, nil
			}
			if err != nil {
				return false, err
			}
		}
		return false, nil
	}
	var prevShift int64
	done := false
	for i, c := range cues {
		if !done {
			if done, err = copyUntil(msToSamples(c.StartMs)); err != nil {
				return err
			}
		}
		if gap := slots[i].ShiftMs - prevShift; gap > 0 {
			if err := m.silence(msToSamples(gap)); err != nil {
				return err
			}
		}
		prevShift = slots[i].ShiftMs
	}
	if !done {
		if _, err := copyUntil(-1); err != nil {
			return err
		}
	}
	if err := m.tail(); err != nil {
		return err
	}
	if err := m.w.Flush(); err != nil {
		return err
	}
	sink.Close()
	if err := dec.Wait(); err != nil {
		return fmt.Errorf("ffmpeg 解码原声失败: %s", decErr.String())
	}
	if err := enc.Wait(); err != nil {
		return fmt.Errorf("ffmpeg 编码输出失败: %s", encErr.String())
	}
	return os.Rename(tmp, out)
}

type capture struct{ b []byte }

func (c *capture) Write(p []byte) (int, error) {
	if len(c.b) < 4096 {
		c.b = append(c.b, p...)
	}
	return len(p), nil
}

func (c *capture) String() string { return string(c.b) }
