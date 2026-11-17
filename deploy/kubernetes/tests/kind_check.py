"""Rehearse each chart on an owned cluster and a throwaway TEST_PG_URL DB."""
import base64
import datetime
import json
import os
from pathlib import Path
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

ROOT = Path(__file__).resolve().parents[3]
PRODUCT = sys.argv[1]
BASE = os.environ.get('TEST_PG_URL', '')
pg = urllib.parse.urlsplit(BASE)
if not pg.hostname or not pg.port or pg.port in (5432, 55412):
    raise SystemExit('FAIL: set TEST_PG_URL to the disposable server (not port 5432 or 55412)')


def run(args, **kwargs):
    result = subprocess.run(args, text=True, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, **kwargs)
    if result.returncode:
        # Tool arguments and Helm output can contain the test DSN or keys.
        raise RuntimeError(Path(args[0]).name + ' failed (exit ' + str(result.returncode) + ')')
    return result.stdout.strip()


def sql(url, statement):
    return run(['psql', '-d', url, '-XAt', '-v', 'ON_ERROR_STOP=1', '-c', statement])


class Relay(socketserver.BaseRequestHandler):
    def handle(self):
        with socket.create_connection((pg.hostname, pg.port)) as upstream:
            peers = (self.request, upstream)
            while True:
                readable, _, _ = select.select(peers, [], [], 1)
                for source in readable:
                    data = source.recv(65536)
                    if not data:
                        return
                    (upstream if source is self.request else self.request).sendall(data)


suffix = uuid.uuid4().hex[:12]
cluster = os.environ.get('KIND_CLUSTER', 'd4-s8-' + PRODUCT + '-' + suffix)
database = 'd4_s8_' + suffix
image = os.environ.get('IMAGE', '')
created_cluster = created_database = built_image = False
forward = None
with tempfile.TemporaryDirectory(prefix='d4-s8-kind-') as directory:
    work = Path(directory)
    # Keep kubectl and Helm away from the operator's kubeconfig and registry auth.
    env = dict(os.environ, KUBECONFIG=str(work / 'kubeconfig'))
    env['DOCKER_CONFIG'] = str(work / 'docker-config')
    Path(env['DOCKER_CONFIG']).mkdir()
    # Reach TEST_PG_URL through a socket mounted into this run's node. Pods
    # need not reach a host bridge address, which may be blocked by its firewall.
    socket_dir = work / 'pg-socket'
    socket_dir.mkdir(mode=0o755)
    socket_path = socket_dir / 'postgres'
    relay = socketserver.ThreadingUnixStreamServer(str(socket_path), Relay)
    socket_path.chmod(0o777)
    relay.daemon_threads = True
    thread = threading.Thread(target=relay.serve_forever, daemon=True)
    thread.start()
    try:
        # Refuse existing clusters: even KEEP runs never take over another fixture.
        existing = run(['kind', 'get', 'clusters'], env=env).splitlines()
        if cluster in existing:
            raise RuntimeError('KIND_CLUSTER already exists; choose a fresh disposable name')
        print('== ' + PRODUCT + ': local image and disposable cluster ==', flush=True)
        if not image:
            image = 'd4-s8-' + PRODUCT + ':' + suffix
            run(['docker', 'build', '--target', PRODUCT.removeprefix('simple-'), '-q', '-t', image,
                 '--build-arg', 'VERSION=v0.0.0-kind-check', '.'], cwd=ROOT, env=env)
            built_image = True
        created_cluster = True
        kind_config = work / 'kind.json'
        kind_config.write_text(json.dumps({
            'kind': 'Cluster', 'apiVersion': 'kind.x-k8s.io/v1alpha4',
            'nodes': [{'role': 'control-plane', 'extraMounts': [{
                'hostPath': str(socket_dir), 'containerPath': '/test-pg',
            }]}],
        }))
        run(['kind', 'create', 'cluster', '--name', cluster, '--config', str(kind_config),
             '--wait', '120s'], env=env)
        run(['kind', 'load', 'docker-image', image, '--name', cluster], env=env)
        print('  ok   image loaded into new cluster', flush=True)
        node = cluster + '-control-plane'
        run(['docker', 'cp', str(ROOT / 'deploy/kubernetes/tests/pg_relay.py'),
             node + ':/usr/local/bin/d4-s8-pg-relay.py'], env=env)
        run(['docker', 'exec', '-d', node, 'python3', '/usr/local/bin/d4-s8-pg-relay.py',
             '/test-pg/postgres', '15432'], env=env)
        node_info = json.loads(run(['kubectl', '--context', 'kind-' + cluster,
                                   'get', 'node', node, '-o', 'json'], env=env))
        node_address = next(address['address'] for address in node_info['status']['addresses']
                            if address['type'] == 'InternalIP')
        sql(urllib.parse.urlunsplit(pg._replace(path='/postgres')), 'CREATE DATABASE ' + database)
        created_database = True
        url = urllib.parse.urlunsplit(pg._replace(path='/' + database, query='sslmode=disable'))
        credentials, marker, _ = pg.netloc.rpartition('@')
        authority = (credentials + marker if marker else '') + node_address + ':15432'
        pod_url = urllib.parse.urlunsplit(pg._replace(netloc=authority, path='/' + database,
                                                     query='sslmode=disable'))
        domain = 'hack.test' if PRODUCT == 'simple-hack' else 'host.test'
        repository, tag = image.rsplit(':', 1)
        values = {
            'host': domain, 'image': {'repository': repository, 'tag': tag, 'pullPolicy': 'Never'},
            'database': {'dsn': pod_url}, 'ingress': {'enabled': False},
            'persistence': {'size': '1Gi'}, 'signIn': {'email': {'enabled': 'off'}},
            # No provider credentials and no external GeoIP downloads in a fixture.
            'extraConfig': {'GEOIP_DIR': '/tmp/geo'},
        }
        values_file = work / 'values.json'
        values_file.write_text(json.dumps(values))
        values_file.chmod(0o600)
        helm = ['helm', '--kube-context', 'kind-' + cluster]
        kubectl = ['kubectl', '--context', 'kind-' + cluster, '-n', PRODUCT]
        chart = str(ROOT / 'deploy/kubernetes' / PRODUCT)
        run(helm + ['install', PRODUCT, chart, '-n', PRODUCT, '--create-namespace',
                    '-f', str(values_file), '--wait', '--timeout', '5m'], env=env)
        print('  ok   Helm install and readiness', flush=True)
        assert sql(url, 'SELECT product FROM install_product') == PRODUCT
        status = run(kubectl + ['exec', 'deploy/' + PRODUCT, '--', '/usr/local/bin/' + PRODUCT,
                                'migrate', '-status'], env=env)
        assert status.splitlines()[-1] == '0 pending'
        secret = json.loads(run(kubectl + ['get', 'secret', PRODUCT + '-secrets', '-o', 'json'], env=env))
        admin = base64.b64decode(secret['data']['ADMIN_API_KEY']).decode()

        def start_forward():
            with socket.socket() as listener:
                listener.bind(('127.0.0.1', 0))
                port = listener.getsockname()[1]
            process = subprocess.Popen(kubectl + ['port-forward', 'svc/' + PRODUCT, str(port) + ':80'],
                                       env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            return process, 'http://127.0.0.1:' + str(port), port

        forward, base, port = start_forward()

        def request(method, path, body=None, key=None, host=domain):
            headers = {'Host': host, 'X-Forwarded-Proto': 'https'}
            if key:
                headers['X-API-Key'] = key
            data = None
            if body is not None:
                headers['Content-Type'] = 'application/json'
                data = json.dumps(body).encode()
            req = urllib.request.Request(base + path, data=data, headers=headers, method=method)
            with urllib.request.urlopen(req, timeout=10) as response:
                raw = response.read().decode()
                return json.loads(raw) if 'application/json' in response.headers.get('Content-Type', '') else raw

        def ready():
            for _ in range(120):
                try:
                    if request('GET', '/readyz') == {'status': 'ok'}:
                        return
                except (OSError, urllib.error.URLError):
                    time.sleep(1)
            raise RuntimeError('port-forwarded service did not become ready')

        ready()
        request('POST', '/v1/auth/password/signup', {'username': 'kind-builder', 'password': 'kind fixture password'})
        key = request('POST', '/v1/auth/password', {'login': 'kind-builder', 'password': 'kind fixture password'})['api_key']
        print('  ok   password sign-in', flush=True)
        page = '<h1>Published from Kubernetes</h1>'
        if PRODUCT == 'simple-host':
            request('POST', '/v1/sites/kind-site/files', {'files': {'index.html': page}}, key)
            handle = request('GET', '/v1/me', key=key)['handle']
            site_name, public_host, public_path = 'kind-site', 'kind-site.' + handle + '.' + domain, '/'
        else:
            now = datetime.datetime.now(datetime.timezone.utc)
            request('POST', '/v1/hack/events', {
                'slug': 'kind-check', 'title': 'Kind check', 'organiser_name': 'Check',
                'organisation': 'Kubernetes', 'contact_email': 'check@example.test',
                'purpose': 'Prove the chart', 'expected_participants': 3,
                'starts_at': now.isoformat(), 'ends_at': (now + datetime.timedelta(days=1)).isoformat(),
                'time_zone': 'UTC'}, admin)
            request('POST', '/v1/hack/events/kind-check/stage', {'stage': 'open'}, admin)
            join = request('GET', '/v1/hack/events/kind-check', key=admin)['organiser']['join_code']
            request('POST', '/v1/hack/join/' + join, {'display_name': 'Participant', 'accept_coc': True}, key)
            team = request('POST', '/v1/hack/events/kind-check/teams', {'name': 'Kind team'}, key)['slug']
            key = request('POST', '/v1/hack/events/kind-check/key', {}, key)['key']
            request('PUT', '/v1/sites/' + team + '/files?create=1', {'files': {'index.html': page}}, key)
            site_name, public_host, public_path = team, team + '.kind-check.' + domain, '/'
        assert request('GET', public_path, host=public_host) == page
        storage = '/v1/sites/' + site_name + '/storage'
        request('PUT', storage + '/resources/tasks', {'kind': 'sqlite'}, key)
        request('POST', storage + '/sqlite/tasks/schema', {'sql': 'CREATE TABLE tasks (id INTEGER PRIMARY KEY, title TEXT)'}, key)
        request('POST', storage + '/sqlite/tasks/execute', {'sql': 'INSERT INTO tasks VALUES (1, ?)', 'params': ['kept on claim']}, key)
        query = {'sql': 'SELECT id, title FROM tasks ORDER BY id', 'params': []}
        before = request('POST', storage + '/sqlite/tasks/query', query, key)
        assert 'kept on claim' in json.dumps(before)
        print('  ok   published site and SQLite row', flush=True)
        claim = json.loads(run(kubectl + ['get', 'pvc', PRODUCT + '-data', '-o', 'json'], env=env))
        pod = json.loads(run(kubectl + ['get', 'pods', '-l', 'app.kubernetes.io/instance=' + PRODUCT, '-o', 'json'], env=env))['items'][0]
        uid = pod['metadata']['uid']
        # Upgrade the actual release; reuse the PVC, change the pod template so
        # Recreate really replaces the pod. Same local image is intentional.
        run(helm + ['upgrade', PRODUCT, chart, '-n', PRODUCT, '-f', str(values_file),
                    '--set', 'podAnnotations.kind-check=upgraded', '--wait', '--timeout', '5m'], env=env)
        next_claim = json.loads(run(kubectl + ['get', 'pvc', PRODUCT + '-data', '-o', 'json'], env=env))
        assert claim['metadata']['uid'] == next_claim['metadata']['uid']
        assert claim['spec']['volumeName'] == next_claim['spec']['volumeName']
        next_pod = json.loads(run(kubectl + ['get', 'pods', '-l', 'app.kubernetes.io/instance=' + PRODUCT, '-o', 'json'], env=env))['items'][0]
        assert next_pod['metadata']['uid'] != uid
        forward.terminate()
        forward.wait(timeout=15)
        forward, base, port = start_forward()
        ready()
        next_secret = json.loads(run(kubectl + ['get', 'secret', PRODUCT + '-secrets', '-o', 'json'], env=env))
        for name in ('ADMIN_API_KEY', 'PASSCODE_ENC_KEY', 'ANALYTICS_SALT'):
            assert secret['data'][name] == next_secret['data'][name]
        assert request('GET', public_path, host=public_host) == page
        assert request('POST', storage + '/sqlite/tasks/query', query, key) == before
        request('GET', '/v1/sites', key=key)
        if PRODUCT == 'simple-hack':
            assert request('GET', '/v1/hack/events/kind-check', key=admin)['event']['title'] == 'Kind check'
        print('  ok   Helm upgrade replaced pod, kept claim/PV and secrets; site and SQLite data survive', flush=True)
    finally:
        if forward:
            forward.terminate()
            forward.wait(timeout=15)
        if created_cluster and os.environ.get('KEEP') != '1':
            run(['kind', 'delete', 'cluster', '--name', cluster], env=env)
        relay.shutdown()
        thread.join()
        relay.server_close()
        if created_database:
            sql(urllib.parse.urlunsplit(pg._replace(path='/postgres')), 'DROP DATABASE ' + database + ' WITH (FORCE)')
        if built_image and os.environ.get('KEEP') != '1':
            run(['docker', 'image', 'rm', image], env=env)
