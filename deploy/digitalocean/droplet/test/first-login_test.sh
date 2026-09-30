#!/usr/bin/env bash
# Runs first-login.sh, 001_onboot and the motd in a throwaway ubuntu:24.04
# container, with the installer replaced by a stub and DNS answered by a stub
# `dig` (or /etc/hosts, for the getent fallback). Nothing is installed on the
# host and the real installer never runs.
#
#   bash test/first-login_test.sh            (needs docker)
set -uo pipefail

if [ "${1:-}" != "--inside" ]; then
  here=$(cd "$(dirname "$0")/.." && pwd)
  exec docker run --rm -i -v "$here:/src:ro" "${TEST_IMAGE:-ubuntu:24.04}" bash /src/test/first-login_test.sh --inside
fi

# ---- inside the container -------------------------------------------------
export DEBIAN_FRONTEND=noninteractive
if ! { apt-get -qq update && apt-get -qqy install curl; } >/dev/null; then echo "could not install curl"; exit 1; fi

FL=/src/files/opt/simple-host-setup/first-login.sh
T=/tmp/t; mkdir -p $T/bin
pass=0; failn=0
ok()   { echo "PASS: $*"; pass=$((pass + 1)); }
bad()  { echo "FAIL: $*"; failn=$((failn + 1)); }
has()  { if grep -qF -- "$2" "$1"; then ok "$3"; else bad "$3 (missing: $2)"; echo "---- output"; cat "$1"; echo "----"; fi; }
hasnt(){ if grep -qF -- "$2" "$1"; then bad "$3 (unexpected: $2)"; else ok "$3"; fi; }

# The stub installer: records its arguments, prints what the real one prints.
cat > $T/fake-install.sh <<'EOF'
#!/bin/bash
echo "$@" > /tmp/t/installer-args
echo '==> writing configuration'
echo '{"host":"https://x","admin_api_key":"sh_admin_test","dir":"/opt/simple-host"}'
exit "${FAKE_EXIT:-0}"
EOF
FAKE_SHA=$(sha256sum $T/fake-install.sh | cut -d' ' -f1)
cat > $T/pins.env <<EOF
INSTALLER_RELEASE=v0.0.0-test
INSTALLER_COMMIT=0000000000000000000000000000000000000000
INSTALLER_SHA256=$FAKE_SHA
EOF

# The stub dig: answers from $T/dns ("name ip" lines). $T/dns-fix-after N
# switches to $T/dns.fixed after N lookups, to test "check again".
cat > $T/bin/dig <<'EOF'
#!/bin/bash
name=""; for a in "$@"; do case "$a" in +*|@*|A) ;; *) name=$a ;; esac; done
n=$(cat /tmp/t/dns-count 2>/dev/null || echo 0); n=$((n + 1)); echo $n > /tmp/t/dns-count
f=/tmp/t/dns
if [ -f /tmp/t/dns-fix-after ] && [ "$n" -gt "$(cat /tmp/t/dns-fix-after)" ]; then f=/tmp/t/dns.fixed; fi
awk -v n="$name" '$1 == n {print $2}' "$f"
EOF
chmod +x $T/bin/dig $T/fake-install.sh

BASHRC_ORIG='# user line before
alias ll="ls -l"'
reset() {
  rm -rf /opt/simple-host /tmp/t/installer-args /tmp/t/dns* /root/simple-host-install.log
  printf '%s\n' "$BASHRC_ORIG" > $T/bashrc
  cat >> $T/bashrc <<'EOF'
# >>> simple-host first-login >>>
if [ -t 0 ] && [ -t 1 ] && [ -x /opt/simple-host-setup/first-login.sh ]; then
  /opt/simple-host-setup/first-login.sh
fi
# <<< simple-host first-login <<<
EOF
  echo '# user line after' >> $T/bashrc
}
run() { # run INPUT [ENV...] -> $T/out, $T/rc
  local input=$1; shift
  printf '%b' "$input" | env PATH="$T/bin:$PATH" PUBLIC_IP=203.0.113.7 BASHRC=$T/bashrc PINS_FILE=$T/pins.env \
    INSTALLER_URL=file://$T/fake-install.sh SELF=/opt/simple-host-setup/first-login.sh \
    DNS_WAIT_SECONDS=3 DNS_POLL_SECONDS=1 "$@" bash "$FL" > $T/out 2>&1
  echo $? > $T/rc
}
rc_is() { if [ "$(cat $T/rc)" = "$1" ]; then ok "$2 (exit $1)"; else bad "$2: exit $(cat $T/rc), want $1"; cat $T/out; fi; }
args_are() { if [ "$(cat $T/installer-args 2>/dev/null)" = "$1" ]; then ok "$2"; else bad "$2: installer args '$(cat $T/installer-args 2>/dev/null)', want '$1'"; fi; }
hook_gone() { if grep -q 'simple-host first-login' $T/bashrc; then bad "hook still in .bashrc"; else ok "hook removed from .bashrc"; fi
  if diff <(printf '%s\n# user line after\n' "$BASHRC_ORIG") $T/bashrc >/dev/null; then ok ".bashrc otherwise unchanged"; else bad ".bashrc other lines changed"; cat $T/bashrc; fi; }
hook_kept() { if [ "$(grep -c 'simple-host first-login' $T/bashrc)" = 2 ]; then ok "hook kept for next login"; else bad "hook not kept"; fi; }

echo "== 1. happy path: DNS right, defaults accepted"
reset
printf 'hosting.example.com 203.0.113.7\nsites.hosting.example.com 203.0.113.7\n' > $T/dns
run 'Hosting.Example.com\n\nme@example.com\ny\n'
rc_is 0 "install ran"
args_are "--host hosting.example.com --content sites.hosting.example.com --email me@example.com" "installer got the answers, sites address defaulted"
has $T/out "203.0.113.7" "shows the droplet IP"
has $T/out "A records for hosting.example.com *.hosting.example.com pointing at 203.0.113.7" "names the DNS records in one line"
has $T/out "ok       hosting.example.com -> 203.0.113.7" "DNS check passes"
has $T/out "https://hosting.example.com/admin" "prints the dashboard URL"
has $T/out "grep ^ADMIN_API_KEY= /opt/simple-host/.env" "says how to get the admin key again"
hook_gone
if [ "$(stat -c %a /root/simple-host-install.log)" = 600 ]; then ok "install log is root-only (600)"; else bad "install log mode $(stat -c %a /root/simple-host-install.log)"; fi

echo "== 2. validation, a sites address elsewhere, no email, DNS missing then continue"
reset
printf 'foo.example.com 203.0.113.7\n' > $T/dns
run 'http://\nnot a host\nhttps://Foo.Example.com/path\nfoo.example.com\ncdn.other.org\nnope\n\nx\nc\ny\n'
rc_is 0 "install ran"
has $T/out "That is not a hostname" "rejects a bad address"
has $T/out "It has to differ from the dashboard address" "rejects sites address = dashboard address"
has $T/out "That is not an email address" "rejects a bad email"
has $T/out "A records for foo.example.com *.foo.example.com cdn.other.org" "adds the sites address when it is not under the domain"
has $T/out "missing  cdn.other.org does not resolve yet" "reports the missing record"
has $T/out "Type r, c or q" "rejects an unknown choice"
args_are "--host foo.example.com --content cdn.other.org" "cleaned address, no --email when skipped"
hook_gone

echo "== 3. DNS wrong, then right after checking again"
reset
printf 'a.example.net 198.51.100.1\nsites.a.example.net 198.51.100.1\n' > $T/dns
printf 'a.example.net 203.0.113.7\nsites.a.example.net 203.0.113.7\n' > $T/dns.fixed
echo 3 > $T/dns-fix-after
run 'a.example.net\n\n\nr\ny\n'
rc_is 0 "install ran"
has $T/out "wrong    a.example.net -> 198.51.100.1 (should be 203.0.113.7)" "reports the wrong address"
has $T/out "ok       sites.a.example.net -> 203.0.113.7" "passes after checking again"
args_are "--host a.example.net --content sites.a.example.net" "installer ran after DNS fixed"

echo "== 4. DNS never resolves: check again times out, then quit"
reset
: > $T/dns
run 'b.example.net\n\n\nr\nq\n'
rc_is 1 "stopped"
has $T/out "Not yet." "says DNS is not there yet after the wait"
if [ -f $T/installer-args ]; then bad "installer ran after quit"; else ok "installer not run"; fi
hook_kept

echo "== 5. answering n starts over"
reset
printf 'one.example.com 203.0.113.7\nsites.one.example.com 203.0.113.7\ntwo.example.com 203.0.113.7\nsites.two.example.com 203.0.113.7\n' > $T/dns
run 'one.example.com\n\n\nn\ntwo.example.com\n\n\ny\n'
rc_is 0 "install ran"
has $T/out "Starting over." "starts over"
args_are "--host two.example.com --content sites.two.example.com" "second answers used"

echo "== 6. installer does not match its sha256"
reset
printf 'c.example.com 203.0.113.7\nsites.c.example.com 203.0.113.7\n' > $T/dns
sed "s/^INSTALLER_SHA256=.*/INSTALLER_SHA256=$(printf '0%.0s' $(seq 64))/" $T/pins.env > $T/pins-bad.env
run 'c.example.com\n\n\ny\n' PINS_FILE=$T/pins-bad.env
rc_is 1 "refused"
has $T/out "does not match its pinned sha256, so it was not run" "says why"
if [ -f $T/installer-args ]; then bad "installer ran with a bad hash"; else ok "installer not run"; fi
hook_kept

echo "== 7. installer fails"
reset
printf 'd.example.com 203.0.113.7\nsites.d.example.com 203.0.113.7\n' > $T/dns
run 'd.example.com\n\n\ny\n' FAKE_EXIT=3
rc_is 1 "failure reported"
has $T/out "The install did not finish." "says it failed"
has $T/out "Run /opt/simple-host-setup/first-login.sh to try again" "says how to retry"
hook_kept

echo "== 8. download fails"
reset
printf 'e.example.com 203.0.113.7\nsites.e.example.com 203.0.113.7\n' > $T/dns
run 'e.example.com\n\n\ny\n' INSTALLER_URL=file:///nonexistent DOWNLOAD_RETRIES=0
rc_is 1 "failure reported"
has $T/out "Could not download the installer." "says the download failed"
hook_kept

echo "== 9. already set up"
reset
mkdir -p /opt/simple-host && echo 'SITE_DOMAIN=done.example.com' > /opt/simple-host/.env
run ''
rc_is 0 "nothing to do"
has $T/out "already set up: https://done.example.com/admin" "points at the dashboard"
hook_gone

echo "== 10. input ends"
reset
run 'f.example.com\n'
rc_is 1 "stops"
has $T/out "No input." "says so"
hook_kept

echo "== 11. getent fallback when dig is missing (/etc/hosts)"
reset
cp /etc/hosts /tmp/hosts.bak
printf '203.0.113.7 g.example.com\n203.0.113.7 sites.g.example.com\n' >> /etc/hosts
mv $T/bin/dig $T/dig.off
run 'g.example.com\n\n\ny\n'
mv $T/dig.off $T/bin/dig; cp /tmp/hosts.bak /etc/hosts
rc_is 0 "install ran"
has $T/out "ok       g.example.com -> 203.0.113.7" "resolved through getent"

echo "== 12. Ctrl+C"
reset
mkfifo $T/fifo
set -m
env PATH="$T/bin:$PATH" PUBLIC_IP=203.0.113.7 BASHRC=$T/bashrc PINS_FILE=$T/pins.env bash "$FL" < $T/fifo > $T/out 2>&1 &
pid=$!
exec 3>$T/fifo
sleep 1; kill -INT $pid; wait $pid; echo $? > $T/rc
exec 3>&-
set +m
rc_is 130 "interrupted"
has $T/out "Setup stopped. It runs again at your next login" "says it runs again"
hook_kept

echo "== 13. 001_onboot adds the hook once and lets SSH in"
reset
cp /root/.bashrc /tmp/bashrc.bak
mkdir -p /etc/ssh
printf 'PermitRootLogin prohibit-password\nMatch User root\n        ForceCommand echo "Please wait while we get your droplet ready..."\n' > /etc/ssh/sshd_config
printf '#!/bin/sh\necho "systemctl $*" >> /tmp/t/systemctl.log\n' > $T/bin/systemctl; chmod +x $T/bin/systemctl
PATH="$T/bin:$PATH" bash /src/files/var/lib/cloud/scripts/per-instance/001_onboot
PATH="$T/bin:$PATH" bash /src/files/var/lib/cloud/scripts/per-instance/001_onboot
if [ "$(grep -c '^# >>> simple-host first-login >>>$' /root/.bashrc)" = 1 ]; then ok "hook added once after two runs"; else bad "hook count $(grep -c 'simple-host first-login >>>' /root/.bashrc)"; fi
if grep -qE 'Match User root|ForceCommand' /etc/ssh/sshd_config; then bad "ForceCommand left in sshd_config"; else ok "ForceCommand removed"; fi
has /etc/ssh/sshd_config "PermitRootLogin prohibit-password" "other sshd settings kept"
has $T/systemctl.log "systemctl restart ssh" "ssh restarted"

echo "== 14. the hook does not run without a terminal"
install -D -m 755 $T/fake-install.sh /opt/simple-host-setup/first-login.sh
rm -f $T/installer-args
bash -ic 'true' </dev/null >/dev/null 2>&1
if [ -f $T/installer-args ]; then bad "hook ran in a non-interactive login"; else ok "hook skipped without a terminal"; fi
# ...and does run in an interactive one (script(1) gives it a terminal).
if command -v script >/dev/null; then
  script -qec "bash -ic 'true'" /dev/null </dev/null >/dev/null 2>&1
  if [ -f $T/installer-args ]; then ok "hook runs at an interactive login"; else bad "hook did not run at an interactive login"; fi
fi
rm -rf /opt/simple-host-setup; cp /tmp/bashrc.bak /root/.bashrc

echo "== 15. motd before and after setup"
rm -rf /opt/simple-host
sh /src/files/etc/update-motd.d/99-one-click > $T/out 2>&1
has $T/out "Setup has not run yet" "before setup: says how to start it"
has $T/out "/opt/simple-host-setup/first-login.sh" "before setup: names the script"
mkdir -p /opt/simple-host && echo 'SITE_DOMAIN=hosting.example.com' > /opt/simple-host/.env
sh /src/files/etc/update-motd.d/99-one-click > $T/out 2>&1
has $T/out "Dashboard:          https://hosting.example.com/admin" "after setup: dashboard URL"
hasnt $T/out "Setup has not run yet" "after setup: no setup prompt"

echo
echo "$pass passed, $failn failed"
[ "$failn" -eq 0 ]
