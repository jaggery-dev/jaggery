package inventory

import (
	"github.com/jaggery-dev/jaggery/internal/airuntime"

	"testing"
)

// Collect must produce a usable report on the machine running the tests
// (Linux or macOS in CI), not zeroes.
func TestCollectOnThisMachine(t *testing.T) {
	r := Collect()

	if r.Hostname == "" || r.Hostname == "unknown" {
		t.Errorf("hostname not collected: %q", r.Hostname)
	}
	if r.CPUCount < 1 {
		t.Errorf("cpu count = %d, want at least 1", r.CPUCount)
	}
	if r.MemoryBytes == 0 {
		t.Error("memory not collected")
	}
	if r.DiskTotalBytes == 0 {
		t.Error("root disk not collected")
	}
	if r.Role == "" {
		t.Error("role not assigned")
	}
}

func TestGuessRole(t *testing.T) {
	tests := []struct {
		name string
		r    Report
		want Role
	}{
		{
			name: "a gpu makes it an AI node regardless of memory",
			r:    Report{GPUs: []GPU{{Vendor: "nvidia"}}, MemoryBytes: 8 << 30},
			want: RoleAI,
		},
		{
			name: "lots of memory alone makes it an AI node",
			r:    Report{MemoryBytes: 64 << 30},
			want: RoleAI,
		},
		{
			name: "a very large disk makes it storage",
			r:    Report{MemoryBytes: 8 << 30, DiskTotalBytes: 8 << 40},
			want: RoleStorage,
		},
		{
			name: "an ordinary machine is a server",
			r:    Report{MemoryBytes: 8 << 30, DiskTotalBytes: 256 << 30},
			want: RoleServer,
		},
		{
			name: "a big disk does not override a GPU",
			r:    Report{GPUs: []GPU{{Vendor: "amd"}}, DiskTotalBytes: 8 << 40},
			want: RoleAI,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := guessRole(tt.r); got != tt.want {
				t.Errorf("guessRole = %q, want %q", got, tt.want)
			}
		})
	}
}

// The nvidia-smi parser ships unverified on hardware, so the format it expects
// is pinned here against captured output.
func TestParseNvidiaSMI(t *testing.T) {
	out := "NVIDIA GeForce RTX 3090, 24576\nNVIDIA GeForce RTX 3060, 12288\n"

	gpus := parseNvidiaSMI(out)
	if len(gpus) != 2 {
		t.Fatalf("got %d GPUs, want 2: %+v", len(gpus), gpus)
	}
	if gpus[0].Model != "NVIDIA GeForce RTX 3090" {
		t.Errorf("model = %q", gpus[0].Model)
	}
	if gpus[0].MemoryBytes != 24576<<20 {
		t.Errorf("memory = %d bytes, want %d", gpus[0].MemoryBytes, 24576<<20)
	}
	if gpus[1].Vendor != "nvidia" {
		t.Errorf("vendor = %q, want nvidia", gpus[1].Vendor)
	}
}

func TestParseNvidiaSMIHandlesJunk(t *testing.T) {
	for _, in := range []string{"", "\n\n", "garbage with no comma"} {
		if gpus := parseNvidiaSMI(in); len(gpus) != 0 {
			t.Errorf("parseNvidiaSMI(%q) = %+v, want none", in, gpus)
		}
	}
}

// detectGPUs is the seam for faking hardware; prove a test can swap it.
func TestDetectGPUsIsSwappable(t *testing.T) {
	original := detectGPUs
	t.Cleanup(func() { detectGPUs = original })

	detectGPUs = func() []GPU {
		return []GPU{{Vendor: "nvidia", Model: "fake", MemoryBytes: 24 << 30}}
	}
	if r := Collect(); r.Role != RoleAI || len(r.GPUs) != 1 {
		t.Errorf("faked GPU not reflected: role=%q gpus=%+v", r.Role, r.GPUs)
	}
}

func TestParseNvidiaSMIReadsFreeMemory(t *testing.T) {
	gpus := parseNvidiaSMI("NVIDIA GeForce RTX 3060, 12288, 11000\n")
	if len(gpus) != 1 || gpus[0].FreeBytes == nil || *gpus[0].FreeBytes != 11000<<20 {
		t.Fatalf("got %+v, want 11000 MiB free", gpus)
	}
	if old := parseNvidiaSMI("NVIDIA GeForce RTX 3060, 12288\n"); old[0].FreeBytes != nil {
		t.Error("without a free column, free memory must stay unknown, not zero")
	}
}

// The 8 GB laptop's budget must match what Ollama measured there (tech-specs/decisions.md, "Routing").
func TestAppleGPUBudgetMatchesMetal(t *testing.T) {
	laptop := appleGPU("Apple M2", 8<<30, 0)
	if laptop.Model != "Apple M2 GPU" || !laptop.Unified || laptop.Vendor != "apple" {
		t.Fatalf("got %+v", laptop)
	}
	if got, want := float64(laptop.MemoryBytes)/(1<<30), 5.33; got < want-0.01 || got > want+0.01 {
		t.Errorf("8 GB budget = %.2f GiB, want %.2f (Ollama's reading on the laptop)", got, want)
	}
	if big := appleGPU("Apple M3 Max", 64<<30, 0); big.MemoryBytes != 48<<30 {
		t.Errorf("64 GB budget = %d GiB, want 48 (three quarters above 36 GB)", big.MemoryBytes>>30)
	}
	if set := appleGPU("Apple M2", 8<<30, 7168); set.MemoryBytes != 7<<30 {
		t.Errorf("with wired_limit_mb=7168 budget = %d MiB, want 7168", set.MemoryBytes>>20)
	}
}

func TestUnifiedFreeIsBudgetMinusLoadedModels(t *testing.T) {
	gpus := []GPU{appleGPU("Apple M2", 8<<30, 0)}
	runtimes := []airuntime.Runtime{{Models: []airuntime.Model{
		{Name: "llama3.2:3b", Loaded: true, VRAMBytes: 2 << 30},
		{Name: "qwen3:8b"}, // on disk, not loaded: holds nothing
	}}}
	fillUnifiedFree(gpus, runtimes)
	if gpus[0].FreeBytes == nil || *gpus[0].FreeBytes != gpus[0].MemoryBytes-2<<30 {
		t.Fatalf("free = %v, want budget minus 2 GiB", gpus[0].FreeBytes)
	}

	overfull := []GPU{appleGPU("Apple M2", 8<<30, 0)}
	fillUnifiedFree(overfull, []airuntime.Runtime{{Models: []airuntime.Model{{VRAMBytes: 9 << 30}}}})
	if *overfull[0].FreeBytes != 0 {
		t.Errorf("free = %d, want 0 rather than wrapping below zero", *overfull[0].FreeBytes)
	}
}
