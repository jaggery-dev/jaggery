# Getting started

From nothing to two machines on one page. You need two computers on the same home network, each running Linux (x86-64 or ARM, including a Raspberry Pi) or macOS on Apple silicon (in testing), and `sudo` on each.

Before you start, skim the [known issues](known-issues.md): Jaggery is in alpha.

## 1. Install it

On each machine:

```sh
curl -fsSL https://jaggery.dev/install | sh
```

The script is at that address for you to read before you run it ([source](../scripts/get.sh)). It picks the build for the machine (Linux on x86-64, 64-bit ARM or a 32-bit Raspberry Pi OS; macOS on Apple silicon), downloads the newest release from GitHub, and checks it against the release's `SHA256SUMS` and its signature. On a Mac without Homebrew's `openssl@3` the signature check is skipped, and the script says so; the checksum still is, and from then on `jag` checks every update's signature itself.

<details>
<summary>Without the script</summary>

Download the build for the machine (`jag-linux-amd64`, `jag-linux-arm64`, `jag-linux-armv7`, `jag-darwin-arm64`), `SHA256SUMS` and `install.sh` from [Releases](https://github.com/jaggery-dev/jaggery/releases), then:

```sh
sha256sum --check --ignore-missing SHA256SUMS
sudo sh install.sh --binary ./jag-linux-amd64
```

</details>

## 2. What the installer did

The installer:

- runs the agent as a service under **your** account (systemd on Linux, a LaunchDaemon on macOS), started at boot;
- puts the binary in `/usr/local/lib/jaggery` (owned by you, so updates need no `sudo`) and links `/usr/local/bin/jag`;
- on Linux, lets the agent serve port 80 without root, and if `ufw` or `firewalld` is active, opens Jaggery's ports to your local network only (`--no-firewall` skips this).

**On a Mac**, macOS asks once whether `jag` may find devices on your local network. Someone at the Mac must click **Allow**; until then it can't see the other machines.

Check it's running:

```sh
jag status
```

## 3. Add the second machine

Install it the same way. Then, on the first machine:

```console
$ jag invite
on the machine you want to add, run:

    jag join amber-attic-barley-brook-basil-acorn

the code works once, until 18:40.
```

And on the second:

```console
$ jag join amber-attic-barley-brook-basil-acorn
looking for the machine that issued the code...
joined the house through desktop (…)
both machines now trust each other.
```

A third machine joins the same way, through any machine already in the house; it then trusts every machine the house trusts.

## 4. Open the page

On any phone or laptop at home, open **http://jaggery.local**. You should see both machines, their hardware, and any apps Jaggery recognises.

If the page doesn't open, run `jag doctor` on either machine. It checks multicast, name lookups, the web port, the firewall and the clock, and says what to type to fix what fails. See [Fix what jag doctor reports](how-to/troubleshoot.md).

## 5. Try `ai.local` (optional)

If a machine runs Ollama, LM Studio, llama.cpp or vLLM, its models show up house-wide:

```sh
jag models
```

Models already there when Jaggery first saw them are approved; models added later wait for you (`jag models approve <name>`). Then point any OpenAI-compatible app at `http://ai.local/v1`. See [Use ai.local from an app](how-to/use-ai-local.md).

## 6. Stay up to date (recommended in the alpha)

Releases (the `prod` channel) only tell you an update is there, unless you let them install themselves:

```sh
jag update --policy auto --channel prod
```

Each machine then waits a day after a fix is published and updates one at a time; a build that won't start is rolled back. See [Control updates](how-to/updates.md).

## Tell us how it went

Whatever happened, we'd like to hear it: [open an issue](https://github.com/jaggery-dev/jaggery/issues/new/choose) for anything that broke (with `jag doctor --json`), or [start a discussion](https://github.com/jaggery-dev/jaggery/discussions) for anything else.

## Next

- [Lend a folder to models and agents](how-to/share-folders.md)
- [Control updates](how-to/updates.md)
- [What leaves your house](security.md)
