#!/usr/bin/env bash
# Rehearse on supplied, stopped-install copies; never opens the source database.
# TEST_PG_URL must name a disposable Postgres server (bare URL).
# Usage: scripts/kv-move-rehearsal.sh --program /path/to/program --product host|hack \
#          --dump /path/to/db.dump --data-dir /path/to/copied/sites --out /path/to/logs
set -eu
exec python3 - "$@" <<'PY'
import argparse
import concurrent.futures
import datetime
import contextlib
import hashlib
import io
import json
import math
import os
from pathlib import Path
import secrets
import shutil
import signal
import socket
import sqlite3
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

p = argparse.ArgumentParser(description='Rehearse KV cutover, reverse move and paired restore on disposable copies.')
p.add_argument('--program', required=True, type=Path)
p.add_argument('--product', required=True, choices=['host', 'hack'])
p.add_argument('--dump', required=True, type=Path, help='custom-format dump from a stopped install')
p.add_argument('--data-dir', required=True, type=Path, help='matching whole data-directory copy')
p.add_argument('--out', required=True, type=Path, help='report directory, retained after scratch cleanup')
a = p.parse_args()
a.program = a.program.resolve(strict=True)
a.dump = a.dump.resolve(strict=True)
a.data_dir = a.data_dir.resolve(strict=True)
a.out = a.out.resolve()
server = urllib.parse.urlsplit(os.environ.get('TEST_PG_URL', ''))
if server.scheme not in ('postgres', 'postgresql') or not server.hostname or server.path or server.query or server.fragment:
    p.error('TEST_PG_URL must be a bare disposable-server URL')
if server.port is None or server.port in (5432, 55412):
    p.error('TEST_PG_URL must specify a disposable port other than 5432 or 55412')
if not a.data_dir.is_dir() or a.out == a.data_dir or a.data_dir in a.out.parents:
    p.error('--data-dir must be a directory and --out must be outside it')
a.out.mkdir(mode=0o700, parents=True, exist_ok=True)
os.umask(0o077)
report = (a.out / 'steps.jsonl').open('a')
def record(**fields):
    report.write(json.dumps(fields, sort_keys=True) + '\n')
    report.flush()

@contextlib.contextmanager
def step(name):
    begin = time.monotonic()
    try:
        yield
    except BaseException:
        record(step=name, seconds=round(time.monotonic()-begin, 6), status='failed')
        raise
    else:
        record(step=name, seconds=round(time.monotonic()-begin, 6), status='ok')

pg_env = {'PATH': os.environ.get('PATH', '/usr/bin:/bin'), 'PGHOST': server.hostname,
          'PGPORT': str(server.port), 'PGUSER': urllib.parse.unquote(server.username or 'postgres'),
          'PGPASSWORD': urllib.parse.unquote(server.password or ''), 'PGSSLMODE': 'disable'}
db_name = 'kv_rehearsal_' + uuid.uuid4().hex
owned = False
child = None
scratch = tempfile.TemporaryDirectory(prefix='kv-rehearsal-')
root = Path(scratch.name)
data = root / 'sites'
keys = {}
admin_key = secrets.token_hex(32)
kv_store = 'postgres'

def command(args, env=None, stdin=None):
    r = subprocess.run([str(x) for x in args], env=env, input=stdin,
                       stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    if r.returncode:
        # Do not include arguments, SQL, credentials or copied values in errors.
        raise RuntimeError(Path(str(args[0])).name + ' failed (exit ' + str(r.returncode) + ')')
    return r.stdout

def sql(query, database=None):
    return command(['psql', '-XAt', '-v', 'ON_ERROR_STOP=1'],
                   {**pg_env, 'PGDATABASE': database or db_name}, query.encode()).decode().strip()

def create_database():
    global owned
    sql('CREATE DATABASE "' + db_name + '"', 'postgres')
    owned = True
    sql('ALTER DATABASE "' + db_name + '" SET timezone TO \'UTC\'', 'postgres')

def restore_database(path):
    command(['pg_restore', '--no-owner', '--no-privileges', '-d', db_name, path], pg_env)

def env():
    user = urllib.parse.quote(pg_env['PGUSER'], safe='')
    password = urllib.parse.quote(pg_env['PGPASSWORD'], safe='')
    host = '[' + server.hostname + ']' if ':' in server.hostname else server.hostname
    return {'PATH': pg_env['PATH'], 'TZ': 'UTC',
            'DB_DSN': f'postgres://{user}:{password}@{host}:{server.port}/{db_name}?sslmode=disable',
            'DATA_DIR': str(data), 'ADMIN_API_KEY': admin_key, 'KV_STORE': kv_store,
            'BIND_ADDR': '127.0.0.1', 'PORT': str(port), 'PUBLIC_BASE_URL': base,
            'SITE_DOMAIN': 'rehearsal.invalid', 'SITE_HOSTS': 'serve', 'PERSON_HOSTS': 'serve', 'EVENTS': 'hosted' if a.product == 'hack' else '',
            'PAPER_METRICS': 'off', 'GEOIP_DIR': str(root / 'geoip'), 'EVENT_NAME_PEER': '',
            'SITE_STORAGE_SITE_INFLIGHT': '1', 'SITE_STORAGE_ACQUIRE_WAIT_MS': '5000',
            'SITE_STORAGE_VISITOR_QUERY_TIMEOUT_MS': '5000'}

with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    port = sock.getsockname()[1]
base = f'http://127.0.0.1:{port}'
http = urllib.request.build_opener(urllib.request.ProxyHandler({}))

def request(method, path, token=None, body=None):
    headers = {'X-API-Key': token or test_key, 'X-SH-CSRF': '1'}
    if body is not None:
        headers['Content-Type'] = 'application/json'
        body = json.dumps(body).encode()
    req = urllib.request.Request(base + path, data=body, method=method, headers=headers)
    try:
        with http.open(req, timeout=60) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as e:
        return e.code, e.read()

def ok(method, path, body=None):
    status, content = request(method, path, body=body)
    if status >= 300:
        raise RuntimeError('local HTTP operation failed (status ' + str(status) + ')')
    return content

def start():
    global child
    child = subprocess.Popen([str(a.program)], env=env(), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        if child.poll() is not None:
            raise RuntimeError('scratch server exited before readiness')
        try:
            if request('GET', '/healthz', admin_key)[0] == 200:
                return
        except OSError:
            pass
        time.sleep(.1)
    raise RuntimeError('scratch server did not become ready')

def stop():
    global child
    if child is not None:
        child.terminate()
        try:
            child.wait(timeout=60)
        except subprocess.TimeoutExpired:
            child.kill()
            child.wait()
        child = None

def move(mode):
    args = [a.program, 'kv-move', '--' + mode, '--log', a.out / (mode + '.jsonl')]
    if mode == 'dry-run':
        args += ['--scratch', root / 'dry-run']
    command(args, env())

def resources():
    return json.loads(sql("SELECT coalesce(json_agg(row_to_json(x)), '[]') FROM (SELECT r.site_id,r.name,r.kv_store,s.user_id,s.name AS site_name,s.deleted_at IS NOT NULL AS deleted FROM site_storage_resources r JOIN sites s ON s.id=r.site_id WHERE r.kind='kv' ORDER BY r.site_id,r.name) x"))

def require_store(expected):
    namespaces = int(sql("SELECT count(*) FROM site_storage_resources WHERE kind='kv'"))
    wrong = int(sql("SELECT count(*) FROM site_storage_resources WHERE kind='kv' AND kv_store <> '" + expected + "'"))
    pg_rows = int(sql('SELECT count(*) FROM site_storage_kv'))
    if wrong or (expected == 'sqlite' and pg_rows):
        raise RuntimeError('move postcondition failed: expected all namespaces on ' + expected)
    record(comparison='all-namespaces-on-' + expected, namespaces=namespaces,
           postgres_rows=pg_rows, differences=0)

def owner_tool(tool, extra=None):
    prefix, arguments = ('hack_event_', {'event': test_site}) if a.product == 'hack' else ('', {'site': test_site})
    rpc = json.loads(ok('POST', '/mcp', {'jsonrpc': '2.0', 'id': 1, 'method': 'tools/call', 'params': {'name': prefix + tool, 'arguments': {**arguments, **(extra or {})}}}))
    if 'error' in rpc or rpc.get('result', {}).get('isError'):
        raise RuntimeError('owner storage tool failed: ' + prefix + tool)
    return rpc['result']

def tool_usage():
    result = owner_tool('storage_get_usage')
    return json.loads(''.join(item['text'] for item in result['content'] if item['type'] == 'text'))

def compare_tool_usage(expected):
    if tool_usage() != expected:
        raise RuntimeError('cutover changed storage_get_usage result')
    record(comparison='storage_get_usage', differences=0)

def capture():
    # Pair by exact key bytes rather than trusting the database collation.
    state, answers = {}, {}
    for r in resources():
        site, name = r['site_id'], r['name']
        if r['kv_store'] == 'postgres':
            rows = json.loads(sql("SELECT coalesce(json_agg(row_to_json(x)), '[]') FROM (SELECT key,value::text AS value,writer_id AS writer,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS.US\"Z\"') AS created,to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS.US\"Z\"') AS updated FROM site_storage_kv WHERE site_id='" + site + "' AND resource_name='" + name.replace("'", "''") + "') x"))
        else:
            site_dir = data / ('deleted' if r['deleted'] else 'by-id') / r['user_id'] / (site if r['deleted'] else r['site_name'])
            path = site_dir / 'runtime' / 'kv' / (name + '.sqlite')
            with contextlib.closing(sqlite3.connect(path.as_uri() + '?mode=ro', uri=True)) as db:
                rows = [dict(zip(('key', 'value', 'writer', 'created', 'updated'), row))
                        for row in db.execute('SELECT key,value,visitor_id,created_at,updated_at FROM kv')]
        state[(site, name)] = sorted(rows, key=lambda e: e['key'].encode())
        if not r['deleted']:
            token = keys[r['user_id']]
            path = '/v1/sites/' + urllib.parse.quote(r['site_name'], safe='') + '/storage/kv/' + name + '/keys'
            for row in rows:
                status, raw = request('GET', path + '/' + urllib.parse.quote(row['key'], safe=''), token)
                answers[(site, name, row['key'])] = (status, json.loads(raw))
    return state, answers

def equal(expected, actual, name):
    if expected != actual:
        raise RuntimeError(name + ': key-value state or HTTP answers differ')
    record(comparison=name, differences=0, namespaces=len(actual[0]), reads=len(actual[1]))

def throughput(label):
    path = test_base + '/kv/data/keys/edit'
    for concurrency in (1, 4):
        begin = time.monotonic()
        with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as pool:
            statuses = list(pool.map(lambda _: request('GET', path)[0], range(100)))
        elapsed = time.monotonic() - begin
        failures = sum(status != 200 for status in statuses)
        record(measurement='kv-read-throughput', store=label, site_inflight=1,
               clients=concurrency, reads=len(statuses), failures=failures,
               seconds=round(elapsed, 6), reads_per_second=round(len(statuses)/elapsed, 3),
               load_average=os.getloadavg())
        if failures:
            raise RuntimeError('throughput reads failed')

def export_kv():
    path = '/v1/admin/export.tar.gz' if a.product == 'hack' else '/v1/sites/' + test_site + '/export.tar.gz'
    status, raw = request('GET', path, admin_key if a.product == 'hack' else test_key)
    if status != 200:
        raise RuntimeError('scratch export failed')
    with tarfile.open(fileobj=io.BytesIO(raw), mode='r:gz') as archive:
        return {item.name: archive.extractfile(item).read() for item in archive.getmembers()
                if item.name.endswith('/storage/kv.json')}

signal.signal(signal.SIGTERM, lambda *_: sys.exit(143))

try:
    with step('restore-input-copies'):
        create_database()
        restore_database(a.dump)
        shutil.copytree(a.data_dir, data, symlinks=True)
        # Scratch-only: never deliver a copied subscription to a real person.
        # Provider credentials and production encryption keys are never inherited.
        sql('DELETE FROM site_notifications')
        # Saved setup hosts override the environment at startup. Only the
        # scratch copy should serve the rehearsal.invalid page hosts.
        sql("DELETE FROM instance_config WHERE key IN ('site_domain', 'content_host')")
        command([a.program, 'migrate'], env())
        for owner in json.loads(sql("SELECT coalesce(json_agg(id), '[]') FROM users")):
            key = secrets.token_hex(32)
            keys[owner] = key
            sql("INSERT INTO api_keys(key_hash,user_id) VALUES('" + hashlib.sha256(key.encode()).hexdigest() + "','" + owner + "')")
        test_owner, test_id = str(uuid.uuid4()), str(uuid.uuid4())
        test_site = 'rehearsal-' + secrets.token_hex(6)
        test_handle = 'rehearsal-' + secrets.token_hex(6)
        test_key = secrets.token_hex(32)
        keys[test_owner] = test_key
        sql("INSERT INTO users(id,username,handle) VALUES('" + test_owner + "','" + test_site + "@example.com','" + test_handle + "'); INSERT INTO api_keys(key_hash,user_id) VALUES('" + hashlib.sha256(test_key.encode()).hexdigest() + "','" + test_owner + "')")
        if a.product == 'host':
            sql("INSERT INTO sites(id,user_id,name) VALUES('" + test_id + "','" + test_owner + "','" + test_site + "')")
            current = data / 'by-id' / test_owner / test_site / 'current'
            current.mkdir(parents=True)
            (current / 'index.html').write_text('<h1>Scratch rehearsal</h1>')
        test_base = '/v1/sites/' + test_site + '/storage'
    with step('start-before-cutover'):
        start()
        if a.product == 'hack':
            now = datetime.datetime.now(datetime.timezone.utc)
            ok('POST', '/v1/hack/events', {'slug': test_site, 'title': 'Scratch rehearsal',
               'organiser_name': 'Rehearsal', 'organisation': 'Test', 'contact_email': 'rehearsal@example.com',
               'purpose': 'Scratch cutover rehearsal', 'expected_participants': 10, 'time_zone': 'UTC',
               'starts_at': (now + datetime.timedelta(days=1)).isoformat(),
               'ends_at': (now + datetime.timedelta(days=2)).isoformat()})
            ok('PUT', '/v1/hack/events/' + test_site + '/website/files?create=1', {'files': {'index.html': '<h1>Scratch rehearsal</h1>'}})
            test_base = '/v1/hack/events/' + test_site + '/website/storage'
            event = json.loads(sql("SELECT row_to_json(x) FROM (SELECT e.id,e.account_id FROM events e WHERE e.slug='" + test_site + "') x"))
            test_id = sql("SELECT id FROM sites WHERE user_id='" + event['account_id'] + "' AND name='" + event['id'] + "'")
            event_owner_key = secrets.token_hex(32)
            keys[event['account_id']] = event_owner_key
            sql("INSERT INTO api_keys(key_hash,user_id) VALUES('" + hashlib.sha256(event_owner_key.encode()).hexdigest() + "','" + event['account_id'] + "')")
        for namespace in ('data', 'delete-later'):
            ok('PUT', test_base + '/resources/' + namespace, {'kind': 'kv', 'preset': 'private'})
        for key in ('edit', 'delete', 'A', 'a', 'x%_\\', 'é'):
            ok('PUT', test_base + '/kv/data/keys/' + urllib.parse.quote(key, safe=''), {'value': {'n': 100, 'text': 'before'}})
        backup_state = capture()
        usage_before = json.loads(ok('GET', test_base + '/usage'))
        tool_usage_before = tool_usage()
        export_before = export_kv()
        throughput('postgres')
    with step('2-dry-run-while-serving'):
        move('dry-run')
    with step('3-free-disk'):
        estimate = root / 'estimate.dump'
        command(['pg_dump', '-Fc', '-f', estimate, db_name], pg_env)
        data_bytes = sum(f.stat().st_size for f in data.rglob('*') if f.is_file() and not f.is_symlink())
        required = math.ceil((data_bytes + estimate.stat().st_size) * 1.2)
        free = shutil.disk_usage(root).free
        record(data_bytes=data_bytes, dump_bytes=estimate.stat().st_size, free_bytes=free, required_bytes=required)
        if free < required:
            raise RuntimeError('insufficient disk for paired backup plus twenty percent')
        estimate.unlink()
    with step('4-stop'):
        stop()
    with step('5-paired-backup'):
        pair_dump, pair_files = root / 'paired.dump', root / 'paired.tar.gz'
        command(['pg_dump', '-Fc', '-f', pair_dump, db_name], pg_env)
        with tarfile.open(pair_files, 'w:gz') as archive:
            archive.add(data, arcname='sites')
        for f in (pair_dump, pair_files):
            with f.open('rb') as stream:
                digest = hashlib.file_digest(stream, 'sha256').hexdigest()
            record(backup=f.name, bytes=f.stat().st_size, sha256=digest)
    with step('6-apply'):
        move('apply')
        require_store('sqlite')
    with step('7-verify'):
        move('verify')
    with step('8-start-sqlite-and-smoke'):
        kv_store = 'sqlite'
        start()
        equal(backup_state, capture(), 'cutover')
        if json.loads(ok('GET', test_base + '/usage')) != usage_before or export_kv() != export_before:
            raise RuntimeError('cutover changed usage or exported kv.json')
        # Exercise the page route without an account credential.
        ok('PUT', test_base + '/resources/public', {'kind': 'kv', 'read': 'anyone', 'add': 'signed-in', 'edit': 'own', 'delete': 'own'})
        page_path = test_base + '/kv/public/keys/page'
        page_host = test_site + '.' + test_handle + '.rehearsal.invalid'
        if a.product == 'hack':
            page_path = '/v1/sites/' + test_site + '/storage/kv/public/keys/page'
            page_host = test_site + '.rehearsal.invalid'
        session = secrets.token_hex(32)
        sql("INSERT INTO visitor_sessions(id,user_id,site_id,host,expires_at,idle_expires_at) VALUES(decode('" + session + "','hex'),'" + test_owner + "','" + test_id + "','" + page_host + "',now()+interval '1 hour',now()+interval '1 hour')")
        headers = {'Cookie': 'sh_vsess=' + session, 'Host': page_host, 'Origin': 'http://' + page_host, 'X-SH-CSRF': '1', 'Content-Type': 'application/json'}
        for method in ('PUT', 'GET', 'DELETE'):
            body = json.dumps({'value': 'page-smoke'}).encode() if method == 'PUT' else None
            req = urllib.request.Request(base + page_path, data=body, method=method, headers=headers)
            with http.open(req, timeout=60) as response:
                if response.status != 200:
                    raise RuntimeError('page write or read failed')
        for tool, extra in (('storage_put_kv', {'name': 'data', 'key': 'tool', 'value': {'smoke': 'tool'}}),
                            ('storage_get_kv', {'name': 'data', 'key': 'tool'}),
                            ('storage_delete_kv', {'name': 'data', 'key': 'tool'})):
            owner_tool(tool, extra)
        compare_tool_usage(tool_usage_before)
        throughput('sqlite')
    with step('later-writes'):
        ok('PUT', test_base + '/kv/data/keys/new', {'value': [2, 3]})
        ok('PUT', test_base + '/kv/data/keys/edit', {'value': {'changed': True}})
        ok('DELETE', test_base + '/kv/data/keys/delete')
        ok('PUT', test_base + '/resources/new-namespace', {'kind': 'kv', 'preset': 'private'})
        ok('PUT', test_base + '/kv/new-namespace/keys/new', {'value': 'new namespace'})
        ok('DELETE', test_base + '/resources/delete-later')
        before_rollback = capture()
        move('verify')
    with step('rollback-and-compare'):
        stop()
        move('rollback')
        require_store('postgres')
        kv_store = 'postgres'
        start()
        equal(before_rollback, capture(), 'rollback-preserves-later-writes')
    with step('last-resort-paired-restore'):
        stop()
        # Discard BOTH scratch stores, never overlay a backup onto later files.
        sql('DROP DATABASE "' + db_name + '" WITH (FORCE)', 'postgres')
        owned = False
        create_database()
        restore_database(pair_dump)
        shutil.rmtree(data)
        with tarfile.open(pair_files, 'r:gz') as archive:
            archive.extractall(root, filter='fully_trusted')
        start()
        equal(backup_state, capture(), 'paired-restore-returns-to-backup-time')
        if json.loads(ok('GET', test_base + '/usage')) != usage_before or export_kv() != export_before:
            raise RuntimeError('paired restore changed usage or exported kv.json')
    record(product=a.product, status='passed', differences=0, input_dump_bytes=a.dump.stat().st_size)
    print('Rehearsal passed; timings, sizes, hashes and throughput: ' + str(a.out))
except Exception as error:
    # Only our fixed, data-free failure messages leave this process.
    print('Rehearsal failed: ' + (str(error) if isinstance(error, RuntimeError) else type(error).__name__), file=sys.stderr)
    sys.exit(1)
finally:
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    signal.signal(signal.SIGINT, signal.SIG_IGN)
    stop()
    try:
        if owned:
            sql('DROP DATABASE "' + db_name + '" WITH (FORCE)', 'postgres')
    finally:
        scratch.cleanup()
        report.close()
PY
