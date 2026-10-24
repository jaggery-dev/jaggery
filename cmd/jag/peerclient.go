package main

import (
	"crypto/ed25519"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/secure"
)

// Every call to a peer used to build a new http.Transport, and each one kept
// its connection open after the request: the agent leaked about 2,300
// connections an hour, and the laptop's hit macOS's 10,240 limit overnight and
// stopped answering anything ("wedged"). Now there is one transport per peer
// key, reused by every call, and idle connections close after a while.

var peerTransports sync.Map // string(peer key) -> *http.Transport

const peerIdle = 90 * time.Second

// peerHTTPClient is a client for mutual TLS to the peer holding key. timeout
// 0 means none (a streamed reply can run long).
func peerHTTPClient(id *identity.Identity, key ed25519.PublicKey, timeout time.Duration) (*http.Client, error) {
	if t, ok := peerTransports.Load(string(key)); ok {
		return &http.Client{Transport: t.(*http.Transport), Timeout: timeout}, nil
	}
	tlsCfg, err := secure.ClientConfig(id, key)
	if err != nil {
		return nil, err
	}
	t := &http.Transport{
		TLSClientConfig:     tlsCfg,
		IdleConnTimeout:     peerIdle,
		MaxIdleConnsPerHost: 4,
	}
	actual, loaded := peerTransports.LoadOrStore(string(key), t)
	if loaded {
		t.CloseIdleConnections()
	}
	return &http.Client{Transport: actual.(*http.Transport), Timeout: timeout}, nil
}

// drainClose finishes reading a reply before closing it: a reply closed
// before its end can't go back to the pool, so its connection is thrown
// away. JSON decoders stop before the trailing newline, so this matters.
func drainClose(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
}
