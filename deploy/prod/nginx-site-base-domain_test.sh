#!/usr/bin/env bash
# Tests for nginx-site-base-domain.sh on throwaway directories (no root, never
# /etc/nginx), with a fake nginx and systemctl on PATH: the dry run changes
# nothing and prints no file contents, --apply installs, links, tests and
# reloads, a second run is a no-op, a failed nginx -t puts the previous state
# back (new install or replacement), --remove backs up, unlinks and reloads.
# The template never declares add_header (it would drop the inherited HSTS).
# Then, when nginx, openssl and curl are present, the rendered conf runs in a
# throwaway nginx (unprivileged, its own ports, self-signed certificates) to
# prove what it serves: redirects, the proxy with inherited HSTS, the
# analytics log, the /internal/ lock, and the per-person certificate.
#   bash deploy/prod/nginx-site-base-domain_test.sh
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
S="$HERE/nginx-site-base-domain.sh"
T=$(mktemp -d)
pids=()
cleanup() { for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done; rm -rf "$T"; }
trap cleanup EXIT
fail=0
ok() { echo "  ok — $1"; }
bad() { echo "  FAIL: $1"; fail=1; }
chk() { if eval "$2"; then ok "$1"; else bad "$1"; fi; }

mkdir -p "$T/bin" "$T/avail" "$T/en"
cat > "$T/bin/nginx" <<EOF
#!/bin/sh
echo "nginx \$*" >> "$T/calls"
[ -e "$T/nginx-fails" ] && { echo "nginx: [emerg] test failure" >&2; exit 1; }
exit 0
EOF
printf '#!/bin/sh\necho "systemctl $*" >> %s/calls\n' "$T" > "$T/bin/systemctl"
chmod +x "$T/bin/"*
touch "$T/calls"
F="$T/avail/simple-host-site"
L="$T/en/simple-host-site"
run() { PATH="$T/bin:$PATH" NGINX_AVAILABLE="$T/avail" NGINX_ENABLED="$T/en" BACKUP_DIR="$T/bak" bash "$S" "$@"; }
reloads() { grep -c 'systemctl reload nginx' "$T/calls" || true; }

echo "== template =="
chk "no add_header in the template" "! grep -qE '^[^#]*add_header' '$HERE/nginx-site-base-domain.conf'"
chk "every :443 server listens like the .app ones (listen 443 ssl; no http2)" "[ \"\$(grep -c 'listen 443 ssl;' '$HERE/nginx-site-base-domain.conf')\" = 5 ] && ! grep -q http2 '$HERE/nginx-site-base-domain.conf'"
chk "apex modes are marked alternatives in the template" "grep -q '^# @apex-redirect$' '$HERE/nginx-site-base-domain.conf' && grep -q '^# @apex-app$' '$HERE/nginx-site-base-domain.conf' && grep -q 'select_apex' '$S'"

echo "== dry run =="
out=$(run)
chk "dry run says it would install and link" "grep -q '^would install: $F' <<<\"\$out\" && grep -q '^would link: $L' <<<\"\$out\""
chk "dry run changes nothing" "[ ! -e '$F' ] && [ ! -e '$L' ] && [ ! -s '$T/calls' ]"
chk "no file contents in the output" "! grep -qE 'proxy_pass|server_name|ssl_certificate' <<<\"\$out\""

echo "== failed nginx -t on a new install =="
touch "$T/nginx-fails"
rc=0; run --apply >/dev/null 2>"$T/err" || rc=$?
chk "apply with a failing nginx -t exits non-zero and says why" "[ $rc != 0 ] && grep -q 'test failure' '$T/err'"
chk "new file and link removed again, nothing reloaded" "[ ! -e '$F' ] && [ ! -L '$L' ] && [ \"\$(reloads)\" = 0 ]"
rm -f "$T/nginx-fails"

echo "== apply =="
out=$(run --apply)
chk "file installed and linked" "[ -f '$F' ] && [ -L '$L' ] && [ \"\$(readlink -f '$L')\" = \"\$(readlink -f '$F')\" ]"
chk "nginx -t then reload" "grep -q 'nginx -t' '$T/calls' && [ \"\$(reloads)\" = 1 ]"
chk "rendered for simple-host.site, upstream 127.0.0.1:8090, no placeholders left" "grep -q 'server_name simple-host.site www.simple-host.site;' '$F' && grep -qF 'simple-host\\.site\$\"' '$F' && grep -q 'proxy_pass http://127.0.0.1:8090;' '$F' && grep -q 'return 301 https://simple-host.app/;' '$F' && grep -q '/etc/letsencrypt/live/simple-host.site/fullchain.pem' '$F' && grep -q '/etc/nginx/simple-host-site-certs-site/\$sh_site_base_cert_person/privkey.pem' '$F' && ! grep -q '__' '$F'"
chk "default render still has three :443 servers (redirect apex)" "[ \"\$(grep -c 'listen 443 ssl;' '$F')\" = 3 ]"
# Default output (no APEX_MODE / ANALYTICS_LOG / CLIENT_MAX_BODY) must match
# the unmodified template, captured from git: HEAD when this is uncommitted,
# otherwise the last ancestor whose template has no @apex-app markers.
ROOT=$(cd "$HERE/../.." && pwd)
unmod="$T/unmod.conf"
found_unmod=0
if git -C "$ROOT" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  while read -r rev; do
    [ -n "$rev" ] || continue
    if git -C "$ROOT" show "$rev:deploy/prod/nginx-site-base-domain.conf" 2>/dev/null | grep -q '^# @apex-app$'; then
      continue
    fi
    git -C "$ROOT" show "$rev:deploy/prod/nginx-site-base-domain.conf" > "$unmod"
    found_unmod=1
    break
  done < <(git -C "$ROOT" log -n 30 --pretty=%H -- deploy/prod/nginx-site-base-domain.conf)
fi
if [ "$found_unmod" = 1 ]; then
  expect="$T/unmod.rendered"
  {
    echo "# Written by deploy/prod/nginx-site-base-domain.sh from nginx-site-base-domain.conf (simple-host.site); edit the template, not this file."
    sed -e '1,/^$/{/^#/d}' \
        -e 's|__BASE_RE__|simple-host\\.site|g' \
        -e 's|__BASE__|simple-host.site|g' \
        -e 's|__APP__|simple-host.app|g' \
        -e 's|__UPSTREAM__|127.0.0.1:8090|g' \
        -e 's|__LE_LIVE__|/etc/letsencrypt/live|g' \
        -e 's|__CERTS__|/etc/nginx/simple-host-site-certs-site|g' \
        "$unmod"
  } > "$expect"
  chk "default render is byte-for-byte the unmodified template" "cmp -s '$F' '$expect'"
else
  bad "could not find an unmodified nginx-site-base-domain.conf in git"
fi
chk "no file contents in the output" "! grep -qE 'proxy_pass|server_name' <<<\"\$out\""
out=$(run --apply)
chk "second run is a no-op" "grep -q '^up to date' <<<\"\$out\" && [ \"\$(reloads)\" = 1 ]"
out=$(run)
chk "dry run after apply: up to date" "grep -q '^up to date' <<<\"\$out\""

echo "== replacing a changed file =="
echo "# hand edit" >> "$F"; cp "$F" "$T/edited"
out=$(run)
chk "dry run notices the difference, changes nothing" "grep -q '^would replace' <<<\"\$out\" && cmp -s '$T/edited' '$F'"
touch "$T/nginx-fails"
rc=0; run --apply >/dev/null 2>&1 || rc=$?
chk "failing nginx -t restores the edited file and keeps the link" "[ $rc != 0 ] && cmp -s '$T/edited' '$F' && [ -L '$L' ] && [ \"\$(reloads)\" = 1 ]"
rm -f "$T/nginx-fails"; rm -rf "$T/bak"
run --apply >/dev/null
chk "replaced, old one backed up outside sites-enabled" "! grep -q 'hand edit' '$F' && cmp -s '$T/edited' '$T/bak/simple-host-site' && [ \"\$(reloads)\" = 2 ]"
chk "nothing but our link in sites-enabled" "[ \"\$(ls '$T/en')\" = simple-host-site ]"

echo "== overrides =="
T2=$(mktemp -d -p "$T"); mkdir -p "$T2/en"
NGINX_TEST=true NGINX_RELOAD=true NGINX_AVAILABLE="$T2" NGINX_ENABLED="$T2/en" NGINX_NAME=x BACKUP_DIR="$T/bak2" SITE_BASE_DOMAIN=example.test APP_UPSTREAM=127.0.0.1:9999 bash "$S" --apply >/dev/null
chk "base domain and upstream overridable" "grep -q 'server_name example.test www.example.test;' '$T2/x' && grep -qF 'example\\.test\$\"' '$T2/x' && grep -q 'proxy_pass http://127.0.0.1:9999;' '$T2/x' && ! grep -qF simple-host.site '$T2/x'"
rc=0; PATH="$T/bin:$PATH" NGINX_AVAILABLE="$T2" NGINX_ENABLED="$T2/en" SITE_BASE_DOMAIN='evil|x' bash "$S" >/dev/null 2>&1 || rc=$?
chk "a malformed base domain is refused" "[ $rc = 2 ]"
rc=0; PATH="$T/bin:$PATH" NGINX_AVAILABLE="$T2" NGINX_ENABLED="$T2/en" APEX_MODE=foo bash "$S" >/dev/null 2>&1 || rc=$?
chk "a bad APEX_MODE is refused" "[ $rc = 2 ]"
rc=0; PATH="$T/bin:$PATH" NGINX_AVAILABLE="$T2" NGINX_ENABLED="$T2/en" CLIENT_MAX_BODY=64mb bash "$S" >/dev/null 2>&1 || rc=$?
chk "a bad CLIENT_MAX_BODY is refused" "[ $rc = 2 ]"
rc=0; PATH="$T/bin:$PATH" NGINX_AVAILABLE="$T2" NGINX_ENABLED="$T2/en" ANALYTICS_LOG=relative/log bash "$S" >/dev/null 2>&1 || rc=$?
chk "a relative ANALYTICS_LOG is refused" "[ $rc = 2 ]"

echo "== APEX_MODE=app (simple-hack.app) =="
T3=$(mktemp -d -p "$T"); mkdir -p "$T3/en"
NGINX_TEST=true NGINX_RELOAD=true NGINX_AVAILABLE="$T3" NGINX_ENABLED="$T3/en" \
  NGINX_NAME=simple-hack BACKUP_DIR="$T/bak-hack" \
  SITE_BASE_DOMAIN=simple-hack.app APP_DOMAIN=simple-hack.app \
  APP_UPSTREAM=127.0.0.1:8091 \
  SITE_BASE_CERTS=/etc/nginx/simple-host-site-certs-hack \
  APEX_MODE=app ANALYTICS_LOG=/var/log/simple-hack/analytics.log \
  CLIENT_MAX_BODY=64m \
  bash "$S" --apply >/dev/null
H="$T3/simple-hack"
chk "hack apex proxies to 127.0.0.1:8091 and has no return 301" "awk 'BEGIN{s=0} /server_name simple-hack.app;/{s=1} s&&/proxy_pass http:\\/\\/127.0.0.1:8091;/{p=1} s&&/return 301/{r=1} s&&/^}/{exit} END{exit !(p && !r)}' '$H'"
chk "www.simple-hack.app 301s to the apex" "grep -A 20 'server_name www.simple-hack.app;' '$H' | grep -q 'return 301 https://simple-hack.app\$request_uri;'"
https_n=$(grep -c 'listen 443 ssl;' "$H")
internal_n=$(grep -c 'location ^~ /internal/' "$H")
chk "/internal/ is blocked in every HTTPS server ($https_n servers, $internal_n locks)" "[ \"$https_n\" = \"$internal_n\" ] && [ \"$https_n\" -ge 4 ]"
chk "analytics path is the hack one" "grep -q '/var/log/simple-hack/analytics.log shanalytics' '$H' && ! grep -q '/var/log/simple-host/analytics.log' '$H'"
chk "no leftover placeholders" "! grep -qE '__[A-Z0-9_]+__' '$H'"
chk "apex has client_max_body_size 64m" "grep -q 'client_max_body_size 64m;' '$H'"
chk "hack render has no apex-mode marker comments" "! grep -q '^# @apex-' '$H'"

echo "== remove =="
touch "$T/nginx-fails"
rc=0; run --remove >/dev/null 2>&1 || rc=$?
chk "failing nginx -t on remove restores file and link" "[ $rc != 0 ] && [ -f '$F' ] && [ -L '$L' ] && [ \"\$(reloads)\" = 2 ]"
rm -f "$T/nginx-fails"; rm -rf "$T/bak"
cp "$F" "$T/installed"
run --remove >/dev/null
chk "removed with a backup, reloaded" "[ ! -e '$F' ] && [ ! -L '$L' ] && cmp -s '$T/installed' '$T/bak/simple-host-site' && [ \"\$(reloads)\" = 3 ]"
# shellcheck disable=SC2034 # read inside chk's eval
out=$(run --remove)
chk "second remove is a no-op" "grep -q '^nothing to remove' <<<\"\$out\" && [ \"\$(reloads)\" = 3 ]"

echo "== someone else's file in sites-enabled =="
echo "hand" > "$L"
rc=0; run --apply >/dev/null 2>&1 || rc=$?
chk "a regular file named like ours is never touched" "[ $rc != 0 ] && [ \"\$(cat '$L')\" = hand ] && [ ! -e '$F' ]"
rm -f "$L"

echo "== a throwaway nginx serving the rendered conf =="
NGINX=/usr/sbin/nginx
[ -x "$NGINX" ] || NGINX=$(PATH=/usr/sbin:/usr/bin:/sbin:/bin command -v nginx || true)
if [ -z "$NGINX" ] || [ ! -x "$NGINX" ] || ! command -v curl >/dev/null || ! command -v openssl >/dev/null; then
  echo "  skip — no nginx, curl or openssl here"
else
  N="$T/ng"; mkdir -p "$N/logs" "$N/en" "$N/le/simple-host.site" "$N/certs/alice"
  cert() { openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 1 -subj "/CN=$1" -addext "subjectAltName=DNS:$1${2:+,DNS:$2}" -keyout "$3/privkey.pem" -out "$3/fullchain.pem" >/dev/null 2>&1; }
  cert '*.simple-host.site' simple-host.site "$N/le/simple-host.site"
  cert '*.alice.simple-host.site' '' "$N/certs/alice"
  port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
  UP=$(port); P80=$(port); P443=$(port)
  python3 - "$UP" >/dev/null 2>&1 <<'PY' &
import sys, http.server
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        b = ("app:" + self.headers.get("Host", "") + self.path + " xfp=" + self.headers.get("X-Forwarded-Proto", "")).encode()
        self.send_response(200); self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)
    def log_message(self, *a): pass
http.server.HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
PY
  pids+=($!)
  PATH="$T/bin:$PATH" NGINX_AVAILABLE="$N" NGINX_ENABLED="$N/en" BACKUP_DIR="$N/bak" LE_LIVE="$N/le" SITE_BASE_CERTS="$N/certs" APP_UPSTREAM="127.0.0.1:$UP" bash "$S" --apply >/dev/null
  # Only the ports and log paths differ from what the box would load.
  sed -e "s|listen 80;|listen 127.0.0.1:$P80;|" -e "s|listen 443 ssl;|listen 127.0.0.1:$P443 ssl;|" \
      -e "s|/var/log/nginx/access.log|$N/logs/access.log|" -e "s|/var/log/simple-host/analytics.log|$N/logs/analytics.log|" \
      "$N/simple-host-site" > "$N/site.conf"
  cat > "$N/nginx.conf" <<EOF
pid $N/nginx.pid;
error_log $N/logs/error.log;
events {}
http {
  access_log $N/logs/default.log;
  client_body_temp_path $N/tmp-body; proxy_temp_path $N/tmp-proxy;
  fastcgi_temp_path $N/tmp-fcgi; uwsgi_temp_path $N/tmp-uwsgi; scgi_temp_path $N/tmp-scgi;
  include $HERE/nginx-analytics-logformat.conf;
  # As conf.d/tls-hardening.conf does on the box: http level, inherited.
  add_header Strict-Transport-Security "max-age=63072000; includeSubDomains" always;
  include $N/site.conf;
}
EOF
  if ! "$NGINX" -t -p "$N" -c "$N/nginx.conf" >"$N/t.out" 2>&1; then
    bad "rendered conf does not pass nginx -t: $(grep -v 'could not open error log' "$N/t.out" | head -3)"
  else
    ok "rendered conf passes nginx -t"
    "$NGINX" -p "$N" -c "$N/nginx.conf" -g 'daemon off;' 2>/dev/null &
    pids+=($!)
    for _ in $(seq 50); do curl -s -o /dev/null "http://127.0.0.1:$P80/" && break; sleep 0.1; done
    r() { curl -s -k --resolve "$1:$P443:127.0.0.1" -D "$N/h" -o "$N/b" -w '%{http_code}' "https://$1:$P443$2" || true; }
    code=$(curl -s -o /dev/null -w '%{http_code} %{redirect_url}' -H 'Host: blog.alice.simple-host.site' "http://127.0.0.1:$P80/p?q=1")
    chk "http → https, same host and path ($code)" "[ '$code' = '301 https://blog.alice.simple-host.site/p?q=1' ]"
    code=$(r simple-host.site /x); loc=$(grep -i '^location:' "$N/h" | tr -d '\r' | cut -d' ' -f2)
    chk "bare domain → https://simple-host.app/ ($code $loc)" "[ '$code' = 301 ] && [ '$loc' = https://simple-host.app/ ]"
    code=$(r www.simple-host.site /); loc=$(grep -i '^location:' "$N/h" | tr -d '\r' | cut -d' ' -f2)
    chk "www → https://simple-host.app/" "[ '$code' = 301 ] && [ '$loc' = https://simple-host.app/ ]"
    code=$(r alice.simple-host.site /a?b=1)
    chk "person host proxied to the app with Host and X-Forwarded-Proto" "[ '$code' = 200 ] && [ \"\$(cat '$N/b')\" = 'app:alice.simple-host.site/a?b=1 xfp=https' ]"
    chk "person host inherits HSTS" "grep -qi '^strict-transport-security: max-age=63072000; includeSubDomains' '$N/h'"
    code=$(r alice.simple-host.site /internal/tls-ask)
    chk "person host: /internal/ closed ($code)" "[ '$code' = 404 ] && ! grep -q app: '$N/b'"
    code=$(r blog.alice.simple-host.site /post)
    chk "site host proxied to the app" "[ '$code' = 200 ] && [ \"\$(cat '$N/b')\" = 'app:blog.alice.simple-host.site/post xfp=https' ]"
    chk "site host inherits HSTS" "grep -qi '^strict-transport-security:' '$N/h'"
    code=$(r blog.alice.simple-host.site /internal/showcase/x)
    chk "site host: /internal/ closed ($code)" "[ '$code' = 404 ] && ! grep -q app: '$N/b'"
    subj() { openssl s_client -connect "127.0.0.1:$P443" -servername "$1" </dev/null 2>/dev/null | openssl x509 -noout -subject 2>/dev/null | sed 's/.*CN *= *//'; }
    chk "person host served on the platform wildcard" "[ \"\$(subj alice.simple-host.site)\" = '*.simple-host.site' ]"
    chk "site host served on the person's own certificate" "[ \"\$(subj blog.alice.simple-host.site)\" = '*.alice.simple-host.site' ]"
    code=$(r blog.nobody.simple-host.site /)
    chk "a person without a certificate fails the handshake ($code)" "[ '$code' = 000 ]"
    chk "analytics log written for both hosts (shanalytics format)" "grep -qP '\\talice\\.simple-host\\.site\\t200\\tGET\\t/a\\t' '$N/logs/analytics.log' && grep -qP '\\tblog\\.alice\\.simple-host\\.site\\t200\\tGET\\t/post\\t' '$N/logs/analytics.log'"
  fi
fi

[ "$fail" = 0 ] && echo "nginx-site-base-domain: ok" || { echo "nginx-site-base-domain: FAILED"; exit 1; }
