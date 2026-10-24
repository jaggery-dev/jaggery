package modelwatch

import (
	"testing"
	"time"

	"github.com/jaggery-dev/jaggery/internal/airuntime"
)

func ollama(models ...airuntime.Model) []airuntime.Runtime {
	return []airuntime.Runtime{{Kind: airuntime.Ollama, Models: models}}
}

func model(name, digest string) airuntime.Model {
	return airuntime.Model{Name: name, Digest: digest, Format: "gguf"}
}

// The models already there when Jaggery first runs are the baseline, not a
// flood of alerts.
func TestFirstLookIsTheBaseline(t *testing.T) {
	lg := Open(t.TempDir())
	events, err := lg.Observe(ollama(model("llama3.2:3b", "a80c")), time.Now())
	if err != nil || len(events) != 0 {
		t.Fatalf("first look raised %+v, %v", events, err)
	}
	if _, ok := lg.FirstSeen(airuntime.Ollama, "llama3.2:3b"); !ok {
		t.Fatal("the baseline model wasn't recorded")
	}
}

func TestNewChangedAndRemoved(t *testing.T) {
	lg := Open(t.TempDir())
	t0 := time.Now()
	_, _ = lg.Observe(ollama(model("llama3.2:3b", "a80c")), t0)

	// Pulling a new model on the laptop: the G3 done-when.
	events, _ := lg.Observe(ollama(model("llama3.2:3b", "a80c"), model("qwen3:8b", "500a")), t0.Add(time.Minute))
	if len(events) != 1 || events[0].Kind != New || events[0].Name != "qwen3:8b" {
		t.Fatalf("pull: got %+v", events)
	}

	// Same name, new files.
	events, _ = lg.Observe(ollama(model("llama3.2:3b", "ffff"), model("qwen3:8b", "500a")), t0.Add(2*time.Minute))
	if len(events) != 1 || events[0].Kind != Changed || events[0].PrevDigest != "a80c" || events[0].Digest != "ffff" {
		t.Fatalf("swap: got %+v", events)
	}
	if first, _ := lg.FirstSeen(airuntime.Ollama, "llama3.2:3b"); !first.Equal(t0.Add(2 * time.Minute)) {
		t.Errorf("a changed model keeps the old first-seen time: %v", first)
	}

	events, _ = lg.Observe(ollama(model("qwen3:8b", "500a")), t0.Add(3*time.Minute))
	if len(events) != 1 || events[0].Kind != Removed || events[0].Name != "llama3.2:3b" {
		t.Fatalf("rm: got %+v", events)
	}

	if got := lg.Recent(t0); len(got) != 3 || got[0].Kind != Removed {
		t.Fatalf("recent, newest first: %+v", got)
	}
}

// Ollama being stopped says nothing about its models.
func TestAStoppedRuntimeRemovesNothing(t *testing.T) {
	lg := Open(t.TempDir())
	_, _ = lg.Observe(ollama(model("llama3.2:3b", "a80c")), time.Now())
	if events, _ := lg.Observe(nil, time.Now()); len(events) != 0 {
		t.Fatalf("a stopped runtime raised %+v", events)
	}
	if events, _ := lg.Observe(ollama(model("llama3.2:3b", "a80c")), time.Now()); len(events) != 0 {
		t.Fatalf("its return raised %+v", events)
	}
}

func TestPickleFormatsAreFlagged(t *testing.T) {
	lg := Open(t.TempDir())
	_, _ = lg.Observe(nil, time.Now())
	rt := []airuntime.Runtime{{Kind: airuntime.LlamaCpp, Models: []airuntime.Model{{Name: "mystery", Format: "pt"}}}}
	events, _ := lg.Observe(rt, time.Now())
	if len(events) != 1 || !events[0].Risky {
		t.Fatalf("got %+v, want a risky new model", events)
	}
	for _, f := range []string{"gguf", "safetensors", "mlx", ""} {
		if Risky(f) {
			t.Errorf("%q flagged as risky", f)
		}
	}
	for _, f := range []string{"pt", ".pth", "bin", "ckpt", "pickle"} {
		if !Risky(f) {
			t.Errorf("%q not flagged", f)
		}
	}
}

func TestLedgerSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Now().UTC().Truncate(time.Second)
	_, _ = Open(dir).Observe(ollama(model("llama3.2:3b", "a80c")), t0)

	lg := Open(dir)
	if events, _ := lg.Observe(ollama(model("llama3.2:3b", "a80c")), t0.Add(time.Hour)); len(events) != 0 {
		t.Fatalf("after a restart, a known model raised %+v", events)
	}
	if first, _ := lg.FirstSeen(airuntime.Ollama, "llama3.2:3b"); !first.Equal(t0) {
		t.Fatalf("first seen %v, want %v", first, t0)
	}
}
