# Use `ai.local` from an app

`ai.local` is an OpenAI-compatible endpoint in front of every AI runtime in the house. Set it once in an app and it keeps working as models move between machines.

## Settings for the app

| Setting | Value |
|---|---|
| Base URL | `http://ai.local/v1` (or `http://<any-machine>.local:7373/v1` if port 80 isn't served) |
| API key | Anything, if the app insists on one. Keys aren't checked yet. |
| Model | A name from `jag models`, e.g. `llama3.2:3b` |

Works with Open WebUI, LibreChat, Continue and anything else that speaks the OpenAI chat API. Today `ai.local` serves `GET /v1/models` and `POST /v1/chat/completions` (streamed or not). Embeddings and the Ollama-native API are planned.

**Apps in Docker:** `.local` names often don't resolve inside containers. Use the host's address and port 7373, e.g. `http://host.docker.internal:7373/v1` with `--add-host=host.docker.internal:host-gateway`.

## Check it

```sh
curl -s http://ai.local/v1/models
curl -s http://ai.local/v1/chat/completions \
  -d '{"model": "llama3.2:3b", "messages": [{"role": "user", "content": "Say hello in five words."}]}'
jag activity
```

`jag activity` shows the request: who asked (device and app), which model, which machine served it, and how fast.

## Which machine answers

A machine that already has the model loaded; otherwise the fastest measured machine the model fits on. See [How it works](../how-it-works.md#ailocal).

## Keep requests off a machine for a while

The fastest machine is often the gaming PC. To keep `ai.local` off it while a game runs, on that machine:

```sh
jag ai pause --for 2h     # or without --for: until you resume
jag ai resume
```

While it's paused, requests go to another machine that has the model. The page shows "AI paused" on its card, and `jag activity` notes the machines passed over. The rest of the house hears within about 20 seconds; the machine itself stops taking requests at once. A model that only the paused machine has is refused with a `503` that says so.

## If a model is missing or refused

- Not in `/v1/models`: the runtime isn't running on that machine, or it listens on a port other than its default (Ollama 11434, LM Studio 1234, llama.cpp 8080, vLLM 8000). `jag map` shows what each machine found, and `jag doctor` says when a default port is taken by another app. To use another port, see below.

## A runtime on another port

If a runtime listens somewhere other than its default port, for example llama.cpp on 8181 because a web app already has 8080, list it in `runtimes.json` in the [state directory](../reference/files-and-ports.md#the-state-directory) of the machine that runs it:

```json
{"runtimes": [{"kind": "llamacpp", "port": 8181}]}
```

`kind` is one of `ollama`, `lmstudio`, `llamacpp` or `vllm`. The agent reads the file again within a few seconds of a change; no restart is needed. The default ports are still checked too. An entry only counts if the runtime there answers in its own way, so a wrong port finds nothing rather than mistaking a web app for a runtime. Jaggery always talks to runtimes on `127.0.0.1`, so the runtime can stay closed to the network.
- `503` "where AI requests are paused": only a paused machine has the model. Run `jag ai resume` there, or wait.
- `403`: the model isn't approved. See [Approve models](approve-models.md).
