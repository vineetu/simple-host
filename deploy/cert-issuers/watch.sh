#!/usr/bin/env bash
# Independent of issuer progress: an outstanding request is created when the
# first site needs its wildcard. Tell the owner after 15 minutes without ready.
set -euo pipefail
# shellcheck source=deploy/cert-issuers/fallback.sh
. "$(dirname "$0")/fallback.sh"
check_instance() (
  local conf=$1 default=$2 h request now
  SITE_DOMAIN=simple-host.app
  STATE=/var/lib/simple-host-site-certs
  if [ "$default" != 1 ]; then SITE_DOMAIN=""; STATE=""; fi
  # shellcheck source=/dev/null
  [ ! -r "$conf" ] || . "$conf"
  [ -n "$SITE_DOMAIN" ] && [ -n "$STATE" ] || exit 0
  now=$(date +%s)
  for request in "$STATE"/requests/*; do
    [ -f "$request" ] || continue
    h=$(basename "$request")
    [[ "$h" =~ ^[a-z0-9]([a-z0-9-]{0,37}[a-z0-9])?$ ]] || continue
    [ ! -f "$STATE/ready/$h" ] || continue
    [ $((now - $(stat -c %Y "$request"))) -ge 900 ] || continue
    cert_fallback_alert "$h.$SITE_DOMAIN" "Site certificate missing for *.$h.$SITE_DOMAIN more than 15 minutes after the request; check $STATE/failed/$h and the issuer journal." || true
  done
)
check_instance "${SITE_CERTS_CONF:-/etc/simple-host-site-certs.conf}" 1
for conf in ${SITE_CERTS_CONF_GLOB:-/etc/simple-host-site-certs-*.conf}; do
  [ -r "$conf" ] || continue
  check_instance "$conf" 0
done
