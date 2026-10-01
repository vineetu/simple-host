#!/usr/bin/env bash
set -euo pipefail
HERE=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cat > "$WORK/config" <<'EOF'
SITE_DOMAIN=hack.example.test
RESEND_API_KEY=test-only-no-real-mail
MAIL_FROM="Équipe $hack <events@example.test>"
EOF
bash "$HERE/install.sh" --dir "$WORK/install" --config "$WORK/config" --prepare-only
cp "$WORK/install/.env" "$WORK/before"
INSTALL_DIR="$WORK/install" bash "$HERE/upgrade.sh" --image local-hack:upgrade --prepare-only
python3 - "$WORK" <<'PY'
import base64,json,pathlib,sys
root=pathlib.Path(sys.argv[1])
def read(path): return dict((k,json.loads(v)) for k,v in (s.split('=',1) for s in path.read_text().splitlines()))
before=read(root/'before'); after=read(root/'install/.env')
assert after.pop('SIMPLE_HACK_IMAGE')=='local-hack:upgrade'
before.pop('SIMPLE_HACK_IMAGE'); assert after==before
assert len(base64.b64decode(after['PASSCODE_ENC_KEY']))==32
assert after['MAIL_FROM']=='Équipe $$hack <events@example.test>'
assert (root/'install/.env').stat().st_mode&0o777==0o600
assert (root/'install/.env.previous').stat().st_mode&0o777==0o600
PY
# Compose's reusable config output doubles literal dollars; the container
# receives one dollar. Keep the non-ASCII sender unchanged as well.
# shellcheck disable=SC2016
docker compose --env-file "$WORK/install/.env" -f "$WORK/install/compose.yaml" config --format json | python3 -c 'import json,sys; assert json.load(sys.stdin)["services"]["app"]["environment"]["MAIL_FROM"] == "Équipe $$hack <events@example.test>"'
if bash "$HERE/install.sh" --dir "$WORK/bad" --domain bad/domain --config "$WORK/config" --prepare-only >"$WORK/refused" 2>&1; then
  echo 'Invalid domain accepted' >&2; exit 1
fi
if bash "$HERE/install.sh" --dir "$WORK/missing" --domain hack.test --prepare-only >"$WORK/refused" 2>&1; then
  echo 'Missing mail provider accepted' >&2; exit 1
fi
printf '\nDB_PASSWORD=%064d\n' 1 >> "$WORK/config"
if bash "$HERE/install.sh" --dir "$WORK/install" --config "$WORK/config" --prepare-only >"$WORK/refused" 2>&1; then
  echo 'Upgrade rotated the database password' >&2; exit 1
fi
echo 'PASS: fresh configuration, upgrade preservation, secret permissions and required inputs'
