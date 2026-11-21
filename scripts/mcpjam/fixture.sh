#!/usr/bin/env bash
# Start a throwaway server for the MCPJam checks: the server on
# 127.0.0.1:18190 and a TLS front on https://localhost:18443 by default (MCP OAuth needs
# https resource URLs). Writes $FIX/user.key (a test account's API key),
# $FIX/friend.key (a second account, handle mcpjam-friend) and $FIX/cert.pem.
# Needs DB_DSN (an empty database).
#
# PRODUCT selects the program: host (Simple Host, the default) or hack
# (Simple Hack). BIN is its binary; without BIN it is built into $FIX.
# For hack, user.key is the organiser of one seeded event, and seed.py also
# writes participant.key (a participant with a team), judge.key, newcomer.key
# (in no event yet), event.slug and join.code.
# PORT and TLS_PORT select independent ports for parallel worktrees.
set -euo pipefail
: "${DB_DSN:?}"
PRODUCT=${PRODUCT:-host}
case "$PRODUCT" in host | hack) ;; *) echo "PRODUCT must be host or hack" >&2; exit 1 ;; esac
FIX=${FIX:-/tmp/mcpjam-fixture}
FIXTURE_PORT=${FIXTURE_PORT:-${PORT:-18190}}
TLS_PORT=${TLS_PORT:-18443}
here=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$FIX/data"
if [ -z "${BIN:-}" ]; then
  BIN="$FIX/simple-$PRODUCT"
  (cd "$here/../.." && CGO_ENABLED=0 go build -o "$BIN" "./cmd/simple-$PRODUCT")
fi
echo "$PRODUCT" >"$FIX/product"
"$BIN" migrate -init >"$FIX/migrate.log" 2>&1
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$FIX/key.pem" -out "$FIX/cert.pem" -days 2 \
  -subj /CN=localhost -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" 2>/dev/null
admin=$(openssl rand -hex 32)
# Team sites live at their own addresses, as on simple-hack.app; without a
# certificate directory every event's addresses count as ready.
hosts=off
[ "$PRODUCT" = hack ] && hosts=serve
# Each OAuth run registers a client or three; the default allows 10 per 6m.
# Every MCP Apps screen is on, so MCP Apps conformance has them to check.
# Turnstile's always-pass test keys turn on the spam check, as on simple-host.app.
MCP_APP_MY_SITES=${MCP_APP_MY_SITES:-on} MCP_APP_DASHBOARD=${MCP_APP_DASHBOARD:-on} MCP_APP_SITE_PREVIEW=${MCP_APP_SITE_PREVIEW:-on} MCP_APP_PHOTO_PICKER=${MCP_APP_PHOTO_PICKER:-on} MCP_APP_PASSCODE=${MCP_APP_PASSCODE:-on} \
  MCP_APP_HACK_JUDGE=${MCP_APP_HACK_JUDGE:-on} MCP_APP_HACK_ORGANISER=${MCP_APP_HACK_ORGANISER:-on} \
  TURNSTILE_SITE_KEY=${TURNSTILE_SITE_KEY:-1x00000000000000000000AA} TURNSTILE_SECRET=${TURNSTILE_SECRET:-1x0000000000000000000000000000000AA} \
  PERSON_HOSTS=${PERSON_HOSTS:-$hosts} SITE_HOSTS=${SITE_HOSTS:-$hosts} RATE_LIMIT_OAUTH_REGISTER=40,90s PASSCODE_ENC_KEY=$(openssl rand -base64 32) DATA_DIR="$FIX/data" SITE_DOMAIN=${SITE_DOMAIN:-localhost} PUBLIC_BASE_URL="https://localhost:$TLS_PORT" ADMIN_API_KEY="$admin" \
  PORT="$FIXTURE_PORT" BIND_ADDR=127.0.0.1 nohup "$BIN" >"$FIX/server.log" 2>&1 </dev/null &
echo $! >"$FIX/server.pid"
TLS_PORT="$TLS_PORT" TARGET="http://127.0.0.1:$FIXTURE_PORT" TLS_KEY="$FIX/key.pem" TLS_CERT="$FIX/cert.pem" nohup node "$here/tls-proxy.mjs" >"$FIX/proxy.log" 2>&1 </dev/null &
echo $! >"$FIX/proxy.pid"
for _ in $(seq 60); do
  curl -sfk -o /dev/null "https://localhost:$TLS_PORT/healthz" && break
  sleep 1
done
curl -sfk -o /dev/null "https://localhost:$TLS_PORT/healthz" || { tail -20 "$FIX/server.log"; exit 1; }
account() { # account <email> <key file>
  curl -sf -X POST "http://127.0.0.1:$FIXTURE_PORT/v1/admin/users" -H "X-API-Key: $admin" -H 'Content-Type: application/json' \
    -d "{\"emails\":[\"$1\"]}" |
    python3 -c 'import json,sys;print(json.load(sys.stdin)["created"][0]["api_key"],end="")' >"$2"
  chmod 600 "$2"
}
# A copy of a seeded $FIX and its database (scripts/mcpeval) keeps its accounts.
if [ ! -s "$FIX/user.key" ]; then
  account mcpjam-ci@example.com "$FIX/user.key"
  account mcpjam-friend@example.com "$FIX/friend.key"
  if [ "$PRODUCT" = hack ]; then
    account mcpjam-participant@example.com "$FIX/participant.key"
    account mcpjam-judge@example.com "$FIX/judge.key"
    account mcpjam-newcomer@example.com "$FIX/newcomer.key"
    FIXTURE_PORT="$FIXTURE_PORT" python3 "$here/seed.py" hack "$FIX"
  fi
fi
echo "fixture up ($PRODUCT): https://localhost:$TLS_PORT/mcp"
