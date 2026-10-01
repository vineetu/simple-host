#!/usr/bin/env bash
# Supply an already-built standalone image. Uses only loopback high ports.
set -euo pipefail
HERE=$(cd "$(dirname "$0")/.." && pwd)
: "${TEST_IMAGE:?build the standalone package and set TEST_IMAGE}"
WORK=$(mktemp -d)
INSTALL="$WORK/install"
FIXTURE_IMAGE=sh-hack-package-smoke:local
cleanup() {
  result=$?
  if [ "$result" != 0 ] && [ -f "$INSTALL/.env" ]; then
    docker compose --env-file "$INSTALL/.env" -f "$INSTALL/compose.yaml" logs --tail 30 app >&2 || true
  fi
  [ ! -f "$INSTALL/.env" ] || docker compose --env-file "$INSTALL/.env" -f "$INSTALL/compose.yaml" down -v --remove-orphans >/dev/null 2>&1 || true
  docker network rm sh-hack-package-proxy >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT
# Local CA exercises HTTPS and certificate-volume persistence without any
# external ACME requests. The delivered image retains on-demand public TLS.
sed '/^[[:space:]]*on_demand$/a\		issuer internal' "$HERE/Caddyfile" > "$WORK/Caddyfile"
printf 'FROM %s\nCOPY Caddyfile /etc/caddy/Caddyfile\n' "$TEST_IMAGE" > "$WORK/Dockerfile"
docker build -q -t "$FIXTURE_IMAGE" "$WORK" >/dev/null
cat > "$WORK/config" <<EOF
SITE_DOMAIN=hack-package.test
RESEND_API_KEY=local-fixture-never-send
MAIL_FROM=Simple Hack <fixture@example.test>
HTTP_BIND=127.0.0.1:18474
HTTPS_BIND=127.0.0.1:18475
HEALTH_BIND=127.0.0.1:18476
COMPOSE_PROJECT_NAME=sh-hack-package-smoke
EOF
bash "$HERE/install.sh" --dir "$INSTALL" --config "$WORK/config" --image "$FIXTURE_IMAGE" --skip-pull
compose() { docker compose --env-file "$INSTALL/.env" -f "$INSTALL/compose.yaml" "$@"; }
compose exec -T app cat /data/caddy/caddy/pki/authorities/local/root.crt > "$INSTALL/root.crt"
python3 "$HERE/tests/smoke.py" "$INSTALL" create
before=$(sha256sum "$INSTALL/.env" | cut -d' ' -f1)
compose restart app >/dev/null
for _ in $(seq 1 30); do curl -fsS http://127.0.0.1:18476/readyz >/dev/null && break; sleep 1; done
python3 "$HERE/tests/smoke.py" "$INSTALL" verify
INSTALL_DIR="$INSTALL" bash "$HERE/upgrade.sh" --image "$FIXTURE_IMAGE" --skip-pull
[ "$before" = "$(sha256sum "$INSTALL/.env" | cut -d' ' -f1)" ]
python3 "$HERE/tests/smoke.py" "$INSTALL" verify
# startup already verified the full schema; assert migrations were recorded.
count=$(compose exec -T db psql -U simplehack -d simplehack -tAc 'SELECT count(*) FROM schema_migrations')
[ "$count" -gt 0 ]
echo "PASS: fresh install, $count recorded migrations, restart, upgrade, retained secrets/database/site files/TLS CA"
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
