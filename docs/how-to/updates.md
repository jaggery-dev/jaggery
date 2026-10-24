# Control updates

Each machine checks for a new release on its channel every 30 minutes.

| Policy | What happens | Default for |
|---|---|---|
| `auto` | Installs new releases itself, one machine at a time | beta builds |
| `notify` | Shows "update available" in `jag status`, `jag doctor` and on the page | prod builds |
| `off` | Never checks | |

```sh
jag update --policy notify
jag update --policy auto --channel prod
jag update --check        # is there a newer build?
jag update                # install it now
jag update --rollback     # go back to the build before
```

## How `auto` rolls out

The machine that answers for `jaggery.local` installs first. The others wait until it has run the new build for a while, then follow one at a time. A build that fails to start three times is rolled back, and the whole house marks it bad and skips it. A machine that can't reach GitHub fetches the build from a machine it approved that keeps a copy.

## Many machines behind one address

GitHub answers 60 requests an hour from one address without a token, shared by every machine behind it. A check that finds nothing new costs one request, so a house stays well under that. If GitHub refuses anyway (`jag doctor` says the rate limit is used up), the machines fetch from one that keeps the build, or give one machine a read-only GitHub token in `github-token` in its state directory (`chmod 600`).

## Channels

`beta` gets a build for every change merged; `prod` gets builds that passed a soak on real machines. See [releases.md](../../tech-specs/releases.md).

## What's checked

Every release is signed in CI (`release.sig`). Before installing, `jag` checks that signature against the release keys built into it, then checks the download against the signed `SHA256SUMS`. A release that isn't signed, or is signed by any other key, is refused, from GitHub or from another machine; `jag status` shows why.
