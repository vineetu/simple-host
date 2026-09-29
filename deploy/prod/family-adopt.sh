#!/usr/bin/env bash
# Move one hand-made wildcard vhost (e.g. sites-enabled/sub-voucher.chhotabreak.com,
# written before address families existed) to the file simple-host-family-certs
# manages, with no downtime. Until this runs, the issuer refuses the family
# ("already served here by another server"), because the hand-made server
# answers its names.
#
#   family-adopt.sh <suffix> --old <vhost file name in sites-enabled> [--apply]
#   family-adopt.sh <suffix> --rollback [--apply]
#
# Dry run by default: prints the file names and a unified diff of the old
# vhost against the managed one (these vhosts hold no secrets). The content
# host (sites-content-host) is refused outright and never read.
#
# Adopt (--apply): needs the family's request (the app has registered it) and
# its link pointing where the request says. The managed file comes from
# `simple-host-family-certs --render <suffix>` and the ready marker from
# `--ready <suffix>` (which also checks the wildcard certificate), so both
# are exactly what the issuer writes. The old enabled link (and its target)
# is backed up to $BACKUP_ROOT/family-adopt-<time>/, recorded in
# $BACKUP_ROOT/family-adopt-latest-<suffix>; then the managed file goes into
# sites-available, the old link is removed and ours created back to back
# (never both enabled: their regex names would be duplicates), `nginx -t`,
# reload, ready marker. A failed `nginx -t` restores the previous state
# exactly. The old file stays in sites-available: the owner removes it after
# a 24 h soak.
#
# Rollback (--apply): removes our enabled link, our file and the ready marker,
# restores the old enabled link from the recorded backup, `nginx -t` (on
# failure ours goes back), reload.
#
# Idempotent: adopting an adopted family, or rolling back twice, is a no-op.
# Holds the issuer's lock while it works. Every path is overridable for the
# test (deploy/prod/family-adopt_test.sh).
set -euo pipefail

AVAILABLE=${AVAILABLE:-/etc/nginx/sites-available}
ENABLED=${ENABLED:-/etc/nginx/sites-enabled}
STATE=${STATE:-/var/lib/simple-host-family-certs}
SITES=${SITES:-/srv/simple-host/sites/families}
BACKUP_ROOT=${BACKUP_ROOT:-/var/backups}
ISSUER=${ISSUER:-/usr/local/sbin/simple-host-family-certs}
NGINX_TEST=${NGINX_TEST:-nginx -t}
NGINX_RELOAD=${NGINX_RELOAD:-systemctl reload nginx}
LOCK=${LOCK:-/run/simple-host-family-certs.lock}
PREFIX=simple-host-family-

usage() { echo "usage: $0 <suffix> --old <vhost in sites-enabled> [--apply] | $0 <suffix> --rollback [--apply]" >&2; exit 2; }
die() { echo "family-adopt: $*" >&2; exit 1; }
say() { echo "family-adopt: $*"; }

[ $# -ge 2 ] || usage
s=$1; shift
OLD=""; ROLLBACK=0; APPLY=0
while [ $# -gt 0 ]; do
  case $1 in
    --old) [ $# -ge 2 ] || usage; OLD=$2; shift 2 ;;
    --rollback) ROLLBACK=1; shift ;;
    --apply) APPLY=1; shift ;;
    *) usage ;;
  esac
done
{ [ "$ROLLBACK" = 1 ] && [ -z "$OLD" ]; } || { [ "$ROLLBACK" = 0 ] && [ -n "$OLD" ]; } || usage
[[ "$s" =~ ^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]] && [ "${#s}" -le 253 ] \
  || die "not a family name: $s"

ours_av="$AVAILABLE/$PREFIX$s"
ours_en="$ENABLED/$PREFIX$s"
ready="$STATE/ready/$s"
latest="$BACKUP_ROOT/family-adopt-latest-$s"

# safe_old <name>: refuse the content host (holds a secret: never read it),
# paths, dot names and our own files.
safe_old() {
  case $1 in
    */*|.*|"") die "--old takes a file name in $ENABLED, not a path" ;;
    sites-content-host) die "refusing sites-content-host (it is never read or moved by this script)" ;;
    "$PREFIX"*) die "$1 is already a managed family file" ;;
  esac
  if [ -L "$ENABLED/$1" ] && [ "$(basename -- "$(readlink -f -- "$ENABLED/$1")")" = sites-content-host ]; then
    die "refusing $1: it is the content host"
  fi
}

# put_ready <file with the marker>: the ready marker, 0644, atomic.
put_ready() {
  mkdir -p "$STATE/ready"
  cp -- "$1" "$ready.new"
  chmod 0644 "$ready.new"
  mv -f -- "$ready.new" "$ready"
  rm -f -- "$STATE/failed/$s"
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if [ "$ROLLBACK" = 0 ]; then
  safe_old "$OLD"
  old_en="$ENABLED/$OLD"
  [ -f "$STATE/requests/$s" ] || die "no request for $s yet: connect the family in the app first"
  target=$(sed -n 2p -- "$STATE/requests/$s")
  [ -L "$SITES/$s" ] && [ "$(readlink -- "$SITES/$s")" = "$target" ] \
    || die "$SITES/$s does not point where the request says: the family is not verified yet"
  $ISSUER --render "$s" > "$tmp/new" || die "the issuer could not render $s"
  $ISSUER --ready "$s" > "$tmp/ready" || die "the issuer refuses $s (see above)"
  [ -s "$tmp/new" ] || die "the issuer rendered an empty file"

  if [ -L "$ours_en" ] && [ "$(readlink -- "$ours_en")" = "$ours_av" ] && cmp -s "$tmp/new" "$ours_av" \
      && [ ! -e "$old_en" ] && [ ! -L "$old_en" ]; then
    if [ "$APPLY" = 1 ] && ! cmp -s "$tmp/ready" "$ready"; then
      put_ready "$tmp/ready"
      say "$s: already adopted; ready marker refreshed"
    else
      say "$s: already adopted; nothing to do"
    fi
    exit 0
  fi
  [ -e "$old_en" ] || [ -L "$old_en" ] || die "no $OLD in $ENABLED"
  [ -f "$old_en" ] || die "$old_en is not a readable vhost file"
  [ ! -L "$ours_en" ] || die "$ours_en and $old_en are both enabled; sort that out by hand first"

  if [ -L "$old_en" ]; then kind="link"; old_target=$(readlink -- "$old_en"); else kind="file"; old_target=""; fi
  ts=$(date +%Y%m%d-%H%M%S)
  bk="$BACKUP_ROOT/family-adopt-$ts"
  if [ "$APPLY" != 1 ]; then
    say "$s (dry run; add --apply to do it)"
    echo "  old:     $old_en${old_target:+ -> $old_target} (disabled; the file stays in place)"
    echo "  new:     $ours_av, enabled as $ours_en"
    echo "  backup:  $bk/ (recorded in $latest)"
    echo "  ready:   $ready"
    sed 's/^/           /' "$tmp/ready"
    echo "  diff (old vhost -> managed file):"
    diff -u --label "$OLD" --label "$PREFIX$s" -- "$old_en" "$tmp/new" || true
    exit 0
  fi

  exec 9>"$LOCK"
  flock -w 120 9 || die "the issuer is busy; try again"
  mkdir -p "$BACKUP_ROOT"
  install -d -m 0700 "$bk"
  cp -P -- "$old_en" "$bk/old-enabled"          # the link itself (or the file)
  cp -p -- "$(readlink -f -- "$old_en")" "$bk/old.conf"
  had_ours=0
  if [ -e "$ours_av" ]; then cp -p -- "$ours_av" "$bk/ours.prev"; had_ours=1; fi
  printf 'suffix=%s\nold=%s\nkind=%s\ntarget=%s\nhad_ours=%s\n' "$s" "$OLD" "$kind" "$old_target" "$had_ours" > "$bk/meta"
  printf '%s\n' "$bk" > "$latest.new" && mv -f -- "$latest.new" "$latest"

  cp -- "$tmp/new" "$ours_av.new"
  chmod 0644 "$ours_av.new"
  mv -f -- "$ours_av.new" "$ours_av"
  # Back to back: the old server out, ours in.
  rm -f -- "$old_en"
  ln -s -- "$ours_av" "$ours_en"
  if ! $NGINX_TEST >/dev/null 2>&1 || ! $NGINX_RELOAD >/dev/null 2>&1; then
    rm -f -- "$ours_en"
    cp -P -- "$bk/old-enabled" "$old_en"
    if [ "$had_ours" = 1 ]; then cp -p -- "$bk/ours.prev" "$ours_av"; else rm -f -- "$ours_av"; fi
    die "nginx -t (or the reload) failed with the managed file; the previous state is back (backup in $bk)"
  fi
  put_ready "$tmp/ready"
  say "$s: adopted; $OLD disabled (still in place for 24 h), $PREFIX$s enabled, nginx reloaded"
  say "backup in $bk; undo with: $0 $s --rollback --apply"
  exit 0
fi

# Rollback.
[ -f "$latest" ] || die "no adoption of $s recorded in $BACKUP_ROOT"
bk=$(head -n1 -- "$latest")
[ -f "$bk/meta" ] || die "the recorded backup $bk is missing"
OLD=$(sed -n 's/^old=//p' "$bk/meta")
old_target=$(sed -n 's/^target=//p' "$bk/meta")
safe_old "$OLD"
old_en="$ENABLED/$OLD"
if [ ! -L "$ours_en" ] && [ ! -e "$ours_av" ] && { [ -e "$old_en" ] || [ -L "$old_en" ]; }; then
  say "$s: already rolled back; nothing to do"
  exit 0
fi
{ [ ! -e "$old_en" ] && [ ! -L "$old_en" ]; } || die "$old_en is enabled again already; sort that out by hand first"
if [ "$APPLY" != 1 ]; then
  say "$s rollback (dry run; add --apply to do it)"
  echo "  remove:  $ours_en, $ours_av, $ready"
  echo "  restore: $old_en${old_target:+ -> $old_target} (from $bk)"
  exit 0
fi

exec 9>"$LOCK"
flock -w 120 9 || die "the issuer is busy; try again"
# Our file and the ready marker stay until nginx accepts the old vhost.
had_link=0; [ -L "$ours_en" ] && had_link=1
rm -f -- "$ours_en"
cp -P -- "$bk/old-enabled" "$old_en"
# The old file was removed from sites-available after the soak: put it back.
if [ -n "$old_target" ] && [ ! -e "$old_en" ]; then
  case $old_target in /*) t=$old_target ;; *) t="$ENABLED/$old_target" ;; esac
  cp -p -- "$bk/old.conf" "$t"
fi
if ! $NGINX_TEST >/dev/null 2>&1; then
  rm -f -- "$old_en"
  if [ "$had_link" = 1 ]; then ln -s -- "$ours_av" "$ours_en"; fi
  die "nginx -t failed with the old vhost back; ours is still in place"
fi
rm -f -- "$ours_av" "$ready"
$NGINX_RELOAD >/dev/null 2>&1 || die "nginx reload failed; run it by hand"
say "$s: rolled back; $OLD enabled again, $PREFIX$s removed, nginx reloaded"
