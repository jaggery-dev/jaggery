# Uninstall

On each machine:

```sh
sudo sh install.sh --uninstall
```

This stops and removes the service, the binary and (on Linux) the firewall rules the installer added. It keeps the machine's identity and the list of machines it trusts, so reinstalling puts it straight back in the house.

To remove those too, delete the state directory:

- Linux: `rm -rf ~/.config/jaggery`
- macOS: `rm -rf ~/Library/Application\ Support/jaggery`

Writable shares keep old versions in a `.jaggery-versions` folder inside each shared folder; delete it if you no longer want them.

The other machines will show this one as offline. To remove it from their list: `jag forget <id>` on each.
