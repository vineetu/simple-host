#!/usr/bin/env python3
"""Check both packaged RC helpers before the upgrade reaches Compose."""
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest


class CandidateVersionTest(unittest.TestCase):
    def test_release_metadata_requires_a_numeric_rc(self):
        for tag, accepted in (('v0.9.0-rc.12', True), ('v0.9.0-rc.beta', False),
                              ('v0.9.0-rc.1.2', False)):
            with self.subTest(tag=tag):
                result = subprocess.run(['bash', 'scripts/release/build.sh', 'metadata', tag],
                                        capture_output=True, text=True)
                if accepted:
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertIn('stable=false', result.stdout.splitlines())
                else:
                    self.assertEqual(result.returncode, 2)
                    self.assertEqual(result.stdout, '')

    def test_matches_full_release_candidate(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / '.env').touch()
            (root / 'compose.yaml').touch()
            docker = root / 'docker'
            docker.write_text('''#!/bin/sh
case "$1" in
  ps) exit 0 ;;
  image) printf '[{"Id":"sha256:test"}]\\n' ;;
  run) printf 'simple-host v0.9.0-rc.1 (commit test)\\n' ;;
  compose) exit 42 ;;
  *) exit 43 ;;
esac
''')
            docker.chmod(0o755)
            env = dict(os.environ, PATH=f'{root}:{os.environ["PATH"]}')
            for product, member in (('simple-host', 'deploy/shared/upgrade.sh'),
                                    ('simple-hack', 'deploy/hack/standalone/shared-upgrade.sh')):
                helper = root / 'helper.sh'
                with tarfile.open(archive) as tar:
                    helper.write_bytes(tar.extractfile(f'simple-hack-0.9.0-rc.1/{member}').read())
                for target, expected in (
                    ('0.9.0-rc.1', 'docker failed (exit 42)'),
                    ('0.9.0-rc.2', 'The candidate image release differs from --to.'),
                ):
                    with self.subTest(product=product, target=target):
                        result = subprocess.run(
                            ['bash', str(helper), '--dir', tmp, '--product', product,
                             '--image', 'test:rc', '--skip-pull', '--to', target],
                            env=env, capture_output=True, text=True,
                        )
                        self.assertEqual(result.returncode, 1)
                        self.assertIn(f'Upgrade stopped: {expected}', result.stderr)


if __name__ == '__main__':
    archive = sys.argv.pop(1)
    unittest.main()
