#!/usr/bin/env bash
# Only creates/drops uniquely named rehearsal databases and this run's container.
set -euo pipefail
cd "$(dirname "$0")/../../.."
container_id=""
dbname=""
db_created=0
admin_url=""

cleanup() {
  local status=$?
  trap - EXIT
  if [ "$db_created" -eq 1 ]; then
    if ! psql "$admin_url" -X -v ON_ERROR_STOP=1 -c "DROP DATABASE \"$dbname\";" >/dev/null 2>&1; then
      echo "FAIL: could not clean up database $dbname" >&2
      status=1
    fi
  fi
  if [ -n "$container_id" ]; then
    if ! docker rm -fv "$container_id" >/dev/null 2>&1; then
      echo "FAIL: could not clean up container $container_id" >&2
      status=1
    fi
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

command -v psql >/dev/null || { echo 'FAIL: psql is required' >&2; exit 1; }
if [ -n "${TEST_PG_URL:-}" ]; then
  rest="${TEST_PG_URL#*://}"
  rest="${rest#*@}"
  if [[ ! "$TEST_PG_URL" =~ ^postgres(ql)?:// || "$rest" == *'/'* || "$rest" == *'?'* || "$rest" == *'#'* ]]; then
    echo 'FAIL: TEST_PG_URL must be a bare postgres server URL, without path or query' >&2
    exit 1
  fi
  base="$TEST_PG_URL"
else
  command -v docker >/dev/null || { echo 'FAIL: set TEST_PG_URL or install Docker' >&2; exit 1; }
  container_name="sh-seed-$(openssl rand -hex 8)"
  pgpass=$(openssl rand -hex 16)
  # Separate create/start so a failed start still leaves an owned ID for cleanup.
  container_id=$(docker create --name "$container_name" -e POSTGRES_PASSWORD="$pgpass" \
    -p 127.0.0.1::5432 postgres:16-alpine)
  docker start "$container_id" >/dev/null
  mapping=$(docker port "$container_id" 5432/tcp)
  port="${mapping##*:}"
  [[ "$port" =~ ^[0-9]+$ ]] || { echo 'FAIL: no mapped Postgres port' >&2; exit 1; }
  base="postgres://postgres:${pgpass}@127.0.0.1:${port}"
  ready=0
  for ((attempt=0; attempt<60; attempt++)); do
    if psql "${base}/postgres?sslmode=disable" -X -v ON_ERROR_STOP=1 -Atc 'SELECT 1' >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 1
  done
  [ "$ready" -eq 1 ] || { echo 'FAIL: throwaway Postgres did not become ready' >&2; exit 1; }
fi
admin_url="${base}/postgres?sslmode=disable"

for dump in simple-host.schema.sql simple-hack.schema.sql; do
  dbname="sh_seed_$$_$(openssl rand -hex 8)"
  target_url="${base}/${dbname}?sslmode=disable"
  psql "$admin_url" -X -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"$dbname\";" >/dev/null
  db_created=1
  # Test the client's meta-command support inside our empty throwaway DB.
  if printf '%s\n' '\restrict seedprobe' '\unrestrict seedprobe' | \
      psql "$target_url" -X -v ON_ERROR_STOP=1 >/dev/null 2>&1; then
    psql "$target_url" -X -v ON_ERROR_STOP=1 -f "scripts/split/evidence/$dump" >/dev/null
  else
    sed '/^\\restrict/d; /^\\unrestrict/d' "scripts/split/evidence/$dump" | \
      psql "$target_url" -X -v ON_ERROR_STOP=1 >/dev/null
  fi
  psql "$target_url" -X -v ON_ERROR_STOP=1 -f scripts/split/seed/seed.sql >/dev/null
  counts=$(psql "$target_url" -X -v ON_ERROR_STOP=1 -Atc \
    "SELECT format('%s users, %s sites, %s events, %s teams, %s grants', (SELECT count(*) FROM users), (SELECT count(*) FROM sites), (SELECT count(*) FROM events), (SELECT count(*) FROM event_teams), (SELECT count(*) FROM oauth_grants));")
  echo "ok $dump: $counts"
  if psql "$target_url" -X -v ON_ERROR_STOP=1 -f scripts/split/seed/seed.sql >/dev/null 2>&1; then
    echo "FAIL: second seed load unexpectedly succeeded for $dump" >&2
    exit 1
  fi
  echo 'ok second load refused'
  # Verify that refusing a second load left the original data intact.
  psql "$target_url" -X -v ON_ERROR_STOP=1 -c \
    "DO \$\$ BEGIN IF (SELECT count(*) FROM users) <> 4 OR (SELECT count(*) FROM sites) <> 5 THEN RAISE EXCEPTION 'second load changed seed'; END IF; END \$\$;" >/dev/null
  psql "$admin_url" -X -v ON_ERROR_STOP=1 -c "DROP DATABASE \"$dbname\";" >/dev/null
  db_created=0
  dbname=""
done
