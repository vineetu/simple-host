#!/usr/bin/env bash
# Release check for the Kubernetes chart: a throwaway kind cluster, a Postgres
# standing in for "their Postgres" (its own namespace, a role that owns one
# empty database and is not a superuser), the image built from this checkout
# and loaded into the cluster, `helm install`, and then the product used: the
# app loads, the schema and every migration land in the empty database, an
# organiser signs in (an admin-issued key; company sign-in needs a real
# identity provider), creates an event, a participant joins, makes a team and
# publishes its site at its own origin, and all of it is still there after
# the pod is replaced.
# The browser part is kind-check.mjs (Playwright, headless Chromium).
#
#   bash deploy/kubernetes/tests/kind-check.sh
#
#   KIND_CLUSTER=name   use (or create) this cluster; default simple-hack-check
#   KEEP=1              leave the cluster up afterwards (default: deleted)
#   IMAGE=repo:tag      skip the docker build and load this image
#   PORT=18090          the local port the Service is forwarded to
#   PLAYWRIGHT_CHROME   path to a Chromium binary for the browser checks;
#                       empty skips them
set -euo pipefail
cd "$(dirname "$0")/../../.."
cluster=${KIND_CLUSTER:-simple-hack-check}
port=${PORT:-18090}
host=hack.test
ns=simple-hack
image=${IMAGE:-}
pf_pid=""
fail=0
ok()  { printf '  ok   %s\n' "$1"; }
bad() { printf '  FAIL %s\n' "$1"; fail=1; }
cleanup() {
  [ -n "$pf_pid" ] && kill "$pf_pid" 2>/dev/null || true
  if [ "${KEEP:-0}" != 1 ]; then
    kind delete cluster --name "$cluster" >/dev/null 2>&1 || true
    echo "deleted kind cluster $cluster"
  fi
}
trap cleanup EXIT

echo "== kind cluster $cluster =="
if kind get clusters 2>/dev/null | grep -qx "$cluster"; then
  ok "exists"
else
  kind create cluster --name "$cluster" --wait 120s >/dev/null
  ok "created"
fi
kubectl config use-context "kind-$cluster" >/dev/null

echo "== image =="
if [ -z "$image" ]; then
  image=simple-hack-k8s:check
  docker build -q -t "$image" --build-arg VERSION=v0.0.0-kind-check --build-arg COMMIT="$(git rev-parse --short HEAD)" . >/dev/null
  ok "built $image"
fi
kind load docker-image "$image" --name "$cluster" >/dev/null 2>&1
ok "loaded into the cluster"

echo "== their Postgres (namespace theirdb) =="
kubectl create namespace theirdb --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n theirdb apply -f - >/dev/null <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata: {name: postgres}
spec:
  replicas: 1
  selector: {matchLabels: {app: postgres}}
  template:
    metadata: {labels: {app: postgres}}
    spec:
      containers:
        - name: postgres
          image: postgres:16-alpine
          env:
            - {name: POSTGRES_USER, value: postgres}
            - {name: POSTGRES_PASSWORD, value: their-superuser-password}
            - {name: POSTGRES_DB, value: postgres}
          ports: [{containerPort: 5432}]
          readinessProbe: {exec: {command: [pg_isready, -U, postgres]}, periodSeconds: 2}
---
apiVersion: v1
kind: Service
metadata: {name: postgres}
spec:
  selector: {app: postgres}
  ports: [{port: 5432, targetPort: 5432}]
EOF
kubectl -n theirdb rollout status deploy/postgres --timeout=180s >/dev/null
psql_their() { kubectl -n theirdb exec deploy/postgres -- psql -U postgres -tAq -c "$1"; }
# A fresh role and database every run: the owner, not a superuser.
db=hack_$(date +%s)
psql_their "CREATE ROLE $db LOGIN PASSWORD 'hack-password' NOSUPERUSER NOCREATEDB NOCREATEROLE;" >/dev/null
psql_their "CREATE DATABASE $db OWNER $db;" >/dev/null
ok "database $db owned by role $db (not a superuser)"
dsn="postgres://$db:hack-password@postgres.theirdb.svc.cluster.local:5432/$db?sslmode=disable"

echo "== helm install =="
helm upgrade --install simple-hack deploy/kubernetes/simple-hack \
  --namespace "$ns" --create-namespace \
  --set host="$host" \
  --set image.repository="${image%%:*}" --set image.tag="${image##*:}" --set image.pullPolicy=Never \
  --set database.dsn="$dsn" \
  --set signIn.email.resendApiKey=re_kind_check_no_mail_is_sent --set signIn.email.from="Simple Hack <hack@$host>" \
  --set persistence.size=1Gi \
  --set ingress.enabled=false \
  --wait --timeout 5m >/dev/null
ok "installed"
if kubectl -n "$ns" rollout status deploy/simple-hack --timeout=180s >/dev/null; then ok "pod ready"; else bad "pod not ready"; kubectl -n "$ns" logs deploy/simple-hack | tail -30; exit 1; fi

log=$(kubectl -n "$ns" logs deploy/simple-hack)
grep -q 'built the schema from db/schema.sql' <<<"$log" && ok "empty database: schema built on start" || bad "schema not built"
grep -q 'hosted events: on' <<<"$log" && ok "hack mode on" || bad "hack mode off"
grep -q 'trusted proxies:' <<<"$log" && ok "trusted proxies set" || bad "trusted proxies not set"
status=$(kubectl -n "$ns" exec deploy/simple-hack -- /usr/local/bin/simple-host migrate -status 2>&1 | tail -1)
[ "$status" = "0 pending" ] && ok "migrate -status: $status" || bad "migrate -status: $status"
tables=$(kubectl -n theirdb exec deploy/postgres -- psql -U postgres -d "$db" -tAq -c "SELECT count(*) FROM pg_tables WHERE schemaname='public'")
[ "${tables:-0}" -gt 50 ] && ok "$tables tables in $db" || bad "only ${tables:-0} tables in $db"
owner=$(kubectl -n theirdb exec deploy/postgres -- psql -U postgres -d "$db" -tAq -c "SELECT tableowner FROM pg_tables WHERE tablename='users'")
[ "$owner" = "$db" ] && ok "tables owned by $db" || bad "users table owned by $owner"
# Starting again on a built database is a no-op, and the start proves it.
kubectl -n "$ns" rollout restart deploy/simple-hack >/dev/null
kubectl -n "$ns" rollout status deploy/simple-hack --timeout=180s >/dev/null
log2=$(kubectl -n "$ns" logs deploy/simple-hack)
grep -q 'database is up to date; nothing to apply' <<<"$log2" && ok "second start: nothing to apply" || bad "second start migrated again"

echo "== the product through a port-forward on 127.0.0.1:$port =="
if ss -ltn 2>/dev/null | grep -q ":$port "; then echo "port $port is in use; set PORT" >&2; exit 1; fi
kubectl -n "$ns" port-forward svc/simple-hack "$port:80" >/dev/null 2>&1 &
pf_pid=$!
for _ in $(seq 1 30); do curl -fsS "http://127.0.0.1:$port/healthz" >/dev/null 2>&1 && break; sleep 1; done
base="http://127.0.0.1:$port"
admin=$(kubectl -n "$ns" get secret simple-hack-secrets -o jsonpath='{.data.ADMIN_API_KEY}' | base64 -d)
call() { # method path [json] [key] [host]
  local m=$1 p=$2 d=${3:-} k=${4:-$admin} h=${5:-$host}
  local args=(-sS -X "$m" -H "Host: $h" -H "X-Forwarded-Proto: https" -H "X-Forwarded-For: 203.0.113.9" "$base$p")
  [ -n "$k" ] && args+=(-H "X-API-Key: $k")
  [ -n "$d" ] && args+=(-H 'Content-Type: application/json' --data "$d")
  curl "${args[@]}"
}
code() { curl -sS -o /dev/null -w '%{http_code}' -H "Host: ${2:-$host}" -H "X-Forwarded-Proto: https" "$base$1"; }
[ "$(curl -fsS "$base/healthz")" = '{"status":"ok"}' ] && ok "/healthz" || bad "/healthz"
[ "$(curl -fsS "$base/readyz")" = '{"status":"ok"}' ] && ok "/readyz (Postgres ping)" || bad "/readyz"
[ "$(code /)" = 200 ] && ok "home page 200" || bad "home page $(code /)"
home=$(curl -sS -H "Host: $host" "$base/")
grep -qi 'hack' <<<"$home" && ok "home page is the hackathon platform" || bad "home page copy"

slug=kind-check
participant=$(call POST /v1/admin/users '{"emails":["participant@example.test"]}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["created"][0]["api_key"])')
[ -n "$participant" ] && ok "admin issued a participant key" || bad "no participant key"
now=$(date -u +%Y-%m-%dT%H:%M:%SZ); later=$(date -u -d '+1 day' +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -v+1d +%Y-%m-%dT%H:%M:%SZ)
call POST /v1/hack/events "{\"slug\":\"$slug\",\"title\":\"Kind check\",\"organiser_name\":\"Check\",\"organisation\":\"Kubernetes\",\"contact_email\":\"check@example.test\",\"purpose\":\"Prove the chart\",\"expected_participants\":3,\"starts_at\":\"$now\",\"ends_at\":\"$later\",\"time_zone\":\"UTC\"}" >/dev/null && ok "event created (admin as organiser)" || bad "event create"
call POST "/v1/hack/events/$slug/stage" '{"stage":"open"}' >/dev/null
join=$(call GET "/v1/hack/events/$slug" | python3 -c 'import sys,json; print(json.load(sys.stdin)["organiser"]["join_code"])')
call POST "/v1/hack/join/$join" '{"display_name":"Participant","accept_coc":true}' "$participant" >/dev/null && ok "participant joined with the join code" || bad "join"
team=$(call POST "/v1/hack/events/$slug/teams" '{"name":"Kind team"}' "$participant" | python3 -c 'import sys,json; print(json.load(sys.stdin)["slug"])')
teamkey=$(call POST "/v1/hack/events/$slug/key" '{}' "$participant" | python3 -c 'import sys,json; print(json.load(sys.stdin)["key"])')
url=$(call PUT "/v1/sites/$team/files?create=1" '{"files":{"index.html":"<!doctype html><title>Kind team</title><h1>Published from Kubernetes</h1>"}}' "$teamkey" | python3 -c 'import sys,json; print(json.load(sys.stdin).get("site_url",""))')
[ -n "$url" ] && ok "team site published at $url" || bad "publish"
[ "$url" = "https://$team.$slug.$host/" ] && ok "address is the team's own origin" || bad "address $url is not https://$team.$slug.$host/"
page=$(curl -sS -H "Host: $slug.$host" -H "X-Forwarded-Proto: https" "$base/")
grep -q 'Kind check' <<<"$page" && ok "event page at $slug.$host" || bad "event page"
site=$(curl -sS -H "Host: $team.$slug.$host" -H "X-Forwarded-Proto: https" "$base/")
grep -q 'Published from Kubernetes' <<<"$site" && ok "team site at $team.$slug.$host" || bad "team site"
[ "$(code "/$team/" "$slug.$host")" = 404 ] && ok "event host does not serve the team by path (own origin only)" || bad "team served by path"
# The admin-only routes hide from a participant (404, not 403).
[ "$(curl -sS -o /dev/null -w '%{http_code}' -H "Host: $host" -H "X-API-Key: $participant" "$base/v1/admin/users")" = 404 ] && ok "participant refused on /v1/admin" || bad "admin route open"

echo "== browser (Playwright) =="
chrome=${PLAYWRIGHT_CHROME:-}
if [ -n "$chrome" ] && [ -x "$chrome" ]; then
  if PLAYWRIGHT_CHROME="$chrome" PORT="$port" HOST="$host" SLUG="$slug" TEAM="$team" PARTICIPANT_KEY="$participant" node deploy/kubernetes/tests/kind-check.mjs; then ok "browser checks"; else bad "browser checks"; fi
else
  echo "  skip browser checks (PLAYWRIGHT_CHROME not set)"
fi

echo "== data survives a pod replacement =="
kubectl -n "$ns" delete pod -l app.kubernetes.io/name=simple-hack --wait=true >/dev/null
kubectl -n "$ns" rollout status deploy/simple-hack --timeout=180s >/dev/null
kill "$pf_pid" 2>/dev/null || true; wait "$pf_pid" 2>/dev/null || true
kubectl -n "$ns" port-forward svc/simple-hack "$port:80" >/dev/null 2>&1 &
pf_pid=$!
for _ in $(seq 1 30); do curl -fsS "$base/healthz" >/dev/null 2>&1 && break; sleep 1; done
call GET "/v1/hack/events/$slug" | grep -q '"Kind check"' && ok "event still there" || bad "event gone"
site=$(curl -sS -H "Host: $team.$slug.$host" -H "X-Forwarded-Proto: https" "$base/")
grep -q 'Published from Kubernetes' <<<"$site" && ok "team site still served from the volume" || bad "team site gone"
call GET /v1/sites '' "$teamkey" | grep -q "\"$team\"" && ok "team key still lists the site" || bad "site list"

echo
if [ "$fail" -eq 0 ]; then echo "kind check ok ✓"; else echo "KIND CHECK FAILED"; exit 1; fi
