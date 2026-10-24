package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/airuntime"
	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/inventory"
	"github.com/jaggery-dev/jaggery/internal/reach"
	"github.com/jaggery-dev/jaggery/internal/update"
)

func snapWith(r *reach.Report, st *update.Status) house.Snapshot {
	return house.Snapshot{Self: house.Machine{Inventory: inventory.Report{Inbound: r, Update: st}}}
}

func TestDoctorChecksFromTheSnapshot(t *testing.T) {
	blocked := &reach.Report{CheckedBy: "pi", CheckedAt: time.Now(), Checks: []reach.Check{
		{Port: 7374, Open: false, Fix: "sudo ufw allow from 192.0.2.0/24 to any port 7374 proto tcp"},
		{Port: 80, Open: true},
	}, Runtimes: []reach.RuntimeCheck{{Kind: airuntime.Ollama, Port: 11434, Exposed: true, Fix: "OLLAMA_HOST=127.0.0.1"}}}

	if r := checkInbound(snapWith(blocked, nil)); r.state != fail || !strings.Contains(r.fix, "ufw allow") {
		t.Errorf("blocked port: %+v", r)
	}
	if r := checkExposure(snapWith(blocked, nil)); r.state != fail || !strings.Contains(r.fix, "OLLAMA_HOST") {
		t.Errorf("exposed runtime: %+v", r)
	}
	if r := checkInbound(snapWith(nil, nil)); r.state != warn {
		t.Errorf("not checked yet should warn, not fail: %+v", r)
	}
	if r := checkUpdates(snapWith(nil, &update.Status{Policy: update.Auto, Channel: "beta", Error: "no GitHub token"})); r.state != warn {
		t.Errorf("update error: %+v", r)
	}
	if r := checkUpdates(snapWith(nil, &update.Status{Policy: update.Auto, Channel: "beta"})); r.state != pass {
		t.Errorf("up to date: %+v", r)
	}
}

// The dial-back check (B1) connects to the TLS port and hangs up; the
// server's resulting "TLS handshake error ... EOF" is noise. Real errors pass.
func TestQuietLogDropsOnlyDialBackNoise(t *testing.T) {
	var buf bytes.Buffer
	q := quietWriter{&buf}
	_, _ = q.Write([]byte("2026/10/07 13:00:08 http: TLS handshake error from 192.0.2.63:42130: EOF\n"))
	_, _ = q.Write([]byte("2026/10/07 13:00:09 http: TLS handshake error from 10.0.0.9:5000: tls: bad certificate\n"))
	if got := buf.String(); strings.Contains(got, "EOF") || !strings.Contains(got, "bad certificate") {
		t.Fatalf("logged %q", got)
	}
}
