package languard

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func newTestGuard(t *testing.T, opts Options) *Guard {
	t.Helper()
	if opts.OnLink == nil {
		opts.OnLink = func() []netip.Prefix {
			return []netip.Prefix{netip.MustParsePrefix("2001:db8:1::/64"), netip.MustParsePrefix("192.0.2.0/24")}
		}
	}
	g, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestSourceAllowed(t *testing.T) {
	g := newTestGuard(t, Options{
		AllowFrom: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")},
		PeerAddr:  func(a netip.Addr) bool { return a == netip.MustParseAddr("203.0.113.9") },
	})
	for _, tc := range []struct {
		addr string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"192.168.1.20", true},
		{"10.77.0.2", true},
		{"172.17.0.2", true},
		{"fe80::1", true},
		{"fd12:3456::1", true},
		{"100.101.102.103", true}, // Tailscale
		{"2001:db8:1::42", true},  // global IPv6 on this machine's own network
		{"::ffff:192.168.1.20", true},
		{"198.51.100.10", true}, // allowed by the owner
		{"203.0.113.9", true},   // an approved machine beyond the LAN
		{"203.0.113.10", false}, // the internet
		{"2001:db8:2::42", false},
		{"8.8.8.8", false},
	} {
		if got := g.SourceAllowed(netip.MustParseAddr(tc.addr)); got != tc.want {
			t.Errorf("SourceAllowed(%s) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

func TestHostAllowed(t *testing.T) {
	g := newTestGuard(t, Options{AllowHosts: []string{"jag.example.org"}})
	for _, tc := range []struct {
		host string
		want bool
	}{
		{"ai.local", true},
		{"jaggery.local:7373", true},
		{"AI.LOCAL.", true},
		{"nas.local", true},
		{"desktop", true},
		{"desktop:7373", true},
		{"nas.lan", true},
		{"nas.home.arpa", true},
		{"localhost:7373", true},
		{"192.0.2.10", true},
		{"192.0.2.10:7373", true},
		{"[fe80::1]:7373", true},
		{"[2001:db8::1]", true},
		{"", true},
		{"jag.example.org", true},
		{"rebind.example.com", false},
		{"rebind.example.com:7373", false},
		{"192.0.2.10.nip.io", false},
		{"local.example.com", false},
	} {
		if got := g.HostAllowed(tc.host); got != tc.want {
			t.Errorf("HostAllowed(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestEnvAllowLists(t *testing.T) {
	t.Setenv(EnvAllowFrom, "198.51.100.0/24, 203.0.113.7")
	t.Setenv(EnvAllowHosts, "jag.example.org")
	g := newTestGuard(t, Options{})
	if !g.SourceAllowed(netip.MustParseAddr("198.51.100.200")) || !g.SourceAllowed(netip.MustParseAddr("203.0.113.7")) {
		t.Error("addresses from the environment not allowed")
	}
	if g.SourceAllowed(netip.MustParseAddr("203.0.113.8")) {
		t.Error("a single allowed address let its neighbour in")
	}
	if !g.HostAllowed("jag.example.org:7373") {
		t.Error("host from the environment not allowed")
	}

	t.Setenv(EnvAllowFrom, "not-an-address")
	if _, err := New(Options{}); err == nil {
		t.Error("a bad prefix was accepted")
	}
}

// TestWrap is what a web page can and can't do to the agent.
func TestWrap(t *testing.T) {
	var logs []string
	g := newTestGuard(t, Options{Logf: func(f string, a ...any) { logs = append(logs, f) }})
	served := 0
	h := g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { served++ }))

	for _, tc := range []struct {
		name    string
		method  string
		remote  string
		host    string
		headers map[string]string
		want    int
	}{
		{"curl on the LAN", "POST", "192.0.2.20:5000", "ai.local", nil, 200},
		{"agent app by IP", "POST", "192.0.2.20:5000", "192.0.2.10:7373", nil, 200},
		{"the page itself", "POST", "192.0.2.20:5000", "jaggery.local",
			map[string]string{"Origin": "http://jaggery.local", "Sec-Fetch-Site": "same-origin"}, 200},
		{"a web page posting cross-origin", "POST", "192.0.2.20:5000", "ai.local",
			map[string]string{"Origin": "https://evil.example.com", "Sec-Fetch-Site": "cross-site"}, 403},
		{"an old browser posting cross-origin", "POST", "192.0.2.20:5000", "ai.local",
			map[string]string{"Origin": "https://evil.example.com"}, 403},
		{"DNS rebinding reads", "GET", "192.0.2.20:5000", "rebind.example.com",
			map[string]string{"Sec-Fetch-Site": "same-origin"}, 403},
		{"DNS rebinding drives /mcp", "POST", "192.0.2.20:5000", "rebind.example.com:7373",
			map[string]string{"Origin": "http://rebind.example.com:7373", "Sec-Fetch-Site": "same-origin"}, 403},
		{"the internet", "GET", "203.0.113.5:5000", "ai.local", nil, 403},
		{"a cross-origin GET is served but unreadable (no CORS)", "GET", "192.0.2.20:5000", "ai.local",
			map[string]string{"Sec-Fetch-Site": "cross-site"}, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/v1/chat/completions", strings.NewReader("{}"))
			r.RemoteAddr = tc.remote
			r.Host = tc.host
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if w.Header().Get("X-Frame-Options") != "DENY" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Error("security headers missing")
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Error("CORS header set")
			}
		})
	}
	if len(logs) == 0 {
		t.Error("refusals weren't logged")
	}
}

// Requests for an app name pass the source and Host checks, then go to the
// app with no cross-origin check and no added headers.
func TestWrapForwardsAppNames(t *testing.T) {
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.WriteHeader(http.StatusOK)
	})
	g := newTestGuard(t, Options{Forward: func(r *http.Request) http.Handler {
		if strings.HasPrefix(r.Host, "chat.local") {
			return app
		}
		return nil
	}})
	own := g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	do := func(method, host, from string, crossSite bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://"+host+"/api", nil)
		req.Host = host
		req.RemoteAddr = from + ":5000"
		if crossSite {
			req.Header.Set("Origin", "https://evil.example.com")
			req.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		rec := httptest.NewRecorder()
		own.ServeHTTP(rec, req)
		return rec
	}
	rec := do(http.MethodPost, "chat.local", "192.0.2.5", true)
	if rec.Code != http.StatusOK || rec.Header().Get("X-Frame-Options") != "SAMEORIGIN" || rec.Header().Get("Referrer-Policy") != "" {
		t.Errorf("app name: %d %v", rec.Code, rec.Header())
	}
	if rec := do(http.MethodPost, "jaggery.local", "192.0.2.5", true); rec.Code != http.StatusForbidden || rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("Jaggery's own name lost its protection: %d %v", rec.Code, rec.Header())
	}
	if rec := do(http.MethodGet, "chat.local", "203.0.113.50", false); rec.Code != http.StatusForbidden {
		t.Errorf("an app name was served to the internet: %d", rec.Code)
	}
}
