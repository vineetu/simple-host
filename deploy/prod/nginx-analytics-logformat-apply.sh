#!/usr/bin/env bash
# Install deploy/prod/nginx-analytics-logformat.conf as nginx's `shanalytics`
# log format (adds the referring domain as an eighth field, 2026-09-27).
#
# Dry run by default: says whether the live file differs and prints the diff
# (the file holds no secrets). With --apply: backs the live file up outside
# nginx's include path, installs the new one, runs `nginx -t` (restoring the
# backup if it fails) and reloads nginx.
#   sudo bash deploy/prod/nginx-analytics-logformat-apply.sh            # what would change
#   sudo bash deploy/prod/nginx-analytics-logformat-apply.sh --apply    # back up, install, nginx -t, reload
# Idempotent: nothing happens when the live file already matches. Order does
# not matter against the app deploy: the ingester reads 7- and 8-field lines.
# Test: deploy/prod/nginx-analytics-logformat-apply_test.sh.
set -euo pipefail
APPLY=0
for a in "$@"; do
  case "$a" in
    --apply) APPLY=1 ;;
    *) echo "usage: $0 [--apply]" >&2; exit 2 ;;
  esac
done
HERE=$(cd "$(dirname "$0")" && pwd)
SRC=${SRC:-$HERE/nginx-analytics-logformat.conf}
DEST=${DEST:-/etc/nginx/conf.d/analytics-logformat.conf}
# Overridable for the test.
NGINX_TEST=${NGINX_TEST:-nginx -t}
NGINX_RELOAD=${NGINX_RELOAD:-systemctl reload nginx}
ts=$(date +%Y%m%d-%H%M%S)
BACKUP_DIR=${BACKUP_DIR:-/var/backups/nginx-analytics-logformat-$ts}

[ -f "$SRC" ] || { echo "missing $SRC" >&2; exit 1; }
if [ -f "$DEST" ] && cmp -s "$SRC" "$DEST"; then
  echo "up to date: $DEST"
  exit 0
fi
echo "would change: $DEST"
diff -u "$DEST" "$SRC" || true
if [ "$APPLY" != 1 ]; then
  echo "dry run: nothing changed (add --apply)"
  exit 0
fi

mkdir -p "$BACKUP_DIR"
had_dest=0
if [ -f "$DEST" ]; then
  cp -p "$DEST" "$BACKUP_DIR/"
  had_dest=1
fi
install -m 644 "$SRC" "$DEST"
if ! $NGINX_TEST; then
  echo "nginx -t failed: restoring $DEST" >&2
  if [ "$had_dest" = 1 ]; then
    cp -p "$BACKUP_DIR/$(basename "$DEST")" "$DEST"
  else
    rm -f "$DEST"
  fi
  exit 1
fi
$NGINX_RELOAD
echo "applied: $DEST (backup in $BACKUP_DIR)"
