#!/bin/sh
# Writes docs/reference/cli.md from jag's own help text, so the reference
# can't drift from the binary. Run it after changing any usage text:
#   sh scripts/gen-cli-docs.sh
# CI runs it with --check, which fails if the committed file is out of date.
set -eu
cd "$(git rev-parse --show-toplevel)"
check=false
case "${1:-}" in
  --check) check=true ;;
  "") ;;
  *) echo "usage: sh scripts/gen-cli-docs.sh [--check]" >&2; exit 2 ;;
esac
bin=$(mktemp)
state=$(mktemp -d)
out=$(mktemp)
trap 'rm -rf "$bin" "$state" "$out"' EXIT
go build -o "$bin" ./cmd/jag
run() { JAGGERY_STATE_DIR="$state" "$bin" "$@" 2>&1 || true; }

{
  echo "# Commands"
  echo
  echo "Generated from \`jag help\` by \`scripts/gen-cli-docs.sh\`; edit the usage text in \`cmd/jag\`, not this file."
  echo
  echo '```'
  run help
  echo '```'
  for cmd in up status peers map invite join approve ping forget models doctor activity outbound apps share files version update; do
    echo
    echo "## jag $cmd"
    echo
    echo '```'
    run "$cmd" --help
    echo '```'
  done
  echo
  echo "## Exit codes"
  echo
  echo '```'
  run help exit-codes
  echo '```'
  echo
  echo "Files, ports and environment variables are in [files-and-ports.md](files-and-ports.md)."
} > "$out"

if $check; then
  if ! diff -u docs/reference/cli.md "$out"; then
    echo "docs/reference/cli.md is out of date: run sh scripts/gen-cli-docs.sh and commit it" >&2
    exit 1
  fi
  echo "docs/reference/cli.md is up to date"
  exit 0
fi
cp "$out" docs/reference/cli.md
echo "wrote docs/reference/cli.md"
