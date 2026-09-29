#!/usr/bin/env bash
# Sandbox test for issue.sh (simple-host-family-certs): runs it against a temp
# tree with fake nginx (-T dump, -t) and systemctl, and REAL openssl
# certificates, and checks that it serves a family only while its binding
# points where the request says, never takes a family another nginx server
# answers, checks the wildcard lineage (present, covering, not expired),
# refuses per_host, renders prefix and reserved labels, releases what is gone
# without touching certificates, and changes nothing on a second run.
#
#   bash deploy/family-certs/issue_test.sh
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
T=$(mktemp -d)
trap '[ -n "${KEEP:-}" ] && cp -r "$T" "$KEEP"; rm -rf "$T"' EXIT

mkdir -p "$T/bin" "$T/state/requests" "$T/state/ready" "$T/state/failed" \
  "$T/sites/families" "$T/avail" "$T/enabled" "$T/confd" "$T/live" "$T/ca"
S=$T/sites/families
cp "$here/vhost.conf.template" "$T/template"
cat > "$T/conf" <<EOF
STATE=$T/state
SITES=$S
TEMPLATE=$T/template
AVAILABLE=$T/avail
ENABLED=$T/enabled
LE_LIVE=$T/live
LOCK=$T/lock
EOF
# nginx: -T dumps every enabled file plus conf.d the way nginx does; -t fails
# while $T/nginx-t-fails exists, or when an enabled file names a certificate
# that is not there (as real nginx would).
cat > "$T/bin/nginx" <<EOF
#!/usr/bin/env bash
echo "nginx \$*" >> "$T/calls"
if [ "\${1:-}" = -T ]; then
  [ -f "$T/nginx-T-fails" ] && exit 1
  for f in "$T/confd"/* "$T/enabled"/*; do
    [ -e "\$f" ] || continue
    echo "# configuration file \$f:"
    cat "\$f"; echo
  done
  exit 0
fi
if [ "\${1:-}" = -t ]; then
  [ -f "$T/nginx-t-fails" ] && exit 1
  for f in "$T/enabled"/*; do
    [ -e "\$f" ] || continue
    for c in \$(sed -n 's/^ *ssl_certificate  *\(.*\);/\1/p' "\$f"); do
      c=\${c/\/etc\/letsencrypt\/live/$T/live}
      [ -f "\$c" ] || exit 1
    done
  done
fi
exit 0
EOF
printf '#!/bin/sh\necho "systemctl $*" >> %s/calls\n' "$T" > "$T/bin/systemctl"
cat > "$T/bin/install" <<'EOF'
#!/usr/bin/env bash
a=(); while [ $# -gt 0 ]; do case $1 in -o|-g) shift 2 ;; *) a+=("$1"); shift ;; esac; done
exec /usr/bin/install "${a[@]}"
EOF
chmod +x "$T/bin/"*
touch "$T/calls"

# Real certificates. mkcert <lineage> <days> <names...>: a self-signed one
# valid for <days>; mkexpired <lineage> <names...>: one that expired in 2020.
cat > "$T/ca/ca.cnf" <<EOF
[ca]
default_ca = d
[d]
database = $T/ca/index.txt
new_certs_dir = $T/ca
serial = $T/ca/serial
default_md = sha256
policy = p
copy_extensions = copy
unique_subject = no
[p]
commonName = supplied
EOF
touch "$T/ca/index.txt"; echo 01 > "$T/ca/serial"
san() { local s="" n; for n in "$@"; do s="$s${s:+,}DNS:$n"; done; printf '%s' "$s"; }
mkcert() {
  local l=$1 days=$2; shift 2
  mkdir -p "$T/live/$l"
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days "$days" \
    -keyout "$T/live/$l/privkey.pem" -out "$T/live/$l/fullchain.pem" -subj "/CN=$1" \
    -addext "subjectAltName=$(san "$@")" >/dev/null 2>&1
}
mkexpired() {
  local l=$1; shift
  mkdir -p "$T/live/$l"
  openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -keyout "$T/live/$l/privkey.pem" \
    -out "$T/ca/$l.csr" -subj "/CN=$1" -addext "subjectAltName=$(san "$@")" >/dev/null 2>&1
  openssl ca -batch -config "$T/ca/ca.cnf" -selfsign -keyfile "$T/live/$l/privkey.pem" -in "$T/ca/$l.csr" \
    -out "$T/live/$l/fullchain.pem" -startdate 20200101000000Z -enddate 20200201000000Z -notext >/dev/null 2>&1
}
mkcert fam.test 90 '*.fam.test' fam.test
mkcert plain.test 90 '*.plain.test'
mkcert other 90 '*.other.test' other.test
mkcert soon.test 5 '*.soon.test'
mkexpired old.test '*.old.test'
mkcert rx.test 90 '*.rx.test'
mkcert wild.test 90 '*.wild.test'
mkcert tfail.test 90 '*.tfail.test'
mkcert pre.test 90 '*.pre.test'
mkcert gone.test 90 '*.gone.test'
mkcert drop.test 90 '*.drop.test'
find "$T/live" -type f -exec sha256sum {} + | sort > "$T/live.sum"

U1=11111111-1111-1111-1111-111111111111
U2=22222222-2222-2222-2222-222222222222
TOK=sh-0123456789abcdef0123456789abcdef
mkdir -p "$T/sites/by-id/$U1" "$T/sites/by-id/$U2"
# req <suffix> <target> <mode> <cert> <prefix> <reserved>
req() { printf '%s\n%s\n%s\n%s\n%s\n%s\n' "$TOK" "$2" "$3" "$4" "$5" "$6" > "$T/state/requests/$1"; }
bind() { ln -sfn "../by-id/$2" "$S/$1"; }

# Happy paths: with a prefix and a reserved label; with neither.
bind fam.test $U1;   req fam.test "../by-id/$U1" wildcard fam.test voucher- "www  api"
bind plain.test $U1; req plain.test "../by-id/$U1" wildcard plain.test "" ""
# Near expiry: served, with a warning.
bind soon.test $U1;  req soon.test "../by-id/$U1" wildcard soon.test "" ""
# Lineage problems.
bind nolin.test $U1; req nolin.test "../by-id/$U1" wildcard missing "" ""
bind nocov.test $U1; req nocov.test "../by-id/$U1" wildcard other "" ""
bind old.test $U1;   req old.test "../by-id/$U1" wildcard old.test "" ""
# per_host is not available yet.
bind ph.test $U1;    req ph.test "../by-id/$U1" per_host ph.test "" ""
# Bad settings.
bind bad.test $U1;   req bad.test "../by-id/$U1" wildcard "Bad/Name" "" ""
bind badp.test $U1;  req badp.test "../by-id/$U1" wildcard fam.test "-x" ""
# Served elsewhere: a regex vhost, and an exact wildcard.
printf 'server {\n    server_name ~^(?<client>[a-z0-9-]+)\\.rx\\.test$;\n    location / { return 404; }\n}\n' > "$T/enabled/sub-rx.test"
printf 'server { server_name *.wild.test; }\n' > "$T/enabled/wild"
bind rx.test $U1;    req rx.test "../by-id/$U1" wildcard rx.test "" ""
bind wild.test $U1;  req wild.test "../by-id/$U1" wildcard wild.test "" ""
# Link missing, and pointing at another account: nothing, and ours removed.
req nolink.test "../by-id/$U1" wildcard fam.test "" ""
echo "ours" > "$T/avail/simple-host-family-moved.test"
ln -s "$T/avail/simple-host-family-moved.test" "$T/enabled/simple-host-family-moved.test"
echo "prefix=" > "$T/state/ready/moved.test"
bind moved.test $U2; req moved.test "../by-id/$U1" wildcard fam.test "" ""
# Request removed: released, certificate kept.
bind gone.test $U1
echo "ours" > "$T/avail/simple-host-family-gone.test"
ln -s "$T/avail/simple-host-family-gone.test" "$T/enabled/simple-host-family-gone.test"
printf 'prefix=\ncert=gone.test\nexpires=1\nreserved=\n' > "$T/state/ready/gone.test"
# A failure note whose binding is gone.
echo "old failure" > "$T/state/failed/stale.test"
# Platform zones and invalid names: requests dropped.
drops="simple-host.app x.simple-host.app simple-hack.app a.simple-hack.app x.simple-host.site localhost Bad_Name.test"
for d in $drops; do bind "$d" $U1; req "$d" "../by-id/$U1" wildcard fam.test "" ""; done
# Half-written: kept for the app, nothing done.
bind half.test $U1; printf 'nope\n' > "$T/state/requests/half.test"

run() { PATH="$T/bin:$PATH" SIMPLE_HOST_FAMILY_CERTS_CONF="$T/conf" bash "$here/issue.sh" "$@" > "$T/out" 2>&1 || { cat "$T/out"; echo "FAIL: issue.sh $* exited non-zero"; exit 1; }; }
run

fail=0
check() { if eval "$2"; then echo "  ok   $1"; else echo "  FAIL $1"; fail=1; fi; }
A=$T/avail/simple-host-family-
E=$T/enabled/simple-host-family-
served() { [ -f "$A$1" ] && [ "$(readlink "$E$1")" = "$A$1" ]; }
none() { [ ! -e "$A$1" ] && [ ! -L "$E$1" ] && [ ! -e "$T/state/ready/$1" ]; }
exp_fam=$(date -u -d "$(openssl x509 -in "$T/live/fam.test/fullchain.pem" -noout -enddate | sed 's/notAfter=//')" +%s)

check "fam.test: served, ready written" "served fam.test && [ -f '$T/state/ready/fam.test' ] && [ ! -e '$T/state/failed/fam.test' ]"
check "fam.test: ready is prefix, cert, expires, reserved" "[ \"\$(cat '$T/state/ready/fam.test')\" = \"\$(printf 'prefix=voucher-\ncert=fam.test\nexpires=$exp_fam\nreserved=www api')\" ]"
check "fam.test: no placeholder left" "! grep -q '__' '${A}fam.test'"
check "fam.test: suffix regex escaped" "grep -qF '\\.fam\\.test\$\";' '${A}fam.test'"
check "fam.test: prefix in root and checks" "grep -qF 'root /srv/simple-host/sites/families/fam.test/voucher-\$sh_label/current;' '${A}fam.test' && grep -qF 'families/fam.test/voucher-\$sh_label/passcode) { rewrite ^ /internal/passcode\$uri last; }' '${A}fam.test'"
check "fam.test: reserved labels block kept, markers removed" "grep -qF 'if (\$sh_label ~ \"^(?:www|api)\$\") { return 404; }' '${A}fam.test' && ! grep -q __RESERVED '${A}fam.test'"
check "fam.test: certificate lineage named" "grep -qF 'ssl_certificate     /etc/letsencrypt/live/fam.test/fullchain.pem;' '${A}fam.test'"
check "plain.test: no prefix, no reserved block" "served plain.test && grep -qF 'families/plain.test/\$sh_label/current;' '${A}plain.test' && ! grep -q 'sh_label ~' '${A}plain.test' && ! grep -q 'never name a site' '${A}plain.test' && grep -qx 'prefix=' '$T/state/ready/plain.test' && grep -qx 'reserved=' '$T/state/ready/plain.test'"
check "soon.test: near expiry still served, warning logged" "served soon.test && [ -f '$T/state/ready/soon.test' ] && grep -q 'warning: soon.test' '$T/out'"
check "nolin.test: missing lineage fails" "none nolin.test && grep -q 'not on this server' '$T/state/failed/nolin.test'"
check "nocov.test: lineage not covering fails" "none nocov.test && grep -qF 'does not cover *.nocov.test' '$T/state/failed/nocov.test'"
check "old.test: expired lineage fails" "none old.test && grep -q 'expired' '$T/state/failed/old.test'"
check "ph.test: per_host refused" "none ph.test && grep -qx 'certificates per site name are not available yet' '$T/state/failed/ph.test'"
check "bad.test: bad cert name fails, not rendered" "none bad.test && grep -q 'certificate name' '$T/state/failed/bad.test'"
check "badp.test: bad prefix fails, not rendered" "none badp.test && grep -q 'prefix' '$T/state/failed/badp.test'"
check "rx.test: regex vhost: already served elsewhere" "none rx.test && grep -qx 'this name is already served here by another server' '$T/state/failed/rx.test'"
check "wild.test: *.wild.test vhost: already served elsewhere" "none wild.test && grep -q 'already served here' '$T/state/failed/wild.test'"
check "hand-made vhosts untouched" "[ -f '$T/enabled/sub-rx.test' ] && [ -f '$T/enabled/wild' ]"
check "nolink.test: no link, nothing rendered, request kept" "none nolink.test && [ -f '$T/state/requests/nolink.test' ]"
check "moved.test: link to another account, ours removed" "none moved.test && [ -f '$T/state/requests/moved.test' ]"
check "gone.test: request gone, file and ready released" "none gone.test"
check "certificates never touched" "find '$T/live' -type f -exec sha256sum {} + | sort | cmp -s - '$T/live.sum'"
check "stale failure note removed" "[ ! -e '$T/state/failed/stale.test' ]"
for d in $drops; do
  check "$d: dropped (platform zone or invalid)" "[ ! -e '$T/state/requests/$d' ] && none '$d'"
done
check "half.test: half-written request kept, nothing done" "[ -f '$T/state/requests/half.test' ] && none half.test && [ ! -e '$T/state/failed/half.test' ]"
check "nginx reloaded" "grep -q 'systemctl reload nginx' '$T/calls'"
check "no nginx configuration in the output" "! grep -q 'server_name' '$T/out'"
check "no leftovers" "[ -z \"\$(ls '$T/state/ready' '$T/avail' | grep -E '\.(new|pending)\$')\" ]"

# --render prints exactly the file the run wrote; --ready the marker.
run --render fam.test; cp "$T/out" "$T/render"
check "--render equals the written file" "cmp -s '$T/render' '${A}fam.test'"
run --ready fam.test; cp "$T/out" "$T/readyout"
check "--ready equals the written marker" "cmp -s '$T/readyout' '$T/state/ready/fam.test'"
for bad in ph.test nolink-nothing.test simple-host.app; do
  if PATH="$T/bin:$PATH" SIMPLE_HOST_FAMILY_CERTS_CONF="$T/conf" bash "$here/issue.sh" --render "$bad" >"$T/o" 2>"$T/e"; then
    check "--render $bad refused" false
  else
    check "--render $bad refused with a message, nothing on stdout" "[ ! -s '$T/o' ] && [ -s '$T/e' ]"
  fi
done

# Second run: nothing changes, no reload.
snap() { (cd "$T" && find state avail enabled -printf '%p %l\n' | sort; cat state/ready/* state/failed/* avail/* 2>/dev/null | sha256sum); }
snap > "$T/before"; : > "$T/calls"
run
snap > "$T/after"
check "second run: no change" "cmp -s '$T/before' '$T/after'"
check "second run: no reload" "! grep -q 'systemctl reload' '$T/calls'"

# Prefix change re-renders and rewrites ready.
req plain.test "../by-id/$U1" wildcard plain.test shop- ""
run
check "prefix change: re-rendered" "grep -qF 'families/plain.test/shop-\$sh_label/current;' '${A}plain.test' && grep -qx 'prefix=shop-' '$T/state/ready/plain.test' && grep -q 'systemctl reload nginx' '$T/calls'"

# nginx -t failure: the previous file comes back, ready removed, failed.
cp "${A}plain.test" "$T/plain.prev"
req plain.test "../by-id/$U1" wildcard plain.test other- ""
touch "$T/nginx-t-fails"
run
check "nginx -t fails: previous file restored" "cmp -s '$T/plain.prev' '${A}plain.test' && [ \"\$(readlink '${E}plain.test')\" = '${A}plain.test' ]"
check "nginx -t fails: ready removed, failure recorded" "[ ! -e '$T/state/ready/plain.test' ] && grep -qx 'the web server could not be set up for this family yet' '$T/state/failed/plain.test'"
# ...and a new family with nothing before: ours removed entirely.
bind tfail.test $U1; req tfail.test "../by-id/$U1" wildcard tfail.test "" ""
run
check "nginx -t fails on a new family: nothing of ours left" "none tfail.test && grep -q 'could not be set up' '$T/state/failed/tfail.test'"
rm -f "$T/nginx-t-fails"
run
check "after nginx -t recovers: served again, failure cleared" "served plain.test && grep -qx 'prefix=other-' '$T/state/ready/plain.test' && [ ! -e '$T/state/failed/plain.test' ] && served tfail.test"

# Release: request deleted and link removed.
rm -f "$T/state/requests/plain.test"
rm -f "$S/fam.test"
run
check "request deleted: file and ready released" "none plain.test"
check "link removed: file and ready released, request kept" "none fam.test && [ -f '$T/state/requests/fam.test' ]"
check "certificates still untouched" "find '$T/live' -type f -exec sha256sum {} + | sort | cmp -s - '$T/live.sum'"

# A hand-made server appearing later withdraws ours.
bind pre.test $U1; req pre.test "../by-id/$U1" wildcard pre.test "" ""
run
check "pre.test: served" "served pre.test"
printf 'server { server_name .pre.test; }\n' > "$T/enabled/pre-hand"
run
check "pre.test: withdrawn once a hand-made server names it" "none pre.test && grep -q 'already served here' '$T/state/failed/pre.test'"

# nginx -T unreadable: nothing written.
touch "$T/nginx-T-fails"
bind drop.test $U1; req drop.test "../by-id/$U1" wildcard drop.test "" ""
run
check "nginx -T failing: nothing written" "none drop.test"
rm -f "$T/nginx-T-fails"

[ "$fail" = 0 ] || { echo "--- issue.sh output"; cat "$T/out"; exit 1; }
echo "family-certs issue.sh sandbox: ok"
