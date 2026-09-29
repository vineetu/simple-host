#!/usr/bin/env bash
# simple-host-site-certs-deploy: certbot deploy hook for *.<handle>.<SITE_DOMAIN>
# lineages (issue and every renewal). nginx loads these certificates per
# handshake by variable, i.e. in its worker processes (www-data), which cannot
# read /etc/letsencrypt private keys: copy the pair where they can, then mark
# the person ready for the Go service.
#
# One hook serves every instance (simple-host.app, simple-host.site). Renewals
# carry no environment, so the instance is picked by the lineage's name: first
# /etc/simple-host-site-certs.conf (optional; simple-host.app defaults), then
# each /etc/simple-host-site-certs-*.conf, which must set SITE_DOMAIN, STATE and
# NGINX_CERTS itself. A lineage none of them owns is left alone.
set -euo pipefail
# Overridable for the test (deploy/site-certs/site-certs_test.sh) only.
DEFAULT_CONF=${SITE_CERTS_CONF:-/etc/simple-host-site-certs.conf}
CONF_GLOB=${SITE_CERTS_CONF_GLOB:-/etc/simple-host-site-certs-*.conf}

lineage=${RENEWED_LINEAGE:?}
name=$(basename "$lineage")

# owns <conf> <default|other>: load the instance and say whether the lineage is its.
owns() {
  if [ "$2" = default ]; then
    SITE_DOMAIN=simple-host.app
    STATE=/var/lib/simple-host-site-certs
    NGINX_CERTS=/etc/nginx/simple-host-site-certs
    # shellcheck source=/dev/null
    [ -r "$1" ] && . "$1"
  else
    [ -r "$1" ] || return 1
    SITE_DOMAIN=""; STATE=""; NGINX_CERTS=""
    # shellcheck source=/dev/null
    . "$1"
    [ -n "$SITE_DOMAIN" ] && [ -n "$STATE" ] && [ -n "$NGINX_CERTS" ] || { echo "deploy-hook: $1 must set SITE_DOMAIN, STATE and NGINX_CERTS" >&2; return 1; }
  fi
  [ "${name%."$SITE_DOMAIN"}" != "$name" ]
}

found=0
if owns "$DEFAULT_CONF" default; then
  found=1
else
  for c in $CONF_GLOB; do
    [ "$c" != "$DEFAULT_CONF" ] || continue
    if owns "$c" other; then found=1; break; fi
  done
fi
[ "$found" = 1 ] || exit 0 # not one of ours
h=${name%."$SITE_DOMAIN"}
[[ "$h" =~ ^[a-z0-9]([a-z0-9-]{0,37}[a-z0-9])?$ ]] || exit 0
[ -f "$lineage/fullchain.pem" ] && [ -f "$lineage/privkey.pem" ] || { echo "deploy-hook: $lineage incomplete" >&2; exit 1; }

install -d -m 0750 -o root -g www-data "$NGINX_CERTS" "$NGINX_CERTS/$h"
install -m 0640 -o root -g www-data "$lineage/privkey.pem" "$NGINX_CERTS/$h/.privkey.pem.new"
install -m 0640 -o root -g www-data "$lineage/fullchain.pem" "$NGINX_CERTS/$h/.fullchain.pem.new"
mv -f "$NGINX_CERTS/$h/.privkey.pem.new" "$NGINX_CERTS/$h/privkey.pem"
mv -f "$NGINX_CERTS/$h/.fullchain.pem.new" "$NGINX_CERTS/$h/fullchain.pem"

install -d -m 0755 -o root -g root "$STATE/ready"
install -m 0644 -o root -g root /dev/null "$STATE/ready/$h"
