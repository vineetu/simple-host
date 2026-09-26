#!/usr/bin/env bash
# simple-host-site-certs: issue *.<handle>.<SITE_DOMAIN> certificates for
# Simple Host site hosts (<site>.<handle>.<SITE_DOMAIN>, owner decision
# 2026-09-26). Runs as root from simple-host-site-certs.service (timer every
# 10 minutes, plus a path unit on new requests).
#
# Hand-off with the Go service (which never runs certbot):
#   $STATE/requests/<handle>  written by simple-host (a name, nothing else)
#   $STATE/ready/<handle>     written here once nginx serves the certificate
#
# Per request: make sure <handle> and *.<handle> have explicit A records (the
# DNS-01 TXT record at _acme-challenge.<handle> would otherwise turn <handle>
# into an empty non-terminal and hide it from the zone wildcard), then certbot
# DNS-01 with the Vercel hooks. Renewals are certbot's normal `certbot renew`
# (the lineage keeps the hooks and the deploy hook). New certificates are
# capped at $BUDGET per rolling 7 days (Let's Encrypt allows 50 per registered
# domain per week) and $PER_RUN per run; the rest wait in the queue.
# Never prints the DNS token.
set -euo pipefail

SITE_DOMAIN=simple-host.app
STATE=/var/lib/simple-host-site-certs
BUDGET=40
DAILY=12          # new certificates per rolling 24h, so a burst of sign-ups cannot spend the week at once
PER_RUN=6
RETRY_AFTER=21600 # seconds before a failed handle is tried again
IP=""             # A record target; default: the zone apex's A record
[ -r /etc/simple-host-site-certs.conf ] && . /etc/simple-host-site-certs.conf

HOOKS=/usr/local/lib/certbot-vercel
DEPLOY_HOOK=/usr/local/sbin/simple-host-site-certs-deploy
DNS_HELPER=/usr/local/sbin/simple-host-site-certs-dns
LABEL_RE='^[a-z0-9]([a-z0-9-]{0,37}[a-z0-9])?$'
# Names that are never a person (the Go service refuses them as handles too).
RESERVED=" www sites lab api admin mail cname app auth mcp static cdn docs status test dev localhost _acme-challenge "

log() { echo "site-certs: $*"; }

exec 9>/run/simple-host-site-certs.lock
flock -n 9 || { log "another run is in progress"; exit 0; }

install -d -m 0755 -o root -g root "$STATE" "$STATE/ready"
install -d -m 0700 -o root -g root "$STATE/failed"
[ -d "$STATE/requests" ] || install -d -m 0755 -o simplehost -g simplehost "$STATE/requests"
touch "$STATE/issued.log"

if [ -z "$IP" ]; then
  IP=$(dig +short +norecurse A "$SITE_DOMAIN" @ns1.vercel-dns.com | grep -E '^[0-9.]+$' | head -1 || true)
fi
[ -n "$IP" ] || { log "cannot determine the A record target"; exit 1; }

now=$(date +%s)
week_ago=$((now - 7 * 86400))
used=$(awk -v t="$week_ago" '$1 >= t' "$STATE/issued.log" | wc -l)
used_today=$(awk -v t="$((now - 86400))" '$1 >= t' "$STATE/issued.log" | wc -l)
issued_now=0

# Oldest request first.
mapfile -t reqs < <(find "$STATE/requests" -maxdepth 1 -type f -printf '%T@ %f\n' | sort -n | cut -d' ' -f2-)
for h in "${reqs[@]}"; do
  [ -n "$h" ] || continue
  if ! [[ "$h" =~ $LABEL_RE ]] || [[ "$RESERVED" == *" $h "* ]] || [[ "$h" == xn--* ]]; then
    log "dropping invalid request name"
    rm -f -- "$STATE/requests/$h"
    continue
  fi
  lineage="/etc/letsencrypt/live/$h.$SITE_DOMAIN"
  if [ -f "$lineage/fullchain.pem" ]; then
    # Already issued (e.g. restored, or a marker cleared by hand): make sure
    # its DNS records exist (renewals need them) and redeploy it.
    "$DNS_HELPER" ensure "$h" "$SITE_DOMAIN" "$IP" || { log "DNS records for $h failed"; continue; }
    RENEWED_LINEAGE="$lineage" "$DEPLOY_HOOK"
    rm -f -- "$STATE/requests/$h"
    continue
  fi
  if [ -f "$STATE/failed/$h" ] && [ $((now - $(stat -c %Y "$STATE/failed/$h"))) -lt "$RETRY_AFTER" ]; then
    continue
  fi
  if [ "$used" -ge "$BUDGET" ]; then
    log "weekly budget of $BUDGET new certificates used; ${#reqs[@]} request(s) wait"
    break
  fi
  if [ "$used_today" -ge "$DAILY" ]; then
    log "daily cap of $DAILY new certificates reached; the rest wait"
    break
  fi
  if [ "$issued_now" -ge "$PER_RUN" ]; then
    log "per-run cap reached; the rest wait for the next run"
    break
  fi
  log "issuing *.$h.$SITE_DOMAIN"
  if ! "$DNS_HELPER" ensure "$h" "$SITE_DOMAIN" "$IP"; then
    log "DNS records for $h failed"
    touch "$STATE/failed/$h"
    continue
  fi
  if certbot certonly --non-interactive --agree-tos --quiet \
      --manual --preferred-challenges dns \
      --manual-auth-hook "$HOOKS/auth-hook.sh" --manual-cleanup-hook "$HOOKS/cleanup-hook.sh" \
      --deploy-hook "$DEPLOY_HOOK" \
      --key-type ecdsa --cert-name "$h.$SITE_DOMAIN" -d "*.$h.$SITE_DOMAIN"; then
    echo "$(date +%s) $h" >> "$STATE/issued.log"
    used=$((used + 1))
    used_today=$((used_today + 1))
    issued_now=$((issued_now + 1))
    rm -f -- "$STATE/requests/$h" "$STATE/failed/$h"
    if [ -f "$STATE/ready/$h" ]; then log "ready: *.$h.$SITE_DOMAIN"; else log "issued but not deployed: $h"; fi
  else
    log "certbot failed for $h; retry in $((RETRY_AFTER / 3600))h"
    touch "$STATE/failed/$h"
  fi
done
