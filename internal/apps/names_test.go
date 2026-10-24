package apps

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"reflect"
	"testing"
	"time"
)

func TestCheckName(t *testing.T) {
	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{"chat", true},
		{"photos-2", true},
		{"a", true},
		{"", false},
		{"Chat", false},
		{"chat.local", false},
		{"house chat", false},
		{"-chat", false},
		{"chat-", false},
		{"café", false},
		{"jaggery", false},
		{"ai", false},
		{"a123456789012345678901234567890123456789012345678901234567890123", false},
	} {
		if err := CheckName(tc.name); (err == nil) != tc.ok {
			t.Errorf("CheckName(%q) = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
	if got := NormalName(" Chat.local "); got != "chat" {
		t.Errorf("NormalName = %q", got)
	}
}

func TestSetRemoveName(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(AddCustom(dir, Custom{Name: "Grafana", Port: 3001}))
	must(SetName(dir, "Open WebUI", "talk"))
	must(SetName(dir, "open webui", "chat")) // one name per app, any case
	must(SetName(dir, "Grafana", "graphs"))
	if err := SetName(dir, "Immich", "chat"); err == nil {
		t.Error("two apps on one machine got the same name")
	}
	if err := SetName(dir, "Immich", "Bad Name"); err == nil {
		t.Error("a bad name was saved")
	}
	names, err := LoadNames(dir)
	must(err)
	want := []Name{{App: "open webui", Name: "chat"}, {App: "Grafana", Name: "graphs"}}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}

	// Entries and names live in one file; changing one keeps the other.
	must(AddCustom(dir, Custom{Name: "Router", URL: "http://192.0.2.1"}))
	must(RemoveCustom(dir, "Grafana"))
	if names, _ := LoadNames(dir); len(names) != 2 {
		t.Errorf("changing entries lost names: %v", names)
	}
	must(RemoveName(dir, "Grafana"))
	if list, _ := LoadCustom(dir); len(list) != 1 || list[0].Name != "Router" {
		t.Errorf("removing a name changed the entries: %v", list)
	}
	if err := RemoveName(dir, "Grafana"); !errors.Is(err, ErrNoName) {
		t.Errorf("second remove: %v", err)
	}
}

func TestApplyNames(t *testing.T) {
	list := []App{
		{Name: "Open WebUI", Port: 3000},
		{Name: "Router", URL: "http://192.0.2.1"},
		{Name: "Jellyfin", Port: 8096},
	}
	got := ApplyNames(list, []Name{
		{App: "open webui", Name: "chat"},
		{App: "Router", Name: "router"},  // a link can't be named
		{App: "Jellyfin", Name: "Films"}, // hand-edited, not a name
		{App: "Immich", Name: "photos"},  // not on this machine
	})
	if got[0].LocalName != "chat" || got[1].LocalName != "" || got[2].LocalName != "" {
		t.Errorf("ApplyNames = %+v", got)
	}
	if list[0].LocalName != "" {
		t.Error("ApplyNames changed its input")
	}
}

// A new or removed name shows at once, without waiting out the cache.
func TestCacheNamesAtOnce(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	var names []Name
	c := Cache{TTL: time.Hour,
		Custom: func() []Custom { return []Custom{{Name: "Grafana", Port: port}} },
		Names:  func() []Name { return names },
	}
	addr, now := netip.MustParseAddr("127.0.0.1"), time.Now()
	if got := c.Get(context.Background(), addr, now); len(got) != 1 || got[0].LocalName != "" {
		t.Fatalf("before naming: %+v", got)
	}
	names = []Name{{App: "Grafana", Name: "graphs"}}
	if got := c.Get(context.Background(), addr, now); got[0].LocalName != "graphs" {
		t.Errorf("after naming: %+v", got)
	}
	names = nil
	if got := c.Get(context.Background(), addr, now); got[0].LocalName != "" {
		t.Errorf("after removing: %+v", got)
	}
}
