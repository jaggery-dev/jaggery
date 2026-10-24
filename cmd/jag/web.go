package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"syscall"
)

// serveWeb adds a listener on port to the agent's LAN server, so the shared
// names open without a port. Failing is not fatal: everything stays reachable
// on the agent port, and the caller tells the person how to fix it.
func serveWeb(server *http.Server, port int) error {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "warning: web port stopped:", err)
		}
	}()
	return nil
}

// webPortHint turns a failure to bind port 80 into the one thing to do about
// it. The names still work with :7373 meanwhile.
func webPortHint(err error) string {
	switch {
	case errors.Is(err, syscall.EACCES) && runtime.GOOS == "linux":
		exe, exeErr := os.Executable()
		if exeErr != nil {
			exe = "jag"
		}
		return fmt.Sprintf("Linux reserves ports below 1024 for root; let jag bind it with\n"+
			"             sudo setcap cap_net_bind_service=+ep %s\n"+
			"             until then, use http://jaggery.local:7373", exe)
	case errors.Is(err, syscall.EACCES):
		return "not permitted on this system; use http://jaggery.local:7373"
	case errors.Is(err, syscall.EADDRINUSE):
		return "another program already uses it; use http://jaggery.local:7373"
	default:
		return fmt.Sprintf("%v; use http://jaggery.local:7373", err)
	}
}
