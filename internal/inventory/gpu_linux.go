package inventory

import "os"

// platformGPUs finds AMD GPUs through the amdgpu driver. NVIDIA comes from
// nvidia-smi.
func platformGPUs() []GPU {
	return amdGPUs("/sys/class/drm", pciIDsFile())
}

// pciIDsFile is where the distribution keeps pci.ids: hwdata (Arch, Fedora)
// or pciutils (Debian, Raspberry Pi OS).
func pciIDsFile() string {
	for _, p := range []string{"/usr/share/hwdata/pci.ids", "/usr/share/misc/pci.ids", "/usr/share/pci.ids"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
