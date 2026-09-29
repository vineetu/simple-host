#!/usr/bin/env bash
# Real-nginx check of the address-family template: renders it with issue.sh
# --render (suffix fam.test, prefix voucher-, reserved www), runs a private
# nginx on 127.0.0.1 high ports (its own prefix, pid, logs and temp paths;
# never the system nginx) with a self-signed *.fam.test certificate and a
# tiny fake app, and checks over HTTPS with curl --resolve:
#   - meera.fam.test serves site voucher-meera's files from disk
#   - www.fam.test (reserved) is 404, even with a voucher-www folder
#   - xn--abc.fam.test and a.b.fam.test are not answered by this server
#   - the suspended / offline / passcode / lives-elsewhere markers rewrite to
#     /internal/suspended, /internal/offline, /internal/passcode/<path>,
#     /internal/family/<path> at the app; /internal/ from outside is 404
#   - a missing file goes to the app with the original path and Host
#   - /v1/x is proxied with Host; port 80 redirects to https
# Skips (exit 0) when nginx, openssl, curl or python3 is missing. No root.
#   bash deploy/family-certs/nginx_fixture_test.sh
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
NGINX=$(command -v nginx || true)
if [ -z "$NGINX" ] && [ -x /usr/sbin/nginx ]; then NGINX=/usr/sbin/nginx; fi
for t in openssl curl python3; do
  command -v "$t" >/dev/null 2>&1 || { echo "nginx fixture: skip (no $t)"; exit 0; }
done
[ -n "$NGINX" ] || { echo "nginx fixture: skip (no nginx)"; exit 0; }

T=$(mktemp -d)
pids=()
cleanup() {
  [ -f "$T/ng/nginx.pid" ] && kill "$(cat "$T/ng/nginx.pid")" 2>/dev/null || true
  for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done
  rm -rf "$T"
}
trap cleanup EXIT
fail=0
ok() { echo "  ok — $1"; }
bad() { echo "  FAIL: $1"; fail=1; }
port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }

# Render through the issuer, exactly as production does.
U=11111111-1111-1111-1111-111111111111
mkdir -p "$T/state/requests" "$T/ng/logs" "$T/live/fam.test" "$T/live/default" "$T/acme"
printf 'sh-0123456789abcdef0123456789abcdef\n../by-id/%s\nwildcard\nfam.test\nvoucher-\nwww\n' "$U" > "$T/state/requests/fam.test"
printf 'STATE=%s/state\nTEMPLATE=%s/vhost.conf.template\n' "$T" "$here" > "$T/conf"
SIMPLE_HOST_FAMILY_CERTS_CONF="$T/conf" bash "$here/issue.sh" --render fam.test > "$T/rendered"

HTTPS=$(port); HTTP=$(port); UP=$(port)
# Only the test rewrites listen ports and host paths.
sed -e "s|listen 443 ssl;|listen 127.0.0.1:$HTTPS ssl;|" \
    -e "s|listen 80;|listen 127.0.0.1:$HTTP;|" \
    -e "s|/srv/simple-host/sites/families|$T/families|g" \
    -e "s|/etc/letsencrypt/live|$T/live|g" \
    -e "s|/var/log/simple-host/analytics.log|$T/ng/logs/analytics.log|g" \
    -e "s|/var/www/acme|$T/acme|g" \
    -e "s|127.0.0.1:8090|127.0.0.1:$UP|g" "$T/rendered" > "$T/ng/family.conf"
grep -qE '/srv/|/etc/letsencrypt|8090|listen (443|80);' "$T/ng/family.conf" && bad "a production path or port survived the rewrite"

cert() {
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 2 \
    -keyout "$T/live/$1/privkey.pem" -out "$T/live/$1/fullchain.pem" -subj "/CN=$1" \
    -addext "subjectAltName=$2" >/dev/null 2>&1
}
cert fam.test 'DNS:*.fam.test'
cert default 'DNS:default.test'

# The families farm: families/fam.test -> ../by-id/<user>, one folder per site.
F=$T/families
mkdir -p "$F/by-id/$U"
ln -s "by-id/$U" "$F/fam.test"
site() { mkdir -p "$F/by-id/$U/$1/current"; echo "file:$1" > "$F/by-id/$U/$1/current/index.html"; }
for s in voucher-meera voucher-www voucher-down voucher-off voucher-lock voucher-moved voucher-xn--abc meera; do site "$s"; done
mkdir -p "$F/by-id/$U/voucher-meera/current/sub"; echo "page:sub" > "$F/by-id/$U/voucher-meera/current/sub/page.html"
touch "$F/by-id/$U/voucher-down/suspended" "$F/by-id/$U/voucher-off/offline" \
      "$F/by-id/$U/voucher-lock/passcode" "$F/by-id/$U/voucher-moved/lives-elsewhere"

# The "app": answers 200 with the path and Host it was asked for.
python3 - "$UP" >/dev/null 2>&1 <<'PY' &
import sys, http.server
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        b = ("app:%s host=%s" % (self.path, self.headers.get("Host"))).encode()
        self.send_response(200); self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)
    def log_message(self, *a): pass
http.server.HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
PY
pids+=($!)

cat > "$T/ng/nginx.conf" <<EOF
pid $T/ng/nginx.pid;
error_log $T/ng/logs/error.log;
events {}
http {
  access_log off;
  log_format shanalytics '\$host \$request_uri \$status';
  client_body_temp_path $T/ng/tmp-body; proxy_temp_path $T/ng/tmp-proxy;
  fastcgi_temp_path $T/ng/tmp-fcgi; uwsgi_temp_path $T/ng/tmp-uwsgi; scgi_temp_path $T/ng/tmp-scgi;
  # What answers a name no other server has (the box's default server).
  server {
    listen 127.0.0.1:$HTTPS ssl default_server;
    listen 127.0.0.1:$HTTP default_server;
    ssl_certificate $T/live/default/fullchain.pem;
    ssl_certificate_key $T/live/default/privkey.pem;
    location / { return 200 "default-server"; }
  }
  include $T/ng/family.conf;
}
EOF
if ! out=$("$NGINX" -t -p "$T/ng" -e "$T/ng/logs/error.log" -c "$T/ng/nginx.conf" 2>&1); then
  echo "$out" | tail -5
  bad "nginx -t rejects the rendered template"
  exit 1
fi
ok "nginx -t accepts the rendered template"
"$NGINX" -p "$T/ng" -e "$T/ng/logs/error.log" -c "$T/ng/nginx.conf" -g 'daemon off;' 2>/dev/null &
pids+=($!)
for _ in $(seq 50); do curl -s -o /dev/null "http://127.0.0.1:$HTTP/" && curl -s -o /dev/null "http://127.0.0.1:$UP/" && break; sleep 0.1; done

# get <host> <path>: body and status over HTTPS.
get() { curl -sk --resolve "$1:$HTTPS:127.0.0.1" -w ' %{http_code}' "https://$1:$HTTPS$2"; }
expect() { local r; r=$(get "$2" "$3"); if [ "$r" = "$4" ]; then ok "$1"; else bad "$1: got '$r', want '$4'"; fi; }

expect "meera.fam.test serves voucher-meera from disk" meera.fam.test / "file:voucher-meera
 200"
expect "a nested file from disk" meera.fam.test /sub/page.html "page:sub
 200"
r=$(get www.fam.test /); [ "${r##* }" = 404 ] && ! grep -q "file:" <<<"$r" && ok "www.fam.test (reserved) is 404" || bad "www: $r"
r=$(get xn--abc.fam.test /); [ "$r" = "default-server 200" ] && ok "xn--abc.fam.test not answered by the family server" || bad "xn--: $r"
r=$(get a.b.fam.test /); [ "$r" = "default-server 200" ] && ok "a.b.fam.test not answered by the family server" || bad "a.b: $r"
r=$(get fam.test /); [ "$r" = "default-server 200" ] && ok "the bare suffix is not answered by the family server" || bad "bare: $r"
expect "suspended marker rewrites to /internal/suspended" down.fam.test /x.html "app:/internal/suspended host=down.fam.test 200"
expect "offline marker rewrites to /internal/offline" off.fam.test / "app:/internal/offline host=off.fam.test 200"
expect "passcode marker rewrites to /internal/passcode/<path>, query kept" lock.fam.test '/a/b.html?x=1' "app:/internal/passcode/a/b.html?x=1 host=lock.fam.test 200"
expect "lives-elsewhere marker rewrites to /internal/family/<path>" moved.fam.test /p/q "app:/internal/family/p/q host=moved.fam.test 200"
r=$(get meera.fam.test /internal/suspended); [ "${r##* }" = 404 ] && ! grep -q "app:" <<<"$r" && ok "/internal/ from outside is 404" || bad "outside /internal/: $r"
expect "a missing file goes to the app with its path and Host" meera.fam.test /missing.html "app:/missing.html host=meera.fam.test 200"
expect "a site that is not there goes to the app" nobody.fam.test / "app:/ host=nobody.fam.test 200"
expect "/v1/x is proxied with Host" meera.fam.test '/v1/x?y=2' "app:/v1/x?y=2 host=meera.fam.test 200"
r=$(curl -s -o /dev/null -w '%{http_code} %{redirect_url}' --resolve "meera.fam.test:$HTTP:127.0.0.1" "http://meera.fam.test:$HTTP/a?b=1")
[ "$r" = "301 https://meera.fam.test/a?b=1" ] && ok "port 80 redirects to https" || bad "http redirect: $r"
grep -q "meera.fam.test /sub/page.html 200" "$T/ng/logs/analytics.log" 2>/dev/null && ok "analytics log written" || bad "analytics log"

[ "$fail" = 0 ] && echo "family nginx fixture: ok" || { echo "family nginx fixture: FAILED"; tail -5 "$T/ng/logs/error.log" 2>/dev/null; exit 1; }
