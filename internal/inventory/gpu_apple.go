//go:build darwin && arm64

package inventory

import "golang.org/x/sys/unix"

func platformGPUs() []GPU {
	chip, err := unix.Sysctl("machdep.cpu.brand_string") // "Apple M2"
	if err != nil || chip == "" {
		return nil
	}
	limit, _ := unix.SysctlUint32("iogpu.wired_limit_mb") // absent before macOS 14
	return []GPU{appleGPU(chip, memoryBytes(), uint64(limit))}
}
