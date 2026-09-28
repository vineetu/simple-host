#!/bin/sh
# Entry point for the small box on Fly.io.
#
#   start.sh            run the server and Caddy (the machine's normal job)
#   start.sh release    fly.toml's release_command: load the schema into a new,
#                       empty database, then `simple-host migrate`. Runs on a
#                       throwaway machine without the volume, before each deploy.
#   start.sh <args>     any other simple-host command, e.g. `version`.
set -eu

if [ "${1:-}" = "release" ]; then
  : "${DB_DSN:?DB_DSN is not set: fly postgres attach --variable-name DB_DSN}"
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

# A new Fly volume is owned by root; the server runs as a normal user.
mkdir -p /data/sites /data/caddy-logs
[ "$(stat -c %U /data/sites)" = "app" ] || chown -R app:app /data/sites

su-exec app simple-host &
APP=$!
caddy run --config /etc/caddy/Caddyfile --adapter caddyfile &
WEB=$!
trap 'kill -TERM $APP $WEB 2>/dev/null; wait' TERM INT
# If either process dies, exit so Fly restarts the machine.
while kill -0 $APP 2>/dev/null && kill -0 $WEB 2>/dev/null; do sleep 2; done
kill -TERM $APP $WEB 2>/dev/null || true
exit 1
