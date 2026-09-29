#!/usr/bin/env bash
# End-to-end check of site passcodes against a real server binary (and a
# real nginx for the custom-domain marker rewrite), all on this machine.
#
# A canary string goes into every kind of file of a test site; the site is
# locked; every address is swept (site host, person path while a certificate
# is pending, the legacy content host, a claimed free name, a custom domain
# through nginx, link-preview bots) and the canary must never come back
# without the unlock. Then the unlock works on that one host, a new passcode
# signs everyone out, SITE_PASSCODES=off refuses new locks (409) while the
# existing one keeps asking, and the server's log never holds a passcode or
# an unlock cookie.
#
#   DB_DSN=postgres://...fresh database with db/schema.sql... bash scripts/e2e-passcode.sh
#
# BIN defaults to a fresh build in a temp dir. Needs curl, psql or docker
# (for the custom-domain row), and nginx (skipped with a note if missing).
set -euo pipefail
: "${DB_DSN:?DB_DSN must name a throwaway database with db/schema.sql applied}"
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
PORT=${PORT:-18093}
NGINX_PORT=${NGINX_PORT:-18094}
DOMAIN=simple-host.test
CANARY="CANARY-e2e-$(openssl rand -hex 6)"
CODE1="Pass-$(openssl rand -hex 4)"
CODE2="Pass-$(openssl rand -hex 4)"
ADMIN=$(openssl rand -hex 32)
PKEY=$(openssl rand -base64 32)
fail() { echo "FAIL: $*" >&2; exit 1; }
cleanup() {
  [ -n "${SRV:-}" ] && kill "$SRV" 2>/dev/null || true
  [ -f "$WORK/nginx.pid" ] && kill "$(cat "$WORK/nginx.pid")" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

BIN=${BIN:-}
if [ -z "$BIN" ]; then
  (cd "$ROOT" && CGO_ENABLED=0 go build -o "$WORK/simple-host" ./cmd/server)
  BIN=$WORK/simple-host
fi
mkdir -p "$WORK/sites" "$WORK/certs/ready" "$WORK/certs/requests"

start() { # $1 = SITE_PASSCODES
  DB_DSN="$DB_DSN" DATA_DIR="$WORK/sites" SITE_DOMAIN=$DOMAIN ADMIN_API_KEY=$ADMIN PORT=$PORT BIND_ADDR=127.0.0.1 \
    PERSON_HOSTS=canonical SITE_HOSTS=canonical SITE_CERT_DIR="$WORK/certs" \
    PASSCODE_ENC_KEY="$PKEY" SITE_PASSCODES="$1" \
    "$BIN" >>"$WORK/server.log" 2>&1 &
  SRV=$!
  for _ in $(seq 1 50); do curl -s -o /dev/null "http://127.0.0.1:$PORT/healthz" && return; sleep 0.2; done
  cat "$WORK/server.log" >&2; fail "server did not start"
}
stop() { kill "$SRV"; wait "$SRV" 2>/dev/null || true; SRV=; }

# req HOST PATH [curl args...]: as nginx would pass it (HTTPS in front).
req() { local host=$1 path=$2; shift 2; curl -s -H "Host: $host" -H "X-Forwarded-Proto: https" "$@" "http://127.0.0.1:$PORT$path"; }
code() { local host=$1 path=$2; shift 2; req "$host" "$path" -o /dev/null -w '%{http_code}' "$@"; }

start on
acct() { req $DOMAIN /v1/admin/users -H "X-API-Key: $ADMIN" -H 'Content-Type: application/json' -d "{\"emails\":[\"$1\"]}"; }
json() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1]))" "$1"; }
A=$(acct "olive-$(openssl rand -hex 3)@example.com")
KEY=$(echo "$A" | json 'd["created"][0]["api_key"]')
H=$(echo "$A" | json 'd["created"][0]["handle"]')
B=$(acct "oscar-$(openssl rand -hex 3)@example.com")
KEY2=$(echo "$B" | json 'd["created"][0]["api_key"]')
H2=$(echo "$B" | json 'd["created"][0]["handle"]')
touch "$WORK/certs/ready/$H" # olive has her certificate; oscar's is pending
SITE=trip.$H.$DOMAIN
PERSON=$H.$DOMAIN
PERSON2=$H2.$DOMAIN
FREE=goa-$(openssl rand -hex 3).$DOMAIN
CUSTOM=trip-$(openssl rand -hex 3).example.org

files="{\"files\":{\"index.html\":\"<h1>$CANARY</h1>\",\"app.js\":\"var c='$CANARY';\",\"data.json\":\"{\\\"c\\\":\\\"$CANARY\\\"}\",\"img.svg\":\"<svg xmlns='http://www.w3.org/2000/svg'><text>$CANARY</text></svg>\",\"days/index.html\":\"$CANARY\",\"404.html\":\"$CANARY\",\"sitemap.xml\":\"<urlset>$CANARY</urlset>\"}}"
[ "$(code $DOMAIN /v1/sites/trip/files -H "X-API-Key: $KEY" -H 'Content-Type: application/json' -d "$files")" = 201 ] || fail "deploy"
[ "$(code $DOMAIN /v1/sites/camp/files -H "X-API-Key: $KEY2" -H 'Content-Type: application/json' -d "$files")" = 201 ] || fail "deploy camp"
req $DOMAIN /v1/sites/trip/state -X PUT -H "X-API-Key: $KEY" -H 'Content-Type: application/json' -d "{\"c\":\"$CANARY\"}" -o /dev/null

lock() { req $DOMAIN "/v1/sites/$1/lock" -X PUT -H "X-API-Key: $2" -H 'Content-Type: application/json' -d "{\"passcode\":\"$3\"}" -w '%{http_code}' -o "$WORK/lock.json"; }
[ "$(lock trip "$KEY" "$CODE1")" = 200 ] || { cat "$WORK/lock.json"; fail "lock"; }
[ "$(lock camp "$KEY2" "$CODE1")" = 200 ] || fail "lock camp"
[ -f "$WORK/sites/by-id" ] || true
[ "$(code "$SITE" /)" = 401 ] || fail "site host is not the gate"
[ "$(code "$SITE" /trip/index.html)" = 401 ] || fail "old-address redirect ran before the gate"
# A claimed free name (Go serves it) ...
[ "$(code $DOMAIN /v1/sites/trip/domain -H "X-API-Key: $KEY" -H 'Content-Type: application/json' -d "{\"domain\":\"$FREE\"}")" = 200 ] || fail "claim $FREE"
for path in / /app.js /data.json /img.svg /days/ /nope /sitemap.xml; do
  case $(req "$FREE" "$path" -A "Slackbot-LinkExpanding 1.0") in *"$CANARY"*) fail "free name $path leaked" ;; esac
done
[ "$(code "$FREE" /)" = 401 ] || fail "free name is not the gate"
hdr=$(curl -s -D - -o /dev/null -H "Host: $FREE" -H "X-Forwarded-Proto: https" -H "Origin: https://$FREE" -H 'Sec-Fetch-Site: same-origin' --data-urlencode "passcode=$CODE1" --data-urlencode "next=/app.js" "http://127.0.0.1:$PORT/v1/site-unlock")
CKF=$(echo "$hdr" | tr -d '\r' | sed -n 's/^[Ss]et-[Cc]ookie: \([^;]*\).*/\1/p' | grep sh_pass_ | head -1)
req "$FREE" /app.js -H "Cookie: $CKF" | grep -q "$CANARY" || fail "unlocked free name"
echo "free name: gate, then open after the unlock"
UID1=$(for u in $(ls "$WORK/sites/by-id"); do if [ -f "$WORK/sites/by-id/$u/trip/passcode" ]; then echo "$u"; fi; done)
[ -n "$UID1" ] || fail "no passcode marker next to current"
FOLDER=$WORK/sites/by-id/$UID1/trip

# ... and a custom domain served from disk by nginx, as the issuer's template does.
if command -v nginx >/dev/null; then
  SITE_ID=$(req $DOMAIN /v1/sites -H "X-API-Key: $KEY" | python3 -c "import json,sys; print([s['id'] for s in json.load(sys.stdin)['sites'] if s['name']=='trip'][0])" 2>/dev/null || true)
  mkdir -p "$WORK/nginx/logs"
  cat >"$WORK/nginx/nginx.conf" <<EOF
pid $WORK/nginx.pid;
error_log $WORK/nginx/logs/error.log;
events {}
http {
  access_log off;
  client_body_temp_path $WORK/nginx; proxy_temp_path $WORK/nginx; fastcgi_temp_path $WORK/nginx; uwsgi_temp_path $WORK/nginx; scgi_temp_path $WORK/nginx;
  server {
    listen 127.0.0.1:$NGINX_PORT;
    server_name $CUSTOM;
    location ^~ /v1/ { proxy_pass http://127.0.0.1:$PORT; proxy_set_header Host \$host; proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for; proxy_set_header X-Forwarded-Proto https; }
    location ^~ /internal/ { internal; proxy_pass http://127.0.0.1:$PORT; proxy_set_header Host \$host; proxy_set_header X-Forwarded-Proto https; }
    root $FOLDER/current;
    index index.html;
    location / {
      if (-f $FOLDER/suspended) { rewrite ^ /internal/suspended last; }
      if (-f $FOLDER/offline) { rewrite ^ /internal/offline last; }
      if (-f $FOLDER/passcode) { rewrite ^ /internal/passcode\$uri last; }
      try_files \$uri \$uri/ =404;
    }
  }
}
EOF
  nginx -p "$WORK/nginx" -c "$WORK/nginx/nginx.conf" 2>/dev/null || fail "nginx did not start"
  # The domain moves from the free name to the custom one (verified by hand).
  if command -v psql >/dev/null; then P="psql $DB_DSN -qAt"; else P=""; fi
  if [ -n "$P" ]; then
    $P -c "UPDATE sites SET custom_domain = '$CUSTOM', domain_status = 'active', domain_verified_at = now() WHERE name = 'trip' AND user_id = '$UID1'"
    NG=1
  else
    echo "note: no psql; custom-domain leg skipped"; NG=
  fi
else
  echo "note: no nginx; custom-domain leg skipped"; NG=
fi
ngx() { local path=$1; shift; curl -s -H "Host: $CUSTOM" "$@" "http://127.0.0.1:$NGINX_PORT$path"; }

# ---- the sweep: nothing of the site without the unlock ----------------------
n=0
for path in / /index.html /app.js /data.json /img.svg /days/ /days /nope /sitemap.xml "/app.js?x=1" /trip/app.js "/$H/trip/app.js"; do
  for ua in curl/8 "Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)" "facebookexternalhit/1.1" "WhatsApp/2.23" "Twitterbot/1.0"; do
    for addr in site person legacy legacy-marker person2 custom; do
      case $addr in
        site) out=$(req "$SITE" "$path" -A "$ua") ;;
        person) out=$(req "$PERSON" "/trip$path" -A "$ua") ;;
        person2) out=$(req "$PERSON2" "/camp$path" -A "$ua") ;;
        legacy) out=$(req "sites.$DOMAIN" "/internal/site-redirect/$H/trip$path" -A "$ua") ;;
        legacy-marker) out=$(req "sites.$DOMAIN" "/internal/passcode/$H/trip$path" -A "$ua") ;;
        custom) [ -n "$NG" ] || continue; out=$(ngx "$path" -A "$ua") ;;
      esac
      case $out in *"$CANARY"*) fail "$addr $path ($ua) leaked the canary" ;; esac
      n=$((n + 1))
    done
  done
done
for path in /v1/sites/trip/state "/v1/u/$H/sites/trip/state" /v1/sites/trip/collections/x /v1/sites/trip/data/x; do
  for host in $DOMAIN "$SITE" "$PERSON" "sites.$DOMAIN"; do
    out=$(req "$host" "$path")
    case $out in *"$CANARY"*) fail "data $host$path leaked" ;; esac
    n=$((n + 1))
  done
done
[ "$(code "$PERSON2" /camp/)" = 401 ] || fail "person path is not the gate"
[ -z "$NG" ] || [ "$(ngx / -o /dev/null -w '%{http_code}')" = 401 ] || fail "custom domain through nginx is not the gate"
[ -z "$NG" ] || [ "$(ngx /internal/passcode/app.js -o /dev/null -w '%{http_code}')" = 404 ] || fail "/internal/ reachable from outside"
echo "sweep: $n requests, no canary"

# ---- unlock on one host -------------------------------------------------------
unlock() { # host code next [base]
  curl -s -D - -o /dev/null -H "Host: $1" -H "X-Forwarded-Proto: https" -H "Origin: https://$1" -H 'Sec-Fetch-Site: same-origin' \
    --data-urlencode "passcode=$2" --data-urlencode "next=$3" "${4:-http://127.0.0.1:$PORT}/v1/site-unlock"
}
# The person path (oscar's certificate is pending).
hdr=$(unlock "$PERSON2" "$CODE1" /camp/app.js)
echo "$hdr" | grep -qi '^location: /camp/app.js' || fail "unlock did not 303 back: $hdr"
CK=$(echo "$hdr" | tr -d '\r' | sed -n 's/^[Ss]et-[Cc]ookie: \([^;]*\).*/\1/p' | grep sh_pass_ | head -1)
[ -n "$CK" ] || fail "no unlock cookie"
req "$PERSON2" /camp/app.js -H "Cookie: $CK" | grep -q "$CANARY" || fail "unlocked person path"
case $(req "$SITE" /app.js -H "Cookie: $CK") in *"$CANARY"*) fail "cookie opened another site" ;; esac
[ "$(code "$PERSON2" /camp/app.js -H "Cookie: $CK" -H 'Sec-Fetch-Site: same-site' -H 'Sec-Fetch-Mode: no-cors' -H 'Sec-Fetch-Dest: script')" = 401 ] || fail "XSSI from a sibling"
# The free name's cookie is useless on the custom domain, and vice versa.
if [ -n "$NG" ]; then
  case $(ngx /app.js -H "Cookie: $CKF") in *"$CANARY"*) fail "free-name cookie opened the custom domain" ;; esac
  hdr=$(unlock "$CUSTOM" "$CODE1" /app.js "http://127.0.0.1:$NGINX_PORT")
  CKD=$(echo "$hdr" | tr -d '\r' | sed -n 's/^[Ss]et-[Cc]ookie: \([^;]*\).*/\1/p' | grep sh_pass_ | head -1)
  [ -n "$CKD" ] || fail "no unlock through nginx: $hdr"
  ngx /app.js -H "Cookie: $CKD" | grep -q "$CANARY" || fail "unlocked custom domain"
  ngx /v1/sites/trip/state -H "Cookie: $CKD" -H "Origin: http://$CUSTOM" | grep -q "$CANARY" || fail "unlocked state read on the custom domain"
fi

# A new passcode signs everyone out.
[ "$(lock camp "$KEY2" "$CODE2")" = 200 ] || fail "change"
[ "$(code "$PERSON2" /camp/app.js -H "Cookie: $CK")" = 401 ] || fail "old cookie still works"
if [ -n "$NG" ]; then
  [ "$(lock trip "$KEY" "$CODE2")" = 200 ] || fail "change trip"
  [ "$(ngx /app.js -H "Cookie: $CKD" -o /dev/null -w '%{http_code}')" = 401 ] || fail "old custom-domain cookie still works"
fi
echo "unlock: ok on its host only; a new passcode signs everyone out"

# ---- SITE_PASSCODES=off: new locks refused, the existing one keeps asking --
stop; start off
st=$(lock camp "$KEY2" "Other-$CODE1")
[ "$st" = 409 ] && grep -q passcodes_not_enabled "$WORK/lock.json" || fail "off: PUT /lock answered $st $(cat "$WORK/lock.json")"
[ "$(code "$PERSON2" /camp/)" = 401 ] || fail "off: the existing lock stopped asking"
echo "off: 409 passcodes_not_enabled; existing locks unchanged"

# ---- log hygiene -------------------------------------------------------------
for s in "$CODE1" "$CODE2" "Other-$CODE1" "${CK#*=}" "${CKF#*=}"; do
  grep -qF -- "$s" "$WORK/server.log" && fail "the log holds a secret"
done
grep -q 'passcode_unlock ok=true' "$WORK/server.log" || fail "unlock not logged"
echo "log: no passcode or cookie"
echo "PASS"
