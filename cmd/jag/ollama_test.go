package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jaggery-dev/jaggery/internal/airuntime"
	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/inventory"
)

// fakeOllama is an Ollama on a port of its own, listed in runtimes.json,
// with an embedding model and a chat model. It records the paths asked.
func fakeOllama(t *testing.T, dir string) *[]string {
	t.Helper()
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		switch r.URL.Path {
		case "/api/tags":
			w.Write([]byte(`{"models":[{"name":"fake-embed:latest","digest":"e1"},{"name":"fake-chat:latest","digest":"c1"}]}`))
		case "/api/ps":
			w.Write([]byte(`{"models":[{"name":"fake-chat:latest","size_vram":1000}]}`))
		case "/v1/embeddings":
			w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"model":"fake-embed:latest"}`))
		case "/api/embed":
			w.Write([]byte(`{"model":"fake-embed:latest","embeddings":[[0.1,0.2]]}`))
		case "/api/chat":
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.Write([]byte(`{"model":"fake-chat:latest","message":{"role":"assistant","content":"hi"},"done":false}` + "\n"))
			w.Write([]byte(`{"model":"fake-chat:latest","done":true,"prompt_eval_count":7,"eval_count":20,"eval_duration":500000000}` + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	os.WriteFile(filepath.Join(dir, airuntime.ConfigFile), []byte(`{"runtimes":[{"kind":"ollama","port":`+port+`}]}`), 0o644)
	return &asked
}

// A house where this machine runs the fake Ollama, the laptop has a model
// in LM Studio, and the nas runs an older Jaggery (it lists nothing it
// serves beyond chat).
func ollamaHouse(t *testing.T) (*http.ServeMux, *[]string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("JAGGERY_STATE_DIR", dir)
	asked := fakeOllama(t, dir)
	self := house.Machine{MachineID: "71df", Name: "desktop", Self: true, Reachable: true,
		Inventory: inventory.Report{Runtimes: []airuntime.Runtime{{Kind: airuntime.Ollama, Models: []airuntime.Model{
			{Name: "fake-embed:latest", Digest: "e1"}, {Name: "fake-chat:latest", Digest: "c1", Loaded: true, VRAMBytes: 1000}}}}}}
	laptop := house.Machine{MachineID: "d47f", Name: "laptop", Trust: home.Approved, Reachable: true,
		Inventory: inventory.Report{Serves: servedAPIs(), Runtimes: []airuntime.Runtime{{Kind: airuntime.LMStudio, Models: []airuntime.Model{{Name: "studio-model"}}}}}}
	nas := house.Machine{MachineID: "a9c3", Name: "nas", Trust: home.Approved, Reachable: true,
		Inventory: inventory.Report{Runtimes: []airuntime.Runtime{{Kind: airuntime.Ollama, Models: []airuntime.Model{{Name: "old-embed:latest"}}}}}}
	if err := house.Save(dir, house.Snapshot{Self: self, Peers: []house.Machine{laptop, nas}}); err != nil {
		t.Fatal(err)
	}
	saved := approvalList
	approvalList = nil
	t.Cleanup(func() { approvalList = saved })
	store, _ := home.Open(t.TempDir())
	mux := http.NewServeMux()
	registerRouter(mux, newIdentity(t, "desktop"), store)
	return mux, asked
}

func call(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, bytes.NewReader([]byte(body))))
	return rec
}

func TestEmbeddingsAndOllamaThroughAILocal(t *testing.T) {
	mux, asked := ollamaHouse(t)

	rec := call(mux, "POST", "/v1/embeddings", `{"model":"fake-embed","input":"hello"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"embedding":[0.1,0.2]`) {
		t.Fatalf("/v1/embeddings: %d %s", rec.Code, rec.Body)
	}
	rec = call(mux, "POST", "/api/embed", `{"model":"fake-embed","input":"hello"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"embeddings":[[0.1,0.2]]`) {
		t.Fatalf("/api/embed: %d %s", rec.Code, rec.Body)
	}
	rec = call(mux, "POST", "/api/chat", `{"model":"fake-chat","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK || strings.Count(rec.Body.String(), "\n") != 2 || !strings.Contains(rec.Body.String(), `"done":true`) {
		t.Fatalf("/api/chat: %d %q", rec.Code, rec.Body)
	}
	for _, want := range []string{"/v1/embeddings", "/api/embed", "/api/chat"} {
		if !contains(*asked, want) {
			t.Errorf("the runtime was never asked %s: %v", want, *asked)
		}
	}

	var tags struct {
		Models []ollamaModel `json:"models"`
	}
	rec = call(mux, "GET", "/api/tags", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &tags); err != nil {
		t.Fatalf("/api/tags: %v %s", err, rec.Body)
	}
	var names []string
	for _, m := range tags.Models {
		names = append(names, m.Name)
	}
	// LM Studio's model isn't Ollama's to serve, and the nas's older
	// Jaggery can't serve Ollama's API, so neither is listed.
	if strings.Join(names, ",") != "fake-chat:latest,fake-embed:latest" {
		t.Errorf("/api/tags names = %v", names)
	}
	rec = call(mux, "GET", "/api/ps", "")
	if !strings.Contains(rec.Body.String(), `"name":"fake-chat:latest"`) || strings.Contains(rec.Body.String(), "fake-embed") {
		t.Errorf("/api/ps should list only loaded models: %s", rec.Body)
	}
	if rec = call(mux, "GET", "/api/version", ""); !strings.Contains(rec.Body.String(), `"version":`) {
		t.Errorf("/api/version: %s", rec.Body)
	}
}

func TestOllamaRefusalsSayWhatToDo(t *testing.T) {
	mux, _ := ollamaHouse(t)
	for _, tc := range []struct{ method, path, body, want string }{
		{"POST", "/api/pull", `{"model":"llama3.2"}`, "ollama pull"},
		{"DELETE", "/api/delete", `{"model":"fake-chat"}`, "doesn't change models"},
		{"POST", "/api/chat", `{"model":"studio-model"}`, "in LM Studio on laptop, not in Ollama"},
		{"POST", "/api/embed", `{"model":"old-embed"}`, "nas, which runs an older Jaggery"},
		{"POST", "/api/chat", `{"messages":[]}`, "missing model"},
		{"GET", "/api/nothing", ``, "isn't served by ai.local"},
	} {
		rec := call(mux, tc.method, tc.path, tc.body)
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(rec.Body.Bytes(), &e) != nil || !strings.Contains(e.Error, tc.want) || rec.Code < 400 {
			t.Errorf("%s %s: %d %s, want an Ollama error with %q", tc.method, tc.path, rec.Code, rec.Body, tc.want)
		}
	}
	rec := call(mux, "POST", "/v1/embeddings", `{"model":"nothing-here","input":"x"}`)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"message":"no machine in the house has model`) {
		t.Errorf("unknown embedding model: %d %s", rec.Code, rec.Body)
	}
}

func TestOllamaUsage(t *testing.T) {
	m := newSpeedMeter(httptest.NewRecorder())
	m.Write([]byte(`{"message":{"content":"a"},"done":false}` + "\n"))
	m.Write([]byte(`{"done":true,"prompt_eval_count":7,"eval_count":20,"eval_duration":500000000}` + "\n"))
	u, ok := ollamaUsageOf(m)
	if !ok || u.PromptEvalCount != 7 || u.EvalCount != 20 {
		t.Fatalf("got %+v %v", u, ok)
	}
	if tps, ok := u.tokensPerSec(); !ok || tps != 40 {
		t.Errorf("tokens/s = %v %v, want 40", tps, ok)
	}
	if _, ok := (ollamaUsage{EvalCount: 1}).tokensPerSec(); ok {
		t.Error("one token is no measure")
	}
}

func TestOllamaStreamsByDefault(t *testing.T) {
	chat := apiOllamaChat()
	if !chat.streamed([]byte(`{}`)) || chat.streamed([]byte(`{"stream":false}`)) || apiChat.streamed([]byte(`{}`)) {
		t.Error("Ollama streams unless told not to; OpenAI only when asked")
	}
	show, _ := apiByName("ollama/show")
	if got := show.model([]byte(`{"name":"llama3.2"}`)); got != "llama3.2" {
		t.Errorf("Ollama's older requests name the model in name: got %q", got)
	}
}
