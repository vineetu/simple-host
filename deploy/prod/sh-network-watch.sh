#!/usr/bin/env bash
# Daily network check, the companion of the box's disk watch: Signal the owner
# once a month when this month's outbound data passes NETWORK_ALERT_PCT
# (default 75) of NETWORK_MONTHLY_ALLOWANCE_GB (default 10240, Oracle Cloud
# Always Free's 10 TB; 0 turns the alert off). Both are read, one named key
# each, from the server's env file, so the alert and the admin page's network
# bar use the same numbers.
#
# This month's outbound is what the server has recorded in net_usage_daily
# (internal/netusage samples the main interface's counters every
# NETWORK_SAMPLE_MINUTES), so it survives reboots.
#
# Who to Signal is not in this repo: SIGNAL_ACCOUNT and SIGNAL_TO come from
# /etc/sh-network-watch.env (root, 600), SIGNAL_RPC defaults to signal-cli's
# JSON-RPC on 127.0.0.1:8086.
#
# Install: sudo install -m 755 deploy/prod/sh-network-watch.sh /usr/local/bin/sh-network-watch && sudo install -m 644 deploy/prod/sh-network-watch.service deploy/prod/sh-network-watch.timer /etc/systemd/system/ && sudo systemctl daemon-reload && sudo systemctl enable --now sh-network-watch.timer
#   sh-network-watch --test   prints the figures, sends nothing
# State: $STATE_DIR/alerted holds the month (YYYY-MM) already alerted.
# Test: deploy/prod/sh-network-watch_test.sh.
set -u
ENV_FILE=${ENV_FILE:-/etc/simple-host.env}
WATCH_ENV=${WATCH_ENV:-/etc/sh-network-watch.env}
STATE_DIR=${STATE_DIR:-/var/lib/sh-network-watch}
PSQL=${PSQL:-sudo -u postgres psql -d simplehost -Atc}
SIGNAL_RPC=${SIGNAL_RPC:-http://127.0.0.1:8086/api/v1/rpc}
MONTH=${MONTH:-$(date -u +%Y-%m)}
[ -r "$WATCH_ENV" ] && . "$WATCH_ENV"

# One named key from the env file; never the whole file.
setting() { local v; v=$(grep -m1 "^$1=" "$ENV_FILE" 2>/dev/null | cut -d= -f2- | tr -dc 0-9); echo "${v:-$2}"; }
allow_gb=$(setting NETWORK_MONTHLY_ALLOWANCE_GB 10240)
alert_pct=$(setting NETWORK_ALERT_PCT 75)
iface=$(grep -m1 "^NETWORK_INTERFACE=" "$ENV_FILE" 2>/dev/null | cut -d= -f2- | tr -dc 'A-Za-z0-9_.:-')
[ -n "$iface" ] || iface=$(awk '$2=="00000000" && $8=="00000000" {print $1; exit}' "${PROC_ROUTE:-/proc/net/route}")

out=$($PSQL "select coalesce(sum(tx_bytes),0) from net_usage_daily where iface = '$iface' and day >= '$MONTH-01'::date" 2>/dev/null | tr -dc 0-9)
out=${out:-0}
if [ "$allow_gb" -gt 0 ]; then pct=$(( out * 100 / (allow_gb * 1073741824) )); else pct=0; fi
gb=$(awk -v b="$out" 'BEGIN{printf "%.0f", b/1073741824}')
allow=$( [ "$allow_gb" -ge 1024 ] && awk -v g="$allow_gb" 'BEGIN{printf "%g TB", g/1024}' || echo "$allow_gb GB")

mkdir -p "$STATE_DIR"; F="$STATE_DIR/alerted"; last=$(cat "$F" 2>/dev/null || true)
send() {
  curl -s -m 20 -X POST "$SIGNAL_RPC" -H 'Content-Type: application/json' -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"send\",\"params\":{\"account\":\"$SIGNAL_ACCOUNT\",\"recipient\":[\"$SIGNAL_TO\"],\"message\":$(python3 -c 'import json,sys;print(json.dumps(sys.argv[1]))' "$1")}}" >/dev/null
}
if [ "${1:-}" = "--test" ]; then echo "network $iface: ${gb} GB out in $MONTH, ${pct}% of $allow (alert at ${alert_pct}%), last alert ${last:-none}"; exit 0; fi
if [ "$allow_gb" -gt 0 ] && [ "$pct" -ge "$alert_pct" ] && [ "$last" != "$MONTH" ]; then
  if [ -z "${SIGNAL_ACCOUNT:-}" ] || [ -z "${SIGNAL_TO:-}" ]; then echo "sh-network-watch: SIGNAL_ACCOUNT and SIGNAL_TO are not set in $WATCH_ENV" >&2; exit 1; fi
  send "Box outbound data is ${pct}% of this month's free ${allow} (${gb} GB sent so far in $MONTH). Past the allowance, Oracle bills each extra GB. The admin page's Overview shows which websites send the most — ask Claude." && echo "$MONTH" > "$F"
fi
