#!/usr/bin/env bash
# Tests for nginx-analytics-logformat-apply.sh against a temp directory:
# dry run changes nothing, --apply installs and reloads, a failing nginx -t
# restores the old file, and a second run is a no-op. Run by `make check`.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
S="$HERE/nginx-analytics-logformat-apply.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
export DEST="$T/analytics-logformat.conf" BACKUP_DIR="$T/backup" NGINX_TEST=true NGINX_RELOAD="touch $T/reloaded"
echo "old format" > "$DEST"

bash "$S" >/dev/null
grep -qx "old format" "$DEST" || fail "dry run changed the file"
[ ! -e "$T/reloaded" ] || fail "dry run reloaded"

NGINX_TEST=false bash "$S" --apply >/dev/null 2>&1 && fail "apply with failing nginx -t succeeded"
grep -qx "old format" "$DEST" || fail "failing nginx -t did not restore the old file"
[ ! -e "$T/reloaded" ] || fail "reloaded after a failing nginx -t"

bash "$S" --apply >/dev/null
cmp -s "$HERE/nginx-analytics-logformat.conf" "$DEST" || fail "apply did not install the file"
[ -e "$T/reloaded" ] || fail "apply did not reload"
grep -qx "old format" "$BACKUP_DIR/analytics-logformat.conf" || fail "no backup of the old file"

rm -f "$T/reloaded"
out=$(bash "$S" --apply)
case "$out" in "up to date:"*) ;; *) fail "second run was not a no-op: $out" ;; esac
[ ! -e "$T/reloaded" ] || fail "second run reloaded"
echo "nginx-analytics-logformat-apply: ok"
