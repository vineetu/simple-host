#!/usr/bin/env python3
"""Retry the real install dispatch/database phase before app creation."""
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]
with tempfile.TemporaryDirectory(prefix='install-retry-') as tmp:
    work = Path(tmp)
    install = work / 'install'
    install.mkdir()
    (install / '.env').write_text('ADMIN_API_KEY=preserved-fixture-key\n')
    (install / 'compose.yaml').write_text('services: {}\n')
    source = (ROOT / 'deploy/install/install.sh').read_text()
    dispatch = source.split('if ! command -v docker', 1)[0]
    database = source.split('say "pulling $IMAGE"', 1)[1].split('say "waiting for the instance to answer"', 1)[0]
    script = work / 'install.sh'
    script.write_text(dispatch.replace('DIR=/opt/simple-host', 'DIR=' + str(install)) + database)
    mockbin = work / 'bin'
    mockbin.mkdir()
    docker = mockbin / 'docker'
    docker.write_text('''#!/bin/bash
set -eu
echo "$*" >> "$FIXTURE/docker-calls"
case "$*" in
  *"ps -aq app")
    if [ -f "$FIXTURE/app-created" ]; then echo fixture-app; fi ;;
  "volume ls "*)
    if [ -f "$FIXTURE/database-volume" ]; then echo simple-host_db; fi ;;
  *"exec -T db sh -c "*"first_install_pending_release"*)
    if [ -f "$FIXTURE/pending-release" ]; then echo t; else echo f; fi ;;
  *"exec -T db sh -c "*)
    if [ -f "$FIXTURE/initialized" ]; then echo t; else echo f; fi ;;
  "compose pull --quiet")
    if [ -f "$FIXTURE/fail-pull" ]; then rm "$FIXTURE/fail-pull"; exit 44; fi ;;
  "compose run --rm -T app migrate -init -record-product")
    if [ -f "$FIXTURE/fail-after-schema" ]; then
      touch "$FIXTURE/database-volume" "$FIXTURE/initialized" "$FIXTURE/pending-release"
      rm "$FIXTURE/fail-after-schema"
      exit 45
    fi
    rm -f "$FIXTURE/pending-release" ;;
  "compose up -d --no-build") touch "$FIXTURE/app-created" ;;
esac
''')
    docker.chmod(0o755)
    curl = mockbin / 'curl'
    curl.write_text('''#!/bin/bash
set -eu
while [ "$1" != -o ]; do shift; done
printf '#!/bin/bash\ntouch "$FIXTURE/upgrade-called"\n' > "$2"
''')
    curl.chmod(0o755)
    env = dict(os.environ, PATH=str(mockbin) + ':' + os.environ['PATH'], FIXTURE=str(work))

    def run():
        return subprocess.run(['bash', str(script)], env=env, stdout=subprocess.DEVNULL,
                              stderr=subprocess.DEVNULL).returncode

    (work / 'fail-pull').touch()
    assert run() == 44, 'first install must reach the failing pull'
    assert not (work / 'app-created').exists()
    assert not (work / 'upgrade-called').exists()
    assert run() == 0, 'preparation must remain resumable before app creation'
    assert (work / 'app-created').exists()
    assert not (work / 'upgrade-called').exists(), 'partial install was routed to upgrade'
    assert 'compose run --rm -T app migrate -init -record-product' in (work / 'docker-calls').read_text()
    assert (install / '.env').read_text() == 'ADMIN_API_KEY=preserved-fixture-key\n'
    assert run() == 0
    assert (work / 'upgrade-called').exists(), 'initialized install must use the backup/upgrade path'
    (work / 'app-created').unlink()  # compose down keeps persistent volumes
    (work / 'upgrade-called').unlink()
    (work / 'database-volume').touch()
    (work / 'initialized').touch()
    before = (install / '.env').read_bytes(), (install / 'compose.yaml').read_bytes()
    assert run() == 0
    assert (work / 'upgrade-called').exists(), 'compose down bypassed upgrade'
    assert before == ((install / '.env').read_bytes(), (install / 'compose.yaml').read_bytes())
    calls = (work / 'docker-calls').read_text()
    assert 'up -d --no-deps --no-build --wait db' in calls
    # A volume created before schema initialization is still a resumable first install.
    (work / 'initialized').unlink()
    (work / 'upgrade-called').unlink()
    assert run() == 0
    assert not (work / 'upgrade-called').exists()
    # Schema and the pending marker commit before migrations/verification.
    # A retry must run migrate again, rather than apply the legacy upgrade floor.
    (work / 'app-created').unlink()
    (work / 'fail-after-schema').touch()
    assert run() != 0, 'first install must fail after committing its schema'
    assert (work / 'initialized').exists()
    assert (work / 'pending-release').exists()
    assert not (work / 'app-created').exists()
    assert not (work / 'upgrade-called').exists()
    calls_before = (work / 'docker-calls').read_text()
    assert run() == 0, 'schema creation must not divert a pending first install to upgrade'
    assert not (work / 'pending-release').exists(), 'retry did not finish migration/verification'
    assert (work / 'app-created').exists()
    assert not (work / 'upgrade-called').exists()
    calls_after = (work / 'docker-calls').read_text()[len(calls_before):]
    assert 'compose run --rm -T app migrate -init -record-product' in calls_after
    assert run() == 0
    assert (work / 'upgrade-called').exists(), 'completed first install must use upgrade'
print('PASS: first-install retries and upgrades with containers removed preserve configuration')
