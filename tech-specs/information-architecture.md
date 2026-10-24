# Information architecture: the repo, the backlog and jaggery.dev

**Status: agreed 2026-10-17.** Where each kind of writing about Jaggery lives, who it is for, and how a new developer gets from "what is this?" to a merged pull request. The launch order is in the site's launch plan; this file is about the words and where they go.

## Who reads what

| Reader | Wants | Starts at | Then |
|---|---|---|---|
| **Family IT person** (evaluating) | Will it work on my machines, and what does it do to my network? | jaggery.dev | README → docs/getting-started.md → docs/security.md |
| **Family IT person** (running it) | How do I do X; why is Y broken? | `jag doctor`, `jag <cmd> --help` | docs/how-to/, docs/reference/ |
| **New contributor** | Can I help, and where do I start? | README "Contributing" | CONTRIBUTING.md → docs/development/ → issues labelled `good first issue` |
| **Regular contributor** | Why is it built this way; what's next? | ROADMAP.md | tech-specs/README.md → decisions.md and plan.md → issues by milestone |
| **Security reviewer** | What listens, what leaves, how trust works, how to report | SECURITY.md | docs/security.md → tech-specs (pairing, updates) |

## The layout

```
README.md            what it is, what works today, install, links. Short and true.
ROADMAP.md           the bet, v1 scope, named milestones. Changes rarely.
CONTRIBUTING.md      how to propose, build, test and send a change.
SECURITY.md          how to report a vulnerability, and what's in scope.
CODE_OF_CONDUCT.md   how we treat each other.
docs/                for people who run Jaggery and people who build it
  README.md            the docs index
  getting-started.md   from nothing to two machines on one page
  how-it-works.md      the plain-language tour: discovery, trust, names, routing, updates
  security.md          ports, what leaves the house, pairing, what's not protected yet
  how-to/              one task per page, each ending in a command that shows it worked
  reference/           every command, file, port and environment variable
  development/         architecture tour, testing, releasing
tech-specs/          designs and decisions: why it is the way it is
  README.md            the index: decisions, the plan, designs
.github/             issue forms, PR template, CI
```

Rules that keep it from rotting:

1. **One home per fact.** A command's flags live in its `--help` text and in docs/reference/cli.md, nowhere else. How-tos link to the reference rather than copying it.
2. **Docs say what works today.** Planned things are named once, in ROADMAP.md or tech-specs/plan.md, with "coming" and a link. BRAND.md's first principle applies to the repo: only claim what works.
3. **tech-specs are designs, not manuals.** A spec explains a decision and its alternatives. When a feature ships, the user-facing part moves to docs/; the spec keeps the why.
4. **Open work lives in issues** (below); specs link to them, and decisions.md keeps the reasons once the work is done.
5. **No personal material**, enforced by `scripts/check-personal.sh` in CI.
6. **The PR that changes behaviour changes the docs.** The PR template asks.

## The backlog: GitHub issues

From now on the backlog of record is GitHub issues; tech-specs hold the designs those issues point to.

- **One issue per slice** that is scoped well enough to start, titled with its slice id when it has one (`R8: Tools only lists apps the house can open`). The spec or plan links each id to its issue.
- **Two milestones**: `Alpha` (now) and `1.0` (the public launch). A ROADMAP milestone (Map, Names, AI router…) is claimed by a release, not tracked as a GitHub milestone.
- **Labels** (kept few, so they get used):

| Label | Meaning |
|---|---|
| `good first issue` | Small, self-contained, a pointer to the files to read. Can be done without special hardware. |
| `help wanted` | Bigger, but the design is settled. |
| `needs triage` | New, not looked at yet. Removed once a maintainer has answered. |
| `needs design` | The what or how isn't agreed; discuss before code. |
| `needs hardware` | Needs a machine the project doesn't have (NVIDIA, Windows, Android, a NAS, mesh Wi-Fi). Great for someone who owns one. |
| `area: discovery`, `area: names`, `area: ai router`, `area: dashboard`, `area: updates`, `area: shares`, `area: hosted`, `area: install`, `area: apps`, `area: docs`, `area: tests` | Where in the code. |
| `bug`, `enhancement`, `documentation` | GitHub's defaults. |

- **Every seeded issue says where to start**: the files to read, the test to copy, and how to check it on one machine (the test house, `scripts/house-test.sh`, runs N agents on one Linux box).
- **Feature requests from real use** become issues as they come, with the evidence (logs, timings) in the issue.
- No project board for now. Milestones and labels are enough for one maintainer; add a board when there are regular contributors.

## jaggery.dev

The site sells the idea and makes promises; the repo holds the manual. One source per fact: the site links to the docs on GitHub rather than copying them, until there's a reason to build the docs into the site.

| Page | What it says | When |
|---|---|---|
| `/` | The pitch, the promises, what leaves the house, how to install | Now |
| `/security` | What Jaggery listens on, what leaves the house, how pairing and updates are protected, how to report a problem | Now |
| `/contribute` | Why help, what kind of help is wanted (apps, hardware reports, docs), links to CONTRIBUTING and good first issues | Now |
| `/privacy` | What the site and `hello@` collect: no cookies, no analytics; mail passes through the mail provider and is kept briefly before reaching a person | Now |
| `/how-it-works` | The long, honest post (discovery, trust, names, routing, updates, what it can't do) | At 1.0, from docs/how-it-works.md |
| `/install` | The one-line install, with the script readable at its URL | Now |
| `/notes` | Release notes for each `prod` release | After the first public `prod` |

Navigation stays four items: How it works · Security · Contribute · GitHub.

## A new developer's first hour

1. README: what it is, and that it's early.
2. CONTRIBUTING: build with `go build ./cmd/jag`, run `scripts/ci-local.sh`, open a PR into `beta`.
3. docs/development/README.md: the package map, so `internal/apps` (where apps are recognised) is easy to find.
4. A `good first issue`: most are one app fingerprint or one `jag doctor` message, with a test to copy.
5. `scripts/house-test.sh`: three machines on one laptop, to see their change across a house without owning one.

## Decisions

1. **Contributions: DCO sign-off, no CLA**, with Apache-2.0 and a trademark policy; see [licensing.md](licensing.md).
2. **Code of conduct: Contributor Covenant 2.1**, reports to `hello@jaggery.dev`.
3. **Security reports:** GitHub's private vulnerability reporting, or `hello@jaggery.dev`.
4. **Questions and feedback in GitHub Discussions;** no Discord or chat server.
5. **`tech-specs/` stays flat**, with the index grouping it.
