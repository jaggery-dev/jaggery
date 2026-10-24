#!/bin/sh
# Signs a release directory (phase 2 U5): writes DIR/release.sig, an ed25519
# signature over the release's statement, from its release.json and
# SHA256SUMS.
#
#   JAGGERY_SIGNING_KEY="$(cat key.pem)" sh scripts/sign-release.sh DIR
#
# The key is a PEM private key in $JAGGERY_SIGNING_KEY: in CI the secret of
# the repo's release environment, in the test house a throwaway one. The
# statement must match update.Statement in internal/update/sign.go
# (TestSignReleaseScript checks the two agree). Needs OpenSSL 3 and jq.
set -eu

die() { printf 'error: %s\n' "$*" >&2; exit 1; }
[ $# -eq 1 ] || die "usage: sh scripts/sign-release.sh DIR"
dir=$1
[ -n "${JAGGERY_SIGNING_KEY:-}" ] || die "no key in JAGGERY_SIGNING_KEY"
[ -f "$dir/release.json" ] && [ -f "$dir/SHA256SUMS" ] || die "$dir needs release.json and SHA256SUMS"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
umask 077
printf '%s\n' "$JAGGERY_SIGNING_KEY" >"$tmp/key.pem"

jq -j '"jaggery release v1\nversion \(.version)\nchannel \(.channel)\ncommit \(.commit)\nbuilt_at \(.built_at)\n"' \
	"$dir/release.json" >"$tmp/statement"
cat "$dir/SHA256SUMS" >>"$tmp/statement"

openssl pkeyutl -sign -rawin -inkey "$tmp/key.pem" -in "$tmp/statement" -out "$tmp/sig"
openssl base64 -A -in "$tmp/sig" >"$dir/release.sig"
echo >>"$dir/release.sig"
