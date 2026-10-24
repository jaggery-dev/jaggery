package router

import (
	"testing"

	"github.com/jaggery-dev/jaggery/internal/airuntime"
	"github.com/jaggery-dev/jaggery/internal/house"
	"github.com/jaggery-dev/jaggery/internal/inventory"
)

func machine(name string, self, reachable bool, rt ...airuntime.Runtime) house.Machine {
	return house.Machine{
		MachineID: name + "-id",
		Name:      name,
		Self:      self,
		Reachable: reachable,
		Inventory: inventory.Report{Runtimes: rt},
	}
}

func ollama(models ...airuntime.Model) airuntime.Runtime {
	return airuntime.Runtime{Kind: airuntime.Ollama, Endpoint: "http://127.0.0.1:11434", Models: models}
}

func TestPickPrefersLoaded(t *testing.T) {
	machines := []house.Machine{
		machine("desktop", true, true, ollama(airuntime.Model{Name: "llama3.2:3b"})),               // has it, not loaded
		machine("laptop", false, true, ollama(airuntime.Model{Name: "llama3.2:3b", Loaded: true})), // loaded
	}
	target, ok := Pick(machines, "llama3.2:3b", nil)
	if !ok {
		t.Fatal("a machine has the model")
	}
	if target.Name != "laptop" {
		t.Errorf("picked %q, want the machine with it loaded", target.Name)
	}
	if !target.Loaded {
		t.Error("target should be marked loaded")
	}
}

func TestPickPrefersSelfOnATie(t *testing.T) {
	machines := []house.Machine{
		machine("laptop", false, true, ollama(airuntime.Model{Name: "m"})),
		machine("desktop", true, true, ollama(airuntime.Model{Name: "m"})),
	}
	target, _ := Pick(machines, "m", nil)
	if !target.Self {
		t.Errorf("picked %q, want self to avoid a network hop when otherwise tied", target.Name)
	}
}

func TestPickSkipsUnreachablePeers(t *testing.T) {
	machines := []house.Machine{
		machine("laptop", false, false, ollama(airuntime.Model{Name: "m", Loaded: true})), // offline
		machine("desktop", true, true, ollama(airuntime.Model{Name: "m"})),
	}
	target, ok := Pick(machines, "m", nil)
	if !ok || target.Name != "desktop" {
		t.Errorf("an offline peer must not be chosen even if the model is loaded there; got %+v", target)
	}
}

func TestPickReturnsFalseForUnknownModel(t *testing.T) {
	machines := []house.Machine{machine("desktop", true, true, ollama(airuntime.Model{Name: "m"}))}
	if _, ok := Pick(machines, "nonexistent", nil); ok {
		t.Error("an unknown model must not resolve to any machine")
	}
}

func TestPickMatchesLatestTag(t *testing.T) {
	machines := []house.Machine{machine("desktop", true, true, ollama(airuntime.Model{Name: "qwen3:latest"}))}
	if _, ok := Pick(machines, "qwen3", nil); !ok {
		t.Error("a bare name should match the :latest tag")
	}
}

func TestMergedModelsDedupesAndSorts(t *testing.T) {
	machines := []house.Machine{
		machine("desktop", true, true, ollama(airuntime.Model{Name: "qwen3:14b"}, airuntime.Model{Name: "llama3.2:3b"})),
		machine("laptop", false, true, ollama(airuntime.Model{Name: "llama3.2:3b"})),
	}
	merged := MergedModels(machines)
	if len(merged) != 2 {
		t.Fatalf("got %d models, want 2 distinct: %+v", len(merged), merged)
	}
	if merged[0].ID != "llama3.2:3b" || merged[1].ID != "qwen3:14b" {
		t.Errorf("not sorted by id: %+v", merged)
	}
	// llama3.2 is on both machines → owned by jaggery, not one machine.
	if merged[0].OwnedBy != "jaggery" {
		t.Errorf("shared model owned_by = %q, want jaggery", merged[0].OwnedBy)
	}
	if merged[1].OwnedBy != "desktop" {
		t.Errorf("single-machine model owned_by = %q, want desktop", merged[1].OwnedBy)
	}
}

func TestLocalRuntimeFor(t *testing.T) {
	rts := []airuntime.Runtime{ollama(airuntime.Model{Name: "llama3.2:3b"})}
	if ep := LocalRuntimeFor(rts, "llama3.2:3b"); ep != "http://127.0.0.1:11434" {
		t.Errorf("endpoint = %q", ep)
	}
	if ep := LocalRuntimeFor(rts, "missing"); ep != "" {
		t.Errorf("want empty for a missing model, got %q", ep)
	}
}

type fixedSpeeds map[string]float64

func (f fixedSpeeds) TokensPerSec(machineID, model string) (float64, bool) {
	tps, ok := f[machineID]
	return tps, ok
}

func appleGPU(free uint64) []inventory.GPU {
	return []inventory.GPU{{Vendor: "apple", Model: "Apple M2 GPU", MemoryBytes: 5 << 30, FreeBytes: &free, Unified: true}}
}

// From tech-specs/decisions.md, "Routing": with the model on both and loaded on neither, the desktop (most
// memory, 7 tok/s) must lose to the laptop (least memory, 41 tok/s) once both
// are measured. Ranking by free memory picked the desktop and was 4.9x slower.
func TestPickRanksByMeasuredSpeedNotMemory(t *testing.T) {
	model := airuntime.Model{Name: "llama3.2:3b", SizeBytes: 2 << 30}
	desktop := machine("desktop", true, true, ollama(model))
	desktop.Inventory.MemoryBytes = 23 << 30
	laptop := machine("laptop", false, true, ollama(model))
	laptop.Inventory.GPUs = appleGPU(5 << 30)

	target, _ := Pick([]house.Machine{desktop, laptop}, "llama3.2:3b", fixedSpeeds{"desktop-id": 7.3, "laptop-id": 41.5})
	if target.Name != "laptop" {
		t.Fatalf("picked %q, want the faster laptop", target.Name)
	}
}

func TestPickTriesAnUnmeasuredMachineOnce(t *testing.T) {
	model := airuntime.Model{Name: "m"}
	machines := []house.Machine{
		machine("desktop", true, true, ollama(model)),
		machine("laptop", false, true, ollama(model)),
	}
	target, _ := Pick(machines, "m", fixedSpeeds{"desktop-id": 7.3})
	if target.Name != "laptop" {
		t.Fatalf("picked %q, want the unmeasured laptop so it gets a number", target.Name)
	}
}

func TestPickSkipsMachinesTheModelDoesNotFit(t *testing.T) {
	big := airuntime.Model{Name: "qwen3:14b", SizeBytes: 9 << 30}
	laptop := machine("laptop", true, true, ollama(big))
	laptop.Inventory.GPUs = appleGPU(5 << 30) // 9 GB won't fit in 5 GB free
	desktop := machine("desktop", false, true, ollama(big))
	desktop.Inventory.MemoryBytes = 23 << 30 // no GPU figure: fits in RAM

	target, _ := Pick([]house.Machine{laptop, desktop}, "qwen3:14b", fixedSpeeds{"laptop-id": 41.5, "desktop-id": 7.3})
	if target.Name != "desktop" {
		t.Fatalf("picked %q, want desktop: the model does not fit the laptop's GPU", target.Name)
	}
}

func TestPickFallsBackWhenTheModelFitsNowhere(t *testing.T) {
	huge := airuntime.Model{Name: "llama3.1:70b", SizeBytes: 40 << 30}
	laptop := machine("laptop", true, true, ollama(huge))
	laptop.Inventory.GPUs = appleGPU(5 << 30)
	if _, ok := Pick([]house.Machine{laptop}, "llama3.1:70b", nil); !ok {
		t.Fatal("fits nowhere: still serve it somewhere rather than 404")
	}
}

func TestPickAmongLoadedPrefersFaster(t *testing.T) {
	loaded := airuntime.Model{Name: "m", Loaded: true}
	machines := []house.Machine{
		machine("desktop", true, true, ollama(loaded)),
		machine("laptop", false, true, ollama(loaded)),
	}
	target, _ := Pick(machines, "m", fixedSpeeds{"desktop-id": 7.3, "laptop-id": 41.5})
	if target.Name != "laptop" {
		t.Fatalf("picked %q, want the faster of two loaded machines", target.Name)
	}
}

func TestSpeedBookSmoothsSamples(t *testing.T) {
	b := NewSpeedBook()
	if _, ok := b.TokensPerSec("laptop-id", "m"); ok {
		t.Fatal("empty book has no speed")
	}
	b.Record("laptop-id", "m", 40)
	b.Record("laptop-id", "m", 10)
	got, _ := b.TokensPerSec("laptop-id", "m")
	if got != 31 {
		t.Fatalf("got %.1f, want 31 (40 moved 30%% towards 10)", got)
	}
	b.Record("laptop-id", "m", 0) // no tokens is no observation
	if again, _ := b.TokensPerSec("laptop-id", "m"); again != 31 {
		t.Fatalf("a zero sample changed the speed to %.1f", again)
	}
}

// P0d: a machine whose house protocol doesn't match gets no work.
func TestPickSkipsIncompatibleMachines(t *testing.T) {
	m := house.Machine{MachineID: "old", Reachable: true, Incompatible: true,
		Inventory: inventory.Report{Runtimes: []airuntime.Runtime{{Kind: airuntime.Ollama, Models: []airuntime.Model{{Name: "llama3.2:3b"}}}}}}
	if _, ok := Pick([]house.Machine{m}, "llama3.2:3b", NewSpeedBook()); ok {
		t.Fatal("routed to an incompatible machine")
	}
}
