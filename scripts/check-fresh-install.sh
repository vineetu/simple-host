#!/usr/bin/env bash
# Fresh-install gate: prove db/schema.sql alone can run the product.
#
# This exists because it did not. schema.sql was missing collection_items,
# sites.state, sites.state_version, sites.view_password_hash and the three
# api_* tables, so a clean install had no per-site backend at all — the
# feature the README leads with. Nothing caught it: the code compiled, the
# tests passed, and production worked because its columns had been added by
# hand-applied migrations years of commits earlier.
#
# Applies the schema to a throwaway database and runs one query of every shape
# the server actually issues. Requires a local postgres superuser.
#
#   bash scripts/check-fresh-install.sh
set -u
cd "$(dirname "$0")/.."

DB=${FRESH_DB:-sh_freshcheck}
PSQL=${PSQL:-"sudo -u postgres psql"}
fail=0

cleanup() { $PSQL -c "DROP DATABASE IF EXISTS $DB;" >/dev/null 2>&1; }
trap cleanup EXIT

echo "== applying db/schema.sql to a fresh database =="
cleanup
$PSQL -c "CREATE DATABASE $DB;" >/dev/null 2>&1 || { echo "  FAIL: cannot create $DB"; exit 1; }

errs=$($PSQL -d "$DB" -f db/schema.sql 2>&1 | grep -cE "^psql.*ERROR" || true)
if [ "$errs" -ne 0 ]; then
  echo "  FAIL: schema.sql reported $errs error(s)"
  $PSQL -d "$DB" -f db/schema.sql 2>&1 | grep -E "^psql.*ERROR" | head -10 | sed 's/^/    /'
  fail=1
else
  echo "  ok — schema applied cleanly"
fi

# One query per feature the schema has to support. Each mirrors a real query in
# internal/db/. A missing column or table fails here instead of in production.
echo "== every feature's query shape runs =="
NIL=00000000-0000-0000-0000-000000000000
run() {
  local label=$1 sql=$2
  if $PSQL -d "$DB" -c "$sql" >/dev/null 2>&1; then
    printf '  ok   %s\n' "$label"
  else
    printf '  FAIL %s\n' "$label"
    $PSQL -d "$DB" -c "$sql" 2>&1 | grep -E "ERROR" | head -2 | sed 's/^/       /'
    fail=1
  fi
}

run "sites + versions"      "SELECT id, name, active_version, visibility FROM sites WHERE user_id='$NIL'"
run "per-site state"        "SELECT COALESCE(state,'null'::jsonb), state_version FROM sites WHERE name='x'"
run "private pages"         "SELECT view_password_hash FROM sites WHERE name='x'"
run "collections"           "SELECT id, data FROM collection_items WHERE site_id='$NIL' AND collection='c' ORDER BY id DESC"
run "custom domains"        "SELECT custom_domain, domain_status FROM sites WHERE custom_domain='x'"
run "auth tokens"           "SELECT id, email, code, link_token FROM auth_tokens WHERE link_token='x'"
run "visitor sessions"      "SELECT id, user_id, site_id, host FROM visitor_sessions WHERE id='x'"
run "oauth identities"      "SELECT provider, provider_user_id FROM oauth_identities WHERE provider_user_id='x'"
run "site analytics"        "SELECT class, SUM(views) FROM site_view_hourly WHERE site_id='$NIL' GROUP BY 1"
run "unique visitors"       "SELECT COUNT(DISTINCT ip_hash) FROM site_visitor_hourly WHERE site_id='$NIL'"
run "pre-classifier history" "SELECT day, views FROM site_view_daily WHERE site_id='$NIL'"
run "geo views"             "SELECT country, class, SUM(views) FROM site_geo_daily WHERE site_id='$NIL' GROUP BY 1,2"
run "geo visitors"          "SELECT country, class, COUNT(DISTINCT ip_hash) FROM site_visitor_hourly WHERE site_id='$NIL' GROUP BY 1,2"
run "ip-country lookup"     "SELECT country FROM ip_country_ranges WHERE start_ip <= '8.8.8.8'::inet ORDER BY start_ip DESC LIMIT 1"
run "ingest checkpoint"     "SELECT offset_bytes, inode FROM analytics_ingest_state WHERE logfile='x'"
run "admin api metrics"     "SELECT route, status, calls FROM api_request_daily WHERE day=current_date"
run "admin caller geo"      "SELECT ip, country FROM ip_geo WHERE ip='127.0.0.1'"

echo
if [ "$fail" -ne 0 ]; then
  echo "FRESH INSTALL BROKEN — db/schema.sql cannot run the product."
  exit 1
fi
echo "fresh install ok ✓"
