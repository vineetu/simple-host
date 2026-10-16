#!/usr/bin/env bash
# Supply an already-built standalone image. Uses only loopback high ports.
set -euo pipefail
HERE=$(cd "$(dirname "$0")/.." && pwd)
: "${TEST_IMAGE:?build the standalone package and set TEST_IMAGE}"
WORK=$(mktemp -d)
INSTALL="$WORK/install"
INSTALL_SOURCE=${TEST_PREVIOUS_PACKAGE:-$HERE}
FIXTURE_IMAGE=$TEST_IMAGE
UPGRADE_IMAGE=$TEST_IMAGE
PREVIOUS_FIXTURE=sh-hack-package-previous:local
cleanup() {
  result=$?
  if [ "$result" != 0 ] && [ -f "$INSTALL/.env" ]; then
    docker compose --env-file "$INSTALL/.env" -f "$INSTALL/compose.yaml" logs --tail 30 app >&2 || true
  fi
  [ ! -f "$INSTALL/.env" ] || docker compose --env-file "$INSTALL/.env" -f "$INSTALL/compose.yaml" down -v --remove-orphans >/dev/null 2>&1 || true
  docker network rm sh-hack-package-proxy >/dev/null 2>&1 || true
  docker image rm "$PREVIOUS_FIXTURE" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT
# TLS_MODE=internal exercises HTTPS and certificate-volume persistence with
# Caddy's own local CA, without any external ACME requests or building a
# throwaway image: the delivered image already understands TLS_MODE, so the
# setting alone is enough (the delivered default, TLS_MODE=public, keeps
# on-demand public TLS). A TEST_PREVIOUS_IMAGE from before TLS_MODE (0.8.9 and
# earlier) gets the same local CA by a one-line patch of its own Caddyfile, so
# the real upgrade path, CA included, is exercised end to end.
if [ -n "${TEST_PREVIOUS_IMAGE:-}" ]; then
  mkdir -p "$WORK/previous"
  docker run --rm --entrypoint cat "$TEST_PREVIOUS_IMAGE" /etc/caddy/Caddyfile > "$WORK/previous/Caddyfile"
  if grep -q '^[[:space:]]*on_demand$' "$WORK/previous/Caddyfile"; then
    sed -i '/^[[:space:]]*on_demand$/a\		issuer internal' "$WORK/previous/Caddyfile"
    printf 'FROM %s\nCOPY Caddyfile /etc/caddy/Caddyfile\n' "$TEST_PREVIOUS_IMAGE" > "$WORK/previous/Dockerfile"
    docker build -q -t "$PREVIOUS_FIXTURE" "$WORK/previous" >/dev/null
    FIXTURE_IMAGE=$PREVIOUS_FIXTURE
  else
    FIXTURE_IMAGE=$TEST_PREVIOUS_IMAGE
  fi
fi
cat > "$WORK/config" <<EOF
SITE_DOMAIN=hack-package.test
RESEND_API_KEY=local-fixture-never-send
MAIL_FROM=Simple Hack <fixture@example.test>
TLS_MODE=internal
HTTP_BIND=127.0.0.1:18474
HTTPS_BIND=127.0.0.1:18475
HEALTH_BIND=127.0.0.1:18476
COMPOSE_PROJECT_NAME=sh-hack-package-smoke
EOF
bash "$INSTALL_SOURCE/install.sh" --dir "$INSTALL" --config "$WORK/config" --image "$FIXTURE_IMAGE" --skip-pull
compose() { docker compose --env-file "$INSTALL/.env" -f "$INSTALL/compose.yaml" "$@"; }
compose exec -T app cat /data/caddy/caddy/pki/authorities/local/root.crt > "$INSTALL/root.crt"
python3 "$HERE/tests/smoke.py" "$INSTALL" create
cp "$INSTALL/.env" "$INSTALL/env-before"
ca_before=$(sha256sum "$INSTALL/root.crt" | cut -d' ' -f1)
compose restart app >/dev/null
for _ in $(seq 1 30); do curl -fsS http://127.0.0.1:18476/readyz >/dev/null && break; sleep 1; done
python3 "$HERE/tests/smoke.py" "$INSTALL" verify
INSTALL_DIR="$INSTALL" bash "$HERE/upgrade.sh" --image "$UPGRADE_IMAGE" --skip-pull
python3 - "$INSTALL" <<'PYVERIFY'
import json, pathlib, sys
root=pathlib.Path(sys.argv[1])
def env(file): return dict((k,json.loads(v)) for k,v in (s.split('=',1) for s in (root/file).read_text().splitlines()))
before,after=env('env-before'),env('.env')
kept={k:v for k,v in before.items() if k!='SIMPLE_HACK_IMAGE'}
assert all(after.get(k)==v for k,v in kept.items()), 'an existing setting changed'
added=set(after)-set(before)
assert added <= {'TLS_MODE','EMAIL_SIGNIN'}, added
assert after.get('TLS_MODE','public') in ('public','internal') and after.get('EMAIL_SIGNIN','on')=='on'
print('PASS: domain, settings and all credentials preserved across upgrade')
PYVERIFY
compose exec -T app cat /data/caddy/caddy/pki/authorities/local/root.crt > "$INSTALL/root-after.crt"
[ "$ca_before" = "$(sha256sum "$INSTALL/root-after.crt" | cut -d' ' -f1)" ]
python3 "$HERE/tests/smoke.py" "$INSTALL" verify
# startup already verified the full schema; assert migrations were recorded.
count=$(compose exec -T db psql -U simplehack -d simplehack -tAc 'SELECT count(*) FROM schema_migrations')
[ "$count" -gt 0 ]
echo "PASS: fresh install, $count recorded migrations, restart, upgrade, retained secrets/database/site files/TLS CA"
if [ "${TEST_HACK_PRESENTATION:-0}" = 1 ]; then
  python3 "$HERE/tests/smoke.py" "$INSTALL" presentation
fi
if [ -n "${TEST_BROWSER_SCRIPT:-}" ]; then
  TEST_INSTALL_DIR="$INSTALL" node "$TEST_BROWSER_SCRIPT"
fi
if [ "${TEST_COOLIFY_PROXY:-0}" = 1 ]; then
  compose stop app
  python3 "$HERE/tests/proxy_fixture.py" "$HERE/coolify/compose.yaml" "$INSTALL/proxy.yaml"
  docker network create sh-hack-package-proxy >/dev/null
  docker compose --env-file "$INSTALL/.env" -f "$INSTALL/proxy.yaml" up -d --wait --wait-timeout 180
  # Docker provider updates asynchronously after the proxy starts.
  sleep 3
  python3 "$HERE/tests/smoke.py" "$INSTALL" verify
  proxy_id=$(docker compose --env-file "$INSTALL/.env" -f "$INSTALL/proxy.yaml" ps -q proxy)
  proxy_ip=$(docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$proxy_id")
  compose exec -T app tail -n 5 /data/logs/access.log | python3 -c 'import json,sys; rows=[json.loads(s) for s in sys.stdin]; assert rows; assert all(r["request"]["remote_ip"] != sys.argv[1] for r in rows); assert all("X-Api-Key" not in r["request"]["headers"] for r in rows); print("PASS: proxy preserves visitor address and does not log API keys")' "$proxy_ip"
  echo 'PASS: Coolify Raw Compose routing through real Traefik with TLS passthrough and PROXY protocol'
fi
