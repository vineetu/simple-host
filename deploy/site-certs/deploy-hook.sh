#!/usr/bin/env bash
# simple-host-site-certs-deploy: certbot deploy hook for *.<handle>.<SITE_DOMAIN>
# lineages (issue and every renewal). nginx loads these certificates per
# handshake by variable, i.e. in its worker processes (www-data), which cannot
# read /etc/letsencrypt private keys: copy the pair where they can, then mark
# the person ready for the Go service.
set -euo pipefail
SITE_DOMAIN=simple-host.app
STATE=/var/lib/simple-host-site-certs
NGINX_CERTS=/etc/nginx/simple-host-site-certs
[ -r /etc/simple-host-site-certs.conf ] && . /etc/simple-host-site-certs.conf

lineage=${RENEWED_LINEAGE:?}
name=$(basename "$lineage")
h=${name%.$SITE_DOMAIN}
[ "$h" != "$name" ] || exit 0 # not one of ours
[[ "$h" =~ ^[a-z0-9]([a-z0-9-]{0,37}[a-z0-9])?$ ]] || exit 0
[ -f "$lineage/fullchain.pem" ] && [ -f "$lineage/privkey.pem" ] || { echo "deploy-hook: $lineage incomplete" >&2; exit 1; }

install -d -m 0750 -o root -g www-data "$NGINX_CERTS" "$NGINX_CERTS/$h"
install -m 0640 -o root -g www-data "$lineage/privkey.pem" "$NGINX_CERTS/$h/.privkey.pem.new"
install -m 0640 -o root -g www-data "$lineage/fullchain.pem" "$NGINX_CERTS/$h/.fullchain.pem.new"
mv -f "$NGINX_CERTS/$h/.privkey.pem.new" "$NGINX_CERTS/$h/privkey.pem"
mv -f "$NGINX_CERTS/$h/.fullchain.pem.new" "$NGINX_CERTS/$h/fullchain.pem"

install -d -m 0755 -o root -g root "$STATE/ready"
install -m 0644 -o root -g root /dev/null "$STATE/ready/$h"
