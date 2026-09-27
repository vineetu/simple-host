#!/usr/bin/env bash
# simple-host-domain-certs: issue certificates for Simple Host custom domains
# (a person's own domain connected to a site) and write their nginx servers,
# so a domain goes live without the operator. Runs as root from
# simple-host-domain-certs.service (timer every 10 minutes, plus a path unit
# on new requests).
#
# Hand-off with the Go service (which never runs certbot):
#   $STATE/requests/<domain>  written by simple-host once the domain resolves here
#   $STATE/ready/<domain>     written here once nginx serves the certificate
#   $STATE/failed/<domain>    written here with one line saying why; retried
#                             after $RETRY_AFTER seconds (the app shows the line)
#
# The binding itself is the domain's link in the sites' domains/ directory
# ($SITES/<domain> -> by-id/<user>/<site>, made by the app on connect and
# removed on disconnect). A request is honoured only while that link exists,
# and a server this script wrote is removed (with its certificate) once the
# link is gone. Domains the operator configured by hand
# (sites-enabled/customdomain-<domain>) are never touched: they count as ready.
#
# Per request: check that the domain's A record points here and that no
# AAAA record points elsewhere (Let's Encrypt prefers IPv6), then certbot
# HTTP-01 with the webroot every port-80 server here already answers
# (/.well-known/acme-challenge/ from $WEBROOT), then write the server from the
# template, `nginx -t`, reload. Renewals are certbot's normal `certbot renew`
# (the lineage keeps the webroot and the reload hook).
set -euo pipefail

SITE_DOMAIN=simple-host.app
STATE=/var/lib/simple-host-domain-certs
SITES=/srv/simple-host/sites/domains
WEBROOT=/var/www/acme
TEMPLATE=/usr/local/lib/simple-host-domain-certs/vhost.conf.template
AVAILABLE=/etc/nginx/sites-available
ENABLED=/etc/nginx/sites-enabled
PREFIX=simple-host-domain-  # our servers; hand-made ones are customdomain-<domain>
DAILY=50                    # new certificates per rolling 24h
PER_RUN=10
RETRY_AFTER=21600           # seconds before a failed domain is tried again
IP=""                       # this server's IPv4 (the A record handed out); default: the apex's A record
IP6=""                      # this server's IPv6, if AAAA records may point here
[ -r /etc/simple-host-domain-certs.conf ] && . /etc/simple-host-domain-certs.conf

DOMAIN_RE='^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'

log() { echo "domain-certs: $*"; }

exec 9>/run/simple-host-domain-certs.lock
flock -n 9 || { log "another run is in progress"; exit 0; }

install -d -m 0755 -o root -g root "$STATE" "$STATE/ready" "$STATE/failed" "$STATE/owned"
[ -d "$STATE/requests" ] || install -d -m 0755 -o simplehost -g simplehost "$STATE/requests"
install -d -m 0755 "$WEBROOT"
touch "$STATE/issued.log"

if [ -z "$IP" ]; then
  IP=$(dig +short A "$SITE_DOMAIN" | grep -E '^[0-9.]+$' | head -1 || true)
fi
[ -n "$IP" ] || { log "cannot determine this server's address"; exit 1; }
[ -f "$TEMPLATE" ] || { log "template $TEMPLATE missing"; exit 1; }

reload=0
now=$(date +%s)

# fail <domain> <reason> [retry-seconds]: record why (one line, shown to the
# site owner); tried again after retry-seconds (default $RETRY_AFTER).
fail() {
  local d=$1 why=$2 retry=${3:-$RETRY_AFTER}
  why=$(printf '%s' "$why" | tr -d '\r' | tr '\n\t' '  ' | tr -cd '[:print:]' | cut -c1-200)
  printf '%s\n' "$why" > "$STATE/failed/$d.new"
  chmod 0644 "$STATE/failed/$d.new"
  touch -d "@$((now - RETRY_AFTER + retry))" "$STATE/failed/$d.new"
  mv -f "$STATE/failed/$d.new" "$STATE/failed/$d"
  log "$d: $why"
}

# write_server <domain>: our nginx server for it, enabled; 0 when nginx accepts it.
write_server() {
  local d=$1 avail="$AVAILABLE/$PREFIX$1" link="$ENABLED/$PREFIX$1"
  sed "s/__DOMAIN__/$d/g" "$TEMPLATE" > "$avail.new"
  mv -f "$avail.new" "$avail"
  ln -sfn "$avail" "$link"
  if nginx -t >/dev/null 2>&1; then
    reload=1
    return 0
  fi
  rm -f -- "$link" "$avail"
  return 1
}

# Release: our servers whose binding is gone.
if [ -d "$SITES" ]; then
  for f in "$STATE"/ready/*; do
    [ -f "$f" ] || continue
    d=$(basename "$f")
    [ -L "$SITES/$d" ] && continue
    if [ -f "$AVAILABLE/$PREFIX$d" ] || [ -L "$ENABLED/$PREFIX$d" ]; then
      rm -f -- "$ENABLED/$PREFIX$d" "$AVAILABLE/$PREFIX$d"
      reload=1
      log "released $d (disconnected)"
    fi
    if [ -f "$STATE/owned/$d" ]; then
      certbot delete --non-interactive --quiet --cert-name "$d" >/dev/null 2>&1 || log "$d: certbot delete failed"
      rm -f -- "$STATE/owned/$d"
    fi
    rm -f -- "$f" "$STATE/failed/$d"
  done
fi

used_today=$(awk -v t="$((now - 86400))" '$1 >= t' "$STATE/issued.log" | wc -l)
issued_now=0

# Oldest request first.
mapfile -t reqs < <(find "$STATE/requests" -maxdepth 1 -type f -printf '%T@ %f\n' | sort -n | cut -d' ' -f2-)
for d in "${reqs[@]}"; do
  [ -n "$d" ] || continue
  if [ "${#d}" -gt 253 ] || ! [[ "$d" =~ $DOMAIN_RE ]] || [ "$d" = "$SITE_DOMAIN" ] || [[ "$d" == *".$SITE_DOMAIN" ]]; then
    log "dropping invalid request name"
    rm -f -- "$STATE/requests/$d"
    continue
  fi
  if [ ! -L "$SITES/$d" ]; then
    # Not (or no longer) connected to a site.
    rm -f -- "$STATE/requests/$d"
    continue
  fi
  if [ -e "$ENABLED/customdomain-$d" ]; then
    # Configured by hand: served already.
    touch "$STATE/ready/$d"
    rm -f -- "$STATE/requests/$d" "$STATE/failed/$d"
    continue
  fi
  lineage="/etc/letsencrypt/live/$d"
  if [ -f "$lineage/fullchain.pem" ] && [ -f "$lineage/privkey.pem" ]; then
    # Issued before (e.g. reconnected, or a server removed by hand): serve it again.
    if write_server "$d"; then
      touch "$STATE/ready/$d"
      rm -f -- "$STATE/requests/$d" "$STATE/failed/$d"
      log "ready again: $d"
    else
      log "$d: nginx -t failed with this domain's server; removed it"
      fail "$d" "the web server could not be set up for this domain yet"
    fi
    continue
  fi
  if [ -f "$STATE/failed/$d" ] && [ $((now - $(stat -c %Y "$STATE/failed/$d"))) -lt "$RETRY_AFTER" ]; then
    continue
  fi
  if [ "$used_today" -ge "$DAILY" ]; then
    log "daily cap of $DAILY new certificates reached; the rest wait"
    break
  fi
  if [ "$issued_now" -ge "$PER_RUN" ]; then
    log "per-run cap reached; the rest wait for the next run"
    break
  fi

  # DNS first: a validation that cannot succeed only spends Let's Encrypt's
  # failed-validation allowance.
  a=$(dig +short A "$d" | grep -E '^[0-9.]+$' || true)
  if ! grep -qxF "$IP" <<<"$a"; then
    fail "$d" "the domain's DNS does not point to this server yet" 900
    continue
  fi
  aaaa=$(dig +short AAAA "$d" | grep -E '^[0-9a-fA-F:]+$' || true)
  if [ -n "$aaaa" ] && { [ -z "$IP6" ] || ! grep -qixF "$IP6" <<<"$aaaa"; }; then
    fail "$d" "the domain has an IPv6 (AAAA) record that does not point to this server; remove it at the registrar" 900
    continue
  fi

  log "issuing $d"
  err=$(mktemp)
  if certbot certonly --non-interactive --agree-tos --quiet \
      --webroot -w "$WEBROOT" \
      --deploy-hook "systemctl reload nginx" \
      --key-type ecdsa --cert-name "$d" -d "$d" 2>"$err"; then
    rm -f -- "$err"
    echo "$(date +%s) $d" >> "$STATE/issued.log"
    touch "$STATE/owned/$d"
    used_today=$((used_today + 1))
    issued_now=$((issued_now + 1))
    if write_server "$d"; then
      touch "$STATE/ready/$d"
      rm -f -- "$STATE/requests/$d" "$STATE/failed/$d"
      log "ready: $d"
    else
      log "$d: nginx -t failed with this domain's server; removed it"
      fail "$d" "the web server could not be set up for this domain yet"
    fi
  else
    why=$(grep -m1 -E 'Detail:' "$err" | sed 's/.*Detail: *//' || true)
    [ -n "$why" ] || why=$(grep -v '^[[:space:]]*$' "$err" | tail -1 || true)
    rm -f -- "$err"
    fail "$d" "Let's Encrypt refused: ${why:-unknown error}"
  fi
done

if [ "$reload" = 1 ]; then
  nginx -t >/dev/null 2>&1 && systemctl reload nginx || log "nginx reload skipped: configuration test failed"
fi
