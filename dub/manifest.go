package dub

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/yangwenmai/voxbridge/core"
)

const manifestFile = "manifest.json"

func loadManifest(dir string) (*core.DubManifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m core.DubManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s 看不懂（%v）；删掉它会从头再做", filepath.Join(dir, manifestFile), err)
	}
	if m.Version != core.DubManifestVersion {
		return nil, fmt.Errorf("%s 是第 %d 版格式，这个版本只认第 %d 版", filepath.Join(dir, manifestFile), m.Version, core.DubManifestVersion)
	}
	return &m, nil
}

// saveManifest replaces the manifest atomically so an interrupt never
// leaves half a file.
func saveManifest(dir string, m *core.DubManifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, manifestFile), append(data, '\n'))
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Speaker labels count when this share of lines carries one.
const speakerShare = 0.8

// assignVoices gives speakers voices in order of total talk time. Lines
// without labels, or too few labelled lines, all use the first voice.
func assignVoices(lines []core.DubLine, voices []string) map[string]string {
	if len(voices) == 0 || len(lines) == 0 {
		return nil
	}
	talk := map[string]int64{}
	labelled := 0
	for i, l := range lines {
		if l.Speaker == nil || *l.Speaker == "" {
			continue
		}
		labelled++
		talk[*l.Speaker] += lineLength(lines, i)
	}
	if float64(labelled) < speakerShare*float64(len(lines)) {
		return nil
	}
	speakers := make([]string, 0, len(talk))
	for s := range talk {
		speakers = append(speakers, s)
	}
	sort.Slice(speakers, func(a, b int) bool {
		if talk[speakers[a]] != talk[speakers[b]] {
			return talk[speakers[a]] > talk[speakers[b]]
		}
		return speakers[a] < speakers[b]
	})
	m := make(map[string]string, len(speakers))
	for i, s := range speakers {
		m[s] = voices[i%len(voices)]
	}
	return m
}

func lineLength(lines []core.DubLine, i int) int64 {
	if e := lines[i].EndMs; e != nil {
		return *e - lines[i].StartMs
	}
	if i+1 < len(lines) {
		return lines[i+1].StartMs - lines[i].StartMs
	}
	return 0
}

func voiceFor(m *core.DubManifest, l core.DubLine, fallback string) string {
	if l.Speaker != nil {
		if v, ok := m.Voices[*l.Speaker]; ok {
			return v
		}
	}
	return fallback
}

// Sound cues such as "[Music]", "(Applause)" or "♪" are not spoken.
var soundOnly = regexp.MustCompile(`^(?:\s*(?:\[[^\]]*\]|\([^)]*\)|[♪♫♬~\-–—.,!?…]+))*\s*$`)

func unspoken(text string) bool {
	return strings.TrimSpace(text) == "" || soundOnly.MatchString(text)
}
