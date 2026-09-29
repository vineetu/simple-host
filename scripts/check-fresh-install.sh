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

run "event accounts" "SELECT display_name, handle, handle_changed_at, event_account FROM users"
run "sign-up source"        "SELECT signup_source, signup_agent, signup_method, signup_inferred FROM users"
run "hashed API keys"       "SELECT u.id FROM api_keys k JOIN users u ON u.id = k.user_id WHERE k.key_hash='x'"
run "named API keys"        "SELECT id, name, last4, created_at, last_used_at FROM api_keys WHERE user_id='$NIL' ORDER BY created_at DESC"
run "key scope and expiry"  "SELECT scope, expires_at, idle_from FROM api_keys WHERE key_hash='x'"
run "sites + versions"      "SELECT id, name, active_version, visibility FROM sites WHERE user_id='$NIL'"
run "per-site state"        "SELECT COALESCE(state,'null'::jsonb), state_version FROM sites WHERE name='x'"
run "private pages"         "SELECT view_password_hash FROM sites WHERE name='x'"
run "site passcode"         "SELECT passcode_enc IS NOT NULL, passcode_set_at, passcode_generation FROM sites WHERE id='$NIL'"
run "collections"           "SELECT id, data FROM collection_items WHERE site_id='$NIL' AND collection='c' ORDER BY id DESC"
run "private collections"   "SELECT s.private, i.submitted_by FROM collection_settings s LEFT JOIN collection_items i ON i.site_id = s.site_id AND i.collection = s.collection WHERE s.site_id='$NIL'"
run "saved-data history"    "SELECT h.id, h.item_id, h.op, h.prev, h.diff, h.actor_id, h.actor_kind, h.actor_email, i.deleted_at, i.submitted_email, s.legacy_data FROM data_history h LEFT JOIN collection_items i ON i.id = h.item_id LEFT JOIN sites s ON s.id = h.site_id WHERE h.site_id='$NIL' AND h.kind = 'state'"
run "idempotency keys"      "SELECT status, etag, ref, body_hash, site_id FROM idempotency_keys WHERE created_at < now()"
run "saved-data size"       "SELECT data_bytes, state_bytes FROM sites WHERE id='$NIL' UNION ALL SELECT count(*), 0 FROM pg_trigger WHERE tgname IN ('sites_state_bytes','collection_items_bytes_ins','collection_items_bytes_upd','collection_items_bytes_del')"
run "saved-data kinds"      "SELECT cs.kind, cs.one_per_person, cs.notify, cs.notify_sent_at, cs.declared_at, s.savers_mode, s.legacy_data, (SELECT count(*) FROM site_savers v WHERE v.site_id = s.id AND v.list = 'block') FROM sites s LEFT JOIN collection_settings cs ON cs.site_id = s.id WHERE s.id='$NIL'"
run "personal and boards"   "SELECT id, data, created_at, version FROM collection_items WHERE site_id='$NIL' AND collection='c' AND submitted_by IS NOT NULL ORDER BY id DESC LIMIT 1"
run "history size"          "SELECT history_bytes FROM sites WHERE history_bytes > 0 AND id='$NIL' UNION ALL SELECT count(*) FROM pg_trigger WHERE tgname IN ('data_history_bytes_ins','data_history_bytes_upd','data_history_bytes_del')"
run "saved-data watch"      "SELECT day, metric, count, last_at FROM data_watch WHERE site_id='$NIL'"
run "custom domains"        "SELECT custom_domain, domain_status FROM sites WHERE custom_domain='x'"
run "domain certificates"   "SELECT previous_domain, domain_cert_status, domain_failing_since, domain_lapse_notified_at FROM sites WHERE previous_domain='x'"
run "domain ownership"      "SELECT domain_token, custom_domain = ANY(domain_proof_exempt) FROM sites WHERE custom_domain='x'"
run "domain cert cap"       "SELECT count(DISTINCT domain) FROM domain_cert_requests WHERE user_id='$NIL' AND requested_at > now() - interval '24 hours'"
run "recently deleted"      "SELECT id, name, deleted_at FROM sites WHERE user_id='$NIL' AND deleted_at IS NOT NULL"
run "promised dates"        "SELECT purge_at, idle_remove_at, domain_release_at FROM sites WHERE user_id='$NIL'"
run "take-down"             "SELECT s.suspended_at, s.suspended_reason, u.suspended_at, u.suspended_reason FROM sites s JOIN users u ON u.id = s.user_id WHERE s.id='$NIL'"
run "migrations record"     "SELECT name, applied_at FROM schema_migrations"
run "ask daily count"       "INSERT INTO ask_daily (day, count) VALUES (CURRENT_DATE, 1) ON CONFLICT (day) DO UPDATE SET count = ask_daily.count + 1 WHERE ask_daily.count < 500 RETURNING count"
run "auth tokens"           "SELECT id, email, code, link_token, nonce_hash FROM auth_tokens WHERE link_token='x'"
run "visitor sessions"      "SELECT id, user_id, site_id, host FROM visitor_sessions WHERE id='x'"
run "oauth identities"      "SELECT provider, provider_user_id FROM oauth_identities WHERE provider_user_id='x'"
run "site analytics"        "SELECT class, SUM(views) FROM site_view_hourly WHERE site_id='$NIL' GROUP BY 1"
run "unique visitors"       "SELECT COUNT(DISTINCT ip_hash) FROM site_visitor_hourly WHERE site_id='$NIL'"
run "pre-classifier history" "SELECT day, views FROM site_view_daily WHERE site_id='$NIL'"
run "geo views"             "SELECT country, class, SUM(views) FROM site_geo_daily WHERE site_id='$NIL' GROUP BY 1,2"
run "top pages"             "SELECT path, SUM(views) FROM site_page_daily WHERE site_id='$NIL' GROUP BY path ORDER BY 2 DESC LIMIT 20"
run "top referrers"         "SELECT domain, SUM(views) FROM site_referrer_daily WHERE site_id='$NIL' GROUP BY domain ORDER BY 2 DESC LIMIT 20"
run "geo visitors"          "SELECT country, class, COUNT(DISTINCT ip_hash) FROM site_visitor_hourly WHERE site_id='$NIL' GROUP BY 1,2"
run "ip-country lookup"     "SELECT country FROM ip_country_ranges WHERE start_ip <= '8.8.8.8'::inet ORDER BY start_ip DESC LIMIT 1"
run "event domains"        "SELECT name, domain, ip, record_ids, expires_at FROM event_domains WHERE user_id='$NIL'"
run "ingest checkpoint"     "SELECT offset_bytes, inode FROM analytics_ingest_state WHERE logfile='x'"
run "admin api metrics"     "SELECT route, status, calls FROM api_request_daily WHERE day=current_date"
run "connector clients"   "SELECT client_id, client_secret_hash, redirect_uris, token_endpoint_auth_method FROM oauth_clients WHERE client_id='x'"
run "connector grants"    "SELECT g.id, g.user_id, g.client_id, c.client_name FROM oauth_grants g JOIN oauth_clients c USING (client_id) WHERE g.user_id='$NIL'"
run "connection device"   "SELECT (array_agg(g.device ORDER BY g.created_at DESC) FILTER (WHERE g.device IS NOT NULL))[1] FROM oauth_grants g WHERE g.user_id='$NIL'"
run "connector codes"     "SELECT client_id, user_id, redirect_uri, code_challenge, resource, grant_id FROM oauth_codes WHERE code_hash='x' AND used_at IS NULL"
run "connector tokens"    "SELECT t.kind, t.expires_at, g.user_id FROM oauth_tokens t JOIN oauth_grants g ON g.id = t.grant_id WHERE t.token_hash='x'"

# An existing box upgrades by running `simple-host migrate`. Prove, on its own
# throwaway database and role (the Go test connects over TCP with a password):
# schema.sql + migrate leaves nothing pending, a second run is a no-op, a box
# from before tracking existed gets every file once, two racing runs apply a
# file once, and a failing file rolls back. Skipped when go is not installed.
echo "== simple-host migrate on a fresh and an older database =="
if command -v go >/dev/null 2>&1; then
  MDB=${DB}_migrate
  MPW=$(openssl rand -hex 16)
  mcleanup() { $PSQL -c "DROP DATABASE IF EXISTS $MDB;" -c "DROP ROLE IF EXISTS $MDB;" >/dev/null 2>&1; }
  mcleanup
  if $PSQL -c "CREATE ROLE $MDB LOGIN PASSWORD '$MPW';" -c "CREATE DATABASE $MDB OWNER $MDB;" >/dev/null 2>&1; then
    if MIGRATE_TEST_DSN="postgres://$MDB:$MPW@127.0.0.1:${PGPORT:-5432}/$MDB?sslmode=disable" \
        go test -count=1 ./db/migrations/ >/tmp/sh-migrate-check.$$ 2>&1; then
      echo "  ok — migrate applies each file once, tracked, and is a no-op after"
    else
      echo "  FAIL migrate"; sed 's/^/    /' /tmp/sh-migrate-check.$$ | tail -20
      fail=1
    fi
    rm -f /tmp/sh-migrate-check.$$
  else
    echo "  FAIL: cannot create $MDB"; fail=1
  fi
  mcleanup
else
  echo "  skipped — go not installed"
fi

echo
if [ "$fail" -ne 0 ]; then
  echo "FRESH INSTALL BROKEN — db/schema.sql cannot run the product."
  exit 1
fi
echo "fresh install ok ✓"
