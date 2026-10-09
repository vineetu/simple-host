#!/usr/bin/env bash
# Shared by the root site and custom-domain issuers. No app settings involved.
# shellcheck source=/dev/null
[ ! -r /etc/simple-host-cert-issuers.conf ] || . /etc/simple-host-cert-issuers.conf
# shellcheck source=deploy/cert-issuers/runtime.sh
. "$(dirname "${BASH_SOURCE[0]}")/runtime.sh"
: "${CERT_FALLBACK_CA:=google,zerossl}"
: "${GOOGLE_EAB_FILE:=/etc/simple-host-secrets/google-eab.json}"
: "${GOOGLE_CERTBOT:=/usr/local/lib/simple-host-cert-issuers/google-certbot}"
: "${GOOGLE_ACME_STATE:=/var/lib/simple-host-google-acme}"
export GOOGLE_ACME_STATE
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
# CERT_ISSUE_CA=google is an explicit direct-CA entry point for smoke tests.
# The primary remains LE; only its rate limits enter the ordered fallback.
cert_issue() (
  local domain=$1 name=$2 tmp rc ca label file runner reason kind failure_reason
  issue_failure() {
    read -r kind failure_reason < <(cert_classify "$1" "$2")
    if [ -n "${CERT_FAILURE_FILE:-}" ]; then
      cert_record_failure "$CERT_FAILURE_FILE" "$kind" "$failure_reason" "${RETRY_AFTER:-21600}"
    fi
    [ "$kind" != transient ] || exit 75
    exit 1
  }
  local -a tiers
  shift 2
  if ! [[ "$CERT_FALLBACK_CA" =~ ^(none|(google|zerossl)(,(google|zerossl))*)$ ]]; then
    echo 'cert-fallback: use an ordered google,zerossl list, or none' >&2
    if [ -n "${CERT_FAILURE_FILE:-}" ]; then cert_record_failure "$CERT_FAILURE_FILE" transient invalid-config; fi
    exit 75
  fi
  umask 077
  tmp=$(mktemp -d)
  trap 'rm -rf -- "$tmp"' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  if [ "${CERT_ISSUE_CA:-}" = google ]; then
    tiers=(google)
    reason='Direct Google test'
  else
    rc=0
    cert_run timeout 600 certbot "$@" > "$tmp/primary" 2>&1 || rc=$?
    cat "$tmp/primary" >&2
    [ "$rc" != 0 ] || exit 0
    [ "$CERT_FALLBACK_CA" != none ] || issue_failure "$tmp/primary" "$rc"
    grep -qiE 'too many certificates|rateLimited|urn:ietf:params:acme:error:rateLimited' "$tmp/primary" || issue_failure "$tmp/primary" "$rc"
    IFS=, read -r -a tiers <<< "$CERT_FALLBACK_CA"
    reason="Let's Encrypt limit hit"
  fi
  for ca in "${tiers[@]}"; do
    runner=certbot
    case $ca in
      google) label='Google Trust Services'; file=$GOOGLE_EAB_FILE; runner=$GOOGLE_CERTBOT ;;
      zerossl) label=ZeroSSL; file=$ZEROSSL_EAB_FILE ;;
    esac
    # Reuse an existing account. EAB is required only for first registration.
    # Config and registration diagnostics are private and removed on exit.
    if ! python3 - "$ca" "$file" "$tmp/$ca.ini" "${LE_LIVE:-/etc/letsencrypt/live}/../accounts" <<'PYCONFIG'
import json, re, sys
from pathlib import Path
ca, credentials, output, accounts = sys.argv[1:]
server = {'google': 'https://dv.acme-v02.api.pki.goog/directory',
          'zerossl': 'https://acme.zerossl.com/v2/DV90'}[ca]
host, path = server.split('://')[1].split('/', 1)
try:
    registered = list((Path(accounts) / host / path).glob('*/regr.json'))
    lines = ['server = ' + server, 'email = support@simple-host.app']
    if registered:
        if len(registered) != 1:
            raise ValueError()
        lines.append('account = ' + registered[0].parent.name)
    else:
        eab = json.load(open(credentials))
        kid, key = eab['eab_kid'], eab['eab_hmac_key']
        if not all(isinstance(v, str) and re.fullmatch(r'[A-Za-z0-9_=-]+', v) for v in (kid, key)):
            raise ValueError()
        if ca == 'zerossl' and not eab.get('success'):
            raise ValueError()
        if ca == 'google' and eab.get('server') != server:
            raise ValueError()
        lines += ['eab-kid = ' + kid, 'eab-hmac-key = ' + key]
    Path(output).write_text('\n'.join(lines) + '\n')
except (OSError, ValueError, KeyError, TypeError):
    print('cert-fallback: account or credentials unavailable', file=sys.stderr)
    sys.exit(1)
PYCONFIG
    then
      echo "cert-fallback: $label account or credentials unavailable for $name" >&2
      printf "account or credentials unavailable\n" > "$tmp/last-failure"
      rc=1
      continue
    fi
    chmod 0600 "$tmp/$ca.ini"
    # Bound a failed name's CA attempt so later names can still be processed.
    rc=0
    cert_run timeout 600 "$runner" "$@" --config "$tmp/$ca.ini" --logs-dir "$tmp/logs-$ca" > "$tmp/$ca-output" 2>&1 || rc=$?
    if [ "$rc" = 0 ]; then
      if ! python3 - "${LE_LIVE:-/etc/letsencrypt/live}/../renewal/$name.conf" <<'PYRENEW'
import sys
from pathlib import Path
p = Path(sys.argv[1])
try:
    text = p.read_text()
    p.write_text(''.join('logs_dir = /var/log/letsencrypt\n' if line.startswith('logs_dir = ') else line for line in text.splitlines(keepends=True)))
except OSError:
    print('cert-fallback: cannot restore renewal log directory', file=sys.stderr)
    sys.exit(1)
PYRENEW
      then
        cert_fallback_alert "$domain" "$reason for $domain; issued $name via $label, but renewal config needs attention." || true
        printf "renewal configuration write failed\n" > "$tmp/last-failure"
        issue_failure "$tmp/last-failure" 1
      fi
      echo "cert-fallback: issued $name via $label"
      cert_fallback_alert "$domain" "$reason for $domain; issued $name via $label." || true
      exit 0
    fi
    cp "$tmp/$ca-output" "$tmp/last-failure"
    # Lock contention is independent of CA; do not try another provider.
    read -r kind failure_reason < <(cert_classify "$tmp/last-failure" "$rc")
    if [ "$failure_reason" = lock-busy ] || [ "$failure_reason" = lock-wait-timeout ]; then
      issue_failure "$tmp/last-failure" "$rc"
    fi
    echo "cert-fallback: $label issuance failed for $name" >&2
  done
  cert_fallback_alert "$domain" "$reason for $domain; fallback chain $CERT_FALLBACK_CA failed for $name (account/credentials unavailable or issuance failed)." || true
  issue_failure "$tmp/last-failure" "$rc"
)
