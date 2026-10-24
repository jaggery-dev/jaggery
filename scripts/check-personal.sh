#!/bin/sh
# Fails if the repo contains personal material: home paths, references to a
# private companion repo, or any pattern in $PERSONAL_MARKERS (an extended
# regex kept in a CI secret, so the list of names is not itself published).
# Run it locally with: PERSONAL_MARKERS='name1|name2' scripts/check-personal.sh
set -eu
cd "$(git rev-parse --show-toplevel)"

# Structural patterns that never belong in a product repo.
patterns='/home/[a-z]|/Users/[A-Za-z]|house[ -]?kit'
if [ -n "${PERSONAL_MARKERS:-}" ]; then
  patterns="$patterns|$PERSONAL_MARKERS"
fi

# Neutral names used in examples are fine.
hits=$(git grep -n -I -i -E "$patterns" -- . ':!scripts/check-personal.sh' \
  | grep -v -i -E '/(home|Users)/(alex|user|you|name|runner|someone)\b' || true)

if [ -n "$hits" ]; then
  echo "personal material found (use neutral examples: nas, Alex, 192.0.2.x, example.com):"
  echo "$hits"
  exit 1
fi
echo "no personal markers"
