//go:build windows

package inventory

import "time"

// Windows collection is not implemented yet: there is no Windows machine to
// test it on, and shipping syscalls that can't be run here would be guesswork. These return
// zero so the binary builds and runs on Windows with a partial report rather
// than failing. Fill in with GlobalMemoryStatusEx / registry / GetTickCount64
// when there is a Windows machine to verify against.

func memoryBytes() uint64 { return 0 }

func cpuModel() string { return "" }

func bootTime() time.Time { return time.Time{} }

func rootDisk() (total, free uint64) { return 0, 0 }
