//go:build darwin

package inventory

import (
	"time"

	"golang.org/x/sys/unix"
)

func memoryBytes() uint64 {
	v, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return v
}

func cpuModel() string {
	s, err := unix.Sysctl("machdep.cpu.brand_string")
	if err != nil {
		return ""
	}
	return s
}

func bootTime() time.Time {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return time.Time{}
	}
	return time.Unix(tv.Unix()).UTC()
}
