package core

import (
	"fmt"
	"strings"
)

// ErrorKind is how the user should react to a provider failure.
type ErrorKind string

const (
	// ErrBadKey: the key is wrong or revoked. Fix the key.
	ErrBadKey ErrorKind = "bad_key"
	// ErrNotEnabled: the key works but this service is not enabled for it,
	// or the key belongs to another project. Enable it in the console.
	ErrNotEnabled ErrorKind = "not_enabled"
	// ErrNotFound: the model or endpoint does not exist for this key.
	ErrNotFound ErrorKind = "not_found"
	// ErrBadRequest: the request was rejected, e.g. unsupported language pair.
	ErrBadRequest ErrorKind = "bad_request"
	// ErrRateLimited: concurrency or quota limit; retry later.
	ErrRateLimited ErrorKind = "rate_limited"
	// ErrServer: a transient server or network fault; retry.
	ErrServer ErrorKind = "server"
)

// Retryable reports whether backing off and retrying can succeed.
func (k ErrorKind) Retryable() bool { return k == ErrRateLimited || k == ErrServer }

// ProviderError is a classified failure from a provider. Message never
// contains the API key; providers scrub it before building the error.
type ProviderError struct {
	Provider string
	Service  string
	Kind     ErrorKind
	HTTP     int
	Code     string
	Message  string
	// LogID is the provider's request id for support tickets.
	LogID string
	Hint  string
}

func (e *ProviderError) Error() string {
	var parts []string
	if e.HTTP != 0 {
		parts = append(parts, fmt.Sprintf("HTTP %d", e.HTTP))
	}
	if e.Code != "" {
		parts = append(parts, e.Code)
	}
	if e.Message != "" {
		parts = append(parts, e.Message)
	}
	if e.LogID != "" {
		parts = append(parts, "logid "+e.LogID)
	}
	return fmt.Sprintf("%s %s: %s (%s)", e.Provider, e.Service, e.Kind, strings.Join(parts, ", "))
}

// Scrub removes every occurrence of secret from s.
func Scrub(s, secret string) string {
	if len(secret) < 4 {
		return s
	}
	return strings.ReplaceAll(s, secret, "[REDACTED]")
}
