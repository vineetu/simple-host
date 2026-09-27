#!/usr/bin/env bash
# Teach every nginx vhost that serves site files straight from disk to honour
# the operator's take-down marker (a `suspended` file in the site folder, see
# internal/handler/suspend.go) and the owner's offline marker (an `offline`
# file, internal/handler/offline.go). Without this, a taken-down or offline
# site with a custom domain keeps serving its files from disk on that domain.
#
# Go already serves both pages on site hosts, person paths and claimed names;
# nginx only needs the checks where it reads files itself:
#   root  /srv/simple-host/sites/domains/<domain>/current;      (custom domains)
#   alias /srv/simple-host/sites/handles/<h>/<s>/current...;     (content host)
# Before each such line it makes sure these two stand, in this order (the
# take-down wins):
#   if (-f <same folder>/suspended) { rewrite ^ /internal/suspended last; }
#   if (-f <same folder>/offline) { rewrite ^ /internal/offline last; }
# (`rewrite ... last` is one of the two things that are safe inside an `if`),
# which land on the vhost's `location ^~ /internal/` proxy to the app. Where a
# take-down line already stands (before the root line, or inside `location /`
# as the issuer's template writes it), the offline line goes right after it.
#
# Dry run by default: prints only file names and counts, never file contents.
#   sudo bash deploy/prod/nginx-suspended-marker.sh            # what would change
#   sudo bash deploy/prod/nginx-suspended-marker.sh --apply    # back up, edit, nginx -t, reload
# The content-host vhost holds a secret and is edited only with the owner's
# approval: add --include-content-host for it. Idempotent; backups go to
# /var/backups/nginx-suspended-marker-<time>/, and a failed `nginx -t` restores
# them all.
set -euo pipefail
APPLY=0; INCLUDE_CH=0
for a in "$@"; do
  case "$a" in
    --apply) APPLY=1 ;;
    --include-content-host) INCLUDE_CH=1 ;;
    *) echo "usage: $0 [--apply] [--include-content-host]" >&2; exit 2 ;;
  esac
done
DIR=${NGINX_SITES_DIR:-/etc/nginx/sites-enabled}
ts=$(date +%Y%m%d-%H%M%S)
# Backups go outside nginx's include path: a copy left in sites-enabled would
# be loaded as a second server block.
BACKUP_DIR=${BACKUP_DIR:-/var/backups/nginx-suspended-marker-$ts}
backups=()
for f in "$DIR"/*; do
  [ -f "$f" ] || continue
  case "$f" in *.bak*|*~) continue ;; esac
  real=$(readlink -f "$f")
  if [ "$(basename "$f")" = "sites-content-host" ] && [ "$INCLUDE_CH" != 1 ]; then
    echo "skip  $f (needs --include-content-host and the owner's approval)"
    continue
  fi
  grep -qE '^[[:space:]]*(root|alias) /srv/simple-host/sites/(domains|handles)/' "$real" || continue
  tmp=$(mktemp)
  n=$(python3 - "$real" "$tmp" <<'PY'
import re, sys
src, dst = sys.argv[1], sys.argv[2]
lines = open(src).read().split("\n")
pat = re.compile(r'^(\s*)(?:root|alias) (/srv/simple-host/sites/(?:domains/[^/\s;]+|handles/[^/\s;]+/[^/\s;]+))/current\S*;')
susp_pat = re.compile(r'^(\s*)if \(-f (\S+)/suspended\)')
off_line = '%sif (-f %s/offline) { rewrite ^ /internal/offline last; }'
susp_line = '%sif (-f %s/suspended) { rewrite ^ /internal/suspended last; }'
# Folders that already have the take-down check somewhere (inside `location /`
# in the issuer's template, or before the root line from an earlier run): the
# offline check goes right after it, in the same block.
checked = {susp_pat.match(l).group(2) for l in lines if susp_pat.match(l)}
out, n = [], 0
for i, line in enumerate(lines):
    m = pat.match(line)
    if m and m.group(2) not in checked:
        out.append(susp_line % (m.group(1), m.group(2)))
        out.append(off_line % (m.group(1), m.group(2)))
        checked.add(m.group(2))
        n += 2
    out.append(line)
    sm = susp_pat.match(line)
    if sm:
        nxt = lines[i + 1] if i + 1 < len(lines) else ""
        if "%s/offline)" % sm.group(2) not in nxt:
            out.append(off_line % (sm.group(1), sm.group(2)))
            n += 1
open(dst, "w").write("\n".join(out))
print(n)
PY
)
  if [ "$n" = 0 ]; then rm -f "$tmp"; echo "ok    $f (already has the checks)"; continue; fi
  if [ "$APPLY" != 1 ]; then rm -f "$tmp"; echo "would $f ($n insert(s))"; continue; fi
  mkdir -p "$BACKUP_DIR"
  cp -p "$real" "$BACKUP_DIR/$(basename "$real")"
  backups+=("$real")
  cat "$tmp" > "$real"; rm -f "$tmp"
  echo "edit  $f ($n insert(s)); backup in $BACKUP_DIR"
done
[ "$APPLY" = 1 ] || exit 0
[ ${#backups[@]} -gt 0 ] || { echo "nothing to change"; exit 0; }
if nginx -t 2>/dev/null; then
  systemctl reload nginx && echo "nginx reloaded"
else
  for b in "${backups[@]}"; do cp -p "$BACKUP_DIR/$(basename "$b")" "$b"; done
  echo "nginx -t failed; every edited file restored from $BACKUP_DIR" >&2
  exit 1
fi
