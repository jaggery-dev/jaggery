package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/hosted"
)

// fakeProvider records every body it receives, the way a leak test (X4)
// needs: whatever reaches it has left the house.
type fakeProvider struct {
	mu     sync.Mutex
	bodies []string
	auth   []string
	// status, when set, is answered instead of a completion (a 429, say).
	status int
}

func (f *fakeProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.bodies = append(f.bodies, string(body))
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	status := f.status
	f.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":{"message":"quota"}}`)
		return
	}
	if strings.Contains(string(body), `"stream":true`) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"hi"}}]}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"choices":[],"usage":{"prompt_tokens":40,"completion_tokens":2}}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":30,"completion_tokens":5}}`)
}

func (f *fakeProvider) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.bodies...)
}

func setUpExit(t *testing.T, capUSD string) (*fakeProvider, string, *http.ServeMux) {
	t.Helper()
	fake := &fakeProvider{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	t.Setenv("JAGGERY_STATE_DIR", dir)
	cfg := `{"providers":[{"name":"fake","base_url":"` + srv.URL + `/v1","key_file":"fake.key",
	  "models":["m1"],"price":{"input_per_mtok":1,"output_per_mtok":5},"cap_usd":` + capUSD + `,"max_output_tokens":100}]}`
	if err := os.WriteFile(filepath.Join(dir, hosted.ConfigFile), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fake.key"), []byte("sk-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := exit
	exit = &exitState{}
	t.Cleanup(func() { exit = saved })
	startHosted(dir)
	if len(hostedConfig().Providers) != 1 {
		t.Fatal("provider not loaded")
	}
	mux := http.NewServeMux()
	registerRouter(mux, nil, nil)
	return fake, dir, mux
}

func chat(mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.RemoteAddr = "192.0.2.65:50000"
	mux.ServeHTTP(rec, req)
	return rec
}

const redCanary = "4111 1111 1111 1111"

func TestHostedGreenGoesOutAndIsLogged(t *testing.T) {
	fake, dir, mux := setUpExit(t, "2")
	rec := chat(mux, `{"model":"fake/m1","max_tokens":5000,"messages":[{"role":"user","content":"how do I bake bread?"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	got := fake.received()
	if len(got) != 1 {
		t.Fatalf("provider got %d requests", len(got))
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(got[0]), &sent)
	if sent["model"] != "m1" || sent["max_tokens"] != float64(100) || fake.auth[0] != "Bearer sk-test" {
		t.Fatalf("sent %s with %q", got[0], fake.auth[0])
	}

	log, _ := hosted.OpenLog(dir)
	entries, _ := log.Day(time.Now())
	if len(entries) != 1 || !entries[0].Sent || entries[0].PromptTokens != 30 || entries[0].CompletionTokens != 5 {
		t.Fatalf("outbound log: %+v", entries)
	}
	p, err := log.ReadPayload(entries[0].ID)
	if err != nil || p.Sent != got[0] || !strings.Contains(p.Reply, `"hi"`) {
		t.Fatalf("payload %+v %v", p, err)
	}
	if u := exit.spend.Used("fake"); u.Requests != 1 || u.USD <= 0 {
		t.Fatalf("spend %+v", u)
	}
}

func TestHostedStreamCountsUsage(t *testing.T) {
	fake, _, mux := setUpExit(t, "2")
	rec := chat(mux, `{"model":"fake/m1","stream":true,"messages":[{"role":"user","content":"say hi"}]}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "[DONE]") {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(fake.received()[0], `"include_usage":true`) {
		t.Fatalf("stream sent without include_usage: %s", fake.received()[0])
	}
	if u := exit.spend.Used("fake"); u.PromptTokens != 40 || u.CompletionTokens != 2 || u.Estimated != 0 {
		t.Fatalf("spend %+v", u)
	}
}

// Content is not inspected (the privacy gate was dropped): a request with
// personal data goes out like any other and is logged with its payload.
// Only the allow-list and the cap can stop a request.
func TestHostedSendsWithoutInspecting(t *testing.T) {
	fake, dir, mux := setUpExit(t, "2")
	if rec := chat(mux, `{"model":"fake/m1","messages":[{"role":"user","content":"email jane@example.com, card `+redCanary+`"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if rec := chat(mux, `{"model":"fake/m2","messages":[{"role":"user","content":"hi"}]}`); rec.Code != http.StatusForbidden {
		t.Fatalf("a model off the allow-list: status %d", rec.Code)
	}
	if got := fake.received(); len(got) != 1 {
		t.Fatalf("provider got %d requests, want 1", len(got))
	}
	log, _ := hosted.OpenLog(dir)
	entries, _ := log.Day(time.Now())
	if len(entries) != 1 || !entries[0].Sent {
		t.Fatalf("outbound log: %+v", entries)
	}
	if p, err := log.ReadPayload(entries[0].ID); err != nil || !strings.Contains(p.Sent, "jane@example.com") {
		t.Fatalf("payload not kept: %v", err)
	}
}

func TestHostedSpendCap(t *testing.T) {
	// 100 output tokens at $5/M is $0.0005; a cap of $0.0001 can't hold it.
	fake, _, mux := setUpExit(t, "0.0001")
	rec := chat(mux, `{"model":"fake/m1","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if len(fake.received()) != 0 {
		t.Fatal("sent past the cap")
	}
}

func TestHostedModelsListed(t *testing.T) {
	_, _, mux := setUpExit(t, "2")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if !strings.Contains(rec.Body.String(), `"fake/m1"`) {
		t.Fatalf("models: %s", rec.Body)
	}
}

func TestHostedProvidersReload(t *testing.T) {
	_, dir, mux := setUpExit(t, "2")
	saved := reloadEvery
	reloadEvery = 0
	t.Cleanup(func() { reloadEvery = saved })

	path := filepath.Join(dir, hosted.ConfigFile)
	raw, _ := os.ReadFile(path)
	changed := strings.Replace(string(raw), `"models":["m1"]`, `"models":["m1","m2"]`, 1)
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	// A different size is enough to notice, whatever the clock says.
	if rec := chat(mux, `{"model":"fake/m2","messages":[{"role":"user","content":"hi"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("new model after reload: %d %s", rec.Code, rec.Body)
	}
	// A broken file turns hosted models off instead of keeping stale ones.
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"fake"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if rec := chat(mux, `{"model":"fake/m1","messages":[{"role":"user","content":"hi"}]}`); rec.Code == http.StatusOK {
		t.Fatal("a broken providers.json still let requests out")
	}
}
