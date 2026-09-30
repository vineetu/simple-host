#!/usr/bin/env bash
# Sandbox test for the two site-certs instances (simple-host.app and
# simple-host.site) on a temp tree with fake certbot, dig and install:
#   - deploy-hook.sh picks the instance by the lineage's suffix: an .app
#     lineage lands in the .app NGINX_CERTS and STATE, a .site lineage in the
#     .site ones, a foreign lineage (or the platform wildcard) is a no-op, and
#     without any .site conf the .app behaviour is unchanged;
#   - issue.sh with no argument is the .app instance, with a conf argument the
#     .site one; each locks its own file, so one running never blocks the other.
#
#   bash deploy/site-certs/site-certs_test.sh
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fail=0
check() { if eval "$2"; then echo "  ok   $1"; else echo "  FAIL $1"; fail=1; fi; }

mkdir -p "$T/bin" "$T/etc" "$T/live" "$T/run" "$T/hooks"
# install without -o/-g (not root here); log the owner/group so REQUESTS_OWNER
# can be checked without root.
cat > "$T/bin/install" <<EOF
#!/usr/bin/env bash
owner=""; group=""; a=()
while [ \$# -gt 0 ]; do
  case \$1 in
    -o) owner=\$2; shift 2 ;;
    -g) group=\$2; shift 2 ;;
    *) a+=("\$1"); shift ;;
  esac
done
echo "install -o \$owner -g \$group \${a[*]}" >> "$T/calls"
exec /usr/bin/install "\${a[@]}"
EOF
# certbot: make the lineage and run the deploy hook, as certbot does.
cat > "$T/bin/certbot" <<EOF
#!/usr/bin/env bash
echo "certbot \$*" >> "$T/calls"
while [ \$# -gt 0 ]; do
  case \$1 in --cert-name) c=\$2 ;; --deploy-hook) hook=\$2 ;; esac
  shift
done
mkdir -p "$T/live/\$c" && touch "$T/live/\$c/fullchain.pem" "$T/live/\$c/privkey.pem"
RENEWED_LINEAGE="$T/live/\$c" "\$hook"
EOF
printf '#!/bin/sh\necho "dig $*" >> %s/calls\necho 203.0.113.7\n' "$T" > "$T/bin/dig"
printf '#!/bin/sh\necho "dns $*" >> %s/calls\n' "$T" > "$T/bin/dns"
chmod +x "$T/bin/"*
touch "$T/calls"

# The .app instance: no conf at all on the live box, so defaults; here the
# default conf only moves its paths into the sandbox.
cat > "$T/etc/simple-host-site-certs.conf" <<EOF
SITE_DOMAIN=simple-host.app
STATE=$T/var/simple-host-site-certs
NGINX_CERTS=$T/nginx/simple-host-site-certs
EOF
cat > "$T/site.conf" <<EOF
SITE_DOMAIN=simple-host.site
STATE=$T/var/simple-host-site-certs-site
NGINX_CERTS=$T/nginx/simple-host-site-certs-site
EOF
APP_STATE=$T/var/simple-host-site-certs
SITE_STATE=$T/var/simple-host-site-certs-site
APP_CERTS=$T/nginx/simple-host-site-certs
SITE_CERTS=$T/nginx/simple-host-site-certs-site

lineage() { mkdir -p "$T/live/$1" && touch "$T/live/$1/fullchain.pem" "$T/live/$1/privkey.pem"; }
hook() { PATH="$T/bin:$PATH" SITE_CERTS_CONF="$T/etc/simple-host-site-certs.conf" SITE_CERTS_CONF_GLOB="$T/etc/simple-host-site-certs-*.conf" RENEWED_LINEAGE="$T/live/$1" bash "$here/deploy-hook.sh"; }

echo "== deploy hook, .app only (no .site conf yet) =="
for l in alice.simple-host.app bob.simple-host.site simple-host.site other.example.com x.y.simple-host.app; do lineage "$l"; done
hook alice.simple-host.app
check ".app lineage: pair copied to the .app nginx dir" "[ -f '$APP_CERTS/alice/privkey.pem' ] && [ -f '$APP_CERTS/alice/fullchain.pem' ]"
check ".app lineage: ready marker in the .app state" "[ -f '$APP_STATE/ready/alice' ]"
rc=0; hook bob.simple-host.site || rc=$?
check ".site lineage without a .site conf: exit 0, nothing written" "[ $rc = 0 ] && [ ! -e '$SITE_STATE' ] && [ ! -e '$SITE_CERTS' ] && [ ! -e '$APP_STATE/ready/bob' ] && [ ! -e '$APP_CERTS/bob' ]"

echo "== deploy hook, both instances =="
cp "$T/site.conf" "$T/etc/simple-host-site-certs-site.conf"
hook bob.simple-host.site
check ".site lineage: pair copied to the .site nginx dir" "[ -f '$SITE_CERTS/bob/privkey.pem' ] && [ -f '$SITE_CERTS/bob/fullchain.pem' ]"
check ".site lineage: ready marker in the .site state, not the .app one" "[ -f '$SITE_STATE/ready/bob' ] && [ ! -e '$APP_STATE/ready/bob' ] && [ ! -e '$APP_CERTS/bob' ]"
rm -rf "$APP_STATE/ready/alice" "$APP_CERTS/alice"
hook alice.simple-host.app
check ".app lineage still lands in the .app dirs with a .site conf present" "[ -f '$APP_STATE/ready/alice' ] && [ -f '$APP_CERTS/alice/privkey.pem' ] && [ ! -e '$SITE_STATE/ready/alice' ] && [ ! -e '$SITE_CERTS/alice' ]"
# shellcheck disable=SC2034 # read inside check's eval
before=$(find "$T/var" "$T/nginx" | sort)
for l in other.example.com simple-host.site x.y.simple-host.app; do
  rc=0; hook "$l" || rc=$?
  check "$l: not one of ours, exit 0" "[ $rc = 0 ]"
done
check "foreign lineages wrote nothing" "[ \"\$(find '$T/var' '$T/nginx' | sort)\" = \"\$before\" ]"
# A .site conf missing a required line is skipped, never read as the default.
printf 'SITE_DOMAIN=simple-host.site\nSTATE=%s/var/half\n' "$T" > "$T/etc/simple-host-site-certs-site.conf"
lineage carol.simple-host.site
rc=0; hook carol.simple-host.site 2>/dev/null || rc=$?
check "incomplete .site conf: skipped, nothing written" "[ $rc = 0 ] && [ ! -e '$T/var/half' ] && [ ! -e '$APP_CERTS/carol' ] && [ ! -e '$APP_STATE/ready/carol' ]"
cp "$T/site.conf" "$T/etc/simple-host-site-certs-site.conf"

echo "== issue.sh, two instances =="
rm -rf "${T:?}/var" "${T:?}/nginx"
# Sandbox paths the live box leaves at their defaults.
cat >> "$T/etc/simple-host-site-certs.conf" <<EOF
LOCK_DIR=$T/run
LE_LIVE=$T/live
DNS_HELPER=$T/bin/dns
DEPLOY_HOOK=$here/deploy-hook.sh
HOOKS=$T/hooks
EOF
cat >> "$T/etc/simple-host-site-certs-site.conf" <<EOF
LOCK_DIR=$T/run
LE_LIVE=$T/live
DNS_HELPER=$T/bin/dns
DEPLOY_HOOK=$here/deploy-hook.sh
HOOKS=$T/hooks
EOF
mkdir -p "$APP_STATE/requests" "$SITE_STATE/requests"
touch "$APP_STATE/requests/dave" "$SITE_STATE/requests/erin" "$SITE_STATE/requests/www"
issue() { PATH="$T/bin:$PATH" SITE_CERTS_CONF="$T/etc/simple-host-site-certs.conf" SITE_CERTS_CONF_GLOB="$T/etc/simple-host-site-certs-*.conf" bash "$here/issue.sh" "$@"; }
# issue.sh reads its default conf from the fixed path; point the default
# instance at the sandbox conf by naming it (same as no argument on the box).
issue "$T/etc/simple-host-site-certs.conf" > "$T/out-app" 2>&1 || { cat "$T/out-app"; echo "FAIL: .app issue.sh exited non-zero"; exit 1; }
check ".app instance issues *.dave.simple-host.app" "grep -q -- '--cert-name dave.simple-host.app -d \*.dave.simple-host.app' '$T/calls' && [ -f '$APP_STATE/ready/dave' ] && [ -f '$APP_CERTS/dave/privkey.pem' ]"
check ".app instance never touches the .site queue" "[ -f '$SITE_STATE/requests/erin' ] && ! grep -q erin '$T/calls'"
check ".app instance locks its own file" "[ -e '$T/run/simple-host-site-certs.lock' ]"
issue "$T/etc/simple-host-site-certs-site.conf" > "$T/out-site" 2>&1 || { cat "$T/out-site"; echo "FAIL: .site issue.sh exited non-zero"; exit 1; }
check ".site instance issues *.erin.simple-host.site" "grep -q -- '--cert-name erin.simple-host.site -d \*.erin.simple-host.site' '$T/calls' && [ -f '$SITE_STATE/ready/erin' ] && [ -f '$SITE_CERTS/erin/privkey.pem' ]"
check ".site instance asks DNS in its own zone" "grep -q 'dns ensure erin simple-host.site 203.0.113.7' '$T/calls'"
check ".site instance: ready marker only in its own state" "[ ! -e '$APP_STATE/ready/erin' ]"
check ".site instance drops a reserved name" "[ ! -e '$SITE_STATE/requests/www' ] && ! grep -q 'www.simple-host.site' '$T/calls'"
check ".site instance writes its limits (DAILY from its conf)" "grep -qx 'DAILY=12' '$SITE_STATE/limits'"
check ".site instance locks its own file" "[ -e '$T/run/simple-host-site-certs-site.lock' ]"

# One instance running never blocks the other.
touch "$SITE_STATE/requests/frank" "$APP_STATE/requests/gina"
exec 8>"$T/run/simple-host-site-certs.lock"
flock -n 8
issue "$T/etc/simple-host-site-certs.conf" > "$T/out-app" 2>&1
check ".app instance with its lock held: waits" "grep -q 'another run is in progress' '$T/out-app' && [ -f '$APP_STATE/requests/gina' ]"
issue "$T/etc/simple-host-site-certs-site.conf" > "$T/out-site" 2>&1
check ".site instance runs while the .app lock is held" "[ -f '$SITE_STATE/ready/frank' ] && ! grep -q 'another run' '$T/out-site'"
exec 8>&-

echo "== issue.sh argument checks =="
rc=0; issue "$T/nope.conf" > "$T/out" 2>&1 || rc=$?
check "unreadable conf argument: refused" "[ $rc != 0 ] && grep -q 'cannot read' '$T/out'"
printf 'SITE_DOMAIN=simple-host.site\n' > "$T/half.conf"
rc=0; issue "$T/half.conf" > "$T/out" 2>&1 || rc=$?
check "conf argument without STATE: refused" "[ $rc != 0 ] && grep -q 'must set SITE_DOMAIN and STATE' '$T/out'"
check "no argument still reads /etc/simple-host-site-certs.conf and /run/simple-host-site-certs.lock" "grep -q '^DEFAULT_CONF=/etc/simple-host-site-certs.conf$' '$here/issue.sh' && grep -q '^LOCK_DIR=/run$' '$here/issue.sh' && grep -q '^STATE=/var/lib/simple-host-site-certs$' '$here/issue.sh'"
check "no argument: the .app defaults (domain, caps)" "grep -q '^SITE_DOMAIN=simple-host.app$' '$here/issue.sh' && grep -q '^DAILY=12 ' '$here/issue.sh' && grep -q '^BUDGET=40$' '$here/issue.sh' && grep -q '^PER_RUN=6$' '$here/issue.sh'"

echo "== units =="
check ".site service runs the issuer with the .site conf" "grep -qx 'ExecStart=/usr/local/sbin/simple-host-site-certs /etc/simple-host-site-certs-site.conf' '$here/simple-host-site-certs-site.service'"
check ".site path watches the .site requests" "grep -qx 'PathChanged=/var/lib/simple-host-site-certs-site/requests' '$here/simple-host-site-certs-site.path' && grep -qx 'Unit=simple-host-site-certs-site.service' '$here/simple-host-site-certs-site.path'"
check ".site timer has the .app timer's cadence" "[ \"\$(grep -E '^On' '$here/simple-host-site-certs-site.timer')\" = \"\$(grep -E '^On' '$here/simple-host-site-certs.timer')\" ]"
check "drop-in: .app line unchanged, .site line optional" "grep -qx 'ReadWritePaths=/var/lib/simple-host-site-certs/requests' '$here/simple-host.service.d-site-certs.conf' && grep -qx 'ReadWritePaths=-/var/lib/simple-host-site-certs-site/requests' '$here/simple-host.service.d-site-certs.conf'"
check "example conf sets every required line" "grep -qx 'SITE_DOMAIN=simple-host.site' '$here/simple-host-site-certs-site.conf.example' && grep -qx 'STATE=/var/lib/simple-host-site-certs-site' '$here/simple-host-site-certs-site.conf.example' && grep -qx 'NGINX_CERTS=/etc/nginx/simple-host-site-certs-site' '$here/simple-host-site-certs-site.conf.example'"
check "hack example conf sets domain, state, certs, owner and caps" "grep -qx 'SITE_DOMAIN=simple-hack.app' '$here/simple-host-site-certs-hack.conf.example' && grep -qx 'STATE=/var/lib/simple-host-site-certs-hack' '$here/simple-host-site-certs-hack.conf.example' && grep -qx 'NGINX_CERTS=/etc/nginx/simple-host-site-certs-hack' '$here/simple-host-site-certs-hack.conf.example' && grep -qx 'REQUESTS_OWNER=simplehack' '$here/simple-host-site-certs-hack.conf.example' && grep -qx 'BUDGET=30' '$here/simple-host-site-certs-hack.conf.example' && grep -qx 'DAILY=8' '$here/simple-host-site-certs-hack.conf.example' && grep -qx 'PER_RUN=4' '$here/simple-host-site-certs-hack.conf.example'"
check ".hack service runs the issuer with the .hack conf" "grep -qx 'ExecStart=/usr/local/sbin/simple-host-site-certs /etc/simple-host-site-certs-hack.conf' '$here/simple-host-site-certs-hack.service'"
check ".hack path watches the .hack requests" "grep -qx 'PathChanged=/var/lib/simple-host-site-certs-hack/requests' '$here/simple-host-site-certs-hack.path' && grep -qx 'Unit=simple-host-site-certs-hack.service' '$here/simple-host-site-certs-hack.path'"
check ".hack timer has the .app timer's cadence" "[ \"\$(grep -E '^On' '$here/simple-host-site-certs-hack.timer')\" = \"\$(grep -E '^On' '$here/simple-host-site-certs.timer')\" ]"

echo "== REQUESTS_OWNER =="
# Creating $STATE/requests uses install -o; our fake install logs that without
# needing root. The live box is root; this check is the same owner argument.
HACK_STATE=$T/var/simple-host-site-certs-hack
cat > "$T/etc/simple-host-site-certs-hack.conf" <<EOF
SITE_DOMAIN=simple-hack.app
STATE=$HACK_STATE
NGINX_CERTS=$T/nginx/simple-host-site-certs-hack
LOCK_DIR=$T/run
LE_LIVE=$T/live
DNS_HELPER=$T/bin/dns
DEPLOY_HOOK=$here/deploy-hook.sh
HOOKS=$T/hooks
EOF
: > "$T/calls"
issue "$T/etc/simple-host-site-certs-hack.conf" > "$T/out-hack" 2>&1 || { cat "$T/out-hack"; echo "FAIL: default REQUESTS_OWNER issue.sh exited non-zero"; exit 1; }
check "REQUESTS_OWNER default is simplehost" "grep -q 'install -o simplehost -g simplehost .*requests' '$T/calls' && [ -d '$HACK_STATE/requests' ]"
echo "REQUESTS_OWNER=simplehack" >> "$T/etc/simple-host-site-certs-hack.conf"
rm -rf "$HACK_STATE"
: > "$T/calls"
issue "$T/etc/simple-host-site-certs-hack.conf" > "$T/out-hack" 2>&1 || { cat "$T/out-hack"; echo "FAIL: REQUESTS_OWNER=simplehack issue.sh exited non-zero"; exit 1; }
check "REQUESTS_OWNER=simplehack is used for requests" "grep -q 'install -o simplehack -g simplehack .*requests' '$T/calls'"
printf 'SITE_DOMAIN=simple-hack.app\nSTATE=%s\nREQUESTS_OWNER=BadUser\n' "$HACK_STATE" > "$T/hack-bad.conf"
rc=0; issue "$T/hack-bad.conf" > "$T/out" 2>&1 || rc=$?
check "invalid REQUESTS_OWNER is refused" "[ $rc != 0 ] && grep -q 'bad REQUESTS_OWNER' '$T/out'"

[ "$fail" = 0 ] || exit 1
echo "site-certs sandbox: ok"
