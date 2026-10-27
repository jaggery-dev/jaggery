package inventory

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// amdMinVRAM is the least dedicated memory a Radeon needs to count. An
// integrated AMD GPU reports a small carve-out (often 512 MB) as its VRAM;
// counting it would call every AMD laptop an AI machine and filter models
// out by a figure that doesn't limit them.
const amdMinVRAM = 2 << 30

var drmCard = regexp.MustCompile(`^card[0-9]+$`)

// amdGPUs reads AMD GPUs from the amdgpu driver's files under drmDir
// (/sys/class/drm): no ROCm or vendor tool needed. pciIDs is a pci.ids file
// for the card's name; without one the name is the PCI id.
func amdGPUs(drmDir, pciIDs string) []GPU {
	entries, err := os.ReadDir(drmDir)
	if err != nil {
		return nil
	}
	var gpus []GPU
	for _, e := range entries {
		if !drmCard.MatchString(e.Name()) {
			continue // connectors (card1-DP-1) and render nodes
		}
		dev := filepath.Join(drmDir, e.Name(), "device")
		if readHex(dev, "vendor") != 0x1002 {
			continue
		}
		total, ok := readUint(dev, "mem_info_vram_total")
		if !ok || total < amdMinVRAM {
			continue
		}
		gpu := GPU{Vendor: "amd", Model: amdName(dev, pciIDs), MemoryBytes: total}
		if used, ok := readUint(dev, "mem_info_vram_used"); ok && used <= total {
			free := total - used
			gpu.FreeBytes = &free
		}
		gpus = append(gpus, gpu)
	}
	return gpus
}

// amdName is the card's marketing name: the driver's product_name where it
// has one, otherwise pci.ids (the board maker's name for the card, then the
// chip's), otherwise the PCI id.
func amdName(dev, pciIDs string) string {
	if b, err := os.ReadFile(filepath.Join(dev, "product_name")); err == nil {
		if name := strings.TrimSpace(string(b)); name != "" {
			return name
		}
	}
	device := readHex(dev, "device")
	if name := pciName(pciIDs, 0x1002, device, readHex(dev, "subsystem_vendor"), readHex(dev, "subsystem_device")); name != "" {
		if !strings.HasPrefix(name, "AMD") {
			name = "AMD " + name
		}
		return name
	}
	return fmt.Sprintf("AMD GPU 1002:%04x", device)
}

// pciName looks a device up in a pci.ids file. A board maker's entry
// ("Radeon Pro 570") wins over the chip's ("Ellesmere [Radeon RX 470/480]"),
// whose bracketed part is the name people know.
func pciName(path string, vendor, device, subVendor, subDevice uint64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	vendorLine := fmt.Sprintf("%04x ", vendor)
	deviceLine := fmt.Sprintf("\t%04x ", device)
	subLine := fmt.Sprintf("\t\t%04x %04x ", subVendor, subDevice)
	inVendor, inDevice := false, false
	chip := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		switch {
		case line[0] != '\t':
			if inVendor {
				return chip
			}
			inVendor = strings.HasPrefix(line, vendorLine)
		case !inVendor:
		case strings.HasPrefix(line, "\t\t"):
			if inDevice && strings.HasPrefix(line, subLine) {
				return strings.TrimSpace(line[len(subLine):])
			}
		default:
			if inDevice {
				return chip
			}
			if strings.HasPrefix(line, deviceLine) {
				inDevice = true
				chip = strings.TrimSpace(line[len(deviceLine):])
				if i, j := strings.Index(chip, "["), strings.LastIndex(chip, "]"); i >= 0 && j > i {
					chip = chip[i+1 : j]
				}
			}
		}
	}
	return chip
}

func readUint(dir, name string) (uint64, bool) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	return n, err == nil
}

// readHex reads a sysfs id such as "0x1002"; zero if absent.
func readHex(dir, name string) uint64 {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(string(b)), "0x"), 16, 64)
	return n
}
