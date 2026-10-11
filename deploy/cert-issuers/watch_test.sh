#!/usr/bin/env bash
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
mkdir -p "$T/bin" "$T/state/requests" "$T/state/ready"
export CERT_ALERT_STATE="$T/alerts" CERT_ALERT_ENV="$T/signal.env" SITE_CERTS_CONF="$T/site.conf" SITE_CERTS_CONF_GLOB="$T/simple-host-site-certs-*.conf"
printf 'STATE=%s/state\nSITE_DOMAIN=fixture.test\n' "$T" > "$SITE_CERTS_CONF"
# Unit states are "<unit> <state>" lines in $T/units; unlisted units are not
# found (systemctl prints nothing and fails). The default instance is enabled.
echo 'simple-host-site-certs.path enabled' > "$T/units"
cat > "$T/bin/systemctl" <<EOFSTUB
#!/usr/bin/env bash
[ "\$1" = is-enabled ] || exit 2
state=\$(awk -v u="\$2" '\$1 == u { s = \$2 } END { print s }' "$T/units")
[ -n "\$state" ] || exit 1
echo "\$state"
[ "\$state" = enabled ]
EOFSTUB
chmod +x "$T/bin/systemctl"
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
# A paused base: its request is overdue but both issuer units are disabled
# (or masked).
mkdir -p "$T/paused/requests" "$T/paused/ready"
printf 'STATE=%s/paused\nSITE_DOMAIN=paused.test\n' "$T" > "$T/simple-host-site-certs-paused.conf"
touch -d '16 minutes ago' "$T/paused/requests/waiting"
printf '%s\n' 'simple-host-site-certs-paused.path disabled' 'simple-host-site-certs-paused.timer masked' >> "$T/units"
bash "$here/watch.sh"
[ "$(wc -l < "$T/messages")" = 2 ]
# Re-enabling either unit brings the alert back.
echo 'simple-host-site-certs-paused.timer enabled' >> "$T/units"
bash "$here/watch.sh"
[ "$(wc -l < "$T/messages")" = 3 ]
grep -q '\*.waiting.paused.test' "$T/messages"
# Units systemctl cannot report on (not installed, query failed) are checked.
printf 'STATE=%s/paused\nSITE_DOMAIN=unknown.test\n' "$T" > "$T/simple-host-site-certs-unknown.conf"
bash "$here/watch.sh"
grep -q '\*.waiting.unknown.test' "$T/messages"
echo 'certificate watch: delayed only, independent scan, hourly throttle, ready clears alert, disabled issuers skipped: ok'
