# tech-specs: why Jaggery is built the way it is

Designs and the decisions behind them. For how to *use* Jaggery, see [docs/](../docs/README.md); for open work, see the [issues](https://github.com/jaggery-dev/jaggery/issues). How these fit together is in [information-architecture.md](information-architecture.md).

Most files open with a **Status** line: read it first.

## Start here

| File | What it is |
|---|---|
| [../ROADMAP.md](../ROADMAP.md) | The bet, v1 scope and the named milestones |
| [decisions.md](decisions.md) | **Why it works the way it does:** the choices and measurements behind discovery, names, routing, trust, updates and content. Read the section for your area before proposing something different. |
| [plan.md](plan.md) | Where it is going: what is built, the alpha, what comes next, and what 1.0 means |
| [releases.md](releases.md) | Versions, the `beta` and `prod` channels, merging and promotion rules |

## Designs

| File | Area |
|---|---|
| [app-names.md](app-names.md) | Short names for the house's apps, like `chat.local`, answered and forwarded by the name holder (track W) |
| [shared-folders.md](shared-folders.md) | Folders lent to models and agents through `ai.local/mcp` (track S) |
| [security-local.md](security-local.md) | Review of hostile web pages, devices and programs against a house: what stops them, what is left. Re-run when trust changes. |
| [agent-friendly.md](agent-friendly.md) | Making Jaggery easy to operate and to contribute to through AI agents: AGENTS.md, a skill, `--json`, a contribution policy |
| [licensing.md](licensing.md) | Licence, DCO vs CLA, and protecting the name. Decided: Apache-2.0 + DCO + trademark policy. |
| [information-architecture.md](information-architecture.md) | Where docs, specs, the backlog and the site live |

## Writing a new spec

Open one when a change needs a decision someone else should see: a new track, a protocol or state format, anything that touches trust or what leaves the house. Start with a Status line, the goal, the done-when you could check on real machines, and the options with a recommendation. Keep it in plain English. Once agreed, put its slices in issues and link them from the spec.
