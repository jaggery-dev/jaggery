//go:build windows

package mdns

import (
	"syscall"

	"golang.org/x/sys/windows"
)

func control(_, _ string, c syscall.RawConn) error {
	var setErr error
	err := c.Control(func(fd uintptr) {
		setErr = windows.SetsockoptInt(
			windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1,
		)
	})
	if err != nil {
		return err
	}
	return setErr
}
