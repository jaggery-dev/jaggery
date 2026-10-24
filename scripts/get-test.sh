#!/bin/sh
# Checks scripts/get.sh (served at https://jaggery.dev/install) against fake
# releases on a local web server, signed with a throwaway key: it takes a good
# release, and refuses a damaged build, a re-checksummed build without a
# matching signature, a release signed with an unknown key, and a withdrawn
# release. Needs python3, OpenSSL 3 and jq. Run by scripts/ci-local.sh.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
server=""
cleanup() {
	[ -z "$server" ] || kill "$server" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
ok() { printf 'ok: %s\n' "$*"; }

# The keys in get.sh are the ones built into jag.
for k in "$here"/../internal/update/keys/*.pem; do
	grep -q "$(sed -n 2p "$k")" "$here/get.sh" || fail "get.sh doesn't carry $(basename "$k") from internal/update/keys"
done
[ "$(grep -c 'BEGIN PUBLIC KEY' "$here/get.sh")" -eq "$(ls "$here"/../internal/update/keys/*.pem | wc -l)" ] ||
	fail "get.sh carries a different number of keys than internal/update/keys"
ok "get.sh carries jag's signing keys"

openssl genpkey -algorithm ed25519 -out "$work/key.pem" 2>/dev/null
openssl pkey -in "$work/key.pem" -pubout -out "$work/pub.pem"
openssl genpkey -algorithm ed25519 -out "$work/stranger.pem" 2>/dev/null

# release <dir> <version> <channel> [key]: a signed release in dir.
release() {
	d=$1
	mkdir -p "$d"
	for a in jag-linux-amd64 jag-linux-arm64 jag-linux-armv7 jag-darwin-amd64 jag-darwin-arm64; do
		printf 'build %s %s\n' "$a" "$2" >"$d/$a"
	done
	cp "$here/install.sh" "$d/install.sh"
	(cd "$d" && sha256sum jag-* install.sh >SHA256SUMS)
	printf '{"version":"%s","channel":"%s","commit":"%s","built_at":"2026-10-18T12:00:00Z"}\n' \
		"$2" "$3" "0123456789abcdef0123456789abcdef01234567" >"$d/release.json"
	JAGGERY_SIGNING_KEY=$(cat "${4:-$work/key.pem}") sh "$here/sign-release.sh" "$d"
}

root=$work/www
release "$root/releases/latest/download" 1.2.0 prod
release "$root/releases/download/v1.1.0" 1.1.0 prod
release "$root/releases/download/v1.3.0-beta.7" 1.3.0-beta.7 beta
mkdir -p "$root/api/releases"
printf '[{"tag_name":"v1.3.0-beta.7","prerelease":true},{"tag_name":"v1.2.0","prerelease":false}]' >"$root/api/releases/index.html"

port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')
(cd "$root" && exec python3 -m http.server "$port" --bind 127.0.0.1 >/dev/null 2>&1) &
server=$!
i=0
until curl -sf -o /dev/null "http://127.0.0.1:$port/api/releases/"; do
	i=$((i + 1))
	[ "$i" -lt 50 ] || fail "test web server didn't start"
	sleep 0.1
done

# get <out dir> <args…>: run get.sh against the fake releases.
get() {
	out=$1
	shift
	JAGGERY_RELEASES_URL="http://127.0.0.1:$port/releases" JAGGERY_API_URL="http://127.0.0.1:$port/api" \
		JAGGERY_EXTRA_KEY="${extra_key-$work/pub.pem}" sh "$here/get.sh" --download-only "$out" "$@"
}
asset=$(get "$work/probe" 2>/dev/null | sed -n 's/^downloading //p')
[ -n "$asset" ] || fail "get.sh didn't say which build it downloads"

get "$work/a" >"$work/a.log" 2>&1 || { cat "$work/a.log"; fail "a good release was refused"; }
grep -q "checked: Jaggery 1.2.0, signed" "$work/a.log" || { cat "$work/a.log"; fail "the newest release wasn't checked as signed"; }
grep -q "build $asset 1.2.0" "$work/a/$asset" || fail "the wrong build was downloaded"
ok "the newest release ($asset), checked and signed"

get "$work/b" --beta >"$work/b.log" 2>&1 || { cat "$work/b.log"; fail "--beta failed"; }
grep -q "Jaggery 1.3.0-beta.7, signed" "$work/b.log" || fail "--beta didn't take the newest beta"
get "$work/c" --version 1.1.0 >"$work/c.log" 2>&1 || { cat "$work/c.log"; fail "--version failed"; }
grep -q "Jaggery 1.1.0, signed" "$work/c.log" || fail "--version didn't take that version"
ok "--beta and --version pick the right release"

# refused <name> <expected message> <args…>
refused() {
	what=$1 msg=$2
	shift 2
	if get "$work/x" "$@" >"$work/x.log" 2>&1; then
		cat "$work/x.log"
		fail "$what was installed"
	fi
	grep -q "$msg" "$work/x.log" || { cat "$work/x.log"; fail "$what: expected \"$msg\""; }
	rm -rf "$work/x"
	ok "refused: $what"
}

latest=$root/releases/latest/download
cp -R "$latest" "$work/good"
reset() { rm -rf "$latest" && cp -R "$work/good" "$latest"; }

echo "changed" >>"$latest/$asset"
refused "a damaged build" "doesn't match the release's SHA256SUMS"
(cd "$latest" && sha256sum jag-* install.sh >SHA256SUMS)
refused "a changed build with new checksums" "isn't signed with Jaggery's key"
reset

echo "rm -rf /" >>"$latest/install.sh"
refused "a changed installer" "install.sh doesn't match"
reset

release "$latest" 1.2.0 prod "$work/stranger.pem"
refused "a release signed with an unknown key" "isn't signed with Jaggery's key"
release "$latest" 1.2.0 prod
extra_key=""
refused "a test release, against only the real keys" "isn't signed with Jaggery's key"
unset extra_key

jq '.withdrawn = "crashes on start"' "$latest/release.json" >"$work/w.json" && mv "$work/w.json" "$latest/release.json"
refused "a withdrawn release" "was withdrawn"
reset

echo "PASS: get.sh"
