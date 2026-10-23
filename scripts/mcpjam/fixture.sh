#!/usr/bin/env bash
# Start a throwaway Simple Host for the MCPJam checks: the server on
# 127.0.0.1:18190 and a TLS front on https://localhost:18443 (MCP OAuth needs
# https resource URLs). Writes $FIX/user.key (a test account's API key) and
# $FIX/cert.pem. Needs DB_DSN (an empty database) and BIN (the server binary).
set -euo pipefail
: "${DB_DSN:?}" "${BIN:?}"
FIX=${FIX:-/tmp/mcpjam-fixture}
PSQL=${PSQL:-psql}
here=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$FIX/data"
PGOPTIONS=--client-min-messages=warning $PSQL -q -v ON_ERROR_STOP=1 "$DB_DSN" <"$here/../../db/schema.sql" >/dev/null
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$FIX/key.pem" -out "$FIX/cert.pem" -days 2 \
  -subj /CN=localhost -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" 2>/dev/null
admin=$(openssl rand -hex 32)
# Each OAuth run registers a client or three; the default allows 10 per 6m.
RATE_LIMIT_OAUTH_REGISTER=40,90s DATA_DIR="$FIX/data" SITE_DOMAIN=localhost PUBLIC_BASE_URL=https://localhost:18443 ADMIN_API_KEY="$admin" \
  PORT=18190 BIND_ADDR=127.0.0.1 nohup "$BIN" >"$FIX/server.log" 2>&1 </dev/null &
TLS_KEY="$FIX/key.pem" TLS_CERT="$FIX/cert.pem" nohup node "$here/tls-proxy.mjs" >"$FIX/proxy.log" 2>&1 </dev/null &
for _ in $(seq 60); do
  curl -sfk -o /dev/null https://localhost:18443/healthz && break
  sleep 1
done
curl -sfk -o /dev/null https://localhost:18443/healthz || { tail -20 "$FIX/server.log"; exit 1; }
curl -sf -X POST http://127.0.0.1:18190/v1/admin/users -H "X-API-Key: $admin" -H 'Content-Type: application/json' \
  -d '{"emails":["mcpjam-ci@example.com"]}' |
  python3 -c 'import json,sys;print(json.load(sys.stdin)["created"][0]["api_key"],end="")' >"$FIX/user.key"
chmod 600 "$FIX/user.key"
echo "fixture up: https://localhost:18443/mcp"
