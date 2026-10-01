#!/bin/sh
# Entry point for the small box on Coolify.
#
#   start.sh            run the server and Caddy (the container's normal job)
#   start.sh release    load the schema into a new, empty database, then
#                       `simple-host migrate`. The compose file runs it as a
#                       one-shot service before the server starts; if it
#                       fails, the server does not start.
#   start.sh <args>     any other simple-host command, e.g. `version`.
set -eu

if [ "${1:-}" = "release" ]; then
  : "${DB_DSN:?DB_DSN is not set: the compose file sets it}"
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
  # A new volume is owned by root. Hand it to the server's user, then run
  # everything as that user.
  mkdir -p /data/sites /data/caddy-logs /data/caddy
  [ "$(stat -c %U /data/sites)" = "app" ] || chown -R app:app /data
  exec su-exec app "$0"
fi

# One setting names the box; the other two follow from it unless set.
if [ -n "${SITE_DOMAIN:-}" ]; then
  export CONTENT_HOST="${CONTENT_HOST:-sites.$SITE_DOMAIN}"
  export PUBLIC_BASE_URL="${PUBLIC_BASE_URL:-https://$SITE_DOMAIN}"
fi

# Caddy listens on WEB_PORT (8080: the compose file's expose and healthcheck
# use it); the server keeps its own port on 127.0.0.1.
export WEB_PORT="${WEB_PORT:-8080}"
export PORT=8090

simple-host &
APP=$!
caddy run --config /etc/caddy/Caddyfile --adapter caddyfile &
WEB=$!
trap 'kill -TERM $APP $WEB 2>/dev/null; wait' TERM INT
# If either process dies, exit so Docker restarts the container.
while kill -0 $APP 2>/dev/null && kill -0 $WEB 2>/dev/null; do sleep 2; done
kill -TERM $APP $WEB 2>/dev/null || true
exit 1
