// Package inventory collects what a machine is: its CPU, memory, disk, and a
// guess at the role it plays in the house. The facts are the raw material for
// the map (step 5) and for routing decisions.
//
// Collection is platform-specific and lives in build-tagged files. Anything a
// platform can't answer yet is left zero rather than failing the whole report,
// so a partial inventory still reaches the map.
package inventory

import (
	"context"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/jaggery-dev/jaggery/internal/activity"
	"github.com/jaggery-dev/jaggery/internal/airuntime"
	"github.com/jaggery-dev/jaggery/internal/apps"
	"github.com/jaggery-dev/jaggery/internal/compat"
	"github.com/jaggery-dev/jaggery/internal/modelwatch"
	"github.com/jaggery-dev/jaggery/internal/reach"
	"github.com/jaggery-dev/jaggery/internal/shares"
	"github.com/jaggery-dev/jaggery/internal/update"
	"github.com/jaggery-dev/jaggery/internal/version"
)

type Role string

const (
	// Server: an always-on machine running services. The default.
	RoleServer Role = "server"
	// Storage: a machine whose point is its disks (a NAS).
	RoleStorage Role = "storage"
	// AI: a machine with a GPU or a lot of memory, worth routing models to.
	RoleAI Role = "ai"
)

type GPU struct {
	Vendor      string `json:"vendor"`
	Model       string `json:"model"`
	MemoryBytes uint64 `json:"memory_bytes"`
	// FreeBytes is nil when unknown, which is different from none free.
	FreeBytes *uint64 `json:"free_bytes,omitempty"`
	// Unified marks a GPU sharing system memory (Apple Silicon). Its
	// MemoryBytes is the share the OS lets the GPU use, not the whole pool.
	Unified bool `json:"unified,omitempty"`
}

// Pause is a machine's owner keeping AI requests off it for a while, for
// example during a game (jag ai pause). Until is nil until resumed.
type Pause struct {
	Since time.Time  `json:"since"`
	Until *time.Time `json:"until,omitempty"`
}

// Active reports whether the pause still holds at now; a nil pause doesn't.
func (p *Pause) Active(now time.Time) bool {
	return p != nil && (p.Until == nil || now.Before(*p.Until))
}

type Report struct {
	// Version is the agent build, e.g. "beta.41 (9c16d76, built …)".
	Version string `json:"version,omitempty"`
	Commit  string `json:"commit,omitempty"`
	// Build is the bare version ("0.5.0-beta.16"), for comparing; Version is
	// for people.
	Build string `json:"build,omitempty"`
	// Protocol..MinProtocol is the house protocol range this build speaks;
	// zeros from builds before it was reported.
	Protocol    int `json:"protocol,omitempty"`
	MinProtocol int `json:"min_protocol,omitempty"`
	// RunningSince is when the agent started on this build, and
	// UpdatePending says the build is still on trial after an update. The
	// rest of the house installs a build only once the first machine has
	// run it for a while. Set by the running agent.
	RunningSince  time.Time `json:"running_since,omitempty"`
	UpdatePending bool      `json:"update_pending,omitempty"`
	// Asset is this machine's release file, so a machine relaying updates
	// knows which builds the house needs.
	Asset string `json:"asset,omitempty"`
	// Update is what the agent last learned about updates; BadCommits are
	// builds it rolled back from, which the rest of the house then skips.
	// Both set by the running agent.
	Update     *update.Status `json:"update,omitempty"`
	BadCommits []string       `json:"bad_commits,omitempty"`
	// WithdrawnCommits are releases pulled after publication, as this
	// machine saw on GitHub; the whole house leaves them (P0c).
	WithdrawnCommits []string            `json:"withdrawn_commits,omitempty"`
	Hostname         string              `json:"hostname"`
	OS               string              `json:"os"`
	Arch             string              `json:"arch"`
	CPUModel         string              `json:"cpu_model"`
	CPUCount         int                 `json:"cpu_count"`
	MemoryBytes      uint64              `json:"memory_bytes"`
	DiskTotalBytes   uint64              `json:"disk_total_bytes"`
	DiskFreeBytes    uint64              `json:"disk_free_bytes"`
	GPUs             []GPU               `json:"gpus"`
	Runtimes         []airuntime.Runtime `json:"runtimes"`
	// AIPaused: the router sends this machine no AI requests while it holds.
	// Set by the running agent.
	AIPaused *Pause `json:"ai_paused,omitempty"`
	// Serves are the model requests beyond chat this machine's agent serves
	// for the house ("embeddings", "ollama/chat", …). Set by the running
	// agent; builds from before it serve chat only.
	Serves []string `json:"serves,omitempty"`
	// ModelEvents are the last week's models appearing, changing or going on
	// this machine. Set by the running agent.
	ModelEvents []modelwatch.Event `json:"model_events,omitempty"`
	// ToolEvents are the last week's first sightings of an app offering a
	// model a tool, on requests this machine routed.
	ToolEvents []activity.ToolEvent `json:"tool_events,omitempty"`
	// Apps are the web apps this machine serves to the LAN. Set by the
	// running agent, which knows the address the house reaches it on.
	Apps []apps.App `json:"apps,omitempty"`
	// Shares are the folders this machine lends to models and agents
	//: names only, never paths. Set by the running agent.
	Shares []shares.Info `json:"shares,omitempty"`
	// Inbound is the last dial-back from a peer: can the house reach this
	// machine's ports? Set by the running agent, nil until checked.
	Inbound *reach.Report `json:"inbound,omitempty"`
	// Laptop: the machine runs on a battery, so it sleeps and leaves. It
	// should never answer for the house's shared names if anything else can.
	Laptop bool `json:"laptop,omitempty"`
	// ServesWeb: the agent holds port 80, so http://jaggery.local works
	// without a port. Set by the running agent.
	ServesWeb   bool      `json:"serves_web,omitempty"`
	Role        Role      `json:"role"`
	BootTime    time.Time `json:"boot_time"`
	CollectedAt time.Time `json:"collected_at"`
}

// thresholds for the role guess. Deliberately coarse — a first guess a human
// can override, not a classifier.
const (
	aiMemoryBytes   = 32 << 30 // 32 GiB of RAM suggests a machine meant for models
	storageDiskByte = 2 << 40  // 2 TiB of disk suggests a NAS
)

// Collect gathers this machine's inventory. Platform helpers that can't answer
// return zero values; the report is still returned.
func Collect() Report {
	r := Report{
		Version:     version.String(),
		Commit:      version.Commit,
		Build:       version.Version,
		Protocol:    compat.Protocol,
		MinProtocol: compat.MinProtocol,
		Asset:       version.Asset(),
		Hostname:    hostname(),
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		CPUModel:    cpuModel(),
		CPUCount:    runtime.NumCPU(),
		MemoryBytes: memoryBytes(),
		GPUs:        detectGPUs(),
		Runtimes:    airuntime.Detect(context.Background()),
		BootTime:    bootTime(),
		Laptop:      laptop(),
		CollectedAt: time.Now().UTC(),
	}
	r.DiskTotalBytes, r.DiskFreeBytes = rootDisk()
	fillUnifiedFree(r.GPUs, r.Runtimes)
	r.Role = guessRole(r)
	return r
}

// fillUnifiedFree estimates free memory on a unified-memory GPU as its budget
// minus what the loaded models hold, since no API reports it without cgo.
// Models are the dominant GPU user on an inference box, which is the case
// this number exists for.
func fillUnifiedFree(gpus []GPU, runtimes []airuntime.Runtime) {
	var held uint64
	for _, rt := range runtimes {
		for _, m := range rt.Models {
			held += m.VRAMBytes
		}
	}
	for i := range gpus {
		if !gpus[i].Unified || gpus[i].FreeBytes != nil {
			continue
		}
		free := uint64(0)
		if gpus[i].MemoryBytes > held {
			free = gpus[i].MemoryBytes - held
		}
		gpus[i].FreeBytes = &free
	}
}

// guessRole picks a first-guess role from the hardware. A GPU or a lot of
// memory means AI; a very large disk means storage; otherwise server.
func guessRole(r Report) Role {
	if len(r.GPUs) > 0 || r.MemoryBytes >= aiMemoryBytes {
		return RoleAI
	}
	if r.DiskTotalBytes >= storageDiskByte {
		return RoleStorage
	}
	return RoleServer
}

var (
	laptopOnce sync.Once
	isLaptop   bool
)

// laptop is hasBattery, asked once: a machine doesn't grow a battery.
func laptop() bool {
	laptopOnce.Do(func() { isLaptop = hasBattery() })
	return isLaptop
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}
