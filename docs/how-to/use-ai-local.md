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

## If a model is missing or refused

- Not in `/v1/models`: the runtime isn't running on that machine, or listens on a port Jaggery doesn't probe yet (Ollama 11434, LM Studio 1234, llama.cpp 8080). `jag map` shows what each machine found.
- `403`: the model isn't approved. See [Approve models](approve-models.md).
