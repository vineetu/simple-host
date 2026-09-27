#!/usr/bin/env bash
# Close the app's /internal/ pages to the internet on every nginx vhost that
# proxies them (security review 2026-09-27, L2).
#
# /internal/* (take-down, offline and not-found pages, the site redirects,
# showcases, the certificate check) are meant to be reached only through
# nginx's own rewrites and error pages. A `location ^~ /internal/` without
# `internal;` also answers requests from outside: anyone could ask
# /internal/tls-ask whether a domain is a customer, or open any person's
# showcase under a customer's domain. With `internal;` in the location,
# `rewrite ... last`, `error_page` and `try_files` still land there, and a
# request from outside gets 404.
#
# In every vhost it adds `internal;` to each `location ^~ /internal/` (and to
# the `location = /internal/suspended|offline` proxies that
# nginx-suspended-marker.sh adds to hand-made vhosts) that does not have it.
# The issuer's template (deploy/domain-certs/vhost.conf.template) already
# carries it for new and re-issued domains.
#
# Dry run by default: prints only file names and counts, never file contents.
#   sudo bash deploy/prod/nginx-internal-lock.sh            # what would change
#   sudo bash deploy/prod/nginx-internal-lock.sh --apply    # back up, edit, nginx -t, reload
# The content-host vhost holds a secret and is edited only with the owner's
# approval: add --include-content-host for it (approved 2026-09-27).
# Idempotent; backups go to /var/backups/nginx-internal-lock-<time>/, and a
# failed `nginx -t` restores them all. Tests: deploy/prod/nginx-internal-lock_test.sh.
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
# Overridable for the test (deploy/prod/nginx-internal-lock_test.sh).
NGINX_TEST=${NGINX_TEST:-nginx -t}
NGINX_RELOAD=${NGINX_RELOAD:-systemctl reload nginx}
ts=$(date +%Y%m%d-%H%M%S)
# Backups go outside nginx's include path: a copy left in sites-enabled would
# be loaded as a second server block.
BACKUP_DIR=${BACKUP_DIR:-/var/backups/nginx-internal-lock-$ts}
backups=()
for f in "$DIR"/*; do
  [ -f "$f" ] || continue
  case "$f" in *.bak*|*~) continue ;; esac
  real=$(readlink -f "$f")
  if [ "$(basename "$f")" = "sites-content-host" ] && [ "$INCLUDE_CH" != 1 ]; then
    echo "skip  $f (needs --include-content-host and the owner's approval)"
    continue
  fi
  grep -qE '^[^#]*location[[:space:]]+(\^~[[:space:]]*/internal/|=[[:space:]]*/internal/)' "$real" || continue
  tmp=$(mktemp)
  n=$(python3 - "$real" "$tmp" <<'PY'
import re, sys
src, dst = sys.argv[1], sys.argv[2]
lines = open(src).read().split("\n")
loc = re.compile(r'location\s+(\^~\s*/internal/|=\s*/internal/\S+)\s*\{')
has_internal = re.compile(r'(^|[\s;{])internal\s*;')
out, n, i = [], 0, 0
while i < len(lines):
    line = lines[i]
    code = line.split("#", 1)[0]
    m = loc.search(code)
    if not m:
        out.append(line)
        i += 1
        continue
    rest = code[m.end():]
    if "}" in rest:
        # One-line block: location ... { directives; }
        if has_internal.search(rest[:rest.index("}")]):
            out.append(line)
        else:
            out.append(line[:m.end()] + " internal;" + line[m.end():])
            n += 1
        i += 1
        continue
    # Multi-line block: look at its lines up to the closing brace.
    j, depth, has = i + 1, 1, bool(has_internal.search(rest))
    while j < len(lines) and depth > 0:
        c = lines[j].split("#", 1)[0]
        if depth == 1 and re.match(r'^\s*internal\s*;', c):
            has = True
        depth += c.count("{") - c.count("}")
        j += 1
    out.append(line)
    if not has:
        ind = re.match(r'^(\s*)', line).group(1)
        out.append(ind + "    internal;")
        n += 1
    i += 1
open(dst, "w").write("\n".join(out))
print(n)
PY
)
  if [ "$n" = 0 ]; then rm -f "$tmp"; echo "ok    $f (already internal)"; continue; fi
  if [ "$APPLY" != 1 ]; then rm -f "$tmp"; echo "would $f ($n location(s))"; continue; fi
  mkdir -p "$BACKUP_DIR"
  cp -p "$real" "$BACKUP_DIR/$(basename "$real")"
  backups+=("$real")
  cat "$tmp" > "$real"; rm -f "$tmp"
  echo "edit  $f ($n location(s)); backup in $BACKUP_DIR"
done
[ "$APPLY" = 1 ] || exit 0
[ ${#backups[@]} -gt 0 ] || { echo "nothing to change"; exit 0; }
if $NGINX_TEST >/dev/null 2>&1; then
  $NGINX_RELOAD && echo "nginx reloaded"
else
  for b in "${backups[@]}"; do cp -p "$BACKUP_DIR/$(basename "$b")" "$b"; done
  echo "nginx -t failed; every edited file restored from $BACKUP_DIR" >&2
  exit 1
fi
