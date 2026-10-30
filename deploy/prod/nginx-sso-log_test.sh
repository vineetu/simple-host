#!/bin/bash
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
T=$(mktemp -d)
trap 'test ! -f "$T/nginx.pid" || kill "$(cat "$T/nginx.pid")"; rm -rf "$T"' EXIT
PORT=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')
cat > "$T/nginx.conf" <<CONF
pid $T/nginx.pid;
error_log $T/error.log;
events {}
http {
 include $HERE/nginx-sso-log.conf;
 access_log $T/access.log shsafe;
 server { listen 127.0.0.1:$PORT; location / { return 204; } }
}
CONF
/usr/sbin/nginx -p "$T" -c "$T/nginx.conf"
for route in /v1/visitor/establish /v1/sso/start /v1/sso/hop /v1/sso/signed-out; do
 curl -fsS -H 'Referer: https://example.test/v1/visitor/establish?once=sso-log-canary' "http://127.0.0.1:$PORT$route?once=sso-log-canary" >/dev/null
done
kill -QUIT "$(cat "$T/nginx.pid")"
for n in {1..20}; do test ! -f "$T/nginx.pid" && break; sleep .1; done
! grep -q 'sso-log-canary' "$T/access.log" "$T/error.log"
grep -q '/v1/visitor/establish' "$T/access.log"
echo 'nginx SSO logs: queries and referrers absent'
