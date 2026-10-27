# Known issues in the alpha

What we already know doesn't work, or hasn't been tried. If you hit one of these, a comment on its issue with what you saw helps more than a new issue. Anything not listed here: please [open an issue](https://github.com/jaggery-dev/jaggery/issues/new/choose).

## Doesn't work yet

| What | Workaround | Issue |
|---|---|---|
| **Chrome on macOS can't open `http://ai.local`** ("This site can't be reached"), although `curl` works. The likely cause is Chrome's Local Network permission. | Check Chrome is allowed under System Settings → Privacy & Security → Local Network, or use Safari, or the machine's own name (`http://<machine>.local:7373`). | [#20](https://github.com/jaggery-dev/jaggery/issues/20) |
| **AMD GPUs aren't detected on Linux**, so a Radeon doesn't show on the map and routing can't use its memory. | Models on it are still served; the map just shows no GPU. | [#3](https://github.com/jaggery-dev/jaggery/issues/3) |
| **NVIDIA GPU memory can be missing** from `jag map` (seen on a cloud L4). NVIDIA hasn't been tried on a home machine yet. | Models are still served. Please tell us what your card shows. | [#4](https://github.com/jaggery-dev/jaggery/issues/4) |
| **A model that isn't loaded yet** can take minutes to answer its first request, and apps with short timeouts give up. | Send a first request with a long timeout (`curl`), or load the model in its runtime first. | [#5](https://github.com/jaggery-dev/jaggery/issues/5) |
| **Windows** isn't supported. | Run Jaggery on another machine; Windows apps can still use `ai.local`. | [#17](https://github.com/jaggery-dev/jaggery/issues/17) |

## Not tried yet

| What | What would help | Issue |
|---|---|---|
| `.local` names on **Android** (they vary by version, browser and app) | Try `http://jaggery.local` on your phone and tell us what happened. | [#18](https://github.com/jaggery-dev/jaggery/issues/18) |
| Networks that **block multicast** (guest Wi-Fi, "AP isolation", some mesh systems) | Run `jag doctor` there. Machines that can't find each other can still join with `jag join <code> --at <address>`. | [#19](https://github.com/jaggery-dev/jaggery/issues/19) |

## By design, for now

These are written up in [security](security.md#what-is-not-protected-yet): anyone on your home network can use `ai.local`, the shared folders and the page (no accounts), local names are plain HTTP, and another device can answer for an app's name (`chat.local`) without Jaggery noticing yet.
