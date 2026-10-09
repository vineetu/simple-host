#!/usr/bin/env bash
# Every <label>.<SITE_DOMAIN> that nginx answers with an exact server_name never
# reaches the app, so a site must not be able to claim it as its free address.
# reservedSubdomainLabels in internal/handler/platformsubdomain.go is the single
# source of truth; this compares it with the nginx config on this box (read-only)
# and fails when nginx serves a single-label name the list does not reserve.
#
#   bash scripts/check-reserved-subdomains.sh        (SITE_DOMAIN defaults to simple-host.app,
#                                                     SITE_BASE_DOMAIN to simple-host.site)
# Both domains are checked: people's addresses move to SITE_BASE_DOMAIN
# (docs/history/site-base-domain-move.md) and a claim there is refused the same way.
set -u
cd "$(dirname "$0")/.."
DOMAINS="${SITE_DOMAIN:-simple-host.app} ${SITE_BASE_DOMAIN:-simple-host.site}"
CONF=${NGINX_CONF_DIR:-/etc/nginx/sites-enabled}

reserved=$(awk '/^var reservedSubdomainLabels/,/^}/' internal/handler/platformsubdomain.go | grep -oE '"[a-z0-9-]+"' | tr -d '"' | sort -u)
if [ -z "$reserved" ]; then
  echo "FAIL: could not read reservedSubdomainLabels"; exit 1
fi
if [ ! -r "$CONF" ]; then
  echo "skip — $CONF not readable here; reserved list has $(echo "$reserved" | wc -l) names"; exit 0
fi
fail=0
for DOMAIN in $DOMAINS; do
  esc=$(printf '%s' "$DOMAIN" | sed 's/\./\\./g')
  served=$(cat "$CONF"/* 2>/dev/null | grep -E '^\s*server_name' | tr ' ;' '\n\n' \
    | grep -E "^[a-z0-9-]+\.$esc$" | sed "s/\.$esc$//" | sort -u)
  dfail=0
  for label in $served; do
    if ! echo "$reserved" | grep -qx "$label"; then
      echo "  FAIL: nginx serves $label.$DOMAIN itself but it is not in reservedSubdomainLabels"; dfail=1; fail=1
    fi
  done
  [ "$dfail" -eq 0 ] && echo "ok — every nginx-served <name>.$DOMAIN is reserved ($(echo "$served" | wc -w) checked)"
done
exit $fail
