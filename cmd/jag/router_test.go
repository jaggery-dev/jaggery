package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWriteAPIErrorIsValidJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeAPIError(rec, http.StatusNotFound, `no machine in the house has model "gpt-9"`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v: %s", err, rec.Body)
	}
	if body.Error.Message != `no machine in the house has model "gpt-9"` {
		t.Fatalf("message = %q", body.Error.Message)
	}
}

func TestProxyStreamPassesThroughStatusAndBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("data: hi\n\n"))
	}))
	defer upstream.Close()

	rec := httptest.NewRecorder()
	if err := proxyStream(t.Context(), rec, upstream.URL, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusTeapot || rec.Body.String() != "data: hi\n\n" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
}

func TestSpeedMeterTimesAStream(t *testing.T) {
	m := newSpeedMeter(httptest.NewRecorder())
	m.WriteHeader(http.StatusOK)
	for i := 0; i < 11; i++ {
		_, _ = m.Write([]byte(`data: {"choices":[{"delta":{"content":"x"}}]}` + "\n\n"))
		time.Sleep(10 * time.Millisecond)
	}
	_, _ = m.Write([]byte("data: [DONE]\n\n"))

	tps, ok := m.tokensPerSec(false)
	if !ok {
		t.Fatal("a stream should be measured even if the model was not loaded")
	}
	// 10 gaps of ~10ms: about 100 tok/s; allow for a slow test machine.
	if tps < 40 || tps > 110 {
		t.Fatalf("tps = %.0f, want about 100", tps)
	}
}

func TestSpeedMeterUsesUsageOnlyWhenLoaded(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"Mars"}}],"usage":{"completion_tokens":20}}`)

	m := newSpeedMeter(httptest.NewRecorder())
	m.start = time.Now().Add(-time.Second)
	_, _ = m.Write(body)
	if tps, ok := m.tokensPerSec(true); !ok || tps < 15 || tps > 21 {
		t.Fatalf("loaded: got %.1f %v, want about 20 tok/s", tps, ok)
	}
	if _, ok := m.tokensPerSec(false); ok {
		t.Fatal("not loaded: the time includes loading the model, so it must not count")
	}
}

func TestSpeedMeterIgnoresErrors(t *testing.T) {
	m := newSpeedMeter(httptest.NewRecorder())
	m.WriteHeader(http.StatusInternalServerError)
	_, _ = m.Write([]byte(`{"usage":{"completion_tokens":20}}`))
	if _, ok := m.tokensPerSec(true); ok {
		t.Fatal("an error reply is not a speed sample")
	}
}
