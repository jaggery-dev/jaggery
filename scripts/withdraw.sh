#!/bin/sh
# Withdraw a published release (tech-specs/releases.md).
#
#   sh scripts/withdraw.sh <version> "<reason>"     e.g. 0.5.0-beta.23 "crashes on the Pi"
#
# Marks the release's release.json as withdrawn. Agents then skip it, and a
# machine running it moves to the newest release that wasn't withdrawn, even
# an older one. Nothing is deleted, so what happened stays on record.
set -eu

die() { printf 'error: %s\n' "$*" >&2; exit 1; }

[ $# -eq 2 ] && [ -n "$2" ] || die 'usage: sh scripts/withdraw.sh <version> "<reason>"'
v=${1#v}
reason=$2
command -v gh >/dev/null || die "needs the GitHub CLI (gh)"
command -v jq >/dev/null || die "needs jq"

tag=""
for t in "v$v" "beta-${v##*-beta.}"; do
	if gh release view "$t" >/dev/null 2>&1; then
		tag=$t
		break
	fi
done
[ -n "$tag" ] || die "no release $v"

dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
gh release download "$tag" -p release.json -D "$dir"
[ "$(jq -r .version "$dir/release.json")" = "$v" ] || die "release $tag isn't $v"
if [ "$(jq -r '.withdrawn // ""' "$dir/release.json")" != "" ]; then
	die "$v is already withdrawn: $(jq -r .withdrawn "$dir/release.json")"
fi

printf 'Withdraw %s (%s): "%s"?\nMachines running it will move off it. Type %s to go on: ' "$v" "$tag" "$reason" "$v"
read -r answer
[ "$answer" = "$v" ] || die "stopped; nothing changed"

jq --arg why "$reason" '.withdrawn = $why' "$dir/release.json" >"$dir/release.json.new"
mv "$dir/release.json.new" "$dir/release.json"
gh release upload "$tag" "$dir/release.json" --clobber
gh release edit "$tag" --title "WITHDRAWN $v: $reason"
echo "withdrawn; agents with GitHub access see it at their next check and tell the house"
