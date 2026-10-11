#!/usr/bin/env bash
# Independent of issuer progress: an outstanding request is created when the
# first site needs its wildcard. Tell the owner after 15 minutes without ready.
# Only issuers that can act are checked: an instance whose .path and .timer
# are both disabled (a paused base such as simple-host.site) is never alerted
# on, and re-enabling its units brings its alerts back.
set -euo pipefail
# shellcheck source=deploy/cert-issuers/fallback.sh
. "$(dirname "$0")/fallback.sh"
# issuer_enabled <unit base>: false only when systemctl reports both its path
# and its retry timer disabled or masked. A missing unit, a failed query or no
# systemd at all keeps the instance checked, so alerts are never lost by error.
issuer_enabled() {
  local u
  command -v systemctl >/dev/null 2>&1 || return 0
  for u in "$1.path" "$1.timer"; do
    case $(systemctl is-enabled "$u" 2>/dev/null) in
      disabled | masked | masked-runtime) ;;
      *) return 0 ;;
    esac
  done
  return 1
}
check_instance() (
  local conf=$1 default=$2 unit=$3 h request now
  issuer_enabled "$unit" || exit 0
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
check_instance "${SITE_CERTS_CONF:-/etc/simple-host-site-certs.conf}" 1 simple-host-site-certs
for conf in ${SITE_CERTS_CONF_GLOB:-/etc/simple-host-site-certs-*.conf}; do
  [ -r "$conf" ] || continue
  # /etc/simple-host-site-certs-site.conf -> simple-host-site-certs-site.{path,timer}
  check_instance "$conf" 0 "$(basename "$conf" .conf)"
done
