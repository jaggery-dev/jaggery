# Testing

## Before every pull request

```sh
scripts/ci-local.sh
```

It runs what GitHub's CI runs: `gofmt` and `go vet` with the Go version in `go.mod`, the race-enabled tests, the personal-material check, a smoke test in a network namespace, the test house, the one-line installer against fake signed releases, and the cross-builds for every release target. A pull request is ready when it prints `all CI checks passed locally`.

It needs Linux with unprivileged user namespaces (most desktops). On macOS, run at least:

```sh
go vet ./... && go test -race ./...
```

and say so in the PR; CI runs the rest.

## Unit tests

`go test ./internal/router/` and so on. Copy the style of the package you're in: table tests, `t.TempDir()` for state, `httptest` for HTTP, `JAGGERY_STATE_DIR` to keep agents apart.

## The test house

```sh
scripts/house-test.sh              # three agents, every scenario, about a minute
scripts/house-test.sh --agents 5
scripts/house-test.sh --keep       # keep state and logs to look at
```

Runs N agents on one Linux machine, each in its own network and hostname namespace on a bridge (`192.0.2.0/24`), with multicast working between them, as if they were separate machines at home. Scenarios: joining with codes and house-wide trust, one agreed name holder, shared folders across machines, a fourth machine on a routed subnet joining with `--at`, and update rollouts against a fake release server (`scripts/fake-releases.py`), including a build that crashes and is rolled back.

Add a scenario when you change how machines behave together.

## The one-line installer

```sh
sh scripts/get-test.sh
```

`scripts/get.sh` is what `https://jaggery.dev/install` serves. The test runs it with `--download-only` against fake releases on a local web server, signed with a throwaway key. It takes a good release, `--beta` and `--version`. It refuses a damaged build, a changed `install.sh`, a build with new checksums but no matching signature, a release signed with an unknown key, and a withdrawn release. It also checks that `get.sh` carries the same signing keys as `internal/update/keys`: **when the keys change, change both**.

## What the test house can't show

Wi-Fi, sleep, phones and `.local` on Android, access-point isolation, the macOS Local Network prompt, real GPUs. Those are checked on real machines, and are where people with that hardware help most: look for issues labelled [needs hardware](https://github.com/jaggery-dev/jaggery/labels/needs%20hardware).

## Docs

If you change a command's usage text, regenerate the reference:

```sh
sh scripts/gen-cli-docs.sh
```

CI runs it with `--check` and fails if `docs/reference/cli.md` doesn't match the help text.
