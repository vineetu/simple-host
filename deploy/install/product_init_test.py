#!/usr/bin/env python3
"""Exercise initialization and Compose publish/restart on owned TEST_PG_URL databases."""
import contextlib
import json
import os
import re
import select
import socket
import socketserver
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
MODE = sys.argv[1]
BASE = os.environ.get('TEST_PG_URL')
if not BASE:
    print('SKIP: product initialization rehearsal needs TEST_PG_URL')
    sys.exit(0)

pg_address = urllib.parse.urlsplit(BASE)
if pg_address.port in (5432, 55412):
    raise SystemExit('Use the disposable TEST_PG_URL server, not port 5432 or 55412')


def run(args, **kwargs):
    result = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                            text=True, **kwargs)
    if result.returncode:
        # Arguments may contain database credentials.
        raise RuntimeError(Path(args[0]).name + ' failed: ' +
                           re.sub(r'postgres(?:ql)?://[^\s]+', '<database>', result.stderr[-2000:]))
    return result.stdout


def sql(url, statement):
    return run(['psql', url, '-XAt', '-v', 'ON_ERROR_STOP=1', '-c', statement]).strip()


@contextlib.contextmanager
def database():
    name = 'd3s6a_' + uuid.uuid4().hex
    admin = BASE + '/postgres?sslmode=disable'
    sql(admin, 'CREATE DATABASE ' + name)
    try:
        yield BASE + '/' + name + '?sslmode=disable'
    finally:
        sql(admin, 'DROP DATABASE ' + name + ' WITH (FORCE)')


with tempfile.TemporaryDirectory(prefix='d3s6a-init-') as tmp:
    work = Path(tmp)
    run(['go', 'build', '-o', str(work) + '/', './cmd/simple-host', './cmd/simple-hack'], cwd=ROOT)
    binary = work / ('simple-hack' if MODE in ('hack', 'chart') else 'simple-host')
    env = {k: v for k, v in os.environ.items() if k in ('PATH', 'HOME', 'GOCACHE', 'GOMODCACHE')}
    env.update(PATH=str(work) + ':' + env['PATH'], ADMIN_API_KEY='install-fixture-only',
               DATA_DIR=str(work / 'sites'), GEOIP_DIR=str(work / 'geo'),
               EVENTS='hosted' if MODE in ('hack', 'chart') else '')
    if MODE == 'root':
        # Execute the actual installer's database phase. Only Docker transport
        # is replaced: `compose run ... app` invokes the locally built program.
        source = (ROOT / 'deploy/install/install.sh').read_text()
        phase = source.split('say "updating the database"\n', 1)[1].split('say "waiting for the instance to answer"', 1)[0]
        docker = work / 'docker'
        docker.write_text('#!/bin/sh\nif [ "$1 $2" = "compose run" ]; then\n'
                          '  while [ "$1" != app ]; do shift; done\n  shift\n'
                          '  exec simple-host "$@"\nfi\n')
        docker.chmod(0o755)
        command = ['bash', '-c', phase]
        compose = (ROOT / 'compose.yaml').read_text()
        assert 'docker-entrypoint-initdb.d' not in compose
        services = yaml.safe_load(compose)['services']
        assert services['migrate']['command'] == ['migrate', '-init', '-record-product']
        assert services['app']['depends_on']['migrate']['condition'] == 'service_completed_successfully'
    elif MODE == 'chart':
        # Use the chart's actual startup setting, rather than replacing startup
        # with a migrate command. Both the empty and repeated starts must work.
        rendered = run(['helm', 'template', 'test', str(ROOT / 'deploy/kubernetes/simple-hack'),
                        '--set', 'host=hack.example.test', '--set', 'database.dsn=postgres://fixture'])
        assert 'MIGRATE_ON_START: "true"' in rendered
        assert 'args: ["migrate", "-init", "-record-product"]' in rendered
        command = [str(binary), 'migrate', '-init', '-record-product']
    elif MODE == 'hack':
        command = None
    else:
        raise ValueError('unknown entrypoint')

    def start(url):
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0))
            port = listener.getsockname()[1]
        child_env = dict(env, DB_DSN=url, MIGRATE_ON_START='true', PORT=str(port), BIND_ADDR='127.0.0.1')
        # No SITE_DOMAIN: setup mode starts no background delivery workers.
        with tempfile.TemporaryFile() as log:
            child = subprocess.Popen([str(binary)], env=child_env, stdout=log, stderr=log)
            try:
                for _ in range(300):
                    if child.poll() is not None:
                        raise RuntimeError('chart startup exited with code ' + str(child.returncode))
                    try:
                        with urllib.request.urlopen('http://127.0.0.1:' + str(port), timeout=0.2) as response:
                            if response.status == 200:
                                return
                    except (OSError, urllib.error.URLError):
                        time.sleep(0.1)
                raise RuntimeError('chart startup did not answer')
            finally:
                child.terminate()
                child.wait(timeout=30)

    for existing in (() if MODE == 'hack' else (False, True)):
        with database() as url:
            env['DB_DSN'] = url
            if existing:
                sql(url, (ROOT / 'host/db/schema.sql').read_text())
                if MODE in ('hack', 'chart'):
                    sql(url, (ROOT / 'products/hack/db/schema.sql').read_text())
                # An older install has tables but no product record.
                sql(url, 'DROP TABLE install_product')
            for _ in range(2):
                if command:
                    run(command, env=env, cwd=work)
                if MODE == 'chart':
                    start(url)
                assert sql(url, 'SELECT count(*) FROM install_product') == '1'
                assert sql(url, 'SELECT product FROM install_product') == ('simple-host' if MODE == 'root' else 'simple-hack')
            print('PASS: ' + MODE + (' existing install and re-run' if existing else ' fresh install and re-run'))

    if MODE == 'root':
        # Other Host release hooks and Coolify's published-image command use
        # the same locally built program against fresh and unrecorded installs.
        template = yaml.safe_load((ROOT / 'deploy/platforms/coolify/service-template.yaml').read_text())
        assert 'schema' not in template['services']
        hooks = [(name, ['sh', str(ROOT / 'deploy/platforms' / name / 'start.sh'), 'release'])
                 for name in ('railway', 'render', 'fly', 'coolify')]
        hooks.append(('coolify-template', [str(binary)] + template['services']['migrate']['command']))
        for name, hook in hooks:
            for existing in (False, True):
                with database() as url:
                    env['DB_DSN'] = url
                    if existing:
                        sql(url, (ROOT / 'host/db/schema.sql').read_text())
                        sql(url, 'DROP TABLE install_product')
                    for _ in range(2):
                        run(hook, env=env)
                        assert sql(url, 'SELECT product FROM install_product') == 'simple-host'
                    print('PASS: ' + name + (' adoption and rerun' if existing else ' fresh install and rerun'))
        # The instance setup has its own filesystem/systemd sandbox test. Run
        # its real database phase here without performing any host setup.
        source = (ROOT / 'deploy/hack/setup-instance.sh').read_text()
        phase = source.split('dsn=$(dsn_from_env)\n', 1)[1].split('\ninstall_units\n', 1)[0]
        for existing in (False, True):
            with database() as url:
                if existing:
                    sql(url, (ROOT / 'host/db/schema.sql').read_text())
                    sql(url, (ROOT / 'products/hack/db/schema.sql').read_text())
                    sql(url, 'DROP TABLE install_product')
                setup_env = dict(env, dsn=url, SIMPLE_HOST=str(work / 'simple-hack'))
                for _ in range(2):
                    run(['bash', '-ec', 'say() { :; }; die() { exit 1; }; ' + phase], env=setup_env)
                    assert sql(url, 'SELECT product FROM install_product') == 'simple-hack'
                print('PASS: setup-instance' + (' adoption and rerun' if existing else ' fresh install and rerun'))

    if MODE in ('root', 'hack'):
        # Exercise the real Compose services and entrypoints. Only the database
        # endpoint, published ports and persistent volumes are replaced: the
        # disposable server is supplied by TEST_PG_URL. A temporary Unix socket relays
        # to that server, including when it is bound to host loopback.
        class Relay(socketserver.BaseRequestHandler):
            def handle(self):
                with socket.create_connection((pg.hostname, pg.port)) as upstream:
                    peers = (self.request, upstream)
                    while True:
                        readable, _, _ = select.select(peers, [], [], 1)
                        for src in readable:
                            data = src.recv(65536)
                            if not data:
                                return
                            (upstream if src is self.request else self.request).sendall(data)

        (work / 'docker').unlink(missing_ok=True)
        docker_config = work / 'docker-config'
        docker_config.mkdir()
        docker_env = dict(os.environ, DOCKER_CONFIG=str(docker_config))
        pg = urllib.parse.urlsplit(BASE)
        tag = 'd3s6a-' + uuid.uuid4().hex
        host_image, hack_image = tag + ':host', tag + ':hack'
        images = [host_image]
        try:
            run(['docker', 'build', '-q', '-t', host_image, '--target', 'hack' if MODE == 'hack' else 'host', '.'], cwd=ROOT, env=docker_env)
            image = host_image
            if MODE == 'hack':
                version = run(['docker', 'run', '--rm', host_image, 'version'], env=docker_env)
                alias = run(['docker', 'run', '--rm', '--entrypoint', '/usr/local/bin/simple-host',
                             host_image, 'version'], env=docker_env)
                assert alias == version
                print('PASS: Hack base image retains the simple-host command alias')
                images.append(hack_image)
                run(['docker', 'build', '-q', '-t', hack_image, '-f',
                     'deploy/hack/standalone/Dockerfile', '--build-arg',
                     'SIMPLE_HOST_IMAGE=' + host_image, '.'], cwd=ROOT, env=docker_env)
                image = hack_image
            socket_dir = work / 'pg-socket'
            socket_dir.mkdir(mode=0o755)
            socket_path = socket_dir / ('.s.PGSQL.' + str(pg.port))
            with socketserver.ThreadingUnixStreamServer(str(socket_path), Relay) as relay:
                socket_path.chmod(0o777)
                relay.daemon_threads = True
                thread = threading.Thread(target=relay.serve_forever, daemon=True)
                thread.start()
                try:
                    # Frozen pre-D4-S7b files also run with the new images.
                    # Keep existing data/volumes across the second startup.
                    for legacy, existing in ((False, False), (False, True), (True, True)):
                        with database() as url:
                            if existing:
                                sql(url, (ROOT / 'host/db/schema.sql').read_text())
                                if MODE == 'hack':
                                    sql(url, (ROOT / 'products/hack/db/schema.sql').read_text())
                                sql(url, 'DROP TABLE install_product')
                            compose_env = dict(env, IMAGE=image, SIMPLE_HACK_IMAGE=image,
                                               DB_PASSWORD='fixture-only', PASSCODE_ENC_KEY='AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=',
                                               ANALYTICS_SALT='fixture-only', SITE_DOMAIN='hack.example.test' if MODE == 'hack' else 'host.example.test',
                                               CONTENT_HOST='sites.host.example.test', PUBLIC_BASE_URL='https://' + ('hack.example.test' if MODE == 'hack' else 'host.example.test'),
                                               TLS_MODE='internal', PASSWORD_SIGNIN='on', DOCKER_CONFIG=str(docker_config))
                            path = ROOT / ('compose.yaml' if MODE == 'root' else 'deploy/hack/standalone/compose.yaml')
                            if legacy:
                                path = ROOT / 'deploy/install/testdata' / ('pre-d4-s7b-host.yaml' if MODE == 'root' else 'pre-d4-s7b-hack.yaml')
                            config = json.loads(run(['docker', 'compose', '-f', str(path),
                                                     'config', '--format', 'json'], env=compose_env))
                            migration = 'migrate' if MODE == 'root' else 'release'
                            services = config['services']
                            # Retain the real app -> migration dependency and
                            # the environment produced by the published recipe.
                            services = {name: services[name] for name in ((migration, 'app', 'web') if MODE == 'root' else (migration, 'app'))}
                            services[migration].pop('depends_on', None)
                            services['app']['depends_on'].pop('db', None)
                            remote = urllib.parse.urlsplit(url)
                            credentials, marker, _ = remote.netloc.rpartition('@')
                            authority = (credentials + marker if marker else '') + ':' + str(pg.port)
                            container_url = urllib.parse.urlunsplit(remote._replace(
                                netloc=authority, query=remote.query + '&host=/test-pg&connect_timeout=5'))
                            for name, service in services.items():
                                service.pop('build', None)
                                service.pop('ports', None)
                                service.pop('volumes', None)
                                service['restart'] = 'no'
                                if name == 'web':
                                    continue
                                service['environment']['DB_DSN'] = container_url
                                # GeoIP needs no download for an install check.
                                service['environment']['GEOIP_DIR'] = '/tmp/geo'
                            if MODE == 'root':
                                services['app']['ports'] = ['127.0.0.1::8090']
                                services['app']['volumes'] = ['sites:/data/sites']
                                # Content files are served by the HTTPS virtual
                                # host; port 80 only proxies setup/API requests.
                                # Use Caddy's local CA instead of public ACME.
                                caddyfile = work / 'Caddyfile'
                                caddy_source = (ROOT / 'deploy/compose/Caddyfile').read_text()
                                assert caddy_source.count('\ttls {\n\t\ton_demand\n\t}') == 1
                                caddyfile.write_text(caddy_source.replace('\ttls {\n\t\ton_demand\n\t}',
                                                                          '\ttls {\n\t\ton_demand\n\t\tissuer internal\n\t}'))
                                services['web']['ports'] = ['127.0.0.1::443']
                                services['web']['volumes'] = [str(caddyfile) + ':/etc/caddy/Caddyfile:ro',
                                                               'sites:/data/sites:ro', 'caddy_logs:/var/log/caddy']
                                target_port = '8090'
                            else:
                                services['app']['ports'] = ['127.0.0.1::8080', '127.0.0.1::8443']
                                services['app']['volumes'] = ['data:/data']
                                target_port = '8080'
                            for name in (migration, 'app'):
                                services[name].setdefault('volumes', []).append(str(socket_dir) + ':/test-pg:ro')
                            fixture = work / 'compose-fixture.json'
                            fixture.write_text(json.dumps({'services': services, 'volumes': {'sites': {}, 'data': {}, 'caddy_logs': {}}}))
                            compose = ['docker', 'compose', '-p', tag, '-f', str(fixture)]
                            try:
                                for restart in (False, True):
                                    if restart:
                                        run(compose + ['restart', 'app'], env=compose_env)
                                    else:
                                        run(compose + ['up', '-d', '--no-build'], env=compose_env)
                                    address = run(compose + ['port', 'app', target_port], env=compose_env).strip()
                                    for attempt in range(300):
                                        try:
                                            with urllib.request.urlopen('http://' + address + '/readyz', timeout=0.5) as response:
                                                if response.status == 200:
                                                    break
                                        except (OSError, urllib.error.URLError):
                                            time.sleep(0.1)
                                    else:
                                        raise RuntimeError('Compose app did not become ready')
                                    states = [json.loads(line) for line in run(compose + ['ps', '-a', '--format', 'json'], env=compose_env).splitlines()]
                                    state = next(row for row in states if row['Service'] == migration)
                                    assert state['State'] == 'exited' and state['ExitCode'] == 0, state
                                    assert sql(url, 'SELECT count(*) FROM install_product') == '1'
                                    assert sql(url, 'SELECT product FROM install_product') == ('simple-host' if MODE == 'root' else 'simple-hack')
                                    public = run(compose + ['port', 'web' if MODE == 'root' else 'app',
                                                            '443' if MODE == 'root' else '8443'], env=compose_env).strip()
                                    domain = compose_env['SITE_DOMAIN']
                                    scheme, port = 'https', '443'

                                    def request(method, path, body=None, key=None, host=domain):
                                        args = ['curl', '--silent', '--show-error', '--fail-with-body', '--insecure',
                                                '--noproxy', '*', '--max-time', '20', '--connect-to',
                                                host + ':' + port + ':' + public, '-X', method]
                                        if key:
                                            args += ['-H', 'X-API-Key: ' + key]
                                        if body is not None:
                                            args += ['-H', 'Content-Type: application/json', '--data-binary', json.dumps(body)]
                                        return run(args + [scheme + '://' + host + path])

                                    if not restart:
                                        request('POST', '/v1/auth/password/signup',
                                                {'username': 'compose-builder', 'password': 'compose fixture password'})
                                    signed_in = json.loads(request('POST', '/v1/auth/password',
                                                                  {'login': 'compose-builder', 'password': 'compose fixture password'}))
                                    key = signed_in['api_key']
                                    if not restart:
                                        page = '<h1>Compose persistent site</h1>'
                                        if MODE == 'root':
                                            request('POST', '/v1/sites/compose-site/files', {'files': {'index.html': page}}, key)
                                            visitor_host = 'sites.host.example.test'
                                            visitor_path = '/' + signed_in['handle'] + '/compose-site/'
                                        else:
                                            request('POST', '/v1/hack/events', {
                                                'slug': 'compose-event', 'title': 'Compose fixture', 'organiser_name': 'Test Organiser',
                                                'organisation': 'Test', 'contact_email': 'compose@example.test',
                                                'purpose': 'Compose rehearsal', 'expected_participants': 10,
                                                'starts_at': '2027-06-01T10:00:00Z', 'ends_at': '2027-06-02T10:00:00Z', 'time_zone': 'UTC'}, key)
                                            request('PUT', '/v1/hack/events/compose-event/website/files?create=1',
                                                    {'files': {'index.html': page}}, key)
                                            visitor_host, visitor_path = 'compose-event.hack.example.test', '/'
                                    assert request('GET', visitor_path, host=visitor_host) == page
                                # Keep the installer's prior rerun coverage as
                                # well as the app restart/persistence check.
                                run(compose + ['run', '--rm', '--no-deps', migration], env=compose_env)
                                assert sql(url, 'SELECT count(*) FROM install_product') == '1'
                                assert request('GET', visitor_path, host=visitor_host) == page
                                print('PASS: Compose ' + MODE + (' old file/new image' if legacy else ' adoption' if existing else ' fresh install') +
                                      ', password sign-in, publish, persisted page after restart and migration rerun')
                            finally:
                                run(compose + ['down', '-v', '--remove-orphans'], env=compose_env)
                finally:
                    relay.shutdown()
                    thread.join()
        finally:
            for image in reversed(images):
                subprocess.run(['docker', 'image', 'rm', '-f', image], env=docker_env,
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
