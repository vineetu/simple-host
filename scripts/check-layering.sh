#!/usr/bin/env bash
# Import contract: docs/architecture/target.md §2 and scripts/layering/rules.txt.
set -e
cd "$(dirname "$0")/.."
python3 - <<'PY'
import pathlib
import re

makefile = pathlib.Path('Makefile').read_text()
check = re.search(r'^check:[^\n]*\n((?:\t[^\n]*\n|\n)*)', makefile, re.M)
if not check or not re.search(r'^\t\$\(MAKE\) test-rules\s*$', check[1], re.M):
    raise SystemExit('layering: check must run $(MAKE) test-rules')
for name in ('ci', 'release'):
    path = pathlib.Path(f'.github/workflows/{name}.yml')
    if not re.search(r'^\s*- name: rules engine module\s*\n\s+run: make test-rules\s*$', path.read_text(), re.M):
        raise SystemExit(f'layering: {path} must have the rules engine module step running make test-rules')
# Simple Hack asks the platform about sites, keys and connections
# (host/sites/product.go, host/access/product.go); it never queries their
# tables itself. Paper metrics, a read-only daily measurement, are the one
# exception.
platform_tables = re.compile(r'\b(?:FROM|JOIN|UPDATE|INTO)\s+(?:sites|versions|api_keys|oauth_\w+)\b', re.I)
for path in pathlib.Path('products/hack').rglob('*.go'):
    if path.name.endswith('_test.go') or 'papermetrics' in path.parts:
        continue
    for n, line in enumerate(path.read_text().splitlines(), 1):
        if platform_tables.search(line):
            raise SystemExit(f'layering: {path}:{n} queries a platform table; ask the platform (host/sites/product.go)')
PY
go run ./scripts/layering
