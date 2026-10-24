//go:build windows

package main

import "errors"

// reexec has no Windows equivalent; the Windows service (A5) will restart the
// agent instead.
func reexec() error {
	return errors.New("restart jag to run the new version")
}
