package inventory

import (
	"os"
	"path/filepath"
	"testing"
)

const testPCIIDs = `# pci.ids sample
1002  Advanced Micro Devices, Inc. [AMD/ATI]
	67df  Ellesmere [Radeon RX 470/480/570/570X/580/580X/590]
		106b 0163  Radeon Pro 570
		1da2 e353  Radeon RX 570 Pulse 4GB
	73bf  Navi 21 [Radeon RX 6800/6800 XT / 6900 XT]
10de  NVIDIA Corporation
	2684  AD102 [GeForce RTX 4090]
`

// card writes a fake /sys/class/drm/<name>/device with the given files.
func card(t *testing.T, drm, name string, files map[string]string) {
	t.Helper()
	dev := filepath.Join(drm, name, "device")
	if err := os.MkdirAll(dev, 0o755); err != nil {
		t.Fatal(err)
	}
	for f, v := range files {
		if err := os.WriteFile(filepath.Join(dev, f), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAMDGPUs(t *testing.T) {
	root := t.TempDir()
	ids := filepath.Join(root, "pci.ids")
	os.WriteFile(ids, []byte(testPCIIDs), 0o644)
	drm := filepath.Join(root, "drm")

	// An iMac's Radeon Pro 570: named by the board maker's entry.
	card(t, drm, "card1", map[string]string{"vendor": "0x1002", "device": "0x67df",
		"subsystem_vendor": "0x106b", "subsystem_device": "0x0163",
		"mem_info_vram_total": "4294967296", "mem_info_vram_used": "1226940416"})
	// A connector and a render node are not cards.
	card(t, drm, "card1-DP-1", map[string]string{"vendor": "0x1002", "mem_info_vram_total": "4294967296"})
	// An RX 6800 from a board maker pci.ids doesn't list: the chip's name.
	card(t, drm, "card2", map[string]string{"vendor": "0x1002", "device": "0x73bf",
		"subsystem_vendor": "0x1458", "subsystem_device": "0x2322",
		"mem_info_vram_total": "17163091968", "mem_info_vram_used": "26214400"})
	// A driver that names the card itself.
	card(t, drm, "card3", map[string]string{"vendor": "0x1002", "device": "0x74a1",
		"product_name": "AMD Instinct MI300X", "mem_info_vram_total": "206141652992"})
	// An integrated GPU's 512 MB carve-out doesn't count.
	card(t, drm, "card4", map[string]string{"vendor": "0x1002", "device": "0x1681", "mem_info_vram_total": "536870912"})
	// NVIDIA and Intel come from elsewhere.
	card(t, drm, "card5", map[string]string{"vendor": "0x10de", "device": "0x2684", "mem_info_vram_total": "25769803776"})
	card(t, drm, "card6", map[string]string{"vendor": "0x8086", "device": "0x9bc5"})

	gpus := amdGPUs(drm, ids)
	if len(gpus) != 3 {
		t.Fatalf("got %d GPUs, want 3: %+v", len(gpus), gpus)
	}
	want := []struct {
		model       string
		total, free uint64
		freeKnown   bool
	}{
		{"AMD Radeon Pro 570", 4294967296, 4294967296 - 1226940416, true},
		{"AMD Radeon RX 6800/6800 XT / 6900 XT", 17163091968, 17163091968 - 26214400, true},
		{"AMD Instinct MI300X", 206141652992, 0, false},
	}
	for i, w := range want {
		g := gpus[i]
		if g.Vendor != "amd" || g.Model != w.model || g.MemoryBytes != w.total {
			t.Errorf("gpu %d = %+v, want %s with %d bytes", i, g, w.model, w.total)
		}
		if (g.FreeBytes != nil) != w.freeKnown || (g.FreeBytes != nil && *g.FreeBytes != w.free) {
			t.Errorf("gpu %d free = %v, want %d (known %v)", i, g.FreeBytes, w.free, w.freeKnown)
		}
	}
}

// Without pci.ids the card still shows, by its PCI id.
func TestAMDGPUsWithoutPCIIDs(t *testing.T) {
	drm := t.TempDir()
	card(t, drm, "card0", map[string]string{"vendor": "0x1002", "device": "0x67df", "mem_info_vram_total": "8589934592"})
	gpus := amdGPUs(drm, "")
	if len(gpus) != 1 || gpus[0].Model != "AMD GPU 1002:67df" || gpus[0].FreeBytes != nil {
		t.Fatalf("got %+v", gpus)
	}
	if gpus := amdGPUs(filepath.Join(drm, "missing"), ""); gpus != nil {
		t.Fatalf("no drm directory: got %+v", gpus)
	}
}
