#!/bin/sh
# Entry point for the small box on Railway.
#
#   start.sh            run the server and Caddy (the service's normal job)
#   start.sh release    the Pre-Deploy Command: initialise or adopt the database with
#                       `simple-host migrate -init -record-product`. Railway
#                       runs it in a separate container, without the volume,
#                       before each deploy; if it fails, the deploy stops.
#   start.sh <args>     any other simple-host command, e.g. `version`.
set -eu

if [ "${1:-}" = "release" ]; then
  : "${DB_DSN:?DB_DSN is not set: reference the Postgres service's DATABASE_URL}"
  exec simple-host migrate -init -record-product
fi
if [ $# -gt 0 ]; then
  exec su-exec app simple-host "$@"
fi

if [ "$(id -u)" = 0 ]; then
  # A new volume is owned by root. Hand it to the server's user, then run
  # everything as that user: Railway's containers, like Render's, do not let
  # root signal another user's processes, so the supervisor below shares a
  # user with the two processes it watches.
  mkdir -p /data/sites /data/caddy-logs /data/caddy
  [ "$(stat -c %U /data/sites)" = "app" ] || chown -R app:app /data
  exec su-exec app "$0"
fi

# Railway injects PORT and sends traffic to it. That port is Caddy's; the
# server keeps its own port on 127.0.0.1.
export WEB_PORT="${PORT:-8080}"
export PORT=8090

simple-host &
APP=$!
caddy run --config /etc/caddy/Caddyfile --adapter caddyfile &
WEB=$!
trap 'kill -TERM $APP $WEB 2>/dev/null; wait' TERM INT
# If either process dies, exit so Railway restarts the container.
while kill -0 $APP 2>/dev/null && kill -0 $WEB 2>/dev/null; do sleep 2; done
kill -TERM $APP $WEB 2>/dev/null || true
exit 1
