# Put your own apps and links on the page

Jaggery finds well-known apps by itself (Jellyfin, Immich, Home Assistant, Open WebUI and others) on every machine that's on, and lists them on the page under Apps. Add the rest yourself: an app Jaggery doesn't know, a link to something that isn't a Jaggery machine, or a better description for an app it found.

Run these on the machine that runs the app. Every phone and laptop in the house sees the change on the page within a minute.

## An app Jaggery doesn't know

```sh
jag apps add Grafana --port 3001 --description "Graphs of the house"
jag apps add "Calibre" --port 8083 --path /books --description "The family's ebooks"
```

It's shown only while something answers on that port on this machine's network address, so the page never links to an app that's off. If the port answers on `127.0.0.1` only, the app isn't reachable from other devices, and it won't show until it is.

## A link

For things that aren't Jaggery machines: the router, a printer, a NAS's settings page.

```sh
jag apps add Router --url http://192.168.1.1 --description "Wi-Fi settings"
```

Links must start with `http://` or `https://`. They're shown as long as the machine that added them is on.

## A better description

```sh
jag apps add Jellyfin --description "Family films"
```

The name must match the app Jaggery found on this machine. An entry with `--port` on a found app's port renames it too:

```sh
jag apps add "House chat" --port 3000 --description "Ask the house's AI"
```

## A name for an app

Give an app on this machine a short name, and every phone and laptop in the house opens it at `http://<name>.local`, without knowing which machine runs it or on which port:

```sh
jag apps name "Open WebUI" chat     # then open http://chat.local
jag apps name "Open WebUI" --remove
```

A name is one word of lower-case letters, digits and hyphens, like `chat` or `photos`. Each app has one name. Jaggery refuses a name that another app in the house already has, a machine's name, and its own names (`jaggery`, `ai`). Links added with `--url` can't have a name, and nor can an app that answers only on `127.0.0.1`, since other devices can't open it either. If two machines give the same name at the same moment, `jag apps` shows the clash on both; the machine that sorts first by id gets it until you remove one.

The name works within a minute, and the app's tile on the page opens it by name. The name holder answers for it, as it does for `jaggery.local`, and passes each request on to the app: pages, uploads, streamed answers and WebSockets. The app's own sign-in, cookies and headers are unchanged. If the name holder goes away, the next one takes the name over within seconds, and the app's own address (`http://desktop.local:3000`) keeps working throughout.

Some apps need a setting of their own:

- **Apps that build links from their own address**, such as n8n's webhook address or Open WebUI's `WEBUI_URL`, keep pointing at the old address until you change the setting to `http://chat.local`. Jaggery tells the app which name it was opened by (`Host` and `X-Forwarded-Host`), but doesn't rewrite its pages.
- **Apps that refuse requests passed on by another machine.** Home Assistant answers `400` until the name holder's address is in its `trusted_proxies` (with `use_x_forwarded_for: true`). Apps that limit or trust by address see the name holder's address, with the real one in `X-Forwarded-For`.

The names are plain `http://`, like `jaggery.local`.

## See and remove them

```sh
jag apps                 # what was added here, then everything on the page
jag apps remove Grafana
```

Adding the same name again replaces the entry. Entries and names live in `apps.json` in the state directory.

## Missing an app everyone has?

If Jaggery should know an app by itself, open an issue with the label "area: apps", or add its fingerprint to `internal/apps/apps.go` (see [CONTRIBUTING](../../CONTRIBUTING.md)).
