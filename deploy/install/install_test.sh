#!/usr/bin/env bash
# Exercise the actual configuration-writing section without touching a host.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
python3 "$ROOT/deploy/install/upgrade_retry_test.py"
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
    # Compose must receive saved flags from .env, not inherited shell overrides.
    source += '\nfor app_flag in MCP_APP_PHOTO_PICKER MCP_APP_MY_SITES MCP_APP_DASHBOARD MCP_APP_SITE_PREVIEW; do\n  [ "${!app_flag+x}" != x ] || exit 91\ndone\n'
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
    flags = ('MCP_APP_PHOTO_PICKER', 'MCP_APP_MY_SITES', 'MCP_APP_DASHBOARD', 'MCP_APP_SITE_PREVIEW')
    assert all(first[n] == 'off' for n in flags)
    choices = dict(zip(flags, ('accounts:alice,bob', 'on', 'accounts:alice', 'off')))
    choices.update(PHOTO_MAX_BYTES='800000', PHOTO_BODY_TIMEOUT_SECONDS='45', NOTIFY_PER_DAY='1000', OIDC_LABEL='Company')
    saved = install / '.env'
    saved.write_text('\n'.join(n + '=' + choices.get(n, v) for n, v in first.items()) + '\n' +
                     ''.join(n + '=' + v + '\n' for n, v in choices.items() if n not in first))
    first.update(choices)
    # A shell setting must not overwrite the stored account allowlist on upgrade.
    env['MCP_APP_PHOTO_PICKER'] = 'off'
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
    for n in flags:
        assert compose['services']['app']['environment'][n] == '${' + n + ':-off}'
    assert compose['services']['app']['environment']['PASSCODE_ENC_KEY'] == '${PASSCODE_ENC_KEY:-}'
    assert 'fetch "$DIR/compose.yaml" compose.yaml' in (root / 'deploy/install/install.sh').read_text()
    for path in ('deploy/hack/standalone/compose.yaml',):
        config = yaml.safe_load((root / path).read_text())
        app_env = config['services']['app']['environment']
        if isinstance(app_env, list): app_env = dict(line.split('=', 1) for line in app_env)
        for n in flags:
            assert app_env[n] == '${' + n + ':-off}', path + ': ' + n

print('PASS: installer generates and preserves PASSCODE_ENC_KEY, upgrades existing .env, passes the key through compose, and replaces .env privately and atomically')
PY

python3 - "$ROOT" <<'PY'
import json, os, pathlib, subprocess, sys
root = pathlib.Path(sys.argv[1])
env = dict(os.environ, DB_PASSWORD='compose-fixture', ADMIN_API_KEY='compose-fixture', EVENTS='hosted')
command = ['docker', 'compose', '-f', str(root / 'compose.yaml')]
default = subprocess.check_output(command + ['config', '--services'], env=env, text=True).splitlines()
assert 'static' not in default, 'Static must be off by default'
config = json.loads(subprocess.check_output(command + ['--profile', 'static', 'config', '--format', 'json'], env=env))
services = config['services']
for name in ('app', 'migrate', 'static'):
    assert 'EVENTS' not in services[name]['environment'], name + ' accepts the removed product selector'
static = services['static']
assert static['profiles'] == ['static'] and static['command'] == ['static']
assert static['image'] == services['app']['image']
mounts = {v['target']: v for v in static['volumes']}
assert mounts['/data/sites']['read_only']
assert not mounts['/data/static-state'].get('read_only', False)
assert static['environment']['STATIC_STATE_DIR'] == '/data/static-state'
assert static['environment']['STATIC_API_URL'] == 'http://app:8090'
assert services['web']['depends_on'].keys() == {'app'}
print('PASS: Compose renders with Static off by default and opt-in read-only site files plus writable state')
PY

python3 "$ROOT/deploy/install/product_init_test.py" root
