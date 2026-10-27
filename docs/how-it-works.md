# How Jaggery works

A plain-language tour. The designs and the measurements behind each choice are in [tech-specs/](../tech-specs/README.md), starting with [decisions.md](../tech-specs/decisions.md).

## One agent per machine

Each computer runs the same small program, `jag up`, usually as a service. There is no central server and no leader to lose: every agent keeps its own copy of what it knows about the house, and any of them can show the page.

## Finding each other

Agents announce themselves over **mDNS** (`_jaggery._tcp`), the way a phone finds a printer, and look for each other every 10 seconds. mDNS only works inside one network: a machine behind a VPN or on another subnet joins by address instead ([how-to](how-to/join-from-another-network.md)).

## Trust

Finding a machine isn't trusting it. Each agent has its own key pair, made on first run (`jag status` shows its fingerprint). A machine is trusted when:

- it joined with a **one-time code** from `jag invite` (six words, works once, for a short time); both sides then pin each other's key; or
- someone ran `jag approve <id>` for it.

Approved machines talk to each other only over **mutual TLS** with those pinned keys. Each machine signs the list of machines it trusts, and the house shares those lists, so a machine joined through one member is trusted by all of them.

## Names

Two names are shared by the whole house: **`jaggery.local`** (the page) and **`ai.local`** (the AI endpoint). One machine, the *name holder*, answers for them over mDNS. The house agrees on it without voting: a machine that stays on beats a laptop (laptops sleep), then one that can serve port 80 beats one that can't, and then the lowest id wins. If the holder goes away, the next one takes over within seconds.

Each machine also keeps its own `<hostname>.local`.

The name holder also answers for the names the owner gives apps (`jag apps name "Open WebUI" chat`), and any agent that receives a request for `chat.local` passes it on to the app, on its own machine and port. It only ever reaches apps the house can already open at their machine's address.

## The page

Every agent serves the same page on port 80 (when it may) and on 7373: machines, hardware, recognised apps, alerts and recent activity. Apps are recognised by probing known ports and matching what they answer ([the list](../internal/apps/apps.go)).

## `ai.local`

Each agent looks for AI runtimes on its own machine (Ollama on 11434, LM Studio on 1234, llama.cpp on 8080, vLLM on 8000, and any other ports listed in `runtimes.json`) and reports their models. `ai.local/v1/models` lists every model in the house. A chat request leaves out machines whose owner ran `jag ai pause`, then goes to:

1. a machine that already has the model loaded, then one loading it for another request; otherwise
2. among machines the model fits on (free GPU memory, or RAM), the one that has generated fastest so far, trying unmeasured machines once so they get a number.

Embeddings (`/v1/embeddings`) and Ollama's own API (`/api/chat`, `/api/tags` and the rest, for Ollama models) are routed the same way.

GPU memory comes from `nvidia-smi` (NVIDIA), the `amdgpu` driver (AMD on Linux, cards with 2 GB or more of their own memory) and macOS (Apple Silicon). Free memory only filters; measured speed ranks. (Ranking by free memory picked a machine five times slower in testing.) The request travels to the chosen machine over mutual TLS, and that machine's agent talks to its runtime on localhost, so runtimes never need to be open to the network.

A model that appears after Jaggery first looked waits for approval (`jag models approve`), and approval pins the model's files, so a model changed under the same name waits again. Every request is written to the activity log (`jag activity`), kept 30 days.

**It's a router, not a cluster:** one request runs on one machine.

## Shared folders

`jag share add <folder>` lends a folder. Models and agents reach every share in the house through one MCP server at `ai.local/mcp`; a share on another machine is read over mutual TLS. Shares are read-only unless added `--writable`; every write keeps the old version for 30 days; dotfiles, `*.key` and `*.pem` are never served. Each machine logs what was read and changed (`jag files log`).

## Hosted models

Off until you add a provider to `providers.json` on one machine, the *exit*. Each provider needs a model allow-list, prices and a spending cap; groups name an ordered list of models used as one, free allowances first. Every request and reply is kept word for word for 30 days (`jag outbound`). Today only the exit machine lists hosted models; other machines will route to it soon.

## Updates

Each machine checks GitHub for a new release on its channel every 30 minutes. With the `auto` policy (the default for beta builds) the name holder installs first, the rest follow one at a time once it has proved the build; a build that fails to start three times is rolled back and marked bad for the whole house. A machine without internet access gets the build from a trusted machine that has it. Every release is signed in CI, and a machine installs a build only if the signature and the checksum both hold.

## What it can't do yet

- Per-person keys: `ai.local` and the page answer anyone on your home network.
- Windows, Android checks, NVIDIA checked on a home machine, AMD GPUs on macOS.
- Names for apps (`photos.local`), HTTPS for local names, remote access from outside the house.

The plan for each is in the [roadmap](../ROADMAP.md) and [tech-specs/plan.md](../tech-specs/plan.md).
