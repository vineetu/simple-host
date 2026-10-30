#!/usr/bin/env bash
# Start a throwaway Simple Host for the MCPJam checks: the server on
# 127.0.0.1:18190 and a TLS front on https://localhost:18443 by default (MCP OAuth needs
# https resource URLs). Writes $FIX/user.key (a test account's API key) and
# $FIX/cert.pem. Needs DB_DSN (an empty database) and BIN (the server binary).
# PORT and TLS_PORT select independent ports for parallel worktrees.
set -euo pipefail
: "${DB_DSN:?}" "${BIN:?}"
FIX=${FIX:-/tmp/mcpjam-fixture}
PSQL=${PSQL:-psql}
FIXTURE_PORT=${FIXTURE_PORT:-${PORT:-18190}}
TLS_PORT=${TLS_PORT:-18443}
here=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$FIX/data"
PGOPTIONS=--client-min-messages=warning $PSQL -q -v ON_ERROR_STOP=1 "$DB_DSN" <"$here/../../db/schema.sql" >/dev/null
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$FIX/key.pem" -out "$FIX/cert.pem" -days 2 \
  -subj /CN=localhost -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" 2>/dev/null
admin=$(openssl rand -hex 32)
# Each OAuth run registers a client or three; the default allows 10 per 6m.
MCP_APP_MY_SITES=${MCP_APP_MY_SITES:-on} MCP_APP_DASHBOARD=${MCP_APP_DASHBOARD:-on} MCP_APP_SITE_PREVIEW=${MCP_APP_SITE_PREVIEW:-on} MCP_APP_PHOTO_PICKER=${MCP_APP_PHOTO_PICKER:-on} MCP_APP_PASSCODE=${MCP_APP_PASSCODE:-on} RATE_LIMIT_OAUTH_REGISTER=40,90s PASSCODE_ENC_KEY=$(openssl rand -base64 32) DATA_DIR="$FIX/data" SITE_DOMAIN=${SITE_DOMAIN:-localhost} PUBLIC_BASE_URL="https://localhost:$TLS_PORT" ADMIN_API_KEY="$admin" \
  PORT="$FIXTURE_PORT" BIND_ADDR=127.0.0.1 nohup "$BIN" >"$FIX/server.log" 2>&1 </dev/null &
TLS_PORT="$TLS_PORT" TARGET="http://127.0.0.1:$FIXTURE_PORT" TLS_KEY="$FIX/key.pem" TLS_CERT="$FIX/cert.pem" nohup node "$here/tls-proxy.mjs" >"$FIX/proxy.log" 2>&1 </dev/null &
for _ in $(seq 60); do
  curl -sfk -o /dev/null "https://localhost:$TLS_PORT/healthz" && break
  sleep 1
done
curl -sfk -o /dev/null "https://localhost:$TLS_PORT/healthz" || { tail -20 "$FIX/server.log"; exit 1; }
curl -sf -X POST "http://127.0.0.1:$FIXTURE_PORT/v1/admin/users" -H "X-API-Key: $admin" -H 'Content-Type: application/json' \
  -d '{"emails":["mcpjam-ci@example.com"]}' |
  python3 -c 'import json,sys;print(json.load(sys.stdin)["created"][0]["api_key"],end="")' >"$FIX/user.key"
chmod 600 "$FIX/user.key"
echo "fixture up: https://localhost:$TLS_PORT/mcp"
