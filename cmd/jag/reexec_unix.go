//go:build !windows

package main

import (
	"os"
	"syscall"
)

// reexec replaces this process with the binary now at its path, keeping the
// pid, so a service manager sees no restart and a terminal keeps the agent.
func reexec() error {
	exe, err := executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}
