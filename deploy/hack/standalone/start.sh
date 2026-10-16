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
: "${SITE_DOMAIN:?}"
# Resend is only required when there is no other way to sign in: company
# sign-in (OIDC_ISSUER + OIDC_CLIENT_ID + OIDC_CLIENT_SECRET) does not need it.
if [ -z "${OIDC_ISSUER:-}" ] || [ -z "${OIDC_CLIENT_ID:-}" ] || [ -z "${OIDC_CLIENT_SECRET:-}" ]; then
  : "${RESEND_API_KEY:?Set RESEND_API_KEY and MAIL_FROM, or configure OIDC_ISSUER, OIDC_CLIENT_ID and OIDC_CLIENT_SECRET for company sign-in.}"
  : "${MAIL_FROM:?Set RESEND_API_KEY and MAIL_FROM, or configure OIDC_ISSUER, OIDC_CLIENT_ID and OIDC_CLIENT_SECRET for company sign-in.}"
fi
TLS_MODE=${TLS_MODE:-public}
case "$TLS_MODE" in
  public|internal|acme|certs) ;;
  *) echo "TLS_MODE must be public, internal, acme or certs (got '$TLS_MODE')." >&2; exit 1 ;;
esac
if [ "$TLS_MODE" = acme ] && [ -z "${ACME_CA:-}" ]; then
  echo "ACME_CA is required when TLS_MODE=acme." >&2
  exit 1
fi
if [ "$(id -u)" = 0 ]; then
  mkdir -p /data/sites /data/logs /data/caddy /data/caddy-config
  [ "$(stat -c %u /data/sites)" = 65532 ] || chown -R app:app /data
  # The company CA, when supplied, is trusted by this Go process in addition
  # to (not instead of) the system roots: a combined bundle under /data
  # survives a restart's environment without touching the read-only mount.
  CA_BUNDLE=""
  if [ -f /etc/simple-hack/tls/ca.pem ]; then
    cat /etc/ssl/certs/ca-certificates.crt /etc/simple-hack/tls/ca.pem > /data/caddy-config/ca-bundle.pem
    chown app:app /data/caddy-config/ca-bundle.pem
    chmod 644 /data/caddy-config/ca-bundle.pem
    CA_BUNDLE=/data/caddy-config/ca-bundle.pem
  fi
  # Caddy's tls block imports this file rather than reading TLS_MODE/ACME_CA
  # itself: Caddyfile env substitution is one token per placeholder, and this
  # directive can span several lines (an issuer block).
  case "$TLS_MODE" in
    public) directive="on_demand" ;;
    internal) directive="on_demand
issuer internal" ;;
    acme)
      if [ -f /etc/simple-hack/tls/ca.pem ]; then
        directive="on_demand
issuer acme {
	dir $ACME_CA
	trusted_roots /etc/simple-hack/tls/ca.pem
}"
      else
        directive="on_demand
issuer acme {
	dir $ACME_CA
}"
      fi
      ;;
    certs) directive="load /etc/simple-hack/tls/certs" ;;
  esac
  printf '%s\n' "$directive" > /data/caddy-config/tls-directive.caddy
  chown app:app /data/caddy-config/tls-directive.caddy
  [ -z "$CA_BUNDLE" ] || export SSL_CERT_FILE="$CA_BUNDLE"
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
