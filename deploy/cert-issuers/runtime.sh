#!/usr/bin/env bash
# Shared Certbot serialization and retry state. No credentials or diagnostics
# are copied into failure markers; reasons are fixed classification labels.
: "${CERTBOT_LOCK:=/run/simple-host-certbot.lock}"
: "${CERTBOT_LOCK_WAIT:=180}"
: "${CERT_TRANSIENT_RETRY:=60}"
: "${CERT_TRANSIENT_MAX:=300}"

# Lock only the invocation, including its hooks, not DNS preparation or alerts.
cert_run() (
  # The paced runner may be called inside an already locked issuance/renewal.
  # FD 7 and this flag are inherited together through timeout and its child.
  if [ "${SIMPLE_HOST_CERTBOT_LOCK_HELD:-}" = "$CERTBOT_LOCK" ]; then
    "$@"
    exit $?
  fi
  exec 7>"$CERTBOT_LOCK"
  if ! flock -w "$CERTBOT_LOCK_WAIT" 7; then
    echo 'cert-runtime: lock-wait-timeout' >&2
    exit 75
  fi
  export SIMPLE_HOST_CERTBOT_LOCK_HELD="$CERTBOT_LOCK"
  "$@"
)

# cert_classify <private output file> <exit status>: only explicit CA refusals
# get long backoff. Unknown/local faults are transient rather than stranding
# a new person for six hours. Transport errors take precedence over ACME detail.
cert_classify() {
  local output=$1 rc=$2
  if grep -qiE 'Another instance of Certbot|(could not|failed to|unable to).*lock|lock.*(busy|held|already)' "$output"; then
    echo 'transient lock-busy'
  elif grep -q 'lock-wait-timeout' "$output"; then
    echo 'transient lock-wait-timeout'
  elif [ "$rc" = 124 ] || grep -qiE 'timed? ?out|timeout|connection|network is unreachable|temporary failure|DNS.*(propagat|SERVFAIL)|propagat.*(fail|wait)|name.*resolution|Max retries exceeded|bad gateway|service unavailable|HTTP[^0-9]*50[234]' "$output"; then
    echo 'transient network-or-dns-timeout'
  elif grep -qiE 'rateLimited|too many certificates|too many requests' "$output"; then
    echo 'ca rate-limit'
  elif grep -qiE 'unauthorized|invalid (challenge|response)|challenge.*(invalid|failed)|urn:ietf:params:acme:error:(rejectedIdentifier|caa|malformed)|Detail:|Some challenges have failed' "$output"; then
    echo 'ca invalid-challenge'
  else
    echo 'transient local-or-unknown-error'
  fi
}

# First line is readable by existing app clients; subsequent lines retain the
# retry schedule. Backdate mtime so older app builds, which add RETRY_AFTER
# to mtime for their estimate, also see the short deadline.
cert_record_failure() {
  local marker=$1 kind=$2 reason=$3 long=${4:-21600} count=0 delay now
  now=$(date +%s)
  if [ "$kind" = transient ]; then
    count=$(sed -n 's/^attempt=//p' "$marker" 2>/dev/null || true)
    [[ "$count" =~ ^[0-9]+$ ]] || count=0
    [ "$count" -lt 10 ] || count=10
    count=$((count + 1))
    delay=$((CERT_TRANSIENT_RETRY * (1 << (count - 1))))
    [ "$delay" -le "$CERT_TRANSIENT_MAX" ] || delay=$CERT_TRANSIENT_MAX
  else
    delay=$long
  fi
  printf '%s: %s\nattempt=%s\nretry_at=%s\n' "$kind" "$reason" "$count" "$((now + delay))" > "$marker.new"
  chmod 0644 "$marker.new"
  touch -d "@$((now - long + delay))" "$marker.new"
  mv -f -- "$marker.new" "$marker"
}

cert_retry_due() {
  local marker=$1 long=${2:-21600} deadline
  [ -f "$marker" ] || return 0
  deadline=$(sed -n 's/^retry_at=//p' "$marker")
  if [[ "$deadline" =~ ^[0-9]+$ ]]; then
    [ "$(date +%s)" -ge "$deadline" ]
  else
    [ $(( $(date +%s) - $(stat -c %Y "$marker") )) -ge "$long" ]
  fi
}

cert_requeue_legacy() {
  local state=$1 unit=$2 helper
  helper="$(dirname "${BASH_SOURCE[0]}")/requeue.py"
  python3 "$helper" "$state" "$unit"
}
