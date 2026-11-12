package core

import (
	"context"
	"slices"
)

// Capability names one thing a provider can do.
type Capability string

const (
	CapInterpret  Capability = "interpret"
	CapRecognize  Capability = "recognize"
	CapTranslate  Capability = "translate"
	CapSynthesize Capability = "synthesize"
)

// AnyLang in LangRule.From means the service detects the source language.
const AnyLang Lang = "*"

// LangRule allows every pair in From x To. A service's pairs are the union
// of its rules.
type LangRule struct {
	From []Lang `json:"from"`
	To   []Lang `json:"to"`
}

type Voice struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Pricing is the list price from the provider's docs. PerHour is an
// estimate for one hour of English speech; Basis says how it was derived.
type Pricing struct {
	Unit     string  `json:"unit"`
	Currency string  `json:"currency"`
	PerHour  float64 `json:"perHour,omitempty"`
	Basis    string  `json:"basis"`
}

// Service describes one capability of one provider. Empty Voices means the
// service picks or clones the voice itself.
type Service struct {
	Capability Capability `json:"capability"`
	Model      string     `json:"model"`
	Languages  []LangRule `json:"languages"`
	Voices     []Voice    `json:"voices,omitempty"`
	// Subtitles is true when the service returns text alongside audio.
	Subtitles        bool    `json:"subtitles"`
	InputSampleRate  int     `json:"inputSampleRate,omitempty"`
	OutputSampleRate int     `json:"outputSampleRate,omitempty"`
	Pricing          Pricing `json:"pricing"`
	// MeasuredLagMs is the average lag of translated speech behind the
	// source as measured by this project; 0 until measured.
	MeasuredLagMs int `json:"measuredLagMs,omitempty"`
}

type Capabilities struct {
	Provider string    `json:"provider"`
	Services []Service `json:"services"`
}

// Service returns the provider's service for c, if it has one.
func (c Capabilities) Service(cap Capability) (Service, bool) {
	for _, s := range c.Services {
		if s.Capability == cap {
			return s, true
		}
	}
	return Service{}, false
}

// Supports reports whether the service accepts from -> to.
func (s Service) Supports(from, to Lang) bool {
	for _, r := range s.Languages {
		if (slices.Contains(r.From, AnyLang) || slices.Contains(r.From, from)) && slices.Contains(r.To, to) {
			return true
		}
	}
	return false
}

// Provider is one vendor. Each provider package registers one.
type Provider interface {
	ID() string
	Name() string
	Capabilities() Capabilities
	// KeyEnv is the environment variable holding the provider's API key.
	KeyEnv() string
	// Check sends the smallest real request per service and reports which
	// capabilities work with key. It must not retry.
	Check(ctx context.Context, key string) []CheckResult
}

// CheckResult is what `voxbridge doctor` prints for one service.
type CheckResult struct {
	Capability Capability `json:"capability"`
	Model      string     `json:"model"`
	OK         bool       `json:"ok"`
	// Scope says what the request actually proved, e.g. that the key was
	// accepted but no audio was sent.
	Scope string `json:"scope,omitempty"`
	Err   error  `json:"-"`
	// Cost is what the check request may bill, empty when free.
	Cost string `json:"cost,omitempty"`
}
