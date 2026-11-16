#!/usr/bin/env python3
"""Check both authored contracts, public references and internal route coverage."""
import importlib.util
import json
import sys
sys.dont_write_bytecode = True
import re
import yaml
from pathlib import Path

loader = importlib.util.spec_from_file_location('compose', Path(__file__).with_name('compose-openapi.py'))
compose = importlib.util.module_from_spec(loader)
loader.loader.exec_module(compose)

platform = yaml.safe_load(Path('internal/webassets/static/openapi.yaml').read_text())
internal = json.loads(Path('products/hack/webassets/openapi.internal.json').read_text())
public_files = ('products/hack/webassets/static/openapi.json', 'products/hack/webassets/static/openapi.yaml')
for spec in (platform, internal, *(yaml.safe_load(Path(name).read_text()) for name in public_files)):
    compose.validate_refs(spec)

operations = {}
for spec, audiences in ((platform, {'public-host', 'internal', 'legacy'}),
                        (internal, {'public-host', 'public-hack', 'internal', 'legacy'})):
    for path, method, operation in compose.operations(spec):
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
            assert 'public-host' not in marked, f'{key}: product API in Host reference'
        if 'public-host' in marked and operation['tags'] == ['Storage resources']:
            assert path.startswith('/v1/sites/') and '/storage/' in path, f'{key}: Host storage must be site-scoped'
        assert (operation['tags'] == ['Deprecated: saved data']) == (marked == {'legacy'}), f'{key}: legacy API classification'
        if marked == {'legacy'}:
            assert operation.get('deprecated'), f'{key}: legacy API must be marked deprecated'
    assert spec['tags'][-1]['name'] == 'Deprecated: saved data' or spec is internal

registered = set()
for root in ('internal', 'cmd', 'products'):
    for file in Path(root).rglob('*.go'):
        if file.name.endswith('_test.go'):
            continue
        for method, path in re.findall(r'mux\.Handle(?:Func)?\("([A-Z]+) ((?:/v1/|/internal/)[^" ]+)"', file.read_text()):
            if method != 'OPTIONS':
                registered.add((method, re.sub(r'\{(\w+)\.\.\.\}', r'{\1}', path)))
assert registered, 'route extraction found nothing'
assert not registered - operations.keys(), f'registered operations missing from contract: {sorted(registered - operations.keys())}'
for audience, spec in (('public-host', platform), ('public-hack', internal)):
    selected = {(method, path) for path, method, op in compose.operations(spec) if audience in op['x-audience']}
    print(f'  ok — {audience}: {len(selected)} operations classified')
print(f'  ok — {len(operations)} operations accounted for; internal operations included; all references resolve')
