# Add a hosted model provider

Hosted models are off until you add a provider. When you do, one machine (the *exit*) holds the key, enforces a spending cap, and keeps every request and reply word for word for 30 days.

Any provider with an OpenAI-compatible chat API works. Choosing providers is up to you: read each one's data-retention and training policy first.

## 1. Put the key in a file

In the agent user's state directory (`~/.config/jaggery` on Linux), readable only by that user:

```sh
cd ~/.config/jaggery
read -rs KEY && printf '%s' "$KEY" > example.key && unset KEY
chmod 600 example.key
```

## 2. Describe the provider in `providers.json`

In the same directory:

```json
{
  "providers": [
    {
      "name": "example",
      "base_url": "https://api.example.com/v1",
      "key_file": "example.key",
      "models": ["small-model", "big-model"],
      "price": {"input_per_mtok": 0.50, "output_per_mtok": 1.50},
      "cap_usd": 5.00,
      "max_output_tokens": 2048,
      "free": {"tokens": 1000000, "per": "month"}
    }
  ],
  "groups": {
    "quick": ["example/small-model"]
  }
}
```

| Field | Required | Meaning |
|---|---|---|
| `name` | yes | Prefixes its models: `example/small-model`. No spaces or slashes. |
| `base_url` | yes | `https://…`, or `http://127.0.0.1…` for a provider's proxy on this machine. |
| `key_file` | yes | Relative to the state directory; must be readable by its owner only. |
| `models` | yes | The only models the house may use here. |
| `price` | yes | US dollars per million tokens, used to keep to the cap. |
| `cap_usd` | yes | The most the house may ever spend here. A request that could pass it is refused. |
| `max_output_tokens` | no | Caps each reply (default 1024). |
| `free` | no | A free allowance used first: `tokens` per `once`, `day` or `month`. |
| `groups` | no | A name for an ordered list of `provider/model`, offered to apps as one model. Each request goes to the first member with free allowance left, and moves on if a provider is out of quota or down. |

The file reloads within two seconds; no restart. A broken file turns hosted models off, and the agent's log says why.

## 3. Check it

```sh
curl -s http://localhost:7373/v1/models | grep example/
curl -s http://localhost:7373/v1/chat/completions \
  -d '{"model": "quick", "messages": [{"role": "user", "content": "Say hello in five words."}]}'
jag outbound                # what was sent today, cost, free tokens left
jag outbound show <id>      # the exact bytes sent and received
```

**Today only the exit machine lists hosted models.** Point apps at that machine (`http://<exit>.local:7373/v1`). Routing hosted requests from every machine to the exit is next.

## Make sure apps can't go around it

Jaggery's log only sees what goes through Jaggery. If an app also holds its own provider keys, or can reach provider APIs directly, requests can leave without a record. Remove provider keys from apps and point them only at `ai.local`.
