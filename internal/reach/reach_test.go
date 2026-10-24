package reach

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/airuntime"
)

func TestDialSeesOpenAndClosedPorts(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	open := ln.Addr().(*net.TCPAddr).Port

	closedLn, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := closedLn.Addr().(*net.TCPAddr).Port
	closedLn.Close()

	checks := Dial(context.Background(), netip.MustParseAddr("127.0.0.1"), []int{open, closed}, time.Second)
	if !checks[0].Open || checks[1].Open {
		t.Fatalf("got %+v, want first open and second closed", checks)
	}
	r := &Report{Checks: checks}
	if b := r.Blocked(); len(b) != 1 || b[0] != closed {
		t.Fatalf("blocked = %v, want [%d]", b, closed)
	}
}

func TestAllowedOnlyPassesTheAgentsPorts(t *testing.T) {
	got := Allowed([]int{22, 7373, 3306, 80}, 7373, 7374, 80)
	if len(got) != 2 || got[0] != 7373 || got[1] != 80 {
		t.Fatalf("got %v, want [7373 80]", got)
	}
}

func TestFixSpeaksTheFirewallsDialect(t *testing.T) {
	if f := Fix(UFW, 7374, "192.0.2.0/24"); f != "sudo ufw allow from 192.0.2.0/24 to any port 7374 proto tcp comment 'jaggery'" {
		t.Errorf("ufw: %q", f)
	}
	if f := Fix(Firewalld, 80, "10.0.0.0/24"); !strings.Contains(f, "source address=10.0.0.0/24 port port=80") {
		t.Errorf("firewalld: %q", f)
	}
	if f := Fix(Unknown, 80, ""); !strings.Contains(f, "port 80") || !strings.Contains(f, "local network") {
		t.Errorf("unknown: %q", f)
	}
}

func TestBlockedOnNoReportIsNothing(t *testing.T) {
	var r *Report
	if r.Blocked() != nil {
		t.Fatal("no report means nothing known to be blocked")
	}
}

func TestSubnetOfLoopback(t *testing.T) {
	if got := SubnetOf(netip.MustParseAddr("127.0.0.1")); got != "127.0.0.0/8" {
		t.Fatalf("got %q, want 127.0.0.0/8", got)
	}
}

// fakeRuntime serves body at path and returns its port.
func fakeRuntime(t *testing.T, path, body string) int {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return int(netip.MustParseAddrPort(srv.Listener.Addr().String()).Port())
}

func TestCheckRuntimesFindsAnExposedOllama(t *testing.T) {
	ollama := fakeRuntime(t, "/api/tags", `{"models":[{"name":"llama3.2:3b"}]}`)
	other := fakeRuntime(t, "/v1/models", `<html>some other app</html>`)
	ports := map[airuntime.Kind]int{airuntime.Ollama: ollama, airuntime.LlamaCpp: other}

	checks := CheckRuntimes(context.Background(), netip.MustParseAddr("127.0.0.1"),
		[]airuntime.Kind{airuntime.Ollama, airuntime.LlamaCpp, "made-up"}, ports)
	if len(checks) != 2 {
		t.Fatalf("got %d checks, want 2 (unknown kinds dropped): %+v", len(checks), checks)
	}
	if !checks[0].Exposed || checks[0].Kind != airuntime.Ollama || checks[0].Port != ollama {
		t.Errorf("Ollama: got %+v, want exposed", checks[0])
	}
	if checks[1].Exposed {
		t.Errorf("an unrelated app on llama.cpp's port was taken for llama.cpp: %+v", checks[1])
	}
	r := &Report{Runtimes: checks}
	if got := r.Exposed(); len(got) != 1 || got[0].Kind != airuntime.Ollama {
		t.Errorf("Exposed() = %+v", got)
	}
}

func TestRuntimeFixBindsToLocalhost(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		if fix := RuntimeFix(airuntime.Ollama, goos); !strings.Contains(fix, "OLLAMA_HOST") || !strings.Contains(fix, "127.0.0.1") {
			t.Errorf("%s: Ollama fix %q does not bind it to localhost", goos, fix)
		}
	}
	for _, k := range []airuntime.Kind{airuntime.LMStudio, airuntime.LlamaCpp} {
		if RuntimeFix(k, "linux") == "" {
			t.Errorf("no fix for %s", k)
		}
	}
}

// The IPv6 dial-back only goes to link-local addresses: a caller can't aim
// it anywhere else.
func TestDialIPv6OnlyDialsLinkLocal(t *testing.T) {
	checks := DialIPv6(context.Background(), []string{"2001:db8::1", "192.0.2.65", "not an address"}, []string{"lo"}, []int{80}, 100*time.Millisecond)
	if len(checks) != 1 || checks[0].Open || checks[0].Error != "" {
		t.Fatalf("got %+v: nothing should have been dialled", checks)
	}
}

func TestFixForIPv6(t *testing.T) {
	if fix := Fix(UFW, 80, "fe80::/10"); !strings.Contains(fix, "from fe80::/10 to any port 80") {
		t.Errorf("ufw: %s", fix)
	}
	if fix := Fix(Firewalld, 80, "fe80::/10"); !strings.Contains(fix, "family=ipv6") {
		t.Errorf("firewalld: %s", fix)
	}
}
