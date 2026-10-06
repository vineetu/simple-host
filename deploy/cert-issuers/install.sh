#!/usr/bin/env bash
# Install the root issuer scripts and their existing units, with dated backups.
# No nginx configuration is read or written; templates retain lineage paths.
set -euo pipefail
[ "$(id -u)" = 0 ] || { echo 'run as root' >&2; exit 1; }
here=$(cd "$(dirname "$0")/.." && pwd)
backup="/var/backups/simple-host-cert-issuers/$(date -u +%Y%m%d-%H%M%S)"
put() {
  local src=$1 dest=$2 mode=$3
  if [ -e "$dest" ]; then
    install -d -m 0700 "$backup$(dirname "$dest")"
    cp -p -- "$dest" "$backup$dest"
  fi
  install -D -m "$mode" -o root -g root "$src" "$dest"
}
put "$here/cert-issuers/fallback.sh" /usr/local/lib/simple-host-cert-issuers/fallback.sh 0644
put "$here/site-certs/issue.sh" /usr/local/sbin/simple-host-site-certs 0755
put "$here/site-certs/deploy-hook.sh" /usr/local/sbin/simple-host-site-certs-deploy 0755
put "$here/site-certs/dns.py" /usr/local/sbin/simple-host-site-certs-dns 0755
put "$here/domain-certs/issue.sh" /usr/local/sbin/simple-host-domain-certs 0755
put "$here/domain-certs/vhost.conf.template" /usr/local/lib/simple-host-domain-certs/vhost.conf.template 0644
for f in "$here/site-certs/"*.service "$here/site-certs/"*.path "$here/site-certs/"*.timer \
         "$here/domain-certs/"*.service "$here/domain-certs/"*.path "$here/domain-certs/"*.timer; do
  put "$f" "/etc/systemd/system/$(basename "$f")" 0644
done
if [ ! -e /etc/simple-host-cert-issuers.conf ]; then
  put "$here/cert-issuers/config.example" /etc/simple-host-cert-issuers.conf 0644
fi
systemctl daemon-reload
echo "issuer scripts and units installed; backups: $backup"
