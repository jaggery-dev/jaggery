package inventory

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// probeNvidiaSMI asks nvidia-smi for each GPU's name and memory. It is written
// without NVIDIA hardware to test against, so it is intentionally forgiving:
// if the tool is missing or its output is unexpected, it returns nothing
// rather than guessing. This is the one piece of the project that ships
// unverified on real hardware; the parser is covered by a unit test against
// captured output instead.
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
		if mib, err := strconv.ParseUint(strings.TrimSpace(cols[1]), 10, 64); err == nil {
			gpu.MemoryBytes = mib << 20
		}
		if len(cols) > 2 {
			if mib, err := strconv.ParseUint(strings.TrimSpace(cols[2]), 10, 64); err == nil {
				free := mib << 20
				gpu.FreeBytes = &free
			}
		}
		gpus = append(gpus, gpu)
	}
	return gpus
}
