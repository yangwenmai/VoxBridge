// Package registry lists every built-in provider. A new provider is one
// package plus one line here.
package registry

import (
	"github.com/yangwenmai/voxbridge/core"
	"github.com/yangwenmai/voxbridge/provider/doubao"
	"github.com/yangwenmai/voxbridge/provider/gemini"
	"github.com/yangwenmai/voxbridge/provider/openai"
)

func All() []core.Provider {
	return []core.Provider{doubao.Provider{}, gemini.Provider{}, openai.Provider{}}
}

// KeyEnv maps each provider id to the environment variable of its key.
func KeyEnv() map[string]string {
	env := map[string]string{}
	for _, p := range All() {
		env[p.ID()] = p.KeyEnv()
	}
	return env
}
