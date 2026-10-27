package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/airuntime"
)

func fastKeepalive(t *testing.T) {
	old := keepaliveEvery
	keepaliveEvery = 10 * time.Millisecond
	t.Cleanup(func() { keepaliveEvery = old })
}

// While the runtime loads, the client gets 200 and comments; then the
// reply passes through untouched.
func TestWaitingStreamKeepsTheClientWaiting(t *testing.T) {
	fastKeepalive(t)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(60 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[]}\n\ndata: [DONE]\n\n"))
	}))
	defer slow.Close()

	rec := httptest.NewRecorder()
	s := newWaitingStream(rec, "loading m on desktop")
	err := proxyStream(t.Context(), s, slow.URL, []byte(`{}`))
	s.finish(err)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	body := rec.Body.String()
	if n := strings.Count(body, ": loading m on desktop\n\n"); n < 2 {
		t.Errorf("want comments while waiting, got %d in %q", n, body)
	}
	if !strings.HasSuffix(body, "data: {\"choices\":[]}\n\ndata: [DONE]\n\n") {
		t.Errorf("the reply should follow the comments untouched: %q", body)
	}
	time.Sleep(30 * time.Millisecond)
	if rec.Body.String() != body {
		t.Error("comments kept coming after the reply")
	}
}

// An error reply can't change the status any more, so it arrives as one
// data event in the OpenAI error shape.
func TestWaitingStreamTurnsAnErrorIntoAnEvent(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("{\n  \"error\": {\"message\": \"model failed to load\"}\n}\n"))
	}))
	defer failing.Close()

	rec := httptest.NewRecorder()
	s := newWaitingStream(rec, "loading m on desktop")
	s.finish(proxyStream(t.Context(), s, failing.URL, []byte(`{}`)))
	if want := "data: {\"error\":{\"message\":\"model failed to load\"}}\n\n"; !strings.HasSuffix(rec.Body.String(), want) {
		t.Fatalf("got %q, want it to end %q", rec.Body.String(), want)
	}

	rec = httptest.NewRecorder()
	s = newWaitingStream(rec, "loading m on desktop")
	s.finish(errors.New("desktop is not a reachable approved peer"))
	if !strings.Contains(rec.Body.String(), `data: {"error":{"message":"desktop is not a reachable approved peer","type":"jaggery_error"}}`) {
		t.Fatalf("got %q", rec.Body.String())
	}
}

func TestLoadTracker(t *testing.T) {
	l := &loadTracker{since: map[string]time.Time{}}
	done1 := l.begin("m")
	done2 := l.begin("m")
	rts := func() []airuntime.Runtime {
		return []airuntime.Runtime{{Models: []airuntime.Model{{Name: "m"}, {Name: "other"}, {Name: "m2", Loaded: true}}}}
	}
	r := rts()
	l.mark(r)
	if r[0].Models[0].LoadingSince == nil || r[0].Models[1].LoadingSince != nil {
		t.Fatalf("after begin: %+v", r[0].Models)
	}
	done1()
	done1() // twice is once
	r = rts()
	l.mark(r)
	if r[0].Models[0].LoadingSince == nil {
		t.Fatal("still loading for the second request")
	}
	done2()
	r = rts()
	l.mark(r)
	if r[0].Models[0].LoadingSince != nil {
		t.Fatal("loading after both requests finished")
	}
}

func TestLoadState(t *testing.T) {
	now := time.Now()
	since := now.Add(-80 * time.Second)
	if got := loadState(airuntime.Model{LoadingSince: &since}, now); got != " · loading for 1m20s" {
		t.Errorf("got %q", got)
	}
	if got := loadState(airuntime.Model{Loaded: true}, now); got != " · loaded" {
		t.Errorf("got %q", got)
	}
}
