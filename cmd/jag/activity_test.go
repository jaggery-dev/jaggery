package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/activity"
	"github.com/jaggery-dev/jaggery/internal/home"
)

func meterWith(chunks ...string) *speedMeter {
	m := newSpeedMeter(httptest.NewRecorder())
	for _, c := range chunks {
		_, _ = m.Write([]byte(c))
	}
	return m
}

func TestUsageOfEachReplyShape(t *testing.T) {
	plain := meterWith(`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":34}}`)
	if p, c, est := usageOf(plain); p != 12 || c != 34 || est {
		t.Errorf("plain reply: %d, %d, estimated %v", p, c, est)
	}

	withUsage := meterWith("data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n",
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":7}}\n\n", "data: [DONE]\n\n")
	if p, c, est := usageOf(withUsage); p != 5 || c != 7 || est {
		t.Errorf("stream with usage: %d, %d, estimated %v", p, c, est)
	}

	bare := meterWith("data: {\"a\":1}\n\n", "data: {\"a\":2}\n\n", "data: {\"a\":3}\n\n", "data: [DONE]\n\n")
	if _, c, est := usageOf(bare); c != 3 || !est {
		t.Errorf("stream without usage: %d, estimated %v; want 3 chunks, estimated", c, est)
	}
}

func TestDeviceNames(t *testing.T) {
	store, err := home.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pi := netip.MustParseAddr("192.0.2.63")
	if err := store.Observe(home.Observation{MachineID: "pi1", Name: "raspberrypi", Addrs: []netip.Addr{pi}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	r := newRecorder(nil, "desktop", store)
	asked := 0
	r.setLookup(func(ctx context.Context, addr netip.Addr, timeout time.Duration) (string, error) {
		asked++
		if addr.String() == "192.0.2.72" {
			return "Alex-iPhone.local", nil
		}
		return "", nil
	})

	cases := map[string]string{
		"127.0.0.1":  "desktop",
		"192.0.2.63": "raspberrypi",
		"192.0.2.72": "Alex-iPhone.local",
		"192.0.2.99": "192.0.2.99", // nothing answered
	}
	for addr, want := range cases {
		if got := r.deviceName(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s: got %q, want %q", addr, got, want)
		}
	}
	before := asked
	r.deviceName(netip.MustParseAddr("192.0.2.72"))
	if asked != before {
		t.Error("asked the network again within the cache's lifetime")
	}
}

func TestRecordNamesTheAppAndPerson(t *testing.T) {
	dir := t.TempDir()
	log, _ := activity.Open(dir)
	store, _ := home.Open(t.TempDir())
	r := newRecorder(log, "desktop", store)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("User-Agent", "Python/3.11 aiohttp/3.9.5")
	req.Header.Set("X-OpenWebUI-User-Name", "Alex")
	req.Header.Set("X-OpenWebUI-Chat-Id", "c1")
	r.record(req, activity.Entry{At: time.Now(), Model: "llama3.2:3b", ServedBy: "desktop", Status: 200})

	var got []activity.Entry
	for i := 0; i < 100 && len(got) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
		got, _ = log.Day(time.Now())
	}
	if len(got) != 1 {
		t.Fatalf("logged %d entries, want 1", len(got))
	}
	e := got[0]
	if e.App != "Open WebUI" || e.User != "Alex" || e.Device != "desktop" || e.Addr != "127.0.0.1" {
		t.Fatalf("logged %+v", e)
	}
}

// The CLI and tests have no recorder; recording must be a no-op, not a crash.
func TestRecordWithoutARecorder(t *testing.T) {
	var r *recorder
	r.record(httptest.NewRequest(http.MethodPost, "/", nil), activity.Entry{})
}
