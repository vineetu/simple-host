#!/usr/bin/env bash
# simple-host-domain-certs: issue certificates for Simple Host custom domains
# (a person's own domain connected to a site) and write their nginx servers,
# so a domain goes live without the operator. Runs as root from
# simple-host-domain-certs.service (timer every 10 minutes, plus a path unit
# on new requests).
#
# Hand-off with the Go service (which never runs certbot):
#   $STATE/requests/<domain>  written by simple-host once the domain resolves here
#                             and its ownership record matched: line 1 the site's
#                             token, line 2 the domain link's target
#   $STATE/ready/<domain>     written here once nginx serves the certificate
#   $STATE/failed/<domain>    written here with one line saying why; retried
#                             after $RETRY_AFTER seconds (the app shows the line)
#
# The binding itself is the domain's link in the sites' domains/ directory
# ($SITES/<domain> -> ../by-id/<user>/<site>, made by the app on connect and
# removed on disconnect or lapse). A request is honoured only while that link
# exists and still points where the request says, and a server this script
# wrote is removed (with its certificate) once the link is gone.
#
# Names already served here are never taken: before acting on a domain, the
# full `nginx -T` configuration is read, and a domain that any server this
# script did not write would answer (an exact server_name, a wildcard *.x /
# .x / x.*, or a regex ~...) is marked failed ("already served here"), never
# ready, and a server of ours for it is withdrawn. A certificate is reused
# only when this script issued it ($STATE/owned/<domain>); a lineage in
# /etc/letsencrypt/live it did not create means the name is someone else's.
#
# Per request: the ownership record (TXT _simple-host.<domain> = the token in
# the request) must match; a taken-down site's domain is skipped. Then check
# that the domain's A record points here and that no
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
LE_LIVE=/etc/letsencrypt/live
LOCK=/run/simple-host-domain-certs.lock
PREFIX=simple-host-domain-  # our servers; anything else naming the domain is hand-made
DAILY=50                    # new certificates per rolling 24h
PER_RUN=10
RETRY_AFTER=21600           # seconds before a failed domain is tried again
IP=""                       # this server's IPv4 (the A record handed out); default: the apex's A record
IP6=""                      # this server's IPv6, if AAAA records may point here
CONF=${SIMPLE_HOST_DOMAIN_CERTS_CONF:-/etc/simple-host-domain-certs.conf}  # override: tests only
[ -r "$CONF" ] && . "$CONF"

DOMAIN_RE='^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'

log() { echo "domain-certs: $*"; }

exec 9>"$LOCK"
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

# The whole nginx configuration (read once per run, never printed: it holds
# secrets). NGINX_OK=0 when it cannot be read: nothing is issued that run.
NGINX_CONF=""
NGINX_OK=0
if NGINX_CONF=$(nginx -T 2>/dev/null); then
  NGINX_OK=1
else
  log "cannot read the nginx configuration (nginx -T); nothing is issued this run"
fi

# served_elsewhere <domain>: 0 (and the file's name on stdout) when a server
# in the nginx configuration that this script did not write would answer the
# domain: an exact server_name, *.parent / .parent / name.*, or a regex
# (~...). Fails closed: a configuration it cannot parse, or a regex it cannot
# evaluate, counts as served.
served_elsewhere() {
  printf '%s' "$NGINX_CONF" | perl -e '
    use strict; use warnings;
    my ($d, $prefix) = @ARGV; $d = lc $d;
    local $/; my $all = <STDIN> // "";
    my @parts = split /^# configuration file (.+?):[ \t\r]*$/m, $all;
    shift @parts;
    sub hit {
      my ($n) = @_;
      return 0 if $n eq "" || $n eq "_" || $n =~ /^\$/;
      if ($n =~ /^~(.*)$/s) {
        my $re = $1;
        my $r = eval { $d =~ /$re/i ? 1 : 0 };
        return defined $r ? $r : 1;
      }
      $n = lc $n;
      return 1 if $n eq $d;
      if ($n =~ /^\*(\..+)$/) { my $s = $1; return length($d) > length($s) && substr($d, -length($s)) eq $s ? 1 : 0 }
      if ($n =~ /^\.(.+)$/)   { my $p = $1; return ($d eq $p || (length($d) > length($n) && substr($d, -length($n)) eq $n)) ? 1 : 0 }
      if ($n =~ /^(.+\.)\*$/) { my $p = $1; return length($d) > length($p) && index($d, $p) == 0 ? 1 : 0 }
      return 0;
    }
    while (@parts) {
      my $file = shift @parts; my $body = shift(@parts) // "";
      (my $base = $file) =~ s{.*/}{};
      next if index($base, $prefix) == 0;
      my @tok;
      pos($body) = 0;
      while (pos($body) < length($body)) {
        if    ($body =~ /\G\s+/gc) {}
        elsif ($body =~ /\G#[^\n]*/gc) {}
        elsif ($body =~ /\G"((?:[^"\\]|\\.)*)"/gc) { (my $v = $1) =~ s/\\(["\x27\\])/$1/g; push @tok, [$v, 1] }
        elsif ($body =~ /\G\x27((?:[^\x27\\]|\\.)*)\x27/gc) { (my $v = $1) =~ s/\\(["\x27\\])/$1/g; push @tok, [$v, 1] }
        elsif ($body =~ /\G([;{}])/gc) { push @tok, [$1, 0] }
        elsif ($body =~ /\G([^\s;{}"\x27#][^\s;{}]*)/gc) { push @tok, [$1, 0] }
        else { print "$base\n"; exit 0 }
      }
      for my $i (0 .. $#tok) {
        next unless !$tok[$i][1] && $tok[$i][0] eq "server_name";
        next unless $i == 0 || (!$tok[$i-1][1] && $tok[$i-1][0] =~ /^[;{}]$/);
        for (my $j = $i + 1; $j <= $#tok && !(!$tok[$j][1] && $tok[$j][0] eq ";"); $j++) {
          if (hit($tok[$j][0])) { print "$base\n"; exit 0 }
        }
      }
    }
    exit 1;
  ' "$1" "$PREFIX"
}

# withdraw_ours <domain>: remove the server this script wrote for it, if any.
withdraw_ours() {
  if [ -e "$AVAILABLE/$PREFIX$1" ] || [ -L "$ENABLED/$PREFIX$1" ]; then
    rm -f -- "$ENABLED/$PREFIX$1" "$AVAILABLE/$PREFIX$1"
    reload=1
  fi
}

# txt_ok <domain> <token>: the ownership record _simple-host.<domain> holds token.
txt_ok() {
  dig +short TXT "_simple-host.$1" | tr -d '" ' | grep -qxF -- "$2"
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
    if [ -L "$SITES/$d" ]; then
      # Still bound, but another server here names it now: never ours.
      if [ "$NGINX_OK" = 1 ] && who=$(served_elsewhere "$d"); then
        withdraw_ours "$d"
        rm -f -- "$f"
        log "$d: named by ${who:-another server}"
        fail "$d" "this name is already served here by another site on this server"
      fi
      continue
    fi
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
  # A certificate of ours whose binding is gone (also one whose server was
  # withdrawn because another server names the domain): delete it.
  for f in "$STATE"/owned/*; do
    [ -f "$f" ] || continue
    d=$(basename "$f")
    [ -L "$SITES/$d" ] && continue
    withdraw_ours "$d"
    certbot delete --non-interactive --quiet --cert-name "$d" >/dev/null 2>&1 || log "$d: certbot delete failed"
    rm -f -- "$f"
  done
  # A failure note outlives its request: drop it once the binding is gone.
  for f in "$STATE"/failed/*; do
    [ -f "$f" ] || continue
    d=$(basename "$f")
    [ -L "$SITES/$d" ] || rm -f -- "$f"
  done
fi

used_today=$(awk -v t="$((now - 86400))" '$1 >= t' "$STATE/issued.log" | wc -l)
issued_now=0

# Oldest request first.
mapfile -d '' -t reqs < <(find "$STATE/requests" -maxdepth 1 -type f -printf '%T@ %f\0' | sort -zn | cut -z -d' ' -f2-)
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
  # The request names the site's token and the link target it was made for.
  body=$(head -c 512 -- "$STATE/requests/$d" 2>/dev/null || true)
  token=$(sed -n 1p <<<"$body")
  target=$(sed -n 2p <<<"$body")
  if ! [[ "$token" =~ ^sh-[0-9a-f]{32}$ ]] || [ -z "$target" ]; then
    # Old-style or half-written: the app rewrites it on its next check.
    continue
  fi
  if [ "$(readlink -- "$SITES/$d")" != "$target" ]; then
    # The domain was bound to another site since: not this request's to act on.
    continue
  fi
  if [ -e "$SITES/$d/suspended" ]; then
    # Taken down by the operator: no certificate.
    rm -f -- "$STATE/requests/$d"
    continue
  fi
  if [ "$NGINX_OK" != 1 ]; then
    continue
  fi
  if who=$(served_elsewhere "$d"); then
    # Another server here answers this name (the operator's, or a customer's
    # set up by hand). Never ours, never ready.
    withdraw_ours "$d"
    rm -f -- "$STATE/ready/$d"
    log "$d: named by ${who:-another server}"
    fail "$d" "this name is already served here by another site on this server"
    continue
  fi
  if ! txt_ok "$d" "$token"; then
    fail "$d" "the ownership record (TXT _simple-host.$d) is not seen or does not match yet" 900
    continue
  fi
  lineage="$LE_LIVE/$d"
  if [ -e "$lineage" ] && [ ! -f "$STATE/owned/$d" ]; then
    # A certificate this script did not issue: someone else's name.
    fail "$d" "this name is already served here (it has a certificate on this server that Simple Host did not issue)"
    continue
  fi
  if [ -f "$STATE/owned/$d" ] && [ -f "$lineage/fullchain.pem" ] && [ -f "$lineage/privkey.pem" ]; then
    # Issued by us before (reconnected, or a server removed by hand): serve it again.
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
