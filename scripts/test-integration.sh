#!/usr/bin/env bash
# Integration test runner.
#
# `go test ./...` alone never exercises the ~15 DB-backed test fixtures: with
# no DB_DSN set they call t.Skip and the suite stays green with zero
# integration coverage. This script gives them a real, disposable database
# and FAILS (rather than silently passing) if any of them still skip.
#
# Database source, in order:
#   1. TEST_PG_URL, e.g. postgres://postgres:postgres@127.0.0.1:5432
#      (no path/query -- this script owns the database name). This is what
#      CI sets, pointed at its postgres:16 service container.
#   2. Otherwise, if `docker` is available, a throwaway `postgres:16-alpine`
#      container on a random free localhost port, removed on exit.
#   3. Otherwise, this script fails loudly -- it never silently skips.
#
# A uniquely named database is created inside that server, db/schema.sql is
# applied with `psql -v ON_ERROR_STOP=1`, and it is dropped on exit. Nothing
# here ever touches a database named anything but its own throwaway one, and
# it never uses sudo or the box's shared/production Postgres.
#
# `go test -json ./...` output is summarised by
# scripts/lib/summarize-go-test.py, which fails the run if any test skipped
# for a DB-related reason (DB_DSN unset / postgres not reachable). Other
# skips (e.g. "needs network") are fine and just counted.
#
# Usage:
#   bash scripts/test-integration.sh                 # starts its own Postgres via Docker
#   TEST_PG_URL=postgres://postgres:postgres@127.0.0.1:5432 bash scripts/test-integration.sh
#   bash scripts/test-integration.sh -run '^TestFoo$' # extra args passed through to `go test`
set -u
cd "$(dirname "$0")/.."

container=""
dbname=""
admin_url=""
json_file=""

cleanup() {
  local status=$?
  if [ -n "$dbname" ] && [ -n "$admin_url" ]; then
    psql "$admin_url" -v ON_ERROR_STOP=1 -c "DROP DATABASE IF EXISTS \"$dbname\";" >/dev/null 2>&1 \
      || echo "warning: failed to drop throwaway database $dbname -- clean up manually" >&2
  fi
  if [ -n "$container" ]; then
    docker rm -f "$container" >/dev/null 2>&1 \
      || echo "warning: failed to remove container $container -- clean up manually" >&2
  fi
  if [ -n "$json_file" ]; then
    rm -f "$json_file"
  fi
  trap - EXIT
  exit "$status"
}
trap cleanup EXIT

base=""

if [ -n "${TEST_PG_URL:-}" ]; then
  base="${TEST_PG_URL%%\?*}"
  base="${base%/}"
  echo "== using TEST_PG_URL server =="
elif command -v docker >/dev/null 2>&1; then
  echo "== starting throwaway postgres:16-alpine via Docker =="
  container="sh-it-$$"
  pgpass=$(openssl rand -hex 16)
  if ! docker run -d --name "$container" -e POSTGRES_PASSWORD="$pgpass" \
      -p 127.0.0.1::5432 postgres:16-alpine >/dev/null 2>&1; then
    echo "FAIL: could not start a disposable postgres container" >&2
    exit 1
  fi
  mapping=$(docker port "$container" 5432/tcp 2>/dev/null | head -1)
  port="${mapping##*:}"
  if [ -z "$port" ]; then
    echo "FAIL: could not determine the mapped port for $container" >&2
    exit 1
  fi
  ready=0
  for _ in $(seq 1 60); do
    if docker exec "$container" pg_isready -U postgres >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 1
  done
  if [ "$ready" -ne 1 ]; then
    echo "FAIL: disposable postgres did not become ready" >&2
    exit 1
  fi
  base="postgres://postgres:${pgpass}@127.0.0.1:${port}"
else
  echo "FAIL: no TEST_PG_URL and no docker available -- cannot run integration tests. Set TEST_PG_URL to an existing Postgres server, or install Docker." >&2
  exit 1
fi

dbname="sh_it_$$_$(openssl rand -hex 4)"
admin_url="${base}/postgres?sslmode=disable"
target_url="${base}/${dbname}?sslmode=disable"

echo "== creating throwaway database $dbname =="
if ! psql "$admin_url" -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"$dbname\";" >/dev/null; then
  echo "FAIL: could not create database $dbname" >&2
  exit 1
fi

echo "== applying db/schema.sql =="
if ! psql "$target_url" -v ON_ERROR_STOP=1 -f db/schema.sql >/dev/null; then
  echo "FAIL: db/schema.sql failed to apply to the throwaway database" >&2
  exit 1
fi

echo "== running go test -p 1 -count=1 -json ./... (DB_DSN set) =="
json_file=$(mktemp)
DB_DSN="$target_url" go test -p 1 -count=1 -json ./... "$@" >"$json_file" 2>&1
go_test_exit=$?

echo
python3 scripts/lib/summarize-go-test.py "$json_file" "$go_test_exit"
exit $?
