#!/usr/bin/env bash
# Tests for sh-network-watch.sh with a fake database, env file and Signal
# endpoint: below the threshold nothing is sent, past it one alert goes out,
# a second run in the same month sends nothing, a new month alerts again, and
# an allowance of 0 never alerts. Run by `make check`.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
S="$HERE/sh-network-watch.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
mkdir -p "$T/bin"
# curl stand-in: records that a message was sent.
printf '#!/bin/sh\necho sent >> "%s/sent"\n' "$T" > "$T/bin/curl"; chmod +x "$T/bin/curl"
# psql stand-in: answers with $OUT bytes.
printf '#!/bin/sh\necho "$OUT"\n' > "$T/bin/fakepsql"; chmod +x "$T/bin/fakepsql"
printf 'Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\neth9\t00000000\t0101000A\t0003\t0\t0\t100\t00000000\n' > "$T/route"
printf 'SIGNAL_ACCOUNT=+10000000000\nSIGNAL_TO=+10000000001\n' > "$T/watch.env"
export PATH="$T/bin:$PATH" ENV_FILE="$T/env" WATCH_ENV="$T/watch.env" STATE_DIR="$T/state" PROC_ROUTE="$T/route"
sent() { [ -f "$T/sent" ] && wc -l < "$T/sent" | tr -d ' ' || echo 0; }
run() { OUT="$1" PSQL=fakepsql MONTH="$2" bash "$S"; }

printf 'NETWORK_MONTHLY_ALLOWANCE_GB=100\n' > "$T/env"
run $((50*1073741824)) 2026-09
[ "$(sent)" = 0 ] || fail "alerted at 50%"
run $((80*1073741824)) 2026-09
[ "$(sent)" = 1 ] || fail "no alert at 80%"
grep -qx 2026-09 "$T/state/alerted" || fail "month not recorded"
run $((90*1073741824)) 2026-09
[ "$(sent)" = 1 ] || fail "alerted twice in one month"
run $((80*1073741824)) 2026-10
[ "$(sent)" = 2 ] || fail "no alert in the next month"
printf 'NETWORK_MONTHLY_ALLOWANCE_GB=100\nNETWORK_ALERT_PCT=90\n' > "$T/env"
run $((85*1073741824)) 2026-11
[ "$(sent)" = 2 ] || fail "ignored NETWORK_ALERT_PCT"
printf 'NETWORK_MONTHLY_ALLOWANCE_GB=0\n' > "$T/env"
run $((999*1073741824)) 2026-12
[ "$(sent)" = 2 ] || fail "alerted with the allowance off"
out=$(OUT=5 PSQL=fakepsql bash "$S" --test)
case "$out" in "network eth9:"*) ;; *) fail "--test: $out" ;; esac
echo "sh-network-watch: ok"
