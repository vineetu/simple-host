#!/usr/bin/env bash
# Runs first-login.sh, upgrade.sh, 001_onboot and the motd in a throwaway
# ubuntu:24.04 container, with the installer replaced by a stub, DNS answered
# by a stub `dig` (or /etc/hosts, for the getent fallback) and the droplet
# metadata served from files. Nothing is installed on the host and the real
# installer never runs.
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
UP=/src/files/opt/simple-host-setup/upgrade.sh
T=/tmp/t; mkdir -p $T/bin
pass=0; failn=0
ok()   { echo "PASS: $*"; pass=$((pass + 1)); }
bad()  { echo "FAIL: $*"; failn=$((failn + 1)); }
has()  { if grep -qF -- "$2" "$1"; then ok "$3"; else bad "$3 (missing: $2)"; echo "---- output"; cat "$1"; echo "----"; fi; }
hasnt(){ if grep -qF -- "$2" "$1"; then bad "$3 (unexpected: $2)"; else ok "$3"; fi; }

# The stub installer: records its arguments, prints what the real one prints
# (the admin key too). FAKE_SLEEP makes it slow, FAKE_EXIT makes it fail.
cat > $T/fake-install.sh <<'EOF'
#!/bin/bash
echo "$@" > /tmp/t/installer-args
echo '==> writing configuration'
sleep "${FAKE_SLEEP:-0}"
echo '{"host":"https://x","admin_api_key":"sh_admin_test","dir":"/opt/simple-host"}'
echo "Keep the admin key."
echo finished > /tmp/t/installer-finished
exit "${FAKE_EXIT:-0}"
EOF
FAKE_SHA=$(sha256sum $T/fake-install.sh | cut -d' ' -f1)
cat > $T/pins.env <<EOF
INSTALLER_RELEASE=v0.0.0-test
INSTALLER_COMMIT=0000000000000000000000000000000000000000
INSTALLER_SHA256=$FAKE_SHA
EOF

# The stub dig: A answers from $T/dns, AAAA from $T/dns6 ("name address"
# lines). $T/dns-fix-after N switches A to $T/dns.fixed after N A lookups.
cat > $T/bin/dig <<'EOF'
#!/bin/bash
name=""; type=A
for a in "$@"; do case "$a" in +*|@*) ;; A|AAAA) type=$a ;; *) name=$a ;; esac; done
if [ "$type" = AAAA ]; then f=/tmp/t/dns6; else
  n=$(cat /tmp/t/dns-count 2>/dev/null || echo 0); n=$((n + 1)); echo $n > /tmp/t/dns-count
  f=/tmp/t/dns
  if [ -f /tmp/t/dns-fix-after ] && [ "$n" -gt "$(cat /tmp/t/dns-fix-after)" ]; then f=/tmp/t/dns.fixed; fi
fi
[ -f "$f" ] && awk -v n="$name" '$1 == n {print $2}' "$f"
exit 0
EOF
chmod +x $T/bin/dig $T/fake-install.sh

BASHRC_ORIG='# user line before
alias ll="ls -l"'
reset() {
  rm -rf /opt/simple-host $T/installer-args $T/installer-finished $T/dns* $T/done $T/lock $T/logs $T/md $T/bin/hostname
  mkdir -p $T/logs
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
ENVS=("PATH=$T/bin:$PATH" PUBLIC_IP=203.0.113.7 "BASHRC=$T/bashrc" "PINS_FILE=$T/pins.env"
  "INSTALLER_URL=file://$T/fake-install.sh" SELF=/opt/simple-host-setup/first-login.sh
  "DONE_FILE=$T/done" "LOCK_FILE=$T/lock" "LOG_DIR=$T/logs" DNS_WAIT_SECONDS=3 DNS_POLL_SECONDS=1)
run() { # run INPUT [ENV...] -> $T/out, $T/rc
  local input=$1; shift
  printf '%b' "$input" | env "${ENVS[@]}" "$@" bash "$FL" > $T/out 2>&1
  echo $? > $T/rc
}
rc_is() { if [ "$(cat $T/rc)" = "$1" ]; then ok "$2 (exit $1)"; else bad "$2: exit $(cat $T/rc), want $1"; cat $T/out; fi; }
args_are() { if [ "$(cat $T/installer-args 2>/dev/null)" = "$1" ]; then ok "$2"; else bad "$2: installer args '$(cat $T/installer-args 2>/dev/null)', want '$1'"; fi; }
hook_gone() { if grep -q 'simple-host first-login' $T/bashrc; then bad "hook still in .bashrc"; else ok "hook removed from .bashrc"; fi
  if diff <(printf '%s\n# user line after\n' "$BASHRC_ORIG") $T/bashrc >/dev/null; then ok ".bashrc otherwise unchanged"; else bad ".bashrc other lines changed"; cat $T/bashrc; fi; }
hook_kept() { if [ "$(grep -c 'simple-host first-login' $T/bashrc)" = 2 ]; then ok "hook kept for next login"; else bad "hook not kept"; fi; }
not_done() { if [ -f $T/done ]; then bad "done marker written"; else ok "no done marker"; fi; }
tmp_count() { find /tmp -maxdepth 1 -name 'tmp.*' | wc -l; }
dns_right() { : > $T/dns; for n in "$@"; do echo "$n 203.0.113.7" >> $T/dns; done; }

echo "== 1. happy path: DNS right, default sites address"
reset
dns_right hosting.example.com sites.hosting.example.com
run 'Hosting.Example.com\n\ny\n'
rc_is 0 "install ran"
args_are "--host hosting.example.com --content sites.hosting.example.com" "installer got the answers, sites address defaulted"
has $T/out "203.0.113.7" "shows the droplet IP"
has $T/out "A records for hosting.example.com *.hosting.example.com pointing at 203.0.113.7" "names the DNS records in one line"
has $T/out "ok       hosting.example.com -> 203.0.113.7" "DNS check passes"
has $T/out "sh_admin_test" "the admin key is shown on screen"
has $T/out "https://hosting.example.com/admin" "prints the dashboard URL"
has $T/out "grep ^ADMIN_API_KEY= /opt/simple-host/.env" "says how to get the admin key again"
has $T/out "/opt/simple-host-setup/upgrade.sh" "says how to upgrade"
hasnt $T/out "email" "asks no email"
hook_gone
has $T/done "host=hosting.example.com" "done marker written after success"
log=$(ls $T/logs/simple-host-install-*.log)
if [ "$(stat -c %a "$log")" = 600 ]; then ok "install log is root-only (600)"; else bad "install log mode $(stat -c %a "$log")"; fi
hasnt "$log" "admin_api_key" "admin key line stripped from the log"
has "$log" "Keep the admin key." "rest of the log kept"

echo "== 2. validation, a sites address elsewhere, DNS missing then continue"
reset
dns_right foo.example.com
run 'http://\nnot a host\nhttps://Foo.Example.com/path\nfoo.example.com\nbad host\ncdn.other.org\nx\nc\ny\n'
rc_is 0 "install ran"
has $T/out "That is not a hostname. Use a name like hosting.example.com." "rejects a bad address"
has $T/out "That is not a hostname. Use a name like sites.hosting.example.com." "rejects a bad sites address"
has $T/out "It has to differ from the dashboard address" "rejects sites address = dashboard address"
has $T/out "A records for foo.example.com *.foo.example.com cdn.other.org" "adds the sites address when it is not under the domain"
has $T/out "missing  cdn.other.org does not resolve yet" "reports the missing record"
has $T/out "check again for up to 1 min" "wait rounds up to whole minutes"
has $T/out "Type r, c or q" "rejects an unknown choice"
args_are "--host foo.example.com --content cdn.other.org" "cleaned address"
hook_gone

echo "== 3. DNS wrong, then right after checking again"
reset
printf 'a.example.net 198.51.100.1\nsites.a.example.net 198.51.100.1\n' > $T/dns
printf 'a.example.net 203.0.113.7\nsites.a.example.net 203.0.113.7\n' > $T/dns.fixed
echo 3 > $T/dns-fix-after
run 'a.example.net\n\nr\ny\n'
rc_is 0 "install ran"
has $T/out "wrong    a.example.net -> 198.51.100.1 (should be 203.0.113.7)" "reports the wrong address"
has $T/out "ok       sites.a.example.net -> 203.0.113.7" "passes after checking again"
args_are "--host a.example.net --content sites.a.example.net" "installer ran after DNS fixed"

echo "== 4. DNS never resolves: check again times out, then quit"
reset
: > $T/dns
run 'b.example.net\n\nr\nq\n'
rc_is 1 "stopped"
has $T/out "Not yet." "says DNS is not there yet after the wait"
if [ -f $T/installer-args ]; then bad "installer ran after quit"; else ok "installer not run"; fi
hook_kept

echo "== 5. answering n starts over"
reset
dns_right one.example.com sites.one.example.com two.example.com sites.two.example.com
run 'one.example.com\n\nn\ntwo.example.com\n\ny\n'
rc_is 0 "install ran"
has $T/out "Starting over." "starts over"
args_are "--host two.example.com --content sites.two.example.com" "second answers used"

echo "== 6. installer does not match its sha256"
reset
dns_right c.example.com sites.c.example.com
sed "s/^INSTALLER_SHA256=.*/INSTALLER_SHA256=$(printf '0%.0s' $(seq 64))/" $T/pins.env > $T/pins-bad.env
before=$(tmp_count)
run 'c.example.com\n\ny\n' PINS_FILE=$T/pins-bad.env
rc_is 1 "refused"
has $T/out "does not match its pinned sha256, so it was not run" "says why"
if [ -f $T/installer-args ]; then bad "installer ran with a bad hash"; else ok "installer not run"; fi
if [ "$(tmp_count)" = "$before" ]; then ok "temporary installer file removed"; else bad "temporary installer file left in /tmp"; fi
hook_kept

echo "== 7. installer fails, and the retry is not treated as done (item 1)"
reset
dns_right d.example.com sites.d.example.com
# install.sh writes SITE_DOMAIN to .env before it pulls, migrates and checks
# health; a failure after that must not look like a finished setup.
run 'd.example.com\n\ny\n' FAKE_EXIT=3
mkdir -p /opt/simple-host && echo 'SITE_DOMAIN=d.example.com' > /opt/simple-host/.env
rc_is 1 "failure reported"
has $T/out "The install did not finish." "says it failed"
has $T/out "Run /opt/simple-host-setup/first-login.sh to try again" "says how to retry"
hook_kept
not_done
log=$(ls $T/logs/simple-host-install-*.log)
has "$log" "admin_api_key" "a failed run's log is left whole for diagnosis"
sleep 1   # a new log name per second
rm -f $T/installer-args
run 'd.example.com\n\ny\n'
rc_is 0 "retry runs setup again despite SITE_DOMAIN in .env"
hasnt $T/out "already set up" "retry not mistaken for done"
args_are "--host d.example.com --content sites.d.example.com" "retry ran the installer"
if [ "$(find $T/logs -name 'simple-host-install-*.log' | wc -l)" = 2 ]; then ok "a fresh log for each run"; else bad "expected two logs: $(ls $T/logs)"; fi
hook_gone

echo "== 8. download fails"
reset
dns_right e.example.com sites.e.example.com
before=$(tmp_count)
run 'e.example.com\n\ny\n' INSTALLER_URL=file:///nonexistent DOWNLOAD_RETRIES=0
rc_is 1 "failure reported"
has $T/out "Could not download the installer." "says the download failed"
if [ "$(tmp_count)" = "$before" ]; then ok "temporary installer file removed"; else bad "temporary installer file left in /tmp"; fi
hook_kept

echo "== 9. already set up (the marker)"
reset
echo 'host=done.example.com' > $T/done
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
run 'g.example.com\n\ny\n'
mv $T/dig.off $T/bin/dig; cp /tmp/hosts.bak /etc/hosts
rc_is 0 "install ran"
has $T/out "ok       g.example.com -> 203.0.113.7" "resolved through getent"

echo "== 12. Ctrl+C at a prompt"
reset
mkfifo $T/fifo
set -m
env "${ENVS[@]}" bash "$FL" < $T/fifo > $T/out 2>&1 &
pid=$!
exec 3>$T/fifo
sleep 1; kill -INT $pid; wait $pid; echo $? > $T/rc
exec 3>&-
set +m
rm -f $T/fifo
rc_is 130 "interrupted"
has $T/out "Setup stopped. It runs again at your next login" "says it runs again"
hook_kept

echo "== 13. the SSH session drops during the install (item 2)"
reset
dns_right h.example.com sites.h.example.com
set -m
printf 'h.example.com\n\ny\n' | env "${ENVS[@]}" FAKE_SLEEP=3 bash "$FL" > $T/out 2>&1 &
pid=$!
set +m
for _ in $(seq 20); do [ -f $T/installer-args ] && break; sleep 0.2; done
pgid=$(awk '{print $5}' /proc/$pid/stat)
kill -HUP -- -"$pgid"; wait $pid 2>/dev/null
if [ -f $T/installer-finished ]; then bad "installer finished before the hang-up (test too fast)"; else ok "hung up while the installer was running"; fi
for _ in $(seq 30); do [ -f $T/done ] && break; sleep 0.2; done
if [ -f $T/installer-finished ]; then ok "installer finished after the session was gone"; else bad "installer killed by the hang-up"; fi
has $T/done "host=h.example.com" "done marker written by the detached run"
hook_gone
log=$(ls $T/logs/simple-host-install-*.log)
hasnt "$log" "admin_api_key" "admin key line stripped by the detached run"

echo "== 14. a second session while setup runs (item 4)"
reset
echo "$T/logs/simple-host-install-running.log" > $T/lock
flock $T/lock sleep 3 &
lockpid=$!
sleep 0.5
run 'i.example.com\n\ny\n'
rc_is 0 "second session leaves"
has $T/out "Setup is already running in another session." "says setup is running"
has $T/out "Follow it with: tail -f $T/logs/simple-host-install-running.log" "says how to follow it"
if [ -f $T/installer-args ]; then bad "second session ran the installer"; else ok "installer not run twice"; fi
hook_kept
wait $lockpid

echo "== 15. droplet addresses from the metadata service (item 5)"
reset
md=$T/md; mkdir -p $md/interfaces/public/0/ipv4 $md/interfaces/public/0/ipv6 $md/reserved_ip/ipv4
echo 203.0.113.20 > $md/interfaces/public/0/ipv4/address
dns_right j.example.com; echo "sites.j.example.com 203.0.113.20" > $T/dns; echo "j.example.com 203.0.113.20" >> $T/dns
run 'j.example.com\n\ny\n' PUBLIC_IP= METADATA_BASE=file://$md
has $T/out "public IPv4 address is 203.0.113.20" "public IPv4 from metadata"
has $T/out "ok       j.example.com -> 203.0.113.20" "DNS checked against it"

reset
mkdir -p $md/interfaces/public/0/ipv4 $md/reserved_ip/ipv4
echo 203.0.113.20 > $md/interfaces/public/0/ipv4/address
echo true > $md/reserved_ip/ipv4/active; echo 198.51.100.50 > $md/reserved_ip/ipv4/ip_address
printf 'k.example.com 198.51.100.50\nsites.k.example.com 203.0.113.20\n' > $T/dns
run 'k.example.com\n\ny\n' PUBLIC_IP= METADATA_BASE=file://$md
has $T/out "public IPv4 address is 198.51.100.50" "a reserved IP is the one shown"
has $T/out "pointing at 198.51.100.50" "records point at the reserved IP"
has $T/out "ok       k.example.com -> 198.51.100.50" "reserved IP accepted"
has $T/out "ok       sites.k.example.com -> 203.0.113.20" "droplet's own IP accepted too"

reset
printf '#!/bin/sh\necho "10.17.0.5 172.17.0.1 fe80::1 2001:db8::5 203.0.113.30"\n' > $T/bin/hostname; chmod +x $T/bin/hostname
printf 'l.example.com 203.0.113.30\nsites.l.example.com 203.0.113.30\n' > $T/dns
run 'l.example.com\n\ny\n' PUBLIC_IP= METADATA_BASE=file:///nonexistent
has $T/out "public IPv4 address is 203.0.113.30" "hostname -I fallback picks the public IPv4"

reset
printf '#!/bin/sh\necho "10.17.0.5 172.17.0.1 fe80::1"\n' > $T/bin/hostname; chmod +x $T/bin/hostname
: > $T/dns
run 'm.example.com\n\ny\n' PUBLIC_IP= METADATA_BASE=file:///nonexistent
rc_is 0 "setup goes on"
has $T/out "public IPv4 address is unknown" "says the IP is unknown"
has $T/out "so DNS is not checked" "skips the DNS check with a clear line"
hasnt $T/out "ok       m.example.com" "an unresolved name is never ok"
rm -f $T/bin/hostname

echo "== 16. AAAA records elsewhere (item 6)"
reset
mkdir -p $md/interfaces/public/0/ipv4 $md/interfaces/public/0/ipv6
echo 203.0.113.20 > $md/interfaces/public/0/ipv4/address
echo 2001:DB8::20 > $md/interfaces/public/0/ipv6/address
printf 'n.example.com 203.0.113.20\nsites.n.example.com 203.0.113.20\n' > $T/dns
printf 'n.example.com 2001:db8::99\nsites.n.example.com 2001:db8::20\n' > $T/dns6
run 'n.example.com\n\ny\n' PUBLIC_IP= METADATA_BASE=file://$md
rc_is 0 "a warning does not stop setup"
has $T/out "warning  n.example.com has an IPv6 (AAAA) record 2001:db8::99 that is not this droplet" "warns about the stray AAAA"
has $T/out "or point it at 2001:db8::20" "names the droplet's IPv6"
hasnt $T/out "sites.n.example.com has an IPv6" "no warning for an AAAA that is the droplet's"

echo "== 17. upgrade.sh keeps the addresses (item 3)"
reset
mkdir -p /opt/simple-host
printf 'IMAGE=x\nSITE_DOMAIN=up.example.com\nCONTENT_HOST=sites.up.example.com\n' > /opt/simple-host/.env
printf "  var INSTALLER_RELEASE = 'v9.9.9';\n  var INSTALLER_COMMIT = '%s';\n  var INSTALLER_SHA256 = '%s';\n" "$(printf 'a%.0s' $(seq 40))" "$FAKE_SHA" > $T/setup.js
env PATH="$T/bin:$PATH" SETUP_JS_URL=file://$T/setup.js INSTALLER_URL=file://$T/fake-install.sh LOG_DIR=$T/logs bash $UP > $T/out 2>&1; echo $? > $T/rc
rc_is 0 "upgrade ran"
has $T/out "simple-host.app/setup pins v9.9.9" "uses the setup page's pin"
args_are "--host up.example.com --content sites.up.example.com" "passes the addresses from .env"
log=$(ls $T/logs/simple-host-upgrade-*.log)
hasnt "$log" "admin_api_key" "admin key line stripped from the upgrade log"
env PATH="$T/bin:$PATH" SETUP_JS_URL=file://$T/setup.js INSTALLER_URL=file://$T/fake-install.sh LOG_DIR=$T/logs bash $UP --commit "$(printf 'a%.0s' $(seq 40))" --sha256 "$(printf '0%.0s' $(seq 64))" > $T/out 2>&1; echo $? > $T/rc
rc_is 1 "a wrong sha256 is refused"
has $T/out "does not match the sha256" "says why"
env PATH="$T/bin:$PATH" LOG_DIR=$T/logs bash $UP --commit abc > $T/out 2>&1; echo $? > $T/rc
rc_is 2 "commit without sha256 refused"
rm -rf /opt/simple-host
env PATH="$T/bin:$PATH" LOG_DIR=$T/logs bash $UP > $T/out 2>&1; echo $? > $T/rc
rc_is 1 "nothing to upgrade without .env"
has $T/out "SITE_DOMAIN and CONTENT_HOST are not both set" "says why"

echo "== 18. 001_onboot adds the hook once and lets SSH in"
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

echo "== 19. the hook does not run without a terminal"
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

echo "== 20. motd before and after setup"
rm -rf /opt/simple-host /opt/simple-host-setup
mkdir -p /opt/simple-host && echo 'SITE_DOMAIN=hosting.example.com' > /opt/simple-host/.env
sh /src/files/etc/update-motd.d/99-one-click > $T/out 2>&1
has $T/out "Simple Host is not set up yet. Setup starts after this message at a root login, or run:" "before setup (even with SITE_DOMAIN in .env): says how to start it"
has $T/out "/opt/simple-host-setup/first-login.sh" "before setup: names the script"
has $T/out "bypasses UFW" "notes that Docker-published ports bypass UFW"
mkdir -p /opt/simple-host-setup && echo 'host=hosting.example.com' > /opt/simple-host-setup/.done
sh /src/files/etc/update-motd.d/99-one-click > $T/out 2>&1
has $T/out "Dashboard:          https://hosting.example.com/admin" "after setup: dashboard URL"
has $T/out "/opt/simple-host-setup/upgrade.sh" "after setup: upgrade command"
hasnt $T/out "not set up yet" "after setup: no setup prompt"

echo
echo "$pass passed, $failn failed"
[ "$failn" -eq 0 ]
