package main

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jaggery-dev/jaggery/internal/appnames"
	"github.com/jaggery-dev/jaggery/internal/discovery"
	"github.com/jaggery-dev/jaggery/internal/netif"
)

// App names (track W2): the name holder answers for every app name in the
// house over mDNS, and any agent forwards a request whose Host is an app name
// to the app. The table is rebuilt from the house every few seconds, not per
// request.

const appNamesCheck = 5 * time.Second

var appNameTable atomic.Pointer[appnames.Table]

var appForwarder = appnames.NewForwarder(func() appnames.Table {
	if t := appNameTable.Load(); t != nil {
		return *t
	}
	return nil
})

// forwardAppName is languard's Forward hook.
func forwardAppName(r *http.Request) http.Handler { return appForwarder.Handler(r) }

// refreshAppNames rebuilds the table from the saved house.
func refreshAppNames() appnames.Table {
	self, _ := lanAddr()
	t := appnames.FromHouse(loadHouse(), self, discovery.AgentPort, discovery.SecurePort)
	appNameTable.Store(&t)
	return t
}

// serveAppNames keeps the table fresh and answers for each name over mDNS
// while this machine holds the house's names, until ctx ends.
func serveAppNames(ctx context.Context, session *discovery.Session, usable []netif.Interface, holds func(bool) bool) {
	running := map[string]context.CancelFunc{}
	shown := map[string]appnames.Target{}
	ticker := time.NewTicker(appNamesCheck)
	defer ticker.Stop()
	for {
		t := refreshAppNames()
		for name, tg := range t {
			if old, ok := shown[name]; !ok || old.Addr != tg.Addr || old.App != tg.App {
				fmt.Printf("app names: %s.local goes to %s on %s (%s)\n", name, tg.App, tg.Machine, tg.Addr)
				shown[name] = tg
			}
			if _, ok := running[name]; !ok {
				c, cancel := context.WithCancel(ctx)
				running[name] = cancel
				session.AnnounceAliasUntil(c, name+".local.", usable, ipv6ForNames, holds)
			}
		}
		for name, cancel := range running {
			if _, ok := t[name]; !ok {
				cancel()
				delete(running, name)
				delete(shown, name)
				fmt.Printf("app names: %s.local is gone\n", name)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
