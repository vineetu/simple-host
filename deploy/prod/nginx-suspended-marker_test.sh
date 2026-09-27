#!/usr/bin/env bash
# Tests for nginx-suspended-marker.sh on throwaway vhost files (no nginx, no
# root): the checks go into every server block that serves a site folder, the
# /internal/ pages are reachable from blocks that had no proxy, the issuer
# template only gains its offline line, hand-made vhosts (by-id, $client,
# $sub) are covered, a second run changes nothing, and a failed nginx -t
# restores every file.
#   bash deploy/prod/nginx-suspended-marker_test.sh
set -euo pipefail
cd "$(dirname "$0")"
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fail=0
ok() { echo "  ok — $1"; }
bad() { echo "  FAIL: $1"; fail=1; }
S=/srv/simple-host/sites

mkdir -p "$T/en"
# Two blocks, same folder (the regression from review M4).
cat > "$T/en/two-blocks" <<EOF
server {
    listen 443 ssl;
    server_name x.example;
    root $S/domains/x.example/current;
}
server {
    listen 80;
    server_name x.example;
    root $S/domains/x.example/current;
}
EOF
# The issuer's template: take-down check inside location /, /internal/ proxied.
cat > "$T/en/template" <<EOF
server {
    listen 443 ssl;
    location ^~ /internal/ { proxy_pass http://127.0.0.1:8090; }
    root $S/domains/t.example/current;
    location / {
        if (-f $S/domains/t.example/suspended) { rewrite ^ /internal/suspended last; }
        try_files \$uri \$uri/ =404;
    }
}
EOF
# Hand-made vhosts: one site by id, a wildcard by handle, the lab.
cat > "$T/en/by-id" <<EOF
server {
    listen 443 ssl;
    server_name paragliding.example;
    root $S/by-id/2be1c8c7/paragliding/current;
    location / { try_files \$uri \$uri/ =404; }
}
EOF
cat > "$T/en/wildcard" <<EOF
server {
    listen 443 ssl;
    server_name ~^(?<client>[a-z0-9-]+)\.quotes\.example\$;
    if (-f $S/handles/chb/\$client/suspended) { rewrite ^ /internal/suspended last; }
    root $S/handles/chb/\$client/current;
}
EOF
cat > "$T/en/lab" <<EOF
server {
    listen 443 ssl;
    server_name ~^(?<sub>.+\.lab)\.example\$;
    root $S/\$sub/current;
}
EOF
cat > "$T/en/sites-content-host" <<EOF
server { alias $S/handles/a/b/current/; }
EOF
cat > "$T/en/unrelated" <<EOF
server { root /var/www/other; }
EOF
cp -r "$T/en" "$T/orig"

run() { NGINX_SITES_DIR="$T/en" BACKUP_DIR="$T/bak" NGINX_TEST=true NGINX_RELOAD=true bash ./nginx-suspended-marker.sh "$@"; }

echo "== dry run =="
out=$(run)
for want in "would $T/en/two-blocks (8 insert(s))" "would $T/en/template (1 insert(s))" "would $T/en/by-id (4 insert(s))" "would $T/en/wildcard (3 insert(s))" "would $T/en/lab (4 insert(s))" "skip  $T/en/sites-content-host"; do
  if grep -qF "$want" <<<"$out"; then ok "$want"; else bad "missing: $want"; fi
done
grep -q unrelated <<<"$out" && bad "unrelated vhost touched" || ok "unrelated vhost left alone"
diff -r "$T/orig" "$T/en" >/dev/null && ok "dry run changes nothing" || bad "dry run changed files"

echo "== apply =="
run --apply >/dev/null
count() { grep -c "$2" "$T/en/$1" || true; }
[ "$(count two-blocks "x.example/suspended) { rewrite")" = 2 ] && [ "$(count two-blocks "x.example/offline) { rewrite")" = 2 ] && ok "both server blocks checked" || bad "two-blocks: $(cat "$T/en/two-blocks")"
[ "$(count two-blocks "location = /internal/offline")" = 2 ] && ok "both blocks reach the offline page" || bad "two-blocks internal locations"
python3 - "$T/en/two-blocks" <<'PY' && ok "checks come before root, take-down first" || bad "order in two-blocks"
import sys
l = open(sys.argv[1]).read().split("\n")
for i, x in enumerate(l):
    if x.strip().startswith("root "):
        assert "/suspended)" in l[i-2] and "/offline)" in l[i-1], l
PY
[ "$(count template "/suspended)")" = 1 ] && [ "$(count template "/offline)")" = 1 ] && [ "$(count template "location = /internal/")" = 0 ] && ok "template: offline after the take-down, no extra locations" || bad "template: $(cat "$T/en/template")"
grep -A1 "t.example/suspended" "$T/en/template" | grep -q "t.example/offline" && ok "template: offline right after the take-down" || bad "template order"
[ "$(count by-id "by-id/2be1c8c7/paragliding/offline)")" = 1 ] && [ "$(count by-id "location = /internal/suspended { proxy_pass http://127.0.0.1:8090")" = 1 ] && ok "by-id vhost covered" || bad "by-id: $(cat "$T/en/by-id")"
[ "$(count wildcard '$client/suspended)')" = 1 ] && [ "$(count wildcard '$client/offline)')" = 1 ] && [ "$(count wildcard "location = /internal/suspended")" = 1 ] && ok "wildcard vhost gains the offline check" || bad "wildcard: $(cat "$T/en/wildcard")"
[ "$(count lab '$sub/offline)')" = 1 ] && ok "lab vhost covered" || bad "lab: $(cat "$T/en/lab")"
cmp -s "$T/orig/sites-content-host" "$T/en/sites-content-host" && ok "content host untouched without the flag" || bad "content host edited"
[ -f "$T/bak/two-blocks" ] && cmp -s "$T/bak/two-blocks" "$T/orig/two-blocks" && ok "backup taken" || bad "no backup"

echo "== second run =="
out=$(run)
grep -q "^would" <<<"$out" && bad "not idempotent: $out" || ok "nothing left to change"

echo "== failed nginx -t restores =="
rm -rf "$T/en" "$T/bak"; cp -r "$T/orig" "$T/en"
if NGINX_SITES_DIR="$T/en" BACKUP_DIR="$T/bak" NGINX_TEST=false NGINX_RELOAD=true bash ./nginx-suspended-marker.sh --apply >/dev/null 2>&1; then
  bad "apply with a failing nginx -t succeeded"
else
  diff -r "$T/orig" "$T/en" >/dev/null && ok "every file restored" || bad "files left edited"
fi

[ "$fail" = 0 ] && echo "nginx-suspended-marker: ok" || { echo "nginx-suspended-marker: FAILED"; exit 1; }
