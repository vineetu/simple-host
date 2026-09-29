#!/usr/bin/env bash
# Behaviour test for deploy/compose/Caddyfile, the small box's front door: a
# throwaway Caddy (the image the compose file runs) in front of a stub app on a
# private Docker network, over plain HTTP (the HTTPS block's hostname and
# certificate lines are swapped for a port; every rule inside it is as shipped).
# It proves the content host hands the application what is not a file on disk,
# the way the hosted nginx does: an old name of a renamed site, a site with its
# own address, a taken-down, offline or passcode-locked site, the person page, a
# missing file and the bare root; and that
# /internal/* from outside is 404 on every host. Skipped without Docker.
#   bash deploy/compose/Caddyfile_test.sh
set -euo pipefail
cd "$(dirname "$0")"
if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
  echo "== Caddyfile behaviour == skipped (no docker)"
  exit 0
fi
IMAGE=${CADDY_IMAGE:-caddy:2-alpine}
T=$(mktemp -d)
N=shcaddytest$$
cleanup() {
  docker rm -f "$N-caddy" "$N-app" >/dev/null 2>&1 || true
  docker network rm "$N" >/dev/null 2>&1 || true
  # Caddy writes its access log as root.
  docker run --rm -v "$T:/t" alpine rm -rf /t/log >/dev/null 2>&1 || true
  rm -rf "$T"
}
trap cleanup EXIT
fail=0
ok() { echo "  ok — $1"; }
bad() { echo "  FAIL: $1"; fail=1; }

# The shipped file with its HTTPS listener made a plain port.
python3 - "$T/Caddyfile" <<'EOF'
import re, sys
s = open("Caddyfile").read()
s = re.sub(r"https:// \{\n\ttls \{\n\t\ton_demand\n\t\}\n", ":8443 {\n", s, count=1)
assert ":8443 {" in s, "the HTTPS block changed shape; update this test"
open(sys.argv[1], "w").write(s)
EOF

# A stub app: answers every request with what it was asked for.
cat > "$T/app.py" <<'EOF'
from http.server import BaseHTTPRequestHandler, HTTPServer
class H(BaseHTTPRequestHandler):
    def do_GET(self):
        body = ("APP " + self.path + " orig=" + (self.headers.get("X-Original-URI") or "")).encode()
        self.send_response(404 if self.path.startswith("/internal/notfound") else 200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *a): pass
HTTPServer(("0.0.0.0", 8090), H).serve_forever()
EOF

# The site farm: alice/live is served; alice/gone has an own address; alice/down
# is taken down; alice/old never existed here (a renamed site's old name).
D="$T/data/sites/handles/alice"
mkdir -p "$D/live/current/sub" "$D/gone/current" "$D/down/current"
echo "LIVE PAGE" > "$D/live/current/index.html"
echo "SUB PAGE" > "$D/live/current/sub/index.html"
echo "GONE PAGE" > "$D/gone/current/index.html"
mkdir -p "$D/locked/current" "$D/off/current"
echo "LOCKED PAGE" > "$D/locked/current/index.html"
echo "OFF PAGE" > "$D/off/current/index.html"
touch "$D/gone/domain-redirect" "$D/down/suspended" "$D/locked/passcode" "$D/off/offline" "$D/off/passcode"
mkdir -p "$T/log"

docker network create "$N" >/dev/null
docker run -d --name "$N-app" --network "$N" --network-alias app -v "$T/app.py:/app.py:ro" python:3-alpine python /app.py >/dev/null
docker run -d --name "$N-caddy" --network "$N" -p 127.0.0.1::8443 -p 127.0.0.1::80 \
  -v "$T/Caddyfile:/etc/caddy/Caddyfile:ro" -v "$T/data/sites:/data/sites:ro" -v "$T/log:/var/log/caddy" "$IMAGE" >/dev/null
P=$(docker port "$N-caddy" 8443/tcp | head -1 | sed 's/.*://')
P80=$(docker port "$N-caddy" 80/tcp | head -1 | sed 's/.*://')
for _ in $(seq 1 60); do
  curl -fsS -o /dev/null -H "Host: ev.test" "http://127.0.0.1:$P/v1/ready" 2>/dev/null && break
  sleep 1
done

# get HOST PATH -> "status body" (redirects not followed).
get() { curl -sS -o "$T/body" -w '%{http_code}' -H "Host: $1" "http://127.0.0.1:$P$2" 2>/dev/null; echo " $(tr -d '\n' < "$T/body")"; }
expect() { # what HOST PATH want-status want-body-substring
  local got; got=$(get "$2" "$3")
  if [[ "$got" == "$4 "* ]] && [[ "$got" == *"$5"* ]]; then ok "$1"; else bad "$1: got '$got', want $4 with '$5'"; fi
}

echo "== Caddyfile behaviour =="
C=sites.ev.test
expect "a site's page is served from disk"            $C /alice/live/          200 "LIVE PAGE"
expect "a site's sub-page too"                         $C /alice/live/sub/      200 "SUB PAGE"
expect "a bare site path gains its slash"              $C /alice/live           308 ""
expect "an old name goes to the app's redirect"        $C /alice/old/x/y        200 "APP /internal/site-redirect/alice/old/x/y"
expect "the query string goes with it"                 $C "/alice/old/?a=1"     200 "APP /internal/site-redirect/alice/old/?a=1"
expect "a site with its own address redirects there"   $C /alice/gone/p         200 "APP /internal/domain-redirect/alice/gone/p"
expect "a taken-down site answers the take-down page"  $C /alice/down/          200 "APP /internal/suspended"
expect "an offline site answers the offline page (over passcode)" $C /alice/off/ 200 "APP /internal/offline"
expect "a locked site goes to the passcode gate"       $C /alice/locked/        200 "APP /internal/passcode/alice/locked/"
expect "the gate gets the path and query"              $C "/alice/locked/sub/p.html?a=1&b=2" 200 "APP /internal/passcode/alice/locked/sub/p.html?a=1&b=2"
expect "a locked site's file is never served"          $C /alice/locked/index.html 200 "APP /internal/passcode/alice/locked/index.html"
expect "/<handle> is the person page"                  $C /alice                200 "APP /internal/showcase/alice"
expect "/<handle>/ too"                                $C /alice/               200 "APP /internal/showcase/alice"
expect "a missing file is the branded not-found page"  $C /alice/live/nope.css  404 "APP /internal/notfound orig=/alice/live/nope.css"
expect "the bare root is the not-found page"           $C /                     404 "APP /internal/notfound orig=/"
expect "/internal/ from outside, content host"         $C /internal/tls-ask?domain=x 404 ""
expect "/internal/ from outside, event host"           ev.test /internal/showcase/alice 404 ""
expect "the API still reaches the app"                 ev.test /v1/sites        200 "APP /v1/sites"
expect "the dashboard still reaches the app"           ev.test /                200 "APP /"
got=$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:$P80/internal/tls-ask?domain=x" 2>/dev/null || true)
[ "$got" = "404" ] && ok "/internal/ from outside, plain HTTP" || bad "/internal/ on :80 answered $got"
got=$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:$P80/v1/setup/state" 2>/dev/null || true)
[ "$got" = "200" ] && ok "the setup page's API on plain HTTP" || bad "/v1/setup/state on :80 answered $got"

exit $fail
