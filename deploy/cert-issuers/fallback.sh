#!/usr/bin/env bash
# Shared by the root site and custom-domain issuers. No app settings involved.
# shellcheck source=/dev/null
[ ! -r /etc/simple-host-cert-issuers.conf ] || . /etc/simple-host-cert-issuers.conf
: "${CERT_FALLBACK_CA:=zerossl}"
: "${ZEROSSL_EAB_FILE:=/etc/simple-host-secrets/zerossl-eab.json}"
: "${CERT_ALERT_ENV:=/etc/sh-network-watch.env}"
: "${CERT_ALERT_STATE:=/var/lib/simple-host-cert-alerts}"

# cert_fallback_alert <domain> <outcome>: the same Hermes/Signal RPC as
# sh-network-watch, serialized across issuer instances; one note/domain/hour.
cert_fallback_alert() (
  local domain=$1 message=$2 now last response
  umask 077
  mkdir -p "$CERT_ALERT_STATE"
  exec 8>"$CERT_ALERT_STATE/$domain.lock"
  flock 8
  now=$(date +%s)
  last=$(cat "$CERT_ALERT_STATE/$domain.sent" 2>/dev/null || echo 0)
  [ $((now - last)) -ge 3600 ] || exit 0
  # shellcheck source=/dev/null
  [ ! -r "$CERT_ALERT_ENV" ] || . "$CERT_ALERT_ENV"
  if [ -z "${SIGNAL_ACCOUNT:-}" ] || [ -z "${SIGNAL_TO:-}" ]; then
    echo 'cert-fallback: Signal recipient is not configured' >&2; exit 0
  fi
  response=$(mktemp)
  trap 'rm -f -- "$response"' EXIT
  export SIGNAL_ACCOUNT SIGNAL_TO
  # Reserve the hour before RPC: a lost response must not cause duplicate notes.
  printf '%s\n' "$now" > "$CERT_ALERT_STATE/$domain.sent"
  if python3 - "$message" <<'PY' | curl -fsS -m 20 "${SIGNAL_RPC:-http://127.0.0.1:8086/api/v1/rpc}" -H 'Content-Type: application/json' --data-binary @- > "$response" 2>/dev/null
import json, os, sys
json.dump({'jsonrpc': '2.0', 'id': 1, 'method': 'send', 'params': {
    'account': os.environ['SIGNAL_ACCOUNT'], 'recipient': [os.environ['SIGNAL_TO']],
    'message': sys.argv[1]}}, sys.stdout)
PY
  then
    if python3 - "$response" <<'PY'
import json, sys
try:
    r = json.load(open(sys.argv[1]))
    sys.exit(0 if 'result' in r and 'error' not in r else 1)
except (ValueError, OSError):
    sys.exit(1)
PY
    then
      exit 0
    fi
  fi
  echo 'cert-fallback: Signal alert failed' >&2
)

# cert_issue <alert-domain> <lineage-name> <original certbot args...>
# Retry only a rate-limit failure, preserving every original issuance argument.
# Subshell traps remove the EAB config and private diagnostic logs on any exit.
cert_issue() (
  local domain=$1 name=$2 tmp rc
  shift 2
  case $CERT_FALLBACK_CA in zerossl|none) ;; *) echo 'cert-fallback: CERT_FALLBACK_CA must be zerossl or none' >&2; exit 1 ;; esac
  umask 077
  tmp=$(mktemp -d)
  trap 'rm -rf -- "$tmp"' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  rc=0
  certbot "$@" > "$tmp/primary" 2>&1 || rc=$?
  cat "$tmp/primary" >&2
  [ "$rc" != 0 ] || exit 0
  [ "$CERT_FALLBACK_CA" = zerossl ] || exit "$rc"
  grep -qiE 'too many certificates|rateLimited|urn:ietf:params:acme:error:rateLimited' "$tmp/primary" || exit "$rc"
  if ! python3 - "$ZEROSSL_EAB_FILE" "$tmp/zerossl.ini" <<'PY'
import json, re, sys
try:
    eab = json.load(open(sys.argv[1]))
    kid, key = eab['eab_kid'], eab['eab_hmac_key']
    if not eab.get('success') or not all(isinstance(v, str) and re.fullmatch(r'[A-Za-z0-9_=-]+', v) for v in (kid, key)):
        raise ValueError()
    with open(sys.argv[2], 'w') as f:
        f.write('server = https://acme.zerossl.com/v2/DV90\nemail = support@simple-host.app\n')
        f.write('eab-kid = ' + kid + '\neab-hmac-key = ' + key + '\n')
except (OSError, ValueError, KeyError, TypeError):
    print('cert-fallback: cannot read ZeroSSL EAB credentials', file=sys.stderr)
    sys.exit(1)
PY
  then
    cert_fallback_alert "$domain" "Let's Encrypt limit hit for $domain; ZeroSSL credentials unavailable for $name." || true
    exit 1
  fi
  chmod 0600 "$tmp/zerossl.ini"
  # Separate private logs: ACME account registration diagnostics may contain
  # the EAB binding. Neither diagnostics nor the credential config are retained.
  if certbot "$@" --config "$tmp/zerossl.ini" --logs-dir "$tmp/logs" > "$tmp/fallback" 2>&1; then
    # Certbot saves logs_dir when explicitly supplied. Restore the usual log
    # path in only this successful lineage; never retain the temporary path.
    if ! python3 - "${LE_LIVE:-/etc/letsencrypt/live}/../renewal/$name.conf" <<'PY'
import sys
from pathlib import Path
p = Path(sys.argv[1])
try:
    text = p.read_text()
    p.write_text(''.join('logs_dir = /var/log/letsencrypt\n' if line.startswith('logs_dir = ') else line for line in text.splitlines(keepends=True)))
except OSError:
    print('cert-fallback: cannot restore renewal log directory', file=sys.stderr)
    sys.exit(1)
PY
    then
      cert_fallback_alert "$domain" "Let's Encrypt limit hit for $domain; issued $name via ZeroSSL, but renewal config needs attention." || true
      exit 1
    fi
    echo "cert-fallback: issued $name via ZeroSSL"
    cert_fallback_alert "$domain" "Let's Encrypt limit hit for $domain; issued $name via ZeroSSL." || true
    exit 0
  fi
  echo "cert-fallback: ZeroSSL issuance failed for $name" >&2
  cert_fallback_alert "$domain" "Let's Encrypt limit hit for $domain; ZeroSSL issuance failed for $name." || true
  exit 1
)
