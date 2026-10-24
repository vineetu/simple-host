#!/usr/bin/env bash
# Install: sudo install -m 755 deploy/prod/sh-uptime-watch.sh /usr/local/bin/sh-uptime-watch && sudo install -m 644 deploy/prod/sh-uptime-watch.service deploy/prod/sh-uptime-watch.timer /etc/systemd/system/ && sudo systemctl daemon-reload && sudo systemctl enable --now sh-uptime-watch.timer
# --test prints health checks without sending or changing alert state.
# SIGNAL_ACCOUNT / SIGNAL_TO and optional SIGNAL_RPC use the network watch convention.
set -u
WATCH_ENV=${WATCH_ENV:-/etc/sh-uptime-watch.env}
STATE_DIR=${STATE_DIR:-/var/lib/sh-uptime-watch}
SIGNAL_RPC=${SIGNAL_RPC:-http://127.0.0.1:8086/api/v1/rpc}
[ -r "$WATCH_ENV" ] && . "$WATCH_ENV"
send() {
  if [ -z "${SIGNAL_ACCOUNT:-}" ] || [ -z "${SIGNAL_TO:-}" ]; then
    echo "sh-uptime-watch: SIGNAL_ACCOUNT and SIGNAL_TO are not set in $WATCH_ENV" >&2
    return 1
  fi
  curl -fsS -m 20 -X POST "$SIGNAL_RPC" -H 'Content-Type: application/json' -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"send\",\"params\":{\"account\":\"$SIGNAL_ACCOUNT\",\"recipient\":[\"$SIGNAL_TO\"],\"message\":$(python3 -c 'import json,sys;print(json.dumps(sys.argv[1]))' "$1")}}" >/dev/null
}
for site in simple-host.app simple-hack.app; do
  # Retry any failed check once, including non-2xx responses. Both attempts
  # are bounded by five seconds; two failed timer runs trigger the alert.
  status=''; error=''
  for attempt in 1 2; do
    if result=$(curl -fsS -m 5 -o /dev/null -w '%{http_code}' "https://$site/healthz" 2>&1); then
      status=$result; error=''; break
    else
      status=failed; error=$result
    fi
  done
  if [ "${1:-}" = --test ]; then echo "$site: $status ${error}"; continue; fi
  mkdir -p "$STATE_DIR"
  F="$STATE_DIR/$site"
  failures=0; alerted=0
  if [ -r "$F" ]; then read -r failures alerted < "$F"; fi
  if [ "$status" != failed ]; then
    failures=0
    if [ "$alerted" = 1 ]; then
      if send "$site is back up (HTTP $status)."; then alerted=0; fi
    fi
  else
    failures=$((failures + 1))
    if [ "$failures" -ge 2 ] && [ "$alerted" = 0 ]; then
      if send "$site is down: $failures consecutive failed checks. Last HTTP status/error: $error"; then alerted=1; fi
    fi
  fi
  printf '%s %s\n' "$failures" "$alerted" > "$F"
done
