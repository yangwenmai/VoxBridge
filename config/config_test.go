package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvironmentWinsOverFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"providers":{"doubao":{"apiKey":"file-doubao"},"gemini":{"apiKey":"file-gemini"}}}`), 0o600)
	env := map[string]string{"DOUBAO_API_KEY": "env-doubao"}
	keys, err := Keys(map[string]string{"doubao": "DOUBAO_API_KEY", "gemini": "GEMINI_API_KEY", "openai": "OPENAI_API_KEY"},
		func(k string) string { return env[k] }, path)
	if err != nil {
		t.Fatal(err)
	}
	if keys["doubao"].Key.Reveal() != "env-doubao" || keys["doubao"].Origin != FromEnv {
		t.Errorf("doubao = %+v", keys["doubao"])
	}
	if keys["gemini"].Key.Reveal() != "file-gemini" || keys["gemini"].Origin != FromFile {
		t.Errorf("gemini = %+v", keys["gemini"])
	}
	if _, ok := keys["openai"]; ok {
		t.Error("openai has a key")
	}
}

func TestMissingFileIsFine(t *testing.T) {
	keys, err := Keys(map[string]string{"openai": "OPENAI_API_KEY"}, func(string) string { return "" }, filepath.Join(t.TempDir(), "nope.json"))
	if err != nil || len(keys) != 0 {
		t.Fatalf("keys %v err %v", keys, err)
	}
}

func TestKeyNeverFormats(t *testing.T) {
	e := Entry{Key: "sk-secret-value"}
	js, _ := json.Marshal(e)
	for _, s := range []string{fmt.Sprint(e), fmt.Sprintf("%v %+v %#v %s", e, e, e, e.Key), string(js)} {
		if strings.Contains(s, "secret") {
			t.Fatalf("key leaked: %s", s)
		}
	}
}
