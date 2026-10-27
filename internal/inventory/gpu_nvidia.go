package inventory

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// probeNvidiaSMI asks nvidia-smi for each GPU's name and memory. It is
// forgiving: if the tool is missing or its output is unexpected, it returns
// nothing rather than guessing. Only a cloud L4 has been tried; the parser is
// covered by unit tests against captured and likely output.
func probeNvidiaSMI() []GPU {
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, path,
		"--query-gpu=name,memory.total,memory.free", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil
	}
	return parseNvidiaSMI(string(out))
}

// parseNvidiaSMI reads the CSV nvidia-smi emits: one line per GPU, with the
// name, total and free memory in MiB, e.g. "NVIDIA GeForce RTX 3090, 24576,
// 23040". A missing free column leaves FreeBytes unknown.
func parseNvidiaSMI(output string) []GPU {
	var gpus []GPU
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		cols := strings.Split(line, ",")
		if len(cols) < 2 {
			continue
		}
		gpu := GPU{Vendor: "nvidia", Model: strings.TrimSpace(cols[0])}
		if gpu.Model == "" {
			continue
		}
		if mib, ok := parseMiB(cols[1]); ok {
			gpu.MemoryBytes = mib << 20
		}
		if len(cols) > 2 {
			if mib, ok := parseMiB(cols[2]); ok && mib<<20 <= gpu.MemoryBytes {
				free := mib << 20
				gpu.FreeBytes = &free
			}
		}
		gpus = append(gpus, gpu)
	}
	return gpus
}

// parseMiB reads one memory column: "24576", or "24576 MiB" from a driver
// that ignores nounits. "[N/A]" and "[Not Supported]" are unknown.
func parseMiB(col string) (uint64, bool) {
	col = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(col), "MiB"))
	n, err := strconv.ParseUint(col, 10, 64)
	return n, err == nil
}
