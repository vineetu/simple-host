#!/usr/bin/env python3
import importlib.util
import os
import sys
sys.dont_write_bytecode = True
from pathlib import Path
import tempfile

spec = importlib.util.spec_from_file_location('requeue', Path(__file__).with_name('requeue.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

with tempfile.TemporaryDirectory() as directory:
    state = Path(directory)
    failed = state / 'failed'
    failed.mkdir()
    names = ['lock', 'refusal', 'later', 'new', 'recorded']
    for name in names:
        p = failed / name
        p.write_text('')
        os.utime(p, (1000, 1000))
    (failed / 'new').write_text('transient: lock-busy\nretry_at=2000\n')
    (failed / 'recorded').write_text('Another instance of Certbot is already running.')
    os.utime(failed / 'later', (2000, 2000))
    def row(message, stamp=1000):
        return {'MESSAGE': message, '__REALTIME_TIMESTAMP': str(stamp * 1_000_000)}
    rows = []
    for name in names:
        rows.extend([row(f'site-certs: issuing *.{name}.fixture.test'),
                     row('Another instance of Certbot is already running.'),
                     row(f'site-certs: certbot failed for {name}; retry in 6h')])
    rows.extend([row('site-certs: issuing *.refusal.fixture.test', 1001),
                 row('Detail: unauthorized', 1001),
                 row('site-certs: certbot failed for refusal; retry in 6h', 1001)])
    module.requeue(state, rows)
    for name in ['lock', 'recorded']:
        assert 'retry_at=0' in (failed / name).read_text()
    for name in ['refusal', 'later']:
        assert (failed / name).read_text() == ''
    assert 'retry_at=2000' in (failed / 'new').read_text()
print('legacy requeue: matching lock only, later CA refusal preserved, classified retries preserved: ok')
