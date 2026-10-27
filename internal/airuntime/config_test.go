package airuntime

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseVLLMModels(t *testing.T) {
	vllm := []byte(`{"object":"list","data":[{"id":"Qwen/Qwen3-8B","object":"model","created":1761500000,"owned_by":"vllm","root":"Qwen/Qwen3-8B","max_model_len":32768}]}`)
	models, ok := parseVLLMModels(vllm)
	if !ok || len(models) != 1 || models[0].Name != "Qwen/Qwen3-8B" {
		t.Fatalf("vLLM's list: got %+v, %v", models, ok)
	}
	// Another OpenAI-compatible server on 8000 is not vLLM.
	for _, body := range [][]byte{
		[]byte(`{"object":"list","data":[{"id":"gpt-oss-20b","object":"model","owned_by":"organization_owner"}]}`),
		[]byte(`{"object":"list","data":[]}`),
		[]byte(`{"status":"UP"}`),
	} {
		if _, ok := parseVLLMModels(body); ok {
			t.Errorf("%s should not count as vLLM", body)
		}
	}
}

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	if ports, err := LoadConfig(dir); err != nil || ports != nil {
		t.Fatalf("no file: got %v, %v", ports, err)
	}
	writeConfig(t, dir, `{"runtimes":[{"kind":"llamacpp","port":8181},{"kind":"ollama","port":11500}]}`)
	ports, err := LoadConfig(dir)
	if err != nil || len(ports) != 2 || ports[0] != (Port{LlamaCpp, 8181}) {
		t.Fatalf("got %v, %v", ports, err)
	}
	for body, want := range map[string]string{
		`{"runtimes":[{"kind":"llama","port":8181}]}`:                                "unknown kind",
		`{"runtimes":[{"kind":"llamacpp","port":0}]}`:                                "not a port",
		`{"runtimes":[{"kind":"llamacpp","port":8181},{"kind":"vllm","port":8181}]}`: "listed twice",
		`{"runtimes":[{"kind":"llamacpp","port":8181}`:                               "runtimes.json",
	} {
		writeConfig(t, dir, body)
		if _, err := LoadConfig(dir); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", body, err, want)
		}
	}
}

func fakeLlamaCpp(t *testing.T) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Write([]byte(`{"object":"list","data":[{"id":"gemma-3-4b","object":"model"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	p, _ := strconv.Atoi(port)
	return p
}

// A runtime on a port of the owner's choosing is found once it is in
// runtimes.json, and the file is read again when it changes.
func TestDetectReadsTheConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JAGGERY_STATE_DIR", dir)
	old := reloadEvery
	reloadEvery = 0
	t.Cleanup(func() { reloadEvery = old })

	port := fakeLlamaCpp(t)
	has := func() bool {
		for _, p := range Ports() {
			if p.Port == port {
				return true
			}
		}
		return false
	}
	if has() {
		t.Fatal("the port is probed before it is configured")
	}
	writeConfig(t, dir, `{"runtimes":[{"kind":"llamacpp","port":`+strconv.Itoa(port)+`}]}`)
	if !has() {
		t.Fatal("a configured port is not probed")
	}
	found := DetectPorts(context.Background(), "127.0.0.1", []Port{{LlamaCpp, port}})
	if len(found) != 1 || found[0].Kind != LlamaCpp || found[0].Models[0].Name != "gemma-3-4b" {
		t.Fatalf("got %+v", found)
	}

	// A broken file is ignored, loudly, and the defaults still apply.
	time.Sleep(10 * time.Millisecond)
	writeConfig(t, dir, `{"runtimes":[{"kind":"llama","port":1}]}`)
	if has() || ConfigError() == nil {
		t.Fatalf("a broken file should be ignored and reported: %v", ConfigError())
	}
	if len(Ports()) != len(Kinds) {
		t.Fatalf("defaults missing: %v", Ports())
	}
}

// Two entries on one port probe it once, the first entry's kind winning.
func TestDetectPortsProbesAPortOnce(t *testing.T) {
	port := fakeLlamaCpp(t)
	found := DetectPorts(context.Background(), "127.0.0.1", []Port{{LMStudio, port}, {LlamaCpp, port}})
	if len(found) != 1 || found[0].Kind != LMStudio {
		t.Fatalf("got %+v", found)
	}
}
