#!/usr/bin/env bash
# simple-host-family-certs: write the nginx servers for Simple Host address
# families, so a family goes live without the operator. Runs as root from
# simple-host-family-certs.service (timer every 10 minutes, plus a path unit
# on the requests directory).
#
# An address family: an account connects *.<suffix> (e.g.
# *.voucher.chhotabreak.com), and each of its sites named <prefix><label>
# answers at <label>.<suffix>. Release 1 serves only cert mode "wildcard":
# the operator already holds a wildcard certificate lineage in
# $LE_LIVE/<cert name> (renewed by certbot with its own DNS hooks). This
# script NEVER issues, renews or deletes a certificate; it only checks that
# the lineage covers the family and has not expired.
#
# Hand-off with the Go service:
#   $SITES/<suffix>           the binding: a link to ../by-id/<user_id>, made
#                             by the app when the family is verified, removed
#                             on disconnect or lapse
#   $STATE/requests/<suffix>  written by the app and kept while the family
#                             exists (rewritten when it changes, deleted on
#                             release). Lines: 1 token sh-<32 hex>, 2 the
#                             link target, 3 cert mode (wildcard | per_host),
#                             4 cert name (lineage), 5 site prefix (may be
#                             empty), 6 reserved labels, space separated
#                             (may be empty)
#   $STATE/ready/<suffix>     written here once nginx serves the family with
#                             our file: prefix=<p>, cert=<name>,
#                             expires=<unix seconds of notAfter>,
#                             reserved=<labels> (the app treats the family as
#                             live only while its prefix line matches)
#   $STATE/failed/<suffix>    one line saying why not (shown to the owner)
#
# A request is honoured only while the link exists and still points where the
# request says; otherwise, and once the request is gone, our server and the
# ready marker are removed (certificates are never touched).
#
# Names already served here are never taken: the full `nginx -T`
# configuration is read once per run, and a family that any server this
# script did not write would answer (tested with a probe name
# sh-probe-<random>.<suffix> against exact, *.x, .x, x.* and ~regex names)
# is marked failed and ours withdrawn. A hand-made wildcard vhost is moved to
# our file with deploy/prod/family-adopt.sh.
#
# Other modes (read-only; used by family-adopt.sh so both write the same):
#   simple-host-family-certs --render <suffix>   the server file for the
#                                                 current request, on stdout
#   simple-host-family-certs --ready <suffix>    the ready marker it would
#                                                 write (checks the lineage)
set -euo pipefail

SITE_DOMAIN=simple-host.app
STATE=/var/lib/simple-host-family-certs
SITES=/srv/simple-host/sites/families
TEMPLATE=/usr/local/lib/simple-host-family-certs/vhost.conf.template
AVAILABLE=/etc/nginx/sites-available
ENABLED=/etc/nginx/sites-enabled
LE_LIVE=/etc/letsencrypt/live
LOCK=/run/simple-host-family-certs.lock
PREFIX=simple-host-family-  # our servers; anything else naming the family is hand-made
WARN_DAYS=14                # log a warning when the lineage expires sooner than this
PLATFORM_ZONES="simple-host.app simple-host.site simple-hack.app"  # never a family
CONF=${SIMPLE_HOST_FAMILY_CERTS_CONF:-/etc/simple-host-family-certs.conf}  # override: tests only
# shellcheck source=/dev/null
[ -r "$CONF" ] && . "$CONF"

DOMAIN_RE='^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'
TARGET_RE='^\.\./by-id/[0-9a-f-]{1,64}$'
CERT_RE='^[a-z0-9][a-z0-9.-]{0,252}$'
PREFIX_RE='^([a-z0-9][a-z0-9-]{0,39})?$'
RESERVED_RE='^([a-z0-9-]{1,63}( [a-z0-9-]{1,63})*)?$'

log() { echo "family-certs: $*"; }

# platform <name>: true for SITE_DOMAIN or a PLATFORM_ZONES zone, or any name
# under one of them (served by the platform's own servers and certificates).
platform() {
  local z
  for z in $SITE_DOMAIN $PLATFORM_ZONES; do
    [ "$1" = "$z" ] || [[ "$1" == *".$z" ]] && return 0
  done
  return 1
}

# valid_name <suffix>: a DNS name of at least two labels, not a platform zone.
valid_name() {
  [ "${#1}" -le 253 ] && [[ "$1" =~ $DOMAIN_RE ]] && [[ "$1" == *.* ]] && ! platform "$1"
}

# parse_request <suffix>: read $STATE/requests/<suffix> into R_TOKEN,
# R_TARGET, R_MODE, R_CERT, R_PREFIX, R_RESERVED. Returns 2 when the token or
# target is unreadable (half-written: the app rewrites it), 1 with ERR set
# when a setting is invalid, 0 when the request is good to serve.
parse_request() {
  local f="$STATE/requests/$1" body
  local -a l=()
  ERR=""
  [ -f "$f" ] && [ ! -L "$f" ] || { ERR="no request"; return 2; }
  body=$(head -c 4096 -- "$f" 2>/dev/null || true)
  [ -n "$body" ] && mapfile -t l <<<"$body"
  R_TOKEN=${l[0]:-}; R_TARGET=${l[1]:-}; R_MODE=${l[2]:-}; R_CERT=${l[3]:-}
  R_PREFIX=${l[4]:-}; R_RESERVED=${l[5]:-}
  if ! [[ "$R_TOKEN" =~ ^sh-[0-9a-f]{32}$ ]] || ! [[ "$R_TARGET" =~ $TARGET_RE ]]; then
    ERR="the request is incomplete"; return 2
  fi
  # Reserved labels: single spaces, none at either end.
  R_RESERVED=$(printf '%s' "$R_RESERVED" | tr -s ' ' | sed -e 's/^ //' -e 's/ $//')
  if [ "${#l[@]}" -gt 6 ] || [[ "$body" == *$'\r'* ]]; then
    ERR="the family's settings could not be read"; return 1
  fi
  case $R_MODE in
    wildcard) ;;
    per_host) ERR="certificates per site name are not available yet"; return 1 ;;
    *) ERR="the family's certificate mode is not recognised"; return 1 ;;
  esac
  [[ "$R_CERT" =~ $CERT_RE ]] || { ERR="the family's certificate name is not valid"; return 1; }
  [[ "$R_PREFIX" =~ $PREFIX_RE ]] || { ERR="the family's site prefix is not valid"; return 1; }
  [[ "$R_RESERVED" =~ $RESERVED_RE ]] || { ERR="the family's reserved labels are not valid"; return 1; }
  return 0
}

# check_lineage <suffix>: the lineage R_CERT holds a key and a certificate
# that names *.<suffix> and has not expired; sets EXPIRES (unix seconds).
# 1 with ERR set otherwise.
check_lineage() {
  local s=$1 dir="$LE_LIVE/$R_CERT" end
  ERR=""; EXPIRES=""
  if [ ! -f "$dir/fullchain.pem" ] || [ ! -f "$dir/privkey.pem" ]; then
    ERR="the certificate $R_CERT is not on this server"; return 1
  fi
  if ! openssl x509 -in "$dir/fullchain.pem" -noout -ext subjectAltName 2>/dev/null \
      | grep -oE 'DNS:[^,[:space:]]+' | grep -qxF "DNS:*.$s"; then
    ERR="the certificate $R_CERT does not cover *.$s"; return 1
  fi
  if ! openssl x509 -in "$dir/fullchain.pem" -noout -checkend 0 >/dev/null 2>&1; then
    ERR="the certificate $R_CERT has expired"; return 1
  fi
  end=$(openssl x509 -in "$dir/fullchain.pem" -noout -enddate 2>/dev/null | sed -n 's/^notAfter=//p')
  EXPIRES=$(date -u -d "$end" +%s 2>/dev/null || true)
  [[ "$EXPIRES" =~ ^[0-9]+$ ]] || { ERR="the certificate $R_CERT could not be read"; return 1; }
  if ! openssl x509 -in "$dir/fullchain.pem" -noout -checkend $((WARN_DAYS * 86400)) >/dev/null 2>&1; then
    log "warning: $s: the certificate $R_CERT expires $(date -u -d "@$EXPIRES" '+%Y-%m-%d %H:%M UTC') (within $WARN_DAYS days)"
  fi
  return 0
}

# render <suffix>: the server file for the parsed request, on stdout.
render() {
  perl -e '
    use strict; use warnings;
    my ($tpl, $s, $p, $c, $r) = @ARGV;
    open(my $fh, "<", $tpl) or die "template $tpl: $!\n";
    local $/; my $t = <$fh>; close $fh;
    (my $sre = $s) =~ s/\./\\./g;
    if ($r eq "") {
      # No reserved labels: the whole block goes, marker lines included.
      $t =~ s/^[^\n]*__RESERVED_BEGIN__.*?__RESERVED_END__[^\n]*\n//ms;
    } else {
      $t =~ s/^[^\n]*__RESERVED_(?:BEGIN|END)__[^\n]*\n//mg;
      (my $re = $r) =~ s/ /|/g;
      $t =~ s/__RESERVED_RE__/$re/g;
    }
    $t =~ s/__SUFFIX_RE__/$sre/g;
    $t =~ s/__SUFFIX__/$s/g;
    $t =~ s/__PREFIX__/$p/g;
    $t =~ s/__CERT__/$c/g;
    die "template placeholder left unrendered\n" if $t =~ /__[A-Z_]+__/;
    print $t;
  ' "$TEMPLATE" "$1" "$R_PREFIX" "$R_CERT" "$R_RESERVED"
}

# ready_body: the ready marker for the parsed request and checked lineage.
ready_body() {
  printf 'prefix=%s\ncert=%s\nexpires=%s\nreserved=%s\n' "$R_PREFIX" "$R_CERT" "$EXPIRES" "$R_RESERVED"
}

# --render / --ready <suffix>: read-only, for family-adopt.sh.
if [ "${1:-}" = --render ] || [ "${1:-}" = --ready ]; then
  mode=$1 s=${2:-}
  [ $# -eq 2 ] || { echo "usage: $0 --render|--ready <suffix>" >&2; exit 2; }
  valid_name "$s" || { echo "$s: not a family name that can be served" >&2; exit 1; }
  [ -f "$TEMPLATE" ] || { echo "template $TEMPLATE missing" >&2; exit 1; }
  parse_request "$s" || { echo "$s: $ERR" >&2; exit 1; }
  if [ "$mode" = --render ]; then
    render "$s"
  else
    check_lineage "$s" >&2 || { echo "$s: $ERR" >&2; exit 1; }
    ready_body
  fi
  exit 0
fi
[ $# -eq 0 ] || { echo "usage: $0 [--render|--ready <suffix>]" >&2; exit 2; }

exec 9>"$LOCK"
flock -n 9 || { log "another run is in progress"; exit 0; }

install -d -m 0755 -o root -g root "$STATE" "$STATE/ready" "$STATE/failed"
[ -d "$STATE/requests" ] || install -d -m 0755 -o simplehost -g simplehost "$STATE/requests"
[ -f "$TEMPLATE" ] || { log "template $TEMPLATE missing"; exit 1; }
# Without the families directory every binding looks gone: do nothing rather
# than withdraw every family (a missing mount, a fresh box).
[ -d "$SITES" ] || { log "$SITES missing; nothing to do"; exit 0; }

reload=0
pending=()   # suffixes whose ready marker is written once nginx has reloaded

# put <file> <content-file>: replace file with the content (0644, atomic)
# only when it differs.
put() {
  cmp -s -- "$2" "$1" && { rm -f -- "$2"; return 0; }
  chmod 0644 "$2"
  mv -f -- "$2" "$1"
}

# fail <suffix> <reason>: record why (one printable line, shown to the owner).
fail() {
  local s=$1 why
  why=$(printf '%s' "$2" | tr -d '\r' | tr '\n\t' '  ' | tr -cd '[:print:]' | cut -c1-200)
  printf '%s\n' "$why" > "$STATE/failed/$s.new"
  cmp -s -- "$STATE/failed/$s.new" "$STATE/failed/$s" || log "$s: $why"
  put "$STATE/failed/$s" "$STATE/failed/$s.new"
}

# withdraw <suffix>: remove our server for it (if any) and its ready marker.
withdraw() {
  if [ -e "$AVAILABLE/$PREFIX$1" ] || [ -L "$ENABLED/$PREFIX$1" ]; then
    rm -f -- "$ENABLED/$PREFIX$1" "$AVAILABLE/$PREFIX$1"
    reload=1
    log "$1: our server removed"
  fi
  rm -f -- "$STATE/ready/$1"
}

# The whole nginx configuration (read once per run, never printed: it holds
# secrets). NGINX_OK=0 when it cannot be read: nothing is written that run.
NGINX_CONF=""
NGINX_OK=0
if NGINX_CONF=$(nginx -T 2>/dev/null); then
  NGINX_OK=1
else
  log "cannot read the nginx configuration (nginx -T); nothing is written this run"
fi

# served_elsewhere <name>: 0 (and the file's name on stdout) when a server
# in the nginx configuration that this script did not write would answer the
# name: an exact server_name, *.parent / .parent / name.*, or a regex
# (~...). Fails closed: a configuration it cannot parse, or a regex it cannot
# evaluate, counts as served. (Same parser as simple-host-domain-certs.)
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

# write_server <suffix> <rendered file>: put our server in place and enable
# it; 0 when nginx accepts it. On failure the previous file (if any) is put
# back as it was, or ours removed.
write_server() {
  local s=$1 new=$2 avail="$AVAILABLE/$PREFIX$1" link="$ENABLED/$PREFIX$1" prev="" linked=0
  [ -L "$link" ] && linked=1
  if [ -f "$avail" ]; then
    prev=$(mktemp)
    cp -p -- "$avail" "$prev"
  fi
  chmod 0644 "$new"
  mv -f -- "$new" "$avail"
  ln -sfn -- "$avail" "$link"
  if nginx -t >/dev/null 2>&1; then
    reload=1
    [ -z "$prev" ] || rm -f -- "$prev"
    return 0
  fi
  if [ -n "$prev" ]; then
    mv -f -- "$prev" "$avail"
    [ "$linked" = 1 ] || rm -f -- "$link"
  else
    rm -f -- "$link" "$avail"
  fi
  return 1
}

# Release: our servers and ready markers whose request or binding is gone.
declare -A seen=()
for f in "$STATE"/ready/* "$AVAILABLE/$PREFIX"* "$ENABLED/$PREFIX"*; do
  [ -e "$f" ] || [ -L "$f" ] || continue
  s=$(basename -- "$f"); s=${s#"$PREFIX"}
  case $s in *.new|*.pending) continue ;; esac
  [ -z "${seen[$s]:-}" ] || continue
  seen[$s]=1
  rc=0; parse_request "$s" || rc=$?
  if [ "$rc" = 2 ] && [ ! -f "$STATE/requests/$s" ]; then
    withdraw "$s"
    log "released $s (request gone)"
  elif [ ! -L "$SITES/$s" ] || { [ "$rc" != 2 ] && [ "$(readlink -- "$SITES/$s")" != "$R_TARGET" ]; }; then
    withdraw "$s"
    log "released $s (disconnected)"
  fi
done
# A failure note outlives its binding only until here.
for f in "$STATE"/failed/*; do
  [ -f "$f" ] || continue
  s=$(basename -- "$f")
  [ -L "$SITES/$s" ] || rm -f -- "$f"
done

# Every request, every run (they are kept while the family exists).
mapfile -d '' -t reqs < <(find "$STATE/requests" -maxdepth 1 -type f -printf '%f\0' | sort -z)
for s in "${reqs[@]}"; do
  [ -n "$s" ] || continue
  if ! valid_name "$s"; then
    log "dropping invalid request name"
    rm -f -- "$STATE/requests/$s"
    continue
  fi
  rc=0; parse_request "$s" || rc=$?
  if [ "$rc" = 2 ]; then
    # Half-written: the app rewrites it. Leave what is served alone.
    continue
  fi
  if [ ! -L "$SITES/$s" ] || [ "$(readlink -- "$SITES/$s")" != "$R_TARGET" ]; then
    # Not (or no longer) bound to this request's account.
    withdraw "$s"
    continue
  fi
  if [ "$rc" = 1 ]; then
    withdraw "$s"
    fail "$s" "$ERR"
    continue
  fi
  [ "$NGINX_OK" = 1 ] || continue
  probe="sh-probe-$(od -An -N6 -tx1 /dev/urandom | tr -d ' \n').$s"
  if who=$(served_elsewhere "$probe"); then
    # Another server here answers names in this family (the operator's,
    # set up by hand). Never ours, never ready, until it is adopted.
    withdraw "$s"
    log "$s: named by ${who:-another server}"
    fail "$s" "this name is already served here by another server"
    continue
  fi
  if ! check_lineage "$s"; then
    # A server naming a missing certificate would break every nginx reload.
    withdraw "$s"
    fail "$s" "$ERR"
    continue
  fi
  new="$AVAILABLE/$PREFIX$s.new"
  if ! render "$s" > "$new"; then
    rm -f -- "$new"
    withdraw "$s"
    fail "$s" "the web server could not be set up for this family yet"
    continue
  fi
  if cmp -s -- "$new" "$AVAILABLE/$PREFIX$s" && [ "$(readlink -- "$ENABLED/$PREFIX$s" 2>/dev/null)" = "$AVAILABLE/$PREFIX$s" ]; then
    # Served as it should be already: only the marker may need refreshing
    # (e.g. a renewed certificate's expiry).
    rm -f -- "$new"
    ready_body > "$STATE/ready/$s.new"
    put "$STATE/ready/$s" "$STATE/ready/$s.new"
    rm -f -- "$STATE/failed/$s"
    continue
  fi
  if write_server "$s" "$new"; then
    ready_body > "$STATE/ready/$s.pending"
    pending+=("$s")
    log "$s: server written (prefix '${R_PREFIX}', certificate $R_CERT)"
  else
    rm -f -- "$STATE/ready/$s"
    log "$s: nginx -t failed with this family's server; put the previous state back"
    fail "$s" "the web server could not be set up for this family yet"
  fi
done

if [ "$reload" = 1 ]; then
  if nginx -t >/dev/null 2>&1 && systemctl reload nginx; then
    for s in "${pending[@]}"; do
      mv -f -- "$STATE/ready/$s.pending" "$STATE/ready/$s.new"
      put "$STATE/ready/$s" "$STATE/ready/$s.new"
      rm -f -- "$STATE/failed/$s"
      log "ready: $s"
    done
  else
    log "nginx reload skipped: configuration test failed"
  fi
fi
rm -f -- "$STATE"/ready/*.pending
