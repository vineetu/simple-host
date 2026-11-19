#!/usr/bin/env bash
# Compose upgrade/paired restore. Run the copy from the candidate release.
set -euo pipefail
exec python3 - "$0" "$@" <<'PY'
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import signal
import sys
import tempfile
import time
import fcntl

p = argparse.ArgumentParser(description='Back up, upgrade, or restore one stopped Compose install.')
p.add_argument('--dir', required=True, type=Path)
p.add_argument('--product', required=True, choices=['simple-host', 'simple-hack'])
p.add_argument('--to', help='expected candidate release (checked against the selected image)')
p.add_argument('--image', help='candidate app image')
p.add_argument('--compose', type=Path, help='candidate Compose file; defaults to the installed file')
p.add_argument('--caddyfile', type=Path, help='candidate Host Caddyfile')
p.add_argument('--set-env', action='append', default=[], help='explicit installer setting, KEY=VALUE')
p.add_argument('--restore', action='store_true')
p.add_argument('--with', dest='steps', action='append', default=[])
p.add_argument('--skip-pull', action='store_true')
script = Path(sys.argv.pop(1)).resolve()
a = p.parse_args()
os.umask(0o077)
root = a.dir.resolve(strict=True)
env_file = root / '.env'
compose_file = root / 'compose.yaml'
if not env_file.is_file() or not compose_file.is_file():
    p.error('--dir must contain .env and compose.yaml')
if not a.restore and not a.image:
    p.error('upgrading requires --image')
if a.to and not re.fullmatch(r'[A-Za-z0-9_.-]+', a.to):
    p.error('invalid release name')
candidate = a.compose.resolve(strict=True) if a.compose else compose_file
caddy_file = root / 'deploy/compose/Caddyfile'
candidate_caddy = a.caddyfile.resolve(strict=True) if a.caddyfile else None
for line in env_file.read_text().splitlines():
    key = line.partition('=')[0].strip()
    if re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]*', key):
        os.environ.pop(key, None)
backups = root / 'backups'
backups.mkdir(mode=0o700, exist_ok=True)
lock_file = (root / '.install.lock').open('a')
try:
    fcntl.flock(lock_file, fcntl.LOCK_EX | fcntl.LOCK_NB)
except BlockingIOError:
    raise SystemExit('Another install or upgrade is running.')

holder = None
# The install lock serializes owners of this identity, including after SIGKILL.
install_id = hashlib.sha256(str(root).encode()).hexdigest()
holder_name = 'sh-upgrade-lock-' + install_id[:32]
holder_label = 'app.simple-host.upgrade-install=' + install_id
db_compose_file, db_env_file = compose_file, env_file
scratch = tempfile.TemporaryDirectory(prefix='.upgrade-', dir=root)
work = Path(scratch.name)
attempt_dir = None

def normalize_release(value):
    # Release workflows stamp vX.Y.Z or hack-vX.Y.Z,
    # while image tags and wrapper targets omit those prefixes.
    return value.removeprefix('hack-').removeprefix('v')

def candidate_release():
    if not a.skip_pull:
        run(['docker', 'pull', a.image])
    a.image = json.loads(run(['docker', 'image', 'inspect', a.image]))[0]['Id']
    # Use the image's normal CLI dispatch, including Hack's wrapper. It needs
    # no installed configuration, data mounts or database connection.
    version = run(['docker', 'run', '--rm', '--network', 'none', a.image, 'version']).decode()
    match = re.search(r'(?<![A-Za-z0-9_.-])(?:hack-)?v?([0-9]+\.[0-9]+\.[0-9]+)(?![A-Za-z0-9_.-])', version)
    if not match:
        raise RuntimeError('The candidate image has no release version.')
    target = match[1]
    if a.to and normalize_release(a.to) != target:
        raise RuntimeError('The candidate image release differs from --to.')
    return target

def apply_overrides():
    if not a.set_env:
        return
    settings = dict(item.split('=', 1) for item in a.set_env)
    for key in settings:
        os.environ.pop(key, None)
    lines = env_file.read_text().splitlines()
    updated = [line for line in lines if line.partition('=')[0] not in settings]
    updated.extend(key + '=' + value for key, value in settings.items())
    publish(env_file, ('\n'.join(updated) + '\n').encode())

def run(args, *, data=None, output=None, source=None, upgrade_diagnostics=False):
    # Configuration, SQL credentials and copied data never reach the terminal.
    result = subprocess.run([str(x) for x in args], input=data, stdin=source,
                            stdout=output if output is not None else subprocess.PIPE,
                            stderr=subprocess.PIPE if upgrade_diagnostics else subprocess.DEVNULL)
    if result.returncode:
        if upgrade_diagnostics:
            # Only the compatibility instruction is safe to show. Docker,
            # configuration and database errors can contain credentials.
            refusal = re.search(rb'upgrade to ((?:hack-)?v?[0-9]+\.[0-9]+\.[0-9]+) first',
                                result.stderr or b'')
            if refusal:
                raise RuntimeError('Unsupported starting point; ' + refusal[0].decode())
        raise RuntimeError(Path(str(args[0])).name + ' failed (exit ' + str(result.returncode) + ')')
    return result.stdout or b''

def compose(*args, file=None, env=None, upgrade_diagnostics=False):
    return run(['docker', 'compose', '--project-directory', root, '--env-file', env or env_file,
                '-f', file or compose_file, '--profile', '*', *args],
               upgrade_diagnostics=upgrade_diagnostics)

def config(file=None, *, template=False):
    flags = ['--no-interpolate', '--no-env-resolution'] if template else []
    return json.loads(compose('config', '--format', 'json', *flags, file=file))

def inspect(cid):
    return json.loads(run(['docker', 'inspect', cid]))[0]

def containers(project):
    ids = run(['docker', 'ps', '-aq', '--filter', 'label=com.docker.compose.project=' + project]).decode().split()
    return [inspect(cid) for cid in ids]

def stop_all(project):
    # Include one-off release/migrate/upgrade containers, not just Compose's
    # service containers. A shared data lock then proves no server remains.
    for c in containers(project):
        service = c['Config']['Labels'].get('com.docker.compose.service')
        if service != 'db' and c['State']['Running']:
            run(['docker', 'stop', c['Id']])

def sql(query):
    return run_db('psql', query.encode())

def run_db(kind, data=None, output=None, source=None):
    commands = {
        'psql': 'exec psql -XAt -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"',
        'dump': 'exec pg_dump -Fc -U "$POSTGRES_USER" -d "$POSTGRES_DB"',
        'restore': 'exec pg_restore --exit-on-error --no-owner --no-privileges -U "$POSTGRES_USER" -d "$POSTGRES_DB"',
    }
    return run(['docker', 'compose', '--project-directory', root, '--env-file', db_env_file,
                '-f', db_compose_file, 'exec', '-T', 'db', 'sh', '-c', commands[kind]],
               data=data, output=output, source=source)

def release():
    if sql("SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='install_product' AND column_name='release')").strip() == b't':
        return sql('SELECT release FROM install_product WHERE "only"').decode().strip()
    return ''

def fingerprint(path):
    h = hashlib.sha256()
    with path.open('rb') as f:
        for block in iter(lambda: f.read(1024 * 1024), b''):
            h.update(block)
    return {'size': path.stat().st_size, 'sha256': h.hexdigest()}

def publish(path, data):
    # A new private inode also fixes permissions of older world-readable files.
    fd, name = tempfile.mkstemp(prefix='.' + path.name + '.', dir=path.parent)
    tmp = Path(name)
    try:
        with os.fdopen(fd, 'wb') as f:
            f.write(data)
            f.flush()
            os.fsync(f.fileno())
        tmp.replace(path)
        sync_dir(path.parent)
    finally:
        tmp.unlink(missing_ok=True)

def write_json(path, value):
    publish(path, (json.dumps(value, indent=2) + '\n').encode())

def sync_dir(path):
    fd = os.open(path, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)

def pointer(name, value):
    tmp = backups / (name + '.tmp')
    with tmp.open('w') as f:
        f.write(value + '\n')
        f.flush()
        os.fsync(f.fileno())
    tmp.replace(backups / name)
    sync_dir(backups)

def load_backup(pointer_name='CURRENT'):
    name = (backups / pointer_name).read_text().strip()
    if not re.fullmatch(r'upgrade-[A-Za-z0-9_.-]+', name):
        raise RuntimeError('Invalid backups/CURRENT.')
    path = backups / name
    if not (path / 'COMPLETE').is_file():
        return path, None
    m = json.loads((path / 'manifest.json').read_text())
    if m['attempt'] != name[len('upgrade-'):] or m['product'] != a.product:
        raise RuntimeError('CURRENT belongs to another attempt or product.')
    expected = {'database.dump', 'data.tar', 'env', 'compose.yaml', 'images.json'}
    if 'Caddyfile' in m['files']:
        expected.add('Caddyfile')
    if set(m['files']) != expected:
        raise RuntimeError('Incomplete backup manifest.')
    for file, checksum in m['files'].items():
        if fingerprint(path / file) != checksum:
            raise RuntimeError('Backup checksum mismatch: ' + file)
    # COMPLETE also binds the manifest itself, including the original images.
    if (path / 'COMPLETE').read_text().strip() != fingerprint(path / 'manifest.json')['sha256']:
        raise RuntimeError('Backup manifest checksum mismatch.')
    return path, m

def saved_images():
    saved = json.loads((attempt_dir / 'images.json').read_text())
    # Locally built images can have repository digests whose repository alias
    # disappears when its tag moves. Retain the actual container image IDs too.
    for service, image_id in saved.pop('x-upgrade-image-ids', {}).items():
        saved['services'][service]['image'] = image_id
    return saved

def take_lock(volume, tool_image):
    global holder
    data = '/volume/sites' if a.product == 'simple-hack' else '/volume'
    # Preserve the inode servers lock, and let the data owner open it read/write.
    command = ('set -eu; test -d "$1"; '
               'test -e "$1/.maintenance.lock" || (umask 022; touch "$1/.maintenance.lock"); '
               'chown "$(stat -c %u:%g "$1")" "$1/.maintenance.lock"; exec 9<"$1/.maintenance.lock"; '
               'flock -n -x 9; touch /tmp/locked; exec sleep 2147483647')
    holder = holder_name
    run(['docker', 'run', '-d', '--rm', '--name', holder_name, '--label', holder_label, '--network', 'none',
                  '-v', volume + ':/volume', '--entrypoint', 'sh', tool_image,
                  '-c', command, 'lock', data])
    for _ in range(50):
        state = inspect(holder)
        if not state['State']['Running']:
            break
        check = subprocess.run(['docker', 'exec', holder, 'test', '-f', '/tmp/locked'],
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        if check.returncode == 0:
            return
        time.sleep(.1)
    raise RuntimeError('The data directory is in use; stop every server and maintenance command first.')

def unlock():
    global holder
    if holder:
        subprocess.run(['docker', 'rm', '-f', holder], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        holder = None

def reclaim_holder():
    # Only a holder belonging to this install can be abandoned under our lock.
    for cid in run(['docker', 'ps', '-aq', '--filter', 'label=' + holder_label]).decode().split():
        run(['docker', 'rm', '-f', cid])

def interrupted(signum, frame):
    raise SystemExit(128 + signum)

for signum in (signal.SIGTERM, signal.SIGINT):
    signal.signal(signum, interrupted)

def ready(file):
    compose('up', '-d', '--no-deps', '--no-build', 'app', file=file)
    # Probe from the app's network namespace; no public ports or TLS assumptions.
    cid = compose('ps', '-q', 'app', file=file).decode().strip()
    settings = json.loads(file.read_text())['services']['app'].get('environment', {})
    port = '8080' if a.product == 'simple-hack' else str(settings.get('PORT', '8090'))
    for _ in range(60):
        result = subprocess.run(['docker', 'run', '--rm', '--network', 'container:' + cid,
                                 '--entrypoint', 'wget', tool_image, '-qO', '/dev/null',
                                 '-T', '2', 'http://127.0.0.1:' + port + '/readyz'],
                                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        if result.returncode == 0:
            return
        time.sleep(1)
    raise RuntimeError('The app did not answer /readyz; CURRENT is retained.')

def start(images, services):
    # An explicit, flattened config avoids rerunning release/migrate via depends_on.
    path = work / 'start.json'
    write_json(path, images)
    try:
        ready(path)
        others = [s for s in services if s not in ('app', 'db', 'release', 'migrate')]
        if others:
            # Atomic Caddyfile publication replaces its inode. Recreate web so
            # its file bind mount sees the published (or restored) file.
            if 'web' in others and caddy_file.is_file():
                compose('up', '-d', '--no-deps', '--no-build', '--force-recreate', 'web', file=path)
                others.remove('web')
            if others:
                compose('up', '-d', '--no-deps', '--no-build', *others, file=path)
    except BaseException:
        # CURRENT still describes an open attempt. Recovery requires all its
        # servers stopped, even when only part of startup succeeded.
        stop_all(images['name'])
        raise

def close_attempt():
    (backups / 'CURRENT').replace(backups / 'LAST')
    sync_dir(backups)

try:
    reclaim_holder()
    if not a.restore:
        a.to = candidate_release()
    current = backups / 'CURRENT'
    manifest = None
    if current.exists():
        attempt_dir, manifest = load_backup()
    elif a.restore and (backups / 'LAST').exists():
        attempt_dir, manifest = load_backup('LAST')
        if manifest is not None:
            pointer('CURRENT', attempt_dir.name)
    if a.restore and manifest is None:
        raise RuntimeError('--restore requires CURRENT with a complete, verified backup.')
    if manifest and not a.restore and normalize_release(manifest['to']) != normalize_release(a.to):
        raise RuntimeError('CURRENT names another target version; restore or finish that attempt first.')
    if a.restore:
        # Restore must also work if the installed configuration is unreadable.
        old = saved_images()
        db_compose_file, db_env_file = work / 'restore.json', attempt_dir / 'env'
        write_json(db_compose_file, old)
    else:
        old = config()
    project = old['name']
    known = containers(project)
    if a.restore:
        stop_all(project)
    recreated = not any(c['Config']['Labels'].get('com.docker.compose.service') == 'app'
                        and c['Config']['Labels'].get('com.docker.compose.oneoff', '').lower() != 'true'
                        for c in known)
    if recreated:
        # compose down removes containers, but keeps the installation volumes.
        # Recreate from the installed configuration without starting a writer.
        defaults = [s for s, c in old['services'].items() if not c.get('profiles')]
        compose('create', '--no-build', *defaults, file=db_compose_file, env=db_env_file)
    if a.restore or recreated or not any(c['Config']['Labels'].get('com.docker.compose.service') == 'db'
                            and c['State']['Running'] for c in known):
        compose('up', '-d', '--no-deps', '--no-build', '--wait', 'db', file=db_compose_file, env=db_env_file)
        known = containers(project)
    app = next((c for c in known if c['Config']['Labels'].get('com.docker.compose.service') == 'app'
                and c['Config']['Labels'].get('com.docker.compose.oneoff', '').lower() != 'true'), None)
    db = next((c for c in known if c['Config']['Labels'].get('com.docker.compose.service') == 'db'), None)
    if not app or not db or not db['State']['Running']:
        raise RuntimeError('An installed app container and a running database are required.')
    tool_image = db['Image']
    destination = '/data' if a.product == 'simple-hack' else '/data/sites'
    mounts = [m for m in app['Mounts'] if m['Destination'] == destination]
    if len(mounts) != 1 or mounts[0]['Type'] != 'volume':
        raise RuntimeError('Expected the install data named volume at ' + destination)
    volume = mounts[0]['Name']
    if manifest and manifest['volume'] != volume:
        raise RuntimeError('CURRENT belongs to another data volume.')
    # Capture the actually installed digests, before stopping anything.
    services = []
    for c in known:
        service = c['Config']['Labels'].get('com.docker.compose.service')
        if service in old['services'] and c['Config']['Labels'].get('com.docker.compose.oneoff', '').lower() != 'true':
            image = json.loads(run(['docker', 'image', 'inspect', c['Image']]))[0]
            old['services'][service]['image'] = next(iter(image.get('RepoDigests') or []), c['Image'])
            old.setdefault('x-upgrade-image-ids', {})[service] = c['Image']
            if c['State']['Running'] or recreated:
                services.append(service)
    if 'app' not in services:
        services.append('app')  # retries have a stopped app
    from_version = release()
    if manifest:
        if from_version and normalize_release(from_version) not in (
                normalize_release(manifest['from']), normalize_release(manifest['to'])):
            raise RuntimeError('CURRENT names another starting version.')
        from_version = manifest['from']
    elif not from_version:
        version = run(['docker', 'run', '--rm', '--network', 'none', '--entrypoint',
                       '/usr/local/bin/simple-host', app['Image'], 'version']).decode()
        match = re.search(r'(?<![0-9])(?:hack-)?v?([0-9]+\.[0-9]+\.[0-9]+)(?![0-9])', version)
        if not match:
            raise RuntimeError('The old image has no release version; cannot name the starting point.')
        from_version = match[1]
    if manifest:
        services = manifest['services']
    else:
        if attempt_dir:
            previous = attempt_dir if attempt_dir.exists() else attempt_dir.with_name(attempt_dir.name + '-incomplete')
            preparation = json.loads((previous / 'preparation.json').read_text())
            if (preparation['product'], normalize_release(preparation['from']),
                    normalize_release(preparation['to']), preparation['volume']) != (
                    a.product, normalize_release(from_version), normalize_release(a.to), volume):
                raise RuntimeError('Incomplete backup belongs to another upgrade.')
            services = preparation['services']
            old = preparation['images']
            # CURRENT may still point at the old name after a kill here.
            if attempt_dir.exists():
                attempt_dir.rename(attempt_dir.with_name(attempt_dir.name + '-incomplete'))
                sync_dir(backups)
        stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')
        attempt = a.product + '-' + from_version + '-' + a.to + '-' + stamp
        if not re.fullmatch(r'[A-Za-z0-9_.-]+', attempt):
            raise RuntimeError('Invalid starting release name.')
        attempt_dir = backups / ('upgrade-' + attempt)
        attempt_dir.mkdir(mode=0o700)
        preparation = {'product': a.product, 'from': from_version, 'to': a.to,
                       'volume': volume, 'services': services, 'images': old}
        # Save the restart set before stopping anything, including on lock failure.
        write_json(attempt_dir / 'preparation.json', preparation)
        pointer('CURRENT', attempt_dir.name)
    stop_all(project)
    take_lock(volume, tool_image)
    if a.restore:
        # Use the old DB container/configuration until its dump is restored.
        sql("DO $$DECLARE s record; BEGIN FOR s IN SELECT nspname FROM pg_namespace WHERE nspname NOT LIKE 'pg_%' AND nspname <> 'information_schema' LOOP EXECUTE format('DROP SCHEMA %I CASCADE', s.nspname); END LOOP; END$$; CREATE SCHEMA public;")
        with (attempt_dir / 'database.dump').open('rb') as f:
            run_db('restore', source=f)
        # Keep the maintenance-lock inode held across clearing and extraction.
        keep = 'sites' if a.product == 'simple-hack' else '.maintenance.lock'
        run(['docker', 'exec', holder, 'sh', '-c',
             'find /volume -mindepth 1 -maxdepth 1 ! -name "$1" -exec rm -rf {} +; '
             'if [ "$1" = sites ]; then find /volume/sites -mindepth 1 -maxdepth 1 ! -name .maintenance.lock -exec rm -rf {} +; fi',
             'clear', keep])
        with (attempt_dir / 'data.tar').open('rb') as f:
            run(['docker', 'exec', '-i', holder, 'tar', '-xpf', '-', '-C', '/volume'], source=f)
        publish(env_file, (attempt_dir / 'env').read_bytes())
        publish(compose_file, (attempt_dir / 'compose.yaml').read_bytes())
        if 'Caddyfile' in manifest['files']:
            publish(caddy_file, (attempt_dir / 'Caddyfile').read_bytes())
        # Start from the saved immutable image IDs, not the old mutable tags.
        restored = saved_images()
        unlock()
        start(restored, manifest['services'])
        close_attempt()
        print('Restored ' + manifest['attempt'] + '; backup retained in backups/LAST.')
    else:
        if manifest is None:
            with (attempt_dir / 'database.dump').open('wb') as f:
                run_db('dump', output=f)
            with (attempt_dir / 'data.tar').open('wb') as f:
                run(['docker', 'exec', holder, 'tar', '--exclude=./.maintenance.lock',
                     '--exclude=./sites/.maintenance.lock', '-cpf', '-', '-C', '/volume', '.'], output=f)
            publish(attempt_dir / 'env', env_file.read_bytes())
            publish(attempt_dir / 'compose.yaml', compose_file.read_bytes())
            if caddy_file.is_file():
                publish(attempt_dir / 'Caddyfile', caddy_file.read_bytes())
            write_json(attempt_dir / 'images.json', old)
            identity_path = '/volume/sites/.install-id' if a.product == 'simple-hack' else '/volume/.install-id'
            identity = run(['docker', 'exec', holder, 'sh', '-c',
                            'if [ -f "$1" ]; then cat "$1"; fi', 'identity', identity_path]).decode().strip()
            files = ['database.dump', 'data.tar', 'env', 'compose.yaml', 'images.json']
            if (attempt_dir / 'Caddyfile').is_file():
                files.append('Caddyfile')
            manifest = {'attempt': attempt, 'product': a.product, 'from': from_version, 'to': a.to,
                        'install_identity': identity, 'volume': volume, 'services': services,
                        'images': {s: c['image'] for s, c in old['services'].items() if 'image' in c},
                        'files': {name: fingerprint(attempt_dir / name) for name in files}}
            write_json(attempt_dir / 'manifest.json', manifest)
            for file in attempt_dir.iterdir():
                with file.open('rb') as f:
                    os.fsync(f.fileno())
            publish(attempt_dir / 'COMPLETE',
                    (fingerprint(attempt_dir / 'manifest.json')['sha256'] + '\n').encode())
        apply_overrides()
        template = config(candidate, template=True)
        new = config(candidate)
        if new['name'] != project:
            raise RuntimeError('Candidate Compose project differs from the install.')
        for service in ('app', 'static', 'migrate', 'release'):
            if service in new['services']:
                new['services'][service]['image'] = a.image
        # Preserve the running DB version; this procedure does not upgrade Postgres.
        new['services']['db']['image'] = tool_image
        for service in ('app', 'static', 'migrate', 'release', 'db'):
            if service in template['services']:
                template['services'][service]['image'] = new['services'][service]['image']
        target_file = work / 'candidate.json'
        write_json(target_file, new)
        # Release the backup holder; the command takes this same lock itself.
        # No service is started between these two exclusive holders.
        data_path = '/volume/sites' if a.product == 'simple-hack' else '/volume'
        data_owner = run(['docker', 'exec', holder, 'stat', '-c', '%u:%g', data_path]).decode().strip()
        unlock()
        flags = ['upgrade', '--attempt', manifest['attempt'], '-record-product']
        for step in a.steps:
            flags += ['--with', step]
        compose('run', '--rm', '--no-deps', '-T', '--user', data_owner, 'app', *flags,
                file=target_file, upgrade_diagnostics=True)
        write_json(compose_file, template)
        if candidate_caddy:
            publish(caddy_file, candidate_caddy.read_bytes())
        start(new, manifest['services'])
        close_attempt()
        print('Upgraded to ' + a.to + '; original backup retained in backups/LAST.')
except (RuntimeError, OSError, ValueError, KeyError, StopIteration) as error:
    print('Upgrade stopped: ' + str(error), file=sys.stderr)
    # No config values or subprocess output are included in this command.
    import shlex
    command = ['bash', str(script)]
    command += ['--dir', str(root), '--product', a.product, '--restore']
    print('Restore with: ' + shlex.join(command), file=sys.stderr)
    sys.exit(1)
finally:
    unlock()
    scratch.cleanup()
PY
