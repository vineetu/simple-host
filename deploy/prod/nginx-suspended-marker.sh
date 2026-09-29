#!/usr/bin/env bash
# Teach every nginx vhost that serves site files straight from disk to honour
# the operator's take-down marker (a `suspended` file in the site folder, see
# internal/handler/suspend.go), the owner's offline marker (an `offline`
# file, internal/handler/offline.go) and the owner's passcode lock (a
# `passcode` file). Without this, a taken-down, offline or locked site with a
# custom domain keeps serving its files from disk on that domain.
#
# Go already serves these pages on site hosts, person paths and claimed names;
# nginx only needs the checks where it reads files itself, i.e. any root or
# alias line under /srv/simple-host/sites/ ending in /current:
#   root  /srv/simple-host/sites/domains/<domain>/current;       (custom domains)
#   alias /srv/simple-host/sites/handles/<h>/<s>/current...;      (content host)
#   root  /srv/simple-host/sites/handles/<h>/$client/current;     (hand-made wildcard vhosts)
#   root  /srv/simple-host/sites/by-id/<id>/<site>/current;       (hand-made single-site vhosts)
#   root  /srv/simple-host/sites/$sub/current;                    (lab)
# In every server block that has such a line it makes sure these three stand
# for that folder, in this order (take-down > offline > passcode):
#   if (-f <same folder>/suspended) { rewrite ^ /internal/suspended last; }
#   if (-f <same folder>/offline) { rewrite ^ /internal/offline last; }
#   if (-f <same folder>/passcode) { rewrite ^ /internal/passcode$uri last; }
# (`rewrite ... last` is one of the two things that are safe inside an `if`;
# the passcode rewrite keeps the path, and the query string carries over
# because the replacement has no `?`.) They are checked per server block,
# not per file: a vhost with the same folder in two blocks gets the checks in
# both. Where a take-down or offline line already stands in the block (before
# the root line, or inside `location /` as the issuer's template writes it),
# the missing later ones go right after it, in order.
# The rewrites land on the block's `location ^~ /internal/` proxy to the app;
# a block serving a site folder without one (checks new or already there)
# gets whichever of `location = /internal/suspended`,
# `location = /internal/offline` and `location ^~ /internal/passcode/` it
# lacks (internal-only, to APP_UPSTREAM, default 127.0.0.1:8090) right after
# its `server {` line, so hand-made vhosts show the real pages instead of a 404.
#
# Dry run by default: prints only file names and counts, never file contents.
#   sudo bash deploy/prod/nginx-suspended-marker.sh            # what would change
#   sudo bash deploy/prod/nginx-suspended-marker.sh --apply    # back up, edit, nginx -t, reload
# The content-host vhost holds a secret and is edited only with the owner's
# approval: add --include-content-host for it. Idempotent; backups go to
# /var/backups/nginx-suspended-marker-<time>/, and a failed `nginx -t` restores
# them all. Tests: deploy/prod/nginx-suspended-marker_test.sh.
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
UPSTREAM=${APP_UPSTREAM:-127.0.0.1:8090}
# Overridable for the test (deploy/prod/nginx-suspended-marker_test.sh).
NGINX_TEST=${NGINX_TEST:-nginx -t}
NGINX_RELOAD=${NGINX_RELOAD:-systemctl reload nginx}
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
  grep -qE '^[[:space:]]*(root|alias)[[:space:]]+/srv/simple-host/sites/\S+/current' "$real" || continue
  tmp=$(mktemp)
  n=$(APP_UPSTREAM="$UPSTREAM" python3 - "$real" "$tmp" <<'PY'
import os, re, sys
src, dst = sys.argv[1], sys.argv[2]
up = os.environ["APP_UPSTREAM"]
lines = open(src).read().split("\n")
pat = re.compile(r'^(\s*)(?:root|alias)\s+(/srv/simple-host/sites/\S+?)/current\S*;')
check_pat = re.compile(r'^(\s*)if \(-f (\S+)/(suspended|offline|passcode)\)')
prefix_pat = re.compile(r'^\s*location\s+\^~\s*/internal/\s*\{')
exact_pat = re.compile(r'^\s*location\s+(?:=\s*/internal/(suspended|offline)\b|\^~\s*/internal/(passcode)/)')
server_pat = re.compile(r'^(\s*)server\s*\{')
check_line = {
    "suspended": '%sif (-f %s/suspended) { rewrite ^ /internal/suspended last; }',
    "offline": '%sif (-f %s/offline) { rewrite ^ /internal/offline last; }',
    "passcode": '%sif (-f %s/passcode) { rewrite ^ /internal/passcode$uri last; }',
}
order = ("suspended", "offline", "passcode")
page_loc = {
    "suspended": "location = /internal/suspended",
    "offline": "location = /internal/offline",
    "passcode": "location ^~ /internal/passcode/",
}

# Which server block each line is in (-1 outside any), by brace depth
# (comments stripped; vhosts here carry no braces inside strings).
block, depth, cur, starts = [], 0, -1, []
for i, line in enumerate(lines):
    code = line.split("#", 1)[0]
    if depth == 0 and server_pat.match(code):
        cur = len(starts)
        starts.append(i)
    block.append(cur if depth > 0 or server_pat.match(code) else -1)
    depth += code.count("{") - code.count("}")
    if depth <= 0:
        depth, cur = 0, -1

# Per block: folders that already have a check, whether the block has the
# catch-all `location ^~ /internal/` proxy, and which per-page proxies it has.
checked = [set() for _ in starts]
has_prefix = [False for _ in starts]
has_page = [set() for _ in starts]
for i, line in enumerate(lines):
    b = block[i]
    if b < 0:
        continue
    cm = check_pat.match(line)
    if cm:
        checked[b].add(cm.group(2))
    if prefix_pat.match(line):
        has_prefix[b] = True
    em = exact_pat.match(line)
    if em:
        has_page[b].add(em.group(1) or em.group(2))

# Root/alias lines that need all three checks before them (first one per
# folder and block), and whether each block serves a site folder at all.
insert_before = {}
serves = [False for _ in starts]
for i, line in enumerate(lines):
    m = pat.match(line)
    b = block[i]
    if not m or b < 0:
        continue
    serves[b] = True
    if m.group(2) in checked[b]:
        continue
    insert_before[i] = (m.group(1), m.group(2))
    checked[b].add(m.group(2))

def same_check(line, folder, kind):
    cm = check_pat.match(line)
    return bool(cm) and cm.group(2) == folder and cm.group(3) == kind

out, n = [], 0
for i, line in enumerate(lines):
    if i in insert_before:
        ind, folder = insert_before[i]
        for kind in order:
            out.append(check_line[kind] % (ind, folder))
            n += 1
    out.append(line)
    b = block[i]
    if b >= 0 and starts[b] == i and serves[b] and not has_prefix[b]:
        ind = server_pat.match(line).group(1) + "    "
        for page in order:
            if page in has_page[b]:
                continue
            out.append("%s%s { internal; proxy_pass http://%s; proxy_set_header Host $host; proxy_set_header X-Forwarded-Proto $scheme; }" % (ind, page_loc[page], up))
            n += 1
    # An existing check is followed by the ones that come after it, in order:
    # take-down, then offline, then passcode.
    cm = check_pat.match(line)
    if cm and cm.group(3) != "passcode":
        ind, folder = cm.group(1), cm.group(2)
        rest = order[order.index(cm.group(3)) + 1:]
        j = i + 1
        for kind in rest:
            if j < len(lines) and same_check(lines[j], folder, kind):
                break  # the source already carries it; its own line continues the chain
            out.append(check_line[kind] % (ind, folder))
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
if $NGINX_TEST 2>/dev/null; then
  $NGINX_RELOAD && echo "nginx reloaded"
else
  for b in "${backups[@]}"; do cp -p "$BACKUP_DIR/$(basename "$b")" "$b"; done
  echo "nginx -t failed; every edited file restored from $BACKUP_DIR" >&2
  exit 1
fi
