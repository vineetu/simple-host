#!/usr/bin/env python3
"""Run with TEST_PG_URL set; all database work uses owned throwaway databases."""
import ast
import json
import os
from pathlib import Path
import subprocess
import tempfile
import types
import unittest
from urllib.parse import unquote, urlsplit
import uuid


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / 'scripts/kv-move-rehearsal.sh'


class RehearsalChecks(unittest.TestCase):
    def test_sqlite_markers_do_not_hide_leftover_postgres_rows(self):
        source = SCRIPT.read_text().split("<<'PY'\n", 1)[1].rsplit('\nPY', 1)[0]
        functions = [node for node in ast.parse(source).body
                     if isinstance(node, ast.FunctionDef) and node.name == 'require_store']
        # Two SQLite markers, but an apply that failed to delete its source rows.
        counts = iter(('2', '0', '6'))
        scope = {'sql': lambda _: next(counts), 'record': lambda **_: None}
        exec(compile(ast.Module(body=functions, type_ignores=[]), str(SCRIPT), 'exec'), scope)
        with self.assertRaisesRegex(RuntimeError, 'expected all namespaces on sqlite'):
            scope['require_store']('sqlite')

    def test_usage_tool_response_is_compared_on_both_products(self):
        # Load only these functions: importing the embedded script would run it.
        source = SCRIPT.read_text().split("<<'PY'\n", 1)[1].rsplit('\nPY', 1)[0]
        functions = [node for node in ast.parse(source).body
                     if isinstance(node, ast.FunctionDef)
                     and node.name in ('owner_tool', 'tool_usage', 'compare_tool_usage')]
        code = compile(ast.Module(body=functions, type_ignores=[]), str(SCRIPT), 'exec')
        for product in ('host', 'hack'):
            with self.subTest(product=product):
                calls, records = [], []
                usage = {'kv_bytes': 100, 'sqlite_bytes': 0, 'files_bytes': 0}

                def ok(method, path, body):
                    calls.append((method, path, body['params']))
                    return json.dumps({'result': {'content': [
                        {'type': 'text', 'text': json.dumps(usage)}], 'isError': False}})

                scope = {'json': json, 'a': types.SimpleNamespace(product=product),
                         'test_site': 'scratch', 'ok': ok,
                         'record': lambda **fields: records.append(fields)}
                exec(code, scope)
                scope['compare_tool_usage'](dict(usage))
                tool = 'hack_event_storage_get_usage' if product == 'hack' else 'storage_get_usage'
                self.assertEqual(calls[-1], ('POST', '/mcp', {
                    'name': tool, 'arguments': {'event' if product == 'hack' else 'site': 'scratch'}}))
                self.assertEqual(records[-1]['differences'], 0)
                baseline = dict(usage)
                usage['kv_bytes'] += 1
                with self.assertRaisesRegex(RuntimeError, 'storage_get_usage result'):
                    scope['compare_tool_usage'](baseline)


class RehearsalIntegration(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        server = urlsplit(os.environ.get('TEST_PG_URL', ''))
        if (server.scheme not in ('postgres', 'postgresql') or not server.hostname
                or server.path or server.query or server.fragment
                or server.port in (None, 5432, 55412)):
            raise RuntimeError('TEST_PG_URL must name a bare disposable server on an allowed port')
        cls.pg_env = {**os.environ, 'PGHOST': server.hostname, 'PGPORT': str(server.port),
                      'PGUSER': unquote(server.username or 'postgres'),
                      'PGPASSWORD': unquote(server.password or ''), 'PGSSLMODE': 'disable'}
        cls.tmp = tempfile.TemporaryDirectory(prefix='kv-rehearsal-test-')
        cls.addClassCleanup(cls.tmp.cleanup)
        cls.root = Path(cls.tmp.name)
        cls.program = cls.root / 'simple-host'
        subprocess.run(['go', 'build', '-o', cls.program, './cmd/server'], cwd=ROOT, check=True)
        cls.dump = cls.root / 'configured.dump'
        cls.data = cls.root / 'sites'
        cls.data.mkdir()
        database = 'kv_rehearsal_input_' + uuid.uuid4().hex
        cls.sql('CREATE DATABASE "' + database + '"', 'postgres')
        try:
            cls.run_pg(['psql', '-X', '-v', 'ON_ERROR_STOP=1', '-f', ROOT / 'db/schema.sql'], database)
            cls.run_pg(['psql', '-X', '-v', 'ON_ERROR_STOP=1', '-f', ROOT / 'products/hack/db/schema.sql'], database)
            cls.sql("INSERT INTO instance_config(key,value) VALUES "
                    "('site_domain','saved.invalid'),('content_host','content.saved.invalid')", database)
            cls.run_pg(['pg_dump', '-Fc', '-f', cls.dump], database)
        finally:
            cls.sql('DROP DATABASE "' + database + '" WITH (FORCE)', 'postgres')

    @classmethod
    def run_pg(cls, args, database, stdin=None):
        result = subprocess.run(args, input=stdin, env={**cls.pg_env, 'PGDATABASE': database},
                                stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        if result.returncode:
            raise RuntimeError('throwaway fixture database operation failed')
        return result.stdout

    @classmethod
    def sql(cls, query, database):
        return cls.run_pg(['psql', '-XAt', '-v', 'ON_ERROR_STOP=1'], database, query.encode())

    def rehearse(self, product, skip=None):
        program = self.program
        if skip:
            program = self.root / ('skip-' + skip)
            program.write_text('#!/usr/bin/env python3\nimport os, sys\n'
                               'if sys.argv[1:2] == ["kv-move"] and '
                               + repr('--' + skip) + ' in sys.argv: sys.exit(0)\n'
                               'os.execv(' + repr(str(self.program)) + ', '
                               '[' + repr(str(self.program)) + '] + sys.argv[1:])\n')
            program.chmod(0o700)
        out = self.root / (product + '-' + (skip or 'complete'))
        result = subprocess.run(['bash', SCRIPT, '--program', program, '--product', product,
                                 '--dump', self.dump, '--data-dir', self.data, '--out', out],
                                cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        records = [json.loads(line) for line in (out / 'steps.jsonl').read_text().splitlines()]
        return result, records

    def test_configured_dump_cutover_rollback_and_restore(self):
        for product in ('host', 'hack'):
            with self.subTest(product=product):
                result, records = self.rehearse(product)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(records[-1]['status'], 'passed')
                checks = {r['comparison']: r for r in records if 'comparison' in r}
                self.assertEqual(checks['all-namespaces-on-sqlite']['postgres_rows'], 0)
                # Includes the public and new namespaces created after cutover.
                self.assertGreater(checks['all-namespaces-on-postgres']['namespaces'],
                                   checks['all-namespaces-on-sqlite']['namespaces'])
                self.assertIn('storage_get_usage', checks)
                self.assertIn('paired-restore-returns-to-backup-time', checks)

    def test_skipped_apply_fails_even_when_command_succeeds(self):
        result, records = self.rehearse('host', 'apply')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('expected all namespaces on sqlite', result.stderr)
        self.assertEqual(records[-1]['step'], '6-apply')
        self.assertEqual(records[-1]['status'], 'failed')

    def test_skipped_rollback_fails_even_when_command_succeeds(self):
        result, records = self.rehearse('host', 'rollback')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('expected all namespaces on postgres', result.stderr)
        self.assertEqual(records[-1]['step'], 'rollback-and-compare')
        self.assertEqual(records[-1]['status'], 'failed')


if __name__ == '__main__':
    unittest.main()
