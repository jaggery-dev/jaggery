package apps

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestCustomCheck(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    Custom
		ok   bool
	}{
		{"port", Custom{Name: "Grafana", Port: 3001}, true},
		{"port and path", Custom{Name: "Plex", Port: 32400, Path: "/web"}, true},
		{"link", Custom{Name: "Router", URL: "http://192.0.2.1"}, true},
		{"https link", Custom{Name: "NAS", URL: "https://nas.local:5001/"}, true},
		{"description only", Custom{Name: "Jellyfin", Description: "Family films"}, true},
		{"no name", Custom{Port: 3001}, false},
		{"long name", Custom{Name: "a very long name that goes on and on and on and on", Port: 1}, false},
		{"name on two lines", Custom{Name: "a\nb", Port: 1}, false},
		{"both", Custom{Name: "X", Port: 1, URL: "http://a"}, false},
		{"bad port", Custom{Name: "X", Port: 70000}, false},
		{"path without port", Custom{Name: "X", Path: "/web"}, false},
		{"relative path", Custom{Name: "X", Port: 1, Path: "web"}, false},
		{"javascript link", Custom{Name: "X", URL: "javascript:alert(1)"}, false},
		{"data link", Custom{Name: "X", URL: "data:text/html,hi"}, false},
		{"no host", Custom{Name: "X", URL: "http://"}, false},
	} {
		if err := tc.c.Check(); (err == nil) != tc.ok {
			t.Errorf("%s: Check() = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestAddRemoveCustom(t *testing.T) {
	dir := t.TempDir()
	if list, err := LoadCustom(dir); err != nil || list != nil {
		t.Fatalf("empty dir: %v, %v", list, err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(AddCustom(dir, Custom{Name: " Router ", URL: "http://192.0.2.1"}))
	must(AddCustom(dir, Custom{Name: "Grafana", Port: 3001}))
	must(AddCustom(dir, Custom{Name: "grafana", Port: 3002, Description: "Graphs"})) // replaces, any case
	if err := AddCustom(dir, Custom{Name: "Bad", URL: "ftp://x"}); err == nil {
		t.Error("a bad entry was saved")
	}
	list, err := LoadCustom(dir)
	must(err)
	want := []Custom{{Name: "grafana", Port: 3002, Description: "Graphs"}, {Name: "Router", URL: "http://192.0.2.1"}}
	if !reflect.DeepEqual(list, want) {
		t.Fatalf("got %+v, want %+v", list, want)
	}
	if fi, err := os.Stat(filepath.Join(dir, CustomFile)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode: %v %v", fi, err)
	}
	must(RemoveCustom(dir, "ROUTER"))
	if err := RemoveCustom(dir, "Router"); !errors.Is(err, ErrNoCustom) {
		t.Errorf("removing twice: %v", err)
	}
}

func TestMerge(t *testing.T) {
	detected := []App{
		{Name: "Jellyfin", Description: "Films, shows and music", Port: 8096, Hue: 30},
		{Name: "Open WebUI", Description: "Chat with the house's AI", Port: 3000, Hue: 265},
	}
	custom := []Custom{
		{Name: "Router", Description: "Wi-Fi settings", URL: "http://192.0.2.1"},
		{Name: "Grafana", Description: "Graphs", Port: 3001},
		{Name: "Off", Port: 4000}, // nothing answers
		{Name: "jellyfin", Description: "Family films"},
		{Name: "House chat", Port: 3000, Path: "/c/new"},
		{Name: "Broken", URL: "javascript:x"}, // hand-edited file: skipped
	}
	open := map[int]bool{3001: true}
	got := Merge(detected, custom, func(p int) bool { return open[p] })
	want := []App{
		{Name: "jellyfin", Description: "Family films", Port: 8096, Hue: 30, Custom: true},
		{Name: "House chat", Description: "Chat with the house's AI", Port: 3000, Hue: 265, Path: "/c/new", Custom: true},
		{Name: "Router", Description: "Wi-Fi settings", URL: "http://192.0.2.1", Custom: true},
		{Name: "Grafana", Description: "Graphs", Port: 3001, Custom: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	if !reflect.DeepEqual(detected[0].Name, "Jellyfin") {
		t.Error("Merge changed its input")
	}
}

// TestCacheCustom: an added entry counts at once, not after the TTL, and a
// port entry needs the port to answer.
func TestCacheCustom(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	var custom []Custom
	c := Cache{TTL: time.Hour, Custom: func() []Custom { return custom }}
	addr := netip.MustParseAddr("127.0.0.1")
	now := time.Now()
	if got := c.Get(context.Background(), addr, now); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	custom = []Custom{{Name: "Mine", Port: port}, {Name: "Gone", Port: freePort(t)}}
	got := c.Get(context.Background(), addr, now.Add(time.Second))
	if len(got) != 1 || got[0].Name != "Mine" || got[0].Port != port {
		t.Fatalf("got %+v, want Mine on %d", got, port)
	}
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()
	n, _ := strconv.Atoi(p)
	return n
}
