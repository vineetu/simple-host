#!/usr/bin/env python3
"""Migrate legacy failure markers only when their reason proves lock contention.

Old empty site markers need the matching failed attempt in the journal. Never
let an older lock error override a later CA refusal. New classified markers keep
their bounded retry schedule and are not reset each run.
"""
import json
import os
from pathlib import Path
import re
import subprocess
import sys


def requeue(state, rows):
    pending = None
    latest = {}
    for row in rows:
        message = row.get('MESSAGE', '')
        stamp = int(row['__REALTIME_TIMESTAMP']) / 1_000_000
        if 'issuing ' in message:
            pending = None
        if re.search(r'Another instance of Certbot|(?:could not|failed to|unable to).*lock|lock.*(?:busy|held|timeout)', message, re.I):
            pending = stamp
        match = re.search(r'certbot failed for ([a-z0-9-]+);', message)
        domain = re.search(r'domain-certs: ([a-z0-9.-]+): .*refused', message)
        ready = re.search(r'(?:ready: \*\.|ready: )([a-z0-9.-]+)', message)
        if match or domain:
            name = (match or domain)[1]
            latest[name] = pending
            pending = None
        elif ready:
            latest[ready[1].split('.')[0] if 'ready: *.' in message else ready[1]] = None
    for marker in (state / 'failed').glob('*'):
        if not marker.is_file() or marker.name.endswith('.new'):
            continue
        text = marker.read_text()
        if 'retry_at=' in text:
            continue
        recorded = re.search(r'Another instance of Certbot|(?:could not|failed to|unable to).*lock|lock.*(?:busy|held|timeout)', text, re.I)
        stamp = latest.get(marker.name)
        if not recorded and (stamp is None or abs(marker.stat().st_mtime - stamp) > 120):
            continue
        new = marker.with_name(marker.name + '.new')
        new.write_text('transient: lock-busy\nattempt=0\nretry_at=0\n')
        new.chmod(0o644)
        # Legacy app builds infer the deadline from mtime + six hours.
        os.utime(new, (0, 0))
        new.replace(marker)
        print(f'cert-runtime: requeued {marker.name} (legacy lock contention)')


if __name__ == '__main__':
    state, unit = Path(sys.argv[1]), sys.argv[2]
    legacy = [p for p in (state / 'failed').glob('*') if p.is_file() and 'retry_at=' not in p.read_text()]
    if legacy:
        result = subprocess.run(['journalctl', '--since', '30 days ago', '-u', unit, '-o', 'json', '--no-pager'], capture_output=True, text=True, check=True)
        requeue(state, [json.loads(line) for line in result.stdout.splitlines()])
