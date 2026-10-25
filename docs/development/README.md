# Architecture tour

Jaggery is one Go binary, `jag`, with no cgo and five direct dependencies (see `go.mod`). The command line and the agent's wiring are in `cmd/jag`; everything with a clear job of its own is a package in `internal/`. The designs behind it are in [tech-specs/](../../tech-specs/README.md); [mvp.md](../../tech-specs/mvp.md) is the best long read.

## How a request flows

**A machine joins.** `jag invite` writes a one-time code (`internal/pairing`). `jag join <code>` finds machines over mDNS (`internal/discovery`, `internal/mdns`), sends a join request to port 7373, and both sides prove they hold their keys and pin each other (`internal/pairing`, `internal/home`). From then on they talk over mutual TLS on 7374 (`internal/secure`), and the house's signed lists of trusted machines spread the trust (`internal/house`).

**The page loads.** The agent collects its own hardware (`internal/inventory`), apps (`internal/apps`) and AI runtimes (`internal/airuntime`), pulls the same from each approved peer over mTLS, and writes a snapshot (`internal/house`). The page (`internal/dashboard`, one static HTML file embedded in the binary) polls the agent for it.

**A chat request arrives at `ai.local`.** `cmd/jag/router.go` checks approval (`cmd/jag/approval.go`), asks `internal/router` which machine should serve it (a pure function of the snapshot and measured speeds), then either proxies to the local runtime or forwards to the chosen peer over mTLS, streaming the reply back while measuring tokens per second. The request goes into the activity log (`internal/activity`). A hosted model (`provider/model`) goes instead through `internal/hosted`: allow-list, cap, and the outbound log.

**An agent reads a shared file.** `cmd/jag/mcp.go` serves the MCP server at `/mcp` (official Go MCP SDK). A share on this machine is read through `os.Root` (`internal/shares`); a share elsewhere is forwarded over mTLS. Every call is logged (`internal/shares/log.go`).

**An update comes out.** Every 30 minutes `cmd/jag/autoupdate.go` asks `internal/update` for the newest release on the channel, waits its turn (the name holder first), downloads and checks it, swaps the binary keeping the old one, and re-execs. Three failed starts roll it back. `cmd/jag/relay.go` passes builds to peers without GitHub access.

## Package map

| Package | Job |
|---|---|
| `cmd/jag` | Commands, the agent's HTTP handlers, and the wiring between packages |
| `internal/activity` | The AI activity log, a file per day |
| `internal/airuntime` | Finds Ollama, LM Studio and llama.cpp on this machine, and their models |
| `internal/apps` | Recognises web apps (the fingerprint catalog), **the easiest place to start** |
| `internal/compat` | Protocol ranges between versions, and state-file schema migrations |
| `internal/dashboard` | The embedded page |
| `internal/discovery`, `internal/mdns` | Announcing and finding machines; answering for `jaggery.local` and `ai.local` |
| `internal/home` | This machine's list of peers and approvals |
| `internal/hosted` | Hosted providers: config, spend, the outbound log |
| `internal/house` | The assembled view of the house; which machine holds the shared names |
| `internal/identity` | The machine's id and key pair; the state directory |
| `internal/inventory` | CPU, memory, disks, GPUs, battery (per OS) |
| `internal/modelwatch` | Every model seen, by digest |
| `internal/netif` | Which network interfaces count as the home network |
| `internal/pairing` | Invite codes and the proof-of-possession exchange |
| `internal/reach` | Can the rest of the house reach this machine's ports? |
| `internal/router` | Which machine serves a model request |
| `internal/secure` | Mutual TLS with self-signed certificates and pinned keys |
| `internal/shares` | Shared folders, versions and the access log |
| `internal/update` | Finding, checking and installing releases; update policy |
| `internal/version` | What this build is |

## Conventions

- **Standard library first.** A new dependency must be permissively licensed (Apache-2.0, MIT, BSD) and worth its weight; say why in the PR, and run `sh scripts/gen-licences.sh` so `jag licences` carries its notice.
- **Plain English** in docs, help text and error messages: say what's wrong and the command that fixes it. The voice is in the site's brand notes: short sentences, no jargon, British spelling.
- **Test without a network.** Decisions are pure functions where possible (`internal/router`); HTTP goes through `httptest`; multi-machine behaviour goes in the test house.
- **No personal material** in code, tests or docs: example hosts like `nas` and `desktop`, people like Alex, addresses from `192.0.2.0/24`, `198.51.100.0/24`, `2001:db8::/32`, emails at `example.com`. CI checks.
- **State files change through `internal/compat`**, so an older build can still read them after a rollback.

Next: [testing](testing.md).
