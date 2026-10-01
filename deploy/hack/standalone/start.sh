#!/bin/sh
set -eu
if [ "${1:-}" = release ]; then
  : "${DB_DSN:?}"
  if [ "$(psql "$DB_DSN" -tAc "SELECT to_regclass('public.users') IS NULL")" = t ]; then
    psql "$DB_DSN" -v ON_ERROR_STOP=1 -q -f /opt/simple-hack/schema.sql
  fi
  exec simple-host migrate
fi
if [ "$#" -gt 0 ]; then exec simple-host "$@"; fi
: "${SITE_DOMAIN:?}" "${RESEND_API_KEY:?}" "${MAIL_FROM:?}"
if [ "$(id -u)" = 0 ]; then
  mkdir -p /data/sites /data/logs /data/caddy /data/caddy-config
  [ "$(stat -c %u /data/sites)" = 65532 ] || chown -R app:app /data
  exec su-exec app "$0"
fi
export PUBLIC_BASE_URL="https://$SITE_DOMAIN"
export EVENTS=hosted PERSON_HOSTS=canonical SITE_HOSTS=canonical
export WRITE_AUTH_MODE=on
# Caddy manages certificates during the handshake; there is no external
# wildcard issuer or ready-marker directory in this installation.
unset SITE_CERT_DIR EVENT_NAME_PEER EVENT_DNS_TOKEN EVENT_DNS_TEAM_ID EVENT_DOMAINS
simple-host &
app_pid=$!
caddy run --config /etc/caddy/Caddyfile --adapter caddyfile &
web_pid=$!
trap 'kill -TERM "$app_pid" "$web_pid" 2>/dev/null || true; wait; exit 0' TERM INT
while kill -0 "$app_pid" 2>/dev/null && kill -0 "$web_pid" 2>/dev/null; do sleep 2; done
kill -TERM "$app_pid" "$web_pid" 2>/dev/null || true
wait || true
exit 1
