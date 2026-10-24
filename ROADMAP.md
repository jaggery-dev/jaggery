# Jaggery: v1 scope and roadmap

## The bet

The person who sets up the home tech for everyone else (the "family IT person") will adopt a tool that makes their *existing* machines feel like one system in under five minutes. The AI router is the hook that gets attention; names and the map are what keep people using it.

**North-star metric:** time from `jag up` on the second machine to seeing both on the map with named apps. Target: under 5 minutes, zero config edits.

## v1 in one sentence

Run one command on each machine → they find each other, show up on one map, get memorable names for themselves and their apps, and pool every local AI runtime behind `ai.local`.

## In scope for v1

### 1. Agent
- Single static binary (Go): Linux x86/ARM, macOS, Windows. Docker image too.
- `jag up`, `jag join <code>`, `jag status`, `jag rename`.
- Runs as a service (systemd, launchd, Windows service).

### 2. Discovery and membership
- mDNS/DNS-SD announcement (`_jaggery._tcp`), UDP broadcast fallback.
- Gossip membership (e.g. hashicorp/memberlist) so there is no leader to lose.
- Pairing: the first machine creates a home; new machines appear as "pending" until approved in the dashboard or joined with a 6-word code.
- mTLS between agents, keys generated on first run.

### 3. Machine inventory and roles
- Reports CPU, RAM, disks, GPUs (NVIDIA via NVML, Apple Silicon unified memory, AMD via ROCm SMI where available), uptime.
- Auto-assigns roles, editable: **Storage** (large disks or known NAS OS), **Server** (always-on, running services), **AI** (GPU, or more than 32 GB unified memory).

### 4. Service detection
- Listening ports + Docker labels + HTTP fingerprinting.
- Fingerprint library as community YAML (port, path, title/header match, suggested name, icon). Ship ~50 at launch: Jellyfin, Plex, Immich, Nextcloud, Home Assistant, Syncthing, Portainer, Open WebUI, Ollama, LM Studio, Vaultwarden, Paperless-ngx, Audiobookshelf, AdGuard Home, Pi-hole, etc.

### 5. Names
- Tier 1 (zero config): publish machine names and service aliases over mDNS as `.local`.
- Tier 2 (opt-in): built-in DNS server for a chosen suffix (default `.internal`) with forwarding to the upstream resolver, for Android apps, TVs and consoles. Shows exact router instructions for common routers.
- Per-machine reverse proxy on :80 routing by Host header, so `photos.local` needs no port.
- Name conflicts resolved with a suffix and a dashboard warning.

### 6. AI router (`ai.local`)
- Detects Ollama, LM Studio, llama.cpp server, vLLM, exo on every machine.
- OpenAI-compatible `/v1/models` (merged list) and `/v1/chat/completions`, `/v1/embeddings`, with streaming. Ollama-native API passthrough for apps that only speak Ollama.
- Routing order: machine already has the model loaded → among machines with enough free memory to hold it, the one with the best observed tokens/sec → configurable fallback machine. Queue-aware. (Free memory is an eligibility filter only; ranking by it was measured to pick a node 4.9× slower — see [tech-specs/decisions.md](tech-specs/decisions.md#routing-internalrouter-internalinventory).)
- Dashboard view: which models live where, what's loaded, live tokens/sec.

### 7. Dashboard
- Served by every agent at `jaggery.local`. Embedded web UI, works on phones.
- Views: Home map, Apps (big tiles a non-technical family member can use), AI, Settings.

## Explicitly out of scope for v1
- Remote access from outside the house (recommend Tailscale; integrate later).
- Per-person access control.
- Trusted HTTPS for local names.
- Splitting a single model across machines (that's exo's job).
- Managing or installing apps (that's Umbrel/CasaOS's job).
- Telemetry, or an account with us. No cloud service is *required*: hosted models are opt-in, through Jaggery's exit (allow-listed providers, spend caps, an outbound log; [docs/how-to/hosted-models.md](docs/how-to/hosted-models.md)).

## Milestones

Milestones have names, not numbers: version numbers belong to releases ([tech-specs/releases.md](tech-specs/releases.md)), and a release *claims* a milestone once its done-when is met on real hardware. The public launch is release **1.0**. Older docs use the former numbers, shown in brackets.

| Milestone | Ships | Done when |
|---|---|---|
| **Map** (was 0.1) | Agent, discovery, pairing, map | Two machines see each other in < 2 min |
| **Names** (was 0.2) | Service fingerprints, `.local` names, reverse proxy | `photos.local` works from an iPhone with no setup |
| **AI router** (was 0.3) | AI router | Open WebUI on the NAS uses the gaming PC's GPU via `ai.local` |
| **App stores** (was 0.4) | Jaggery DNS (tier 2), Umbrel + CasaOS apps, Home Assistant integration | Installable from those app stores |
| **Launch** (was 0.5) | Polish, docs, demo video; release 1.0 | **Public launch** |
| **After launch** (was 0.6+) | Per-person access, local HTTPS, Tailscale integration, wake-on-LAN for sleeping GPU boxes | |

## Key risks
- **mDNS is inconsistent** across Android apps, smart TVs and some Wi-Fi mesh systems that block multicast. Mitigation: tier 2 DNS, plus a clear "test your network" check in the dashboard.
- **Port 80 is often taken** on NAS boxes and Umbrel. Mitigation: fall back to a per-machine alt port and use Jaggery DNS + a central proxy.
- **GPU telemetry differs per platform.** Start with NVIDIA and Apple Silicon; AMD best-effort.
- **Scope creep into an OS.** Every feature request that starts "can Jaggery install…" is a no for v1.

## Launch plan
1. **Public alpha (now):** the repo is public and anyone can install it. Feedback in issues and Discussions; shared one community at a time, never all at once. What's known not to work is in [docs/known-issues.md](docs/known-issues.md).
2. **Fix the first-five-minutes problems** the alpha finds, on networks and hardware we didn't choose.
3. **1.0, the launch:** a "how Jaggery works" post, then Show HN and the communities (r/selfhosted, r/homelab, r/LocalLLaMA) one at a time, each after the last one's issues are fixed. Umbrel and CasaOS store apps with the App stores milestone.

What 1.0 needs is in [tech-specs/plan.md](tech-specs/plan.md#10).

## Stack
- Go, one static binary, no cgo. Standard library first; direct dependencies: miekg/dns, the official MCP Go SDK, golang.org/x/{net,sys,mod}. All permissively licensed.
- Our own small mDNS participant (`internal/mdns`); no gossip library: each machine pulls from its approved peers over mutual TLS, and the house's trust lists are signed.
- An embedded single-file web page, no build step.
- State is JSON files in the state directory, versioned through `internal/compat`.
- How it fits together: [docs/development/](docs/development/README.md).
