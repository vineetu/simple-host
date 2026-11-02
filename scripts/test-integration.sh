#!/usr/bin/env bash
# Integration test runner.
#
# `go test ./...` alone never exercises the ~15 DB-backed test fixtures: with
# no DB_DSN set they call t.Skip and the suite stays green with zero
# integration coverage. This script gives them a real, disposable database
# and FAILS (rather than silently passing) if any of them still skip.
#
# Database source, in order:
#   1. TEST_PG_URL -- a bare server URL with NO path and NO query string,
#      e.g. postgres://postgres:postgres@127.0.0.1:5432 (this script owns
#      the database name and always appends sslmode=disable itself). This
#      is what CI sets, pointed at its postgres:16 service container. A
#      URL with a path or query is rejected rather than silently mangled.
#   2. Otherwise, if `docker` is available, a throwaway `postgres:16-alpine`
#      container on a random free localhost port, removed (container AND
#      its anonymous volume) on exit.
#   3. Otherwise, this script fails loudly -- it never silently skips.
#
# A uniquely named database is created inside that server, db/schema.sql is
# applied with `psql -v ON_ERROR_STOP=1`, and it is dropped on exit. Nothing
# here ever touches a database named anything but its own throwaway one, and
# it never uses sudo or the box's shared/production Postgres. Cleanup only
# ever acts on resources THIS run actually created (recorded by ID, not by a
# name that could collide with another run or with something left over from
# a prior failed one).
#
# `go test -json ./...` output is summarised by
# scripts/lib/summarize-go-test.py, which fails the run if any test skipped
# for a DB-related reason (DB_DSN unset / postgres not reachable), in any
# -count iteration, or if the output stream isn't real `go test -json`
# output (e.g. -json got disabled). Other skips (e.g. "needs network") are
# fine and just counted. The required -p 1 -count=N -json flags are placed
# AFTER "$@" so an overriding flag in extra args can never disable them.
#
# Usage:
#   bash scripts/test-integration.sh                 # starts its own Postgres via Docker
#   TEST_PG_URL=postgres://postgres:postgres@127.0.0.1:5432 bash scripts/test-integration.sh
#   bash scripts/test-integration.sh -run '^TestFoo$' # extra args passed through to `go test`
#   TEST_PKGS=./internal/handler TEST_COUNT=5 bash scripts/test-integration.sh -shuffle=on -run '^TestFoo'
#     # packages via TEST_PKGS (default ./...), repeat count via TEST_COUNT (default 1);
#     # `make test-db PKGS=... RUN=... COUNT=...` wraps this.
set -u
cd "$(dirname "$0")/.."

container_id=""
dbname=""
admin_url=""
json_file=""
db_created=0

cleanup() {
  local status=$?
  if [ "$db_created" -eq 1 ] && [ -n "$dbname" ] && [ -n "$admin_url" ]; then
    psql "$admin_url" -v ON_ERROR_STOP=1 -c "DROP DATABASE IF EXISTS \"$dbname\";" >/dev/null 2>&1 \
      || echo "warning: failed to drop throwaway database $dbname -- clean up manually" >&2
  fi
  if [ -n "$container_id" ]; then
    # -v also removes the image's anonymous data volume; without it every
    # run leaks one.
    docker rm -fv "$container_id" >/dev/null 2>&1 \
      || echo "warning: failed to remove container $container_id -- clean up manually: docker rm -fv $container_id" >&2
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
  # This script owns the database name and connection parameters below it;
  # a path or query in TEST_PG_URL would be silently discarded or clash
  # with what this script appends, so reject it instead.
  rest="${TEST_PG_URL#*://}"
  rest="${rest#*@}"
  if [[ "$rest" == *"/"* || "$rest" == *"?"* ]]; then
    echo "FAIL: TEST_PG_URL must be a bare server URL with no path or query string," >&2
    echo "      e.g. postgres://postgres:postgres@127.0.0.1:5432 -- got: $TEST_PG_URL" >&2
    exit 1
  fi
  base="${TEST_PG_URL%/}"
  echo "== using TEST_PG_URL server =="
elif command -v docker >/dev/null 2>&1; then
  echo "== starting throwaway postgres:16-alpine via Docker =="
  container_name="sh-it-$(od -An -N4 -tx4 /dev/urandom | tr -d ' \n')"
  pgpass=$(openssl rand -hex 16)
  started_id=$(docker run -d --name "$container_name" -e POSTGRES_PASSWORD="$pgpass" \
      -p 127.0.0.1::5432 postgres:16-alpine 2>/tmp/sh-it-docker-run.$$)
  run_status=$?
  if [ "$run_status" -ne 0 ] || [ -z "$started_id" ]; then
    echo "FAIL: could not start a disposable postgres container" >&2
    cat /tmp/sh-it-docker-run.$$ >&2 2>/dev/null
    rm -f /tmp/sh-it-docker-run.$$
    # Nothing was recorded, so cleanup won't try to remove a container by
    # this name -- but docker run can still have created one even while
    # reporting failure (e.g. the start step failing after create). Make
    # sure, by ID if we have one, by name otherwise, without ever touching
    # a name/ID we didn't just get back from this invocation.
    if [ -n "$started_id" ]; then
      docker rm -fv "$started_id" >/dev/null 2>&1
    fi
    exit 1
  fi
  rm -f /tmp/sh-it-docker-run.$$
  container_id="$started_id"

  mapping=$(docker port "$container_id" 5432/tcp 2>/dev/null | head -1)
  port="${mapping##*:}"
  if [ -z "$port" ]; then
    echo "FAIL: could not determine the mapped port for $container_id" >&2
    exit 1
  fi
  base="postgres://postgres:${pgpass}@127.0.0.1:${port}"

  # docker exec pg_isready can succeed against the image's own temporary
  # bootstrap server before the real postgres is accepting connections on
  # the mapped TCP port. Poll the actual exposed endpoint with a real,
  # authenticated query instead.
  ready=0
  for _ in $(seq 1 60); do
    if psql "${base}/postgres?sslmode=disable" -v ON_ERROR_STOP=1 -XAtc 'select 1' >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 1
  done
  if [ "$ready" -ne 1 ]; then
    echo "FAIL: disposable postgres did not become ready on 127.0.0.1:${port}" >&2
    exit 1
  fi
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
db_created=1

echo "== applying db/schema.sql =="
if ! psql "$target_url" -v ON_ERROR_STOP=1 -f db/schema.sql >/dev/null; then
  echo "FAIL: db/schema.sql failed to apply to the throwaway database" >&2
  exit 1
fi

test_pkgs="${TEST_PKGS:-./...}"
test_count="${TEST_COUNT:-1}"
echo "== running go test $test_pkgs (DB_DSN set; TESTDB_ISOLATE=1; forcing -p 1 -count=$test_count -json) =="
json_file=$(mktemp)
# "$@" comes first so our own -p/-count/-json always win over anything in
# it trying to turn JSON output off -- go's flag parser takes the last
# occurrence of a repeated flag. Packages go LAST, after every flag: go test
# stops parsing its own flags at the first package path, so a path inside
# "$@" would hand -json to the test binary instead. Pass packages via
# TEST_PKGS (space-separated), never in "$@".
# TESTDB_ISOLATE=1 makes each test package's TestMain (internal/testdb)
# clone this database into its own throwaway copy, so this one stays an
# untouched, connection-free template.
# MIGRATE_TEST_DSN is cleared: the migration tests drop columns and need an
# EMPTY database, so they belong to scripts/check-fresh-install.sh (also run
# by `make check`), never to a database inherited from the caller's shell.
# $test_pkgs is unquoted on purpose: it may list several packages.
DB_DSN="$target_url" MIGRATE_TEST_DSN= TESTDB_ISOLATE=1 go test "$@" -p 1 -count="$test_count" -json $test_pkgs >"$json_file" 2>&1
go_test_exit=$?

echo
python3 scripts/lib/summarize-go-test.py "$json_file" "$go_test_exit"
exit $?
