package core

// Usage is what a provider has billed so far in this process: requests
// sent (retries included) and billable units per capability.
type Usage struct {
	Requests map[Capability]int     `json:"requests"`
	Units    map[Capability]float64 `json:"units"`
	// UnitNames says what Units counts, e.g. "token" or "字符".
	UnitNames map[Capability]string `json:"unitNames"`
	// Cost is Units at the provider's list price.
	Cost     float64 `json:"cost"`
	Currency string  `json:"currency"`
}

// Dubbing is the set of services a prepared dub needs from one provider.
// Recognizer is nil when the provider cannot transcribe.
type Dubbing struct {
	Recognizer  Recognizer
	Translator  Translator
	Synthesizer Synthesizer
	Usage       func() Usage
}

// Dubber is implemented by providers that can prepare a dub.
type Dubber interface {
	Dubbing(key string) Dubbing
}
