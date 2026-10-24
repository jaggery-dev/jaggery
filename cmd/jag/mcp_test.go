package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/inventory"
	"github.com/jaggery-dev/jaggery/internal/secure"
	"github.com/jaggery-dev/jaggery/internal/shares"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Two machines: "desktop" takes the MCP calls and holds a read-only share,
// docs; "nas" holds a writable share, summaries, reached over mutual TLS.
// This is S2's done-when, in one process.
func TestMCPSharesAcrossTheHouse(t *testing.T) {
	desktop, nas := newIdentity(t, "desktop"), newIdentity(t, "nas")
	desktopDir, nasDir := t.TempDir(), t.TempDir()
	t.Setenv("JAGGERY_STATE_DIR", desktopDir)

	docs, sums := t.TempDir(), t.TempDir()
	writeFiles(t, docs, map[string]string{
		"notes.md":   "# Notes\nbuy a NAS\n",
		"photo.png":  "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR",
		"report.pdf": "%PDF-1.7\n%\xe2\xe3\xcf\xd3\n",
		".env":       "TOKEN=nope",
	})
	if _, err := shares.Add(desktopDir, docs, "docs", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := shares.Add(nasDir, sums, "summaries", true, time.Now()); err != nil {
		t.Fatal(err)
	}

	// The nas's secured listener, serving its own shares.
	tlsCfg, err := secure.ServerConfig(nas, approveAll{})
	if err != nil {
		t.Fatal(err)
	}
	holder := httptest.NewUnstartedServer(filesHandler(func() (string, error) { return nasDir, nil }, func(*http.Request) string { return "desktop" }))
	holder.TLS = tlsCfg
	holder.StartTLS()
	defer holder.Close()
	saved := peerFilesAddr
	peerFilesAddr = func(home.Peer) string { return holder.Listener.Addr().String() }
	defer func() { peerFilesAddr = saved }()

	// The desktop knows the nas: approved, and its inventory lists the share.
	store, err := home.Open(desktopDir)
	if err != nil {
		t.Fatal(err)
	}
	obs := home.Observation{MachineID: nas.ID, Name: "nas", Addrs: []netip.Addr{netip.MustParseAddr("127.0.0.1")}}
	if _, err := store.Adopt(obs, nas.PublicKey, home.Approved, time.Now()); err != nil {
		t.Fatal(err)
	}
	snap := house.Snapshot{
		Self: house.Machine{MachineID: desktop.ID, Name: "desktop", Self: true, Reachable: true},
		Peers: []house.Machine{{MachineID: nas.ID, Name: "nas", Trust: home.Approved, Reachable: true,
			Inventory: inventory.Report{Shares: []shares.Info{{Name: "summaries", Writable: true}}}}},
	}
	if err := house.Save(desktopDir, snap); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	registerMCP(mux, desktop, store)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL + mcpPath}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	callTool := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return res
	}
	text := func(res *mcp.CallToolResult, i int) string {
		t.Helper()
		if len(res.Content) <= i {
			t.Fatalf("result has %d blocks", len(res.Content))
		}
		tc, ok := res.Content[i].(*mcp.TextContent)
		if !ok {
			t.Fatalf("block %d is %T", i, res.Content[i])
		}
		return tc.Text
	}

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 6 {
		t.Fatalf("%d tools", len(tools.Tools))
	}

	got := text(callTool("list_shares", nil), 0)
	if !strings.Contains(got, "docs: on desktop, read-only") || !strings.Contains(got, "summaries: on nas, writable") {
		t.Fatalf("list_shares:\n%s", got)
	}

	if got := text(callTool("list_dir", map[string]any{"share": "docs"}), 0); strings.Contains(got, ".env") || !strings.Contains(got, "report.pdf") {
		t.Fatalf("list_dir:\n%s", got)
	}

	res := callTool("read_file", map[string]any{"share": "docs", "path": "notes.md"})
	if text(res, 1) != "# Notes\nbuy a NAS\n" {
		t.Fatalf("notes.md: %q", text(res, 1))
	}
	res = callTool("read_file", map[string]any{"share": "docs", "path": "photo.png"})
	if img, ok := res.Content[1].(*mcp.ImageContent); !ok || img.MIMEType != "image/png" {
		t.Fatalf("photo.png came back as %T", res.Content[1])
	}
	res = callTool("read_file", map[string]any{"share": "docs", "path": "report.pdf"})
	if r, ok := res.Content[1].(*mcp.EmbeddedResource); !ok || r.Resource.MIMEType != "application/pdf" || len(r.Resource.Blob) == 0 {
		t.Fatalf("report.pdf came back as %T", res.Content[1])
	}

	if res := callTool("read_file", map[string]any{"share": "docs", "path": ".env"}); !res.IsError {
		t.Fatal(".env was served")
	}
	// A missing file and a path that tries to climb out of the share: both
	// answers name the share and the path as given, and no OS error text.
	for _, c := range []struct{ share, path string }{
		{"docs", "missing.md"},
		{"docs", "../../etc/passwd"},
		{"summaries", "../../etc/passwd"}, // served by the nas
	} {
		res := callTool("read_file", map[string]any{"share": c.share, "path": c.path})
		want := `no such file in the share "` + c.share + `": ` + c.path
		if got := text(res, 0); !res.IsError || got != want {
			t.Fatalf("read_file %s %s: %q, want %q", c.share, c.path, got, want)
		}
	}

	res = callTool("write_file", map[string]any{"share": "docs", "path": "notes.md", "content": "x"})
	if !res.IsError || !strings.Contains(text(res, 0), "read-only") {
		t.Fatal("wrote to a read-only share")
	}

	// A write that crosses to the nas, twice: the second keeps the first.
	for _, body := range []string{"first", "second"} {
		res = callTool("write_file", map[string]any{"share": "summaries", "path": "week/notes.md", "content": body})
		if res.IsError {
			t.Fatalf("write to the nas: %s", text(res, 0))
		}
	}
	if b, _ := os.ReadFile(filepath.Join(sums, "week", "notes.md")); string(b) != "second" {
		t.Fatalf("the nas has %q", b)
	}
	if vs, _ := os.ReadDir(filepath.Join(sums, shares.VersionsDir, "week", "notes.md")); len(vs) != 1 {
		t.Fatalf("%d old versions on the nas", len(vs))
	}
	if got := text(callTool("read_file", map[string]any{"share": "summaries", "path": "week/notes.md"}), 1); got != "second" {
		t.Fatalf("read back from the nas: %q", got)
	}
	if got := text(callTool("search", map[string]any{"share": "docs", "query": "nas"}), 0); !strings.Contains(got, "notes.md:2: buy a NAS") {
		t.Fatalf("search:\n%s", got)
	}

	// Both machines logged the write to the nas: the desktop that took it,
	// and the nas that served it, with the app, the hashes and the version.
	var taken, served []shares.LogEntry
	if all, _ := shares.ReadLog(desktopDir, time.Now()); len(all) > 0 {
		for _, e := range all {
			if e.Op == shares.OpWrite && e.Share == "summaries" {
				taken = append(taken, e)
			}
		}
	}
	all, _ := shares.ReadLog(nasDir, time.Now())
	for _, e := range all {
		if e.Op == shares.OpWrite {
			served = append(served, e)
		}
	}
	if len(taken) != 2 || taken[0].ServedBy != "nas" || taken[0].App != "test-agent" {
		t.Fatalf("the desktop's log of the nas writes: %+v", taken)
	}
	if len(served) != 2 || served[1].Via != "desktop" || served[1].App != "test-agent" ||
		served[1].Path != "week/notes.md" || served[1].OldSHA256 == "" || served[1].NewSHA256 == "" || served[1].Version == "" {
		t.Fatalf("the nas's log of its writes: %+v", served)
	}
	if u := shares.Infos(nasDir, time.Now()); len(u) != 1 || u[0].Use == nil || u[0].Use.Writes != 2 || u[0].Use.Reads != 1 {
		t.Fatalf("the nas's use today: %+v", u)
	}

	// A web page in a browser can't drive the tools.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+mcpPath, strings.NewReader(`{}`))
	req.Header.Set("Origin", "http://attacker.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a cross-site request got %d", resp.StatusCode)
	}
}

func TestFindShareNamesAmbiguity(t *testing.T) {
	list := []houseShare{
		{Share: "docs", Machine: "desktop", Name: "docs@desktop"},
		{Share: "docs", Machine: "NAS", Name: "docs@nas"},
		{Share: "photos", Machine: "NAS", Name: "photos"},
	}
	if _, err := findShare(list, "docs"); err == nil || !strings.Contains(err.Error(), "docs@desktop or docs@nas") {
		t.Fatalf("ambiguous docs: %v", err)
	}
	if s, err := findShare(list, "docs@nas"); err != nil || s.Machine != "NAS" {
		t.Fatalf("docs@nas: %+v %v", s, err)
	}
	if _, err := findShare(list, "music"); err == nil {
		t.Fatal("found a share that doesn't exist")
	}
}
