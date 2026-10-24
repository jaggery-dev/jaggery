#!/bin/sh
# Promote a beta build to prod (tech-specs/releases.md).
#
#   sh scripts/promote.sh <beta version>      e.g. 0.5.0-beta.23
#
# The candidate is a beta build that already ran in the house; prod is the
# same commit, tagged v<VERSION>. The script checks what it can (the build
# was published, its commit is on beta, VERSION on it matches), then shows
# the gates a person must confirm. It never decides they are met.
set -eu

die() { printf 'error: %s\n' "$*" >&2; exit 1; }

[ $# -eq 1 ] || die "usage: sh scripts/promote.sh <beta version, e.g. 0.5.0-beta.23>"
candidate=${1#v}
case "$candidate" in
*-beta.*) ;;
*) die "$candidate isn't a beta build; promote one that ran in the house" ;;
esac
release=${candidate%%-beta.*}

command -v gh >/dev/null || die "needs the GitHub CLI (gh)"
git fetch -q origin beta prod --tags

# The published build's commit, from its release.json; older builds used beta-N tags.
commit=""
for tag in "v$candidate" "beta-${candidate##*-beta.}"; do
	if json=$(gh release download "$tag" -p release.json -O - 2>/dev/null); then
		commit=$(printf '%s' "$json" | sed -n 's/.*"commit":"\([0-9a-f]*\)".*/\1/p')
		break
	fi
done
[ -n "$commit" ] || die "no published beta build $candidate"
git merge-base --is-ancestor "$commit" origin/beta || die "$commit isn't on beta"
git merge-base --is-ancestor origin/prod "$commit" || die "prod isn't behind $commit, so it can't fast-forward"
[ "$(git show "$commit:VERSION" | tr -d '[:space:]')" = "$release" ] || die "VERSION on $commit isn't $release"
git rev-parse -q --verify "refs/tags/v$release" >/dev/null && die "v$release already exists"

prev=$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' --exclude '*-*' origin/prod 2>/dev/null || echo "")
cat <<EOF

Promote $candidate ($commit) to prod as v$release.

Gates (tech-specs/releases.md); confirm each yourself:
  1. Soak: this commit has run on every house machine for 48 hours,
     with no rollback and no machine marking it bad.
  2. Every slice in the release met its done-when on real machines.
  3. A machine on ${prev:-the current prod} updated to it, and rolled back with its state intact.
  4. Release notes: the changes since ${prev:-the start}:
$(git log --format='       - %s' "${prev:+$prev..}$commit" | head -40)
  5. Any ROADMAP milestone it claims has its done-when met on hardware.

EOF
printf 'Type v%s to promote, anything else to stop: ' "$release"
read -r answer
[ "$answer" = "v$release" ] || die "stopped; nothing changed"

git push origin "$commit:refs/heads/prod"
git tag -a "v$release" -m "v$release" "$commit"
git push origin "v$release"

next=$(printf '%s' "$release" | awk -F. '{printf "%d.%d.0", $1, $2 + 1}')
cat <<EOF

v$release is tagged; CI builds and publishes it as a full release.
Next: on beta, set VERSION to $next (or the next patch) in a PR, so beta
builds become $next-beta.N.
EOF
