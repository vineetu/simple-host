#!/usr/bin/env bash
# Restore a trusted Simple Hack custom-format .dump[.zst] into disposable Postgres.
# No application is started, no network or port is enabled, no row data is printed.
# Usage: sudo bash scripts/check-hack-restore.sh /var/backups/agentbox/postgres/simplehack.dump.zst
set -Eeuo pipefail
umask 077

[[ $# == 1 && -f $1 && -r $1 ]] || { echo 'Usage: check-hack-restore.sh READABLE_DUMP[.zst]' >&2; exit 2; }
dump=$1
image=${HACK_RESTORE_IMAGE:-postgres:16-alpine}
command -v docker >/dev/null || { echo 'FAIL: Docker is required'; exit 1; }
if [[ $dump == *.zst ]]; then
  command -v zstd >/dev/null || { echo 'FAIL: zstd is required'; exit 1; }
fi
# Never pull an image as a side effect of handling a production backup.
docker image inspect "$image" >/dev/null 2>&1 || { echo 'FAIL: load a PostgreSQL 16 image locally first'; exit 1; }
private=$(mktemp -d)
container="sh-hack-restore-$(date +%s)-$$"
created=0
cleanup() {
  local status=$?
  if (( created )); then
    if ! docker rm -f "$container" >/dev/null 2>&1; then
      echo "FAIL: remove disposable container manually: docker rm -f $container" >&2
      status=1
    fi
  fi
  rm -rf -- "$private" || status=1
  trap - EXIT
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Trust applies only inside this network-less disposable container. The production
# dump is streamed over stdin; the container never mounts the host or its secrets.
if ! docker create --name "$container" --network none --read-only --memory 768m \
    --tmpfs /var/lib/postgresql/data:rw,noexec,nosuid,size=512m \
    --tmpfs /var/run/postgresql:rw,noexec,nosuid,size=16m \
    --tmpfs /tmp:rw,noexec,nosuid,size=16m \
    -e POSTGRES_HOST_AUTH_METHOD=trust -e POSTGRES_DB=hack_restore \
    "$image" >"$private/create.log" 2>&1; then
  echo 'FAIL: cannot create disposable Postgres'; exit 1
fi
created=1
docker start "$container" >"$private/start.log" 2>&1 || { echo 'FAIL: cannot start disposable Postgres'; exit 1; }
ready=0
for ((attempt=0; attempt<60; attempt++)); do
  # The entrypoint's temporary bootstrap server also answers pg_isready. Wait
  # for the final postgres process before streaming the non-retryable dump.
  if docker exec "$container" sh -c 'test "$(cat /proc/1/comm)" = postgres' >/dev/null 2>&1 &&
      docker exec "$container" pg_isready -U postgres -d hack_restore >/dev/null 2>&1; then ready=1; break; fi
  sleep 1
done
(( ready )) || { echo 'FAIL: disposable Postgres did not become ready'; exit 1; }
version=$(docker exec "$container" psql -U postgres -d hack_restore -XAtc 'SHOW server_version_num' 2>"$private/version.log") || { echo 'FAIL: cannot query disposable Postgres'; exit 1; }
[[ $version =~ ^16[0-9]{4}$ ]] || { echo 'FAIL: restore verification requires PostgreSQL 16'; exit 1; }

stream_dump() {
  if [[ $dump == *.zst ]]; then zstd -q -dc -- "$dump"; else cat -- "$dump"; fi
}
# PostgreSQL errors can contain row values. Keep them private and remove them
# with the container; only the stage's pass/fail result goes to the caller.
if ! { stream_dump | docker exec -i "$container" pg_restore -U postgres \
    -d hack_restore --no-owner --no-acl --exit-on-error; } >"$private/restore.log" 2>&1; then
  echo 'FAIL: database restore failed (diagnostics withheld because they may contain row data)'; exit 1
fi
echo 'PASS: custom-format dump restored, including its constraints'

scalar() {
  local value
  value=$(docker exec "$container" psql -U postgres -d hack_restore -XAt \
    -v ON_ERROR_STOP=1 -c "$1" 2>"$private/query.log") || return 1
  [[ $value =~ ^[0-9]+$ ]] || return 1
  printf '%s' "$value"
}
failed=0
check_count() {
  local label=$1 expected=$2 actual
  actual=$(scalar "$3") || { echo "FAIL: $label query"; failed=1; return; }
  if [[ $actual == "$expected" ]]; then
    echo "PASS: $label ($actual)"
  else
    echo "FAIL: $label (actual $actual; expected $expected)"
    failed=1
  fi
}
check_count 'core tables present' 8 "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('users','sites','versions','events','event_members','event_teams','event_entries','schema_migrations')"
check_count 'judging tables present' 5 "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('rubric_criteria','event_assignments','event_conflicts','event_scores','event_results')"
check_count 'archive columns present' 4 "SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='events' AND column_name IN ('closed_at','removal_warned_at','sites_removed_at','keep_sites')"
check_count 'unvalidated public constraints' 0 "SELECT count(*) FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace WHERE n.nspname='public' AND NOT c.convalidated"
check_count 'member/team event mismatches' 0 'SELECT count(*) FROM event_members m JOIN event_teams t ON t.id=m.team_id WHERE m.event_id<>t.event_id'
check_count 'entry/team event mismatches' 0 'SELECT count(*) FROM event_entries e JOIN event_teams t ON t.id=e.team_id WHERE e.event_id<>t.event_id'
check_count 'score/team event mismatches' 0 'SELECT count(*) FROM event_scores s JOIN event_teams t ON t.id=s.team_id WHERE s.event_id<>t.event_id'
check_count 'score/criterion event mismatches' 0 'SELECT count(*) FROM event_scores s JOIN rubric_criteria c ON c.id=s.criterion_id WHERE s.event_id<>c.event_id'
for table in events event_teams event_members event_entries event_scores event_results sites versions; do
  if count=$(scalar "SELECT count(*) FROM $table"); then echo "ROWS: $table=$count"; else echo "FAIL: $table aggregate query"; failed=1; fi
done
if count=$(scalar "SELECT count(*) FROM event_results WHERE published_at IS NOT NULL AND CASE WHEN jsonb_typeof(snapshot)='array' THEN jsonb_array_length(snapshot)>0 ELSE false END"); then
  echo "ROWS: nonempty_published_snapshots=$count"
else
  echo 'FAIL: published snapshot aggregate query'; failed=1
fi
if (( failed )); then
  echo 'FAIL: restore completed but current Hack schema or consistency checks did not pass'
  exit 1
fi
echo 'PASS: current Hack schema and aggregate consistency checks; project-file recovery is a separate check'
