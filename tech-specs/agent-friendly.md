# Agent-friendly Jaggery: for people who use it, and people who build it, through agents

**Status: exploration 2026-10-17; decisions 2026-10-18 (below). C1 and C2 done.** Many people will run Jaggery through an AI agent ("set up Jaggery on my NAS", "why is ai.local slow?"), and many contributions will be written with one. This file collects what has become standard practice by late 2026, checks Jaggery against it, and proposes slices for the owner to choose from.

## What's standard now (checked 2026-10-17)

| Practice | What it is | Where it stands |
|---|---|---|
| **AGENTS.md** | A Markdown file at the repo root telling coding agents how to build, test and behave in this repo. Read by Codex, Copilot, Cursor, Gemini CLI, Goose and others; Claude Code reads `CLAUDE.md`, which can point at it. | A Linux Foundation project (Agentic AI Foundation, with MCP and goose) since December 2025; tens of thousands of repos. The de facto standard. |
| **Agent Skills (`SKILL.md`)** | A folder with a `SKILL.md` (name, description, instructions, optional scripts) that an agent loads only when a task matches. Portable across about 40 tools. | Open standard at agentskills.io since December 2025, stewarded by the same foundation. |
| **MCP** | The protocol agents use to call tools. Tools carry hints (`readOnlyHint`, `destructiveHint`) so clients can ask before acting. | Jaggery already serves `ai.local/mcp` (shares) with these hints set. |
| **CLIs agents can drive** | `--json` output with a stable shape, results on stdout and logs on stderr, documented exit codes, no prompts, `--dry-run` for changes, errors that say what to do next. | Widely written up as "agentic CLI design"; no formal standard. |
| **AI contribution policies** | Most popular projects that have a policy allow AI help but require a human who understands and answers for the change, and about half require disclosure. Some (curl, Ghostty) tightened after floods of plausible but wrong PRs; Ghostty now takes PRs only from vouched contributors. | Common, fragmented. Disclosure plus accountability is the middle path. |
| **llms.txt** | A Markdown index of a site's key pages at `/llms.txt`. | Adopted widely, but server logs show almost no AI crawler reads it. Cheap, low value. |
| **MCP Registry** (`server.json`) | Listing an MCP server for discovery. | For installable or remote servers. Jaggery's is on the home network, found by the agent at `ai.local/mcp`, so a listing would mislead. Not for now. |

## Part 1: using Jaggery through an agent

There are two kinds of agent: one **operating** Jaggery (an assistant with a shell, setting up and fixing a house) and one **using** the house (an app calling `ai.local` and `ai.local/mcp`).

### What already works
- `ai.local/v1` is OpenAI-compatible, so any agent framework can use the house's models.
- `ai.local/mcp` serves shares with read-only and destructive hints, and every call is logged.
- The CLI never prompts; every error says the command that fixes it (BRAND principle 4); `jag doctor` names each fix.
- Docs are plain Markdown in the repo, which agents read well.

### Gaps and proposals

| # | Slice | Why | Done when |
|---|---|---|---|
| **G1** | **`--json` on the read commands**: `status`, `peers`, `map`, `models`, `doctor`, `activity`, `outbound`, `share`, `files log`, `update --check`. One stable envelope (`{"ok": true, "data": …}` or `{"ok": false, "error": {"code": …, "message": …, "fix": …}}`), versioned with the protocol in `internal/compat`. | Agents now parse text meant for people, which breaks when we reword it, and rewording is something we do often. | An agent can run `jag doctor --json` and act on each `fix` without parsing prose. |
| **G2** | **Exit codes that mean something**, documented in the reference: 0 ok, 1 failed, 2 usage, 3 only test builds that crash on purpose (it never meant restart: an update re-executes in place), and for `doctor` 0 all pass, 4 warnings only, 5 failures. | Lets an agent (or a script) branch without parsing output. | Listed in `docs/reference/cli.md`; tests pin them. |
| **G3** | **Every command answers `--help`**, so an agent exploring the CLI never triggers an action by accident. | `jag join --help` currently tries to reach a host called `--help`. | Done. |
| **[G4](https://github.com/jaggery-dev/jaggery/issues/23)** | **A Jaggery skill** (`skills/jaggery/SKILL.md` in the repo, linked from the docs): how to install, join, read `jag doctor`, approve a model, add a share, add a provider safely; what never to do without asking the person (approve models, `forget` a machine, add `--writable`, add a hosted provider, change update policy). | One file teaches any agent to operate a house the way the docs intend, and loads only when needed. | An agent given only the skill takes a fresh machine to "joined and on the map", and stops to ask before each action in the "ask first" list. |
| **[G5](https://github.com/jaggery-dev/jaggery/issues/24)** | **Agents say who they are.** Document a header (`X-Jaggery-App: <name>`, falling back to User-Agent) so the activity log names "Goose" or "my-research-agent", not just the device. With K1, the key names the app. | Today only Open WebUI is named reliably. "Nothing out of sight" needs to say which agent did what. | `jag activity` shows the app name an agent sent. |
| **[G6](https://github.com/jaggery-dev/jaggery/issues/15)** | **Agent-facing capabilities in `/v1/models`** (H1, #15) and **the read-only house MCP server** (H3): machines, models, approvals, health and activity as MCP tools. | An agent can answer "which machine has GPU free?" or "why is this model refused?" from the house itself. | H1 and H3's own done-whens. |
| **G7** | **Prompt injection is documented as a risk.** Files in shares and replies from hosted models are untrusted text that can steer an agent. Add it to docs/security.md, and say why writes stay opt-in per share. | People should hear it from us first. | In docs/security.md. |

**Ask-first actions stay at a terminal.** Approving models, trusting or forgetting machines, writable shares and hosted providers change who can do what, or what leaves the house. Agents read; people decide ([decisions.md](decisions.md#agents-and-content-cmdjagmcpgo-internalshares-internalhosted)). An agent can *propose* the command; nothing in G1–G6 adds a way for an agent to approve its own access.

## Part 2: contributing to Jaggery with an agent

### What already helps
- **One command checks everything**: `scripts/ci-local.sh` (format, vet, race tests, personal-material check, smoke test, the test house, cross-builds). Agents do best with a single, deterministic "is it right?" command.
- **The test house** gives an agent multi-machine feedback on one laptop.
- **The personal-material check** stops an agent from pasting someone's paths or names into the repo.
- **Seeded issues** say where to start and have a checkable done-when, which is also what an agent needs.

### Proposals

| # | Slice | Done when |
|---|---|---|
| **C1** | **`AGENTS.md`** at the root, short (under 150 lines): build and test commands; Go version and the gofmt gotcha; PRs into `beta`; plain-English voice, British spelling; neutral examples; regenerate the CLI docs; where designs live; read `privacy-gate-learnings.md` before anything that inspects content; never touch `prod`, release tags or workflows' secrets. `CLAUDE.md` holds one line, `@AGENTS.md`, so both tools read one file. | An agent with no other context makes a change that passes `ci-local.sh` and follows the conventions. |
| **C2** | **AI contribution policy** in CONTRIBUTING: AI help is welcome; **a person answers for every PR**, has run it, and can explain each line; **say which tools you used** (a PR-template checkbox, as Ghostty started with); no PRs opened by an agent on its own; issues and security reports must be checked by a person before filing; maintainers may close unreviewed AI PRs without a detailed review. | The policy is in CONTRIBUTING and the PR template asks. |
| ~~C3~~ | ~~An `agent ready` label.~~ **Dropped 2026-10-18:** most issues are workable by an agent now; a separate label adds nothing. Issues keep a checkable done-when, which serves people and agents alike. | |
| **[C4](https://github.com/jaggery-dev/jaggery/issues/26)** | **Repo-level agent hygiene**: CI workflows never give secrets to PR builds from forks (already true for `pull_request`); no workflow runs an agent on issue or PR text with write access; the personal-markers check stays required. | Checked once and noted in releases.md. |
| **[C5](https://github.com/jaggery-dev/jaggery/issues/25)** | **Skills for contributors** (optional, after C1): `skills/add-app-fingerprint/`, `skills/add-house-test-scenario/`, the two most repeated tasks, each with the steps and the test to copy. | An agent adds a fingerprint from the issue form alone. |

## Part 3: jaggery.dev

- **`/llms.txt`**: a short index (what Jaggery is, links to the docs on GitHub, security page). Ten minutes of work; publish it, expect little traffic.
- **The docs stay Markdown in the repo**, the best format for agents; the site doesn't copy them.

## Recommended order

1. **C1 AGENTS.md** and **C2 policy**: before the repo is public, since that's when agent PRs arrive.
2. **G3** and **G2 exit codes**: small.
3. **G1 `--json`**: the biggest win for operating agents; do it once, with the envelope, rather than per command.
4. **G4 the skill**, written against G1's JSON.
5. **G5, G7**, with K1.
6. C4, C5 and llms.txt alongside.

## Decisions (2026-10-18)

1. **Disclosure: yes.** PRs say which AI tools were used, and a person answers for every PR.
2. **No `agent ready` label.** Most issues are workable by agents; C3 is dropped.
3. **`--json` is opt-in.** Output stays for people by default.
4. **The skill lives in this repo**, versioned with the CLI.

## Sources

- AGENTS.md and the Agentic AI Foundation: [Linux Foundation launch](https://itbrief.co.uk/story/linux-foundation-launches-agentic-ai-open-standards-hub), [adoption figures](https://codex.danielvaughan.com/2026/04/07/agents-md-open-standard-cross-tool-portability)
- Agent Skills: [overview and adopters](https://noqta.tn/en/news/skill-md-open-standard-30-ai-coding-tools-adoption-2026), [format](https://atlan.com/know/ai-agent/ai-agent-skills/what-are-agent-skills/)
- AI contribution policies: [RedMonk survey](https://redmonk.com/kholterhoff/2026/02/26/generative-ai-policy-landscape-in-open-source/), [study of 1,000 repos](https://arxiv.org/abs/2605.16706), [curl, Ghostty, tldraw](https://codenote.net/en/posts/oss-ai-slop-contribution-policy-shift/), [Ghostty's disclosure rule](https://newsletter.specstory.com/p/mitchell-hashimoto-on-the-ai-assisted)
- Agent-friendly CLIs: [agentic CLI design](https://skills.sh/tumf/skills/agentic-cli-design)
- llms.txt usage: [adoption vs requests](https://ppc.land/llms-txt-adoption-rises-8-8x-but-97-of-files-get-zero-ai-requests/)
- MCP Registry: [registry guide](https://roxyapi.com/blogs/mcp-registries-where-to-list-your-server)
