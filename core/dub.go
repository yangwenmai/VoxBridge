package core

import "context"

// Line is one sentence of the source, from platform subtitles or a Recognizer.
type Line struct {
	StartMs int64
	EndMs   *int64
	Speaker *string
	Text    string
}

// Audio is one synthesized sentence.
type Audio struct {
	Data       []byte
	Format     string
	SampleRate int
	DurationMs int64
}

type Recognizer interface {
	Transcribe(ctx context.Context, audio AudioInput, tasks RecognizeTasks) ([]Line, error)
}

// RecognizeTasks keeps a recognizer's submitted tasks in the dub's working
// directory, so an interrupted run waits on the tasks it already paid for
// instead of submitting them again. The format is the recognizer's own.
type RecognizeTasks interface {
	// Load returns nil when nothing was saved.
	Load() ([]byte, error)
	Save(state []byte) error
	Progress(format string, args ...any)
}

// Translator returns one translation per input text, in order.
type Translator interface {
	Translate(ctx context.Context, texts []string, to Lang) ([]string, error)
}

type Synthesizer interface {
	Synthesize(ctx context.Context, text, voice string) (Audio, error)
}

// DubManifest is the single source of truth for a prepared dub. It is
// written after every finished line so an interrupted run resumes without
// repeating requests.
type DubManifest struct {
	Version   int            `json:"version"`
	Source    ManifestSource `json:"source"`
	Target    Lang           `json:"target"`
	Providers DubProviders   `json:"providers"`
	// Voices maps a speaker label to a voice; nil when lines have no speakers.
	Voices map[string]string `json:"voices"`
	Lines  []DubLine         `json:"lines"`
}

const DubManifestVersion = 1

type ManifestSource struct {
	URL      string `json:"url"`
	Language Lang   `json:"language"`
}

type DubProviders struct {
	Recognizer  string `json:"recognizer,omitempty"`
	Translator  string `json:"translator"`
	Synthesizer string `json:"synthesizer"`
}

type DubLine struct {
	StartMs     int64   `json:"startMs"`
	EndMs       *int64  `json:"endMs"`
	Speaker     *string `json:"speaker"`
	Source      string  `json:"source"`
	Translation string  `json:"translation"`
	Skipped     bool    `json:"skipped,omitempty"`
	Voice       *string `json:"voice"`
	AudioFile   *string `json:"audioFile"`
	DubMs       *int64  `json:"dubMs"`
}
