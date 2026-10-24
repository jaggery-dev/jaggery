# Jaggery

**Every computer in your home, working as one.**

Jaggery is a small program you run on each computer in your house. They find each other, show up on one page, and share their AI models through one address. No account, no router changes, nothing to configure.

> **Public alpha.** Jaggery is early (v0.x). Linux and Raspberry Pi are supported; the Mac is in testing; Windows comes later. Expect rough edges: read the [known issues](docs/known-issues.md), and please [tell us](#feedback) what you find.

```console
desktop $ jag invite
on the machine you want to add, run:
    jag join amber-attic-barley-brook-basil-acorn

laptop  $ jag join amber-attic-barley-brook-basil-acorn
joined the house through desktop
both machines now trust each other.
```

Then open **http://jaggery.local** on any phone or laptop at home.

## What you get today

- **One page for the house** at `jaggery.local`: every machine, its memory, disks and GPU, and the apps it runs (Jellyfin, Immich, Home Assistant and [17 more](internal/apps/apps.go)). Works on a phone.
- **One address for AI** at `ai.local`: an OpenAI-compatible endpoint in front of Ollama, LM Studio and llama.cpp on every machine. Each request goes to the machine that has the model loaded, or ran it fastest. A new model stays off `ai.local` until you approve it.
- **Shared folders for models and agents**: lend a folder with `jag share add`, and models and agents read (and, if you allow it, write) it through the MCP server at `ai.local/mcp`. Every access is logged, and old versions are kept for 30 days.
- **Hosted models, only if you add them**: allow-listed providers with a spending cap, and every request kept word for word (`jag outbound`).
- **Nothing out of sight**: `jag activity` shows who used which model on which machine; `jag files log` shows what agents read and changed.
- **It looks after itself**: updates roll out one machine at a time and undo themselves if a build won't start. `jag doctor` checks the machine and its network and says what to type to fix what fails.

## Get started

On each machine:

```sh
curl -fsSL https://jaggery.dev/install | sh
```

The script picks the build for the machine, checks it against the release's signed checksums, and installs it as a service (it asks for `sudo`). [Read it first](scripts/get.sh) if you like; that's what it's for. Then run `jag invite` on the first machine and `jag join <code>` on the next. The full walk-through, including the Mac's one-time network permission, is in **[docs/getting-started.md](docs/getting-started.md)**.

Point any OpenAI-compatible app at `http://ai.local/v1`:

```sh
curl http://ai.local/v1/chat/completions \
  -d '{"model": "llama3.2:3b", "messages": [{"role": "user", "content": "hi"}]}'
```

## What Jaggery will not do

- **Ask you to sign up.** There is no account with us.
- **Report on you.** No telemetry.
- **Touch your router.** Nothing is opened to the internet.
- **Trust a stranger.** A machine joins only with a one-time code or your approval; machines talk to each other over mutual TLS.
- **Take over.** It finds what you already run. It doesn't install apps.

**What leaves your house:** each machine asks GitHub for a new release every 30 minutes (`jag update --policy off` stops it), and hosted models, if you add one. Nothing else. Jaggery trusts your home network and everything on it, like a printer: put guests and smart devices on a separate network. Web pages and the internet are refused. Details: [docs/security.md](docs/security.md).

## Feedback

Jaggery is in alpha so that we can learn from houses that aren't ours.

- **Something broke or confused you:** open an [issue](https://github.com/jaggery-dev/jaggery/issues/new/choose), and paste the output of `jag doctor --json`.
- **Questions, ideas, or what's in your house:** start a [discussion](https://github.com/jaggery-dev/jaggery/discussions).
- **A security problem:** don't open an issue; see [SECURITY.md](SECURITY.md).

During the alpha, fixes ship often. To get them without thinking about it: `jag update --policy auto --channel prod` (each machine waits a day after a fix is published, then updates one at a time).

## Documentation

| | |
|---|---|
| [Getting started](docs/getting-started.md) | From nothing to two machines on one page |
| [How-to guides](docs/README.md#how-to) | Approve models, share folders, add a hosted provider, join from another network, updates, troubleshooting |
| [Reference](docs/reference/cli.md) | Every command, file, port and environment variable |
| [How it works](docs/how-it-works.md) | Discovery, trust, names, routing and updates in plain words |
| [Roadmap](ROADMAP.md) | Where it's going, and what's out of scope |

## Contributing

Jaggery is young and help is welcome, especially from people with hardware we don't have (NVIDIA, Windows, Android, a NAS) and people who run self-hosted apps Jaggery doesn't recognise yet. Start with [CONTRIBUTING.md](CONTRIBUTING.md) and the issues labelled [good first issue](https://github.com/jaggery-dev/jaggery/labels/good%20first%20issue). The design history is in [tech-specs/](tech-specs/README.md).

Found a security problem? Please don't open an issue: see [SECURITY.md](SECURITY.md).

## Why "Jaggery"?

Jaggery is unrefined sugar, made at home in kitchens across South Asia: simple, honest, a little sweet. That's how we want the computers in your home to feel. The command is just `jag`.

## Licence

[Apache-2.0](LICENSE). The name and logo have their own [trademark policy](TRADEMARKS.md): use them to talk about Jaggery and to package it unmodified; a changed version needs another name.
