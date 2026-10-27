# Files, ports and environment variables

## Ports

| Port | Reachable from | Serves |
|---|---|---|
| 80/tcp | home network | The page, `ai.local/v1`, `ai.local/mcp` (when the agent may bind it) |
| 7373/tcp | home network | The same, always; plus identity and join requests between machines |
| 7374/tcp | approved machines | Mutual TLS between agents |
| 5353/udp | home network | mDNS (shared with Avahi or mDNSResponder) |

AI runtimes the agent looks for on its own machine: Ollama 11434, LM Studio 1234, llama.cpp 8080, vLLM 8000, plus the ports in `runtimes.json`.

## The state directory

`~/.config/jaggery` on Linux, `~/Library/Application Support/jaggery` on macOS, or `$JAGGERY_STATE_DIR`. Owned by the user the agent runs as. Uninstalling keeps it; deleting it makes the machine new to the house.

| Path | What | You edit it? |
|---|---|---|
| `identity.json` | This machine's id, name and Ed25519 key pair | No. Keep it private. |
| `peers.json` | Machines this one knows, approved or pending, with pinned keys | No: `jag approve`, `jag forget` |
| `house.json` | The house's signed lists of trusted machines | No |
| `invite.json` | The one pending invite code, while it lasts | No |
| `models.json`, `approvals.json` | Models seen, and the house-wide approvals | No: `jag models approve` |
| `tools.json` | Apps seen on the network, for the page's alerts | No |
| `shares.json` | Folders this machine lends | No: `jag share` |
| `apps.json` | Apps and links added to the page from this machine, and the names given to its apps | No: `jag apps` |
| `files/` | Shared-folder access log, a file per day (30 days) | No: `jag files log` |
| `activity/` | Model requests, a file per day (30 days) | No: `jag activity` |
| `runtimes.json` | AI runtimes on ports other than their defaults | **Yes**: [how-to](../how-to/use-ai-local.md#a-runtime-on-another-port) |
| `ai-pause.json` | This machine's AI pause, while it lasts | No: `jag ai pause`, `jag ai resume` |
| `providers.json` | Hosted model providers, caps and groups | **Yes**: [how-to](../how-to/hosted-models.md) |
| `*.key` | Hosted providers' API keys, mode `0600` | **Yes** |
| `hosted-spend.json` | Spend and free allowance used, per provider | No |
| `outbound/` | Every hosted request and reply, word for word (30 days, `0700`) | No: `jag outbound` |
| `update.json` | Update policy and channel | No: `jag update --policy` |
| `update-pending.json`, `update-bad.json`, `update-withdrawn.json` | A build on trial, builds that failed here, builds withdrawn | No |
| `releases/` | Builds kept to pass on to machines without GitHub access | No |
| `github-token` | Optional GitHub token for update checks; raises GitHub's rate limit | Yes, `0600` |
| `state.json` | The state format's version, for upgrades and rollbacks | No |

Inside each writable share, `.jaggery-versions/` keeps old copies of changed files for 30 days.

## Environment variables

| Variable | Effect |
|---|---|
| `JAGGERY_STATE_DIR` | Use another state directory (tests run many agents on one machine this way) |
| `JAGGERY_GITHUB_TOKEN` (or `GITHUB_TOKEN`) | Token for update checks |
| `JAGGERY_RELEASES_API` | Another release server (tests) |
| `JAGGERY_UPDATE_TIMES` | Update timings in seconds instead of minutes (tests) |
| `JAGGERY_ALLOW_FROM` | More addresses or prefixes the page port answers, comma-separated, beyond private, link-local, on-link and approved machines' addresses (see [security](../security.md#web-pages-and-the-internet)) |
| `JAGGERY_ALLOW_HOSTS` | More host names the page port answers to, beyond IP addresses and local names |
