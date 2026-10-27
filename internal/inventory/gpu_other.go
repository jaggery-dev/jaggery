//go:build !(darwin && arm64) && !linux

package inventory

// platformGPUs finds GPUs the platform reports without a vendor tool: Apple
// Silicon (gpu_apple.go) and AMD on Linux (gpu_linux.go) so far. Elsewhere
// NVIDIA comes from nvidia-smi.
func platformGPUs() []GPU { return nil }
