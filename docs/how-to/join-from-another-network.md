# Join a machine on another network

mDNS only works inside one network. A machine the house reaches only by routing (over WireGuard or Tailscale, a cloud machine, another subnet or VLAN) can't find the house by itself. Give it an address to send the code to.

## Steps

1. Make sure the two machines can reach each other on TCP ports **7373 and 7374** over the link (check a firewall on each side, and any cloud security group).
2. On a machine in the house: `jag invite`.
3. On the new machine, using the house machine's address on the link:

```sh
jag join amber-attic-barley-brook-basil-acorn --at 198.51.100.10
```

`--at` takes an address or a host name, with an optional `:port` (default 7373).

The house machine answers only its home network. Private addresses (WireGuard's `10.x`, most VPNs) and Tailscale's `100.x` are fine as they are. If the new machine reaches it from a public address, allow that address on the house machine first: add `JAGGERY_ALLOW_FROM=203.0.113.7` (an address or a prefix) to the agent's environment and restart it. Otherwise the join is refused with "this agent answers only its home network".

The join is mutual, and each side keeps the address it reached the other at: mDNS never overwrites it.

## Check it

```sh
jag peers       # the new machine is approved
jag ping <id>   # reached over mutual TLS
jag map         # its hardware and models
```

## What to expect

- Machines in the house that have no route to the new one show it as unreachable. That's harmless: requests to it go through machines that can reach it.
- The new machine updates through the house's relay if it can't reach GitHub.
- A model on a network disk can take minutes to load the first time; apps with short timeouts may fail that first request.
