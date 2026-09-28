#!/bin/sh
# Entry point for the small box on Render.
#
#   start.sh            run the server and Caddy (the service's normal job)
#   start.sh release    the Pre-Deploy Command: load the schema into a new,
#                       empty database, then `simple-host migrate`. Render runs
#                       it on separate compute, without the disk, before each
#                       deploy; if it fails, the deploy stops.
#   start.sh <args>     any other simple-host command, e.g. `version`.
set -eu

if [ "${1:-}" = "release" ]; then
  : "${DB_DSN:?DB_DSN is not set: link it to the database's internal connection string}"
  # A new database gets schema.sql first, exactly as the compose Postgres
  # container does on a new volume; migrate then records each file.
  if [ "$(psql "$DB_DSN" -tAc "SELECT to_regclass('public.users') IS NULL")" = "t" ]; then
    echo "empty database: loading schema.sql"
    psql "$DB_DSN" -v ON_ERROR_STOP=1 -q -f /opt/simple-host/schema.sql
  fi
  exec simple-host migrate
fi
if [ $# -gt 0 ]; then
  exec su-exec app simple-host "$@"
fi

if [ "$(id -u)" = 0 ]; then
  # A new Render disk is owned by root. Hand it to the server's user, then run
  # everything as that user: in Render's containers root cannot signal another
  # user's processes, so the supervisor below shares a user with the two
  # processes it watches.
  mkdir -p /data/sites /data/caddy-logs /data/caddy
  [ "$(stat -c %U /data/sites)" = "app" ] || chown -R app:app /data
  exec su-exec app "$0"
fi

# Render sends traffic to PORT (10000 unless you set it). That port is
# Caddy's; the server keeps its own port on 127.0.0.1.
export WEB_PORT="${PORT:-10000}"
export PORT=8090

simple-host &
APP=$!
caddy run --config /etc/caddy/Caddyfile --adapter caddyfile &
WEB=$!
trap 'kill -TERM $APP $WEB 2>/dev/null; wait' TERM INT
# If either process dies, exit so Render restarts the service.
while kill -0 $APP 2>/dev/null && kill -0 $WEB 2>/dev/null; do sleep 2; done
kill -TERM $APP $WEB 2>/dev/null || true
exit 1
