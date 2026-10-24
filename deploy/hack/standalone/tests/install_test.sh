#!/usr/bin/env bash
set -euo pipefail
HERE=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cat > "$WORK/config" <<'EOF'
SITE_DOMAIN=hack.example.test
MCP_APP_PHOTO_PICKER=accounts:alice,bob
MCP_APP_MY_SITES=on
MCP_APP_DASHBOARD=accounts:alice
MCP_APP_SITE_PREVIEW=off
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
assert [after[n] for n in ('MCP_APP_PHOTO_PICKER','MCP_APP_MY_SITES','MCP_APP_DASHBOARD','MCP_APP_SITE_PREVIEW')] == ['accounts:alice,bob','on','accounts:alice','off']
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

# -- TLS: always-created directory, validation, and persistence --
[ -d "$WORK/install/tls" ] || { echo 'tls directory not created' >&2; exit 1; }
if bash "$HERE/install.sh" --dir "$WORK/badtls" --config "$WORK/config" --tls carrier-pigeon --prepare-only >"$WORK/refused" 2>&1; then
  echo 'Invalid TLS_MODE accepted' >&2; exit 1
fi
if bash "$HERE/install.sh" --dir "$WORK/badacme" --config "$WORK/config" --tls acme --prepare-only >"$WORK/refused" 2>&1; then
  echo 'acme TLS_MODE accepted without --acme-ca' >&2; exit 1
fi
if bash "$HERE/install.sh" --dir "$WORK/badcerts" --config "$WORK/config" --tls certs --prepare-only >"$WORK/refused" 2>&1; then
  echo 'certs TLS_MODE accepted without any certificate' >&2; exit 1
fi
mkdir -p "$WORK/certs"
echo 'fixture certificate and key, never a real one' > "$WORK/certs/wild.pem"
bash "$HERE/install.sh" --dir "$WORK/tls" --config "$WORK/config" --tls certs --cert-dir "$WORK/certs" --prepare-only
[ -f "$WORK/tls/tls/certs/wild.pem" ] || { echo 'cert file not copied into the install directory' >&2; exit 1; }
if [ "$(stat -c %a "$WORK/tls/tls")" != 700 ] || [ "$(stat -c %a "$WORK/tls/tls/certs/wild.pem")" != 600 ]; then
  echo 'tls directory or file has the wrong mode' >&2; exit 1
fi
# A bare re-run (no --tls, no --cert-dir) keeps the mode and the copied file.
bash "$HERE/install.sh" --dir "$WORK/tls" --config "$WORK/config" --prepare-only
grep -qx 'TLS_MODE="certs"' "$WORK/tls/.env"
[ -f "$WORK/tls/tls/certs/wild.pem" ] || { echo 'bare re-run lost the certificate file' >&2; exit 1; }
bash "$HERE/install.sh" --dir "$WORK/acme" --config "$WORK/config" --tls acme --acme-ca https://ca.example.test/acme/directory --prepare-only
grep -qx 'TLS_MODE="acme"' "$WORK/acme/.env"
grep -qx 'ACME_CA="https://ca.example.test/acme/directory"' "$WORK/acme/.env"
bash "$HERE/install.sh" --dir "$WORK/acme" --config "$WORK/config" --prepare-only
grep -qx 'ACME_CA="https://ca.example.test/acme/directory"' "$WORK/acme/.env"
echo 'PASS: TLS directory always created, bad TLS_MODE/acme/certs combinations refused, mode/certificate/ACME_CA persist across a bare re-run'

# -- Company sign-in: Resend required only when OIDC is not fully configured --
cat > "$WORK/oidc-config" <<'EOF'
SITE_DOMAIN=hack.example.test
OIDC_ISSUER=https://idp.example.test
OIDC_CLIENT_ID=hack-standalone
OIDC_CLIENT_SECRET=not-a-real-secret
EOF
bash "$HERE/install.sh" --dir "$WORK/oidc" --config "$WORK/oidc-config" --prepare-only
cat > "$WORK/partial-oidc-config" <<'EOF'
SITE_DOMAIN=hack.example.test
OIDC_ISSUER=https://idp.example.test
EOF
if bash "$HERE/install.sh" --dir "$WORK/partial-oidc" --config "$WORK/partial-oidc-config" --prepare-only >"$WORK/refused" 2>&1; then
  echo 'Partial OIDC configuration (missing client id/secret) accepted without Resend' >&2; exit 1
fi
echo 'PASS: company sign-in makes Resend optional only once OIDC_ISSUER, OIDC_CLIENT_ID and OIDC_CLIENT_SECRET are all set'
