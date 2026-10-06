#!/usr/bin/env python3
"""Offline checks of individual pacing decisions; no ACME or burst traffic."""
import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import types
from unittest.mock import patch

clock = [1000.0]
calls = []
reply = [200, {}]


class Network:
    def _send_request(self, method, url, *args, **kwargs):
        calls.append((method, url, clock[0]))
        status, headers = reply
        return types.SimpleNamespace(status_code=status, headers=headers, ok=status == 200,
                                     json=lambda: {'newOrder': 'https://dv.acme-v02.api.pki.goog/new-order'})


sys.modules['acme'] = types.ModuleType('acme')
sys.modules['acme.client'] = types.SimpleNamespace(ClientNetwork=Network)
sys.modules['certbot'] = types.ModuleType('certbot')
sys.modules['certbot.main'] = types.SimpleNamespace(main=lambda: None)
with tempfile.TemporaryDirectory() as tmp:
    os.environ['GOOGLE_ACME_STATE'] = tmp
    spec = importlib.util.spec_from_file_location('pacing', Path(__file__).with_name('google-certbot.py'))
    pacing = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(pacing)
    with patch.object(pacing.time, 'time', lambda: clock[0]), patch.object(pacing.time, 'sleep', lambda n: clock.__setitem__(0, clock[0] + n)):
        n = Network()
        n._send_request('GET', 'https://dv.acme-v02.api.pki.goog/directory')
        n._send_request('POST', 'https://dv.acme-v02.api.pki.goog/new-order')
        assert calls[-1][2] == 1002
        # A separate client/process reads the same request/order timestamps.
        m = Network()
        m._send_request('GET', 'https://dv.acme-v02.api.pki.goog/directory')
        m._send_request('POST', 'https://dv.acme-v02.api.pki.goog/new-order')
        assert calls[-1][2] == 1042
        reply[:] = [429, {'Retry-After': '120'}]
        m._send_request('POST', 'https://dv.acme-v02.api.pki.goog/challenge')
        assert float((Path(tmp) / 'next-request').read_text()) == 1164
        reply[:] = [200, {}]
        n._send_request('POST', 'https://dv.acme-v02.api.pki.goog/cert')
        assert calls[-1][2] == 1164
        n._send_request('GET', 'https://other.example/directory')
        assert calls[-1][2] == 1164
print('Google pacing decisions: request gap, shared order gap, Retry-After and other CAs: ok')
