#!/usr/bin/env bash
# Sandbox test for family-adopt.sh (no root, no system nginx): a hand-made
# wildcard vhost is swapped for the managed family file rendered by the real
# issuer (deploy/family-certs/issue.sh --render / --ready, with a real
# self-signed *.fam.test certificate). Checks: the dry run changes nothing;
# apply swaps (old link gone, ours present, ready written as the issuer
# writes it, backup recorded); a second apply is a no-op; a failed nginx -t
# restores the old state exactly; rollback restores the old link and removes
# ours; rolling back twice is a no-op; sites-content-host is refused; the
# order: the ready marker is written and the app reports the family live
# before the vhosts are swapped (and never swapped when it does not), and
# rollback reverses it.
#   bash deploy/prod/family-adopt_test.sh
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fail=0
ok() { echo "  ok — $1"; }
bad() { echo "  FAIL: $1"; fail=1; }

U=11111111-1111-1111-1111-111111111111
mkdir -p "$T/avail" "$T/enabled" "$T/state/requests" "$T/state/ready" "$T/state/failed" "$T/sites/families" "$T/live/fam.test" "$T/bak"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 90 \
  -keyout "$T/live/fam.test/privkey.pem" -out "$T/live/fam.test/fullchain.pem" -subj /CN=fam.test \
  -addext "subjectAltName=DNS:*.fam.test" >/dev/null 2>&1
printf 'sh-0123456789abcdef0123456789abcdef\n../by-id/%s\nwildcard\nfam.test\nvoucher-\nwww\n' "$U" > "$T/state/requests/fam.test"
ln -s "../by-id/$U" "$T/sites/families/fam.test"
cat > "$T/conf" <<EOF
STATE=$T/state
SITES=$T/sites/families
TEMPLATE=$root/deploy/family-certs/vhost.conf.template
AVAILABLE=$T/avail
ENABLED=$T/enabled
LE_LIVE=$T/live
LOCK=$T/issuer.lock
EOF
# The operator's hand-made vhost, enabled by a link; plus a neighbour.
printf 'server {\n    server_name ~^(?<client>[a-z0-9-]+)\\.fam\\.test$;\n    root /srv/simple-host/sites/handles/x/$client/current;\n}\n' > "$T/avail/sub-fam.test"
ln -s "$T/avail/sub-fam.test" "$T/enabled/sub-fam.test"
echo "server { server_name other.test; }" > "$T/avail/other"; ln -s "$T/avail/other" "$T/enabled/other"
echo "the issuer said: already served here" > "$T/state/failed/fam.test"
echo "secret-do-not-read" > "$T/enabled/sites-content-host"

# The app stand-in: it reports the family live once the ready marker is on
# disk (mode auto), or never (mode never); every question and every reload
# is logged with what was enabled at that moment.
echo auto > "$T/live-mode"
cat > "$T/live.sh" <<LIVE
#!/usr/bin/env bash
r=0; [ -e "$T/state/ready/\$1" ] && r=1
o=0; [ -L "$T/enabled/simple-host-family-\$1" ] && o=1
d=0; [ -L "$T/enabled/sub-\$1" ] && d=1
if [ "\$(cat "$T/live-mode")" = auto ] && [ \$r = 1 ]; then v=true; else v=false; fi
echo "live \$v ready=\$r ours=\$o old=\$d" >> "$T/events"
echo \$v
LIVE
cat > "$T/reload.sh" <<RELOAD
#!/usr/bin/env bash
r=0; [ -e "$T/state/ready/fam.test" ] && r=1
o=0; [ -L "$T/enabled/simple-host-family-fam.test" ] && o=1
d=0; [ -L "$T/enabled/sub-fam.test" ] && d=1
echo "reload ready=\$r ours=\$o old=\$d" >> "$T/events"
touch "$T/reloaded"
RELOAD

export SIMPLE_HOST_FAMILY_CERTS_CONF="$T/conf"
export AVAILABLE="$T/avail" ENABLED="$T/enabled" STATE="$T/state" SITES="$T/sites/families" \
  BACKUP_ROOT="$T/bak" LOCK="$T/adopt.lock" ISSUER="bash $root/deploy/family-certs/issue.sh" NGINX_RELOAD="bash $T/reload.sh" \
  FAMILY_LIVE="bash $T/live.sh" LIVE_TIMEOUT=2 LIVE_INTERVAL=1
adopt() { NGINX_TEST=${NT:-true} bash "$here/family-adopt.sh" "$@"; }
snap() { (cd "$T" && find avail enabled state bak -printf '%p %l\n' | sort; find avail enabled state -type f ! -name sites-content-host -exec cat {} + | sha256sum); }

echo "== refusals =="
for o in sites-content-host ../avail/other /etc/passwd; do
  if out=$(adopt fam.test --old "$o" 2>&1); then bad "--old $o accepted"; else ok "--old $o refused ($out)"; fi
done
ln -s "$T/enabled/sites-content-host" "$T/enabled/ch-alias"
if adopt fam.test --old ch-alias >/dev/null 2>&1; then bad "a link to the content host accepted"; else ok "a link to the content host refused"; fi
rm -f "$T/enabled/ch-alias"

echo "== dry run =="
snap > "$T/s0"
out=$(adopt fam.test --old sub-fam.test)
snap > "$T/s1"
cmp -s "$T/s0" "$T/s1" && ok "dry run changes nothing" || bad "dry run changed files"
grep -q '^+++ simple-host-family-fam.test' <<<"$out" && grep -q '^-    server_name ~^(?<client>' <<<"$out" && ok "dry run shows the diff" || bad "dry run output: $out"
grep -q "secret-do-not-read" <<<"$out" && bad "content host printed" || ok "content host never printed"

echo "== apply =="
bash "$root/deploy/family-certs/issue.sh" --render fam.test > "$T/want"
bash "$root/deploy/family-certs/issue.sh" --ready fam.test > "$T/want-ready"
adopt fam.test --old sub-fam.test --apply >/dev/null
[ ! -e "$T/enabled/sub-fam.test" ] && [ ! -L "$T/enabled/sub-fam.test" ] && ok "old link gone" || bad "old link still there"
[ -f "$T/avail/sub-fam.test" ] && ok "old file kept in sites-available" || bad "old file removed"
[ "$(readlink "$T/enabled/simple-host-family-fam.test")" = "$T/avail/simple-host-family-fam.test" ] && cmp -s "$T/want" "$T/avail/simple-host-family-fam.test" && ok "managed file enabled, identical to --render" || bad "managed file"
cmp -s "$T/want-ready" "$T/state/ready/fam.test" && grep -qx 'prefix=voucher-' "$T/state/ready/fam.test" && grep -qx 'reserved=www' "$T/state/ready/fam.test" && ok "ready written as the issuer writes it" || bad "ready: $(cat "$T/state/ready/fam.test" 2>/dev/null)"
[ ! -e "$T/state/failed/fam.test" ] && ok "failure note cleared" || bad "failure note left"
[ -e "$T/reloaded" ] && ok "nginx reloaded" || bad "no reload"
bk=$(cat "$T/bak/family-adopt-latest-fam.test")
[ -L "$bk/old-enabled" ] && [ "$(readlink "$bk/old-enabled")" = "$T/avail/sub-fam.test" ] && cmp -s "$bk/old.conf" "$T/avail/sub-fam.test" && grep -qx 'old=sub-fam.test' "$bk/meta" && ok "backup recorded" || bad "backup: $bk"

# The issuer's own run now finds nothing to change.
rm -f "$T/reloaded"
mkdir -p "$T/bin"
printf '#!/bin/sh\n[ "$1" = -T ] && for f in %s/enabled/*; do echo "# configuration file $f:"; cat "$f"; done\nexit 0\n' "$T" > "$T/bin/nginx"
printf '#!/bin/sh\necho "systemctl $*" >> %s/calls\n' "$T" > "$T/bin/systemctl"
cat > "$T/bin/install" <<'EOF'
#!/usr/bin/env bash
a=(); while [ $# -gt 0 ]; do case $1 in -o|-g) shift 2 ;; *) a+=("$1"); shift ;; esac; done
exec /usr/bin/install "${a[@]}"
EOF
chmod +x "$T/bin/"*
rm -f "$T/enabled/sites-content-host"   # the fake nginx -T would dump it
snap > "$T/s2"
PATH="$T/bin:$PATH" bash "$root/deploy/family-certs/issue.sh" > "$T/issuer.out" 2>&1
snap > "$T/s3"
cmp -s "$T/s2" "$T/s3" && [ ! -e "$T/calls" ] && ok "issuer run after adoption: no change, no reload" || { bad "issuer changed the adopted family"; cat "$T/issuer.out"; }

echo "== second apply =="
out=$(adopt fam.test --old sub-fam.test --apply)
snap > "$T/s4"
cmp -s "$T/s3" "$T/s4" && grep -q "already adopted" <<<"$out" && ok "second apply is a no-op" || bad "second apply: $out"

echo "== rollback =="
out=$(adopt fam.test --rollback)
snap > "$T/s5"
cmp -s "$T/s4" "$T/s5" && ok "rollback dry run changes nothing" || bad "rollback dry run changed files"
rm -f "$T/reloaded"
adopt fam.test --rollback --apply >/dev/null
[ "$(readlink "$T/enabled/sub-fam.test")" = "$T/avail/sub-fam.test" ] && ok "old link restored" || bad "old link not restored"
[ ! -e "$T/enabled/simple-host-family-fam.test" ] && [ ! -e "$T/avail/simple-host-family-fam.test" ] && [ ! -e "$T/state/ready/fam.test" ] && ok "ours and ready removed" || bad "ours left behind"
[ -e "$T/reloaded" ] && ok "nginx reloaded" || bad "no reload on rollback"
snap > "$T/s6"
out=$(adopt fam.test --rollback --apply)
snap > "$T/s7"
cmp -s "$T/s6" "$T/s7" && grep -q "already rolled back" <<<"$out" && ok "second rollback is a no-op" || bad "second rollback: $out"

echo "== nginx -t fails =="
echo "note" > "$T/state/failed/fam.test"
snap > "$T/s8"
ls "$T/bak" > "$T/bak.before"
if NT=false adopt fam.test --old sub-fam.test --apply >/dev/null 2>&1; then bad "apply with a failing nginx -t succeeded"; fi
# The only new things are the backup and its record.
(cd "$T" && find avail enabled state -printf '%p %l\n' | sort; find avail enabled state -type f -exec cat {} + | sha256sum) > "$T/s9a"
(cd "$T" && grep -v '^bak' "$T/s8") > "$T/s8a"
cmp -s "$T/s8a" "$T/s9a" && ok "failed nginx -t restores the previous state exactly" || { bad "state differs after failed nginx -t"; diff "$T/s8a" "$T/s9a" || true; }

echo "== rollback's nginx -t fails: ours stays =="
adopt fam.test --old sub-fam.test --apply >/dev/null
if NT=false adopt fam.test --rollback --apply >/dev/null 2>&1; then bad "rollback with a failing nginx -t succeeded"; fi
[ "$(readlink "$T/enabled/simple-host-family-fam.test")" = "$T/avail/simple-host-family-fam.test" ] && [ -f "$T/state/ready/fam.test" ] && [ ! -e "$T/enabled/sub-fam.test" ] && ok "ours put back, old stays disabled" || bad "rollback failure left a mixed state"

echo "== preconditions =="
rm -f "$T/sites/families/fam.test"
adopt fam.test --rollback --apply >/dev/null
if out=$(adopt fam.test --old sub-fam.test --apply 2>&1); then bad "adopted without the link"; else ok "no link: refused ($out)"; fi
ln -s "../by-id/$U" "$T/sites/families/fam.test"
mv "$T/state/requests/fam.test" "$T/req"
if out=$(adopt fam.test --old sub-fam.test --apply 2>&1); then bad "adopted without the request"; else ok "no request: refused ($out)"; fi
mv "$T/req" "$T/state/requests/fam.test"

echo "== order: marker, live, then swap =="
{ [ -L "$T/enabled/sub-fam.test" ] && [ ! -e "$T/enabled/simple-host-family-fam.test" ]; } || bad "not back at the start"
rm -f "$T/events"
adopt fam.test --old sub-fam.test --apply >/dev/null
first=$(head -n1 "$T/events")
[ "$first" = "live true ready=1 ours=0 old=1" ] && ok "the app is asked with the marker written and the old vhost still serving" || bad "first event: $first"
[ "$(grep -c '^reload' "$T/events")" = 1 ] && grep -q '^reload ready=1 ours=1 old=0$' "$T/events" \
  && [ "$(grep -n '^live true' "$T/events" | head -n1 | cut -d: -f1)" -lt "$(grep -n '^reload' "$T/events" | cut -d: -f1)" ] \
  && ok "live reported before the one reload, which swapped" || bad "events: $(cat "$T/events")"

echo "== order: rollback is the reverse =="
rm -f "$T/events"
adopt fam.test --rollback --apply >/dev/null
first=$(head -n1 "$T/events")
[ "$first" = "reload ready=1 ours=0 old=1" ] && ok "old vhost back and reloaded while the marker is still there" || bad "first event: $first"
tail -n1 "$T/events" | grep -q '^live false ready=0 ours=0 old=1$' && ok "then the marker removed and the app reports not live" || bad "events: $(cat "$T/events")"

echo "== the app never reports live: nothing swapped =="
echo never > "$T/live-mode"
echo "note" > "$T/state/failed/fam.test"
(cd "$T" && find avail enabled state -printf '%p %l\n' | sort; find avail enabled state -type f -exec cat {} + | sha256sum) > "$T/n0"
rm -f "$T/events" "$T/reloaded"
if out=$(adopt fam.test --old sub-fam.test --apply 2>&1); then bad "adopted although never live"; else ok "refused ($(tail -n1 <<<"$out"))"; fi
(cd "$T" && find avail enabled state -printf '%p %l\n' | sort; find avail enabled state -type f -exec cat {} + | sha256sum) > "$T/n1"
cmp -s "$T/n0" "$T/n1" && ok "state exactly as before (marker taken back, failure note kept)" || { bad "state changed"; diff "$T/n0" "$T/n1" || true; }
{ grep -q '^reload' "$T/events" || [ -e "$T/reloaded" ]; } && bad "reloaded although never live" || ok "no reload"
[ "$(grep -c '^live false ready=1 ours=0 old=1$' "$T/events")" -ge 2 ] && ok "asked until the timeout, old vhost serving throughout" || bad "events: $(cat "$T/events")"
echo auto > "$T/live-mode"

[ "$fail" = 0 ] && echo "family-adopt: ok" || { echo "family-adopt: FAILED"; exit 1; }
