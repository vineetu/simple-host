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
put "$here/cert-issuers/google-certbot.py" /usr/local/lib/simple-host-cert-issuers/google-certbot.py 0644
put "$here/cert-issuers/google-certbot" /usr/local/lib/simple-host-cert-issuers/google-certbot 0755
put "$here/cert-issuers/runtime.sh" /usr/local/lib/simple-host-cert-issuers/runtime.sh 0644
put "$here/cert-issuers/requeue.py" /usr/local/lib/simple-host-cert-issuers/requeue.py 0644
put "$here/cert-issuers/certbot-locked" /usr/local/lib/simple-host-cert-issuers/certbot-locked 0755
put "$here/cert-issuers/watch.sh" /usr/local/lib/simple-host-cert-issuers/watch.sh 0755
put "$here/cert-issuers/simple-host-cert-watch.service" /etc/systemd/system/simple-host-cert-watch.service 0644
put "$here/cert-issuers/simple-host-cert-watch.timer" /etc/systemd/system/simple-host-cert-watch.timer 0644
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
# Explicit hosted-box migration; ordinary installs preserve operator configs.
if [ "${1:-}" = --google-default ]; then
  for conf in /etc/simple-host-cert-issuers.conf /etc/simple-host-site-certs.conf /etc/simple-host-site-certs-site.conf /etc/simple-host-site-certs-hack.conf; do
    [ -e "$conf" ] || continue
    install -d -m 0700 "$backup$(dirname "$conf")"
    cp -p -- "$conf" "$backup$conf"
    python3 - "$conf" <<'PYCONFIG'
from pathlib import Path
import re, sys
p = Path(sys.argv[1])
s = p.read_text()
settings = {'CERT_FALLBACK_CA': 'google,zerossl'} if p.name == 'simple-host-cert-issuers.conf' else {'BUDGET': '10000', 'DAILY': '1000', 'PER_RUN': '30'}
for key, value in settings.items():
    line = key + '=' + value
    if re.search(r'^' + key + '=', s, re.M):
        s = re.sub(r'^' + key + '=.*$', line, s, flags=re.M)
    else:
        s += '\n' + line + '\n'
p.write_text(s)
PYCONFIG
  done
fi
put "$here/cert-issuers/certbot-google-pacing.conf" /etc/systemd/system/certbot.service.d/zz-google-pacing.conf 0644
systemctl daemon-reload
systemctl enable --now simple-host-cert-watch.timer
# Each enabled issuer path also needs a timer to retry failures without a
# second site creation. Leave wholly disabled instances disabled.
for timer in simple-host-site-certs.timer simple-host-site-certs-site.timer simple-host-site-certs-hack.timer simple-host-domain-certs.timer; do
  if systemctl is-active --quiet "$timer" || systemctl is-enabled --quiet "${timer%.timer}.path"; then
    systemctl enable --now "$timer"
    systemctl restart "$timer"
  fi
done
echo "issuer scripts and units installed; backups: $backup"
