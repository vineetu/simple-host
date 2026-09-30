#!/usr/bin/env bash
# Install (or remove) the nginx servers for the sites' base domain
# (simple-host.site): renders deploy/prod/nginx-site-base-domain.conf into
# /etc/nginx/sites-available/simple-host-site and links it from sites-enabled.
#
# Dry run by default: says what it would do, never prints file contents.
#   sudo bash deploy/prod/nginx-site-base-domain.sh            # what would change
#   sudo bash deploy/prod/nginx-site-base-domain.sh --apply    # back up, install, link, nginx -t, reload
#   sudo bash deploy/prod/nginx-site-base-domain.sh --remove   # back up, unlink, delete, nginx -t, reload
# Idempotent: nothing happens when the installed file already matches and is
# linked. Backups go outside nginx's include path, to
# /var/backups/nginx-site-base-domain-<time>/; a failed `nginx -t` puts the
# previous state back (file and link) and leaves nginx unreloaded.
# Needs /etc/letsencrypt/live/<base>/ (the platform wildcard) and the
# `shanalytics` log format (nginx-analytics-logformat-apply.sh); `nginx -t`
# fails without either. APEX_MODE=redirect (default) 301s the bare domain and
# www to APP_DOMAIN; APEX_MODE=app proxies the bare domain to APP_UPSTREAM
# and 301s www to the apex. ANALYTICS_LOG and CLIENT_MAX_BODY are overridable;
# defaults leave the installed file unchanged from the pre-app-mode template.
# Test: deploy/prod/nginx-site-base-domain_test.sh.
set -euo pipefail
MODE=dry
for a in "$@"; do
  case "$a" in
    --apply) MODE=apply ;;
    --remove) MODE=remove ;;
    *) echo "usage: $0 [--apply | --remove]" >&2; exit 2 ;;
  esac
done
HERE=$(cd "$(dirname "$0")" && pwd)
BASE=${SITE_BASE_DOMAIN:-simple-host.site}
APP=${APP_DOMAIN:-simple-host.app}
UPSTREAM=${APP_UPSTREAM:-127.0.0.1:8090}
LE_LIVE=${LE_LIVE:-/etc/letsencrypt/live}
CERTS=${SITE_BASE_CERTS:-/etc/nginx/simple-host-site-certs-site}
TEMPLATE=${TEMPLATE:-$HERE/nginx-site-base-domain.conf}
# Overridable for the test.
AVAILABLE=${NGINX_AVAILABLE:-/etc/nginx/sites-available}
ENABLED=${NGINX_ENABLED:-/etc/nginx/sites-enabled}
NAME=${NGINX_NAME:-simple-host-site}
APEX_MODE=${APEX_MODE:-redirect}
ANALYTICS_LOG=${ANALYTICS_LOG:-/var/log/simple-host/analytics.log}
BODY_MAX=${CLIENT_MAX_BODY:-64m}
NGINX_TEST=${NGINX_TEST:-nginx -t}
NGINX_RELOAD=${NGINX_RELOAD:-systemctl reload nginx}
ts=$(date +%Y%m%d-%H%M%S)
BACKUP_DIR=${BACKUP_DIR:-/var/backups/nginx-site-base-domain-$ts}

FILE="$AVAILABLE/$NAME"
LINK="$ENABLED/$NAME"
label='^[a-z0-9]([a-z0-9-]*[a-z0-9])?$'
for d in "$BASE" "$APP"; do
  IFS=. read -ra parts <<<"$d"
  [ "${#parts[@]}" -ge 2 ] || { echo "not a domain: $d" >&2; exit 2; }
  for p in "${parts[@]}"; do [[ "$p" =~ $label ]] || { echo "not a domain: $d" >&2; exit 2; }; done
done
up_re='^[][A-Za-z0-9.:-]+$'
[[ "$UPSTREAM" =~ $up_re ]] || { echo "bad upstream: $UPSTREAM" >&2; exit 2; }
case "$APEX_MODE" in redirect|app) ;; *) echo "bad APEX_MODE: $APEX_MODE (redirect or app)" >&2; exit 2 ;; esac
[[ "$BODY_MAX" =~ ^[0-9]+[km]?$ ]] || { echo "bad CLIENT_MAX_BODY: $BODY_MAX" >&2; exit 2; }
for p in "$LE_LIVE" "$CERTS" "$ANALYTICS_LOG"; do [[ "$p" =~ ^/[A-Za-z0-9._/-]+$ ]] || { echo "bad path: $p" >&2; exit 2; }; done
[ -f "$TEMPLATE" ] || { echo "missing $TEMPLATE" >&2; exit 1; }
# A sites-enabled entry that is not our symlink is someone else's: never touched.
if [ -e "$LINK" ] && [ ! -L "$LINK" ]; then
  echo "$LINK exists and is not a symlink: left alone" >&2
  exit 1
fi

# Keep one of the two marked apex blocks in the template; drop the other
# and the marker comment lines themselves. The nginx text lives in the
# template, not here.
select_apex() {
  local keep drop
  case "$APEX_MODE" in
    redirect) keep=apex-redirect; drop=apex-app ;;
    app) keep=apex-app; drop=apex-redirect ;;
  esac
  sed -e "/^# @${drop}\$/,/^# @${drop}\$/d" -e "/^# @${keep}\$/d"
}

render() {
  local re=${BASE//./\\\\.} # \\. in the sed replacement: a literal \. in the regex
  # The template's opening comment (up to the first blank line) documents the
  # placeholders; the installed file says where it came from instead.
  echo "# Written by deploy/prod/nginx-site-base-domain.sh from nginx-site-base-domain.conf ($BASE); edit the template, not this file."
  select_apex < "$TEMPLATE" | sed -e '1,/^$/{/^#/d}' -e "s|__BASE_RE__|$re|g" -e "s|__BASE__|$BASE|g" -e "s|__APP__|$APP|g" \
      -e "s|__UPSTREAM__|$UPSTREAM|g" -e "s|__LE_LIVE__|$LE_LIVE|g" -e "s|__CERTS__|$CERTS|g" \
      -e "s|__ANALYTICS_LOG__|$ANALYTICS_LOG|g" -e "s|__BODY_MAX__|$BODY_MAX|g"
}

had_file=0; had_link=0; old_target=""
[ -f "$FILE" ] && had_file=1
if [ -L "$LINK" ]; then had_link=1; old_target=$(readlink "$LINK"); fi

# restore: put the file and the link back the way they were before this run.
# check: nginx -t; its messages are shown only when it fails.
check() {
  local out
  if out=$($NGINX_TEST 2>&1); then return 0; fi
  printf '%s\n' "$out" >&2
  return 1
}
restore() {
  if [ "$had_file" = 1 ]; then cp -p "$BACKUP_DIR/$NAME" "$FILE"; else rm -f "$FILE"; fi
  if [ "$had_link" = 1 ]; then ln -sfn "$old_target" "$LINK"; else rm -f "$LINK"; fi
}
backup() {
  [ "$had_file" = 1 ] || return 0
  mkdir -p "$BACKUP_DIR"
  cp -p "$FILE" "$BACKUP_DIR/$NAME"
}

if [ "$MODE" = remove ]; then
  if [ "$had_file" = 0 ] && [ "$had_link" = 0 ]; then echo "nothing to remove: $FILE"; exit 0; fi
  backup
  rm -f "$LINK" "$FILE"
  if ! check; then
    restore
    echo "nginx -t failed: $FILE and $LINK restored" >&2
    exit 1
  fi
  $NGINX_RELOAD
  if [ "$had_file" = 1 ]; then echo "removed: $LINK and $FILE (backup in $BACKUP_DIR)"; else echo "removed: $LINK"; fi
  exit 0
fi

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
render > "$tmp"
same=0; linked=0
[ "$had_file" = 1 ] && cmp -s "$tmp" "$FILE" && same=1
[ "$had_link" = 1 ] && [ "$(readlink -f "$LINK")" = "$(readlink -f "$FILE")" ] && linked=1
if [ "$same" = 1 ] && [ "$linked" = 1 ]; then
  echo "up to date: $FILE (linked from $LINK)"
  exit 0
fi
if [ "$MODE" = dry ]; then
  if [ "$had_file" = 0 ]; then echo "would install: $FILE (new, $BASE)"
  elif [ "$same" = 0 ]; then echo "would replace: $FILE (differs; backed up first)"
  else echo "ok: $FILE already matches"; fi
  [ "$linked" = 1 ] || echo "would link: $LINK -> $FILE"
  echo "then: nginx -t (restore on failure) and reload"
  echo "dry run: nothing changed (add --apply)"
  exit 0
fi

backup
install -m 644 "$tmp" "$FILE"
ln -sfn "$FILE" "$LINK"
if ! check; then
  restore
  echo "nginx -t failed: previous state restored ($FILE, $LINK); nginx not reloaded" >&2
  exit 1
fi
$NGINX_RELOAD
if [ "$had_file" = 1 ]; then echo "applied: $FILE, linked from $LINK (backup in $BACKUP_DIR)"
else echo "applied: $FILE, linked from $LINK"; fi
