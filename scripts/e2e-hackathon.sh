#!/usr/bin/env bash
# End-to-end proof, on real infrastructure, that a hackathon still works.
#
# This exists because reading does not find these bugs. Three separate reviewers
# read this code and its documentation and none of them found that participants
# could not publish; an agent that actually ran the flow found it in twenty
# minutes. Everything below is a step that has broken at least once.
#
#   UPCLOUD_TOKEN=... SH_API_KEY=... bash scripts/e2e-hackathon.sh
#
# Creates a server, destroys it, and fails loudly. About a cent per run.
set -uo pipefail

: "${UPCLOUD_TOKEN:?set UPCLOUD_TOKEN}"
: "${SH_API_KEY:?set SH_API_KEY (an account key on the public instance)}"
API=${SH_PUBLIC_API:-https://simple-host.app}
EVENT=${EVENT_NAME:-e2e-$(date +%s)}
IMAGE=${IMAGE:-ghcr.io/vineetu/simple-host:latest}
KEYFILE=$(mktemp -u /tmp/e2e-key-XXXX)
SERVER_ID=""; CLAIMED=""; FAILED=0

step() { printf '\n== %s\n' "$*"; }
ok()   { printf '   ok   %s\n' "$*"; }
bad()  { printf '   FAIL %s\n' "$*"; FAILED=1; }

# Teardown runs on every exit path, including a failure halfway through.
# A server left running bills forever and a DNS record left behind points our
# domain at an address someone else will be given.
cleanup() {
  step "cleaning up"
  [ -n "$CLAIMED" ] && curl -sS -o /dev/null -X DELETE -H "X-API-Key: $SH_API_KEY" \
      "$API/v1/events/$EVENT?domain=$DOMAIN" && ok "released $EVENT.$DOMAIN"
  if [ -n "$SERVER_ID" ]; then
    curl -sS -o /dev/null -X POST -H "Authorization: Bearer $UPCLOUD_TOKEN" \
      -H 'Content-Type: application/json' -d '{"stop_server":{"stop_type":"hard"}}' \
      "https://api.upcloud.com/1.3/server/$SERVER_ID/stop" || true
    for _ in $(seq 1 30); do
      st=$(curl -sS -H "Authorization: Bearer $UPCLOUD_TOKEN" "https://api.upcloud.com/1.3/server/$SERVER_ID" \
           | python3 -c 'import json,sys;print(json.load(sys.stdin)["server"]["state"])' 2>/dev/null || echo "?")
      [ "$st" = "stopped" ] && break
      sleep 5
    done
    curl -sS -o /dev/null -X DELETE -H "Authorization: Bearer $UPCLOUD_TOKEN" \
      "https://api.upcloud.com/1.3/server/$SERVER_ID?storages=1&backups=delete" && ok "deleted the server and its disk"
  fi
  rm -f "$KEYFILE" "$KEYFILE.pub"
  [ "$FAILED" -eq 0 ] && printf '\ne2e ok\n' || printf '\nE2E FAILED\n'
  exit $FAILED
}
trap cleanup EXIT

step "which domains does the instance offer"
DOMAIN=$(curl -sS -H "X-API-Key: $SH_API_KEY" "$API/v1/events" \
  | python3 -c 'import json,sys; d=json.load(sys.stdin).get("domains") or [""]; print(d[0])')
# Asked rather than assumed: the skill named two domains this instance refused,
# and nothing surfaced that until someone tried it.
[ -n "$DOMAIN" ] || { bad "instance did not name its event domains"; exit 1; }
ok "$DOMAIN"

step "create the smallest server"
ssh-keygen -q -t ed25519 -N "" -f "$KEYFILE" </dev/null
TEMPLATE=$(curl -sS -H "Authorization: Bearer $UPCLOUD_TOKEN" https://api.upcloud.com/1.3/storage/template \
  | python3 -c '
import json,sys
# Two templates match "Ubuntu Server 24.04 LTS"; the CUDA one needs 20 GB and
# will not fit the 10 GB disk ordered below.
for s in json.load(sys.stdin)["storages"]["storage"]:
    if "24.04" in s["title"] and "CUDA" not in s["title"]: print(s["uuid"]); break')
[ -n "$TEMPLATE" ] || { bad "no Ubuntu 24.04 template"; exit 1; }
RESP=$(curl -sS -H "Authorization: Bearer $UPCLOUD_TOKEN" -H 'Content-Type: application/json' \
  -d "$(python3 -c "
import json,sys
print(json.dumps({'server':{'zone':'us-nyc1','title':'$EVENT','hostname':'e2e','plan':'STARTER-1xCPU-1GB','metadata':'yes',
 'login_user':{'username':'root','ssh_keys':{'ssh_key':[open('$KEYFILE.pub').read().strip()]}},
 'storage_devices':{'storage_device':[{'action':'clone','storage':'$TEMPLATE','title':'e2e-root','size':10,'tier':'standard'}]},
 'networking':{'interfaces':{'interface':[{'type':'public','ip_addresses':{'ip_address':[{'family':'IPv4'}]}}]}}}}))")" \
  https://api.upcloud.com/1.3/server)
SERVER_ID=$(python3 -c "import json,sys;print(json.loads(sys.argv[1])['server']['uuid'])" "$RESP" 2>/dev/null)
[ -n "$SERVER_ID" ] || { bad "create failed: $(head -c 200 <<<"$RESP")"; exit 1; }
ok "$SERVER_ID"

step "wait for it to start"
for _ in $(seq 1 60); do
  D=$(curl -sS -H "Authorization: Bearer $UPCLOUD_TOKEN" "https://api.upcloud.com/1.3/server/$SERVER_ID")
  ST=$(python3 -c 'import json,sys;print(json.load(sys.stdin)["server"]["state"])' <<<"$D")
  [ "$ST" = "started" ] && break
  sleep 10
done
[ "$ST" = "started" ] || { bad "server never started"; exit 1; }
IP=$(python3 -c '
import json,sys
# The utility 10.x address is listed too; DNS must point at the public one.
for a in json.load(sys.stdin)["server"]["ip_addresses"]["ip_address"]:
    if a["access"]=="public" and a["family"]=="IPv4": print(a["address"]); break' <<<"$D")
ok "$IP"

step "claim the two hostnames"
CLAIM=$(curl -sS -X POST -H "X-API-Key: $SH_API_KEY" -H 'Content-Type: application/json' \
  -d "{\"name\":\"$EVENT\",\"ip\":\"$IP\",\"domain\":\"$DOMAIN\"}" "$API/v1/events")
HOST=$(python3 -c 'import json,sys;print(json.load(sys.stdin).get("host",""))' <<<"$CLAIM")
[ -n "$HOST" ] || { bad "claim failed: $(head -c 200 <<<"$CLAIM")"; exit 1; }
CLAIMED=1; CONTENT="sites.$HOST"; ok "$HOST"

step "wait for ssh, then install"
for _ in $(seq 1 40); do
  ssh -o StrictHostKeyChecking=accept-new -o ConnectTimeout=8 -i "$KEYFILE" "root@$IP" true 2>/dev/null && break
  sleep 8
done
ssh -o StrictHostKeyChecking=accept-new -i "$KEYFILE" "root@$IP" \
  "curl -fsSL https://raw.githubusercontent.com/vineetu/simple-host/main/deploy/install/install.sh -o /root/i.sh && bash /root/i.sh --host '$HOST' --content '$CONTENT' --image '$IMAGE'" \
  > /tmp/e2e-install.log 2>&1 || { bad "install failed"; tail -5 /tmp/e2e-install.log; exit 1; }
ADMIN=$(grep -oE '"admin_api_key":"[^"]+"' /tmp/e2e-install.log | head -1 | cut -d'"' -f4)
[ -n "$ADMIN" ] || { bad "install printed no admin key"; exit 1; }
ok "installed, admin key issued"

step "the records are visible to the world"
# Checked against public resolvers, never this machine's. A resolver that looked
# up the name before the claim keeps serving the domain's wildcard for half an
# hour, and the wildcard holds a real certificate, so a working instance answers
# 404 behind a green padlock. That is a stale cache, not a broken server, and a
# test that cannot tell them apart is worse than no test.
for _ in $(seq 1 30); do
  A=$(dig +short "$HOST" A @1.1.1.1 2>/dev/null | head -1)
  B=$(dig +short "sites.$HOST" A @8.8.8.8 2>/dev/null | head -1)
  [ "$A" = "$IP" ] && [ "$B" = "$IP" ] && break
  sleep 8
done
[ "$A" = "$IP" ] && ok "both names resolve to $IP" || bad "DNS never propagated (saw '$A' and '$B')"

# Everything below pins the address, so the instance is tested rather than
# whatever this machine's resolver happens to believe.
PIN="--resolve $HOST:443:$IP --resolve sites.$HOST:443:$IP"

step "certificates arrive without anyone configuring a hostname"
for _ in $(seq 1 40); do
  C=$(curl -sS $PIN -o /dev/null -w '%{http_code}' --max-time 8 "https://$HOST/healthz" 2>/dev/null || echo 000)
  [ "$C" = "200" ] && break
  sleep 8
done
[ "$C" = "200" ] && ok "https://$HOST answers 200" || bad "no certificate after five minutes (a 404 here means DNS, not the install)"

step "the server sizes itself against its own disk"
CAP=$(curl -sS $PIN --max-time 20 -H "X-API-Key: $ADMIN" "https://$HOST/v1/admin/capacity?people=100")
cap_field() { python3 -c "import json,sys;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" <<<"$CAP" 2>/dev/null || echo 0; }
INFORCE=$(cap_field "['in_force_mb']")
SITEMB=$(cap_field "['recommended']['site_mb']")
MAXP=$(cap_field "['recommended']['max_people']")
# The numbers must come from this box, not from a default. An event server that
# reports the 500 MB ceiling, or a headcount of zero, has read nothing.
[ "${SITEMB:-0}" -ge 1 ] && [ "${SITEMB:-0}" -le 500 ] && ok "recommends ${SITEMB} MB per site" || bad "capacity returned no per-site cap"
[ "${MAXP:-0}" -ge 100 ] && ok "budgets ${MAXP} people" || bad "capacity says this server cannot hold 100 people"
# install.sh writes MAX_ARCHIVE_MB=auto, so the box must have sized itself at
# boot rather than kept the 100 MB default it ships with.
[ "${INFORCE:-0}" -ge 1 ] && [ "${INFORCE:-0}" -lt 100 ] && ok "sized itself to ${INFORCE} MB at boot" || bad "the box is still on the ${INFORCE} MB default; auto-sizing did not run"

step "the cap in force is the one actually enforced"
# A cap the API quotes but the uploader ignores is the failure this catches.
# The payload goes through a file: a megabyte of JSON on the command line is
# past Linux's per-argument limit and fails as "Argument list too long".
python3 -c 'import json,sys; mb=int(sys.argv[1]); json.dump({"files":{"index.html":"A"*(mb*1024*1024+65536)}}, sys.stdout)' "$INFORCE" > /tmp/e2e-toobig.json
CODE=$(curl -sS $PIN -o /dev/null -w '%{http_code}' --max-time 120 -X POST -H "X-API-Key: $ADMIN" \
  -H 'Content-Type: application/json' --data-binary @/tmp/e2e-toobig.json \
  "https://$HOST/v1/sites/toobig/files" 2>/dev/null || echo 000)
rm -f /tmp/e2e-toobig.json
[ "$CODE" = "413" ] && ok "a site over the ${INFORCE} MB cap is refused" || bad "a site over the ${INFORCE} MB cap returned $CODE, not 413"

step "the organiser issues a participant key"
PKEY=$(curl -sS $PIN -X POST -H "X-API-Key: $ADMIN" -H 'Content-Type: application/json' \
  -d '{"count":1,"prefix":"team"}' "https://$HOST/v1/admin/users" \
  | python3 -c 'import json,sys;d=json.load(sys.stdin);print(d["created"][0]["api_key"] if d.get("created") else "")')
[ -n "$PKEY" ] && ok "key issued" || bad "no participant key"

step "a participant publishes, the way llms.txt tells them to"
SITEURL=$(curl -sS $PIN -X POST -H "X-API-Key: $PKEY" -H 'Content-Type: application/json' \
  -d '{"files":{"index.html":"<h1>e2e</h1>"}}' "https://$HOST/v1/sites/entry/files" \
  | python3 -c 'import json,sys;print(json.load(sys.stdin).get("site_url",""))')
[ -n "$SITEURL" ] && ok "$SITEURL" || bad "publish failed"

step "the entry is actually reachable"
BODY=""
[ -n "$SITEURL" ] && BODY=$(curl -sS $PIN --max-time 20 "$SITEURL" 2>/dev/null)
grep -q "e2e" <<<"$BODY" && ok "served the right bytes" || bad "content host did not serve it"

step "redeploys do not accumulate copies of the site"
# An event box keeps only the live version (KEEP_VERSIONS=1 in install.sh).
# Nothing pruned history before, and a site on disk cost its size times its
# whole deploy history — on the smallest plan that history is what fills the
# disk, not the sites. Redeploy three times and check both halves: the history
# is gone, and the thing participants actually look at still works.
for n in 2 3 4; do
  curl -sS $PIN -o /dev/null -X PUT -H "X-API-Key: $PKEY" -H 'Content-Type: application/json' \
    -d "{\"files\":{\"index.html\":\"<h1>e2e v$n</h1>\"}}" --max-time 60 \
    "https://$HOST/v1/sites/entry/files"
done
KEPT=$(curl -sS $PIN --max-time 20 -H "X-API-Key: $PKEY" "https://$HOST/v1/sites/entry/versions" \
  | python3 -c 'import json,sys;d=json.load(sys.stdin);print(len(d.get("versions",[])))' 2>/dev/null || echo 0)
[ "${KEPT:-0}" = "1" ] && ok "four deploys, one version kept" || bad "four deploys left $KEPT versions; retention did not run"
BODY=$(curl -sS $PIN --max-time 20 "$SITEURL" 2>/dev/null)
grep -q "e2e v4" <<<"$BODY" && ok "the live page is the newest deploy" || bad "pruning history broke the live page"
# Export reads the live tree, not the history, so taking your work with you must
# survive retention. This is the promise the page makes to every participant.
EXP=$(curl -sS $PIN --max-time 60 -o /dev/null -w '%{http_code}' -H "X-API-Key: $PKEY" \
  "https://$HOST/v1/sites/entry/export.tar.gz" 2>/dev/null || echo 000)
[ "$EXP" = "200" ] && ok "a participant can still export" || bad "export returned $EXP after retention ran"

step "the instance describes itself, not the public one"
LLMS=$(curl -sS $PIN --max-time 20 "https://$HOST/llms.txt")
grep -q "$HOST" <<<"$LLMS" && ok "names this instance" || bad "llms.txt does not name this instance"
grep -q "/files" <<<"$LLMS" && ok "tells a participant how to publish" || bad "llms.txt never says how to publish"

step "analytics count a real visit"
[ -n "$SITEURL" ] && for _ in 1 2 3; do curl -sS $PIN -o /dev/null -A "Mozilla/5.0 Chrome/140" --max-time 15 "$SITEURL"; done
sleep 3
ssh -o StrictHostKeyChecking=accept-new -i "$KEYFILE" "root@$IP" "cd /opt/simple-host && docker compose restart app >/dev/null 2>&1" || true
sleep 14
V=$(curl -sS $PIN --max-time 20 -H "X-API-Key: $ADMIN" "https://$HOST/v1/analytics/sites?days=1&all=1" \
  | python3 -c 'import json,sys;d=json.load(sys.stdin);print(sum(s["person"]["views"] for s in d.get("sites",[])))' 2>/dev/null || echo 0)
[ "${V:-0}" -gt 0 ] && ok "$V views counted" || bad "analytics reported zero"
