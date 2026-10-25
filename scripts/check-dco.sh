#!/bin/sh
# Checks every commit in <base>..<head> is signed off under the Developer
# Certificate of Origin (CONTRIBUTING.md, "Sign your work"): its message has a
# Signed-off-by: line with the author's email. Merge commits are skipped.
#   sh scripts/check-dco.sh origin/beta HEAD
# Commit text is only compared, never run, so a PR can't steer this script.
set -eu
if [ $# -ne 2 ]; then
  echo "usage: sh scripts/check-dco.sh <base> <head>" >&2
  exit 2
fi
base=$1
head=$2

lower() { tr '[:upper:]' '[:lower:]'; }

commits=$(git rev-list --no-merges "$base..$head")
bad=0
count=0
for c in $commits; do
  count=$((count + 1))
  email=$(git log -1 --format=%ae "$c" | lower)
  if git log -1 --format='%(trailers:key=Signed-off-by,valueonly)' "$c" |
    lower | grep -F -q "<$email>"; then
    continue
  fi
  bad=$((bad + 1))
  echo "not signed off: $(git log -1 --format='%h %s' "$c")"
  echo "  needs: Signed-off-by: $(git log -1 --format='%an <%ae>' "$c")"
done

if [ "$bad" -gt 0 ]; then
  cat >&2 <<'EOF'

Each commit needs a Signed-off-by: line with its author's name and email,
which says you have the right to submit it (CONTRIBUTING.md, "Sign your work").
Add it to the commits on your branch and push again:
  git rebase --signoff origin/beta
  git push --force-with-lease
EOF
  exit 1
fi
echo "$count commit(s) signed off"
