#!/usr/bin/env bash
# Tests for nginx-suspended-marker.sh on throwaway vhost files (no nginx, no
# root): the three checks (take-down, offline, passcode) go into every server
# block that serves a site folder, the /internal/ pages are reachable from
# blocks that had no proxy, blocks with one or two of the checks gain only the
# missing ones right after them, a block with all three is left alone,
# hand-made vhosts (by-id, $client, $sub) are covered, the content host is
# edited only with its flag, a second run changes nothing, the result parses
# (when nginx is installed), and a failed nginx -t restores every file.
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
# Custom domain with the take-down and offline checks already (the issuer's
# template before passcode): gains only the passcode line, after offline.
cat > "$T/en/two-lines" <<EOF
server {
    listen 443 ssl;
    location ^~ /internal/ { internal; proxy_pass http://127.0.0.1:8090; }
    root $S/domains/two.example/current;
    location / {
        if (-f $S/domains/two.example/suspended) { rewrite ^ /internal/suspended last; }
        if (-f $S/domains/two.example/offline) { rewrite ^ /internal/offline last; }
        try_files \$uri \$uri/ =404;
    }
}
EOF
# All three already: nothing to do.
cat > "$T/en/three-lines" <<EOF
server {
    listen 443 ssl;
    location ^~ /internal/ { internal; proxy_pass http://127.0.0.1:8090; }
    root $S/domains/three.example/current;
    location / {
        if (-f $S/domains/three.example/suspended) { rewrite ^ /internal/suspended last; }
        if (-f $S/domains/three.example/offline) { rewrite ^ /internal/offline last; }
        if (-f $S/domains/three.example/passcode) { rewrite ^ /internal/passcode\$uri last; }
        try_files \$uri \$uri/ =404;
    }
}
EOF
# Hand-made vhost the previous version of this script already edited: the two
# exact-match proxies and two checks, no /internal/ prefix proxy.
cat > "$T/en/handmade-old" <<EOF
server {
    location = /internal/suspended { internal; proxy_pass http://127.0.0.1:8090; proxy_set_header Host \$host; proxy_set_header X-Forwarded-Proto \$scheme; }
    location = /internal/offline { internal; proxy_pass http://127.0.0.1:8090; proxy_set_header Host \$host; proxy_set_header X-Forwarded-Proto \$scheme; }
    listen 443 ssl;
    server_name old.example;
    if (-f $S/by-id/9/old/suspended) { rewrite ^ /internal/suspended last; }
    if (-f $S/by-id/9/old/offline) { rewrite ^ /internal/offline last; }
    root $S/by-id/9/old/current;
}
EOF
# The content host as the repo copy had it before passcode (checks inside the
# regex location, next to the alias).
cat > "$T/en/sites-content-host" <<EOF
server {
    listen 443 ssl;
    location ^~ /internal/ { internal; proxy_pass http://127.0.0.1:8090; }
    location ~ "^/(?<h>[a-z0-9-]{1,39})/(?<s>[a-z0-9-]{1,63})(?<rest>/.*)?\$" {
        if (-f $S/handles/\$h/\$s/suspended) { rewrite ^ /internal/suspended last; }
        if (-f $S/handles/\$h/\$s/offline) { rewrite ^ /internal/offline last; }
        alias $S/handles/\$h/\$s/current\$rest;
    }
}
EOF
cat > "$T/en/unrelated" <<EOF
server { root /var/www/other; }
EOF
cp -r "$T/en" "$T/orig"

run() { NGINX_SITES_DIR="$T/en" BACKUP_DIR="$T/bak" NGINX_TEST=true NGINX_RELOAD=true bash ./nginx-suspended-marker.sh "$@"; }

echo "== dry run =="
out=$(run)
for want in "would $T/en/two-blocks (12 insert(s))" "would $T/en/template (2 insert(s))" "would $T/en/by-id (6 insert(s))" "would $T/en/wildcard (5 insert(s))" "would $T/en/lab (6 insert(s))" "would $T/en/two-lines (1 insert(s))" "ok    $T/en/three-lines (already has the checks)" "would $T/en/handmade-old (2 insert(s))" "skip  $T/en/sites-content-host"; do
  if grep -qF "$want" <<<"$out"; then ok "$want"; else bad "missing: $want"; fi
done
grep -q unrelated <<<"$out" && bad "unrelated vhost touched" || ok "unrelated vhost left alone"
diff -r "$T/orig" "$T/en" >/dev/null && ok "dry run changes nothing" || bad "dry run changed files"

echo "== apply =="
run --apply >/dev/null
count() { grep -c "$2" "$T/en/$1" || true; }
[ "$(count two-blocks "x.example/suspended) { rewrite")" = 2 ] && [ "$(count two-blocks "x.example/offline) { rewrite")" = 2 ] && ok "both server blocks checked" || bad "two-blocks: $(cat "$T/en/two-blocks")"
[ "$(count two-blocks 'x.example/passcode) { rewrite ^ /internal/passcode$uri last; }')" = 2 ] && ok "both server blocks gain the passcode check" || bad "two-blocks passcode: $(cat "$T/en/two-blocks")"
[ "$(count two-blocks "location = /internal/offline")" = 2 ] && ok "both blocks reach the offline page" || bad "two-blocks internal locations"
[ "$(grep -cF 'location ^~ /internal/passcode/ { internal; proxy_pass http://127.0.0.1:8090; proxy_set_header Host $host; proxy_set_header X-Forwarded-Proto $scheme; }' "$T/en/two-blocks")" = 2 ] && ok "both blocks reach the passcode page" || bad "two-blocks passcode location"
python3 - "$T/en/two-blocks" <<'PY' && ok "checks come before root: take-down, offline, passcode" || bad "order in two-blocks"
import sys
l = open(sys.argv[1]).read().split("\n")
for i, x in enumerate(l):
    if x.strip().startswith("root "):
        assert "/suspended)" in l[i-3] and "/offline)" in l[i-2] and "/passcode)" in l[i-1], l
PY
[ "$(count template "/suspended)")" = 1 ] && [ "$(count template "/offline)")" = 1 ] && [ "$(count template "location = /internal/")" = 0 ] && ok "template: offline after the take-down, no extra locations" || bad "template: $(cat "$T/en/template")"
grep -A2 "t.example/suspended" "$T/en/template" | tr '\n' '|' | grep -q "t.example/offline.*|.*t.example/passcode" && [ "$(count template "/passcode)")" = 1 ] && ok "template: offline then passcode right after the take-down" || bad "template order: $(cat "$T/en/template")"
grep -A1 "two.example/offline" "$T/en/two-lines" | grep -qF 'if (-f /srv/simple-host/sites/domains/two.example/passcode) { rewrite ^ /internal/passcode$uri last; }' && [ "$(count two-lines "/passcode)")" = 1 ] && [ "$(count two-lines "location = /internal/")" = 0 ] && [ "$(count two-lines "internal/passcode/")" = 0 ] && ok "two-lines: passcode right after offline, no extra locations" || bad "two-lines: $(cat "$T/en/two-lines")"
cmp -s "$T/orig/three-lines" "$T/en/three-lines" && ok "three-lines: untouched" || bad "three-lines edited"
[ "$(count handmade-old "by-id/9/old/passcode)")" = 1 ] && grep -A1 "by-id/9/old/offline" "$T/en/handmade-old" | grep -q "by-id/9/old/passcode" && [ "$(count handmade-old "location ^~ /internal/passcode/ { internal;")" = 1 ] && [ "$(count handmade-old "location = /internal/suspended")" = 1 ] && [ "$(count handmade-old "location = /internal/offline")" = 1 ] && ok "handmade-old: gains only the passcode check and proxy" || bad "handmade-old: $(cat "$T/en/handmade-old")"
[ "$(count by-id "by-id/2be1c8c7/paragliding/offline)")" = 1 ] && [ "$(count by-id "location = /internal/suspended { internal; proxy_pass http://127.0.0.1:8090")" = 1 ] && ok "by-id vhost covered" || bad "by-id: $(cat "$T/en/by-id")"
[ "$(count wildcard '$client/suspended)')" = 1 ] && [ "$(count wildcard '$client/offline)')" = 1 ] && [ "$(count wildcard "location = /internal/suspended")" = 1 ] && ok "wildcard vhost gains the offline check" || bad "wildcard: $(cat "$T/en/wildcard")"
[ "$(count lab '$sub/offline)')" = 1 ] && [ "$(count lab '$sub/passcode)')" = 1 ] && ok "lab vhost covered" || bad "lab: $(cat "$T/en/lab")"
cmp -s "$T/orig/sites-content-host" "$T/en/sites-content-host" && ok "content host untouched without the flag" || bad "content host edited"
[ -f "$T/bak/two-blocks" ] && cmp -s "$T/bak/two-blocks" "$T/orig/two-blocks" && ok "backup taken" || bad "no backup"

echo "== second run =="
out=$(run)
grep -q "^would" <<<"$out" && bad "not idempotent: $out" || ok "nothing left to change"

echo "== content host with its flag =="
run --include-content-host --apply >/dev/null
grep -A1 'handles/$h/$s/offline' "$T/en/sites-content-host" | grep -qF 'if (-f /srv/simple-host/sites/handles/$h/$s/passcode) { rewrite ^ /internal/passcode$uri last; }' && [ "$(count sites-content-host "/passcode)")" = 1 ] && [ "$(count sites-content-host "internal/passcode/")" = 0 ] && ok "content host gains the passcode check with --include-content-host" || bad "content host: $(cat "$T/en/sites-content-host")"
out=$(run --include-content-host)
grep -q "^would" <<<"$out" && bad "content host not idempotent: $out" || ok "content host: second run changes nothing"

if command -v nginx >/dev/null 2>&1; then
  echo "== nginx -t on the edited vhosts =="
  mkdir -p "$T/ng/logs"
  port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')
  { echo "pid $T/ng/nginx.pid; error_log $T/ng/logs/error.log; events {} http { access_log off;"
    for f in "$T"/en/*; do sed -e "s/listen 443 ssl;/listen 127.0.0.1:$port;/" -e "s/listen 80;/listen 127.0.0.1:$port;/" "$f"; echo; done
    echo "}"; } > "$T/ng/nginx.conf"
  if nginx -t -p "$T/ng" -c "$T/ng/nginx.conf" >/dev/null 2>&1; then ok "edited vhosts parse"; else bad "nginx -t: $(nginx -t -p "$T/ng" -c "$T/ng/nginx.conf" 2>&1 | tail -3)"; fi
fi

echo "== failed nginx -t restores =="
rm -rf "$T/en" "$T/bak"; cp -r "$T/orig" "$T/en"
if NGINX_SITES_DIR="$T/en" BACKUP_DIR="$T/bak" NGINX_TEST=false NGINX_RELOAD=true bash ./nginx-suspended-marker.sh --apply >/dev/null 2>&1; then
  bad "apply with a failing nginx -t succeeded"
else
  diff -r "$T/orig" "$T/en" >/dev/null && ok "every file restored" || bad "files left edited"
fi

[ "$fail" = 0 ] && echo "nginx-suspended-marker: ok" || { echo "nginx-suspended-marker: FAILED"; exit 1; }
