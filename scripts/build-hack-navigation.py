#!/usr/bin/env python3
"""Project the shared Hack navigation map into the web app and the MCP Apps, and check its help layer."""
import argparse
import json
from pathlib import Path

root = Path(__file__).resolve().parent.parent
parser = argparse.ArgumentParser()
parser.add_argument('--check', action='store_true')
args = parser.parse_args()
data = json.loads((root / 'products/hack/rules/hackevents/navigation.json').read_text())
ids = [t['id'] for t in data['targets']]
assert len(ids) == len(set(ids)), 'duplicate navigation target'
screens = {s['id']: s for s in data['screens']}
assert len(screens) == len(data['screens']), 'duplicate screen'
inventory = json.loads((root / 'docs/generated/inventory.json').read_text())
paths = {r['path'] for r in inventory['routes'] if r['method'] in ('GET', 'ANY')}
for screen in screens.values():
    if screen['path'].startswith('/'):
        assert screen['path'] in paths, 'screen missing from app route inventory: ' + screen['path']
for t in data['targets']:
    assert t['screen'] in screens, t
    assert t['href_pattern'].split('?')[0].split('#')[0] == screens[t['screen']]['path'], t
for r in data['roles']:
    if r['id'] == 'visitor':
        continue
    for n in r['nav']:
        assert n['target_id'] in ids, n
        assert any(s['role'] == r['id'] and s['section'] == n['id'] for s in screens.values()), n
# The explanation layer (navigation-help.json) is what the organiser's
# assistant says about each setting. Every label it names, and every option
# label, is text the app shows: each record cites the app files (under
# web/hack/src) that carry it, and the check fails when a label leaves them.
import re


def label_parts(label):
    """A label's fixed text: the parts around any ${...} placeholder."""
    return [p for p in re.split(r'\$\{[^}]*\}', label) if p.strip()]


def help_errors(help_map, navigation):
    errors, seen, targets_used = [], set(), set()
    records = help_map.get('records', [])
    ids = {r['id'] for r in records}
    targets = {t['id']: t for t in navigation['targets']}
    app = root / 'web/hack/src'
    for r in records:
        rid = r['id']
        if not rid or rid in seen:
            errors.append(f'help record {rid!r}: add a unique non-empty id')
        seen.add(rid)
        target = targets.get(r.get('target'))
        if not target or target['role'] != 'organiser' or '{' in target['href_pattern'].replace('{slug}', ''):
            errors.append(f'help record {rid}: target must be an existing organiser list/setting target (only shared {{slug}} is allowed)')
        if ('options' in r or r.get('target') not in targets_used) and not r.get('when', '').strip():
            errors.append(f'help record {rid}: options and first record for a target require non-empty when')
        targets_used.add(r.get('target'))
        for key in ('label', 'explains', 'sources'):
            if not r.get(key) or (isinstance(r[key], str) and not r[key].strip()):
                errors.append(f'help record {rid}: add non-empty {key}')
        texts = []
        for name in r.get('sources', []):
            path = app / name
            if path.suffix not in ('.ts', '.tsx') or not path.resolve().is_relative_to(app) or not path.exists():
                errors.append(f'help record {rid}: missing app source {name}')
            else:
                texts.append(path.read_text())
        for label in [r.get('label', '')] + [o.get('label', '') for o in r.get('options', [])]:
            if not label or not any(all(p in t for p in label_parts(label)) for t in texts):
                errors.append(f'help record {rid}: label {label!r} missing from its sources; use the exact UI text or cite the file that shows it')
        for option in r.get('options', []):
            if not option.get('label', '').strip() or not option.get('means', '').strip():
                errors.append(f'help record {rid}: every option needs label and means')
        for related in r.get('related', []):
            if related not in ids:
                errors.append(f'help record {rid}: related id {related!r} does not resolve')
    for target in navigation['targets']:
        if target['role'] == 'organiser' and target['kind'] in ('setting', 'section', 'action') and '{' not in target['href_pattern'].replace('{slug}', '') and target['id'] not in targets_used:
            errors.append(f'organiser target {target["id"]}: add a help record targeting it')
    return errors


assert label_parts('Email ${count} participants') == ['Email ', ' participants']
help_map = json.loads((root / 'products/hack/rules/hackevents/navigation-help.json').read_text())
errors = help_errors(help_map, data)
if errors:
    raise SystemExit('Navigation help parity failed:\n- ' + '\n- '.join(errors))

spec_path = root / 'docs/ux-atlas/spec.json'
spec = json.loads(spec_path.read_text())
if not args.check:
    original = json.dumps(spec, sort_keys=True)
    for role in data['roles']:
        projection = next(r for r in spec['roles'] if r['id'] == role['id'])
        projection['nav'] = role['nav']
    settings_screen = next(s for s in spec['screens'] if s['id'] == 'organiser-settings')
    existing = {s['tab']: s for s in settings_screen['sub_pages']}
    settings_screen['sub_pages'] = [dict(existing.get(t['id'], {}), tab=t['id'], label=t['label'], url='?s=settings&tab=' + t['id']) for t in data['resolution']['settings_tabs']]
    if json.dumps(spec, sort_keys=True) != original:
        spec_path.write_text(json.dumps(spec, indent=2, ensure_ascii=False) + '\n')
for role in data['roles']:
    expected = next(r for r in spec['roles'] if r['id'] == role['id'])
    assert expected['nav'] == role['nav'], 'atlas role nav drift: ' + role['id']
settings = next(s for s in spec['screens'] if s['id'] == 'organiser-settings')['sub_pages']
assert data['resolution']['settings_tabs'] == [{'id': s['tab'], 'label': s['label']} for s in settings], 'atlas Settings projection drift'
text = '// Generated by scripts/build-hack-navigation.py; do not edit.\nexport const navigation = ' + json.dumps(data, indent=2, ensure_ascii=False) + '\n'
# The MCP Apps' bundled screens (products/hack/mcp/apps/legacy) read it as a module.
p = root / 'products/hack/mcp/apps/legacy/navigation-data.js'
if args.check:
    assert p.exists() and p.read_text() == text, 'stale navigation-data.js; run python3 scripts/build-hack-navigation.py'
else:
    p.write_text(text)
# The new web app (web/hack) reads the same map, typed.
ts = '// Generated by scripts/build-hack-navigation.py from products/hack/rules/hackevents/navigation.json; do not edit.\nexport const navigation = ' + json.dumps(data, indent=2, ensure_ascii=False) + ' as const;\n'
p = root / 'web/hack/src/navigation.gen.ts'
if args.check:
    assert p.exists() and p.read_text() == ts, 'stale web/hack/src/navigation.gen.ts; run python3 scripts/build-hack-navigation.py'
else:
    p.write_text(ts)
