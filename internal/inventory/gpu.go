package inventory

// GPU detection is deliberately isolated behind this one function. The MVP's
// primary GPU target is NVIDIA via NVML, which there is no hardware to test
// against yet (see docs/known-issues.md). Keeping detection
// here means everything downstream — the role guess, the map, routing — is
// testable with GPUs faked, and a real detector drops in without touching
// callers.
//
// detectGPUs returns an empty slice when it finds nothing, which is the honest
// answer where no supported GPU is found.
var detectGPUs = defaultDetectGPUs

func defaultDetectGPUs() []GPU {
	return append(probeNvidiaSMI(), platformGPUs()...)
}

// appleGPU describes an Apple Silicon GPU. Its memory is the share of unified
// memory Metal lets the GPU wire: the iogpu.wired_limit_mb override when set,
// otherwise macOS's default of two thirds of RAM, or three quarters above
// 36 GB. On the 8 GB laptop that is 5.33 GiB, exactly what Ollama reports as the
// GPU's budget (tech-specs/decisions.md, "Routing"), not the 8 GB on the box.
func appleGPU(chip string, ramBytes, wiredLimitMB uint64) GPU {
	budget := ramBytes * 2 / 3
	if ramBytes > 36<<30 {
		budget = ramBytes * 3 / 4
	}
	if wiredLimitMB > 0 {
		budget = wiredLimitMB << 20
	}
	return GPU{Vendor: "apple", Model: chip + " GPU", MemoryBytes: budget, Unified: true}
}
