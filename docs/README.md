# Jaggery documentation

For people who run Jaggery at home, and people who build it. Everything here describes what works today; what's planned is in the [roadmap](../ROADMAP.md).

## Start

- [Getting started](getting-started.md): from nothing to two machines on one page, in about five minutes.
- [How it works](how-it-works.md): discovery, trust, names, routing and updates, in plain words.
- [Security and what leaves your house](security.md): every port, every outbound connection, and what isn't protected yet.
- [Known issues in the alpha](known-issues.md): what doesn't work yet, and what hasn't been tried.

## How-to

One task per page.

- [Use `ai.local` from an app](how-to/use-ai-local.md)
- [Approve the models the house may use](how-to/approve-models.md)
- [Lend a folder to models and agents](how-to/share-folders.md)
- [Put your own apps and links on the page](how-to/add-apps.md)
- [Add a hosted model provider](how-to/hosted-models.md)
- [Join a machine on another network](how-to/join-from-another-network.md) (a VPN, a cloud machine, another subnet)
- [Control updates](how-to/updates.md)
- [Fix what `jag doctor` reports](how-to/troubleshoot.md)
- [Uninstall](how-to/uninstall.md)

## Reference

- [Commands](reference/cli.md)
- [JSON output](reference/json.md)
- [Files, ports and environment variables](reference/files-and-ports.md)

## Building Jaggery

- [Contributing](../CONTRIBUTING.md): how to propose, build, test and send a change, including with AI tools.
- [AGENTS.md](../AGENTS.md): the short version for coding agents.
- [Architecture tour](development/README.md): what each package does and how a request flows.
- [Testing](development/testing.md): unit tests, the test house, and checking on real machines.
- [Releases](../tech-specs/releases.md): versions, channels, and how `beta` becomes `prod`.
- [Releasing](development/releasing.md): the release checklist, and what to do about a bad release.
- [Design history](../tech-specs/README.md): why it is the way it is.
