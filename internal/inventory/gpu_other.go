//go:build !(darwin && arm64)

package inventory

// platformGPUs finds GPUs the platform reports without a vendor tool. Only
// Apple Silicon does so far; elsewhere NVIDIA comes from nvidia-smi and AMD
// is out of MVP scope.
func platformGPUs() []GPU { return nil }
