package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/jaggery-dev/jaggery/internal/airuntime"
	"github.com/jaggery-dev/jaggery/internal/reach"
)

func askReach(t *testing.T, req reach.Request) reach.Report {
	t.Helper()
	srv := httptest.NewServer(reachHandler(newIdentity(t, "checker")))
	defer srv.Close()
	body, _ := json.Marshal(req)
	resp, err := http.Post(srv.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var report reach.Report
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	return report
}

// The G1 scenario: a machine with Ollama answering the LAN asks a peer to
// dial it back, and hears that its Ollama is exposed.
func TestDialBackReportsAnExposedRuntime(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer ollama.Close()
	saved := runtimePorts
	runtimePorts = map[airuntime.Kind]int{airuntime.Ollama: int(netip.MustParseAddrPort(ollama.Listener.Addr().String()).Port())}
	defer func() { runtimePorts = saved }()

	report := askReach(t, reach.Request{Runtimes: checkedRuntimes})
	exposed := report.Exposed()
	if len(exposed) != 1 || exposed[0].Kind != airuntime.Ollama {
		t.Fatalf("exposed = %+v, want Ollama", exposed)
	}
}

// An agent older than G1 sends no runtimes, and gets no runtime verdict back:
// unknown must not read as safe.
func TestDialBackChecksNoRuntimesUnlessAsked(t *testing.T) {
	if report := askReach(t, reach.Request{}); report.Runtimes != nil {
		t.Fatalf("runtimes checked without being asked: %+v", report.Runtimes)
	}
}

// R6: one success turns IPv6 on; only three misses in a row turn it off.
func TestIPv6StateDoesNotFlap(t *testing.T) {
	var s ipv6State
	steps := []struct {
		checked, open       bool
		wantReady, wantFlip bool
	}{
		{false, false, false, false}, // an older peer: no verdict
		{true, true, true, true},     // first success
		{true, false, true, false},   // one miss on Wi-Fi
		{true, false, true, false},   // two
		{true, true, true, false},    // back: the count resets
		{true, false, true, false},
		{true, false, true, false},
		{false, false, true, false}, // no verdict in between changes nothing
		{true, false, false, true},  // the third miss in a row
		{true, true, true, true},
	}
	for i, st := range steps {
		ready, flip := s.observe(st.checked, st.open)
		if ready != st.wantReady || flip != st.wantFlip {
			t.Fatalf("step %d: ready %v changed %v, want %v %v", i, ready, flip, st.wantReady, st.wantFlip)
		}
	}
}
