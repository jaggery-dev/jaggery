#!/bin/sh
# Runs the checks in .github/workflows/ci.yml on this machine: gofmt, the CLI
# reference (scripts/gen-cli-docs.sh --check), the licences built into jag
# (scripts/gen-licences.sh --check), sign-offs (scripts/check-dco.sh), vet and race tests with the Go
# version in go.mod, the personal-material check, the smoke test, the house
# test (scripts/house-test.sh), and the cross-builds. A PR merges when this
# passes; GitHub's CI is not waited for.
#
# The personal markers come from $PERSONAL_MARKERS, or from the file
# ${XDG_CONFIG_HOME:-$HOME/.config}/jaggery-dev/personal-markers (one extended
# regex), which stays outside the repo.
set -eu
cd "$(git rev-parse --show-toplevel)"

gover=$(sed -n 's/^go //p' go.mod)
export GOTOOLCHAIN="go$gover"
step() { printf '\n== %s\n' "$*"; }

step "test: gofmt (go $gover)"
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  echo "these files need gofmt:"
  echo "$unformatted"
  exit 1
fi

step "test: cli reference"
sh scripts/gen-cli-docs.sh --check

step "test: licences built into jag"
sh scripts/gen-licences.sh --check

step "dco: commits on this branch are signed off"
sh scripts/check-dco.sh origin/beta HEAD

step "test: go vet"
go vet ./...

step "test: go test -race"
go test -race -count=1 ./...

step "separation"
markers_file="${XDG_CONFIG_HOME:-$HOME/.config}/jaggery-dev/personal-markers"
if [ -z "${PERSONAL_MARKERS:-}" ] && [ -r "$markers_file" ]; then
  PERSONAL_MARKERS=$(cat "$markers_file")
  export PERSONAL_MARKERS
fi
if [ -z "${PERSONAL_MARKERS:-}" ]; then
  echo "warning: no personal markers set; only the structural patterns are checked"
fi
scripts/check-personal.sh

step "smoke"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
go build -o "$tmp/jag" ./cmd/jag
cat > "$tmp/smoke.sh" <<'SMOKE'
set -eu
if "$JAG" status >/dev/null 2>&1; then
  echo "status should fail before any identity exists"
  exit 1
fi
timeout -s TERM 5 "$JAG" up >/dev/null 2>&1 || [ $? -eq 124 ]
before=$("$JAG" status | grep 'machine id')
timeout -s TERM 5 "$JAG" up >/dev/null 2>&1 || [ $? -eq 124 ]
after=$("$JAG" status | grep 'machine id')
if [ "$before" != "$after" ]; then
  echo "identity changed across restarts: '$before' then '$after'"
  exit 1
fi
echo "identity kept across restarts"
# --json: one envelope on stdout. doctor fails here (no agent, no peers).
"$JAG" status --json | grep -q '"ok": true'
code=0; "$JAG" doctor --json >"$JAGGERY_STATE_DIR/doctor.json" || code=$?
if [ "$code" -ne 5 ] || ! grep -q '"code": "checks_failed"' "$JAGGERY_STATE_DIR/doctor.json"; then
  echo "jag doctor --json: exit $code"; cat "$JAGGERY_STATE_DIR/doctor.json"; exit 1
fi
echo "status and doctor answer --json"
SMOKE
export JAG="$tmp/jag" JAGGERY_STATE_DIR="$tmp/state"
# A running agent on this machine holds Jaggery's ports, so on Linux the
# smoke test gets its own network with one dummy interface (no root needed).
if command -v unshare >/dev/null && unshare -rn true 2>/dev/null; then
  unshare -rn sh -c 'ip link set lo up && ip link add eth0 type dummy &&
    ip addr add 192.0.2.10/24 dev eth0 && ip link set eth0 up && sh "$0"' "$tmp/smoke.sh"
else
  sh "$tmp/smoke.sh"
fi

step "house"
if { command -v unshare >/dev/null && unshare -rn true 2>/dev/null; } || [ "$(id -u)" -eq 0 ]; then
  # Keep the exit status: a pipe into tail would hide a failure.
  if ! out=$(scripts/house-test.sh 2>&1); then
    printf '%s\n' "$out" | tail -40
    exit 1
  fi
  printf '%s\n' "$out" | tail -1
else
  echo "skipped: no user namespaces here (run scripts/house-test.sh with sudo)"
fi

step "installer"
if ! out=$(sh scripts/get-test.sh 2>&1); then
  printf '%s\n' "$out" | tail -20
  exit 1
fi
printf '%s\n' "$out" | tail -1

step "cross-build"
for t in linux/amd64 linux/arm64 linux/arm/7 darwin/arm64 darwin/amd64 windows/amd64; do
  goos=${t%%/*}; rest=${t#*/}; goarch=${rest%%/*}; goarm=""
  [ "$rest" != "$goarch" ] && goarm=${rest#*/}
  CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch GOARM=$goarm go build -o /dev/null ./cmd/jag
  echo "built $t"
done

printf '\nall CI checks passed locally\n'
