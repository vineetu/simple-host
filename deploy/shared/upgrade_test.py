#!/usr/bin/env python3
"""Real Compose backup/restore rehearsal, against TEST_PG_URL only.

The old servers are built from v0.7.22 and hack-v0.8.20. A fixture substitutes for the
D4-S10b upgrade command, so failures can be injected without adding that slice.
Only this run's project, containers, images, volumes and database are removed.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import signal
import shutil
import socket
import subprocess
import tarfile
import tempfile
import time
import unittest
from urllib.parse import urlsplit, unquote

ROOT = Path(__file__).resolve().parents[2]
SERVER = urlsplit(os.environ.get('TEST_PG_URL', ''))
if SERVER.scheme not in ('postgres', 'postgresql') or SERVER.path or SERVER.query or not SERVER.hostname or SERVER.port in (None, 5432, 55412):
    raise SystemExit('Set TEST_PG_URL to a bare disposable server URL, with a port other than 5432 or 55412.')
PG = dict(os.environ, PGHOST=SERVER.hostname, PGPORT=str(SERVER.port),
          PGUSER=unquote(SERVER.username or 'postgres'), PGPASSWORD=unquote(SERVER.password or ''), PGSSLMODE='disable')


def run(args, data=None, env=None, ok=True):
    r = subprocess.run([str(x) for x in args], input=data, env=env,
                       stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if ok and r.returncode:
        # Command arguments and stderr can contain test credentials.
        raise RuntimeError(str(args[0]) + ' failed: exit ' + str(r.returncode))
    return r


class UpgradeRehearsal(unittest.TestCase):
    product = 'simple-host'
    tag = 'v0.7.22'

    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory(prefix='d4s10a-')
        cls.base = Path(cls.temp.name)
        # Docker's client state belongs to this disposable fixture too.
        os.environ['DOCKER_CONFIG'] = str(cls.base / 'docker-config')
        (cls.base / 'docker-config').mkdir()
        cls.project = 'd4s10a-' + secrets.token_hex(6)
        cls.database = 'd4s10a_' + secrets.token_hex(6)
        cls.image = cls.project + ':fixture'
        cls.pg_owned = False
        cls.image_owned = False
        cls.extra = None
        cls.addClassCleanup(cls.cleanup)
        run(['psql', '-XAt', '-v', 'ON_ERROR_STOP=1', '-d', 'postgres'],
            ('CREATE DATABASE "' + cls.database + '"').encode(), PG)
        cls.pg_owned = True
        cls.pg = dict(PG, PGDATABASE=cls.database)
        schema = run(['git', 'show', cls.tag + ':db/schema.sql']).stdout
        run(['psql', '-Xq', '-v', 'ON_ERROR_STOP=1'], schema, cls.pg)
        cls.source = cls.base / 'old-source'
        cls.source.mkdir()
        archive = cls.base / 'source.tar'
        archive.write_bytes(run(['git', 'archive', cls.tag]).stdout)
        with tarfile.open(archive) as t:
            t.extractall(cls.source, filter='data')
        binary = cls.base / 'simple-host'
        result = subprocess.run(['go', 'build', '-buildvcs=false', '-ldflags',
                                 '-X github.com/vsriram/simple-host/internal/buildinfo.Version=' + cls.tag,
                                 '-o', str(binary), './cmd/server'], cwd=cls.source, env=dict(os.environ, CGO_ENABLED='0'),
                                stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        if result.returncode:
            raise RuntimeError('building the tagged old server failed: ' + result.stderr.decode())
        shutil.rmtree(cls.source)
        archive.unlink()
        # Run the actual Go compatibility check through the wrapper's command
        # boundary. The upgrade runner itself belongs to the next slice.
        (cls.base / 'floor.go').write_text('''package main
import (
 "context"
 "database/sql"
 "fmt"
 "os"
 _ "github.com/lib/pq"
 "github.com/vsriram/simple-host/host/upgrade"
)
func main() {
 db, err := sql.Open("postgres", os.Getenv("DB_DSN")); if err != nil { panic(err) }; defer db.Close()
 if err = upgrade.Check(context.Background(), db, os.Getenv("UPGRADE_PRODUCT")); err != nil {
  fmt.Fprintln(os.Stderr, err); os.Exit(1)
 }
}
''')
        run(['go', 'build', '-o', cls.base / 'floor', cls.base / 'floor.go'],
            env=dict(os.environ, CGO_ENABLED='0'))
        (cls.base / 'entry.sh').write_text('''#!/bin/sh
set -eu
if [ "${1:-}" = version ]; then echo 'simple-host v0.9.0 (commit fixture)'; exit; fi
if [ "${1:-}" != upgrade ]; then
  if [ -e /control/fail-ready ]; then exec sleep 2147483647; fi
  exec /usr/local/bin/simple-host "$@"
fi
[ "$(id -u)" = 65532 ] || exit 74
[ "$(stat -c %u:%g /data/sites)" = "$(id -u):$(id -g)" ] || exit 74
exec 9<>/data/sites/.maintenance.lock
flock -n -x 9 || exit 73
if [ -e /control/unsupported ]; then
  echo 'fixture secret must not reach the operator' >&2
  exec /usr/local/bin/floor
fi
printf candidate > /data/sites/probe
printf owned > /data/sites/maintenance-created
psql -Xq -v ON_ERROR_STOP=1 <<'SQL'
UPDATE upgrade_probe SET value='candidate';
CREATE TABLE IF NOT EXISTS install_product ("only" boolean PRIMARY KEY DEFAULT true, product text, release text);
SQL
psql -Xq -v ON_ERROR_STOP=1 -v product="$UPGRADE_PRODUCT" -v release="$UPGRADE_RELEASE" <<'SQL'
INSERT INTO install_product (product, release) VALUES (:'product', :'release') ON CONFLICT ("only") DO UPDATE SET release=excluded.release;
SQL
if [ -e /control/fail-upgrade ]; then exit 42; fi
''')
        (cls.base / 'dump.sh').write_text('''#!/bin/sh
set -eu
if [ -e /control/fail-backup ]; then exit 43; fi
if [ -e /control/block-backup ]; then
  touch /control/backup-held
  while [ -e /control/block-backup ]; do sleep .1; done
fi
exec /usr/local/bin/pg_dump-real "$@"
''')
        (cls.base / 'Dockerfile').write_text('''FROM postgres:16-alpine
RUN cp /usr/local/bin/pg_dump /usr/local/bin/pg_dump-real
COPY simple-host /usr/local/bin/simple-host
COPY floor /usr/local/bin/floor
COPY entry.sh /entry.sh
COPY dump.sh /usr/local/bin/pg_dump
RUN chmod 755 /entry.sh /usr/local/bin/pg_dump
ENTRYPOINT ["/entry.sh"]
''')
        if cls.product == 'simple-hack':
            # Keep the real root entrypoint, its early CLI dispatch, its app
            # user and Caddy. Only the next slice's command is a fixture.
            shutil.copy(ROOT / 'deploy/hack/standalone/start.sh', cls.base / 'start-hack')
            shutil.copy(ROOT / 'deploy/hack/standalone/Caddyfile', cls.base / 'Caddyfile')
            dockerfile = (cls.base / 'Dockerfile').read_text()
            dockerfile = 'FROM caddy:2.11.4-alpine AS caddy\n' + dockerfile
            dockerfile += '''RUN apk add --no-cache su-exec socat && adduser -D -H -u 65532 app
COPY --from=caddy /usr/bin/caddy /usr/bin/caddy
COPY start-hack /usr/local/bin/start-hack
COPY Caddyfile /etc/caddy/Caddyfile
RUN chmod 755 /usr/local/bin/start-hack && ln -s /entry.sh /usr/local/bin/simple-hack
ENV XDG_DATA_HOME=/data/caddy XDG_CONFIG_HOME=/data/caddy-config
ENTRYPOINT ["/usr/local/bin/start-hack"]
'''
            (cls.base / 'Dockerfile').write_text(dockerfile)
        build = run(['docker', 'build', '-q', '-t', cls.image, cls.base], ok=False)
        if build.returncode:
            raise RuntimeError('test image build failed: ' + build.stderr.decode())
        cls.image_owned = True
        # Use the supplied server's host network, with a unique app port. No
        # Postgres container is started, and neither prohibited port is used.
        with socket.socket() as s:
            s.bind(('127.0.0.1', 0))
            cls.port = s.getsockname()[1]
        if cls.product == 'simple-hack':
            cls.port = 8090
        cls.install = cls.base / 'install'
        cls.install.mkdir()
        cls.control = cls.base / 'control'
        cls.control.mkdir()
        common = {'PGHOST': PG['PGHOST'], 'PGPORT': PG['PGPORT'], 'PGUSER': PG['PGUSER'],
                  'PGPASSWORD': PG['PGPASSWORD'], 'PGDATABASE': cls.database,
                  'POSTGRES_USER': PG['PGUSER'], 'POSTGRES_DB': cls.database}
        common.update(UPGRADE_PRODUCT=cls.product,
                      UPGRADE_RELEASE='hack-v0.9.0' if cls.product == 'simple-hack' else 'v0.9.0')
        dsn = SERVER._replace(path='/' + cls.database, query='sslmode=disable').geturl()
        cls.config = {'name': cls.project, 'services': {
            'db': {'image': cls.image, 'entrypoint': ['sleep', '2147483647'],
                   'network_mode': 'host', 'environment': common,
                   'volumes': [str(cls.control) + ':/control', 'db:/fixture-db']},
            'app': {'image': cls.image, 'network_mode': 'host', 'user': '65532:65532',
                    'environment': {**common, 'DB_DSN': dsn, 'ADMIN_API_KEY': '${ADMIN_API_KEY}',
                                    'SITE_DOMAIN': 'upgrade.example.test', 'CONTENT_HOST': 'sites.upgrade.example.test',
                                    'DATA_DIR': '/data/sites', 'BIND_ADDR': '127.0.0.1', 'PORT': str(cls.port)},
                    'volumes': ['sites:/data/sites', str(cls.control) + ':/control:ro']}
        }, 'volumes': {'sites': {}, 'db': {}}}
        if cls.product == 'simple-hack':
            del cls.config['services']['app']['user']
            # Keep Caddy's fixed ports private. A Unix socket in this fixture's
            # volume relays only to TEST_PG_URL from the db's host namespace.
            cls.config['services']['db']['entrypoint'] = [
                'socat', 'UNIX-LISTEN:/relay/.s.PGSQL.' + PG['PGPORT'] + ',mode=777,unlink-early,fork',
                'TCP:' + PG['PGHOST'] + ':' + PG['PGPORT']]
            cls.config['services']['db']['volumes'][-1] = 'db:/relay'
            cls.config['services']['db']['healthcheck'] = {
                'test': ['CMD', 'test', '-S', '/relay/.s.PGSQL.' + PG['PGPORT']],
                'interval': '1s', 'timeout': '1s', 'retries': 20}
            cls.config['services']['app']['depends_on'] = {'db': {'condition': 'service_healthy'}}
            app_env = cls.config['services']['app']['environment']
            userinfo = SERVER.netloc.rsplit('@', 1)[0] + '@' if '@' in SERVER.netloc else ''
            app_env['DB_DSN'] = SERVER._replace(netloc=userinfo, path='/' + cls.database,
                                              query='sslmode=disable&host=%2Frelay&port=' + PG['PGPORT']).geturl()
            app_env.update(PGHOST='/relay')
            del cls.config['services']['app']['network_mode']
            cls.config['services']['app']['environment'].update(
                EVENTS='hosted', PASSWORD_SIGNIN='on', TLS_MODE='internal')
            cls.config['services']['app']['volumes'][0] = 'sites:/data'
            cls.config['services']['app']['volumes'].append('db:/relay')
        for service in ('web', 'static'):
            cls.config['services'][service] = {'image': cls.image, 'entrypoint': ['sleep', '2147483647']}
        if cls.product == 'simple-host':
            with socket.socket() as s:
                s.bind(('127.0.0.1', 0))
                cls.web_port = s.getsockname()[1]
            with socket.socket() as s:
                s.bind(('127.0.0.1', 0))
                cls.setup_port = s.getsockname()[1]
            cls.caddy = cls.install / 'deploy/compose/Caddyfile'
            cls.caddy.parent.mkdir(parents=True)

            def local_caddy(text):
                # Only listeners/upstream/log location change; all routing is
                # the tagged or shipped configuration, as in Caddyfile_test.sh.
                text = re.sub(r'https:// \{\n\ttls \{\n\t\ton_demand\n\t\}\n',
                              ':' + str(cls.web_port) + ' {\n', text, count=1)
                return text.replace(':80 {', ':' + str(cls.setup_port) + ' {').replace(
                    'app:8090', '127.0.0.1:' + str(cls.port)).replace('/var/log/caddy/access.log', '/tmp/access.log')

            cls.original_caddy = local_caddy(run(['git', 'show', cls.tag + ':deploy/compose/Caddyfile']).stdout.decode()).encode()
            cls.caddy.write_bytes(cls.original_caddy)
            cls.candidate_caddy = cls.base / 'candidate-Caddyfile'
            cls.candidate_caddy.write_text(local_caddy((ROOT / 'deploy/compose/Caddyfile').read_text()))
            cls.config['services']['web'] = {
                'image': 'caddy:2.11.4-alpine', 'network_mode': 'host',
                'volumes': [str(cls.caddy) + ':/etc/caddy/Caddyfile:ro', 'sites:/data/sites:ro']}
            cls.config['services']['app']['environment'].update({
                'SITE_DOMAIN': '${SITE_DOMAIN:-upgrade.example.test}',
                'CONTENT_HOST': '${CONTENT_HOST:-sites.upgrade.example.test}',
                'MAX_ARCHIVE_MB': '${MAX_ARCHIVE_MB:-100}', 'KEEP_VERSIONS': '${KEEP_VERSIONS:-1}'})
        cls.config['services']['static']['profiles'] = ['static']
        for service in cls.config['services'].values():
            service['stop_grace_period'] = '1s'
        (cls.install / 'compose.yaml').write_text(json.dumps(cls.config))
        (cls.install / 'compose.yaml').chmod(0o644)
        (cls.install / '.env').write_text('ADMIN_API_KEY=' + secrets.token_hex(32) + '\n')
        cls.original_config = (cls.install / 'compose.yaml').read_bytes()
        cls.original_env = (cls.install / '.env').read_bytes()
        cls.candidate = cls.base / 'candidate.json'
        cls.candidate.write_text(json.dumps(cls.config))
        cls.sql("CREATE TABLE upgrade_probe (value text); INSERT INTO upgrade_probe VALUES ('original')")
        # Supported Host images run as distroless nonroot, with their data
        # directory owned by that uid. Neither old release creates the lock.
        mount = '/data' if cls.product == 'simple-hack' else '/data/sites'
        run(['docker', 'run', '--rm', '-v', cls.project + '_sites:' + mount,
             '--entrypoint', 'sh', cls.image, '-c',
             'mkdir -p /data/sites /data/logs /data/caddy /data/caddy-config; chown -R 65532:65532 /data'])
        cls.compose('up', '-d', 'db', 'app', 'web', 'static')
        cid = cls.compose('ps', '-q', 'app').decode().strip()
        cls.original_image_id = json.loads(run(['docker', 'inspect', cid]).stdout)[0]['Image']
        cls.compose('exec', '-T', '--user', '65532:65532', 'app', 'sh', '-c', 'printf original > /data/sites/probe')
        if cls.product == 'simple-host':
            cls.compose('exec', '-T', 'app', 'sh', '-c',
                        'mkdir -p /data/sites/handles/alice/live/current /data/sites/handles/alice/rules/current; '
                        'printf "PLAIN FAQ" > /data/sites/handles/alice/live/current/faq.html; '
                        'printf "RULES DISK PAGE" > /data/sites/handles/alice/rules/current/index.html; '
                        'touch /data/sites/handles/alice/rules/redirects')

    @classmethod
    def sql(cls, query):
        return run(['psql', '-XAt', '-v', 'ON_ERROR_STOP=1'], query.encode(), cls.pg).stdout.decode().strip()

    @classmethod
    def compose(cls, *args):
        return run(['docker', 'compose', '--project-directory', cls.install, '--env-file', cls.install / '.env',
                    '-f', cls.install / 'compose.yaml', '--profile', 'static', *args]).stdout

    @classmethod
    def cleanup(cls):
        if cls.extra:
            run(['docker', 'rm', '-f', cls.extra], ok=False)
        if hasattr(cls, 'install'):
            label = 'app.simple-host.upgrade-install=' + hashlib.sha256(str(cls.install.resolve()).encode()).hexdigest()
            for cid in run(['docker', 'ps', '-aq', '--filter', 'label=' + label], ok=False).stdout.decode().split():
                run(['docker', 'rm', '-f', cid], ok=False)
            run(['docker', 'compose', '-f', cls.install / 'compose.yaml', '--profile', '*',
                 'down', '-v', '--remove-orphans'], ok=False)
        if cls.image_owned:
            run(['docker', 'image', 'rm', cls.image], ok=False)
        if cls.pg_owned:
            run(['psql', '-Xq', '-d', 'postgres'], ('DROP DATABASE "' + cls.database + '" WITH (FORCE)').encode(), PG, ok=False)
        cls.temp.cleanup()

    def upgrade_args(self, *flags, script=None):
        return ['bash', script or ROOT / 'deploy/shared/upgrade.sh', '--dir', self.install,
                 '--product', self.product, '--to', '0.9.0', '--image', self.image,
                 '--compose', self.candidate, '--skip-pull', *flags]

    def upgrade(self, *flags, success=False, script=None):
        r = run(self.upgrade_args(*flags, script=script), ok=False)
        if success:
            self.assertEqual(r.returncode, 0, r.stderr.decode())
        else:
            self.assertNotEqual(r.returncode, 0)
        return r

    def fault_script(self, name, before):
        # Kill the actual Python script at this filesystem boundary. No test
        # switches or failure hooks are added to the distributed script.
        source = (ROOT / 'deploy/shared/upgrade.sh').read_text()
        self.assertEqual(source.count(before), 1)
        script = self.base / name
        script.write_text(source.replace(before, before.split(before.lstrip())[0] +
                                        'os.kill(os.getpid(), signal.SIGKILL)\n' + before))
        return script

    def assert_services(self):
        for service in ('app', 'web', 'static'):
            cid = self.compose('ps', '-q', service).decode().strip()
            self.assertTrue(cid, service + ' was not restarted')
            self.assertTrue(json.loads(run(['docker', 'inspect', cid]).stdout)[0]['State']['Running'])

    def backup(self, pointer='CURRENT'):
        return self.install / 'backups' / (self.install / 'backups' / pointer).read_text().strip()

    def test_backup_retry_close_and_restore(self):
        # A shared server lock outside Compose's service set must prevent the
        # backup and any upgrade step even after Compose stops its own app.
        volume = self.project + '_sites'
        self.__class__.extra = run(['docker', 'run', '-d', '--rm', '-v', volume + ':/data/sites',
                                   '--entrypoint', 'sh', self.image, '-c',
                                   'exec 9>/data/sites/.maintenance.lock; flock -s 9; touch /tmp/locked; exec sleep 2147483647']).stdout.decode().strip()
        for _ in range(100):
            r = run(['docker', 'exec', self.extra, 'test', '-f', '/tmp/locked'], ok=False)
            if r.returncode == 0:
                break
            time.sleep(.1)
        lock_inode = run(['docker', 'exec', self.extra, 'stat', '-c', '%i', '/data/sites/.maintenance.lock']).stdout
        self.upgrade()
        self.assertEqual(run(['docker', 'exec', self.extra, 'stat', '-c', '%i', '/data/sites/.maintenance.lock']).stdout, lock_inode)
        self.assertFalse((self.backup() / 'COMPLETE').exists())
        self.assertEqual(self.sql('SELECT value FROM upgrade_probe'), 'original')
        run(['docker', 'rm', '-f', self.extra])
        self.__class__.extra = None
        # Old releases have no lock: the backup must create one writable by
        # the nonroot app, which opens it read/write in the fixture command.
        run(['docker', 'run', '--rm', '-v', volume + ':/volume', '--entrypoint',
             'rm', self.image, '/volume/.maintenance.lock'])
        # Interrupt the backup itself. It is quarantined, not treated as an
        # original backup of an attempt that may already have changed data.
        (self.control / 'fail-backup').touch()
        self.upgrade()
        incomplete = self.backup()
        self.assertFalse((incomplete / 'COMPLETE').exists())
        (self.control / 'fail-backup').unlink()
        (self.control / 'fail-upgrade').touch()
        quarantine_fault = self.fault_script('kill-quarantine.sh', '                sync_dir(backups)')
        result = self.upgrade(script=quarantine_fault)
        self.assertEqual(result.returncode, -signal.SIGKILL)
        self.assertEqual(self.backup(), incomplete)
        self.assertFalse(incomplete.exists())
        self.upgrade()
        self.assertTrue(list((self.install / 'backups').glob(incomplete.name + '-incomplete*')))
        original = self.backup()
        checksums = {f.name: hashlib.sha256(f.read_bytes()).hexdigest() for f in original.iterdir()}
        self.assertEqual(self.sql('SELECT value FROM upgrade_probe'), 'candidate')
        self.upgrade()
        self.assertEqual(original, self.backup())
        self.assertEqual(checksums, {f.name: hashlib.sha256(f.read_bytes()).hexdigest() for f in original.iterdir()})
        # Match --to to the new candidate so the refusal must come from
        # CURRENT's target guard, after the complete backup has been loaded.
        conflicting_image = self.project + ':conflicting-target'
        try:
            run(['docker', 'build', '-q', '-t', conflicting_image, '-'],
                ('FROM ' + self.image + '\n'
                 "RUN sed -i 's/v0.9.0/v0.10.0/' /entry.sh\n").encode())
            installed_data = self.sql('SELECT value, release FROM upgrade_probe CROSS JOIN install_product')
            data_archive = run(['docker', 'run', '--rm', '-v', volume + ':/data:ro',
                                '--entrypoint', 'tar', self.image, '-cf', '-', '-C', '/data', '.']).stdout
            r = self.upgrade('--to', '0.10.0', '--image', conflicting_image)
            self.assertIn('CURRENT names another target version', r.stderr.decode())
            self.assertEqual(self.sql('SELECT value, release FROM upgrade_probe CROSS JOIN install_product'), installed_data)
            self.assertEqual(run(['docker', 'run', '--rm', '-v', volume + ':/data:ro',
                                  '--entrypoint', 'tar', self.image, '-cf', '-', '-C', '/data', '.']).stdout, data_archive)
        finally:
            run(['docker', 'image', 'rm', conflicting_image], ok=False)
        self.assertEqual(original, self.backup())
        self.assertEqual(checksums, {f.name: hashlib.sha256(f.read_bytes()).hexdigest() for f in original.iterdir()})
        # Real dump and data-volume restore, then start the actual tagged server
        # by its saved immutable image ID and require /readyz before closing.
        self.upgrade('--restore', success=True)
        self.assertFalse((self.install / 'backups/CURRENT').exists())
        self.assertEqual(self.backup('LAST'), original)
        self.assert_services()
        self.assertEqual(self.sql('SELECT value FROM upgrade_probe'), 'original')
        self.assertEqual(self.compose('exec', '-T', 'app', 'cat', '/data/sites/probe').decode(), 'original')
        self.assertEqual((self.install / 'compose.yaml').read_bytes(), self.original_config)
        self.assertEqual((self.install / '.env').read_bytes(), self.original_env)
        old_digest = json.loads((original / 'manifest.json').read_text())['images']['app']
        # A repository digest and a container's image ID identify different
        # Docker objects. Resolve the saved reference before comparing IDs.
        saved_image_id = json.loads(run(['docker', 'image', 'inspect', old_digest]).stdout)[0]['Id']
        self.assertEqual(saved_image_id, self.original_image_id)
        cid = self.compose('ps', '-q', 'app').decode().strip()
        self.assertEqual(json.loads(run(['docker', 'inspect', cid]).stdout)[0]['Image'], self.original_image_id)
        # Both termination signals hit an actual running script while the
        # detached holder owns the data lock. SIGKILL requires next-run reclaim.
        for sig in (signal.SIGTERM, signal.SIGKILL):
            (self.control / 'backup-held').unlink(missing_ok=True)
            (self.control / 'block-backup').touch()
            with tempfile.TemporaryFile() as log:
                child = subprocess.Popen([str(x) for x in self.upgrade_args()], stdout=log, stderr=log)
                try:
                    for _ in range(300):
                        if (self.control / 'backup-held').exists():
                            break
                        self.assertIsNone(child.poll(), 'upgrade exited before backup')
                        time.sleep(.1)
                    else:
                        self.fail('backup did not acquire its lock')
                    child.send_signal(sig)
                    (self.control / 'block-backup').unlink()
                    child.wait(timeout=30)
                    self.assertNotEqual(child.returncode, 0)
                    label = 'app.simple-host.upgrade-install=' + hashlib.sha256(str(self.install.resolve()).encode()).hexdigest()
                    holders = run(['docker', 'ps', '-aq', '--filter', 'label=' + label]).stdout.strip()
                    self.assertEqual(bool(holders), sig == signal.SIGKILL,
                                     'graceful cleanup or abandoned-holder fixture failed')
                finally:
                    (self.control / 'block-backup').unlink(missing_ok=True)
                    if child.poll() is None:
                        child.kill()
                        child.wait()
            self.upgrade()  # reclaims any abandoned holder, then fails the data step
            self.assertTrue((self.backup() / 'COMPLETE').exists())
            self.upgrade('--restore', success=True)
            self.assert_services()
        (self.control / 'fail-upgrade').unlink()
        (self.install / 'compose.yaml').chmod(0o644)
        # Interrupt publication after the private file is fsynced and before
        # rename: the old 0644 file stays valid, and the real restore still runs.
        publication_fault = self.fault_script('kill-publication.sh', '        tmp.replace(path)')
        source = publication_fault.read_text().replace('        os.kill(os.getpid(), signal.SIGKILL)',
                                                      '        if path == compose_file: os.kill(os.getpid(), signal.SIGKILL)')
        publication_fault.write_text(source)
        self.assertEqual(self.upgrade(script=publication_fault).returncode, -signal.SIGKILL)
        self.assertEqual((self.install / 'compose.yaml').read_bytes(), self.original_config)
        self.upgrade('--restore', success=True)
        self.assert_services()
        # Two completed upgrades must have different attempts and original backups.
        (self.install / 'compose.yaml').chmod(0o644)
        self.upgrade(success=True)
        first = self.backup('LAST')
        self.assert_services()
        self.assertEqual((self.install / 'compose.yaml').stat().st_mode & 0o777, 0o600)
        self.assertEqual(json.loads((self.install / 'compose.yaml').read_text())['services']['app']['environment']['ADMIN_API_KEY'], '${ADMIN_API_KEY}')
        # Editing .env must still affect recreation after a completed upgrade.
        rotated = secrets.token_hex(32)
        (self.install / '.env').write_text('ADMIN_API_KEY=' + rotated + '\n')
        self.compose('up', '-d', '--no-deps', '--force-recreate', 'app')
        cid = self.compose('ps', '-q', 'app').decode().strip()
        environment = json.loads(run(['docker', 'inspect', cid]).stdout)[0]['Config']['Env']
        self.assertIn('ADMIN_API_KEY=' + rotated, environment)
        self.sql("UPDATE upgrade_probe SET value='between upgrades'")
        self.compose('exec', '-T', '--user', '65532:65532', 'app', 'sh', '-c', 'printf between > /data/sites/probe')
        self.upgrade(success=True)
        second = self.backup('LAST')
        self.assertNotEqual(first, second)
        self.assertTrue((first / 'COMPLETE').exists())
        # LAST is also restorable after a successfully closed attempt.
        self.upgrade('--restore', success=True)
        self.assertEqual(self.sql('SELECT value FROM upgrade_probe'), 'between upgrades')
        self.assertEqual(self.compose('exec', '-T', 'app', 'cat', '/data/sites/probe').decode(), 'between')

    def test_compatibility_refusal_through_wrapper(self):
        self.sql('CREATE TABLE IF NOT EXISTS install_product ("only" boolean PRIMARY KEY DEFAULT true, product text, release text); '
                 "INSERT INTO install_product (product, release) VALUES ('" + self.product + "', '') "
                 'ON CONFLICT ("only") DO UPDATE SET release=excluded.release')
        (self.control / 'unsupported').touch()
        try:
            result = self.upgrade()
            self.assertIn('upgrade to ' + self.tag + ' first', result.stderr.decode())
            self.assertNotIn('fixture secret', result.stderr.decode())
        finally:
            (self.control / 'unsupported').unlink()
        self.upgrade('--restore', success=True)

    def test_interrupted_complete_publication_retakes_backup(self):
        before = self.sql('SELECT value FROM upgrade_probe')
        fault = self.fault_script('kill-complete.sh', '        tmp.replace(path)')
        fault.write_text(fault.read_text().replace(
            '        os.kill(os.getpid(), signal.SIGKILL)',
            '        if path.name == "COMPLETE": os.kill(os.getpid(), signal.SIGKILL)'))
        self.assertEqual(self.upgrade(script=fault).returncode, -signal.SIGKILL)
        unfinished = self.backup()
        self.assertTrue((unfinished / 'manifest.json').exists())
        self.assertTrue(list(unfinished.glob('.COMPLETE.*')), 'did not interrupt before rename')
        self.assertFalse((unfinished / 'COMPLETE').exists())
        self.assertEqual(self.sql('SELECT value FROM upgrade_probe'), before)
        (self.control / 'fail-upgrade').touch()
        try:
            self.upgrade()
            replacement = self.backup()
            self.assertNotEqual(replacement, unfinished)
            quarantined = unfinished.with_name(unfinished.name + '-incomplete')
            self.assertTrue(quarantined.is_dir())
            self.assertFalse((quarantined / 'COMPLETE').exists())
            self.assertTrue((replacement / 'COMPLETE').exists())
            self.assertEqual(self.sql('SELECT value FROM upgrade_probe'), 'candidate')
        finally:
            (self.control / 'fail-upgrade').unlink()
        self.upgrade('--restore', success=True)
        self.assertEqual(self.sql('SELECT value FROM upgrade_probe'), before)

    def test_startup_failures_stop_every_server(self):
        self.compose('up', '-d', 'web', 'static')
        # Bound the fixture's readiness timeout without a production test flag.
        fast = self.base / 'short-readiness.sh'
        fast.write_text((ROOT / 'deploy/shared/upgrade.sh').read_text().replace(
            'for _ in range(60):', 'for _ in range(2):'))
        for failure in ('readiness', 'secondary-service'):
            with self.subTest(failure=failure):
                candidate = json.loads(self.candidate.read_text())
                if failure == 'readiness':
                    (self.control / 'fail-ready').touch()
                else:
                    candidate['services']['web']['entrypoint'] = ['/fixture-missing-command']
                    self.candidate.write_text(json.dumps(candidate))
                try:
                    result = self.upgrade(script=fast if failure == 'readiness' else None)
                    self.assertIn('did not answer /readyz' if failure == 'readiness' else 'docker failed',
                                  result.stderr.decode())
                    self.assertTrue((self.backup() / 'COMPLETE').exists())
                    for service in ('app', 'web', 'static'):
                        cid = self.compose('ps', '-aq', service).decode().strip()
                        self.assertFalse(json.loads(run(['docker', 'inspect', cid]).stdout)[0]['State']['Running'])
                finally:
                    (self.control / 'fail-ready').unlink(missing_ok=True)
                    self.candidate.write_text(json.dumps(self.config))
                self.upgrade('--restore', success=True)
                self.assert_services()

    def installer(self, *flags):
        # Execute the installer's real dispatch with local candidate downloads.
        dispatch = (ROOT / 'deploy/install/install.sh').read_text().split('if ! command -v docker', 1)[0]
        dispatch = dispatch.replace('DIR=/opt/simple-host', 'DIR=' + str(self.install))
        dispatch = dispatch.replace('project=simple-host', 'project=' + self.project)
        installer = self.base / 'install-dispatch.sh'
        installer.write_text(dispatch)
        mockbin = self.base / 'downloads'
        mockbin.mkdir(exist_ok=True)
        # The helper still runs real Docker and Postgres; only network downloads
        # and the registry pull are replaced by this run's local artifacts.
        (mockbin / 'curl').write_text('''#!/bin/bash
set -eu
case "$*" in
  *deploy/shared/upgrade.sh*) source=$FIXTURE_HELPER ;;
  *deploy/compose/Caddyfile*) source=$FIXTURE_CADDY ;;
  *) source=$FIXTURE_COMPOSE ;;
esac
while [ "$1" != -o ]; do shift; done
cp "$source" "$2"
''')
        (mockbin / 'curl').chmod(0o755)
        local_helper = self.base / 'local-helper.sh'
        local_helper.write_text((ROOT / 'deploy/shared/upgrade.sh').read_text().replace(
            "p.add_argument('--skip-pull', action='store_true')",
            "p.add_argument('--skip-pull', action='store_true', default=True)").replace(
            "for _ in range(60):", "for _ in range(2):"))
        return run(['bash', installer, '--image', self.image, '--ref', 'fixture-candidate', *flags], env=dict(
            os.environ, PATH=str(mockbin) + ':' + os.environ['PATH'],
            FIXTURE_HELPER=str(local_helper), FIXTURE_COMPOSE=str(self.candidate),
            FIXTURE_CADDY=str(self.candidate_caddy)), ok=False)

    def test_install_after_compose_down(self):
        if self.product != 'simple-host':
            self.skipTest('Host installer dispatch only; Hack invokes upgrade directly')
        self.compose('down')
        original = (self.install / '.env').read_bytes(), (self.install / 'compose.yaml').read_bytes()
        (self.control / 'fail-upgrade').touch()
        result = self.installer()
        (self.control / 'fail-upgrade').unlink()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Restore with:', result.stderr.decode())
        self.assertEqual(original, ((self.install / '.env').read_bytes(), (self.install / 'compose.yaml').read_bytes()))
        backup = self.backup()
        self.assertTrue((backup / 'COMPLETE').exists())
        self.assertEqual((backup / 'env').read_bytes(), original[0])
        self.assertEqual((backup / 'compose.yaml').read_bytes(), original[1])
        self.upgrade('--restore', success=True)
        for service in ('app', 'web'):
            cid = self.compose('ps', '-q', service).decode().strip()
            self.assertTrue(json.loads(run(['docker', 'inspect', cid]).stdout)[0]['State']['Running'])

    def web_get(self, path):
        for _ in range(50):
            result = run(['curl', '-sS', '--max-time', '2', '-H', 'Host: sites.upgrade.example.test',
                          '-w', '\n%{http_code}', 'http://127.0.0.1:' + str(self.web_port) + path], ok=False)
            if result.returncode == 0:
                body, status = result.stdout.decode().rsplit('\n', 1)
                return int(status), body
            time.sleep(.1)
        self.fail('Caddy did not answer')

    def test_installer_image_settings_and_web_routing_retry_restore(self):
        if self.product != 'simple-host':
            self.skipTest('Host installer and separate Caddy service only')
        self.compose('up', '-d', 'web', 'static')
        original_env = (self.install / '.env').read_bytes()
        original_caddy = self.caddy.read_bytes()
        original_compose = (self.install / 'compose.yaml').read_bytes()
        self.assertEqual(self.web_get('/alice/live/faq')[0], 404)
        self.assertEqual(self.web_get('/alice/rules/'), (200, 'RULES DISK PAGE'))
        overrides = ['--host', 'changed.example.test', '--content', 'pages.changed.example.test',
                     '--email', 'operator@example.test', '--max-site-mb', '250', '--keep-versions', '0']
        (self.control / 'fail-ready').touch()
        try:
            first = self.installer(*overrides)
            self.assertNotEqual(first.returncode, 0)
            self.assertIn('did not answer /readyz', first.stderr.decode())
            backup = self.backup()
            manifest = json.loads((backup / 'manifest.json').read_text())
            self.assertEqual(manifest['to'], '0.9.0', 'installer pin must not name the overridden image')
            self.assertEqual(self.sql('SELECT release FROM install_product'), 'v0.9.0')
            self.assertEqual((backup / 'env').read_bytes(), original_env)
            self.assertEqual((backup / 'Caddyfile').read_bytes(), original_caddy)
            self.assertIn('Caddyfile', manifest['files'])
            checksums = {f.name: hashlib.sha256(f.read_bytes()).hexdigest() for f in backup.iterdir()}
            # Retry after the actual candidate has recorded its release, with
            # unspecified settings preserved from the first run's overrides.
            retry = self.installer('--max-site-mb', '300')
            self.assertNotEqual(retry.returncode, 0)
            self.assertIn('did not answer /readyz', retry.stderr.decode())
            self.assertEqual(self.backup(), backup)
            self.assertEqual(checksums, {f.name: hashlib.sha256(f.read_bytes()).hexdigest() for f in backup.iterdir()})
            kept = dict(line.split('=', 1) for line in (self.install / '.env').read_text().splitlines())
            self.assertEqual(kept['SITE_DOMAIN'], 'changed.example.test')
            self.assertEqual(kept['CONTENT_HOST'], 'pages.changed.example.test')
            self.assertEqual(kept['ACME_EMAIL'], 'operator@example.test')
            self.assertEqual(kept['KEEP_VERSIONS'], '0')
            self.assertEqual(kept['MAX_ARCHIVE_MB'], '300')
        finally:
            (self.control / 'fail-ready').unlink()
        self.upgrade('--restore', success=True)
        self.assertEqual((self.install / '.env').read_bytes(), original_env)
        self.assertEqual(self.caddy.read_bytes(), original_caddy)
        self.assertEqual((self.install / 'compose.yaml').read_bytes(), original_compose)
        # Apply again to prove that the restored configuration and Caddy mount
        # can complete the upgrade, then restore the successful attempt too.
        completed = self.installer(*overrides, '--max-site-mb', '300')
        self.assertEqual(completed.returncode, 0, completed.stderr.decode())
        settings = dict(line.split('=', 1) for line in (self.install / '.env').read_text().splitlines())
        self.assertEqual(settings['SITE_DOMAIN'], 'changed.example.test')
        self.assertEqual(settings['SITE_ADDR'], 'changed.example.test')
        self.assertEqual(settings['PUBLIC_BASE_URL'], 'https://changed.example.test')
        self.assertEqual(settings['CONTENT_HOST'], 'pages.changed.example.test')
        self.assertEqual(settings['CONTENT_ADDR'], 'pages.changed.example.test')
        self.assertEqual(settings['ACME_EMAIL'], 'operator@example.test')
        self.assertEqual(settings['MAX_ARCHIVE_MB'], '300')
        self.assertEqual(settings['KEEP_VERSIONS'], '0')
        for key, value in (line.split('=', 1) for line in original_env.decode().splitlines()):
            self.assertEqual(settings[key], value, 'unspecified setting changed: ' + key)
        cid = self.compose('ps', '-q', 'app').decode().strip()
        environment = json.loads(run(['docker', 'inspect', cid]).stdout)[0]['Config']['Env']
        self.assertIn('SITE_DOMAIN=changed.example.test', environment)
        self.assertIn('MAX_ARCHIVE_MB=300', environment)
        self.assertEqual(self.caddy.read_bytes(), self.candidate_caddy.read_bytes())
        self.assertEqual(self.web_get('/alice/live/faq'), (200, 'PLAIN FAQ'))
        # The rules marker now reaches the real app's gate instead of serving
        # the disk page. This tagged server requires a sign-in at that gate.
        self.assertEqual(self.web_get('/alice/rules/')[0], 401)
        self.upgrade('--restore', success=True)
        self.assertEqual((self.install / '.env').read_bytes(), original_env)
        self.assertEqual((self.install / 'compose.yaml').read_bytes(), original_compose)
        self.assertEqual(self.caddy.read_bytes(), original_caddy)
        self.assertEqual(self.web_get('/alice/live/faq')[0], 404)
        self.assertEqual(self.web_get('/alice/rules/'), (200, 'RULES DISK PAGE'))
        self.assert_services()

    def test_restore_after_down_uses_saved_database_image(self):
        self.compose('up', '-d', 'web', 'static')
        before = self.sql('SELECT value FROM upgrade_probe')
        (self.control / 'fail-upgrade').touch()
        try:
            self.upgrade()
        finally:
            (self.control / 'fail-upgrade').unlink()
        backup = self.backup()
        saved = json.loads((backup / 'images.json').read_text())['services']['db']['image']
        expected = json.loads(run(['docker', 'image', 'inspect', saved]).stdout)[0]['Id']
        original_id = json.loads(run(['docker', 'image', 'inspect', self.image]).stdout)[0]['Id']
        self.compose('down')
        try:
            # Only this fixture's local tag moves. The new tag cannot even run
            # psql, so tag-based database recreation cannot pass this restore.
            run(['docker', 'tag', 'caddy:2.11.4-alpine', self.image])
            self.upgrade('--restore', success=True)
            cid = self.compose('ps', '-q', 'db').decode().strip()
            self.assertEqual(json.loads(run(['docker', 'inspect', cid]).stdout)[0]['Image'], expected)
            self.assertEqual(self.sql('SELECT value FROM upgrade_probe'), before)
            self.assert_services()
        finally:
            run(['docker', 'tag', original_id, self.image])


class HackUpgradeRehearsal(UpgradeRehearsal):
    product = 'simple-hack'
    tag = 'hack-v0.8.20'

    def test_backup_retry_close_and_restore(self):
        (self.control / 'fail-upgrade').touch()
        self.upgrade()
        original = self.backup()
        checksums = {f.name: hashlib.sha256(f.read_bytes()).hexdigest() for f in original.iterdir()}
        self.assertEqual(self.sql('SELECT release FROM install_product'), 'hack-v0.9.0')
        self.assertEqual(self.sql('SELECT value FROM upgrade_probe'), 'candidate')
        self.upgrade('--to', 'hack-v0.9.0')
        self.assertEqual(self.backup(), original)
        self.assertEqual(checksums, {f.name: hashlib.sha256(f.read_bytes()).hexdigest() for f in original.iterdir()})
        owner = run(['docker', 'run', '--rm', '-v', self.project + '_sites:/data',
                     '--entrypoint', 'stat', self.image, '-c', '%u:%g', '/data/sites/maintenance-created']).stdout
        self.assertEqual(owner.strip(), b'65532:65532')
        (self.control / 'fail-upgrade').unlink()
        self.upgrade('--restore', success=True)
        self.assertEqual(self.sql('SELECT value FROM upgrade_probe'), 'original')
        self.assert_services()
        # A completed upgrade records the workflow's hack-v prefix; LAST must
        # remain restorable when the wrapper supplies an unprefixed target.
        self.upgrade(success=True)
        self.assertEqual(self.sql('SELECT release FROM install_product'), 'hack-v0.9.0')
        self.upgrade('--restore', success=True)
        self.assertEqual(self.sql('SELECT value FROM upgrade_probe'), 'original')


if __name__ == '__main__':
    unittest.main()
