#!/usr/bin/env python3
"""The shared contract accounts for public Host, public Hack, legacy and internal APIs."""
import json
import re
from pathlib import Path

spec = json.loads(Path('internal/handler/static/openapi.json').read_text())
methods = {'get', 'post', 'put', 'patch', 'delete', 'head', 'options', 'trace'}
audiences = {'public-host', 'public-hack', 'internal', 'legacy'}
operations = {}
for path, item in spec['paths'].items():
    for method, operation in item.items():
        if method not in methods:
            continue
        key = (method.upper(), path)
        operations[key] = operation
        marked = set(operation.get('x-audience', []))
        assert marked and marked <= audiences, f'{key}: missing/invalid audience'
        assert 'internal' not in marked or marked == {'internal'}, f'{key}: internal API advertised'
        assert 'legacy' not in marked or marked == {'legacy'}, f'{key}: legacy API advertised'
        assert operation.get('tags'), f'{key}: untagged operation'
        if path.startswith(('/v1/admin/', '/internal/', '/v1/setup/')):
            assert marked == {'internal'}, f'{key}: operator API must stay internal'
        if path.startswith(('/v1/hack/', '/v1/events')):
            assert 'public-host' not in marked, f'{key}: Hack API in Host reference'
        if 'public-host' in marked and operation['tags'] == ['Storage resources']:
            assert path.startswith('/v1/sites/') and '/storage/' in path, f'{key}: Host storage must be site-scoped'
        # Older saved-data routes keep serving existing sites but are not
        # published: they are in the contract only so coverage counts them.
        assert (operation['tags'] == ['Deprecated: saved data']) == (marked == {'legacy'}), f'{key}: legacy API classification'
        if marked == {'legacy'}:
            assert operation.get('deprecated'), f'{key}: legacy API must be marked deprecated'

registered = set()
for root in ('internal', 'cmd'):
    for file in Path(root).rglob('*.go'):
        if file.name.endswith('_test.go'):
            continue
        for method, path in re.findall(r'mux\.Handle(?:Func)?\("([A-Z]+) ((?:/v1/|/internal/)[^" ]+)"', file.read_text()):
            if method != 'OPTIONS':
                registered.add((method, re.sub(r'\{(\w+)\.\.\.\}', r'{\1}', path)))
assert registered, 'route extraction found nothing'
assert not registered - operations.keys(), f'registered operations missing from contract: {sorted(registered - operations.keys())}'
assert spec['tags'][-1]['name'] == 'Deprecated: saved data', 'deprecated section must follow current APIs'
for audience in ('public-host', 'public-hack'):
    selected = {key for key, op in operations.items() if audience in op['x-audience']}
    print(f'  ok — {audience}: {len(selected)} operations classified')
print(f'  ok — {len(operations)} operations accounted for; {sum(op["x-audience"] == ["internal"] for op in operations.values())} internal, {sum(op["x-audience"] == ["legacy"] for op in operations.values())} legacy')
