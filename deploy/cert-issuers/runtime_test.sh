#!/usr/bin/env bash
# Pure stubs: no production CA traffic. Classification, bounded contention,
# retry state and the renewal entry point use the same shared runtime.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
export CERTBOT_LOCK="$T/shared.lock" CERTBOT_LOCK_WAIT=0.1 CERT_ALERT_ENV="$T/absent"
# shellcheck source=deploy/cert-issuers/runtime.sh
. "$here/runtime.sh"
classify() {
  printf '%s\n' "$1" > "$T/error"
  [ "$(cert_classify "$T/error" "${3:-1}")" = "$2" ] || { echo "bad classification: $1"; exit 1; }
}
classify 'Another instance of Certbot is already running.' 'transient lock-busy'
classify 'Could not acquire lock' 'transient lock-busy'
classify 'cert-runtime: lock-wait-timeout' 'transient lock-wait-timeout' 75
classify '' 'transient network-or-dns-timeout' 124
classify 'Detail: DNS problem: query timed out looking up TXT' 'transient network-or-dns-timeout'
classify 'requests.exceptions.ConnectionError: Max retries exceeded' 'transient network-or-dns-timeout'
classify 'DNS SERVFAIL waiting for propagation' 'transient network-or-dns-timeout'
# The paced runner returns 75 for every transient cause, not only lock waits.
classify 'ConnectionError: DNS query timed out' 'transient network-or-dns-timeout' 75
classify '' 'transient local-or-unknown-error' 75
classify 'HTTP 503 service unavailable' 'transient network-or-dns-timeout'
classify 'Detail: Invalid response from http://fixture.test/' 'ca invalid-challenge'
classify 'Some challenges have failed' 'ca invalid-challenge'
classify 'urn:ietf:params:acme:error:rateLimited' 'ca rate-limit'
classify 'permission denied' 'transient local-or-unknown-error'
mkdir -p "$T/failed"
for expected in 60 120 240 300 300; do
  before=$(date +%s)
  cert_record_failure "$T/failed/alice" transient lock-busy
  deadline=$(sed -n 's/^retry_at=//p' "$T/failed/alice")
  [ "$((deadline - before))" -ge "$expected" ] && [ "$((deadline - before))" -le "$((expected + 1))" ]
  [ "$(( $(stat -c %Y "$T/failed/alice") + 21600 ))" = "$deadline" ]
  if cert_retry_due "$T/failed/alice"; then echo 'retried before deadline'; exit 1; fi
done
cert_record_failure "$T/failed/bob" ca invalid-challenge
[ "$(sed -n 's/^retry_at=//p' "$T/failed/bob")" -ge "$(( $(date +%s) + 21599 ))" ]
printf 'transient: lock-busy\nattempt=1\nretry_at=0\n' > "$T/failed/alice"
cert_retry_due "$T/failed/alice"
exec 6>"$CERTBOT_LOCK"
flock 6
rc=0
"$here/certbot-locked" touch "$T/called" > "$T/out" 2>&1 || rc=$?
[ "$rc" = 75 ] && [ ! -e "$T/called" ]
grep -q lock-wait-timeout "$T/out"
exec 6>&-
"$here/certbot-locked" touch "$T/called"
[ -f "$T/called" ]
# Reentrant Google/renewal wrapper inherits the held lock rather than deadlocking.
cert_run "$here/certbot-locked" touch "$T/nested"
[ -f "$T/nested" ]
# All tiers record safe reasons, including private fallback output.
# shellcheck source=deploy/cert-issuers/fallback.sh
. "$here/fallback.sh"
mkdir -p "$T/bin" "$T/live"
cat > "$T/bin/certbot" <<'STUB'
#!/usr/bin/env bash
if [[ " $* " = *' --config '* ]]; then
  echo "${FALLBACK_ERROR:-private-fixture-key}" >&2
else
  echo "$PRIMARY_ERROR" >&2
fi
exit 1
STUB
chmod +x "$T/bin/certbot"
export PATH="$T/bin:$PATH" CERT_FAILURE_FILE="$T/failed/carol" CERT_FALLBACK_CA=none
for message in 'Another instance of Certbot is already running.' 'Detail: DNS query timed out' 'Detail: unauthorized'; do
  export PRIMARY_ERROR="$message"
  rc=0; cert_issue fixture.test carol certonly --cert-name carol > "$T/out" 2>&1 || rc=$?
  if [[ "$message" = *unauthorized* ]]; then [ "$rc" = 1 ]; grep -qx 'ca: invalid-challenge' "$CERT_FAILURE_FILE"
  else [ "$rc" = 75 ]; grep -q '^transient:' "$CERT_FAILURE_FILE"; fi
done
export CERT_FALLBACK_CA=zerossl PRIMARY_ERROR=rateLimited ZEROSSL_EAB_FILE="$T/eab.json" LE_LIVE="$T/live" CERT_ALERT_STATE="$T/alerts"
printf '{"success":true,"eab_kid":"fixture-kid","eab_hmac_key":"fixture-key"}\n' > "$ZEROSSL_EAB_FILE"
for message in 'Detail: DNS query timed out private-fixture-key' 'Detail: unauthorized private-fixture-key' 'Another instance of Certbot is already running.'; do
  export FALLBACK_ERROR="$message"
  rc=0; cert_issue fixture.test carol certonly --cert-name carol > "$T/out" 2>&1 || rc=$?
  if [[ "$message" = *unauthorized* ]]; then [ "$rc" = 1 ]; grep -qx 'ca: invalid-challenge' "$CERT_FAILURE_FILE"
  else [ "$rc" = 75 ]; grep -q '^transient:' "$CERT_FAILURE_FILE"; fi
  if grep -q private-fixture-key "$T/out" "$CERT_FAILURE_FILE"; then exit 1; fi
done
# A Google transport failure mapped to 75 must still advance to ZeroSSL.
cat > "$T/bin/google" <<STUB
#!/usr/bin/env bash
exec "$here/certbot-locked" "$T/bin/certbot" "\$@"
STUB
chmod +x "$T/bin/google"
export GOOGLE_CERTBOT="$T/bin/google" GOOGLE_EAB_FILE="$T/google-eab.json"
printf '{"server":"https://dv.acme-v02.api.pki.goog/directory","eab_kid":"fixture-kid","eab_hmac_key":"fixture-key"}\n' > "$GOOGLE_EAB_FILE"
export CERT_FALLBACK_CA=google,zerossl FALLBACK_ERROR='ConnectionError: DNS query timed out'
rc=0; cert_issue fixture.test carol certonly --cert-name carol > "$T/out" 2>&1 || rc=$?
[ "$rc" = 75 ]
grep -q 'ZeroSSL issuance failed' "$T/out"
grep -qx 'transient: network-or-dns-timeout' "$CERT_FAILURE_FILE"
echo 'cert runtime: classification, capped retry, timeout, renewal and fallback markers: ok'
