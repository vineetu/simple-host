#!/usr/bin/env bash
# Run from the unpacked, versioned standalone package. Never prints secrets.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
INSTALL_DIR=${INSTALL_DIR:-/opt/simple-hack}
CONFIG=""; DOMAIN=""; IMAGE=""; PULL=1; PREPARE=0
TLS_MODE=""; ACME_CA=""; CERT_DIR=""; CA_CERT=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --dir) INSTALL_DIR=${2:?}; shift 2 ;;
    --config) CONFIG=${2:?}; shift 2 ;;
    --domain) DOMAIN=${2:?}; shift 2 ;;
    --image) IMAGE=${2:?}; shift 2 ;;
    --tls) TLS_MODE=${2:?}; shift 2 ;;
    --acme-ca) ACME_CA=${2:?}; shift 2 ;;
    --cert-dir) CERT_DIR=${2:?}; shift 2 ;;
    --ca-cert) CA_CERT=${2:?}; shift 2 ;;
    --skip-pull) PULL=0; shift ;;
    --prepare-only) PREPARE=1; shift ;;
    *) echo "Usage: $0 [--dir PATH] [--config ENV_FILE] [--domain DOMAIN] [--image IMAGE] [--tls public|internal|acme|certs] [--acme-ca URL] [--cert-dir DIR] [--ca-cert FILE] [--skip-pull] [--prepare-only]" >&2; exit 2 ;;
  esac
done
command -v python3 >/dev/null
command -v openssl >/dev/null
if [ -n "$CERT_DIR" ] && [ ! -d "$CERT_DIR" ]; then echo '--cert-dir must be an existing directory.' >&2; exit 2; fi
if [ -n "$CA_CERT" ] && [ ! -f "$CA_CERT" ]; then echo '--ca-cert must be an existing file.' >&2; exit 2; fi
mkdir -p "$INSTALL_DIR"
chmod 700 "$INSTALL_DIR"
exec 9>"$INSTALL_DIR/.install.lock"
flock -n 9 || { echo 'Another install or upgrade is running.' >&2; exit 1; }
export SH_INSTALL_DIR="$INSTALL_DIR" SH_CONFIG="$CONFIG" SH_DOMAIN="$DOMAIN" SH_IMAGE="$IMAGE" SH_TLS_MODE="$TLS_MODE" SH_ACME_CA="$ACME_CA"
python3 - <<'PY'
import base64, json, os, re, secrets
from pathlib import Path
root = Path(os.environ['SH_INSTALL_DIR'])
dest = root / '.env'
def read(path):
    values = {}
    if not path.exists(): return values
    for line in path.read_text().splitlines():
        if not line.strip() or line.lstrip().startswith('#'): continue
        key, sep, value = line.partition('=')
        if not sep or not re.fullmatch(r'[A-Z][A-Z0-9_]*', key):
            raise SystemExit('Invalid environment-file line; use NAME=value.')
        if value.startswith('"'):
            value = json.loads(value)
        elif value.startswith("'") and value.endswith("'"):
            value = value[1:-1]
        values[key] = value.replace('$$', '$')
    return values
values = read(dest)
initial = not dest.exists()
config = os.environ['SH_CONFIG']
if config:
    if not Path(config).is_file(): raise SystemExit('Configuration file does not exist.')
    supplied = read(Path(config))
    for key in ('DB_PASSWORD', 'ADMIN_API_KEY', 'PASSCODE_ENC_KEY', 'ANALYTICS_SALT'):
        if values.get(key) and supplied.get(key, values[key]) != values[key]:
            raise SystemExit('Upgrade configuration must retain existing '+key+'.')
    values.update(supplied)
if initial:
    for key in ('SITE_DOMAIN', 'RESEND_API_KEY', 'MAIL_FROM', 'OIDC_ISSUER', 'OIDC_CLIENT_ID', 'OIDC_CLIENT_SECRET',
                'ALLOWED_EMAIL_DOMAINS', 'OIDC_LABEL', 'OIDC_INSECURE_ALLOWED', 'EMAIL_SIGNIN',
                'MCP_APP_PHOTO_PICKER', 'MCP_APP_MY_SITES', 'MCP_APP_DASHBOARD', 'MCP_APP_SITE_PREVIEW'):
        if os.environ.get(key): values[key] = os.environ[key]
for key in ('MCP_APP_PHOTO_PICKER', 'MCP_APP_MY_SITES', 'MCP_APP_DASHBOARD', 'MCP_APP_SITE_PREVIEW'):
    values.setdefault(key, 'off')
if os.environ['SH_DOMAIN']: values['SITE_DOMAIN'] = os.environ['SH_DOMAIN']
if os.environ['SH_IMAGE']: values['SIMPLE_HACK_IMAGE'] = os.environ['SH_IMAGE']
values.setdefault('SIMPLE_HACK_IMAGE', 'ghcr.io/vineetu/simple-hack:0.8.10')
domain = values.get('SITE_DOMAIN', '').lower()
if len(domain)>253 or not re.fullmatch(r'[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+', domain):
    raise SystemExit('SITE_DOMAIN must be your own DNS hostname, without a URL or wildcard.')
values['SITE_DOMAIN'] = domain
# TLS_MODE/ACME_CA persist like every other setting: a bare re-run or an
# upgrade keeps them unless a flag overrides.
if os.environ.get('SH_TLS_MODE'): values['TLS_MODE'] = os.environ['SH_TLS_MODE']
values.setdefault('TLS_MODE', 'public')
if values['TLS_MODE'] not in ('public', 'internal', 'acme', 'certs'):
    raise SystemExit('--tls (TLS_MODE) must be public, internal, acme or certs.')
if os.environ.get('SH_ACME_CA'): values['ACME_CA'] = os.environ['SH_ACME_CA']
if values['TLS_MODE'] == 'acme' and not values.get('ACME_CA'):
    raise SystemExit('Set --acme-ca (or ACME_CA) when --tls acme.')
if not values.get('EMAIL_SIGNIN'): values['EMAIL_SIGNIN'] = 'on'
if values['EMAIL_SIGNIN'] not in ('on', 'off'):
    raise SystemExit('EMAIL_SIGNIN must be on or off.')
# Resend is only required when there is no other way to sign in.
company_signin = bool(values.get('OIDC_ISSUER') and values.get('OIDC_CLIENT_ID') and values.get('OIDC_CLIENT_SECRET'))
if not company_signin:
    for key in ('RESEND_API_KEY', 'MAIL_FROM'):
        if not values.get(key):
            raise SystemExit('Set '+key+' in the private --config file before installing, or configure OIDC_ISSUER, OIDC_CLIENT_ID and OIDC_CLIENT_SECRET for company sign-in.')
if values['EMAIL_SIGNIN'] == 'off' and not company_signin:
    raise SystemExit('EMAIL_SIGNIN=off needs company sign-in (OIDC_ISSUER, OIDC_CLIENT_ID and OIDC_CLIENT_SECRET).')
for key in ('DB_PASSWORD', 'ADMIN_API_KEY', 'ANALYTICS_SALT'):
    if not values.get(key): values[key] = secrets.token_hex(32)
    if not re.fullmatch(r'[0-9a-fA-F]{64}', values[key]):
        raise SystemExit(key+' must be 32 random bytes encoded as 64 hex characters.')
if not values.get('PASSCODE_ENC_KEY'):
    values['PASSCODE_ENC_KEY'] = base64.b64encode(secrets.token_bytes(32)).decode()
try:
    if len(base64.b64decode(values['PASSCODE_ENC_KEY'], validate=True)) != 32: raise ValueError()
except ValueError:
    raise SystemExit('PASSCODE_ENC_KEY must be 32 random bytes encoded as base64.')
for value in values.values():
    if any(ord(c)<32 for c in value): raise SystemExit('Configuration values must not contain control characters.')
if dest.exists():
    backup = root / '.env.previous'
    backup.write_bytes(dest.read_bytes()); backup.chmod(0o600)
temporary = root / '.env.new'
with temporary.open('w') as f:
    for key, value in values.items():
        f.write(key+'='+json.dumps(value.replace('$', '$$'), ensure_ascii=False)+'\n')
temporary.chmod(0o600); temporary.replace(dest)
PY
for file in compose.yaml install.sh upgrade.sh; do
  if [ "$HERE/$file" != "$INSTALL_DIR/$file" ]; then
    install -m 600 "$HERE/$file" "$INSTALL_DIR/$file"
  fi
done
chmod 700 "$INSTALL_DIR/install.sh" "$INSTALL_DIR/upgrade.sh"
# The bind mount in compose.yaml always needs this directory, even when
# running with the default public TLS and no company files to copy.
mkdir -p "$INSTALL_DIR/tls"
chmod 700 "$INSTALL_DIR/tls"
chown 65532:65532 "$INSTALL_DIR/tls" 2>/dev/null || true
if [ -n "$CA_CERT" ]; then
  install -m 600 "$CA_CERT" "$INSTALL_DIR/tls/ca.pem"
  chown 65532:65532 "$INSTALL_DIR/tls/ca.pem" 2>/dev/null || true
fi
if [ -n "$CERT_DIR" ]; then
  mkdir -p "$INSTALL_DIR/tls/certs"
  chmod 700 "$INSTALL_DIR/tls/certs"
  find "$INSTALL_DIR/tls/certs" -mindepth 1 -delete
  copied=0
  for pem in "$CERT_DIR"/*.pem; do
    [ -e "$pem" ] || continue
    install -m 600 "$pem" "$INSTALL_DIR/tls/certs/$(basename "$pem")"
    copied=$((copied+1))
  done
  [ "$copied" -gt 0 ] || { echo '--cert-dir has no .pem files.' >&2; exit 2; }
  chown -R 65532:65532 "$INSTALL_DIR/tls/certs" 2>/dev/null || true
fi
tls_mode_now=$(python3 -c "
import json
for line in open('$INSTALL_DIR/.env'):
    line = line.rstrip('\n')
    if line.startswith('TLS_MODE='):
        print(json.loads(line[len('TLS_MODE='):]))
        break
")
if [ "$tls_mode_now" = certs ]; then
  have_certs=$(find "$INSTALL_DIR/tls/certs" -maxdepth 1 -name '*.pem' -print -quit 2>/dev/null)
  if [ -z "$have_certs" ]; then
    echo '--tls certs needs at least one certificate: pass --cert-dir DIR, where each .pem file holds a certificate chain and its private key.' >&2
    exit 1
  fi
fi
if [ "$PREPARE" = 1 ]; then echo "Configuration prepared in $INSTALL_DIR/.env; no services started."; exit 0; fi
docker compose version >/dev/null
cd "$INSTALL_DIR"
# Saved values win over unrelated shell variables, particularly on upgrade.
while IFS='=' read -r key _; do unset "$key"; done < .env
compose() { docker compose --env-file .env -f compose.yaml "$@"; }
compose config --quiet
if [ "$PULL" = 1 ]; then
  compose pull || { echo 'Image unavailable. Check SIMPLE_HACK_IMAGE and registry access, then retry.' >&2; exit 1; }
fi
compose up -d --wait --wait-timeout 120 db
compose stop app
if ! compose run --rm --no-deps -T release; then
  echo 'Migration failed. The app is stopped; the database and site volumes are retained. Fix the cause and rerun this installer.' >&2
  exit 1
fi
compose up -d --no-deps --wait --wait-timeout 180 app
echo "Simple Hack is ready. Open the configured domain over HTTPS to verify DNS and certificates."
echo "The admin key and persistent settings are in $INSTALL_DIR/.env (not printed)."
if [ "$tls_mode_now" = internal ]; then
  echo "After the first HTTPS request, copy Caddy's root certificate for IT to trust on company devices: docker compose --env-file $INSTALL_DIR/.env -f $INSTALL_DIR/compose.yaml exec app cat /data/caddy/caddy/pki/authorities/local/root.crt > root.crt"
fi
