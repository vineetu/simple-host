#!/usr/bin/env bash
# End-to-end check of the admin page at phone widths (see
# scripts/e2e-admin-layout.mjs for what it asserts): builds a server, seeds a
# throwaway admin account, a few sites and a many-version site directly in
# the database (no email provider needed), then runs the Playwright checks
# in Chromium and WebKit.
#
#   DB_DSN=postgres://...fresh database with db/schema.sql applied... \
#     bash scripts/e2e-admin-layout.sh
#
# BIN defaults to a fresh build. Needs psql and Playwright
# (PLAYWRIGHT_DIR=/opt/pw/node_modules, or installed another way).
set -euo pipefail
: "${DB_DSN:?DB_DSN must name a throwaway database with db/schema.sql applied}"
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
PORT=${PORT:-18199}
ADMIN=$(openssl rand -hex 32)
fail() { echo "FAIL: $*" >&2; exit 1; }
cleanup() { [ -n "${SRV:-}" ] && kill "$SRV" 2>/dev/null || true; rm -rf "$WORK"; }
trap cleanup EXIT

BIN=${BIN:-}
if [ -z "$BIN" ]; then
  (cd "$ROOT" && CGO_ENABLED=0 go build -o "$WORK/simple-host" ./cmd/server)
  BIN=$WORK/simple-host
fi
mkdir -p "$WORK/sites" "$WORK/certs/ready" "$WORK/certs/requests" "$WORK/fam/ready" "$WORK/fam/requests" "$WORK/fam/failed"

DB_DSN="$DB_DSN" DATA_DIR="$WORK/sites" SITE_DOMAIN=simple-host.test ADMIN_API_KEY="$ADMIN" PORT="$PORT" BIND_ADDR=127.0.0.1 \
  PERSON_HOSTS=canonical SITE_HOSTS=canonical SITE_CERT_DIR="$WORK/certs" ADDRESS_FAMILY_CERT_DIR="$WORK/fam" \
  "$BIN" >"$WORK/server.log" 2>&1 &
SRV=$!
for _ in $(seq 1 50); do curl -s -o /dev/null "http://127.0.0.1:$PORT/healthz" && break; sleep 0.2; done
curl -s -o /dev/null "http://127.0.0.1:$PORT/healthz" || { cat "$WORK/server.log" >&2; fail "server did not start"; }

psql "$DB_DSN" -qAt -v ON_ERROR_STOP=1 <<'SQL'
INSERT INTO users (username, handle, display_name, signup_source) VALUES
  ('e2e-owner', 'e2e-owner', 'E2E Owner', 'website'),
  ('e2e-second', 'e2e-second-account-name', 'E2E Second', 'website')
ON CONFLICT DO NOTHING;

WITH u AS (SELECT id FROM users WHERE handle = 'e2e-owner')
INSERT INTO sites (user_id, name, site_url, visibility, active_version, created_at, updated_at)
SELECT u.id, v.name, 'https://'||v.name||'.e2e-owner.simple-host.test/', 'public', 1, now() - interval '3 days', now() - interval '1 hour'
FROM u, (VALUES ('e2e-travel-demo'), ('e2e-survey-results')) AS v(name)
ON CONFLICT DO NOTHING;

WITH u AS (SELECT id FROM users WHERE handle = 'e2e-second-account-name')
INSERT INTO sites (user_id, name, site_url, visibility, active_version, created_at, updated_at)
SELECT u.id, 'e2e-second-site', 'https://e2e-second-site.e2e-second-account-name.simple-host.test/', 'public', 1, now() - interval '1 day', now()
FROM u
ON CONFLICT DO NOTHING;

WITH s AS (SELECT id FROM sites WHERE name = 'e2e-travel-demo')
INSERT INTO versions (site_id, version_number, disk_path, status, created_at)
SELECT s.id, n, 'by-id/e2e/e2e-travel-demo/v'||n, 'ready', now() - ((13 - n) || ' hours')::interval
FROM s, generate_series(1, 13) AS n
ON CONFLICT DO NOTHING;
UPDATE sites SET active_version = 13 WHERE name = 'e2e-travel-demo';

INSERT INTO versions (site_id, version_number, disk_path, status, created_at)
SELECT s.id, 1, 'by-id/e2e/'||s.name||'/v1', 'ready', s.created_at
FROM sites s WHERE s.name <> 'e2e-travel-demo'
ON CONFLICT DO NOTHING;
SQL

PLAYWRIGHT_DIR=${PLAYWRIGHT_DIR:-/opt/pw/node_modules} node "$ROOT/scripts/e2e-admin-layout.mjs" "http://127.0.0.1:$PORT" "$ADMIN"
