package hosted

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

const goodProvider = `{"name":"anthropic","base_url":"https://api.anthropic.com/v1","key_file":"anthropic.key",
 "models":["claude-haiku-4-5"],"price":{"input_per_mtok":1,"output_per_mtok":5},"cap_usd":2}`

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if c, err := Load(dir); err != nil || len(c.Providers) != 0 {
		t.Fatalf("no file: %v %v", c, err)
	}
	writeConfig(t, dir, `{"providers":[`+goodProvider+`]}`)
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Providers[0].MaxOutputTokens != DefaultMaxOutput {
		t.Fatalf("max output %d", c.Providers[0].MaxOutputTokens)
	}
	for name, bad := range map[string]string{
		"no cap":       strings.Replace(goodProvider, `"cap_usd":2`, `"cap_usd":0`, 1),
		"plain http":   strings.Replace(goodProvider, "https://api.anthropic.com", "http://api.anthropic.com", 1),
		"no models":    strings.Replace(goodProvider, `["claude-haiku-4-5"]`, `[]`, 1),
		"no price":     strings.Replace(goodProvider, `"input_per_mtok":1`, `"input_per_mtok":0`, 1),
		"slash name":   strings.Replace(goodProvider, `"anthropic"`, `"a/b"`, 1),
		"listed twice": goodProvider + `,` + goodProvider,
	} {
		writeConfig(t, dir, `{"providers":[`+bad+`]}`)
		if _, err := Load(dir); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
	// A vendor's proxy on this machine may be plain HTTP.
	writeConfig(t, dir, `{"providers":[`+strings.Replace(goodProvider, "https://api.anthropic.com/v1", "http://127.0.0.1:3301/v1", 1)+`]}`)
	if _, err := Load(dir); err != nil {
		t.Fatalf("local proxy: %v", err)
	}
}

func TestFind(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `{"providers":[`+goodProvider+`]}`)
	c, _ := Load(dir)
	if p, m, ok := c.Find("anthropic/claude-haiku-4-5"); !ok || p.Name != "anthropic" || m != "claude-haiku-4-5" {
		t.Fatalf("find: %v %q %v", p, m, ok)
	}
	if _, _, ok := c.Find("anthropic/claude-opus-5-5"); ok {
		t.Fatal("found a model off the allow-list")
	}
	if _, ok := c.Named("anthropic/claude-opus-5-5"); !ok {
		t.Fatal("Named should recognise the provider")
	}
	if _, _, ok := c.Find("llama3.2:3b"); ok {
		t.Fatal("a local model matched a provider")
	}
}

func TestKeyFileMustBePrivate(t *testing.T) {
	dir := t.TempDir()
	p := Provider{Name: "x", KeyFile: "x.key"}
	path := filepath.Join(dir, "x.key")
	if err := os.WriteFile(path, []byte("sk-test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Key(dir); err == nil {
		t.Fatal("read a world-readable key")
	}
	_ = os.Chmod(path, 0o600)
	if k, err := p.Key(dir); err != nil || k != "sk-test" {
		t.Fatalf("key %q %v", k, err)
	}
}

func TestPrepare(t *testing.T) {
	p := &Provider{MaxOutputTokens: 500}
	out, maxOut, err := Prepare([]byte(`{"model":"anthropic/claude-haiku-4-5","max_completion_tokens":9000,"stream":true,"messages":[]}`), p, "claude-haiku-4-5")
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	_ = json.Unmarshal(out, &req)
	if req["model"] != "claude-haiku-4-5" || req["max_tokens"] != float64(500) || maxOut != 500 {
		t.Fatalf("got %s (max %d)", out, maxOut)
	}
	if _, ok := req["max_completion_tokens"]; ok {
		t.Fatal("max_completion_tokens kept beside max_tokens")
	}
	if so, _ := req["stream_options"].(map[string]any); so["include_usage"] != true {
		t.Fatalf("stream without include_usage: %s", out)
	}
	out, maxOut, _ = Prepare([]byte(`{"model":"x","max_tokens":20}`), p, "m")
	if maxOut != 20 || strings.Contains(string(out), "stream_options") {
		t.Fatalf("small max kept? %s %d", out, maxOut)
	}
}

func TestLedgerHoldsAndPersists(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenLedger(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{Name: "x", CapUSD: 1}
	// Ten requests at once, each could cost 0.3: only three fit.
	var mu sync.Mutex
	held := 0
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Reserve(p, 0.3) {
				mu.Lock()
				held++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if held != 3 {
		t.Fatalf("%d reservations fit under a $1 cap at $0.30 each", held)
	}
	for i := 0; i < 3; i++ {
		if err := l.Settle("x", 0.3, 100, 10, 0.1, false); err != nil {
			t.Fatal(err)
		}
	}
	l2, _ := OpenLedger(dir)
	if u := l2.Used("x"); u.Requests != 3 || u.USD < 0.29 || u.USD > 0.31 {
		t.Fatalf("after reopening: %+v", u)
	}
	if !l2.Reserve(p, 0.6) || l2.Reserve(p, 0.2) {
		t.Fatal("cap not kept across a restart")
	}
	if info, _ := os.Stat(filepath.Join(dir, spendFile)); info.Mode().Perm() != 0o600 {
		t.Fatalf("ledger mode %v", info.Mode())
	}
}

func TestOutboundLog(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	id := NewID(now)
	if err := l.Append(Entry{ID: id, At: now, Provider: "x", Model: "m", Verdict: "green", Sent: true, Status: 200}); err != nil {
		t.Fatal(err)
	}
	if err := l.SavePayload(id, []byte(`{"model":"m"}`), []byte("data: hi\n\n")); err != nil {
		t.Fatal(err)
	}
	es, err := l.Day(now)
	if err != nil || len(es) != 1 || es[0].ID != id {
		t.Fatalf("day: %v %v", es, err)
	}
	p, err := l.ReadPayload(id)
	if err != nil || p.Sent != `{"model":"m"}` || p.Reply != "data: hi\n\n" {
		t.Fatalf("payload: %+v %v", p, err)
	}
	if _, err := l.ReadPayload("../../identity"); err == nil {
		t.Fatal("read outside the payload directory")
	}
	for _, path := range []string{filepath.Join(dir, outboundDir), filepath.Join(dir, outboundDir, payloadDir)} {
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode %v", path, info.Mode())
		}
	}
	// Old payloads and days go after OutboundKeep.
	old := now.Add(-OutboundKeep - 48*time.Hour)
	_ = l.Append(Entry{ID: NewID(old), At: old})
	pp, _ := l.payloadPath(id)
	_ = os.Chtimes(pp, old, old)
	l.Prune(now)
	if es, _ := l.Day(old); len(es) != 0 {
		t.Fatal("old day kept")
	}
	if _, err := l.ReadPayload(id); err == nil {
		t.Fatal("old payload kept")
	}
}

func TestGroupsAndFreeConfig(t *testing.T) {
	dir := t.TempDir()
	withFree := strings.Replace(goodProvider, `"cap_usd":2`, `"cap_usd":2,"free":{"tokens":1000,"per":"day"}`, 1)
	writeConfig(t, dir, `{"providers":[`+withFree+`],"groups":{"coding":["anthropic/claude-haiku-4-5"]}}`)
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ms, ok := c.Group("coding"); !ok || len(ms) != 1 || ms[0].ID() != "anthropic/claude-haiku-4-5" {
		t.Fatalf("group: %v %v", ms, ok)
	}
	for name, bad := range map[string]string{
		"unknown member":        `{"providers":[` + goodProvider + `],"groups":{"coding":["anthropic/claude-opus-5-5"]}}`,
		"named like a provider": `{"providers":[` + goodProvider + `],"groups":{"anthropic":["anthropic/claude-haiku-4-5"]}}`,
		"empty group":           `{"providers":[` + goodProvider + `],"groups":{"coding":[]}}`,
		"bad period":            `{"providers":[` + strings.Replace(goodProvider, `"cap_usd":2`, `"cap_usd":2,"free":{"tokens":5,"per":"week"}`, 1) + `]}`,
		"no free tokens":        `{"providers":[` + strings.Replace(goodProvider, `"cap_usd":2`, `"cap_usd":2,"free":{"tokens":0,"per":"day"}`, 1) + `]}`,
	} {
		writeConfig(t, dir, bad)
		if _, err := Load(dir); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
}

func TestFreeAllowance(t *testing.T) {
	l, _ := OpenLedger(t.TempDir())
	p := &Provider{Name: "x", CapUSD: 1, Price: Price{InputPerMTok: 1, OutputPerMTok: 1}, Free: &Free{Tokens: 1000, Per: "day"}}
	day1 := time.Date(2026, 10, 10, 12, 0, 0, 0, time.Local)
	if l.FreeLeft(p, day1) != 1000 || l.Bill(p, 400, 100, day1) != 0 {
		t.Fatal("a fresh allowance should cover 500 tokens for free")
	}
	_ = l.SettleAt("x", 0, 400, 400, 0, false, p.Free, day1)
	if got := l.FreeLeft(p, day1); got != 200 {
		t.Fatalf("left %d, want 200", got)
	}
	// 1000 tokens now: 200 free, 800 billed at $1/M.
	if got := l.Bill(p, 500, 500, day1); got < 0.000799 || got > 0.000801 {
		t.Fatalf("bill %v, want 0.0008", got)
	}
	// The next day starts afresh, and the old day is dropped.
	day2 := day1.Add(24 * time.Hour)
	if l.FreeLeft(p, day2) != 1000 {
		t.Fatal("daily allowance did not reset")
	}
	_ = l.SettleAt("x", 0, 10, 10, 0, false, p.Free, day2)
	if u := l.Used("x"); len(u.Periods) != 1 {
		t.Fatalf("old periods kept: %v", u.Periods)
	}
	// A provider without an allowance bills everything.
	q := &Provider{Name: "y", Price: Price{InputPerMTok: 1, OutputPerMTok: 1}}
	if l.FreeLeft(q, day1) != 0 || l.Bill(q, 1000000, 0, day1) != 1 {
		t.Fatal("no allowance should bill in full")
	}
}
