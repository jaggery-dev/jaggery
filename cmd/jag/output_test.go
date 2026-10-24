package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/activity"
	"github.com/jaggery-dev/jaggery/internal/compat"
	"github.com/jaggery-dev/jaggery/internal/hosted"
	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/inventory"
	"github.com/jaggery-dev/jaggery/internal/shares"
)

// runJSON runs one command with --json, as main does, and returns what it
// printed on stdout and its exit code.
func runJSON(t *testing.T, cmdline string) (envelope, json.RawMessage, int) {
	t.Helper()
	parts := strings.Fields(cmdline)
	args, asJSON := takeJSONFlag(append(parts[1:], "--json"))
	if !asJSON {
		t.Fatal("--json not taken")
	}
	jsonOutput = true
	defer func() { jsonOutput = false }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	var runErr error
	if !jsonCommands[parts[0]] {
		runErr = errJSONNotHere("jag " + parts[0])
	} else {
		runErr = run(parts[0], args)
	}
	code, show := exitCode(runErr)
	if show {
		emitJSONError(runErr, nil)
	}
	w.Close()
	os.Stdout = saved
	out, _ := io.ReadAll(r)

	// Exactly one JSON document on stdout, nothing else.
	dec := json.NewDecoder(bytes.NewReader(out))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		t.Fatalf("jag %s --json: stdout isn't JSON: %v\n%s", cmdline, err, out)
	}
	if dec.More() {
		t.Fatalf("jag %s --json: more than one document on stdout:\n%s", cmdline, out)
	}
	var env envelope
	var data struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(raw, &data)
	if env.Version != compat.CLIOutput {
		t.Fatalf("jag %s --json: version %d", cmdline, env.Version)
	}
	if env.OK == (env.Error != nil) {
		t.Fatalf("jag %s --json: ok %v with error %+v", cmdline, env.OK, env.Error)
	}
	return env, data.Data, code
}

func TestJSONOnEveryReadCommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JAGGERY_STATE_DIR", dir)
	id, _, err := identity.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()
	writeFiles(t, folder, map[string]string{"notes.md": "one"})
	if _, err := shares.Add(dir, folder, "docs", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := shares.Do(t.Context(), dir, shares.Request{Op: shares.OpWrite, Share: "docs", Path: "notes.md", Data: []byte("two")}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := shares.AppendLog(dir, shares.LogEntry{Time: time.Now(), App: "test-agent", Op: shares.OpWrite, Share: "docs", Path: "notes.md"}); err != nil {
		t.Fatal(err)
	}
	if err := house.Save(dir, house.Snapshot{UpdatedAt: time.Now(),
		Self: house.Machine{MachineID: id.ID, Name: id.Name, Self: true, Reachable: true, Inventory: inventory.Report{OS: "linux"}}}); err != nil {
		t.Fatal(err)
	}
	log, err := hosted.OpenLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Append(hosted.Entry{ID: "abc", At: time.Now(), Provider: "example", Model: "small", Sent: true}); err != nil {
		t.Fatal(err)
	}

	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"day": "2026-10-22", "entries": []activity.Entry{{App: "Goose", Model: "small"}}})
	}))
	defer agent.Close()
	savedAgent := localAgent
	localAgent = func() string { return agent.URL }
	defer func() { localAgent = savedAgent }()
	releases := httptest.NewServer(http.NotFoundHandler())
	defer releases.Close()
	t.Setenv("JAGGERY_RELEASES_API", releases.URL)

	for _, c := range []struct {
		cmdline string
		ok      bool
		code    int
		has     string // in data, or in the error's code
	}{
		{"peers", true, 0, `"peers":[]`},
		{"map", true, 0, `"machine_id":"` + id.ID + `"`},
		{"models", true, 0, `"models":[]`},
		{"activity", true, 0, `"app":"Goose"`},
		{"outbound", true, 0, `"id":"abc"`},
		{"outbound show nope", false, 1, "not_found"},
		{"share", true, 0, `"name":"docs"`},
		{"files log", true, 0, `"app":"test-agent"`},
		{"files versions docs notes.md", true, 0, `"versions":[{`},
		{"files versions nope notes.md", false, 1, "not_found"},
		{"update --check", false, 1, "failed"},
		{"update", false, 2, "usage"},
		{"invite", false, 2, "usage"},
		{"share add " + folder, false, 2, "usage"},
	} {
		env, data, code := runJSON(t, c.cmdline)
		got := strings.ReplaceAll(string(data), " ", "")
		got = strings.ReplaceAll(got, "\n", "")
		if env.Error != nil {
			got = env.Error.Code
		}
		if env.OK != c.ok || code != c.code || !strings.Contains(got, c.has) {
			t.Errorf("jag %s --json: ok %v exit %d, want ok %v exit %d with %s; got %s", c.cmdline, env.OK, code, c.ok, c.code, c.has, got)
		}
	}
}

func TestDoctorJSONGivesEachFix(t *testing.T) {
	jsonOutput = true
	defer func() { jsonOutput = false }()
	r, w, _ := os.Pipe()
	saved := os.Stdout
	os.Stdout = w
	err := doctorResult([]checkResult{
		{"agent", fail, "not running on this machine", "start it: `jag up`"},
		{"clock", warn, "no approved peer to compare with", ""},
		{"network", pass, "eth0 192.0.2.10", "never shown"},
	})
	w.Close()
	os.Stdout = saved
	out, _ := io.ReadAll(r)
	if code, show := exitCode(err); code != exitDoctorFail || show {
		t.Fatalf("exit %d (print %v)", code, show)
	}
	var env struct {
		OK    bool       `json:"ok"`
		Error *jsonError `json:"error"`
		Data  struct {
			Checks []doctorJSON `json:"checks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if env.OK || env.Error == nil || env.Error.Code != "checks_failed" || len(env.Data.Checks) != 3 {
		t.Fatalf("%s", out)
	}
	if c := env.Data.Checks[0]; c.State != "fail" || c.Fix != "start it: `jag up`" {
		t.Errorf("failing check: %+v", c)
	}
	if c := env.Data.Checks[2]; c.State != "pass" || c.Fix != "" {
		t.Errorf("passing check: %+v", c)
	}
}

func TestTakeJSONFlag(t *testing.T) {
	args, ok := takeJSONFlag([]string{"log", "--json", "--share", "docs", "--", "--json"})
	if !ok || strings.Join(args, " ") != "log --share docs -- --json" {
		t.Fatalf("%v %v", args, ok)
	}
}
