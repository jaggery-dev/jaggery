package main

import (
	"strings"
	"testing"

	"github.com/jaggery-dev/jaggery/internal/apps"
	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/inventory"
)

func TestStraightQuotes(t *testing.T) {
	if err := straightQuotes("add", []string{"House chat", "--port", "3000"}); err != nil {
		t.Errorf("straight quotes refused: %v", err)
	}
	err := straightQuotes("add", []string{"“House", "Finance“", "--port", "8080", "--description", "‘Bills’"})
	if err == nil {
		t.Fatal("curly quotes accepted")
	}
	if want := `jag apps add "House Finance" --port 8080 --description 'Bills'`; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q doesn't suggest %q", err, want)
	}
}

func TestNameTaken(t *testing.T) {
	snap := house.Snapshot{
		Self: house.Machine{Name: "desktop", Self: true, Inventory: inventory.Report{Hostname: "desktop", Apps: []apps.App{
			{Name: "Open WebUI", Port: 3000, LocalName: "chat"},
			{Name: "Grafana", Port: 3001},
		}}},
		Peers: []house.Machine{{Name: "nas", Inventory: inventory.Report{Hostname: "nas", Apps: []apps.App{
			{Name: "Immich", Port: 2283, LocalName: "photos"},
		}}}},
	}
	for _, tc := range []struct {
		app, name, want string
	}{
		{"Grafana", "graphs", ""},
		{"Open WebUI", "chat", ""}, // its own name again
		{"Open WebUI", "talk", ""}, // a new name for it
		{"Grafana", "chat", "on this machine"},
		{"Grafana", "photos", "Immich on nas"},
		{"Grafana", "nas", "the machine nas"},
		{"Grafana", "desktop", "the machine desktop"},
	} {
		got := nameTaken(snap, tc.app, tc.name)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("nameTaken(%q, %q) = %q, want %q", tc.app, tc.name, got, tc.want)
		}
	}

	snap.Peers[0].Inventory.Apps[0].LocalName = "chat"
	if c := nameClashes(snap); len(c["chat"]) != 2 || len(c["graphs"]) != 0 {
		t.Errorf("nameClashes = %v", c)
	}
}
