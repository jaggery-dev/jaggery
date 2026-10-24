package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/hosted"
)

// setUpGroup starts two fake providers, a and b, each offering m1, in the
// group "coding" (a first). a's free allowance is aFree tokens a day; b has
// a million once.
func setUpGroup(t *testing.T, aFree string) (a, b *fakeProvider, dir string, mux *http.ServeMux) {
	t.Helper()
	a, b = &fakeProvider{}, &fakeProvider{}
	sa, sb := httptest.NewServer(a), httptest.NewServer(b)
	t.Cleanup(sa.Close)
	t.Cleanup(sb.Close)
	dir = t.TempDir()
	t.Setenv("JAGGERY_STATE_DIR", dir)
	cfg := `{"providers":[
	  {"name":"a","base_url":"` + sa.URL + `/v1","key_file":"a.key","models":["m1"],"price":{"input_per_mtok":1,"output_per_mtok":5},"cap_usd":2,"max_output_tokens":100,"free":{"tokens":` + aFree + `,"per":"day"}},
	  {"name":"b","base_url":"` + sb.URL + `/v1","key_file":"b.key","models":["m1"],"price":{"input_per_mtok":1,"output_per_mtok":5},"cap_usd":2,"max_output_tokens":100,"free":{"tokens":1000000,"per":"once"}}],
	 "groups":{"coding":["a/m1","b/m1"]}}`
	for name, body := range map[string]string{hosted.ConfigFile: cfg, "a.key": "ka", "b.key": "kb"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	saved := exit
	exit = &exitState{}
	groupState.Lock()
	groupState.cool, groupState.sticky = nil, nil
	groupState.Unlock()
	t.Cleanup(func() { exit = saved })
	startHosted(dir)
	mux = http.NewServeMux()
	registerRouter(mux, nil, nil)
	return a, b, dir, mux
}

func ask(mux *http.ServeMux, model, question string) *httptest.ResponseRecorder {
	return chat(mux, `{"model":"`+model+`","messages":[{"role":"user","content":"`+question+`"}]}`)
}

func TestGroupFallsBackOnQuotaAndCoolsDown(t *testing.T) {
	a, b, dir, mux := setUpGroup(t, "100000")
	a.status = http.StatusTooManyRequests
	if rec := ask(mux, "coding", "first question"); rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if len(a.received()) != 1 || len(b.received()) != 1 {
		t.Fatalf("a got %d, b got %d; want 1 each", len(a.received()), len(b.received()))
	}
	// a is cooling down now: the next request goes straight to b.
	if rec := ask(mux, "coding", "second question"); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if len(a.received()) != 1 || len(b.received()) != 2 {
		t.Fatalf("after cool-down: a got %d, b got %d", len(a.received()), len(b.received()))
	}
	// Both attempts that left the house are in the outbound log.
	log, _ := hosted.OpenLog(dir)
	entries, _ := log.Day(time.Now())
	var fellBack, served int
	for _, e := range entries {
		if e.Group != "coding" || !e.Sent {
			t.Fatalf("entry %+v", e)
		}
		if e.Status == http.StatusTooManyRequests && strings.Contains(e.Reason, "tried the next model") {
			fellBack++
		}
		if e.Status == http.StatusOK && e.Provider == "b" {
			served++
		}
	}
	if fellBack != 1 || served != 2 {
		t.Fatalf("log: %d fallbacks, %d served by b: %+v", fellBack, served, entries)
	}
}

func TestGroupUsesFreeAllowanceBeforePaying(t *testing.T) {
	// a's daily allowance (40 tokens) is gone after one answer (30+5).
	a, b, _, mux := setUpGroup(t, "40")
	ask(mux, "coding", "one")
	ask(mux, "coding", "two")
	if len(a.received()) != 1 || len(b.received()) != 1 {
		t.Fatalf("a got %d, b got %d; want a then b", len(a.received()), len(b.received()))
	}
	if u := exit.spend.Used("a"); u.USD != 0 || u.Periods["day:"+time.Now().Format("2006-01-02")] != 35 {
		t.Fatalf("a inside its free allowance should cost nothing: %+v", u)
	}
	if u := exit.spend.Used("b"); u.USD != 0 {
		t.Fatalf("b inside its free allowance should cost nothing: %+v", u)
	}
	// With every allowance gone, the group still answers, and it is paid for.
	exit.config.Providers[1].Free = nil
	ask(mux, "coding", "three")
	if u := exit.spend.Used("a").USD + exit.spend.Used("b").USD; u <= 0 {
		t.Fatal("paid use not counted")
	}
}

func TestGroupKeepsAConversationOnOneModel(t *testing.T) {
	a, b, _, mux := setUpGroup(t, "100000")
	a.status = http.StatusTooManyRequests
	conv := `{"model":"coding","messages":[{"role":"user","content":"plan the refactor"}]}`
	chat(mux, conv) // a refuses, b answers
	a.status = 0
	groupState.Lock()
	groupState.cool = nil // a is back
	groupState.Unlock()
	// The same conversation, a turn later, stays with b; a new one goes to a.
	chat(mux, `{"model":"coding","messages":[{"role":"user","content":"plan the refactor"},{"role":"assistant","content":"ok"},{"role":"user","content":"go on"}]}`)
	ask(mux, "coding", "a new conversation")
	if len(b.received()) != 2 || len(a.received()) != 2 {
		t.Fatalf("a got %d (want 2: the refusal and the new conversation), b got %d (want 2)", len(a.received()), len(b.received()))
	}
}

func TestGroupListedAsAModel(t *testing.T) {
	_, _, _, mux := setUpGroup(t, "100000")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if !strings.Contains(rec.Body.String(), `"id":"coding"`) {
		t.Fatalf("models: %s", rec.Body)
	}
}
