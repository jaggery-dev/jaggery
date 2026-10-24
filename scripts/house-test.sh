#!/bin/bash
# A house on one Linux machine: N agents, each in its own network and
# hostname namespace, joined by a bridge, as if they were separate machines on
# one home network (multicast and all). Then it checks what a house must do.
#
#   scripts/house-test.sh              build jag, run every scenario, clean up
#   scripts/house-test.sh --agents 4   more machines (default 3)
#   scripts/house-test.sh --keep       leave the state and logs for a look
#
# Runs without root where unprivileged user namespaces are allowed (most
# desktops); elsewhere run it with sudo. Nothing touches the real network:
# the bridge and every address (192.0.2.0/24, a documentation range) exist
# only inside the namespaces, and vanish with them.
set -euo pipefail

agents=3
keep=0
while [ $# -gt 0 ]; do
	case "$1" in
	--agents) agents=$2; shift 2 ;;
	--keep) keep=1; shift ;;
	-h | --help) sed -n '2,13p' "$0"; exit 0 ;;
	*) echo "unknown option: $1" >&2; exit 2 ;;
	esac
done

# ---- Outside: build, then re-run this script inside a fresh network
# namespace where we are root.
if [ -z "${HOUSE_TEST_INNER:-}" ]; then
	cd "$(git rev-parse --show-toplevel)"
	work=$(mktemp -d)
	# Releases here are signed with a throwaway key the builds trust (U5),
	# and a second key no build trusts signs the forgery in scenario 7.
	openssl genpkey -algorithm ed25519 -out "$work/signing-key.pem"
	openssl genpkey -algorithm ed25519 -out "$work/forger-key.pem"
	test_key=$(openssl pkey -in "$work/signing-key.pem" -pubout -outform DER | openssl base64 -A)
	# Three builds, as releases would be: the one the house starts on, a good
	# update, and a broken update that crashes soon after it starts.
	pkg=github.com/jaggery-dev/jaggery/internal/version
	build() { # <out> <version> <commit> [extra ldflags]
		go build -o "$1" -ldflags "-X $pkg.Version=$2 -X $pkg.Commit=$3 -X $pkg.Channel=beta -X $pkg.BuiltAt=2026-01-01T00:00:00Z -X github.com/jaggery-dev/jaggery/internal/update.testKey=$test_key ${4:-}" ./cmd/jag
	}
	build "$work/jag" 0.9.0-beta.1 1111111111111111111111111111111111111111
	build "$work/jag-good" 0.9.0-beta.2 2222222222222222222222222222222222222222
	build "$work/jag-crash" 0.9.0-beta.3 3333333333333333333333333333333333333333 "-X main.testCrashAfter=1s"
	build "$work/jag-forged" 0.9.0-beta.4 4444444444444444444444444444444444444444
	export HOUSE_TEST_INNER=1 JAG="$work/jag" WORK="$work" SCRIPTS="$PWD/scripts" ASSET="jag-$(go env GOOS)-$(go env GOARCH)"
	set +e
	if [ "$(id -u)" -eq 0 ]; then
		unshare --net --uts "$0" --agents "$agents"
	else
		unshare --user --map-root-user --net --uts "$0" --agents "$agents"
	fi
	status=$?
	set -e
	if [ "$keep" -eq 1 ] || [ $status -ne 0 ]; then
		echo "state and logs kept in $work"
	else
		rm -rf "$work"
	fi
	exit $status
fi

# ---- Inside: the house.
pids=()
cleanup() {
	for p in "${pids[@]}"; do kill -- "-$p" 2>/dev/null || kill "$p" 2>/dev/null || true; done
	[ -n "${releases_pid:-}" ] && kill "$releases_pid" 2>/dev/null || true
	# Fake apps a failed scenario left running.
	kill $(jobs -p) 2>/dev/null || true
	# An agent writes its state as it shuts down, after its loop has gone:
	# wait for every process in its group, or removing the state races it.
	for p in "${pids[@]}"; do
		for _ in $(seq 50); do
			kill -0 -- "-$p" 2>/dev/null || break
			sleep 0.2
		done
	done
	wait 2>/dev/null || true
}
trap cleanup EXIT

fail() {
	echo "FAIL: $*" >&2
	for i in $(seq 1 "$((agents + 1))"); do
		[ -f "$WORK/a$i/agent.log" ] || continue
		echo "---- a$i log (last 25 lines)" >&2
		tail -25 "$WORK/a$i/agent.log" >&2 || true
	done
	exit 1
}

# wait_for <seconds> <what> <command…>: retry until the command succeeds.
wait_for() {
	local limit=$1 what=$2
	shift 2
	local start=$SECONDS
	until "$@" >/dev/null 2>&1; do
		[ $((SECONDS - start)) -lt "$limit" ] || fail "$what (waited ${limit}s)"
		sleep 1
	done
	echo "ok: $what ($((SECONDS - start))s)"
}

ip link set lo up
ip link add br0 type bridge
ip link set br0 type bridge mcast_snooping 0
ip addr add 192.0.2.1/24 dev br0
ip link set br0 up
# A second network, routed to the first but not bridged: multicast (mDNS)
# doesn't cross, as over a VPN to a cloud machine.
ip link add br1 type bridge
ip addr add 198.51.100.1/24 dev br1
ip link set br1 up
echo 1 >/proc/sys/net/ipv4/ip_forward

# The fake release server (Q3), on the bridge; every agent updates from it.
mkdir -p "$WORK/releases"
python3 "$(dirname "$0")/fake-releases.py" --dir "$WORK/releases" --listen 192.0.2.1:8080 &
releases_pid=$!

remote=$((agents + 1))
addr() {
	if [ "$1" -eq "$remote" ]; then echo "198.51.100.10"; else echo "192.0.2.$((10 + $1))"; fi
}
gateway() {
	if [ "$1" -eq "$remote" ]; then echo "198.51.100.1"; else echo "192.0.2.1"; fi
}
bridge() {
	if [ "$1" -eq "$remote" ]; then echo br1; else echo br0; fi
}

# on <i> <command…>: run a command as machine i (its network, hostname and
# state directory).
on() {
	local i=$1
	shift
	nsenter --target "${pids[$((i - 1))]}" --net --uts env JAGGERY_STATE_DIR="$WORK/a$i" "$@"
}

# start_agent <i>: machine i in its own namespaces, on its network.
start_agent() {
	local i=$1
	mkdir -p "$WORK/a$i"
	# The agent's namespace waits for its network before starting jag.
	# Each machine has its own copy, which updates replace, and is restarted
	# when it exits, as systemd or launchd would.
	mkdir -p "$WORK/a$i/bin"
	cp "$JAG" "$WORK/a$i/bin/jag"
	JAGGERY_STATE_DIR="$WORK/a$i" BIN="$WORK/a$i/bin/jag" \
		JAGGERY_RELEASES_API=http://192.0.2.1:8080 JAGGERY_GITHUB_TOKEN=house-test \
		JAGGERY_UPDATE_TIMES=3s,5s,30s,8s JAGGERY_ALLOW_FROM=198.51.100.0/24 \
		setsid unshare --net --uts sh -c "
		hostname a$i
		until ip -4 addr show eth0 2>/dev/null | grep -q inet; do sleep 0.1; done
		while :; do \"\$BIN\" up; sleep 1; done" >"$WORK/a$i/agent.log" 2>&1 &
	pids+=($!)
	sleep 0.2
	ip link add "h$i" type veth peer name eth0 netns "$!"
	ip link set "h$i" master "$(bridge "$i")" up
	nsenter --target "$!" --net sh -c "ip link set lo up; ip addr add $(addr "$i")/24 dev eth0; ip link set eth0 up; ip route add default via $(gateway "$i")"
}
for i in $(seq 1 "$agents"); do
	start_agent "$i"
done
echo "started $agents agents on 192.0.2.0/24"

house() { jq -e "$2" "$WORK/a$1/house.json"; }
agent_up() { curl -sf -m 2 "http://$(addr "$1"):7373/healthz" || curl -sf -m 2 -o /dev/null "http://$(addr "$1"):7373/"; }

for i in $(seq 1 "$agents"); do
	wait_for 20 "a$i is up" agent_up "$i"
done

# ---- Scenario 1: join with a code, and house-wide trust (C1). Every machine
# joins through a1; each then trusts every other.
for i in $(seq 2 "$agents"); do
	code=$(on 1 "$JAG" invite | awk '/jag join/ {print $3; exit}')
	[ -n "$code" ] || fail "a1 issued no join code"
	on "$i" "$JAG" join "$code" >"$WORK/a$i/join.log" 2>&1 || { cat "$WORK/a$i/join.log" >&2; fail "a$i could not join with a code"; }
done
echo "ok: $((agents - 1)) machines joined with codes"

# ---- The LAN port refuses web pages: a rebound domain name in Host, and a
# cross-origin change from a browser. Apps that aren't browsers get through.
lan_status() { on 2 curl -s -o /dev/null -w '%{http_code}' -m 3 "$@"; }
[ "$(lan_status -H 'Host: rebind.example.com' "http://$(addr 1):7373/v1/house")" = 403 ] ||
	fail "a1 answered a rebound Host name"
[ "$(lan_status -X POST -H 'Origin: https://evil.example.com' -H 'Sec-Fetch-Site: cross-site' \
	-d '{"model":"none"}' "http://$(addr 1):7373/v1/chat/completions")" = 403 ] ||
	fail "a1 took a cross-origin POST from a web page"
[ "$(lan_status -H 'Host: a1.local' "http://$(addr 1):7373/v1/house")" = 200 ] ||
	fail "a1 refused its own local name"
echo "ok: the LAN port refuses rebound names and cross-origin changes"

# ---- Apps added by hand (jag apps): a link added on a1 reaches a2's page;
# one by port shows only while the port answers.
on 1 "$JAG" apps add Router --url http://192.0.2.1 --description "Wi-Fi settings" >/dev/null
on 1 "$JAG" apps add Grafana --port 7999 >/dev/null
app_on_a2() { house 2 ".peers[] | select(.name == \"a1\") | .inventory.apps[]? | select($1)"; }
wait_for 60 "a link added on a1 reaches a2" app_on_a2 '.url == "http://192.0.2.1" and .description == "Wi-Fi settings"'
! app_on_a2 '.name == "Grafana"' >/dev/null || fail "an app whose port is closed was listed"
on 1 "$JAG" apps remove Router >/dev/null
on 1 "$JAG" apps remove Grafana >/dev/null

all_reachable() {
	local i=$1 want=$((agents - 1))
	house "$i" "[.peers[] | select(.trust == \"approved\" and .reachable)] | length == $want"
}
for i in $(seq 1 "$agents"); do
	wait_for 90 "a$i sees every other machine approved and reachable" all_reachable "$i"
done

# ---- App names (track W1): a name given on a1 reaches a2's page; a2 can't
# give the same name to its own app; the name goes when a1 removes it.
on 1 python3 -m http.server 7999 --bind "$(addr 1)" >/dev/null 2>&1 &
web1=$!
on 2 python3 -m http.server 7998 --bind "$(addr 2)" >/dev/null 2>&1 &
web2=$!
on 1 "$JAG" apps add Graphs --port 7999 >/dev/null
on 2 "$JAG" apps add Files --port 7998 >/dev/null
own_app() { house "$1" ".self.inventory.apps[]? | select(.name == \"$2\")"; }
wait_for 60 "a1 lists its own app" own_app 1 Graphs
on 1 "$JAG" apps name Graphs "Charts.local" >/dev/null || fail "a1 could not name its app"
wait_for 60 "a1's app name reaches a2" app_on_a2 '.name == "Graphs" and .local_name == "charts"'
wait_for 60 "a2 lists its own app" own_app 2 Files
if out=$(on 2 "$JAG" apps name Files charts 2>&1); then fail "a2 gave its app a name a1's app has"; fi
grep -q "already the name of Graphs on a1" <<<"$out" || fail "a2's refusal didn't say why: $out"
if on 2 "$JAG" apps name Files a1 >/dev/null 2>&1; then fail "a2 named an app after a machine"; fi
on 1 "$JAG" apps name Graphs --remove >/dev/null
wait_for 60 "a removed name leaves a2's page" app_on_a2 '.name == "Graphs" and .local_name == null'
on 2 "$JAG" apps name Files charts >/dev/null || fail "a2 could not take the name once a1 removed it"
kill "$web1" "$web2" 2>/dev/null || true
on 1 "$JAG" apps remove Graphs >/dev/null
on 2 "$JAG" apps remove Files >/dev/null
echo "ok: app names travel, clash and go"

# ---- Scenario 2: one name holder, agreed by everyone (C2).
same_holder() {
	local first
	first=$(jq -r '.name_holder // ""' "$WORK/a1/house.json")
	[ -n "$first" ] || return 1
	for i in $(seq 2 "$agents"); do
		[ "$(jq -r '.name_holder // ""' "$WORK/a$i/house.json")" = "$first" ] || return 1
	done
}
wait_for 60 "every machine agrees on one name holder" same_holder
for i in $(seq 1 "$agents"); do
	if house "$i" '.name_claims' >/dev/null 2>&1; then fail "a$i reports a claim on the shared names from inside the house"; fi
done

# ---- App names, answered and forwarded (track W2). An app on a machine
# that isn't the name holder, named there, is found over mDNS at the
# holder's address and opened by its name through the holder: a page, a
# streamed answer and a WebSocket. An app on localhost only can't be named.
holder_id=$(jq -r '.name_holder' "$WORK/a1/house.json")
holder=""
for i in $(seq 1 "$agents"); do
	[ "$(jq -r '.self.machine_id' "$WORK/a$i/house.json")" = "$holder_id" ] && holder=$i
done
[ -n "$holder" ] || fail "no machine is the name holder"
appm=$((holder % agents + 1))
fake_app() { on "$appm" python3 "$SCRIPTS/fake-app.py" "$@"; }
fake_app serve "$(addr "$appm")" 7997 >/dev/null 2>&1 &
appweb=$!
on "$appm" python3 "$SCRIPTS/fake-app.py" serve 127.0.0.1 7996 >/dev/null 2>&1 &
hiddenweb=$!
on "$appm" "$JAG" apps add Echo --port 7997 >/dev/null
on "$appm" "$JAG" apps add Hidden --port 7996 >/dev/null
wait_for 60 "a$appm lists its fake app" own_app "$appm" Echo
on "$appm" "$JAG" apps name Echo echo >/dev/null || fail "a$appm could not name its fake app"
if on "$appm" "$JAG" apps name Hidden hidden >/dev/null 2>&1; then fail "an app on localhost only was given a name"; fi
answers_echo() { [ "$(on "$appm" python3 "$SCRIPTS/fake-app.py" mdns echo.local 2>/dev/null)" = "$(addr "$holder")" ]; }
wait_for 60 "the holder (a$holder) answers echo.local over mDNS" answers_echo
web=$(addr "$holder"):80
house "$holder" '.self.inventory.serves_web' >/dev/null || web=$(addr "$holder"):7373
page=$(on "$appm" python3 "$SCRIPTS/fake-app.py" get "$web" echo.local) || fail "echo.local didn't open through the holder"
grep -q "hello from the fake app; Host echo.local; forwarded host echo.local" <<<"$page" || fail "echo.local gave the wrong page: $page"
grep -qi "^X-Frame-Options: SAMEORIGIN" <<<"$page" || fail "the app's own headers were changed: $page"
on "$appm" python3 "$SCRIPTS/fake-app.py" stream "$web" echo.local || fail "a streamed answer through echo.local was held back"
on "$appm" python3 "$SCRIPTS/fake-app.py" ws "$web" echo.local || fail "a WebSocket through echo.local failed"
on "$appm" "$JAG" apps name Echo --remove >/dev/null
gone_echo() { ! on "$appm" python3 "$SCRIPTS/fake-app.py" mdns echo.local >/dev/null 2>&1; }
wait_for 60 "echo.local stops answering once the name is removed" gone_echo
kill "$appweb" "$hiddenweb" 2>/dev/null || true
on "$appm" "$JAG" apps remove Echo >/dev/null
on "$appm" "$JAG" apps remove Hidden >/dev/null
echo "ok: an app name is answered by the holder and opens the app on a$appm through $web (page, stream, WebSocket)"

# ---- Scenario 3: shared folders across machines (phase 4 S2, S3). A share
# on the last machine, read and written through a1's MCP server.
last=$agents
mkdir -p "$WORK/share"
printf 'hello from a%s\n' "$last" >"$WORK/share/hello.txt"
on "$last" "$JAG" share add "$WORK/share" --name docs --writable >/dev/null
a1_sees_share() { house 1 ".peers[] | select(.name == \"a$last\") | .inventory.shares[]? | select(.name == \"docs\")"; }
wait_for 60 "a1 sees a$last's share" a1_sees_share

mcp="http://$(addr 1):7373/mcp"
hdr=(-H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream')
sid=$(curl -sf -D - -o /dev/null "${hdr[@]}" "$mcp" \
	-d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"house-test","version":"1"}}}' |
	awk -F': ' 'tolower($1) == "mcp-session-id" {print $2}' | tr -d '\r')
[ -n "$sid" ] || fail "a1's MCP server gave no session"
curl -sf "${hdr[@]}" -H "Mcp-Session-Id: $sid" "$mcp" -d '{"jsonrpc":"2.0","method":"notifications/initialized"}' >/dev/null
tool() {
	curl -sf "${hdr[@]}" -H "Mcp-Session-Id: $sid" "$mcp" \
		-d "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"$1\",\"arguments\":$2}}" |
		sed -n 's/^data: //p'
}
got=$(tool read_file '{"share":"docs","path":"hello.txt"}' | jq -r '.result.content[1].text')
[ "$got" = "hello from a$last" ] || fail "reading a$last's file through a1 gave: $got"
echo "ok: a1 read a file from a$last's share over mTLS"
tool write_file '{"share":"docs","path":"from-a1.txt","content":"written through a1\n"}' | jq -e '.result.isError | not' >/dev/null ||
	fail "writing to a$last's share through a1 failed"
[ "$(cat "$WORK/share/from-a1.txt")" = "written through a1" ] || fail "the write didn't land on a$last"
echo "ok: a1 wrote a file into a$last's share"
on "$last" "$JAG" files log | grep -q "write docs:from-a1.txt.*via a1" || fail "a$last's log doesn't show the write via a1"
echo "ok: a$last's access log shows the write via a1"

# ---- Scenario 4: a machine beyond the local network (a cloud GPU box over a
# VPN, say). It is routed to the house but hears no mDNS, so it joins with a
# code sent to a1's address.
start_agent "$remote"
wait_for 20 "a$remote (on another network) is up" agent_up "$remote"
sleep 12
[ "$(jq '.peers | length' "$WORK/a$remote/peers.json" 2>/dev/null || echo 0)" -eq 0 ] ||
	fail "a$remote found machines by mDNS across a routed link"
echo "ok: a$remote hears no mDNS from the house"
code=$(on 1 "$JAG" invite | awk '/jag join/ {print $3; exit}')
on "$remote" "$JAG" join "$code" --at "$(addr 1)" >"$WORK/a$remote/join.log" 2>&1 ||
	{ cat "$WORK/a$remote/join.log" >&2; fail "a$remote could not join with --at"; }
echo "ok: a$remote joined with a code sent to a1's address"
remote_known() { house "$1" ".peers[] | select(.name == \"a$remote\" and .trust == \"approved\" and .reachable)"; }
for i in $(seq 1 "$agents"); do
	wait_for 90 "a$i trusts and reaches a$remote" remote_known "$i"
done
remote_sees_all() { house "$remote" "[.peers[] | select(.trust == \"approved\" and .reachable)] | length == $agents"; }
wait_for 90 "a$remote trusts and reaches every machine" remote_sees_all

# ---- Scenario 5: an update rolls out (U1-U3, R1). The name holder installs
# it first; the rest follow once it has proven itself. One machine can't
# reach the release server (like a machine without a GitHub token) and gets
# the build from a peer's relay.
holder_id=$(jq -r '.name_holder' "$WORK/a1/house.json")
holder=""
blocked=""
for i in $(seq 1 "$remote"); do
	id=$(jq -r '.self.machine_id' "$WORK/a$i/house.json")
	if [ "$id" = "$holder_id" ]; then holder=$i; elif [ -z "$blocked" ] && [ "$i" -ne "$remote" ]; then blocked=$i; fi
done
[ -n "$holder" ] && [ -n "$blocked" ] || fail "couldn't pick the holder and a machine to block"
for i in $(seq 1 "$remote"); do [ "$i" -eq "$blocked" ] || addr "$i"; done >"$WORK/releases/allow"

publish() { # <tag> <binary> <version> <commit> [signing key]
	local d="$WORK/staging/$1"
	mkdir -p "$d"
	cp "$2" "$d/$ASSET"
	(cd "$d" && sha256sum "$ASSET" >SHA256SUMS)
	printf '{"version":"%s","channel":"beta","commit":"%s","built_at":"%s"}\n' "$3" "$4" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$d/release.json"
	JAGGERY_SIGNING_KEY=$(cat "${5:-$WORK/signing-key.pem}") sh "$SCRIPTS/sign-release.sh" "$d"
	mv "$d" "$WORK/releases/$1" # whole, so no agent sees it half-signed
}
build_of() { jq -r '.self.inventory.build' "$WORK/a$1/house.json"; }
runs() { [ "$(build_of "$1")" = "$2" ]; }

publish v0.9.0-beta.2 "$WORK/jag-good" 0.9.0-beta.2 2222222222222222222222222222222222222222
wait_for 60 "the name holder (a$holder) installs 0.9.0-beta.2 first" runs "$holder" 0.9.0-beta.2
for i in $(seq 1 "$remote"); do
	[ "$i" -eq "$holder" ] || runs "$i" 0.9.0-beta.1 || fail "a$i installed 0.9.0-beta.2 before the holder proved it"
done
echo "ok: the rest wait for the holder to prove it"
for i in $(seq 1 "$remote"); do
	wait_for 120 "a$i runs 0.9.0-beta.2" runs "$i" 0.9.0-beta.2
done
grep -q "installed 0.9.0-beta.2 from" "$WORK/a$blocked/agent.log" || fail "a$blocked didn't get the build from a peer's relay"
echo "ok: a$blocked, cut off from the release server, got it from a peer's relay"
rm -f "$WORK/releases/allow"

# ---- Scenario 6: a broken update is rolled back and pulled house-wide. The
# holder installs a build that crashes after start; after repeated failed
# starts it goes back to the previous build and marks this one bad, and no
# other machine installs it.
publish v0.9.0-beta.3 "$WORK/jag-crash" 0.9.0-beta.3 3333333333333333333333333333333333333333
marked_bad() { jq -e '.self.inventory.bad_commits // [] | index("3333333333333333333333333333333333333333")' "$WORK/a$holder/house.json"; }
wait_for 120 "the holder rolls back from the crashing 0.9.0-beta.3 and marks it bad" marked_bad
wait_for 30 "the holder runs 0.9.0-beta.2 again" runs "$holder" 0.9.0-beta.2
grep -q "failed to start 3 times; going back" "$WORK/a$holder/agent.log" || fail "the holder's log doesn't show the rollback"
sleep 35 # longer than the stagger: the others would have installed it by now
for i in $(seq 1 "$remote"); do
	runs "$i" 0.9.0-beta.2 || fail "a$i runs $(build_of "$i") after the holder pulled 0.9.0-beta.3"
	[ "$i" -eq "$holder" ] && continue
	! grep -q "installed 0.9.0-beta.3" "$WORK/a$i/agent.log" || fail "a$i installed the bad build while the holder was trying it"
done
echo "ok: no other machine installed the bad build"

# ---- Scenario 7: a release not signed by a trusted key (U5), as if someone
# could publish to the repo without the release key. Nobody installs it,
# from the release server or from a relay.
publish v0.9.0-beta.4 "$WORK/jag-forged" 0.9.0-beta.4 4444444444444444444444444444444444444444 "$WORK/forger-key.pem"
refused() { grep -q "refused the newest release on GitHub: 0.9.0-beta.4 is not signed by a Jaggery release key" "$WORK/a$1/agent.log"; }
for i in $(seq 1 "$remote"); do
	wait_for 60 "a$i refuses the wrongly signed 0.9.0-beta.4" refused "$i"
done
sleep 35 # longer than the stagger
for i in $(seq 1 "$remote"); do
	runs "$i" 0.9.0-beta.2 || fail "a$i runs $(build_of "$i") after a wrongly signed release"
done
echo "ok: no machine installed a wrongly signed build"

# ---- Scenario 8: a device outside the house answers for ai.local.
# Every machine on the network hears it; the page's data and jag doctor say
# where it is.
python3 "$SCRIPTS/fake-app.py" claim ai.local 192.0.2.50 192.0.2.1 &
rogue=$!
claimed() { house "$1" '.name_claims[]? | select(.name == "ai.local" and .addr == "192.0.2.50")'; }
for i in $(seq 1 "$agents"); do
	wait_for 60 "a$i notices 192.0.2.50 answering for ai.local" claimed "$i"
done
kill "$rogue" 2>/dev/null || true
doctor=$(on 1 "$JAG" doctor 2>&1 || true)
grep -q "Something at 192.0.2.50 also answers for ai.local" <<<"$doctor" || fail "jag doctor doesn't name the rogue device: $doctor"
curl -sf -m 3 "http://$(addr 1):7373/v1/house" | jq -e '.name_claims[] | select(.addr == "192.0.2.50")' >/dev/null || fail "the page's data doesn't carry the claim"
echo "ok: a rogue device answering for ai.local shows in jag doctor and on the page"

echo
echo "PASS: $agents machines and one beyond the network, every scenario"
