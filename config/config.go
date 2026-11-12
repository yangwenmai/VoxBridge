// Package config reads API keys from the environment or
// ~/.config/voxbridge/config.json. Keys never leave this process except in
// the request headers of the provider they belong to.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Key is an API key. It formats as "[REDACTED]" so it cannot reach logs.
type Key string

func (Key) String() string               { return "[REDACTED]" }
func (Key) GoString() string             { return "[REDACTED]" }
func (Key) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }
func (k Key) Reveal() string             { return string(k) }

// Origin says where a key came from, for doctor output.
type Origin string

const (
	FromEnv  Origin = "env"
	FromFile Origin = "file"
)

type Entry struct {
	Key    Key
	Origin Origin
	// Source names the variable or file the key came from.
	Source string
}

// File is the shape of config.json.
//
//	{"providers": {"doubao": {"apiKey": "..."}, "gemini": {"apiKey": "..."}}}
type File struct {
	Providers map[string]struct {
		APIKey string `json:"apiKey"`
	} `json:"providers"`
}

// Path is ~/.config/voxbridge/config.json, or $XDG_CONFIG_HOME/voxbridge.
func Path() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "voxbridge", "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "voxbridge", "config.json"), nil
}

// Keys resolves a key per provider id. env maps a provider id to its
// variable; the environment wins over the file. A missing file is fine.
func Keys(env map[string]string, getenv func(string) string, path string) (map[string]Entry, error) {
	keys := map[string]Entry{}
	file, err := readFile(path)
	if err != nil {
		return nil, err
	}
	for id, p := range file.Providers {
		if k := strings.TrimSpace(p.APIKey); k != "" {
			keys[id] = Entry{Key: Key(k), Origin: FromFile, Source: path}
		}
	}
	for id, name := range env {
		if k := strings.TrimSpace(getenv(name)); k != "" {
			keys[id] = Entry{Key: Key(k), Origin: FromEnv, Source: name}
		}
	}
	return keys, nil
}

func readFile(path string) (File, error) {
	var f File
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	return f, nil
}
