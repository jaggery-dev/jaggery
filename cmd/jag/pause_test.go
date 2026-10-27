package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/inventory"
)

func TestPauseAndResume(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JAGGERY_STATE_DIR", dir)
	if pausedHere() != nil {
		t.Fatal("a new machine is paused")
	}
	if err := aiCmd([]string{"pause", "--for", "2h"}); err != nil {
		t.Fatal(err)
	}
	p := pausedHere()
	if p == nil || p.Until == nil || time.Until(*p.Until) < 119*time.Minute {
		t.Fatalf("after pause --for 2h: %+v", p)
	}
	if loadPause(dir, time.Now().Add(3*time.Hour)) != nil {
		t.Error("the pause should have run out after 2 hours")
	}
	if err := aiCmd([]string{"resume"}); err != nil || pausedHere() != nil {
		t.Fatalf("resume: %v, paused %+v", err, pausedHere())
	}
	if err := aiCmd([]string{"pause"}); err != nil {
		t.Fatal(err)
	}
	if p := pausedHere(); p == nil || p.Until != nil {
		t.Fatalf("pause without --for should last until resumed: %+v", p)
	}
	for _, args := range [][]string{{"pause", "--for", "-1h"}, {"pause", "desktop"}, {"stop"}, {"resume", "now"}} {
		if err := aiCmd(args); err == nil {
			t.Errorf("jag ai %v should be refused", args)
		}
	}
}

// A model only a paused machine has is refused with a message that says
// why, rather than "no machine has it".
func TestRouterRefusesAModelOnlyAPausedMachineHas(t *testing.T) {
	dir, a := houseWithModels(t)
	saved := approvalList
	approvalList = a
	defer func() { approvalList = saved }()

	snap, _ := house.Load(dir)
	until := time.Now().Add(time.Hour)
	snap.Peers[0].Inventory.AIPaused = &inventory.Pause{Since: time.Now(), Until: &until}
	if err := house.Save(dir, snap); err != nil {
		t.Fatal(err)
	}

	store, _ := home.Open(t.TempDir())
	mux := http.NewServeMux()
	registerRouter(mux, newIdentity(t, "desktop"), store)
	body, _ := json.Marshal(map[string]any{"model": "llama3.2:3b", "messages": []any{}})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "laptop, where AI requests are paused") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}
