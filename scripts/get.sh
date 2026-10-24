#!/bin/sh
# Install Jaggery on this machine:
#
#   curl -fsSL https://jaggery.dev/install | sh
#   curl -fsSL https://jaggery.dev/install | sh -s -- --beta
#
# This script is served at https://jaggery.dev/install; read it there before
# you run it. It works out this machine's system (Linux or macOS) and
# processor (x86-64, 64-bit ARM, 32-bit ARM such as an older Raspberry Pi OS),
# downloads the matching build of the newest release from GitHub, checks it,
# and runs the release's own installer with sudo, which sets jag up as a
# service that starts at boot.
#
# The checks: the build must match the release's SHA256SUMS, and the release
# must carry a signature from one of the keys built into jag (the same check
# every machine makes before installing an update). The signature needs
# OpenSSL 3; macOS ships LibreSSL, so there it's checked only if Homebrew's
# openssl@3 is installed, and the script says so. From then on jag checks
# every update itself.
#
# Options:
#   --beta                 the newest beta instead of the newest release
#   --version <v>          a particular release, e.g. 0.6.0 or 0.6.0-beta.62
#   --download-only <dir>  download and check, but don't install
#   --uninstall            remove the service and the binary (keeps identity)
#   --no-firewall          passed to the installer: leave the firewall alone
set -eu

REPO=jaggery-dev/jaggery
RELEASES=${JAGGERY_RELEASES_URL:-https://github.com/$REPO/releases}
API=${JAGGERY_API_URL:-https://api.github.com/repos/$REPO}

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

channel=prod
want=""
download_only=""
uninstall=0
installer_args=""
while [ $# -gt 0 ]; do
	case "$1" in
	--beta) channel=beta; shift ;;
	--version) [ $# -ge 2 ] || die "--version needs a version, e.g. 0.6.0"; want=${2#v}; shift 2 ;;
	--download-only) [ $# -ge 2 ] || die "--download-only needs a folder"; download_only=$2; shift 2 ;;
	--uninstall) uninstall=1; shift ;;
	--no-firewall) installer_args="$installer_args --no-firewall"; shift ;;
	-h | --help) sed -n '2,27p' "$0" 2>/dev/null || say "see https://jaggery.dev/install"; exit 0 ;;
	*) die "unknown option: $1 (see https://jaggery.dev/install)" ;;
	esac
done

# ---- What this machine is.

os=$(uname -s)
case "$os" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) die "Jaggery runs on Linux and macOS for now; $os isn't supported yet" ;;
esac

arch=$(uname -m)
case "$arch" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
armv7* | armv8l) arch=armv7 ;;
armv6*) die "this processor (armv6: Raspberry Pi 1 and Zero) is too old for Jaggery; a Pi 2 or newer works" ;;
*) die "no Jaggery build for this processor ($arch) yet" ;;
esac
[ "$os/$arch" != darwin/armv7 ] || die "no Jaggery build for this Mac ($arch)"
asset="jag-$os-$arch"

if [ "$(id -u)" -eq 0 ] && [ -z "$download_only" ]; then
	die "run this as your normal user, not as root: the service runs as you, and the script asks for sudo when it needs it"
fi

# ---- Downloads.

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL --proto '=https,http' --retry 2 -o "$2" "$1"; }
	fetch_text() { curl -fsSL --retry 2 "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q -O "$2" "$1"; }
	fetch_text() { wget -q -O - "$1"; }
else
	die "needs curl or wget"
fi

# json_field <name> <file>: a string field from a small JSON file, on one
# line or several. Enough for release.json; no jq needed.
json_field() {
	tr -d '\n' <"$2" | sed -n "s/.*\"$1\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p"
}

# Which release: a version asked for, the newest release (GitHub's "latest"
# skips betas), or the newest beta.
if [ -n "$want" ]; then
	base="$RELEASES/download/v$want"
elif [ "$channel" = prod ]; then
	base="$RELEASES/latest/download"
else
	tag=$(fetch_text "$API/releases?per_page=20" | tr ',' '\n' |
		sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\(v[0-9.]*-beta\.[0-9]*\)".*/\1/p' | head -1)
	[ -n "$tag" ] || die "couldn't find a beta release at $API (GitHub may be limiting requests; try again in a while)"
	base="$RELEASES/download/$tag"
fi

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t jaggery)
trap 'rm -rf "$tmp"' EXIT INT TERM
dir=${download_only:-$tmp}
mkdir -p "$dir"

for f in release.json SHA256SUMS release.sig install.sh; do
	fetch "$base/$f" "$dir/$f" || die "couldn't download $f from $base"
done
if [ "$uninstall" -eq 1 ]; then
	files="install.sh"
else
	files="$asset install.sh"
	say "downloading $asset"
	fetch "$base/$asset" "$dir/$asset" || die "couldn't download $asset from $base"
fi

version=$(json_field version "$dir/release.json")
[ -n "$version" ] || die "release.json from $base has no version"
withdrawn=$(json_field withdrawn "$dir/release.json")
[ -z "$withdrawn" ] || die "Jaggery $version was withdrawn ($withdrawn); try --version with an older release"

# ---- Checks.

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}
for f in $files; do
	expected=$(awk -v f="$f" '$2 == f || $2 == "*" f {print $1}' "$dir/SHA256SUMS")
	[ -n "$expected" ] || die "$f isn't listed in the release's SHA256SUMS"
	[ "$(sha256 "$dir/$f")" = "$expected" ] || die "$f doesn't match the release's SHA256SUMS: the download is damaged or was changed"
done

# The keys built into jag (internal/update/keys): main, then the spare kept
# offline for rotation.
write_keys() {
	cat >"$1/main.pem" <<'EOF'
-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAPNUCpO8CT2gyLkhHH9VRBF+Ub/OZG0C20U4650PUCK0=
-----END PUBLIC KEY-----
EOF
	cat >"$1/spare.pem" <<'EOF'
-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEABBKSHRuk7G99yY5TaIdBgGr2ZatMyrkd3u0/NEihnwI=
-----END PUBLIC KEY-----
EOF
	if [ -n "${JAGGERY_EXTRA_KEY:-}" ]; then
		cp "$JAGGERY_EXTRA_KEY" "$1/extra.pem" # tests only (scripts/get-test.sh)
	fi
}

openssl3=""
for o in openssl /opt/homebrew/opt/openssl@3/bin/openssl /usr/local/opt/openssl@3/bin/openssl; do
	if "$o" version 2>/dev/null | grep -q '^OpenSSL [3-9]'; then
		openssl3=$o
		break
	fi
done

if [ -n "$openssl3" ]; then
	# The statement must match update.Statement in internal/update/sign.go.
	printf 'jaggery release v1\nversion %s\nchannel %s\ncommit %s\nbuilt_at %s\n' \
		"$version" "$(json_field channel "$dir/release.json")" "$(json_field commit "$dir/release.json")" \
		"$(json_field built_at "$dir/release.json")" >"$tmp/statement"
	cat "$dir/SHA256SUMS" >>"$tmp/statement"
	"$openssl3" base64 -d -A -in "$dir/release.sig" -out "$tmp/sig.bin" 2>/dev/null || die "the release's signature is unreadable"
	mkdir -p "$tmp/keys"
	write_keys "$tmp/keys"
	signed=0
	for key in "$tmp"/keys/*.pem; do
		if "$openssl3" pkeyutl -verify -rawin -pubin -inkey "$key" -in "$tmp/statement" -sigfile "$tmp/sig.bin" >/dev/null 2>&1; then
			signed=1
			break
		fi
	done
	[ "$signed" -eq 1 ] || die "Jaggery $version isn't signed with Jaggery's key: don't install it, and please tell us (https://github.com/$REPO/security)"
	say "checked: Jaggery $version, signed, checksums match"
else
	say "checked: Jaggery $version, checksums match"
	say "note: the signature wasn't checked (it needs OpenSSL 3; on a Mac: brew install openssl@3). jag checks it on every update from now on."
fi

if [ -n "$download_only" ]; then
	chmod +x "$dir/$asset" 2>/dev/null || true
	say "downloaded to $dir"
	exit 0
fi

# ---- Install.

command -v sudo >/dev/null 2>&1 || die "needs sudo to install the service (or use --download-only and run install.sh with sudo yourself)"
chmod +x "$dir/$asset" 2>/dev/null || true
if [ "$uninstall" -eq 1 ]; then
	say "removing Jaggery (sudo asks for your password)"
	# shellcheck disable=SC2086
	sudo sh "$dir/install.sh" --uninstall
	exit 0
fi
say "installing Jaggery $version (sudo asks for your password)"
# shellcheck disable=SC2086
sudo sh "$dir/install.sh" --binary "$dir/$asset" $installer_args
