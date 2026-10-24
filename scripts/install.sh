#!/bin/sh
# Install the Jaggery agent as a service: systemd on Linux, launchd on macOS.
#
#   sudo sh install.sh --binary ./jag     install this binary and start the service
#   sudo sh install.sh --uninstall        stop and remove the service and binary
#
# The service runs as the user who invoked sudo, with that user's state
# directory, so `jag invite`, `jag status` and friends work without sudo and an
# existing identity carries over. On Linux, port 80 comes from an ambient
# capability rather than root, and if ufw or firewalld is active the agent's
# ports are opened to the local subnet only (skip with --no-firewall). On
# macOS the service is a LaunchDaemon running as that user; macOS asks once to
# let it find devices on the local network, and someone must click Allow.
#
# The binary lives in /usr/local/lib/jaggery, owned by that user, so `jag
# update` can replace it and the agent restarts into the new version with no
# sudo; /usr/local/bin/jag links to it.
set -eu

BIN_DIR=/usr/local/bin
LIB_DIR=/usr/local/lib/jaggery
UNIT=/etc/systemd/system/jaggery.service
PLIST=/Library/LaunchDaemons/com.jaggery.agent.plist
LABEL=com.jaggery.agent
PORTS="7373 7374 80"

binary=""
firewall=1
uninstall=0

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
	case "$1" in
	--binary) [ $# -ge 2 ] || die "--binary needs a path"; binary=$2; shift 2 ;;
	--no-firewall) firewall=0; shift ;;
	--uninstall) uninstall=1; shift ;;
	-h|--help) sed -n '2,17p' "$0"; exit 0 ;;
	*) die "unknown option: $1" ;;
	esac
done

os=$(uname -s)
case "$os" in
Linux | Darwin) ;;
*) die "this installer is for Linux and macOS; Windows isn't supported yet" ;;
esac
[ "$(id -u)" -eq 0 ] || die "run with sudo"

run_user=${SUDO_USER:-}
[ -n "$run_user" ] && [ "$run_user" != root ] || die "run with sudo from your normal account, not as root: the service runs as you"
if [ "$os" = Darwin ]; then
	run_home=$(dscl . -read "/Users/$run_user" NFSHomeDirectory 2>/dev/null | awk '{print $2}')
else
	run_home=$(getent passwd "$run_user" | cut -d: -f6)
fi
[ -d "$run_home" ] || die "no home directory for $run_user"

# install_binary puts the binary where the service runs it from, owned by the
# user so `jag update` can replace it.
install_binary() {
	[ -n "$binary" ] || die "pass the agent binary with --binary, or install with: curl -fsSL https://jaggery.dev/install | sh"
	[ -f "$binary" ] || die "no such file: $binary"
	"$binary" help >/dev/null 2>&1 || die "$binary does not run on this machine (wrong architecture?)"
	install -d -m 0755 "$BIN_DIR"
	install -d -o "$run_user" -m 0755 "$LIB_DIR"
	install -o "$run_user" -m 0755 "$binary" "$LIB_DIR/jag.new"
	mv "$LIB_DIR/jag.new" "$LIB_DIR/jag"
	ln -sfn "$LIB_DIR/jag" "$BIN_DIR/jag"
}

# ---- macOS: a LaunchDaemon that runs the bare binary as the user (phase 4
# M1). A LaunchAgent can't reach the local network without an app bundle, and
# a daemon also runs before login.

macos_uninstall() {
	launchctl bootout "system/$LABEL" 2>/dev/null || true
	rm -f "$PLIST" "$BIN_DIR/jag"
	rm -rf "$LIB_DIR"
	say "removed the jaggery service and binary"
	say "this machine's identity and peers are kept in $run_home/Library/Application Support/jaggery; delete it to forget them"
}

macos_plist() {
	cat <<PLIST_END
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>$LABEL</string>
	<key>ProgramArguments</key>
	<array>
		<string>$LIB_DIR/jag</string>
		<string>up</string>
	</array>
	<key>UserName</key>
	<string>$run_user</string>
	<key>GroupName</key>
	<string>$1</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>HOME</key>
		<string>$run_home</string>
		<key>PATH</key>
		<string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>$2</string>
	<key>StandardErrorPath</key>
	<string>$2</string>
</dict>
</plist>
PLIST_END
}

macos_install() {
	# An agent started by hand holds the ports the service needs.
	if ! launchctl print "system/$LABEL" >/dev/null 2>&1 && pgrep -f '/jag up' >/dev/null 2>&1; then
		die "a jag agent is already running (pid $(pgrep -f '/jag up' | head -1)); stop it first, then run this again"
	fi
	install_binary
	group=$(id -gn "$run_user")
	logs="$run_home/Library/Logs"
	install -d -o "$run_user" -g "$group" -m 0755 "$logs"
	touch "$logs/jaggery.log"
	chown "$run_user:$group" "$logs/jaggery.log"

	macos_plist "$group" "$logs/jaggery.log" >"$PLIST.new"
	plutil -lint "$PLIST.new" >/dev/null || die "the service definition didn't validate"
	chown root:wheel "$PLIST.new"
	chmod 0644 "$PLIST.new"
	launchctl bootout "system/$LABEL" 2>/dev/null || true
	mv "$PLIST.new" "$PLIST"
	launchctl bootstrap system "$PLIST"

	fw=/usr/libexec/ApplicationFirewall/socketfilterfw
	if [ "$firewall" -eq 1 ] && [ -x "$fw" ] && "$fw" --getglobalstate 2>/dev/null | grep -q enabled; then
		"$fw" --add "$LIB_DIR/jag" >/dev/null 2>&1 || true
		"$fw" --unblockapp "$LIB_DIR/jag" >/dev/null 2>&1 || true
		say "firewall: the macOS firewall now lets jag accept connections"
	fi

	sleep 3
	if launchctl print "system/$LABEL" 2>/dev/null | grep -q 'state = running'; then
		say "jaggery is running as a service (user $run_user) and will start at boot"
		say ""
		say "  macOS will ask to let \"jag\" find devices on your local network: click Allow."
		say "  Without it, this Mac can't see the rest of the house. (It asks only once.)"
		say ""
		say "  add a machine:   jag invite        (here), then jag join <code> (there)"
		say "  see the house:   http://jaggery.local"
		say "  logs:            tail -f ~/Library/Logs/jaggery.log"
		say "  update:          jag update    (no sudo needed from now on)"
	else
		tail -20 "$logs/jaggery.log" >&2 || true
		die "the service did not start; the log is above"
	fi
}

if [ "$os" = Darwin ]; then
	if [ "$uninstall" -eq 1 ]; then
		macos_uninstall
	else
		macos_install
	fi
	exit 0
fi

# ---- Linux: a systemd service.

command -v systemctl >/dev/null 2>&1 || die "systemd is required"

# The subnet of the interface holding the default route, e.g. 192.0.2.0/24.
lan_subnet() {
	dev=$(ip route show default 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "dev") { print $(i + 1); exit }}')
	[ -n "$dev" ] || return 1
	ip -o -4 route show dev "$dev" scope link 2>/dev/null | awk '{print $1; exit}'
}

open_firewall() {
	[ "$firewall" -eq 1 ] || return 0
	subnet=$(lan_subnet) || subnet=""
	if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q '^Status: active'; then
		[ -n "$subnet" ] || { say "firewall: ufw is active but the local subnet is unknown; open ports $PORTS yourself"; return 0; }
		for p in $PORTS; do
			ufw allow from "$subnet" to any port "$p" proto tcp comment 'jaggery' >/dev/null
			# Link-local IPv6 never leaves the network; the shared names need it
			# to resolve fast on Linux (phase 3 R5).
			ufw allow from fe80::/10 to any port "$p" proto tcp comment 'jaggery' >/dev/null 2>&1 || true
		done
		say "firewall: ufw now allows ports $PORTS from $subnet and link-local IPv6"
	elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
		[ -n "$subnet" ] || { say "firewall: firewalld is active but the local subnet is unknown; open ports $PORTS yourself"; return 0; }
		for p in $PORTS; do
			firewall-cmd --permanent --add-rich-rule="rule family=ipv4 source address=$subnet port port=$p protocol=tcp accept" >/dev/null
			firewall-cmd --permanent --add-rich-rule="rule family=ipv6 source address=fe80::/10 port port=$p protocol=tcp accept" >/dev/null 2>&1 || true
		done
		firewall-cmd --reload >/dev/null
		say "firewall: firewalld now allows ports $PORTS from $subnet and link-local IPv6"
	else
		say "firewall: none active, nothing to open"
	fi
}

close_firewall() {
	[ "$firewall" -eq 1 ] || return 0
	if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q '^Status: active'; then
		# Delete the rules we added, newest first so the numbering holds.
		ufw status numbered | grep 'jaggery$' | sed 's/^\[ *\([0-9]*\)\].*/\1/' | sort -rn | while read -r n; do
			ufw --force delete "$n" >/dev/null
		done
		say "firewall: removed jaggery's ufw rules"
	fi
}

if [ "$uninstall" -eq 1 ]; then
	systemctl disable --now jaggery.service 2>/dev/null || true
	rm -f "$UNIT" "$BIN_DIR/jag"
	rm -rf "$LIB_DIR"
	systemctl daemon-reload
	close_firewall
	say "removed the jaggery service and binary"
	say "this machine's identity and peers are kept in $run_home/.config/jaggery; delete it to forget them"
	exit 0
fi

install_binary

cat >"$UNIT" <<EOF
[Unit]
Description=Jaggery agent: every machine in your house, one system
Wants=network-online.target
After=network-online.target

[Service]
User=$run_user
ExecStart=$LIB_DIR/jag up
Restart=on-failure
RestartSec=5
# Port 80 for http://jaggery.local and http://ai.local, without root.
AmbientCapabilities=CAP_NET_BIND_SERVICE
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
open_firewall
systemctl enable jaggery.service >/dev/null 2>&1
systemctl restart jaggery.service

sleep 2
if systemctl is-active --quiet jaggery.service; then
	say "jaggery is running as a service (user $run_user) and will start at boot"
	say ""
	say "  add a machine:   jag invite        (here), then jag join <code> (there)"
	say "  see the house:   http://jaggery.local"
	say "  logs:            journalctl -u jaggery -f"
	say "  update:          jag update    (no sudo needed from now on)"
else
	journalctl -u jaggery.service -n 20 --no-pager >&2 || true
	die "the service did not start; the log is above"
fi
