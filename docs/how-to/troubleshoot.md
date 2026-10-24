# Fix what `jag doctor` reports

Run `jag doctor` on the machine with the problem. Each line is ✓ (fine), ! (worth a look) or ✗ (broken), and every ! or ✗ says what to type.

| Check | What it means when it fails | Usually |
|---|---|---|
| `identity`, `agent`, `network` | No identity yet, the agent isn't running, or no usable network interface | Install the service, or `sudo systemctl start jaggery`. VPN and container interfaces don't count as the home network. |
| `multicast` | No other Jaggery machine answered over mDNS | Guest Wi-Fi or "AP isolation" blocks multicast; a firewall blocks UDP 5353; the other machines are asleep. Join by address as a workaround: [another network](join-from-another-network.md). |
| `jaggery.local`, `ai.local` | The names don't resolve, or resolve slowly | On Linux, install and start Avahi with `nss-mdns`. A 5-second delay means IPv4 answers but IPv6 doesn't; the inbound check says why. |
| `port 80` | Port 80 isn't served | Another program has it, or Linux needs `setcap` (the hint shows the command). Use `http://jaggery.local:7373` meanwhile. |
| `inbound` | Another machine can't reach this one's ports | A firewall. The fix lists the exact `ufw` or `firewall-cmd` rule, including IPv6 link-local. |
| `ai runtimes` | A runtime (e.g. Ollama) listens on the whole network, not just this machine | Anyone could use it directly, around approval and the log. Bind it to `127.0.0.1` (Ollama: `OLLAMA_HOST=127.0.0.1`). |
| `clock` | This machine's clock is far from a peer's | Turn on network time; TLS and update timing depend on it. |
| `updates` | The last update check failed, or an update is waiting | Read the message; `jag update` installs a waiting one. |

## Other common problems

- **The page doesn't open on a phone.** Try `http://<machine>.local:7373`. If that works, port 80 isn't served (see `port 80`). If nothing works, the phone may be on a guest network.
- **A Mac can't see anything.** It needs Local Network permission: System Settings → Privacy & Security → Local Network → allow `jag`.
- **A new model isn't served (403).** It's waiting for approval: `jag models approve <name>`.
- **A machine shows offline.** `jag status` on it, and check it's awake; laptops leave when the lid closes.

## Logs

- Linux: `journalctl -u jaggery -f`
- macOS: `tail -f ~/Library/Logs/jaggery.log`

Still stuck? [Open an issue](https://github.com/jaggery-dev/jaggery/issues/new/choose) with the output of `jag doctor`, `jag version` and the log lines around the problem. Remove anything you'd rather not share.
