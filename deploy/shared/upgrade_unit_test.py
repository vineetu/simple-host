#!/usr/bin/env python3
"""Configuration regressions that need no container daemon."""
import ast
import errno
import json
import multiprocessing
import os
from pathlib import Path
import signal
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
SOURCE = (ROOT / 'deploy/shared/upgrade.sh').read_text().split("<<'PY'\n", 1)[1].rsplit('\nPY', 1)[0]
# Load the distributed functions without running its CLI or contacting Docker.
TREE = ast.parse(SOURCE)
FUNCTIONS = compile(ast.Module(body=[node for node in TREE.body if isinstance(
    node, (ast.Import, ast.ImportFrom, ast.FunctionDef))], type_ignores=[]), '<upgrade.sh>', 'exec')


class ConfigurationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='upgrade-config-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.ns = {}
        exec(FUNCTIONS, self.ns)

    def test_release_prefixes_match_go_floor(self):
        for version in ('0.9.0', 'v0.9.0', 'hack-v0.9.0'):
            self.assertEqual(self.ns['normalize_release'](version), '0.9.0')
        self.assertNotEqual(self.ns['normalize_release']('v0.9.1'), '0.9.0')

    def test_selected_candidate_release_is_derived_and_validated(self):
        args = SimpleNamespace(skip_pull=True, image='fixture:override', to=None)
        self.ns['a'] = args
        calls = []

        def run(command):
            calls.append(command)
            if command[1:3] == ['image', 'inspect']:
                return b'[{"Id":"sha256:fixture"}]'
            return b'simple-host hack-v0.9.0 (commit abcdef1)\n'

        self.ns['run'] = run
        self.assertEqual(self.ns['candidate_release'](), '0.9.0')
        self.assertEqual(args.image, 'sha256:fixture')
        self.assertEqual(calls[-1], ['docker', 'run', '--rm', '--network', 'none', 'sha256:fixture', 'version'])
        args.to = 'v0.9.0'
        self.assertEqual(self.ns['candidate_release'](), '0.9.0')
        args.to = '0.8.0'
        with self.assertRaisesRegex(RuntimeError, 'differs from --to'):
            self.ns['candidate_release']()
        for value in (b'simple-host dev', b'simple-host v0.9.0-dirty', b'simple-host v0.9.0.1'):
            self.ns['run'] = lambda command: b'[{"Id":"sha256:fixture"}]' if command[1] == 'image' else value
            with self.assertRaisesRegex(RuntimeError, 'no release version'):
                self.ns['candidate_release']()

    def test_saved_configuration_prefers_original_container_image_ids(self):
        path = self.root / 'images.json'
        saved = {'services': {'db': {'image': 'fixture@sha256:repository'}},
                 'x-upgrade-image-ids': {'db': 'sha256:original'}}
        path.write_text(json.dumps(saved))
        original = path.read_bytes()
        self.ns['attempt_dir'] = self.root
        restored = self.ns['saved_images']()
        self.assertEqual(restored['services']['db']['image'], 'sha256:original')
        self.assertNotIn('x-upgrade-image-ids', restored)
        self.assertEqual(path.read_bytes(), original)
        del saved['x-upgrade-image-ids']
        path.write_text(json.dumps(saved))
        self.assertEqual(self.ns['saved_images'](), saved)

    def test_upgrade_diagnostics_only_expose_compatibility_instruction(self):
        output = b'docker config secret=fixture-secret\nunsupported migration ledger for simple-hack; upgrade to hack-v0.8.20 first\n'
        result = subprocess.CompletedProcess(['docker'], 1, stdout=b'fixture-secret', stderr=output)
        with patch('subprocess.run', return_value=result):
            with self.assertRaisesRegex(RuntimeError, '^Unsupported starting point; upgrade to hack-v0.8.20 first$'):
                self.ns['run'](['docker'], upgrade_diagnostics=True)
            with self.assertRaisesRegex(RuntimeError, '^docker failed \\(exit 1\\)$'):
                self.ns['run'](['docker'])

    def test_hack_upgrade_retains_restore_helper_for_old_install(self):
        package = self.root / 'package'
        package.mkdir()
        install = self.root / 'old-install'
        install.mkdir()
        (install / '.env').write_text('old configuration')
        (install / 'compose.yaml').write_text('old compose')
        (package / 'upgrade.sh').write_bytes((ROOT / 'deploy/hack/standalone/upgrade.sh').read_bytes())
        helper = package / 'shared-upgrade.sh'
        helper.write_text('#!/bin/bash\nexit 42\n')
        result = subprocess.run(['bash', str(package / 'upgrade.sh'), '--dir', str(install)],
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.assertEqual(result.returncode, 42)
        retained = install / 'shared-upgrade.sh'
        self.assertEqual(retained.read_bytes(), helper.read_bytes())
        self.assertEqual(retained.stat().st_mode & 0o777, 0o700)
        # Also allow the wrapper and helper to live in the installation itself.
        (install / 'upgrade.sh').write_bytes((package / 'upgrade.sh').read_bytes())
        again = subprocess.run(['bash', str(install / 'upgrade.sh'), '--dir', str(install), '--restore'],
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.assertEqual(again.returncode, 42, again.stderr.decode())

    def test_private_atomic_publication_and_failed_write(self):
        path = self.root / 'compose.yaml'
        path.write_bytes(b'original configuration')
        path.chmod(0o644)
        inode = path.stat().st_ino
        with patch('os.fsync', side_effect=OSError(errno.ENOSPC, 'fixture disk full')):
            with self.assertRaises(OSError):
                self.ns['publish'](path, b'replacement with credentials')
        self.assertEqual(path.read_bytes(), b'original configuration')
        self.assertEqual(path.stat().st_ino, inode)
        self.assertEqual(list(self.root.iterdir()), [path])
        self.ns['publish'](path, b'replacement with credentials')
        self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        self.assertNotEqual(path.stat().st_ino, inode)

    def test_kill_during_publication_preserves_restorable_configuration(self):
        path = self.root / 'compose.yaml'
        original = b'{"services":{"app":{"image":"old"}}}'
        path.write_bytes(original)
        context = multiprocessing.get_context('fork')
        receive, send = context.Pipe(duplex=False)

        def publish_and_pause():
            real_sync = os.fsync

            def synced(fd):
                real_sync(fd)
                send.send('synced')
                signal.pause()

            with patch('os.fsync', side_effect=synced):
                self.ns['publish'](path, b'candidate')

        child = context.Process(target=publish_and_pause)
        child.start()
        try:
            self.assertTrue(receive.poll(10), 'publication did not reach fsync')
            self.assertEqual(receive.recv(), 'synced')
            os.kill(child.pid, signal.SIGKILL)
            child.join(10)
            self.assertEqual(child.exitcode, -signal.SIGKILL)
            self.assertEqual(path.read_bytes(), original)
            # Restore uses the same atomic publisher for the backup bytes.
            self.ns['publish'](path, original)
            self.assertEqual(json.loads(path.read_text())['services']['app']['image'], 'old')
        finally:
            if child.is_alive():
                child.kill()
            child.join()
            receive.close()
            send.close()

    def test_saved_template_still_reads_changed_environment(self):
        path = self.root / 'compose.yaml'
        env = self.root / '.env'
        env.write_text('ADMIN_API_KEY=first-fixture-key\n')
        path.write_text(json.dumps({'name': 'upgrade-config-test', 'services': {
            'app': {'image': 'fixture:old', 'environment': {'ADMIN_API_KEY': '${ADMIN_API_KEY}'}}
        }}))

        def compose(*args, file=None):
            # Compose's real parser runs read-only, without a daemon.
            return subprocess.check_output(['docker', 'compose', '--project-directory', str(self.root),
                                            '--env-file', str(env), '-f', str(file or path), *args],
                                           stderr=subprocess.DEVNULL)

        self.ns['compose'] = compose
        template = self.ns['config'](template=True)
        template['services']['app']['image'] = 'fixture:candidate'
        self.ns['write_json'](path, template)
        env.write_text('ADMIN_API_KEY=rotated-fixture-key\n')
        resolved = self.ns['config']()
        self.assertEqual(template['services']['app']['environment']['ADMIN_API_KEY'], '${ADMIN_API_KEY}')
        self.assertEqual(resolved['services']['app']['environment']['ADMIN_API_KEY'], 'rotated-fixture-key')
        self.assertEqual(resolved['services']['app']['image'], 'fixture:candidate')


if __name__ == '__main__':
    unittest.main()
