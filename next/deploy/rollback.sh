#!/usr/bin/env bash
# Go back to the old Simple Host: web server entries back, new service stopped, old one started.
set -uo pipefail
cd /etc/nginx/sites-enabled
sudo rm -f simple-host-next zz-simple-host-next-test
for f in /etc/nginx/sites-old/*; do [ -e "$f" ] && sudo mv "$f" /etc/nginx/sites-enabled/; done
[ -f /var/backups/ielts-vhost-pre-switch ] && sudo cp -p /var/backups/ielts-vhost-pre-switch /etc/nginx/sites-available/ielts.vineetsriram.com
sudo nginx -t && sudo systemctl reload nginx
sudo systemctl stop simple-host-next
sudo systemctl start simple-host
sudo systemctl start simple-host-domain-certs.timer simple-host-family-certs.timer simple-host-domain-certs.path simple-host-family-certs.path 2>/dev/null
sleep 3; systemctl is-active simple-host; curl -s -o /dev/null -w "simple-host.app %{http_code}\n" https://simple-host.app/
