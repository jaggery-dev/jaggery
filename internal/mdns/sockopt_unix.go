//go:build unix

package mdns

import (
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

// control shares port 5353 with whatever responder the OS already runs.
//
// The two platforms need different options and getting it wrong is silent:
// on Linux, SO_REUSEADDR gives every socket joined to the group its own copy,
// while adding SO_REUSEPORT would make the kernel load-balance datagrams and
// steal them from avahi. On darwin, SO_REUSEPORT is mandatory or the bind
// fails outright because mDNSResponder holds the port.
func control(_, _ string, c syscall.RawConn) error {
	opts := []int{unix.SO_REUSEADDR}
	if runtime.GOOS == "darwin" {
		opts = append(opts, unix.SO_REUSEPORT)
	}

	var setErr error
	err := c.Control(func(fd uintptr) {
		for _, opt := range opts {
			if err := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, opt, 1); err != nil {
				setErr = err
				return
			}
		}
	})
	if err != nil {
		return err
	}
	return setErr
}
