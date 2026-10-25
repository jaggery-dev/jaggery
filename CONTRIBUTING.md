# Contributing to Jaggery

Thank you for looking. Jaggery is young and maintained by one person, so the most useful thing you can bring is something we can't do alone: hardware we don't have, apps we don't run, and fresh eyes on the first five minutes.

## Ways to help

- **Try it and tell us what happened.** An issue that says "I ran X on Y, expected Z, got this" is gold, especially on a network or machine we've never seen. Include `jag doctor` and `jag version`.
- **Teach it an app.** Jaggery recognises self-hosted apps by probing a port and matching what comes back. Adding one is a few lines in `internal/apps/apps.go` and a test: see the issues labelled [good first issue](https://github.com/jaggery-dev/jaggery/labels/good%20first%20issue).
- **Run it on hardware we don't have:** NVIDIA, AMD, Windows, a NAS, Android, mesh Wi-Fi. See [needs hardware](https://github.com/jaggery-dev/jaggery/labels/needs%20hardware).
- **Fix the words.** Help text, error messages and docs should say what's wrong and what to type. If something confused you, it's a bug.
- **Code** from the issues labelled [help wanted](https://github.com/jaggery-dev/jaggery/labels/help%20wanted).

## Before you write code

- **Small fixes** (a typo, an app fingerprint, a clearer message): just open a pull request.
- **Anything bigger:** comment on the issue, or open one, and say how you'd do it. Issues labelled `needs design` aren't agreed yet; let's talk first so your time isn't wasted.
- Jaggery deliberately doesn't do some things (accounts, telemetry, installing apps, splitting one model across machines). The [roadmap](ROADMAP.md) lists them.

## Build and run

You need Go (the version in `go.mod`) and Linux or macOS.

```sh
git clone https://github.com/jaggery-dev/jaggery
cd jaggery
go build -o jag ./cmd/jag
JAGGERY_STATE_DIR=$(mktemp -d) ./jag up     # an agent with a throwaway identity
```

To see a whole house on one Linux machine: `scripts/house-test.sh --keep`.

Read the [architecture tour](docs/development/README.md) to find your way around, and [testing](docs/development/testing.md) for what to run.

## Pull requests

1. Branch from **`beta`** and open the pull request against `beta`. (`prod` only moves when a beta build passes the [release gates](tech-specs/releases.md).)
2. Run `scripts/ci-local.sh` and say in the PR that it passed (or what you could run, on macOS).
3. Keep it to one change. Say what it changes for a person using Jaggery, and how you checked it.
4. If it changes behaviour, change the docs in the same PR. If it changes usage text, run `sh scripts/gen-cli-docs.sh` (CI fails until you do).
5. Use neutral examples: hosts like `nas`, people like Alex, addresses from `192.0.2.0/24`. CI rejects personal material.

Pull requests are squash-merged, so the PR title becomes the commit message: write it as what the change does, in plain words. A pull request merges once GitHub's CI passes and a maintainer has reviewed it. For a first contribution, a maintainer has to approve the CI run before it starts (a GitHub setting), so don't worry if it waits.

## What to expect

Jaggery is in alpha and has one maintainer for now. During the alpha:

- **Issues and pull requests get a first answer within two working days.** New issues carry `needs triage` until then.
- **Security reports** get an answer within two working days too ([SECURITY.md](SECURITY.md)).
- **Discussions** are answered when there's time; other people's answers are very welcome.
- **Not everything will be accepted.** Jaggery stays small on purpose (see "Explicitly out of scope" in the [roadmap](ROADMAP.md)), so for anything bigger than a fix, open an issue first and agree the approach.

## AI-assisted contributions

Using AI tools to write code, tests or docs is welcome. Most of Jaggery has been written with them. Coding agents should read [AGENTS.md](AGENTS.md), which covers the same rules in short.

What we ask:

- **A person answers for every pull request.** You've read every line, run it, and can explain it and answer review questions yourself. Don't open PRs, issues or comments from an agent running on its own.
- **Say which tools you used**, and for what, in the PR template ("Claude Code wrote the tests", "Copilot completions"). This isn't a judgement; it tells the reviewer how to spend their attention.
- **Check what the AI claims.** Issues, bug reports and security reports must describe something you saw happen, not something a model said might happen.
- **Keep it small.** A large AI-generated PR nobody asked for is hard to review and likely to be closed. Open an issue first.

Maintainers may close pull requests that look unreviewed without a detailed reply.

## Sign your work (DCO)

The decision and its reasons are in [tech-specs/licensing.md](tech-specs/licensing.md). The project name is covered by [TRADEMARKS.md](TRADEMARKS.md), not the licence.

Jaggery is licensed under Apache-2.0, and contributions are accepted under the same licence. There's no contributor licence agreement. Instead, each commit is signed off under the [Developer Certificate of Origin](https://developercertificate.org/), which says you wrote it or have the right to submit it:

```sh
git commit -s -m "Recognise Navidrome"
```

This adds `Signed-off-by: Your Name <you@example.com>` to the commit. Your GitHub `noreply` address (GitHub → Settings → Emails) is fine, if you'd rather not publish your email. The sign-off must use the same email as the commit's author. A check on every pull request (`scripts/check-dco.sh`) says which commits need it. To sign off commits you've already made, run `git rebase --signoff origin/beta`.

## Security problems

Please don't open a public issue. See [SECURITY.md](SECURITY.md).

## Conduct

Be kind and assume good intent. See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
