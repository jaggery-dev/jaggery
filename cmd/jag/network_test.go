package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/netif"
)

// A service that starts before Wi-Fi joins waits for it, instead of exiting
// (launchd's first runs at boot died with "no usable network interface").
func TestWaitForNetwork(t *testing.T) {
	calls := 0
	usable := func() ([]netif.Interface, map[string]string, error) {
		calls++
		if calls < 3 {
			return nil, map[string]string{"en0": "no routable address"}, nil
		}
		return []netif.Interface{{Name: "en0"}}, nil, nil
	}
	var out bytes.Buffer
	ifaces, _, err := waitForNetwork(usable, time.Millisecond, &out)
	if err != nil || len(ifaces) != 1 || calls != 3 {
		t.Fatalf("got %v, %v after %d checks", ifaces, err, calls)
	}
	if got := out.String(); strings.Count(got, "waiting") != 1 || !strings.Contains(got, "network found") {
		t.Fatalf("said %q", got)
	}

	// With a network from the start, it says nothing.
	out.Reset()
	if _, _, err := waitForNetwork(usable, time.Millisecond, &out); err != nil || out.Len() != 0 {
		t.Fatalf("%v, said %q", err, out.String())
	}
}

func TestScanReportIgnoresOneFailure(t *testing.T) {
	var r scanReport
	boom := errors.New("sendmsg: no route to host")
	if msg := r.failed(boom); msg != "" {
		t.Fatalf("the first failure was reported: %q", msg)
	}
	if msg := r.worked(); msg != "" {
		t.Fatalf("recovery from an unreported failure was reported: %q", msg)
	}
	r.failed(boom)
	if msg := r.failed(boom); !strings.Contains(msg, "2 in a row") {
		t.Fatalf("two failures in a row: %q", msg)
	}
	if msg := r.failed(boom); msg != "" {
		t.Fatalf("a third failure was reported again: %q", msg)
	}
	if msg := r.worked(); msg != "peer scans work again" {
		t.Fatalf("recovery: %q", msg)
	}
}
