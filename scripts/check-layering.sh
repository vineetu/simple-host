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
for name in ('ci', 'release', 'hack-release'):
    path = pathlib.Path(f'.github/workflows/{name}.yml')
    if not re.search(r'^\s*- name: rules engine module\s*\n\s+run: make test-rules\s*$', path.read_text(), re.M):
        raise SystemExit(f'layering: {path} must have the rules engine module step running make test-rules')
PY
go run ./scripts/layering
