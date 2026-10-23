#!/usr/bin/env bash
# Exercise the actual configuration-writing section without touching a host.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
python3 - "$ROOT" <<'PY'
import base64, os, pathlib, subprocess, sys, tempfile
import yaml
root = pathlib.Path(sys.argv[1])
with tempfile.TemporaryDirectory(prefix='notify-install-test-') as tmp:
    work = pathlib.Path(tmp)
    install = work / 'install'
    mockbin = work / 'bin'
    mockbin.mkdir()
    for name in ('docker', 'systemctl'):
        stub = mockbin / name
        stub.write_text('#!/bin/sh\nexit 0\n')
        stub.chmod(0o755)
    # Stop after .env is written, before fetching files or running containers.
    source = (root / 'deploy/install/install.sh').read_text().split('say "fetching compose files"')[0]
    source = source.replace('DIR=/opt/simple-host', 'DIR=' + str(install))
    script = work / 'install.sh'
    script.write_text(source)
    env = dict(os.environ, PATH=str(mockbin) + ':' + os.environ['PATH'])
    # Observe permissions while the here-document is still being written.
    cat = mockbin / 'cat'
    cat.write_text('#!/bin/bash\n'
                   'target=$(readlink /proc/$$/fd/1)\n'
                   'case "$target" in */.env.*) [ "$(stat -c %a "$target")" = 600 ] || exit 90;; esac\n'
                   'exec /bin/cat "$@"\n')
    cat.chmod(0o755)
    def run():
        subprocess.run(['bash', str(script), '--host', 'notify.example.test'], env=env,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True)
        return dict(line.split('=', 1) for line in (install / '.env').read_text().splitlines())
    first = run()
    assert len(base64.b64decode(first['PASSCODE_ENC_KEY'])) == 32
    assert (install / '.env').stat().st_mode & 0o777 == 0o600
    again = run()
    assert first == again, 're-run changed configuration'
    old = install / '.env'
    old.write_text('\n'.join(line for line in old.read_text().splitlines()
                             if not line.startswith('PASSCODE_ENC_KEY=')) + '\n')
    upgraded = run()
    assert len(base64.b64decode(upgraded['PASSCODE_ENC_KEY'])) == 32
    assert upgraded['ADMIN_API_KEY'] == first['ADMIN_API_KEY']
    assert upgraded['DB_PASSWORD'] == first['DB_PASSWORD']
    assert run() == upgraded, 'upgrade key was not preserved'
    # Interrupt before the atomic rename and verify the existing key survives.
    prior = old.read_bytes()
    prior_inode = old.stat().st_ino
    mv = mockbin / 'mv'
    mv.write_text('#!/bin/bash\n'
                  'test "$(dirname "$2")" = "$(dirname "$3")" || exit 90\n'
                  'test "$(stat -c %a "$2")" = 600 || exit 90\n'
                  'kill -TERM "$PPID"\n'
                  'exit 1\n')
    mv.chmod(0o755)
    interrupted = subprocess.run(['bash', str(script), '--host', 'notify.example.test'], env=env,
                                 stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    assert interrupted.returncode == -15, 'run did not reach the simulated interruption'
    assert old.read_bytes() == prior and old.stat().st_ino == prior_inode, 'interrupted run changed .env'
    mv.unlink()
    assert run() == upgraded
    assert old.stat().st_ino != prior_inode, '.env was overwritten in place'
    assert not list(install.glob('.env.*')), 'temporary configuration was not cleaned up'
    compose = yaml.safe_load((root / 'compose.yaml').read_text())
    assert compose['services']['app']['environment']['PASSCODE_ENC_KEY'] == '${PASSCODE_ENC_KEY:-}'
    assert 'fetch "$DIR/compose.yaml" compose.yaml' in (root / 'deploy/install/install.sh').read_text()
print('PASS: installer generates and preserves PASSCODE_ENC_KEY, upgrades existing .env, passes the key through compose, and replaces .env privately and atomically')
PY
