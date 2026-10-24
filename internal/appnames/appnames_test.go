package appnames

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/apps"
	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/inventory"
)

func machine(name, id string, self, reachable bool, addr string, list ...apps.App) house.Machine {
	m := house.Machine{Name: name, MachineID: id, Self: self, Reachable: reachable, Inventory: inventory.Report{Apps: list}}
	if addr != "" {
		m.Addrs = []netip.Addr{netip.MustParseAddr(addr)}
	}
	return m
}

func TestFromHouse(t *testing.T) {
	snap := house.Snapshot{
		Self: machine("desktop", "bbb", true, false, "",
			apps.App{Name: "Open WebUI", Port: 3000, LocalName: "chat"},
			apps.App{Name: "Router", URL: "http://192.0.2.1", LocalName: "router"},
			apps.App{Name: "Agent", Port: 7373, LocalName: "loop"},
			apps.App{Name: "Odd", Port: 3001, LocalName: "Not A Name"},
			apps.App{Name: "Grafana", Port: 3002}),
		Peers: []house.Machine{
			machine("nas", "ccc", false, true, "192.0.2.20", apps.App{Name: "Immich", Port: 2283, LocalName: "photos"},
				apps.App{Name: "Other chat", Port: 3000, LocalName: "chat"}),
			machine("laptop", "aaa", false, false, "192.0.2.30", apps.App{Name: "Notes", Port: 8000, LocalName: "notes"}),
			machine("pi", "aab", false, true, "192.0.2.40", apps.App{Name: "Paperless", Port: 8000, LocalName: "paper"}),
		},
	}
	got := FromHouse(snap, netip.MustParseAddr("192.0.2.10"), 7373, 7374)
	if want := []string{"chat", "paper", "photos"}; fmt.Sprint(got.Names()) != fmt.Sprint(want) {
		t.Fatalf("names = %v, want %v", got.Names(), want)
	}
	if c := got["chat"]; c.Machine != "desktop" || c.Addr.String() != "192.0.2.10:3000" {
		t.Errorf("chat = %+v; the machine whose id sorts first should win", c)
	}
	if p := got["photos"]; p.Addr.String() != "192.0.2.20:2283" || p.App != "Immich" {
		t.Errorf("photos = %+v", p)
	}
}

func TestLookup(t *testing.T) {
	tab := Table{"chat": {Name: "chat"}}
	for host, ok := range map[string]bool{
		"chat.local": true, "CHAT.local:80": true, "chat.local.": true, "chat.local:7373": true,
		"chat": false, "jaggery.local": false, "chat.example.com": false, "192.0.2.10": false,
	} {
		if _, got := tab.Lookup(host); got != ok {
			t.Errorf("Lookup(%q) = %v, want %v", host, got, ok)
		}
	}
}

// forwarding runs a Forwarder whose only name, chat, goes to app.
func forwarding(t *testing.T, app http.Handler) (front *httptest.Server, backend *httptest.Server) {
	t.Helper()
	backend = httptest.NewServer(app)
	t.Cleanup(backend.Close)
	addr := netip.MustParseAddrPort(strings.TrimPrefix(backend.URL, "http://"))
	f := NewForwarder(func() Table { return Table{"chat": {Name: "chat", App: "Open WebUI", Machine: "desktop", Addr: addr}} })
	front = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := f.Handler(r); h != nil {
			h.ServeHTTP(w, r)
			return
		}
		http.Error(w, "not an app name", http.StatusTeapot)
	}))
	t.Cleanup(front.Close)
	return front, backend
}

func get(t *testing.T, front *httptest.Server, host, path string, hdr ...string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, front.URL+path, nil)
	req.Host = host
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestForwardKeepsTheAppsHeadersAndSaysWhoAsked(t *testing.T) {
	var seen *http.Request
	front, _ := forwarding(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		http.SetCookie(w, &http.Cookie{Name: "token", Value: "abc"})
		fmt.Fprint(w, "hello from the app")
	}))
	resp := get(t, front, "chat.local", "/c/1?x=2", "X-Forwarded-For", "203.0.113.9")
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello from the app" {
		t.Fatalf("body = %q", body)
	}
	if resp.Header.Get("X-Frame-Options") != "SAMEORIGIN" || resp.Header.Get("Access-Control-Allow-Origin") != "*" || resp.Header.Get("Set-Cookie") == "" {
		t.Errorf("the app's headers weren't kept: %v", resp.Header)
	}
	if seen.Host != "chat.local" || seen.URL.RequestURI() != "/c/1?x=2" {
		t.Errorf("the app saw Host %q, path %q", seen.Host, seen.URL.RequestURI())
	}
	if seen.Header.Get("X-Forwarded-Host") != "chat.local" || seen.Header.Get("X-Forwarded-Proto") != "http" {
		t.Errorf("X-Forwarded-* = %v", seen.Header)
	}
	// What a client claims is dropped; the app hears the real address.
	if xff := seen.Header.Get("X-Forwarded-For"); xff != "127.0.0.1" {
		t.Errorf("X-Forwarded-For = %q, want only the real client", xff)
	}
	if seen.Header.Get(LoopHeader) == "" {
		t.Error("the forwarded request isn't marked")
	}
	if resp := get(t, front, "jaggery.local", "/"); resp.StatusCode != http.StatusTeapot {
		t.Errorf("jaggery.local was forwarded: %d", resp.StatusCode)
	}
}

func TestForwardStreamsAsItGoes(t *testing.T) {
	release := make(chan struct{})
	front, _ := forwarding(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintln(w, `{"token":"Hello"}`)
		w.(http.Flusher).Flush()
		<-release
		fmt.Fprintln(w, `{"token":" world"}`)
	}))
	resp := get(t, front, "chat.local", "/api/chat")
	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(resp.Body).ReadString('\n')
		line <- s
	}()
	select {
	case s := <-line:
		if !strings.Contains(s, "Hello") {
			t.Errorf("first line = %q", s)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the first chunk was held back until the answer ended")
	}
	close(release)
}

func TestForwardWebSocket(t *testing.T) {
	front, _ := forwarding(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			http.Error(w, "want an upgrade", http.StatusBadRequest)
			return
		}
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		rw.Flush()
		msg, _ := rw.ReadString('\n')
		fmt.Fprint(rw, "echo "+msg)
		rw.Flush()
	}))
	u, _ := url.Parse(front.URL)
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprint(conn, "GET /ws HTTP/1.1\r\nHost: chat.local\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status %d", resp.StatusCode)
	}
	fmt.Fprint(conn, "ping\n")
	if got, _ := br.ReadString('\n'); got != "echo ping\n" {
		t.Errorf("echo = %q", got)
	}
}

func TestForwardRefusesLoopsAndSaysWhenTheAppIsOff(t *testing.T) {
	front, backend := forwarding(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if resp := get(t, front, "chat.local", "/", LoopHeader, "1"); resp.StatusCode != http.StatusLoopDetected {
		t.Errorf("a looped request got %d", resp.StatusCode)
	}
	backend.Close()
	resp := get(t, front, "chat.local", "/")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "Open WebUI on desktop didn't answer") ||
		!strings.Contains(string(body), "http://desktop.local:") {
		t.Errorf("app off: %d %q", resp.StatusCode, body)
	}
}
