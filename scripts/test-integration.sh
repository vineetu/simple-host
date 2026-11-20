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
# Two uniquely named templates are created inside that server: the platform
# schema alone, and the platform followed by the product schema. The resolved
# package directory selects its template; both are dropped on exit. Nothing
# here ever touches a database named anything but its own throwaway one, and
# it never uses sudo or the box's shared/production Postgres. Cleanup only
# ever acts on resources THIS run actually created (recorded by ID, not by a
# name that could collide with another run or with something left over from
# a prior failed one).
#
# Go's test cache: by default a package whose code, read files and read
# environment are unchanged reports "(cached)" instead of running again. For
# that the database URLs must be the same every run, so the Docker server
# listens on a port derived from this checkout's path, uses one password kept
# in ~/.cache/simple-host-tests, and the templates are named after a hash of
# both schema files (a schema change re-runs every DB-backed package; a
# migration or Go change re-runs the packages that embed or import it).
# --full forces every package to run (-count=1): installs and CI use it.
#
# `go test -json ./...` output is summarised by
# scripts/lib/summarize-go-test.py, which fails the run if any test skipped
# for a DB-related reason (DB_DSN unset / postgres not reachable), in any
# -count iteration, or if the output stream isn't real `go test -json`
# output (e.g. -json got disabled). Other skips (e.g. "needs network") are
# fine and just counted. The required -p N -json flags (and -count=N when
# forced) are placed AFTER "$@" so extra args can never disable them.
#
# Usage:
#   bash scripts/test-integration.sh                 # starts its own Postgres via Docker
#   bash scripts/test-integration.sh --full          # run every package, ignoring the cache
#   TEST_PG_URL=postgres://postgres:postgres@127.0.0.1:5432 bash scripts/test-integration.sh
#   bash scripts/test-integration.sh -run '^TestFoo$' # extra args passed through to `go test`
#   TEST_PKGS=./host/handler TEST_COUNT=5 bash scripts/test-integration.sh -shuffle=on -run '^TestFoo'
#     # packages via TEST_PKGS (default ./...), repeat count via TEST_COUNT (forces runs);
#     # `make test-db PKGS=... RUN=... COUNT=...` wraps this.
#   TEST_WORKERS=1 bash scripts/test-integration.sh  # serial packages (default 3)
set -u
cd "$(dirname "$0")/.."

count_flag=()
if [ "${1:-}" = "--full" ]; then
  shift
  count_flag=(-count=1)
fi
if [ -n "${TEST_COUNT:-}" ]; then
  count_flag=(-count="$TEST_COUNT")
fi

test_workers="${TEST_WORKERS:-3}"
if [[ ! "$test_workers" =~ ^[1-9][0-9]*$ ]]; then
  echo "FAIL: TEST_WORKERS must be a positive integer" >&2
  exit 1
fi

container_id=""
dbname=""
admin_url=""
json_file=""
created_databases=()
go_test_pid=""

cleanup() {
  local status=$?
  trap '' INT TERM
  if [ -n "$go_test_pid" ]; then
    # The background job has its own process group, including test binaries.
    kill -TERM -- "-$go_test_pid" 2>/dev/null || true
    wait "$go_test_pid" 2>/dev/null || true
  fi
  for owned_db in "${created_databases[@]}"; do
    # An interrupted test cannot run testdb's deferred cleanup. Remove only
    # clones of THIS run's uniquely named templates, before the templates.
    psql "$admin_url" -XAt -v ON_ERROR_STOP=1 -c \
      "SELECT format('DROP DATABASE %I WITH (FORCE);', datname) FROM pg_database WHERE starts_with(datname, '${owned_db}_t');" \
      | psql "$admin_url" -X -v ON_ERROR_STOP=1 >/dev/null 2>&1 \
      || echo "warning: failed to drop clones of $owned_db -- clean up manually" >&2
    psql "$admin_url" -v ON_ERROR_STOP=1 -c "DROP DATABASE IF EXISTS \"$owned_db\";" >/dev/null 2>&1 \
      || echo "warning: failed to drop throwaway database $owned_db -- clean up manually" >&2
  done
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
trap 'exit 130' INT
trap 'exit 143' TERM

base=""

if [ -n "${TEST_PG_URL:-}" ]; then
  # This script owns the database name and connection parameters below it;
  # a path or query in TEST_PG_URL would be silently discarded or clash
  # with what this script appends, so reject it instead.
  rest="${TEST_PG_URL#*://}"
  rest="${rest#*@}"
  if [[ "$rest" == *"/"* || "$rest" == *"?"* ]]; then
    echo "FAIL: TEST_PG_URL must be a bare server URL with no path or query string," >&2
    echo "      e.g. postgres://postgres:postgres@127.0.0.1:5432 " >&2
    exit 1
  fi
  base="${TEST_PG_URL%/}"
  echo "== using TEST_PG_URL server =="
elif command -v docker >/dev/null 2>&1; then
  echo "== starting throwaway postgres:16-alpine via Docker =="
  container_name="sh-it-$(od -An -N4 -tx4 /dev/urandom | tr -d ' \n')"
  pass_file="${XDG_CACHE_HOME:-$HOME/.cache}/simple-host-tests/pg-password"
  if [ ! -s "$pass_file" ]; then
    mkdir -p "$(dirname "$pass_file")"
    (umask 077 && openssl rand -hex 16 >"$pass_file")
  fi
  pgpass=$(cat "$pass_file")
  # 20000-39999, steady per checkout; a busy port falls back to a random one.
  stable_port=$(( 20000 + $(printf '%s' "$PWD" | cksum | cut -d' ' -f1) % 20000 ))
  started_id=""
  for publish in "127.0.0.1:${stable_port}:5432" "127.0.0.1::5432"; do
    started_id=$(docker run -d --name "$container_name" -e POSTGRES_PASSWORD="$pgpass" \
        -p "$publish" postgres:16-alpine 2>/tmp/sh-it-docker-run.$$)
    run_status=$?
    if [ "$run_status" -eq 0 ] && [ -n "$started_id" ]; then
      break
    fi
    # A failed start can leave the container created under our random name.
    docker rm -fv "$container_name" >/dev/null 2>&1
    started_id=""
    [ "$publish" = "127.0.0.1::5432" ] || echo "note: port $stable_port is busy; this run's results will not be cached" >&2
  done
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

if [ -n "$container_id" ]; then
  # Our own server: a steady name keeps DB_DSN, and so the test cache, steady.
  dbname="sh_it_$(cat host/db/schema.sql products/hack/db/schema.sql | sha256sum | cut -c1-12)"
else
  # A shared server may hold another run's templates.
  dbname="sh_it_$$_$(openssl rand -hex 4)"
fi
admin_url="${base}/postgres?sslmode=disable"
platform_url="${base}/${dbname}_platform?sslmode=disable"
product_url="${base}/${dbname}_product?sslmode=disable"

for schema_set in platform product; do
  template_name="${dbname}_${schema_set}"
  target_url="${base}/${template_name}?sslmode=disable"
  echo "== creating throwaway database $template_name =="
  # Record CREATE's result before honoring an interrupt during psql.
  setup_signal=0
  trap 'setup_signal=130' INT
  trap 'setup_signal=143' TERM
  if psql "$admin_url" -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"$template_name\";" >/dev/null; then
    created_databases+=("$template_name")
  fi
  trap 'exit 130' INT
  trap 'exit 143' TERM
  if [ "$setup_signal" -ne 0 ]; then
    exit "$setup_signal"
  fi
  if [ "${#created_databases[@]}" -eq 0 ] || [ "${created_databases[-1]}" != "$template_name" ]; then
    echo "FAIL: could not create $schema_set template" >&2
    exit 1
  fi

  echo "== applying host/db/schema.sql ($schema_set template) =="
  if ! psql "$target_url" -v ON_ERROR_STOP=1 -f host/db/schema.sql >/dev/null; then
    echo "FAIL: platform schema failed to apply to the throwaway database" >&2
    exit 1
  fi
  if [ "$schema_set" = product ]; then
    echo "== applying products/hack/db/schema.sql (product template) =="
    if ! psql "$target_url" -v ON_ERROR_STOP=1 -f products/hack/db/schema.sql >/dev/null; then
      echo "FAIL: product schema failed to apply to the throwaway database" >&2
      exit 1
    fi
  fi
done

test_pkgs="${TEST_PKGS:-./...}"
platform_packages=()
product_packages=()
# Resolve patterns before selecting a database. An import path, ./..., or an
# explicit directory all give the same schema for a package under products/ or its program entry point.
# $test_pkgs is intentionally split into package patterns.
if ! package_list=$(go list -f '{{.ImportPath}} {{.Dir}}' $test_pkgs); then
  echo "FAIL: could not resolve TEST_PKGS" >&2
  exit 1
fi
repo_root="$PWD"
while read -r import_path package_dir; do
  [ -n "$import_path" ] || continue
  case "$package_dir" in
    "$repo_root"/products/*|"$repo_root"/cmd/simple-hack) product_packages+=("$import_path") ;;
    *) platform_packages+=("$import_path") ;;
  esac
done <<< "$package_list"

echo "== running go test $test_pkgs (DB_DSN set; TESTDB_ISOLATE=1; forcing -p $test_workers ${count_flag[*]} -json) =="
json_file=$(mktemp)
go_test_exit=0
for schema_set in platform product; do
  if [ "$schema_set" = platform ]; then
    packages=("${platform_packages[@]}")
    target_url="$platform_url"
  else
    packages=("${product_packages[@]}")
    target_url="$product_url"
  fi
  [ "${#packages[@]}" -gt 0 ] || continue
  echo "== $schema_set schema: ${#packages[@]} packages =="
  # Packages choose DB_DSN here; the fixture helpers also receive the two
  # schema sets explicitly, so Fresh always clones the caller's schema.
  # Clear inherited migration DSNs: those tests require empty databases and
  # belong to check-fresh-install.sh, never to a schema template.
  # Forced flags follow caller arguments; package paths come last.
  set -m
  launch_signal=0
  trap 'launch_signal=130' INT
  trap 'launch_signal=143' TERM
  DB_DSN="$target_url" MIGRATE_TEST_DSN= TESTDB_ISOLATE=1 \
    TESTDB_PLATFORM_DSN="$platform_url" TESTDB_PRODUCT_DSN="$product_url" \
    go test "$@" -p "$test_workers" "${count_flag[@]}" -json "${packages[@]}" >>"$json_file" 2>&1 &
  go_test_pid=$!
  trap 'exit 130' INT
  trap 'exit 143' TERM
  if [ "$launch_signal" -ne 0 ]; then
    exit "$launch_signal"
  fi
  set +m
  wait "$go_test_pid"
  test_exit=$?
  if [ "$test_exit" -ne 0 ]; then
    go_test_exit="$test_exit"
  fi
  go_test_pid=""
done

echo
python3 scripts/lib/summarize-go-test.py "$json_file" "$go_test_exit"
exit $?
