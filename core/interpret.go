package core

import "context"

// InputSampleRate is the rate of the mono 16-bit PCM the pipeline sends to
// every InterpretSession. Adapters resample when their service wants another.
const InputSampleRate = 16000

// InterpretConfig opens one simultaneous interpretation session.
type InterpretConfig struct {
	From  Lang
	To    Lang
	Voice string
}

// Interpreter turns speech into translated speech as it arrives.
type Interpreter interface {
	ID() string
	Open(ctx context.Context, cfg InterpretConfig) (InterpretSession, error)
}

// InterpretSession is one connection. Pausing, seeking or stopping closes it;
// resuming opens a new one, because services bill input and output time.
type InterpretSession interface {
	// Send takes mono 16-bit PCM at InputSampleRate, fed at 1x speed.
	Send(pcm []int16) error
	// Events is closed after an EndEvent or a non-retryable ErrorEvent.
	Events() <-chan InterpretEvent
	Close() error
}

// InterpretEvent is one of AudioEvent, SubtitleEvent, ErrorEvent, EndEvent.
type InterpretEvent interface{ interpretEvent() }

type AudioEvent struct {
	PCM        []int16
	SampleRate int
}

type SubtitleEvent struct {
	Text  string
	Final bool
}

type ErrorEvent struct {
	Code      string
	Retryable bool
	Message   string
}

type EndEvent struct{}

func (AudioEvent) interpretEvent()    {}
func (SubtitleEvent) interpretEvent() {}
func (ErrorEvent) interpretEvent()    {}
func (EndEvent) interpretEvent()      {}
