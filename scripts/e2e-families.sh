#!/usr/bin/env bash
# End-to-end check of address families against a real server binary and a
# real nginx serving the family issuer's template, all on this machine.
#
# An account gets a family *.fam.test (site prefix voucher-) through the
# admin API; the app makes the link and the issuer request, the issuer
# renders the server file and the ready marker (issue.sh --render/--ready,
# as production does), and a private nginx serves it with a self-signed
# *.fam.test certificate. Then, over HTTPS with curl --resolve:
#   - <label>.fam.test serves the account's site voucher-<label> from disk;
#     another account's site, www and a missing site do not
#   - the site's own address redirects to its family address (main address)
#   - the site API on the family host: the host names the site, the page's
#     own origin saves, a sibling origin does not
#   - a renamed site's old label redirects through the app; offline shows
#     the offline page; a site with a domain of its own redirects there
#   - the passcode canary sweep: a locked site's files never come back on
#     the family host (every path, link-preview bots, the marker rewrite)
#     until the unlock there, and the unlock is that host's only
#   - the analytics log line names the family host
#
#   DB_DSN=postgres://...fresh database with db/schema.sql... bash scripts/e2e-families.sh
#
# BIN defaults to a fresh build. Needs curl, psql (or docker exec, see PSQL),
# openssl, python3 and nginx (skipped with a note if nginx is missing).
set -euo pipefail
: "${DB_DSN:?DB_DSN must name a throwaway database with db/schema.sql applied}"
ROOT=$(cd "$(dirname "$0")/.." && pwd)
NGINX=$(command -v nginx || true)
[ -n "$NGINX" ] || [ ! -x /usr/sbin/nginx ] || NGINX=/usr/sbin/nginx
[ -n "$NGINX" ] || { echo "e2e-families: skip (no nginx)"; exit 0; }
PSQL=${PSQL:-psql "$DB_DSN" -qAt}
WORK=$(mktemp -d)
port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
PORT=$(port); HTTPS=$(port)
DOMAIN=simple-host.test
SUFFIX=fam-$(openssl rand -hex 3).test
CANARY="CANARY-fam-$(openssl rand -hex 6)"
CODE="Pass-$(openssl rand -hex 4)"
ADMIN=$(openssl rand -hex 32)
PKEY=$(openssl rand -base64 32)
fail() { echo "FAIL: $*" >&2; exit 1; }
cleanup() {
  [ -n "${SRV:-}" ] && kill "$SRV" 2>/dev/null || true
  [ -f "$WORK/ng/nginx.pid" ] && kill "$(cat "$WORK/ng/nginx.pid")" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

BIN=${BIN:-}
if [ -z "$BIN" ]; then
  (cd "$ROOT" && CGO_ENABLED=0 go build -o "$WORK/simple-host" ./cmd/server)
  BIN=$WORK/simple-host
fi
mkdir -p "$WORK/sites" "$WORK/certs/ready" "$WORK/certs/requests" "$WORK/fam/requests" "$WORK/fam/ready" "$WORK/fam/failed" "$WORK/ng/logs" "$WORK/live/$SUFFIX"

DB_DSN="$DB_DSN" DATA_DIR="$WORK/sites" SITE_DOMAIN=$DOMAIN ADMIN_API_KEY=$ADMIN PORT=$PORT BIND_ADDR=127.0.0.1 \
  PERSON_HOSTS=canonical SITE_HOSTS=canonical SITE_CERT_DIR="$WORK/certs" \
  ADDRESS_FAMILY_CERT_DIR="$WORK/fam" ADDRESS_FAMILY_CACHE_SECONDS=1 \
  PASSCODE_ENC_KEY="$PKEY" SITE_PASSCODES=on \
  "$BIN" >>"$WORK/server.log" 2>&1 &
SRV=$!
for _ in $(seq 1 50); do curl -s -o /dev/null "http://127.0.0.1:$PORT/healthz" && break; sleep 0.2; done
curl -s -o /dev/null "http://127.0.0.1:$PORT/healthz" || { cat "$WORK/server.log" >&2; fail "server did not start"; }

# app HOST PATH [curl args]: straight to the app, as nginx passes it.
app() { local host=$1 path=$2; shift 2; curl -s -H "Host: $host" -H "X-Forwarded-Proto: https" "$@" "http://127.0.0.1:$PORT$path"; }
json() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1]))" "$1"; }
acct() { app $DOMAIN /v1/admin/users -H "X-API-Key: $ADMIN" -H 'Content-Type: application/json' -d "{\"emails\":[\"$1\"]}"; }
OLIVE="olive-$(openssl rand -hex 3)@example.com"
A=$(acct "$OLIVE")
KEY=$(echo "$A" | json 'd["created"][0]["api_key"]')
H=$(echo "$A" | json 'd["created"][0]["handle"]')
UID1=$($PSQL -c "SELECT id FROM users WHERE username = '$OLIVE'")
B=$(acct "oscar-$(openssl rand -hex 3)@example.com")
KEY2=$(echo "$B" | json 'd["created"][0]["api_key"]')
touch "$WORK/certs/ready/$H"

deploy() { # key site body
  local st
  st=$(app $DOMAIN "/v1/sites/$2/files" -H "X-API-Key: $1" -H 'Content-Type: application/json' -d "$3" -o /dev/null -w '%{http_code}')
  [ "$st" = 201 ] || fail "deploy $2: $st"
}
plain() { echo "{\"files\":{\"index.html\":\"<h1>$1</h1>\",\"sub/index.html\":\"sub-$1\"}}"; }
canary="{\"files\":{\"index.html\":\"<h1>$CANARY</h1>\",\"app.js\":\"var c='$CANARY';\",\"data.json\":\"{\\\"c\\\":\\\"$CANARY\\\"}\",\"days/index.html\":\"$CANARY\",\"404.html\":\"$CANARY\"}}"
deploy "$KEY" voucher-meera "$(plain voucher-meera)"
deploy "$KEY" voucher-anu "$(plain voucher-anu)"
deploy "$KEY" voucher-off "$(plain voucher-off)"
deploy "$KEY" voucher-moved "$(plain voucher-moved)"
deploy "$KEY" voucher-lock "$canary"
deploy "$KEY" meera "$(plain meera)"
deploy "$KEY2" voucher-zed "$(plain voucher-zed)"

# ---- the family: admin connects it (proof exempt, operator certificate) -----
F=$(app $DOMAIN "/v1/admin/users/$UID1/address-families" -H "X-API-Key: $ADMIN" -H 'Content-Type: application/json' \
  -d "{\"suffix\":\"*.$SUFFIX\",\"site_prefix\":\"voucher-\",\"proof_exempt\":true,\"cert_name\":\"$SUFFIX\"}")
FID=$(echo "$F" | json 'd["id"]')
[ "$(echo "$F" | json 'd["status"]')" = pending ] || fail "new family: $F"
# DNS for fam.test cannot be proven here: stand in for a passing check, then
# let the app's check make the link and the issuer request.
$PSQL -c "UPDATE address_families SET verified_at = now(), status = 'active' WHERE id = '$FID'"
app $DOMAIN "/v1/admin/address-families/$FID/check" -X POST -H "X-API-Key: $ADMIN" -o /dev/null
[ "$(readlink "$WORK/sites/families/$SUFFIX")" = "../by-id/$UID1" ] || fail "no family link"
[ -f "$WORK/fam/requests/$SUFFIX" ] || fail "no issuer request"
[ "$(sed -n 5p "$WORK/fam/requests/$SUFFIX")" = voucher- ] || fail "request prefix"
# Not live yet: nothing is served under the family.
case $(app meera.$SUFFIX /) in *voucher-meera*) fail "served before the issuer made it live" ;; esac

# ---- the issuer's file and ready marker, served by a private nginx ----------
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 30 \
  -keyout "$WORK/live/$SUFFIX/privkey.pem" -out "$WORK/live/$SUFFIX/fullchain.pem" -subj "/CN=*.$SUFFIX" \
  -addext "subjectAltName=DNS:*.$SUFFIX" >/dev/null 2>&1
printf 'STATE=%s/fam\nSITES=%s/sites/families\nLE_LIVE=%s/live\nTEMPLATE=%s/deploy/family-certs/vhost.conf.template\nSITE_DOMAIN=%s\n' \
  "$WORK" "$WORK" "$WORK" "$ROOT" "$DOMAIN" > "$WORK/issuer.conf"
SIMPLE_HOST_FAMILY_CERTS_CONF="$WORK/issuer.conf" bash "$ROOT/deploy/family-certs/issue.sh" --render $SUFFIX > "$WORK/rendered"
sed -e "s|listen 443 ssl;|listen 127.0.0.1:$HTTPS ssl;|" -e '/^# http -> https/,$d' \
    -e "s|/srv/simple-host/sites/families|$WORK/sites/families|g" \
    -e "s|/etc/letsencrypt/live|$WORK/live|g" \
    -e "s|/var/log/simple-host/analytics.log|$WORK/ng/logs/analytics.log|g" \
    -e "s|127.0.0.1:8090|127.0.0.1:$PORT|g" "$WORK/rendered" > "$WORK/ng/family.conf"
# (the port-80 server is cut off above; deploy/family-certs/nginx_fixture_test.sh covers it)
cat > "$WORK/ng/nginx.conf" <<EOF
pid $WORK/ng/nginx.pid;
error_log $WORK/ng/logs/error.log;
events {}
http {
  access_log off;
  log_format shanalytics '\$host \$request_uri \$status';
  client_body_temp_path $WORK/ng; proxy_temp_path $WORK/ng; fastcgi_temp_path $WORK/ng; uwsgi_temp_path $WORK/ng; scgi_temp_path $WORK/ng;
  include $WORK/ng/family.conf;
}
EOF
"$NGINX" -t -p "$WORK/ng" -e "$WORK/ng/logs/error.log" -c "$WORK/ng/nginx.conf" >/dev/null 2>&1 || { "$NGINX" -t -p "$WORK/ng" -e "$WORK/ng/logs/error.log" -c "$WORK/ng/nginx.conf"; fail "nginx -t"; }
"$NGINX" -p "$WORK/ng" -e "$WORK/ng/logs/error.log" -c "$WORK/ng/nginx.conf" 2>/dev/null || fail "nginx did not start"
SIMPLE_HOST_FAMILY_CERTS_CONF="$WORK/issuer.conf" bash "$ROOT/deploy/family-certs/issue.sh" --ready $SUFFIX > "$WORK/fam/ready/$SUFFIX"
grep -qx 'prefix=voucher-' "$WORK/fam/ready/$SUFFIX" || fail "ready marker: $(cat "$WORK/fam/ready/$SUFFIX")"
sleep 2 # ADDRESS_FAMILY_CACHE_SECONDS=1

ngx() { local host=$1 path=$2; shift 2; curl -sk --resolve "$host:$HTTPS:127.0.0.1" "$@" "https://$host:$HTTPS$path"; }
code() { ngx "$1" "$2" -o /dev/null -w '%{http_code}' "${@:3}"; }

# ---- serving ------------------------------------------------------------------
[ "$(ngx meera.$SUFFIX /)" = "<h1>voucher-meera</h1>" ] || fail "family serves the site from disk: $(ngx meera.$SUFFIX /)"
[ "$(ngx meera.$SUFFIX /sub/)" = "sub-voucher-meera" ] || fail "subdirectory"
[ "$(code zed.$SUFFIX /)" = 404 ] || fail "another account's site answered"
[ "$(code www.$SUFFIX /)" = 404 ] || fail "www answered"
[ "$(code nobody.$SUFFIX /)" = 404 ] || fail "missing site: $(code nobody.$SUFFIX /)"
[ "$(code meera.$SUFFIX /internal/suspended)" = 404 ] || fail "/internal/ reachable from outside"
echo "serving: ok"

# The site's own address redirects to its family address; the list names it.
loc=$(app "voucher-meera.$H.$DOMAIN" /sub/ -o /dev/null -w '%{redirect_url}')
[ "$loc" = "https://meera.$SUFFIX/sub/" ] || fail "site host -> family: $loc"
app $DOMAIN /v1/sites -H "X-API-Key: $KEY" | grep -q "\"family_address\":\"https://meera.$SUFFIX/\"" || fail "family_address in the site list"
echo "main address: ok"

# ---- the site API on the family host ------------------------------------------
st=$(ngx meera.$SUFFIX /v1/sites/meera/state -o /dev/null -w '%{http_code}')
[ "$st" = 200 ] || [ "$st" = 404 ] || fail "state read on the family host: $st"
[ "$(ngx meera.$SUFFIX /v1/sites/voucher-anu/state -o /dev/null -w '%{http_code}')" = 404 ] || fail "another site resolved on the family host"
st=$(ngx meera.$SUFFIX /v1/sites/meera/state -X PUT -H "X-API-Key: $KEY" -H 'Content-Type: application/json' -d '{"n":1}' -o /dev/null -w '%{http_code}')
[ "$st" = 200 ] || fail "owner save through the family host: $st"
st=$(ngx meera.$SUFFIX /v1/sites/meera/state -H "Origin: https://anu.$SUFFIX" -o /dev/null -w '%{http_code}')
[ "$st" = 403 ] || fail "sibling origin read: $st"
ngx meera.$SUFFIX /v1/sites/meera/state -H "Origin: https://meera.$SUFFIX" -D - -o /dev/null | tr -d '\r' | grep -qi "^access-control-allow-origin: https://meera.$SUFFIX" || fail "own origin not allowed"
echo "api: ok"

# ---- rename, offline, a domain of its own -------------------------------------
app $DOMAIN /v1/sites/voucher-anu -X PATCH -H "X-API-Key: $KEY" -H 'Content-Type: application/json' -d '{"name":"voucher-anu2"}' -o /dev/null
loc=$(ngx anu.$SUFFIX /x -o /dev/null -w '%{redirect_url}')
[ "$loc" = "https://anu2.$SUFFIX/x" ] || fail "renamed: $loc"
app $DOMAIN /v1/sites/voucher-off -X PATCH -H "X-API-Key: $KEY" -H 'Content-Type: application/json' -d '{"offline":true}' -o /dev/null
[ "$(code off.$SUFFIX /)" = 503 ] || fail "offline on the family host: $(code off.$SUFFIX /)"
FREE="moved-$(openssl rand -hex 3).$DOMAIN"
app $DOMAIN /v1/sites/voucher-moved/domain -H "X-API-Key: $KEY" -H 'Content-Type: application/json' -d "{\"domain\":\"$FREE\"}" -o /dev/null
[ -f "$WORK/sites/by-id/$UID1/voucher-moved/lives-elsewhere" ] || fail "no lives-elsewhere marker"
loc=$(ngx moved.$SUFFIX /p?q=1 -o /dev/null -w '%{redirect_url}')
[ "$loc" = "https://$FREE/p?q=1" ] || fail "family -> own domain: $loc"
echo "rename, offline, own domain: ok"

# ---- the passcode canary sweep on the family host -----------------------------
st=$(app $DOMAIN /v1/sites/voucher-lock/lock -X PUT -H "X-API-Key: $KEY" -H 'Content-Type: application/json' -d "{\"passcode\":\"$CODE\"}" -o /dev/null -w '%{http_code}')
[ "$st" = 200 ] || fail "lock: $st"
n=0
for path in / /index.html /app.js /data.json /days/ /days /nope "/app.js?x=1" /voucher-lock/app.js; do
  for ua in curl/8 "Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)" "facebookexternalhit/1.1" "WhatsApp/2.23" "Twitterbot/1.0"; do
    out=$(ngx lock.$SUFFIX "$path" -A "$ua")
    case $out in *"$CANARY"*) fail "family host $path ($ua) leaked the canary" ;; esac
    out=$(app lock.$SUFFIX "/internal/passcode$path" -A "$ua")
    case $out in *"$CANARY"*) fail "marker rewrite $path ($ua) leaked the canary" ;; esac
    n=$((n + 2))
  done
done
for path in /v1/sites/lock/state /v1/sites/voucher-lock/state; do
  case $(ngx lock.$SUFFIX "$path") in *"$CANARY"*) fail "data $path leaked" ;; esac
  n=$((n + 1))
done
[ "$(code lock.$SUFFIX /)" = 401 ] || fail "family host is not the gate"
echo "sweep: $n requests, no canary"
hdr=$(ngx lock.$SUFFIX /v1/site-unlock -D - -o /dev/null -H "Origin: https://lock.$SUFFIX" -H 'Sec-Fetch-Site: same-origin' \
  --data-urlencode "passcode=$CODE" --data-urlencode "next=/app.js")
CK=$(echo "$hdr" | tr -d '\r' | sed -n 's/^[Ss]et-[Cc]ookie: \([^;]*\).*/\1/p' | grep sh_pass_ | head -1)
[ -n "$CK" ] || fail "no unlock through nginx: $hdr"
ngx lock.$SUFFIX /app.js -H "Cookie: $CK" | grep -q "$CANARY" || fail "unlocked family host"
case $(app "voucher-lock.$H.$DOMAIN" /app.js -H "Cookie: $CK") in *"$CANARY"*) fail "family unlock opened the site host" ;; esac
echo "unlock: ok on the family host only"

# ---- analytics, log hygiene ---------------------------------------------------
grep -q "^meera.$SUFFIX / 200" "$WORK/ng/logs/analytics.log" || fail "analytics log line"
grep -qF -- "$CODE" "$WORK/server.log" && fail "the log holds the passcode"
grep -qF -- "${CK#*=}" "$WORK/server.log" && fail "the log holds the unlock cookie"
echo "PASS"
