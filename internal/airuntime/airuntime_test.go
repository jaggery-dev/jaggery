package airuntime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseOllamaTags(t *testing.T) {
	body := []byte(`{"models":[{"name":"llama3.2:3b"},{"name":"qwen3:14b"}]}`)
	models, ok := parseOllamaTags(body)
	if !ok {
		t.Fatal("valid Ollama tags should parse")
	}
	if len(models) != 2 || models[0].Name != "llama3.2:3b" {
		t.Fatalf("got %+v", models)
	}
}

func TestParseOllamaLoaded(t *testing.T) {
	loaded := parseOllamaLoaded([]byte(`{"models":[{"name":"llama3.2:3b","size_vram":2738880512}]}`))
	vram, ok := loaded["llama3.2:3b"]
	if !ok || vram != 2738880512 {
		t.Errorf("llama3.2:3b: loaded=%v vram=%d, want loaded with 2738880512", ok, vram)
	}
	if _, ok := loaded["qwen3:14b"]; ok {
		t.Errorf("loaded set wrong: %v", loaded)
	}
}

func TestParseOpenAIModels(t *testing.T) {
	body := []byte(`{"object":"list","data":[{"id":"gpt-oss-20b","object":"model"}]}`)
	models, ok := parseOpenAIModels(body)
	if !ok {
		t.Fatal("valid OpenAI model list should parse")
	}
	if len(models) != 1 || models[0].Name != "gpt-oss-20b" {
		t.Fatalf("got %+v", models)
	}
}

// The defensive case: a 200 from some other app on the port must not be
// mistaken for a runtime.
func TestParseOpenAIModelsRejectsForeignJSON(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`{"status":"UP"}`),    // a Spring actuator
		[]byte(`<html>hello</html>`), // a web page
		[]byte(`{"object":"error"}`), // wrong object
		[]byte(`{"object":"list"}`),  // list but no data
		[]byte(``),                   // empty
	} {
		if _, ok := parseOpenAIModels(body); ok {
			t.Errorf("foreign body should not parse as a runtime: %q", body)
		}
	}
}

func TestParseOllamaTagsDefensive(t *testing.T) {
	// A foreign 200 without a "models" key must not be mistaken for Ollama.
	if _, ok := parseOllamaTags([]byte(`{"status":"UP"}`)); ok {
		t.Error("a body with no models key must not parse as Ollama")
	}
	if _, ok := parseOllamaTags([]byte(`not json`)); ok {
		t.Error("non-JSON must not parse")
	}
	// Ollama with zero models pulled is valid and returns {"models":[]}.
	if models, ok := parseOllamaTags([]byte(`{"models":[]}`)); !ok || len(models) != 0 {
		t.Errorf("empty-but-present models should parse as empty Ollama, got ok=%v models=%+v", ok, models)
	}
}

// Detect against a fake Ollama server proves the end-to-end probe + merge of
// loaded state.
func TestDetectAgainstFakeOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Write([]byte(`{"models":[{"name":"llama3.2:3b"},{"name":"qwen3:14b"}]}`))
		case "/api/ps":
			w.Write([]byte(`{"models":[{"name":"qwen3:14b"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	models, ok := detectOllama(context.Background(), srv.Client(), srv.URL)
	if !ok {
		t.Fatal("fake Ollama should be detected")
	}
	loaded := map[string]bool{}
	for _, m := range models {
		loaded[m.Name] = m.Loaded
	}
	if loaded["qwen3:14b"] != true || loaded["llama3.2:3b"] != false {
		t.Errorf("loaded flags wrong: %+v", models)
	}
}

func TestSummary(t *testing.T) {
	if s := Summary(nil); s != "no AI runtime detected" {
		t.Errorf("empty summary = %q", s)
	}
	s := Summary([]Runtime{{Kind: Ollama, Models: []Model{{Name: "a", Loaded: true}, {Name: "b"}}}})
	if s != "ollama: 2 models (1 loaded)" {
		t.Errorf("summary = %q", s)
	}
}

func TestOllamaTagsCarryTheFingerprint(t *testing.T) {
	body := []byte(`{"models":[{"name":"llama3.2:3b","size":2019393189,"digest":"a80c4f17acd5",
		"details":{"format":"gguf","family":"llama","parameter_size":"3.2B","quantization_level":"Q4_K_M"},
		"capabilities":["completion","tools"]}]}`)
	models, ok := parseOllamaTags(body)
	if !ok || len(models) != 1 {
		t.Fatalf("got %+v, %v", models, ok)
	}
	m := models[0]
	if m.Digest != "a80c4f17acd5" || m.Format != "gguf" || m.Parameters != "3.2B" || m.Quantization != "Q4_K_M" || len(m.Capabilities) != 2 {
		t.Fatalf("got %+v", m)
	}
}
