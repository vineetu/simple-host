#!/usr/bin/env bash
# Sandbox test for issue.sh: runs it against a temp tree with fake certbot,
# nginx, dig, systemctl and install, and checks that it never takes over a
# server the operator wrote by hand, issues for the rest, and cleans up
# failure notes for disconnected domains.
#
#   bash deploy/domain-certs/issue_test.sh
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

mkdir -p "$T/bin" "$T/state/requests" "$T/sites/by-id/u/s" "$T/avail" "$T/enabled" "$T/live" "$T/webroot"
cp "$here/vhost.conf.template" "$T/template"
cat > "$T/conf" <<EOF
STATE=$T/state
SITES=$T/sites
WEBROOT=$T/webroot
TEMPLATE=$T/template
AVAILABLE=$T/avail
ENABLED=$T/enabled
LE_LIVE=$T/live
LOCK=$T/lock
IP=203.0.113.7
EOF
cat > "$T/bin/certbot" <<EOF
#!/usr/bin/env bash
echo "certbot \$*" >> "$T/calls"
if [ "\$1" = certonly ]; then
  while [ \$# -gt 0 ]; do [ "\$1" = -d ] && d=\$2; shift; done
  mkdir -p "$T/live/\$d" && touch "$T/live/\$d/fullchain.pem" "$T/live/\$d/privkey.pem"
fi
EOF
printf '#!/bin/sh\necho 203.0.113.7\n' > "$T/bin/dig"
printf '#!/bin/sh\nexit 0\n' > "$T/bin/nginx"
printf '#!/bin/sh\necho "systemctl $*" >> %s/calls\n' "$T" > "$T/bin/systemctl"
# install without -o/-g (not root here).
cat > "$T/bin/install" <<'EOF'
#!/usr/bin/env bash
a=(); while [ $# -gt 0 ]; do case $1 in -o|-g) shift 2 ;; *) a+=("$1"); shift ;; esac; done
exec /usr/bin/install "${a[@]}"
EOF
chmod +x "$T/bin/"*
touch "$T/calls"

# Hand-made servers on the live box are named after the site, not customdomain-*.
printf 'server {\n  listen 443 ssl;\n  server_name vineetsriram.com\n              www.vineetsriram.com;\n}\n' > "$T/enabled/vineetsriram.com"
printf 'server { server_name ielts.vineetsriram.com; }\n' > "$T/enabled/ielts.vineetsriram.com"
printf 'server { server_name *.wild.test; }\n# server_name commented.test;\n' > "$T/enabled/wild"
# A hand lineage exists for the hand-made domain (the old takeover path).
mkdir -p "$T/live/vineetsriram.com" && touch "$T/live/vineetsriram.com/fullchain.pem" "$T/live/vineetsriram.com/privkey.pem"
# A server of ours written before the hand-made one appeared.
echo "old" > "$T/avail/simple-host-domain-ielts.vineetsriram.com"
ln -s "$T/avail/simple-host-domain-ielts.vineetsriram.com" "$T/enabled/simple-host-domain-ielts.vineetsriram.com"

for d in vineetsriram.com ielts.vineetsriram.com a.wild.test fresh.test sub.vineetsriram.com commented.test kept.test; do
  ln -s by-id/u/s "$T/sites/$d"
done
for d in vineetsriram.com ielts.vineetsriram.com a.wild.test fresh.test sub.vineetsriram.com commented.test; do
  touch "$T/state/requests/$d"
done
mkdir -p "$T/state/failed"
echo "old failure" > "$T/state/failed/gone.test"
echo "dns" > "$T/state/failed/kept.test"
touch -d '@0' "$T/state/failed/kept.test"

PATH="$T/bin:$PATH" SIMPLE_HOST_DOMAIN_CERTS_CONF="$T/conf" bash "$here/issue.sh" > "$T/out" 2>&1 || { cat "$T/out"; echo "FAIL: issue.sh exited non-zero"; exit 1; }

fail=0
check() { if eval "$2"; then echo "  ok   $1"; else echo "  FAIL $1"; fail=1; fi; }
for d in vineetsriram.com ielts.vineetsriram.com a.wild.test; do
  check "$d: hand-made server left alone, marked ready" "[ -f '$T/state/ready/$d' ] && [ ! -e '$T/avail/simple-host-domain-$d' ] && [ ! -e '$T/enabled/simple-host-domain-$d' ] && ! grep -q -- '-d $d\$' '$T/calls'"
done
check "hand-made files untouched" "grep -q 'www.vineetsriram.com' '$T/enabled/vineetsriram.com' && [ -f '$T/enabled/ielts.vineetsriram.com' ]"
for d in fresh.test sub.vineetsriram.com commented.test; do
  check "$d: issued and served by ours" "[ -f '$T/state/ready/$d' ] && [ -L '$T/enabled/simple-host-domain-$d' ] && grep -q -- '-d $d\$' '$T/calls'"
done
check "template carries the take-down check" "grep -q 'domains/fresh.test/suspended' '$T/avail/simple-host-domain-fresh.test'"
check "failure note of a disconnected domain removed" "[ ! -e '$T/state/failed/gone.test' ]"
check "failure note of a connected domain kept" "[ -e '$T/state/failed/kept.test' ]"
[ "$fail" = 0 ] || { echo "--- issue.sh output"; cat "$T/out"; exit 1; }
echo "issue.sh sandbox: ok"
