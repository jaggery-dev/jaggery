// Package apps finds the web apps a machine serves to the house: Open WebUI,
// Home Assistant, Jellyfin and the like. The dashboard lists them as the
// house's tools, each with a link and a line on what it is for.
//
// Detection follows airuntime's rule: a port answering is not enough, since
// default ports collide (3000 is Open WebUI's and AdGuard Home's, 8080 is
// everyone's). An app counts only if a path it always serves answers with a
// marker only it uses. Probes go to the machine's LAN address, not localhost,
// so an app is listed only if other devices in the house can actually open it.
package apps

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// App is one detected app, as the dashboard shows it.
type App struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Port        int    `json:"port"`
	// Hue is the app's colour on the dashboard, an oklch hue in degrees.
	Hue int `json:"hue,omitempty"`
	// Path is where a person should land, e.g. "/web" for Plex.
	Path string `json:"path,omitempty"`
	// URL is set for a link the owner added (jag apps add --url); Port is
	// then zero and the page opens URL as it is.
	URL string `json:"url,omitempty"`
	// Custom marks an app added or relabelled with jag apps.
	Custom bool `json:"custom,omitempty"`
	// LocalName is the short name the owner gave the app (jag apps name):
	// "chat" means the house opens it at chat.local.
	LocalName string `json:"local_name,omitempty"`
}

// Fingerprint says how to recognise one app.
type Fingerprint struct {
	Name        string
	Description string
	// Hue tints the app's tile on the dashboard (oklch, 0-359), fixed so
	// an app keeps its colour on every machine.
	Hue   int
	Ports []int
	// Probe is fetched on each port; Marker must appear in the response body
	// (case-insensitive) for the app to count.
	Probe  string
	Marker string
	// Open is where a person should land; "/" when empty.
	Open string
}

// Catalog is the apps Jaggery knows: a start on ROADMAP §4's fingerprint
// library. Open WebUI, LibreChat, Jellyfin and Home Assistant are checked
// against the running apps; the others come from what each app serves and
// are unverified. A wrong marker means an app is missed, not misnamed,
// since each is specific to its app.
var Catalog = []Fingerprint{
	{Name: "Open WebUI", Description: "Chat with the house's AI", Hue: 265, Ports: []int{3000, 8080}, Probe: "/api/config", Marker: `"name":"Open WebUI"`},
	// Matched on a key its config always has, not appTitle, which the
	// owner can rename (APP_TITLE).
	{Name: "LibreChat", Description: "Chat and agents on the house's AI models", Hue: 290, Ports: []int{3080}, Probe: "/api/config", Marker: `"discordLoginEnabled"`},
	{Name: "Home Assistant", Description: "Lights, heating, the doorbell", Hue: 220, Ports: []int{8123}, Probe: "/manifest.json", Marker: "Home Assistant"},
	{Name: "Jellyfin", Description: "Films, shows and music", Hue: 30, Ports: []int{8096}, Probe: "/System/Info/Public", Marker: `"ProductName":"Jellyfin Server"`},
	{Name: "Plex", Description: "Films, shows and music", Hue: 50, Ports: []int{32400}, Probe: "/identity", Marker: "machineIdentifier", Open: "/web"},
	{Name: "Immich", Description: "Photos from everyone's phones", Hue: 145, Ports: []int{2283}, Probe: "/", Marker: "<title>Immich</title>"},
	{Name: "AdGuard Home", Description: "Ad blocking for the Wi-Fi", Hue: 110, Ports: []int{3000, 8080}, Probe: "/", Marker: "<title>AdGuard Home</title>"},
	{Name: "Portainer", Description: "Manage Docker containers", Hue: 200, Ports: []int{9000}, Probe: "/", Marker: "<title>Portainer</title>"},
	{Name: "Syncthing", Description: "Keep folders in sync between machines", Hue: 240, Ports: []int{8384}, Probe: "/", Marker: "<title>Syncthing</title>"},
	{Name: "Paperless-ngx", Description: "Scanned letters and bills", Hue: 75, Ports: []int{8000}, Probe: "/accounts/login/", Marker: "Paperless-ngx"},
	{Name: "Audiobookshelf", Description: "Audiobooks and podcasts", Hue: 330, Ports: []int{13378}, Probe: "/", Marker: "<title>Audiobookshelf</title>"},
	{Name: "Vaultwarden", Description: "The family's passwords", Hue: 180, Ports: []int{8000, 8080}, Probe: "/", Marker: "<title>Vaultwarden Web</title>"},
	{Name: "Nextcloud", Description: "Files, calendars and contacts", Hue: 250, Ports: []int{8080, 8443}, Probe: "/status.php", Marker: `"productname":"Nextcloud"`},
	// Checked against n8n 2.43: every page, signed in or not, is the editor's
	// HTML with this title. Its /healthz says only {"status":"ok"}, which
	// plenty of apps do.
	{Name: "n8n", Description: "Automations between apps and services", Hue: 10, Ports: []int{5678}, Probe: "/", Marker: "<title>n8n.io"},
	// The six below were checked against the running apps (2026-10-21). Each
	// probe is the page itself, not "/", which only redirects there.
	{Name: "Navidrome", Description: "Music from the house's disks", Hue: 300, Ports: []int{4533}, Probe: "/app/", Marker: `og:site_name" content="Navidrome"`},
	{Name: "Uptime Kuma", Description: "Is everything up? Checks on the house's services", Hue: 160, Ports: []int{3001}, Probe: "/dashboard", Marker: `content="Uptime Kuma monitoring tool"`},
	// Forgejo began as a fork of Gitea and serves the same pages. Both keep
	// the sign-in page open when the owner makes everything else private,
	// and the author line in its head names the product even when the owner
	// renames the site (APP_NAME changes the title, not this).
	{Name: "Forgejo", Description: "The house's own Git server", Hue: 40, Ports: []int{3000}, Probe: "/user/login", Marker: `<meta name="author" content="Forgejo`},
	{Name: "Gitea", Description: "The house's own Git server", Hue: 130, Ports: []int{3000}, Probe: "/user/login", Marker: `<meta name="author" content="Gitea`},
	// /api/health says only database, version and commit; the sign-in page
	// has a body class only Grafana uses.
	{Name: "Grafana", Description: "Dashboards and graphs", Hue: 20, Ports: []int{3000}, Probe: "/login", Marker: "app-grafana"},
	// The owner can retitle Calibre-Web, so match a dialog its every page
	// carries instead of the title.
	{Name: "Calibre-Web", Description: "E-books from the house's library", Hue: 95, Ports: []int{8083}, Probe: "/login", Marker: `id="bookDetailsModal"`},
}

// Detect probes every fingerprint against addr and returns the apps found,
// one per port: when two apps share a default port, the first match wins.
func Detect(ctx context.Context, addr netip.Addr, catalog []Fingerprint) []App {
	client := &http.Client{
		Timeout: 2 * time.Second,
		// A login redirect is still the app; read the page it is on.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	type hit struct {
		order int
		app   App
	}
	var (
		mu   sync.Mutex
		hits []hit
		wg   sync.WaitGroup
	)
	for i, fp := range catalog {
		for _, port := range fp.Ports {
			wg.Add(1)
			go func(i int, fp Fingerprint, port int) {
				defer wg.Done()
				url := "http://" + net.JoinHostPort(addr.String(), strconv.Itoa(port)) + fp.Probe
				if !matches(ctx, client, url, fp.Marker) {
					return
				}
				mu.Lock()
				hits = append(hits, hit{i, App{Name: fp.Name, Description: fp.Description, Port: port, Hue: fp.Hue, Path: fp.Open}})
				mu.Unlock()
			}(i, fp, port)
		}
	}
	wg.Wait()

	sort.Slice(hits, func(a, b int) bool { return hits[a].order < hits[b].order })
	var out []App
	taken := map[int]bool{}
	for _, h := range hits {
		if taken[h.app.Port] {
			continue
		}
		taken[h.app.Port] = true
		out = append(out, h.app)
	}
	return out
}

func matches(ctx context.Context, c *http.Client, url, marker string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := c.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(body)), strings.ToLower(marker))
}

// Cache runs Detect at most once per TTL. Inventory is collected every few
// seconds for each peer that asks, and apps come and go far more slowly.
// A change to the owner's entries counts at once, without waiting out the TTL.
type Cache struct {
	TTL     time.Duration
	Catalog []Fingerprint
	// Custom, if set, returns the owner's entries (jag apps add).
	Custom func() []Custom
	// Names, if set, returns the names the owner gave apps (jag apps name).
	Names func() []Name

	mu     sync.Mutex
	at     time.Time
	addr   netip.Addr
	custom string
	found  []App
}

func (c *Cache) Get(ctx context.Context, addr netip.Addr, now time.Time) []App {
	var custom []Custom
	if c.Custom != nil {
		custom = c.Custom()
	}
	var names []Name
	if c.Names != nil {
		names = c.Names()
	}
	key := fmt.Sprint(custom)
	c.mu.Lock()
	defer c.mu.Unlock()
	if addr != c.addr || key != c.custom || c.at.IsZero() || now.Sub(c.at) >= c.TTL {
		c.found = Merge(Detect(ctx, addr, c.Catalog), custom, func(port int) bool { return PortAnswers(ctx, addr, port) })
		c.addr, c.at, c.custom = addr, now, key
	}
	// Names cost nothing to apply, so a new or removed one shows at once.
	return ApplyNames(c.found, names)
}
