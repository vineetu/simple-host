#!/usr/bin/env python3
"""Offline parity checks for the Simple Hack setup helper and install docs."""
import json
from pathlib import Path
import re
import sys
import textwrap

ROOT = Path(__file__).resolve().parents[1]
SETTINGS = (
    'SITE_DOMAIN', 'SUPPORT_EMAIL', 'PRIVACY_URL',
    'RESEND_API_KEY', 'MAIL_FROM', 'ASSISTANT_BASE_URL', 'ASSISTANT_MODEL',
    'ASSISTANT_API_KEY',
)


def main():
    failures = []

    def check(ok, message):
        if not ok:
            failures.append(message)

    data = json.loads((ROOT / 'internal/webassets/static/setup/simple-hack.json').read_text())
    compose = (ROOT / 'deploy/hack/standalone/compose.yaml').read_text()
    installer = (ROOT / 'deploy/hack/standalone/install.sh').read_text()
    compose_pin = re.search(r'simple-hack:(\d+\.\d+\.\d+)', compose)
    installer_pin = re.search(r"if not values.get\('SIMPLE_HACK_IMAGE'\): values\['SIMPLE_HACK_IMAGE'\] = 'ghcr.io/vineetu/simple-hack:(\d+\.\d+\.\d+)'", installer)
    check(compose_pin and installer_pin and data['version'] == compose_pin[1] == installer_pin[1],
          'simple-hack.json version, compose.yaml image pin and install.sh default image tag must match')
    for step in data['steps']:
        for pin in re.findall(r'VERSION=(\S+)', step.get('code', '')):
            check(pin == data['version'], f'simple-hack.json step downloads VERSION={pin}, not the pinned {data["version"]}')
    domain = 'hack.example.com'
    rendered = [step['code'].replace('{SETTINGS}', 'SITE_DOMAIN=' + domain).replace('{DOMAIN}', domain)
                for step in data['steps'] if 'code' in step]
    docs_path = ROOT / 'docs/platforms/simple-hack-standalone.md'
    docs = docs_path.read_text()
    install = re.search(r'^## Install\s*\n(.*?)(?=^## |\Z)', docs, re.M | re.S)
    check(install is not None, f'{docs_path.relative_to(ROOT)} must have a ## Install section')
    blocks = []
    if install:
        # Fences in numbered lists may be indented. Dedent each block's body.
        blocks = [textwrap.dedent(m[1]).rstrip('\n') for m in re.finditer(
            r'^ *```[^\n]*\n(.*?)^ *```[ \t]*$', install[1], re.M | re.S)]
    check(blocks == rendered,
          f'{docs_path.relative_to(ROOT)} Install code blocks differ from the helper (expected {len(rendered)}, found {len(blocks)})')
    if blocks != rendered:
        for i in range(max(len(blocks), len(rendered))):
            if i >= len(blocks) or i >= len(rendered) or blocks[i] != rendered[i]:
                failures.append(f'Install code block {i + 1} does not match simple-hack.json')
    for step in data['steps']:
        if 'text' in step:
            text = step['text'].replace('{DOMAIN}', domain)
            check(install is not None and text in install[1],
                  f'{docs_path.relative_to(ROOT)} Install section must contain the helper step: {text}')
    check(domain in docs and '*.' + domain in docs,
          f'{docs_path.relative_to(ROOT)} must name {domain} and *.{domain}')

    js = (ROOT / 'internal/webassets/static/setup/setup.js').read_text()
    example = (ROOT / 'deploy/hack/standalone/.env.example').read_text()
    environment = re.search(r'^  environment: &environment\n(.*?)(?=^\S)', compose, re.M | re.S)
    for key in SETTINGS:
        check(re.search(r'\b' + key + r'\b', js), f'helper must contain emitted setting {key}')
        check(re.search(r'^\s*#?\s*' + key + '=', example, re.M), f'.env.example missing setting {key}')
        check(environment and re.search(r'^    ' + key + ':', environment[1], re.M),
              f'compose.yaml environment map missing setting {key}')
    history = re.compile(r'release status|checked on|verified on|tested on|tested 20|verified 20|clean-install test', re.I)
    for path in sorted((ROOT / 'docs/platforms').glob('*.md')):
        for number, line in enumerate(path.read_text().splitlines(), 1):
            match = history.search(line)
            if match:
                failures.append(f'{path.relative_to(ROOT)}:{number}: release-history prose ({match[0]})')
    for message in failures:
        print('FAIL: ' + message)
    if failures:
        return 1
    print('Simple Hack setup: versions, install steps, settings and platform docs agree')
    return 0


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (OSError, ValueError, KeyError, TypeError) as exc:
        print(f'FAIL: Simple Hack setup check could not read its inputs: {exc}')
        sys.exit(1)
