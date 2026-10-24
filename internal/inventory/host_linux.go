//go:build linux

package inventory

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

func memoryBytes() uint64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line) // "MemTotal: 24567890 kB"
		if len(fields) >= 2 {
			if kb, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				return kb * 1024
			}
		}
	}
	return 0
}

func cpuModel() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		// "model name" on x86, "Model" on some ARM; take whichever appears.
		if key, value, ok := strings.Cut(line, ":"); ok {
			switch strings.TrimSpace(key) {
			case "model name", "Model":
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func bootTime() time.Time {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return time.Time{}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == "btime" {
			if secs, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
				return time.Unix(secs, 0).UTC()
			}
		}
	}
	return time.Time{}
}
