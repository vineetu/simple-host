#!/usr/bin/env python3
"""D3-S5c fresh-schema comparison and seeded upgrade/rollback rehearsal.

Uses only uniquely owned databases on TEST_PG_URL. --previous is a pre-split
binary (at or above the rollback floor); it starts in setup mode after both
schema checks, so the seeded notification/delivery workers never run.
"""
import argparse
import contextlib
import difflib
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import tempfile
import time
import urllib.parse
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[2]
START = 'ea1f87b1'


def command(args, *, data=None, env=None):
    result = subprocess.run([str(a) for a in args], input=data, text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, cwd=ROOT, env=env)
    if result.returncode:
        # Command arguments can contain a DSN; never print them.
        raise RuntimeError(f'{Path(args[0]).name} failed: {result.stderr[-4000:]}')
    return result.stdout


def sql(url, text):
    return command(['psql', url, '-XAt', '-v', 'ON_ERROR_STOP=1'], data=text)


@contextlib.contextmanager
def database(base):
    name = 'd3s5c_' + uuid.uuid4().hex
    admin = base + '/postgres?sslmode=disable'
    sql(admin, 'CREATE DATABASE ' + name)
    try:
        yield base + '/' + name + '?sslmode=disable'
    finally:
        sql(admin, 'DROP DATABASE ' + name + ' WITH (FORCE)')


def git_file(path):
    return command(['git', 'show', START + ':' + path])


def env_for(url, product):
    # In particular, inherit no provider credentials or production settings.
    env = {k: v for k, v in os.environ.items() if k in ('PATH', 'HOME', 'TMPDIR')}
    env.update(DB_DSN=url, EVENTS='hosted' if product == 'hack' else 'off')
    return env


def migrate(binary, url, product, init=False):
    args = [binary, 'migrate'] + (['-init'] if init else [])
    return command(args, env=env_for(url, product))


def dump(url):
    """Compare every pg_dump object body; ignore only comments/column order.

    PostgreSQL appends migrated columns, whereas schema.sql may declare them
    inline. Column ordinal order is not part of the application contract.
    Constraint, index, trigger, function and sequence bodies remain intact.
    """
    text = command(['pg_dump', url, '--schema-only', '--no-owner', '--no-privileges'])
    objects = {}
    pattern = r'-- Name: (.*?); Type: (.*?); Schema: (.*?); Owner:.*?\n--\n(.*?)(?=\n--\n-- Name:|\Z)'
    for name, kind, schema, body in re.findall(pattern, text, re.S):
        lines = [line for line in body.splitlines() if line and not line.startswith(('--', '\\'))]
        if kind == 'TABLE':
            # One pg_dump column/constraint per line; remove the final-list
            # comma before sorting so a different last column is immaterial.
            lines = [lines[0]] + sorted(line.rstrip(',') for line in lines[1:-1]) + [lines[-1]]
        objects[f'{kind} {schema}.{name}'] = '\n'.join(lines)
    if not any(k.startswith('TABLE public.users') for k in objects):
        raise RuntimeError('pg_dump object parsing found no users table')
    return objects


def compare(want, got, label):
    if want != got:
        lines = []
        for key in sorted(want.keys() | got.keys()):
            if want.get(key) != got.get(key):
                lines.append(key)
                lines.extend(difflib.unified_diff(want.get(key, '').splitlines(), got.get(key, '').splitlines()))
        raise RuntimeError(label + '\n' + '\n'.join(lines))


def changes(before, after, product, reason):
    lines = []
    for key in sorted(before.keys() | after.keys()):
        if before.get(key) != after.get(key):
            sign = '+' if key not in before else '-' if key not in after else '~'
            lines.append(f'{product} {sign} {key} | {reason}')
            if sign == '~' and key.startswith('TABLE '):
                for line in sorted(set(after[key].splitlines()) - set(before[key].splitlines())):
                    member = 'constraint' if line.strip().startswith('CONSTRAINT ') else 'column'
                    lines.append(f'{product} + {member} {key.removeprefix("TABLE ")}.{line.strip()} | {reason}')
                for line in sorted(set(before[key].splitlines()) - set(after[key].splitlines())):
                    member = 'constraint' if line.strip().startswith('CONSTRAINT ') else 'column'
                    lines.append(f'{product} - {member} {key.removeprefix("TABLE ")}.{line.strip()} | {reason}')
    return lines


def initial_schema(url):
    sql(url, git_file('host/db/schema.sql'))
    runner = git_file('host/db/migrations/migrations.go')
    legacy = re.search(r'var Legacy = \[\]string\{(.*?)\n\}', runner, re.S).group(1)
    names = re.findall(r'"([^"]+)"', legacy)
    files = command(['git', 'ls-tree', '--name-only', START + ':host/db/migrations']).splitlines()
    names += sorted(n for n in files if re.match(r'^\d{4}-.*\.sql$', n))
    for name in names:
        sql(url, git_file('host/db/migrations/' + name))


def additions():
    out = []
    for line in (ROOT / 'scripts/split/migration-owners.txt').read_text().splitlines():
        if line.startswith('#') or not line.strip():
            continue
        name, owner, _ = map(str.strip, line.split('|', 2))
        if re.match(r'^\d{4}-', name) and int(name[:4]) > 17:
            folder = 'products/hack/db/migrations' if owner == 'hack' else 'host/db/migrations'
            out.append((name, owner, ROOT / folder / name))
    return sorted(out)


def remove_product(url):
    objects = []
    for line in (ROOT / 'scripts/split/hack-objects.txt').read_text().splitlines():
        if line.startswith('#') or not line.strip():
            continue
        kind, name, *_ = map(str.strip, line.split('|'))
        if kind == 'table':
            objects.append('DROP TABLE IF EXISTS public.' + name + ' CASCADE;')
        elif kind == 'function':
            objects.append('DROP FUNCTION IF EXISTS public.' + name + '() CASCADE;')
        elif kind == 'column':
            table, column = name.split('.')
            objects.append(f'ALTER TABLE {table} DROP COLUMN IF EXISTS {column};')
    sql(url, '\n'.join(objects))


def data_snapshot(url):
    tables = sql(url, "SELECT tablename FROM pg_tables WHERE schemaname='public' AND tablename<>'schema_migrations' ORDER BY 1").splitlines()
    return {t: sql(url, f'SELECT row_to_json(t)::text FROM "{t}" t ORDER BY row_to_json(t)::text') for t in tables}


def previous_start(binary, url, product):
    # Setup mode runs normal startup schema verification but starts no workers.
    with tempfile.TemporaryDirectory(prefix='d3s5c-start-') as tmp, socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
        sock.close()
        env = env_for(url, product)
        env.update(ADMIN_API_KEY=uuid.uuid4().hex + uuid.uuid4().hex, DATA_DIR=tmp, BIND_ADDR='127.0.0.1', PORT=str(port), MIGRATE_ON_START='false')
        with open(Path(tmp) / 'server.log', 'w+') as log:
            proc = subprocess.Popen([str(binary)], env=env, stdout=log, stderr=log)
            try:
                for _ in range(300):
                    if proc.poll() is not None:
                        log.seek(0)
                        raise RuntimeError('previous program exited: ' + log.read())
                    try:
                        with urllib.request.urlopen(f'http://127.0.0.1:{port}/', timeout=0.2) as response:
                            if response.status == 200:
                                log.flush()
                                log.seek(0)
                                assert 'SETUP MODE' in log.read(), 'previous program started workers'
                                return
                    except (OSError, urllib.error.URLError):
                        time.sleep(0.1)
                raise RuntimeError('previous program did not start')
            finally:
                proc.terminate()
                proc.wait(timeout=30)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--candidate', required=True, type=Path)
    parser.add_argument('--previous', required=True, type=Path)
    parser.add_argument('--write-expected', action='store_true')
    args = parser.parse_args()
    base = os.environ['TEST_PG_URL']
    parsed = urllib.parse.urlparse(base)
    if parsed.scheme not in ('postgres', 'postgresql') or parsed.path or parsed.query or parsed.fragment:
        raise RuntimeError('TEST_PG_URL must be a bare server URL')
    report = [f'# Generated by scripts/split/schema-check.py; reference fresh install: {START}.',
              '# - absent from platform by hack-objects.txt; +/~ caused only by named post-reference migrations.',
              '# Object definitions compared using pg_dump; column ordinal order ignored.']
    for product in ('host', 'hack'):
        with database(base) as expected, database(base) as fresh:
            initial_schema(expected)
            before = dump(expected)
            if product == 'host':
                remove_product(expected)
                after = dump(expected)
                report += changes(before, after, product, 'hack-objects.txt (D3-S5c)')
                before = after
            for name, owner, path in additions():
                if product == 'host' and owner == 'hack':
                    continue
                sql(expected, path.read_text())
                after = dump(expected)
                report += changes(before, after, product, name)
                before = after
            migrate(args.candidate, fresh, product, init=True)
            compare(dump(expected), dump(fresh), product + ' fresh schema has an unapproved difference')
            assert 'nothing to apply' in migrate(args.candidate, fresh, product)
            print('PASS: fresh ' + product + ' matches the generated expected schema', flush=True)
        with database(base) as upgraded:
            evidence = ROOT / 'scripts/split/evidence' / ('simple-' + product)
            text = evidence.with_suffix('.schema.sql').read_text()
            text = '\n'.join(line for line in text.splitlines() if not line.startswith('\\'))
            sql(upgraded, text)
            ledger = evidence.with_suffix('.schema_migrations.tsv').read_text()
            sql(upgraded, 'COPY schema_migrations(name,applied_at) FROM STDIN;\n' + ledger + '\\.\n')
            sql(upgraded, (ROOT / 'scripts/split/seed/seed.sql').read_text())
            before_data = data_snapshot(upgraded)
            migrate(args.candidate, upgraded, product)
            after_schema, after_data = dump(upgraded), data_snapshot(upgraded)
            for table, rows in before_data.items():
                # Additive columns acquire their defaults; compare old columns only.
                columns = list(json.loads(rows.splitlines()[0])) if rows else []
                if columns:
                    selected = ','.join('"' + c + '"' for c in columns)
                    projected = sql(upgraded, f'SELECT row_to_json(t)::text FROM (SELECT {selected} FROM "{table}") t ORDER BY row_to_json(t)::text')
                    assert projected == rows, 'upgrade changed seeded rows in ' + table
                else:
                    assert after_data[table] == rows, 'upgrade populated ' + table
            ledger_after = sql(upgraded, 'SELECT name,applied_at FROM schema_migrations ORDER BY name')
            assert 'nothing to apply' in migrate(args.candidate, upgraded, product)
            compare(after_schema, dump(upgraded), 'repeated Apply changed schema')
            assert after_data == data_snapshot(upgraded), 'repeated Apply changed data'
            assert ledger_after == sql(upgraded, 'SELECT name,applied_at FROM schema_migrations ORDER BY name')
            previous_start(args.previous, upgraded, product)
            print('PASS: seeded ' + product + ' upgrade, repeat no-op, previous program startup', flush=True)
    expected_file = ROOT / 'scripts/split/schema-expected-diff.txt'
    text = '\n'.join(report) + '\n'
    if args.write_expected:
        expected_file.write_text(text)
    elif expected_file.read_text() != text:
        raise RuntimeError('schema-expected-diff.txt is stale; regenerate with --write-expected')


if __name__ == '__main__':
    main()
