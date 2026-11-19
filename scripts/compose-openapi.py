#!/usr/bin/env python3
"""Compose the product reference from authored platform and product sources."""
import argparse
import copy
import json
import sys
sys.dont_write_bytecode = True
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[1]
BASE = Path('host/webassets/static/openapi.yaml')
PART = Path('products/hack/webassets/openapi.part.yaml')
PRODUCT = Path('products/hack/webassets')
METHODS = {'get', 'put', 'post', 'delete', 'patch', 'options', 'head', 'trace'}


def operations(spec):
    for path, item in spec['paths'].items():
        for method, op in item.items():
            if method in METHODS:
                yield path, method, op


def operation(spec, selector):
    matches = [op for path, method, op in operations(spec)
               if op.get('operationId') == selector or
               (not op.get('operationId') and f'{method.upper()} {path}' == selector)]
    if len(matches) != 1:
        raise ValueError(f'{selector!r}: expected one operation, found {len(matches)}')
    return matches[0]


def pointer(root, path):
    if not path.startswith('/'):
        raise ValueError(f'not a JSON Pointer: {path!r}')
    node = root
    for token in path[1:].split('/'):
        token = token.replace('~1', '/').replace('~0', '~')
        node = node[int(token)] if isinstance(node, list) else node[token]
    return node


def change(root, path, value=None, remove=False):
    parent, _, token = path.rpartition('/')
    node = pointer(root, parent) if parent else root
    token = token.replace('~1', '/').replace('~0', '~')
    key = int(token) if isinstance(node, list) else token
    # Overrides may change existing fields only. A miss must never silently
    # become an addition (or hide a drifted schema).
    node[key]
    if remove:
        del node[key]
    else:
        node[key] = copy.deepcopy(value)


def compose(base, part):
    spec = copy.deepcopy(base)
    for selector in part['include']:
        op = operation(spec, selector)
        op['x-audience'] = [*op['x-audience'], 'public-hack']
    for path, item in part.get('paths', {}).items():
        target = spec['paths'].setdefault(path, {})
        for key, value in item.items():
            if key in target and target[key] != value:
                raise ValueError(f'duplicate path field: {path} {key}')
            target[key] = copy.deepcopy(value)
    for section, additions in part.get('components_add', {}).items():
        target = spec.setdefault('components', {}).setdefault(section, {})
        for name, value in additions.items():
            if name in target:
                raise ValueError(f'component already exists: {section}/{name}')
            target[name] = copy.deepcopy(value)
    drop_host_only(spec, part)
    for override in part.get('overrides', []):
        if 'component' in override:
            change(spec['components'], override['component'], override.get('value'), 'remove' in override)
        else:
            target = operation(spec, override['op'])
            change(target, override.get('set', override.get('remove')),
                   override.get('value'), 'remove' in override)
    for key in ('info', 'servers'):
        spec[key] = copy.deepcopy(part[key])
    # Tags are normal OpenAPI metadata: replace shared descriptions and add
    # product tags, retaining the platform's order for shared tags.
    tags = {tag['name']: copy.deepcopy(tag) for tag in part.get('tags', [])}
    spec['tags'] = [tags.pop(tag['name'], tag) for tag in spec['tags']]
    spec['tags'].extend(tags.values())
    validate_refs(spec)
    return spec


def drop_host_only(spec, part):
    """Leave out what the product does not offer: operations under a tag in
    drop_tags (and the tag), and every use of the parameters in
    drop_parameters (and the parameters)."""
    tags = set(part.get('drop_tags', []))
    params = set(part.get('drop_parameters', []))
    for path, item in list(spec['paths'].items()):
        for method in [m for m in item if m in METHODS]:
            if tags & set(item[method].get('tags', [])):
                del item[method]
        if not any(m in METHODS for m in item):
            del spec['paths'][path]
            continue
        for holder in [item] + [item[m] for m in item if m in METHODS]:
            if 'parameters' in holder:
                holder['parameters'] = [p for p in holder['parameters'] if p.get('$ref') not in params]
                if not holder['parameters']:
                    del holder['parameters']
    spec['tags'] = [tag for tag in spec.get('tags', []) if tag['name'] not in tags]
    for ref in params:
        del pointer(spec, '/'.join(ref[1:].split('/')[:-1]))[ref.rsplit('/', 1)[1]]


def validate_refs(spec):
    visited = set()

    def scan(node):
        if isinstance(node, dict):
            ref = node.get('$ref')
            if ref and ref not in visited:
                if not ref.startswith('#/'):
                    raise ValueError(f'nonlocal reference: {ref}')
                visited.add(ref)
                try:
                    scan(pointer(spec, ref[1:]))
                except (KeyError, IndexError, TypeError) as exc:
                    raise ValueError(f'unresolved reference: {ref}') from exc
            for value in node.values():
                scan(value)
        elif isinstance(node, list):
            for value in node:
                scan(value)
    scan(spec)


def public_spec(source, audience):
    spec = copy.deepcopy(source)
    used_tags = set()
    for path, item in list(spec['paths'].items()):
        retained = False
        for method, op in list(item.items()):
            if method not in METHODS:
                continue
            if audience not in op.get('x-audience', []):
                del item[method]
                continue
            retained = True
            del op['x-audience']
            used_tags.update(op.get('tags', []))
        if not retained:
            del spec['paths'][path]
    spec['tags'] = [tag for tag in spec.get('tags', []) if tag['name'] in used_tags]
    used = set()

    def scan(node):
        if isinstance(node, dict):
            ref = node.get('$ref', '')
            if ref.startswith('#/components/') and ref not in used:
                used.add(ref)
                scan(pointer(spec, ref[1:]))
            for value in node.values():
                scan(value)
        elif isinstance(node, list):
            for value in node:
                scan(value)
    scan(spec['paths'])
    for section, definitions in spec.get('components', {}).items():
        if section != 'securitySchemes':
            for name in list(definitions):
                if f'#/components/{section}/{name}' not in used:
                    del definitions[name]
    validate_refs(spec)
    return spec


def go_json(spec):
    # Match encoding/json.MarshalIndent, including its HTML escaping. These
    # bytes are also valid YAML 1.2 and preserve both existing public URLs.
    text = json.dumps(spec, indent=2, ensure_ascii=False, sort_keys=True)
    for char, escaped in [('&', r'\u0026'), ('<', r'\u003c'), ('>', r'\u003e'),
                          ('\u2028', r'\u2028'), ('\u2029', r'\u2029')]:
        text = text.replace(char, escaped)
    return text


def generated():
    base = yaml.safe_load((ROOT / BASE).read_text())
    part = yaml.safe_load((ROOT / PART).read_text())
    validate_refs(base)
    internal = compose(base, part)
    public = go_json(public_spec(internal, 'public-hack'))
    return {
        Path('host/webassets/static/openapi.json'): json.dumps(base, indent=2, ensure_ascii=False),
        PRODUCT / 'static/openapi.json': public,
        PRODUCT / 'static/openapi.yaml': '# Generated; edit host/webassets/static/openapi.yaml and products/hack/webassets/openapi.part.yaml.\n' + public,
        PRODUCT / 'openapi.internal.json': go_json(internal),
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    stale = []
    for path, body in generated().items():
        target = ROOT / path
        if args.check:
            if not target.exists() or target.read_text() != body:
                stale.append(str(path))
        else:
            target.write_text(body)
    if stale:
        parser.exit(1, 'stale generated references; run make inventory: ' + ', '.join(stale) + '\n')


if __name__ == '__main__':
    main()
