#!/usr/bin/env bash
# Teach the issuer-written nginx servers (simple-host-domain-<domain> and
# simple-host-family-<suffix>) already in place to hand an unlisted site's
# requests to the app, which serves its files with X-Robots-Tag noindex and
# a "Disallow: /" robots.txt (internal/handler/unlisted.go). New and
# re-issued servers get the line from the templates
# (deploy/domain-certs/vhost.conf.template, deploy/family-certs/...).
#
# In each such file, after the last marker check of `location /` (passcode
# for a domain, lives-elsewhere for a family) it adds the same check for the
# `unlisted` marker of that folder:
#   if (-f <folder>/unlisted) { rewrite ^ /internal/passcode$uri last; }
# Hand-made vhosts are left alone: the app does not know their hosts.
#
# Dry run by default: prints only file names and counts, never contents.
#   sudo bash deploy/prod/nginx-unlisted-marker.sh            # what would change
#   sudo bash deploy/prod/nginx-unlisted-marker.sh --apply    # back up, edit, nginx -t, reload
# Idempotent; backups go to /var/backups/nginx-unlisted-marker-<time>/, and a
# failed `nginx -t` restores them all. Test: nginx-unlisted-marker_test.sh.
set -euo pipefail
APPLY=0
for a in "$@"; do
  case "$a" in
    --apply) APPLY=1 ;;
    *) echo "usage: $0 [--apply]" >&2; exit 2 ;;
  esac
done
DIR=${NGINX_SITES_DIR:-/etc/nginx/sites-enabled}
NGINX_TEST=${NGINX_TEST:-nginx -t}
NGINX_RELOAD=${NGINX_RELOAD:-systemctl reload nginx}
BACKUP_DIR=${BACKUP_DIR:-/var/backups/nginx-unlisted-marker-$(date +%Y%m%d-%H%M%S)}
backups=()
for f in "$DIR"/simple-host-domain-* "$DIR"/simple-host-family-*; do
  [ -f "$f" ] || continue
  case "$f" in *.bak*|*~) continue ;; esac
  real=$(readlink -f "$f")
  tmp=$(mktemp)
  n=$(python3 - "$real" "$tmp" <<'PY'
import re, sys
src, dst = sys.argv[1], sys.argv[2]
lines = open(src).read().split("\n")
chk = re.compile(r'^(\s*)if \(-f (\S+)/(passcode|lives-elsewhere|unlisted)\)')
last, have = {}, set()
for i, line in enumerate(lines):
    m = chk.match(line)
    if not m:
        continue
    if m.group(3) == "unlisted":
        have.add(m.group(2))
    else:
        last[m.group(2)] = i  # the later of passcode and lives-elsewhere
out, n = [], 0
at = {i: folder for folder, i in last.items() if folder not in have}
for i, line in enumerate(lines):
    out.append(line)
    if i in at:
        ind = chk.match(line).group(1)
        out.append('%sif (-f %s/unlisted) { rewrite ^ /internal/passcode$uri last; }' % (ind, at[i]))
        n += 1
open(dst, "w").write("\n".join(out))
print(n)
PY
)
  if [ "$n" = 0 ]; then rm -f "$tmp"; echo "ok    $f (already has the check, or no site folder)"; continue; fi
  if [ "$APPLY" != 1 ]; then rm -f "$tmp"; echo "would $f ($n insert(s))"; continue; fi
  mkdir -p "$BACKUP_DIR"
  cp -p "$real" "$BACKUP_DIR/$(basename "$real")"
  backups+=("$real")
  cat "$tmp" > "$real"; rm -f "$tmp"
  echo "edit  $f ($n insert(s)); backup in $BACKUP_DIR"
done
[ "$APPLY" = 1 ] || exit 0
[ ${#backups[@]} -gt 0 ] || { echo "nothing to change"; exit 0; }
if $NGINX_TEST 2>/dev/null; then
  $NGINX_RELOAD && echo "nginx reloaded"
else
  for b in "${backups[@]}"; do cp -p "$BACKUP_DIR/$(basename "$b")" "$b"; done
  echo "nginx -t failed; every edited file restored from $BACKUP_DIR" >&2
  exit 1
fi
