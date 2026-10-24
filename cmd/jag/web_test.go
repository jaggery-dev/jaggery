package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestWebPortHintNamesTheFix(t *testing.T) {
	denied := &net.OpError{Op: "listen", Net: "tcp", Err: fmt.Errorf("bind: %w", syscall.EACCES)}
	hint := webPortHint(denied)
	if runtime.GOOS == "linux" && !strings.Contains(hint, "setcap cap_net_bind_service=+ep") {
		t.Errorf("linux permission hint lacks the setcap fix: %q", hint)
	}
	if !strings.Contains(hint, ":7373") {
		t.Errorf("hint should give the fallback address: %q", hint)
	}

	inUse := &net.OpError{Op: "listen", Net: "tcp", Err: fmt.Errorf("bind: %w", syscall.EADDRINUSE)}
	if hint := webPortHint(inUse); !strings.Contains(hint, "another program") {
		t.Errorf("in-use hint = %q", hint)
	}
}

// The web port must serve exactly what the agent port serves.
func TestServeWebSharesTheAgentHandler(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	server := &http.Server{Handler: mux}
	defer server.Close()

	// Port 0 picks a free port; find which by listening first and releasing.
	probe := httptest.NewUnstartedServer(nil)
	port := probe.Listener.Addr().(*net.TCPAddr).Port
	probe.Listener.Close()

	if err := serveWeb(server, port); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
