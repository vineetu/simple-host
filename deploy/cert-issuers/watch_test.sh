#!/usr/bin/env bash
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
mkdir -p "$T/bin" "$T/state/requests" "$T/state/ready"
export CERT_ALERT_STATE="$T/alerts" CERT_ALERT_ENV="$T/signal.env" SITE_CERTS_CONF="$T/site.conf" SITE_CERTS_CONF_GLOB="$T/no-other-*.conf"
printf 'STATE=%s/state\nSITE_DOMAIN=fixture.test\n' "$T" > "$SITE_CERTS_CONF"
printf 'SIGNAL_ACCOUNT=fixture\nSIGNAL_TO=fixture\n' > "$CERT_ALERT_ENV"
cat > "$T/bin/curl" <<EOFSTUB
#!/usr/bin/env bash
cat >> "$T/messages"
echo >> "$T/messages"
printf '{"result":{}}\\n'
EOFSTUB
chmod +x "$T/bin/curl"
export PATH="$T/bin:$PATH"
touch "$T/state/requests/recent" "$T/state/requests/delayed" "$T/state/requests/ready" "$T/state/ready/ready"
touch -d '16 minutes ago' "$T/state/requests/delayed" "$T/state/requests/ready"
bash "$here/watch.sh"
bash "$here/watch.sh"
[ "$(wc -l < "$T/messages")" = 1 ]
grep -q '\*.delayed.fixture.test' "$T/messages"
if grep -qE '\*\.(recent|ready)\.fixture' "$T/messages"; then exit 1; fi
# A new hour sends again; marking ready stops later alerts.
printf '%s\n' "$(( $(date +%s) - 3601 ))" > "$T/alerts/delayed.fixture.test.sent"
bash "$here/watch.sh"
[ "$(wc -l < "$T/messages")" = 2 ]
touch "$T/state/ready/delayed"
printf '0\n' > "$T/alerts/delayed.fixture.test.sent"
bash "$here/watch.sh"
[ "$(wc -l < "$T/messages")" = 2 ]
echo 'certificate watch: delayed only, independent scan, hourly throttle, ready clears alert: ok'
