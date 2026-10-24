# Approve the models the house may use

A model that turns up after Jaggery first looked stays off `ai.local` until someone approves it. This stops a model pulled by anyone on any machine from silently becoming available to every app.

```console
$ jag models
```

lists every model in the house, where it is, and whether it's approved. To approve one:

```sh
jag models approve qwen2.5:0.5b
```

Approval is house-wide: approve on any machine and the others follow within seconds. It pins the model's digest, so if the files under that name change (a new pull, `ollama cp` over it), the model waits for approval again.

The models already on a machine when the agent first ran are approved as a starting point. The page shows an alert for each model waiting.

To turn approval off and serve every model:

```sh
jag models enforce off
```

**Check it:** a request for an unapproved model returns `403` and names the command to approve it.

Approving from the page isn't possible yet: the page has no sign-in, so approval stays at a terminal until per-person keys exist.
