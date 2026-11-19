#!/usr/bin/env bash
# Fresh-install gate: prove the platform schema alone, then the composed Hack schema.
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

DB=${FRESH_DB:-sh_freshcheck_$$_$(openssl rand -hex 4)}
PSQL=${PSQL:-"sudo -u postgres psql"}
fail=0

# TEST_PG_URL selects this run's disposable server. Keep credentials out of logs.
psql_run() {
  if [ -n "${TEST_PG_URL:-}" ]; then
    local database=postgres
    local args=()
    while [ "$#" -gt 0 ]; do
      if [ "$1" = -d ]; then database=$2; shift 2; else args+=("$1"); shift; fi
    done
    psql "${TEST_PG_URL}/${database}?sslmode=disable" "${args[@]}"
  else
    $PSQL "$@"
  fi
}

cleanup() { psql_run -c "DROP DATABASE IF EXISTS $DB;" >/dev/null 2>&1; }
trap cleanup EXIT

echo "== applying host/db/schema.sql to a fresh database =="
cleanup
psql_run -c "CREATE DATABASE $DB;" >/dev/null 2>&1 || { echo "  FAIL: cannot create $DB"; exit 1; }

errs=$(psql_run -d "$DB" -f host/db/schema.sql 2>&1 | grep -cE "^psql.*ERROR" || true)
if [ "$errs" -ne 0 ]; then
  echo "  FAIL: schema.sql reported $errs error(s)"
  psql_run -d "$DB" -f host/db/schema.sql 2>&1 | grep -E "^psql.*ERROR" | head -10 | sed 's/^/    /'
  fail=1
else
  echo "  ok — schema applied cleanly"
fi

# One query per feature the schema has to support. Each mirrors a real query in
# host/db/. A missing column or table fails here instead of in production.
echo "== every feature's query shape runs =="
NIL=00000000-0000-0000-0000-000000000000
run() {
  local label=$1 sql=$2
  if psql_run -d "$DB" -c "$sql" >/dev/null 2>&1; then
    printf '  ok   %s\n' "$label"
  else
    printf '  FAIL %s\n' "$label"
    psql_run -d "$DB" -c "$sql" 2>&1 | grep -E "ERROR" | head -2 | sed 's/^/       /'
    fail=1
  fi
}

run "personal home and bio" "SELECT home_site_id, showcase_bio FROM users"
run "showcase pin and order" "SELECT showcase_pinned, showcase_order FROM sites"
run "event accounts" "SELECT display_name, handle, handle_changed_at, event_account FROM users"
run "teams"                 "SELECT m.team_id, m.user_id, m.added_by, m.added_at, u.handle FROM team_members m JOIN users u ON u.id = m.team_id WHERE u.is_team AND m.user_id='$NIL'"
run "team audit"            "SELECT team_id, team_handle, actor_id, action, subject_id, invitation_id, at FROM team_audit WHERE team_id='$NIL' ORDER BY at"
run "team invitations"      "SELECT id, team_id, invited_by, addressed_to, created_at, expires_at, state FROM team_invitations WHERE addressed_to='x' AND state='pending'"
run "sign-up source"        "SELECT signup_source, signup_agent, signup_method, signup_inferred FROM users"
run "hashed API keys"       "SELECT u.id FROM api_keys k JOIN users u ON u.id = k.user_id WHERE k.key_hash='x'"
run "named API keys"        "SELECT id, name, last4, created_at, last_used_at FROM api_keys WHERE user_id='$NIL' ORDER BY created_at DESC"
run "key scope and expiry"  "SELECT scope, expires_at, idle_from FROM api_keys WHERE key_hash='x'"
run "sites + versions"      "SELECT id, name, active_version, visibility FROM sites WHERE user_id='$NIL'"
run "private pages"         "SELECT view_password_hash FROM sites WHERE name='x'"
run "site passcode"         "SELECT passcode_enc IS NOT NULL, passcode_set_at, passcode_generation FROM sites WHERE id='$NIL'"
run "visitor AI last site" "SELECT last_visitor_site FROM oauth_grants"
run "visitor AI daily cap" "SELECT user_id, site_id, day, writes FROM visitor_ai_daily"
run "custom domains"        "SELECT custom_domain, domain_status FROM sites WHERE custom_domain='x'"
run "domain certificates"   "SELECT previous_domain, domain_cert_status, domain_failing_since, domain_lapse_notified_at FROM sites WHERE previous_domain='x'"
run "domain ownership"      "SELECT domain_token, custom_domain = ANY(domain_proof_exempt) FROM sites WHERE custom_domain='x'"
run "domain cert cap"       "SELECT count(DISTINCT domain) FROM domain_cert_requests WHERE user_id='$NIL' AND requested_at > now() - interval '24 hours'"
run "address families"      "SELECT f.id, f.suffix, f.site_prefix, f.rank, f.canonical, f.token, f.status, f.verified_at, f.checked_at, f.failing_since, f.release_at, f.cert_mode, f.cert_name, f.proof_exempt, u.handle FROM address_families f JOIN users u ON u.id = f.user_id WHERE f.user_id='$NIL' AND f.verified_at IS NOT NULL"
run "family cert cap"       "SELECT count(DISTINCT host) FROM family_cert_requests WHERE user_id='$NIL' AND requested_at > now() - interval '24 hours'"
run "recently deleted"      "SELECT id, name, deleted_at FROM sites WHERE user_id='$NIL' AND deleted_at IS NOT NULL"
run "promised dates"        "SELECT purge_at, idle_remove_at, domain_release_at FROM sites WHERE user_id='$NIL'"
run "take-down"             "SELECT s.suspended_at, s.suspended_reason, u.suspended_at, u.suspended_reason FROM sites s JOIN users u ON u.id = s.user_id WHERE s.id='$NIL'"
run "migrations record"     "SELECT name, applied_at FROM schema_migrations"
run "auth tokens"           "SELECT id, email, code, link_token, nonce_hash FROM auth_tokens WHERE link_token='x'"
run "visitor sessions"      "SELECT id, user_id, site_id, host FROM visitor_sessions WHERE id='x'"
run "oauth identities"      "SELECT provider, provider_user_id FROM oauth_identities WHERE provider_user_id='x'"
run "site analytics"        "SELECT class, SUM(views) FROM site_view_hourly WHERE site_id='$NIL' GROUP BY 1"
run "unique visitors"       "SELECT COUNT(DISTINCT ip_hash) FROM site_visitor_hourly WHERE site_id='$NIL'"
run "owner visit marks"     "SELECT user_id, ip_hash, last_seen FROM owner_visitor_marks WHERE last_seen >= now() - interval '30 days'"
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
run "storage presets"     "SELECT name, kind, read_policy, write_policy, site_passcode, write_mode, acc_read FROM site_storage_resources WHERE site_id = '00000000-0000-0000-0000-000000000000'"
run "storage table rules" "SELECT table_name, rules FROM site_storage_tables WHERE site_id = '00000000-0000-0000-0000-000000000000'"
# The platform half has now passed alone. Hack adds its own objects afterwards.
echo "== applying the Hack schema after the platform schema =="
if ! psql_run -d "$DB" -v ON_ERROR_STOP=1 -f products/hack/db/schema.sql >/dev/null; then
  echo "  FAIL: Hack schema"; fail=1
fi
run "team kept-after-leave marker" "SELECT kept_after_leave FROM event_teams WHERE id = '$NIL'"

run "Hack paper event snapshot" "SELECT snapshot_day,event_key,run_id,synthetic,stage,created_week,duration_hours,expected_participants,participants,organisers,judges,applications,applications_approved,teams,teams_with_entry,teams_kept_after_leave,team_size_p50,team_size_max,team_sites,team_site_bytes_sum,team_site_bytes_p50,team_site_versions_sum,event_storage_bytes,submissions,entries_with_code_url,entries_with_video,scores,score_criteria,assignments,conflicts,votes,announcements,results_published,deploys_total,minutes_create_to_first_deploy FROM paper_hack_event"
run "Hack paper activity slots" "SELECT run_id,slot,slot_secs,event_key,synthetic,requests,writes,data_writes,deploys,active_members,inflight_max,peak_rps_1s,arrivals_1s,writes_1s FROM paper_hack_minute"
run "Hack paper histogram cells" "SELECT run_id,slot,event_key,synthetic,class,status_group,n,sum_us,max_us,bytes_out,idx_offset,counts,arrivals_1s,writes_1s FROM paper_hack_latency"
run "Hack paper phases" "SELECT run_id,event_key,synthetic,stage,phase_slot,minute_offset,origin_slot,observed,baseline,recorded_at,origin_change FROM paper_hack_phase"
run "Paper provenance" "SELECT run_id,started_at,role,vcs_revision,vcs_modified,go_version,binary_sha256,config_hash,metric_def_version,bucket_scheme,gomaxprocs,num_cpu,cgroup_memory_max,cgroup_cpu_max FROM paper_run"
run "Private Hack key lookup" "SELECT event_id,event_key,synthetic,run_id,first_open_at FROM paper_hack_key"
run "Hack daily provenance" "SELECT run_id,day,metric,value FROM paper_hack_daily"
run "Hack checklist dismissals" "SELECT event_id,item_id,marked_by,marked_at FROM event_checklist_dismissals"
run "Hack checklist acknowledgements" "SELECT event_id,subject_id,item_id,version,acknowledged_by,acknowledged_at FROM event_checklist_acknowledgements"
run "Hack wait notices" "SELECT n.id,n.kind,n.seen_at,n.email_state,COALESCE(p.email_updates,true) FROM event_notices n LEFT JOIN hack_account_preferences p ON p.user_id=n.user_id WHERE n.email_state='pending'"

# An existing box upgrades by running `simple-host migrate`. Prove, on its own
# throwaway database and role (the Go test connects over TCP with a password):
# schema.sql + migrate leaves nothing pending, a second run is a no-op, a box
# from before tracking existed gets every file once, two racing runs apply a
# file once, and a failing file rolls back. Skipped when go is not installed.
echo "== simple-host migrate on a fresh and an older database =="
if command -v go >/dev/null 2>&1; then
  for package in ./host/db/migrations ./products/hack/db/migrations; do
    MDB=${DB}_migrate
    MDB_old=${MDB}_old
    MDB_new=${MDB}_new
    MPW=$(openssl rand -hex 16)
    mcleanup() {
      psql_run -c "DROP DATABASE IF EXISTS $MDB_old;" -c "DROP DATABASE IF EXISTS $MDB_new;" -c "DROP DATABASE IF EXISTS $MDB;" >/dev/null 2>&1
      if [ -z "${TEST_PG_URL:-}" ]; then psql_run -c "DROP ROLE IF EXISTS $MDB;" >/dev/null 2>&1; fi
    }
    mcleanup
    owner=""
    if [ -n "${TEST_PG_URL:-}" ]; then
      migrate_base=$TEST_PG_URL
    else
      psql_run -c "CREATE ROLE $MDB LOGIN PASSWORD '$MPW';" >/dev/null 2>&1 || { fail=1; break; }
      owner=" OWNER $MDB"
      migrate_base="postgres://$MDB:$MPW@127.0.0.1:${PGPORT:-5432}"
    fi
    if psql_run -c "CREATE DATABASE $MDB$owner;" -c "CREATE DATABASE $MDB_old$owner;" -c "CREATE DATABASE $MDB_new$owner;" >/dev/null 2>&1; then
      if DB_DSN= TESTDB_ISOLATE= MIGRATE_TEST_DSN="$migrate_base/$MDB?sslmode=disable" \
          MIGRATE_UPGRADE_OLD_DSN="$migrate_base/$MDB_old?sslmode=disable" \
          MIGRATE_UPGRADE_NEW_DSN="$migrate_base/$MDB_new?sslmode=disable" \
          go test -count=1 "$package" >/tmp/sh-migrate-check.$$ 2>&1; then
        echo "  ok — $package applies each file once and is a no-op after"
      else
        echo "  FAIL migrate: $package"; sed 's/^/    /' /tmp/sh-migrate-check.$$ | tail -30
        fail=1
      fi
      rm -f /tmp/sh-migrate-check.$$
    else
      echo "  FAIL: cannot create $MDB"; fail=1
    fi
    mcleanup
  done
else
  echo "  skipped — go not installed"
fi

echo
if [ "$fail" -ne 0 ]; then
  echo "FRESH INSTALL BROKEN — host/db/schema.sql cannot run the product."
  exit 1
fi
echo "fresh install ok ✓"
