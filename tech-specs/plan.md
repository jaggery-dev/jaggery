# Where Jaggery is going

**Status: public alpha, October 2026.** What's built, what's next, and what 1.0 means. Open work is tracked as [issues](https://github.com/jaggery-dev/jaggery/issues); this page explains how the pieces fit. The bet and the scope are in [ROADMAP.md](../ROADMAP.md); the reasons behind past choices are in [decisions.md](decisions.md).

## Built

- **The map:** machines find each other with mDNS, join with a one-time code, trust each other house-wide over mutual TLS, and show on one page at `jaggery.local` with their hardware and the apps they run.
- **`ai.local`:** one OpenAI-compatible endpoint in front of Ollama, LM Studio and llama.cpp on every machine, routed by what's loaded and observed speed, with model approval and an activity log.
- **Hosted models, opt-in:** allow-listed providers and models, spend caps, groups with free allowances, and an outbound log of every request word for word.
- **Shared folders** for models and agents through `ai.local/mcp`: read-only unless made writable, every access logged, old versions kept 30 days ([shared-folders.md](shared-folders.md)).
- **App names** like `chat.local`, answered and forwarded by the name holder ([app-names.md](app-names.md)).
- **It looks after itself:** signed releases, updates one machine at a time with rollback and withdrawal, relays for machines that can't reach GitHub, `jag doctor`.
- **Linux, Raspberry Pi and macOS** (a LaunchDaemon), installed with one command.
- **Machines beyond the local network** join with `jag join <code> --at <address>`.
- **A test house:** many agents on one Linux machine in network namespaces, with a fake release server and a fake app, on every change.

## The alpha (now)

Jaggery is public and anyone can try it. The alpha is for learning what breaks in houses that aren't ours: networks, routers, hardware and apps we've never seen. What we already know is in [docs/known-issues.md](../docs/known-issues.md).

What we most want to hear about: how long two machines took to reach one page (the north star: under five minutes, with no config files), `.local` names on Android ([E2](https://github.com/jaggery-dev/jaggery/issues/18)), networks that block multicast ([E3](https://github.com/jaggery-dev/jaggery/issues/19)), NVIDIA and AMD GPUs ([#4](https://github.com/jaggery-dev/jaggery/issues/4), [#3](https://github.com/jaggery-dev/jaggery/issues/3)), and apps Jaggery doesn't recognise yet.

## Next

Roughly in this order; each track's issues say where to start and what done means.

| Track | What | Issues |
|---|---|---|
| **Fixes from the alpha** | Whatever the alpha finds in the first five minutes comes first. | [bugs](https://github.com/jaggery-dev/jaggery/issues?q=is%3Aopen+label%3Abug) |
| **GPUs** | AMD on Linux, NVIDIA memory, and a warning when a runtime runs on the CPU despite a GPU. | [#3](https://github.com/jaggery-dev/jaggery/issues/3), [#4](https://github.com/jaggery-dev/jaggery/issues/4), [#2](https://github.com/jaggery-dev/jaggery/issues/2) |
| **Keys (K1)** | Per-person and per-app keys on `ai.local` and the page, for households that want them. The activity log then names the key, not just the device. | [#14](https://github.com/jaggery-dev/jaggery/issues/14) |
| **Agents** | Each model's capabilities in `/v1/models` (H1), `/v1/embeddings` and Ollama-native passthrough (D2), agents naming themselves in the activity log, and a Jaggery skill for agents operating a house. | [#15](https://github.com/jaggery-dev/jaggery/issues/15), [#16](https://github.com/jaggery-dev/jaggery/issues/16), [#24](https://github.com/jaggery-dev/jaggery/issues/24), [#23](https://github.com/jaggery-dev/jaggery/issues/23) |
| **Hosted models from every machine** | Today only the machine holding the provider's key can use it; next, every machine through it (X6), with per-model prices (X7) and a warning when a provider is reachable around Jaggery (O6). | [#11](https://github.com/jaggery-dev/jaggery/issues/11), [#12](https://github.com/jaggery-dev/jaggery/issues/12), [#13](https://github.com/jaggery-dev/jaggery/issues/13) |
| **Runtimes** | vLLM detection, a model that isn't loaded yet, and setting a runtime's address. | [#6](https://github.com/jaggery-dev/jaggery/issues/6), [#5](https://github.com/jaggery-dev/jaggery/issues/5), [#1](https://github.com/jaggery-dev/jaggery/issues/1) |
| **The house looks after itself** | A stray or wedged agent says so (R7); Tools lists only apps the house can open (R8). | [#7](https://github.com/jaggery-dev/jaggery/issues/7), [#8](https://github.com/jaggery-dev/jaggery/issues/8) |
| **Test house** | A fake AI runtime and a fake hosted provider, so routing and hosted models are tested across machines on every change. | [#9](https://github.com/jaggery-dev/jaggery/issues/9), [#10](https://github.com/jaggery-dev/jaggery/issues/10) |
| **Platforms and networks** | Windows (A5), and tier-2 DNS if Android or multicast-blocking networks turn out to need it (A6). | [#17](https://github.com/jaggery-dev/jaggery/issues/17) |

Later, if wanted: revoking an approval and forgetting a machine house-wide (K2), agent apps and MCP servers recognised under Tools (H2), an MCP gateway that logs and controls agents' tool calls (K3), and an AI view on the page with measured speeds (D3, D4).

## 1.0

1.0 is the public launch, when the alpha has earned it:

- the first-five-minutes problems the alpha finds are fixed, on networks and hardware we didn't choose;
- the compatibility promises in [releases.md](releases.md) start to bind (state files, the house protocol, the CLI and `ai.local`);
- releases are also signed keylessly in public CI, so anyone can check a build came from this repo;
- the name has been checked and registered;
- and only then the wider launch: a "how Jaggery works" post, Show HN and the communities, one at a time.

Until then, versions are `0.x`, and a minor release may change things, with a note.
