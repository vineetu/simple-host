#!/usr/bin/env bash
# Fake health responses and Signal endpoint; run by make check.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
S="$HERE/sh-uptime-watch.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
mkdir -p "$T/bin"
cat > "$T/bin/curl" <<'STUB'
#!/usr/bin/env bash
case "$*" in
  *'https://simple-host.app/healthz'*)
    if [ "$HEALTH" = down ]; then echo 'curl: timeout; HTTP 000' >&2; exit 28; fi
    echo 200 ;;
  *'https://simple-hack.app/healthz'*) echo 200 ;;
  *) printf '%s\n' "$*" >> "$SENT" ;;
esac
STUB
chmod +x "$T/bin/curl"
printf 'SIGNAL_ACCOUNT=+10000000000\nSIGNAL_TO=+10000000001\n' > "$T/watch.env"
export PATH="$T/bin:$PATH" WATCH_ENV="$T/watch.env" STATE_DIR="$T/state" SENT="$T/sent"
sent() { [ -f "$T/sent" ] && wc -l < "$T/sent" | tr -d ' ' || echo 0; }
run() { HEALTH="$1" bash "$S"; }
run up
[ "$(sent)" = 0 ] || fail 'healthy alert'
run down
[ "$(sent)" = 0 ] || fail 'first failure alerted'
run up
run down
[ "$(sent)" = 0 ] || fail 'healthy did not reset count'
run down
[ "$(sent)" = 1 ] || fail 'second failure did not alert'
grep -q 'simple-host.app is down' "$SENT" || fail 'wrong site/message'
grep -q 'timeout; HTTP 000' "$SENT" || fail 'missing status/error'
run down
[ "$(sent)" = 1 ] || fail 'repeat outage alert'
HEALTH=up bash "$S" --test
[ "$(sent)" = 1 ] || fail '--test sent'
grep -q '3 1' "$STATE_DIR/simple-host.app" || fail '--test changed state'
run up
[ "$(sent)" = 2 ] || fail 'no recovery'
grep -q 'simple-host.app is back up' "$SENT" || fail 'missing recovery message'
run up
[ "$(sent)" = 2 ] || fail 'repeat recovery'
echo 'sh-uptime-watch: ok'
