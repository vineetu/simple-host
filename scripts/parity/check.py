#!/usr/bin/env python3
"""Validate outcome references, optionally against an actual golden replay."""
import argparse
import json
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[2]
COMMON = {'sign-in', 'publish', 'update', 'roll-back', 'set-passcode',
          'name-viewers', 'connect-domain', 'save-data', 'read-data',
          'delete-site', 'restore-site'}
HACK = {'create-event', 'join', 'form-team', 'publish-team-site', 'submit',
        'judge', 'publish-results'}


def check(matrix, recordings=None):
    errors = []
    products = matrix['products']
    source = (ROOT / 'scripts/golden/run.mjs').read_text()
    # Dynamic labels name their source template explicitly. --recordings then
    # checks the concrete name in its actual product and scenario.
    labels = re.findall(r'\bat\(\s*([\'"`])(.*?)\1', source)
    literals = {label for quote, label in labels if quote != '`' or '${' not in label}
    templates = {label for quote, label in labels if quote == '`' and '${' in label}
    covered = set((ROOT / 'scripts/golden/coverage-covered.txt').read_text().splitlines())
    table = re.search(r'^const SCENARIOS = \{(.*?)^\};', source, re.M | re.S)[1]
    scenarios = set(re.findall(r"^ {0,2}['\"]?([\w-]+)['\"]?:\s*\{", table, re.M))
    browser_source = (ROOT / 'scripts/split/browser-check.sh').read_text()
    runners = re.findall(r"^'[^'\n]*\|([\w-]+)'$", browser_source, re.M)
    browser_checks = {f'scripts/e2e-{runner}.mjs' for runner in runners}
    runs = {}
    if recordings:
        manifest = json.loads((recordings / 'manifest.json').read_text())
        for run in manifest['runs']:
            data = json.loads((recordings / run['file']).read_text())
            runs[data['product'], data['scenario']] = {r['name']: r for r in data['requests']}
    if set(products) != {'host', 'hack'}:
        errors.append('matrix must list host and hack')
    for product, rows in products.items():
        steps = [row['step'] for row in rows]
        required = COMMON | (HACK if product == 'hack' else set())
        if len(steps) != len(set(steps)) or set(steps) != required:
            errors.append(f'{product}: steps must appear exactly once: {sorted(required)}')
        for row in rows:
            for surface in ('browser', 'api', 'mcp'):
                label = f"{product}/{row['step']}/{surface}"
                ref = row.get(surface, {})
                if not isinstance(ref, dict):
                    errors.append(f'{label}: missing check or exception')
                    continue
                name, exception = ref.get('check'), ref.get('exception')
                if exception is not None:
                    if name is not None or not isinstance(exception, str) or not exception.strip():
                        errors.append(f'{label}: supply one check or a nonempty exception')
                    continue
                if not isinstance(name, str) or not name.strip():
                    errors.append(f'{label}: missing check or exception')
                    continue
                if surface == 'browser':
                    if name not in browser_checks or not (ROOT / name).is_file():
                        errors.append(f'{label}: dangling browser check {name}')
                    continue
                scenario = ref.get('scenario')
                if scenario not in scenarios:
                    errors.append(f'{label}: dangling scenario {scenario}')
                if surface == 'api':
                    template = ref.get('source')
                    pattern = re.sub(r'\\\$\\\{.*?\\\}', '.+', re.escape(template or ''))
                    if name not in literals and not (template in templates and re.fullmatch(pattern, name)):
                        errors.append(f'{label}: dangling golden request {name}')
                else:
                    tool = re.fullmatch(r'tools/call ([\w-]+)', name)
                    if not tool or f'tool {product} {tool[1]}' not in covered:
                        errors.append(f'{label}: dangling MCP replay call {name}')
                if recordings:
                    response = runs.get((product, scenario), {}).get(name)
                    if response is None:
                        errors.append(f'{label}: absent from replay {scenario}: {name}')
                        continue
                    if not 200 <= response['status'] < 300:
                        errors.append(f"{label}: replay {scenario}: {name}: unsuccessful HTTP status {response['status']}")
                    if surface == 'mcp':
                        body = response.get('body', {})
                        if body.get('error') is not None:
                            errors.append(f'{label}: replay {scenario}: {name}: JSON-RPC error')
                        if body.get('result', {}).get('isError'):
                            errors.append(f'{label}: replay {scenario}: {name}: MCP isError')
    return errors


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--matrix', type=Path, default=ROOT / 'scripts/parity/outcomes.json')
    parser.add_argument('--recordings', type=Path)
    args = parser.parse_args()
    errors = check(json.loads(args.matrix.read_text()), args.recordings)
    for error in errors:
        print('FAIL: ' + error, file=sys.stderr)
    if errors:
        return 1
    print('outcome matrix: all steps and surface references accounted for' +
          (' in the replay' if args.recordings else ' (source checks)'))
    return 0


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (OSError, ValueError, KeyError, TypeError) as exc:
        print(f'FAIL: could not check outcome matrix: {exc}', file=sys.stderr)
        sys.exit(1)
