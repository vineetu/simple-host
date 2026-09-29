#!/usr/bin/env bash
# Tests for nginx-internal-lock.sh on throwaway vhost files (no root): every
# /internal/ location gains `internal;` (multi-line, one-line, the marker's
# exact-match ones), the content host is skipped without its flag, a second
# run changes nothing, and a failed nginx -t restores every file. Then, when
# an nginx binary is present, a throwaway nginx (unprivileged, its own port)
# proves the point of the change: with `internal;`, the take-down/offline
# and passcode rewrites and the not-found error page still reach the app, while a request
# for /internal/... from outside gets 404.
#   bash deploy/prod/nginx-internal-lock_test.sh
set -euo pipefail
cd "$(dirname "$0")"
T=$(mktemp -d)
pids=()
cleanup() { for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done; rm -rf "$T"; }
trap cleanup EXIT
fail=0
ok() { echo "  ok — $1"; }
bad() { echo "  FAIL: $1"; fail=1; }

mkdir -p "$T/en"
cat > "$T/en/customdomain" <<'EOF'
server {
    listen 443 ssl;
    location ^~ /internal/ {
        proxy_pass http://127.0.0.1:8090;
        proxy_set_header Host $host;
    }
    location / { try_files $uri =404; }
}
EOF
cat > "$T/en/handmade" <<'EOF'
server {
    location = /internal/suspended { proxy_pass http://127.0.0.1:8090; proxy_set_header Host $host; }
    location = /internal/offline { proxy_pass http://127.0.0.1:8090; }
    location ^~ /internal/passcode/ { proxy_pass http://127.0.0.1:8090; }
    location ^~ /internal/ { proxy_pass http://127.0.0.1:8090; }
}
EOF
cat > "$T/en/done" <<'EOF'
server {
    location ^~ /internal/ {
        internal;
        proxy_pass http://127.0.0.1:8090;
    }
    location = /internal/offline { internal; proxy_pass http://127.0.0.1:8090; }
}
EOF
cat > "$T/en/sites-content-host" <<'EOF'
server { location ^~ /internal/ { proxy_pass http://127.0.0.1:8090; } }
EOF
cat > "$T/en/unrelated" <<'EOF'
server { root /var/www/other; }
EOF
cp -r "$T/en" "$T/orig"

run() { NGINX_SITES_DIR="$T/en" BACKUP_DIR="$T/bak" NGINX_TEST=true NGINX_RELOAD=true bash ./nginx-internal-lock.sh "$@"; }

echo "== dry run =="
out=$(run)
for want in "would $T/en/customdomain (1 location(s))" "would $T/en/handmade (4 location(s))" "ok    $T/en/done (already internal)" "skip  $T/en/sites-content-host"; do
  if grep -qF "$want" <<<"$out"; then ok "$want"; else bad "missing: $want"; fi
done
grep -q unrelated <<<"$out" && bad "unrelated vhost touched" || ok "unrelated vhost left alone"
diff -r "$T/orig" "$T/en" >/dev/null && ok "dry run changes nothing" || bad "dry run changed files"
if grep -q "proxy_pass" <<<"$out"; then bad "file contents printed"; else ok "no file contents in the output"; fi

echo "== apply =="
run --apply >/dev/null
grep -A1 'location ^~ /internal/ {' "$T/en/customdomain" | grep -q '^        internal;$' && ok "multi-line block gains internal;" || bad "customdomain: $(cat "$T/en/customdomain")"
[ "$(grep -c '{ internal; proxy_pass' "$T/en/handmade")" = 4 ] && ok "one-line blocks gain internal;" || bad "handmade: $(cat "$T/en/handmade")"
cmp -s "$T/orig/done" "$T/en/done" && ok "already-internal vhost untouched" || bad "done edited"
cmp -s "$T/orig/sites-content-host" "$T/en/sites-content-host" && ok "content host untouched without the flag" || bad "content host edited"
[ -f "$T/bak/customdomain" ] && cmp -s "$T/bak/customdomain" "$T/orig/customdomain" && ok "backup taken" || bad "no backup"
out=$(run)
grep -q "^would" <<<"$out" && bad "not idempotent: $out" || ok "second run changes nothing"
run --apply --include-content-host >/dev/null
grep -q '{ internal; proxy_pass' "$T/en/sites-content-host" && ok "content host edited with --include-content-host" || bad "content host not edited with the flag"

echo "== failed nginx -t restores =="
rm -rf "$T/en" "$T/bak"; cp -r "$T/orig" "$T/en"
if NGINX_SITES_DIR="$T/en" BACKUP_DIR="$T/bak" NGINX_TEST=false NGINX_RELOAD=true bash ./nginx-internal-lock.sh --apply >/dev/null 2>&1; then
  bad "apply with a failing nginx -t succeeded"
else
  diff -r "$T/orig" "$T/en" >/dev/null && ok "every file restored" || bad "files left edited"
fi

echo "== a throwaway nginx: internal pages still reached by rewrites =="
NGINX=$(command -v nginx || true)
[ -x "$NGINX" ] || NGINX=/usr/sbin/nginx
if [ ! -x "$NGINX" ] || ! command -v curl >/dev/null; then
  echo "  skip — no nginx or curl here"
else
  port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
  UP=$(port); WEB=$(port)
  # The "app": answers 200 with the path it was asked for.
  python3 - "$UP" >/dev/null 2>&1 <<'PY' &
import sys, http.server
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        b = ("app:" + self.path).encode()
        self.send_response(200); self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)
    def log_message(self, *a): pass
http.server.HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
PY
  pids+=($!)
  mkdir -p "$T/ng/logs" "$T/ng/site/current" "$T/ng/down/current"
  echo hello > "$T/ng/site/current/index.html"
  echo hello > "$T/ng/down/current/index.html"
  touch "$T/ng/down/suspended"
  mkdir -p "$T/ng/locked/current"; echo secret > "$T/ng/locked/current/index.html"
  touch "$T/ng/locked/passcode"
  cat > "$T/ng/nginx.conf" <<EOF
pid $T/ng/nginx.pid;
error_log $T/ng/logs/error.log;
events {}
http {
  access_log off;
  client_body_temp_path $T/ng/tmp-body; proxy_temp_path $T/ng/tmp-proxy;
  fastcgi_temp_path $T/ng/tmp-fcgi; uwsgi_temp_path $T/ng/tmp-uwsgi; scgi_temp_path $T/ng/tmp-scgi;
  server {
    listen 127.0.0.1:$WEB;
    server_name up.test;
    location ^~ /internal/ {
        proxy_pass http://127.0.0.1:$UP;
    }
    root $T/ng/site/current;
    location / { try_files \$uri \$uri/ =404; }
  }
  server {
    listen 127.0.0.1:$WEB;
    server_name down.test;
    location ^~ /internal/ { proxy_pass http://127.0.0.1:$UP; }
    root $T/ng/down/current;
    error_page 404 = /internal/notfound;
    location / {
        if (-f $T/ng/down/suspended) { rewrite ^ /internal/suspended last; }
        try_files \$uri \$uri/ =404;
    }
  }
  server {
    listen 127.0.0.1:$WEB;
    server_name locked.test;
    location ^~ /internal/ { proxy_pass http://127.0.0.1:$UP; }
    root $T/ng/locked/current;
    location / {
        if (-f $T/ng/locked/suspended) { rewrite ^ /internal/suspended last; }
        if (-f $T/ng/locked/offline) { rewrite ^ /internal/offline last; }
        if (-f $T/ng/locked/passcode) { rewrite ^ /internal/passcode\$uri last; }
        try_files \$uri \$uri/ =404;
    }
  }
  server {
    listen 127.0.0.1:$WEB;
    server_name live.test;
    location ^~ /internal/ { proxy_pass http://127.0.0.1:$UP; }
    root $T/ng/site/current;
    error_page 404 = /internal/notfound;
    location / { try_files \$uri \$uri/ =404; }
  }
}
EOF
  # Lock it with the script itself (the conf is its own "sites-enabled").
  mkdir -p "$T/ng/en"; cp "$T/ng/nginx.conf" "$T/ng/en/conf"
  NGINX_SITES_DIR="$T/ng/en" BACKUP_DIR="$T/ng/bak" NGINX_TEST=true NGINX_RELOAD=true bash ./nginx-internal-lock.sh --apply >/dev/null
  cp "$T/ng/en/conf" "$T/ng/nginx.conf"
  if ! "$NGINX" -t -p "$T/ng" -c "$T/ng/nginx.conf" >/dev/null 2>&1; then
    bad "throwaway nginx config does not load"
  else
    "$NGINX" -p "$T/ng" -c "$T/ng/nginx.conf" -g 'daemon off;' 2>/dev/null &
    pids+=($!)
    for _ in $(seq 50); do curl -s -o /dev/null "http://127.0.0.1:$WEB/" && break; sleep 0.1; done
    get() { curl -s -H "Host: $1" -w ' %{http_code}' "http://127.0.0.1:$WEB$2"; }
    r=$(get up.test /internal/tls-ask?domain=x.example); [ "${r##* }" = 404 ] && ok "outside request to /internal/ → 404" || bad "outside /internal/: $r"
    # From outside it is an unknown path: the site's not-found page, never the showcase.
    r=$(get live.test /internal/showcase/someone); ! grep -q "showcase" <<<"$r" && ok "showcase not reachable from outside ($r)" || bad "outside showcase: $r"
    r=$(get down.test /); [ "$r" = "app:/internal/suspended 200" ] && ok "take-down rewrite still reaches the app" || bad "rewrite: $r"
    r=$(get locked.test '/a/page.html?x=1&y=2'); [ "$r" = "app:/internal/passcode/a/page.html?x=1&y=2 200" ] && ok "passcode rewrite reaches the app with path and query" || bad "passcode rewrite: $r"
    r=$(get locked.test /); [ "$r" = "app:/internal/passcode/ 200" ] && ok "passcode rewrite on the site root" || bad "passcode root: $r"
    r=$(get live.test /missing.html); grep -q "app:/internal/notfound" <<<"$r" && ok "error_page still reaches the app" || bad "error_page: $r"
    r=$(get live.test /); [ "$r" = "hello
 200" ] && ok "site files still served" || bad "site: $r"
  fi
fi

[ "$fail" = 0 ] && echo "nginx-internal-lock: ok" || { echo "nginx-internal-lock: FAILED"; exit 1; }
