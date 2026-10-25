# AGENTS.md

Instructions for AI coding agents working in this repo. People: the same rules are in [CONTRIBUTING.md](CONTRIBUTING.md), with more context.

## What this is

Jaggery is one Go binary, `jag`, that runs on every machine in a home: it finds the other machines (mDNS), pairs them (one-time codes, mutual TLS), shows them on one page (`jaggery.local`), and routes AI requests to whichever machine can serve them (`ai.local`). Read [docs/how-it-works.md](docs/how-it-works.md), then the package map in [docs/development/README.md](docs/development/README.md).

## Commands

```sh
go build -o jag ./cmd/jag          # build
go test ./internal/router/         # one package
scripts/ci-local.sh                # everything CI runs; must end "all CI checks passed locally"
scripts/house-test.sh --keep       # N agents on one Linux machine, in namespaces
sh scripts/gen-cli-docs.sh         # after changing any usage text in cmd/jag
sh scripts/gen-licences.sh         # after adding, removing or upgrading a dependency
```

- `scripts/ci-local.sh` formats and tests with the Go version in `go.mod`, not your local one. gofmt aligns trailing comments differently between versions: run it rather than plain `gofmt`.
- It needs Linux with unprivileged user namespaces for the smoke test and the test house. On macOS run `go vet ./... && go test -race ./...` and say so in the PR.
- To run an agent without touching a real identity: `JAGGERY_STATE_DIR=$(mktemp -d) ./jag up`.

## Rules

- **Branch from `beta`, open PRs against `beta`.** Never push to `prod`, create or move tags, or edit release workflows unless the issue asks for it.
- **One change per PR**, with tests. Copy the style of the package you're in: table tests, `t.TempDir()`, `httptest`.
- **No personal material.** Use neutral examples: hosts `nas`, `desktop`, `laptop`, `living-room-pi`; people "Alex"; addresses from `192.0.2.0/24`, `198.51.100.0/24`, `2001:db8::/32`; emails at `example.com`. Never paste paths from your own machine (`/home/<you>`, `/Users/<you>`). `scripts/check-personal.sh` enforces this.
- **Standard library first.** A new dependency must be MIT, BSD or Apache-2.0 and worth it; say why in the PR. No cgo.
- **State files change through `internal/compat`** (schema version and migration), so a rolled-back build can still read them. Changes to what agents send each other must stay within the protocol range there.
- **Words matter here.** Help text, errors and docs use plain English and British spelling, short sentences, no jargon, no exclamation marks, no emoji. Every error says what's wrong and the command that fixes it. Say "machine", "the house", "approve"; not "node", "cluster", "provision".
- **Only claim what works.** Docs describe today's behaviour; planned things go in [ROADMAP.md](ROADMAP.md) or a tech-spec, marked as coming.
- **The PR that changes behaviour changes the docs** in `docs/`, and regenerates `docs/reference/cli.md` if usage text changed.

## Before you design something

- Designs and the reasons behind them are in [tech-specs/](tech-specs/README.md). The current plan is [tech-specs/plan.md](tech-specs/plan.md); open work is in GitHub issues, and most issues say where to start and what "done" means.
- **Don't inspect request or file content** to judge it (privacy filters, classifiers): it was built and dropped. Read "Agents and content" in [tech-specs/decisions.md](tech-specs/decisions.md#agents-and-content-cmdjagmcpgo-internalshares-internalhosted) first.
- **Agents using Jaggery are read-only by default.** Don't add ways for an API caller or MCP client to approve models, trust machines, add shares, change update policy or add hosted providers: those stay at a terminal, by a person. Writable shares are the one exception, opt-in per share with every change undoable.
- Things Jaggery won't do: accounts, telemetry, installing or managing apps, splitting one model across machines, opening ports on the router.
- If the issue doesn't settle a design question, ask in the issue instead of guessing.

## Pull request

Fill in [the template](.github/pull_request_template.md): what changes for a person using Jaggery, how it was checked, which AI tools were used. A person must have read and run the change and answer for it; see "AI-assisted contributions" in [CONTRIBUTING.md](CONTRIBUTING.md).
