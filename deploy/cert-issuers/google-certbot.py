#!/usr/bin/env python3
"""Run Certbot with project-wide Google ACME pacing, without changing Certbot.

Use the same Python environment as Certbot. All Google requests, including
challenge and certificate polls, share a two-second gap. Orders share a
40-second gap (at most 90/hour). HTTP 429 reserves the Retry-After cooldown;
Certbot reports failure to the caller, which can continue to the next CA.
"""
import email.utils
import fcntl
import os
from pathlib import Path
import sys
import time
from urllib.parse import urlsplit

from acme.client import ClientNetwork
from certbot.main import main

STATE = Path(os.environ.get('GOOGLE_ACME_STATE', '/var/lib/simple-host-google-acme'))
os.umask(0o077)
STATE.mkdir(parents=True, exist_ok=True)
send = ClientNetwork._send_request


def paced_send(self, method, url, *args, **kwargs):
    if urlsplit(url).hostname != 'dv.acme-v02.api.pki.goog':
        return send(self, method, url, *args, **kwargs)
    with (STATE / 'requests.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        next_request = STATE / 'next-request'
        next_order = STATE / 'next-order'
        # Directory endpoint names the order resource; don't assume its path.
        is_order = method == 'POST' and url == getattr(self, '_google_new_order', None)
        deadline = float(next_request.read_text()) if next_request.exists() else 0
        if is_order and next_order.exists():
            deadline = max(deadline, float(next_order.read_text()))
        time.sleep(max(0, deadline - time.time()))
        now = time.time()
        next_request.write_text(str(now + 2))
        if is_order:
            next_order.write_text(str(now + 40))
        response = send(self, method, url, *args, **kwargs)
        if urlsplit(url).path == '/directory' and response.ok:
            self._google_new_order = response.json().get('newOrder')
        if response.status_code == 429:
            retry = response.headers.get('Retry-After', '60')
            try:
                until = time.time() + float(retry)
            except ValueError:
                until = email.utils.parsedate_to_datetime(retry).timestamp()
            next_request.write_text(str(max(time.time() + 2, until)))
        return response


ClientNetwork._send_request = paced_send
if __name__ == '__main__':
    sys.exit(main())
