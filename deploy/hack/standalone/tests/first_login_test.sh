#!/usr/bin/env bash
# Tests the detached install path in a disposable Ubuntu container. DNS and
# mail prompts are bypassed by an existing private configuration; no network
# requests or real install commands run in this test.
set -euo pipefail
HERE=$(cd "$(dirname "$0")/.." && pwd)
docker run -i --rm --memory 256m -v "$HERE/digitalocean/first-login.sh:/first-login.sh:ro" ubuntu:24.04 bash -s <<'EOF'
set -eu
mkdir -p /work/setup/digitalocean /work/install
cp /first-login.sh /work/setup/digitalocean/first-login.sh
touch /work/install/.env
printf '# >>> simple-hack setup >>>\nplaceholder\n# <<< simple-hack setup <<<\n' > /work/bashrc
cat > /work/setup/install.sh <<'INSTALL'
#!/bin/bash
sleep 2
touch /work/installed
INSTALL
export SETUP_DIR=/work/setup INSTALL_DIR=/work/install BASHRC=/work/bashrc
bash /first-login.sh >/work/session.log 2>&1 &
parent=$!
# Wait until first-login has launched the detached worker, then simulate a
# lost SSH session. The install must finish and remove its login hook.
for i in $(seq 1 30); do
  grep -q 'Installing.' /work/session.log && break
  sleep 0.1
done
kill -HUP "$parent" 2>/dev/null || true
wait "$parent" 2>/dev/null || true
for i in $(seq 1 50); do [ -f /work/setup/.done ] && break; sleep 0.1; done
test -f /work/installed
test -f /work/setup/.done
! grep -q 'simple-hack setup' /work/bashrc
bash /first-login.sh
echo 'PASS: first-login install survives disconnect, marks success, removes hook and does not rerun'
flock -w 5 /work/install/.first-login.lock true
rm /work/setup/.done
printf '# >>> simple-hack setup >>>\nplaceholder\n# <<< simple-hack setup <<<\n' > /work/bashrc
printf '#!/bin/bash\nexit 12\n' > /work/setup/install.sh
if bash /first-login.sh >/work/failure.log 2>&1; then exit 1; fi
test ! -f /work/setup/.done
grep -q 'simple-hack setup' /work/bashrc
echo 'PASS: failed first-login keeps the retry hook and never marks success'
EOF
