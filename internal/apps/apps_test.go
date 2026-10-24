package apps

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

// serve runs a fake app and returns the address and port to probe.
func serve(t *testing.T, routes map[string]string) (netip.Addr, int) {
	t.Helper()
	mux := http.NewServeMux()
	for path, body := range routes {
		body := body
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	ap := netip.MustParseAddrPort(srv.Listener.Addr().String())
	return ap.Addr(), int(ap.Port())
}

func TestDetectRecognisesAnAppByItsMarker(t *testing.T) {
	// The body Open WebUI really serves, trimmed.
	addr, port := serve(t, map[string]string{"/api/config": `{"status":true,"name":"Open WebUI","version":"0.11.4"}`})
	catalog := []Fingerprint{{Name: "Open WebUI", Description: "chat", Ports: []int{port}, Probe: "/api/config", Marker: `"name":"Open WebUI"`, Open: "/chat"}}

	got := Detect(context.Background(), addr, catalog)
	if len(got) != 1 || got[0].Name != "Open WebUI" || got[0].Port != port || got[0].Path != "/chat" {
		t.Fatalf("got %+v", got)
	}
}

// Any web app answers 200; only the marker makes it this app.
func TestDetectIgnoresAnUnrelatedPage(t *testing.T) {
	addr, port := serve(t, map[string]string{"/": "<title>Some other app</title>"})
	catalog := []Fingerprint{{Name: "Immich", Ports: []int{port}, Probe: "/", Marker: "<title>Immich</title>"}}
	if got := Detect(context.Background(), addr, catalog); len(got) != 0 {
		t.Fatalf("detected %+v on an unrelated page", got)
	}
}

func TestDetectListsOneAppPerPort(t *testing.T) {
	addr, port := serve(t, map[string]string{"/": "<title>Open WebUI</title><title>AdGuard Home</title>"})
	catalog := []Fingerprint{
		{Name: "first", Ports: []int{port}, Probe: "/", Marker: "Open WebUI"},
		{Name: "second", Ports: []int{port}, Probe: "/", Marker: "AdGuard Home"},
	}
	got := Detect(context.Background(), addr, catalog)
	if len(got) != 1 || got[0].Name != "first" {
		t.Fatalf("got %+v, want only the first catalog entry", got)
	}
}

func TestCatalogEntriesAreComplete(t *testing.T) {
	for _, fp := range Catalog {
		if fp.Name == "" || fp.Description == "" || len(fp.Ports) == 0 || fp.Probe == "" || fp.Marker == "" {
			t.Errorf("incomplete fingerprint: %+v", fp)
		}
	}
}

func TestCacheProbesOncePerTTL(t *testing.T) {
	hits := 0
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { hits++; _, _ = w.Write([]byte("marker")) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ap := netip.MustParseAddrPort(srv.Listener.Addr().String())

	c := Cache{TTL: time.Minute, Catalog: []Fingerprint{{Name: "x", Ports: []int{int(ap.Port())}, Probe: "/", Marker: "marker"}}}
	now := time.Now()
	c.Get(context.Background(), ap.Addr(), now)
	c.Get(context.Background(), ap.Addr(), now.Add(30*time.Second))
	if hits != 1 {
		t.Fatalf("probed %d times within the TTL, want 1", hits)
	}
	c.Get(context.Background(), ap.Addr(), now.Add(2*time.Minute))
	if hits != 2 {
		t.Fatalf("probed %d times after the TTL, want 2", hits)
	}
}

// LibreChat's real /api/config (2026-10-09, trimmed).
func TestLibreChatIsRecognised(t *testing.T) {
	addr, port := serve(t, map[string]string{"/api/config": `{"appTitle":"LibreChat","discordLoginEnabled":false,"facebookLoginEnabled":false}`})
	var fp Fingerprint
	for _, f := range Catalog {
		if f.Name == "LibreChat" {
			fp = f
		}
	}
	fp.Ports = []int{port}
	if got := Detect(context.Background(), addr, []Fingerprint{fp}); len(got) != 1 || got[0].Name != "LibreChat" {
		t.Fatalf("got %+v", got)
	}
}

// n8n's real start page (2.43.7, 2026-10-18, trimmed): the same HTML whether
// or not anyone is signed in.
const n8nPage = `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8" />` +
	`<meta name="n8n:config:rest-endpoint" content="cmVzdA==">` +
	`<title>n8n.io - Workflow Automation</title></head><body><div id="app"></div></body></html>`

func n8nFingerprint(t *testing.T, port int) Fingerprint {
	t.Helper()
	for _, f := range Catalog {
		if f.Name == "n8n" {
			f.Ports = []int{port}
			return f
		}
	}
	t.Fatal("n8n is not in the catalog")
	return Fingerprint{}
}

func TestN8nIsRecognised(t *testing.T) {
	addr, port := serve(t, map[string]string{"/": n8nPage})
	got := Detect(context.Background(), addr, []Fingerprint{n8nFingerprint(t, port)})
	if len(got) != 1 || got[0].Name != "n8n" || got[0].Description != "Automations between apps and services" {
		t.Fatalf("got %+v", got)
	}
}

// Another app on n8n's port, even one that names n8n in its text, is not n8n.
func TestN8nIgnoresAnotherAppOnItsPort(t *testing.T) {
	addr, port := serve(t, map[string]string{
		"/":        `<title>Workflow dashboard</title><p>Import your n8n workflows here.</p>`,
		"/healthz": `{"status":"ok"}`,
	})
	if got := Detect(context.Background(), addr, []Fingerprint{n8nFingerprint(t, port)}); len(got) != 0 {
		t.Fatalf("detected %+v on an unrelated page", got)
	}
}

// onPort is the whole catalog with every app expected on one port, so a test
// sees which app a page would be taken for.
func onPort(port int) []Fingerprint {
	out := make([]Fingerprint, len(Catalog))
	for i, fp := range Catalog {
		fp.Ports = []int{port}
		out[i] = fp
	}
	return out
}

// Pages the apps really served (2026-10-21, trimmed): Navidrome 0.64.2,
// Uptime Kuma 1.23, Forgejo 11.0.16, Gitea 28.2.0, Grafana 13.2.4 and
// Calibre-Web from linuxserver.io, each fresh in Docker.
func TestRealPagesAreRecognisedAsTheirApp(t *testing.T) {
	for _, tc := range []struct {
		want   string
		routes map[string]string
	}{
		{"Navidrome", map[string]string{"/app/": `<meta property="og:site_name" content="Navidrome">` +
			`<title>Navidrome</title>`}},
		{"Uptime Kuma", map[string]string{"/dashboard": `<meta name="description" content="Uptime Kuma monitoring tool" />` +
			`<title>Uptime Kuma</title>`}},
		// Renamed by the owner: the title no longer says Forgejo, the author line does.
		{"Forgejo", map[string]string{"/user/login": `<html lang="en-US" data-theme="forgejo-auto"><title>Sign in - House code</title>` +
			`<meta name="author" content="Forgejo – Beyond coding. We forge.">`}},
		{"Gitea", map[string]string{"/user/login": `<html lang="en-US" data-theme="gitea-auto"><title>Sign In - House code</title>` +
			`<meta name="author" content="Gitea - Git with a cup of tea">`}},
		{"Grafana", map[string]string{"/login": `<title>Grafana</title><body class="theme-light app-grafana">`,
			"/api/health": `{"database":"ok","version":"13.2.4"}`}},
		{"Calibre-Web", map[string]string{"/login": `<title>Family books | Login</title>` +
			`<a class="navbar-brand" href="/">Family books</a><div class="modal fade" id="bookDetailsModal">`}},
	} {
		t.Run(tc.want, func(t *testing.T) {
			addr, port := serve(t, tc.routes)
			got := Detect(context.Background(), addr, onPort(port))
			if len(got) != 1 || got[0].Name != tc.want {
				t.Fatalf("got %+v, want only %s", got, tc.want)
			}
		})
	}
}

// Pages that mention an app, or answer its probe path, are not that app.
func TestPagesThatOnlyMentionAnAppAreNotIt(t *testing.T) {
	addr, port := serve(t, map[string]string{
		"/":           `<title>Links</title><p>Grafana, Navidrome, Uptime Kuma, Gitea and Calibre-Web are on the NAS.</p>`,
		"/app/":       `<title>Navidrome</title>`,
		"/dashboard":  `<title>Uptime Kuma</title>`,
		"/user/login": `<title>Sign in - Gitea</title><meta name="description" content="Forgejo">`,
		"/login":      `<title>Grafana</title><title>Calibre-Web | Login</title>`,
		"/api/health": `{"database":"ok","version":"13.2.4"}`,
	})
	if got := Detect(context.Background(), addr, onPort(port)); len(got) != 0 {
		t.Fatalf("detected %+v on pages that only name the apps", got)
	}
}
