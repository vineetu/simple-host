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
# Simple Hack asks the platform about people, sites, keys and connections
# (host/access/product.go, host/sites/product.go); it never queries their
# tables itself. Paper metrics, the daily measurement, and hacktest, the
# feature suites' fixture, are the exceptions.
platform_tables = re.compile(r'\b(?:FROM|JOIN|UPDATE|INTO)\s+(?:users|sites|versions|api_keys|oauth_\w+)\b', re.I)
for path in pathlib.Path('products/hack').rglob('*.go'):
    if path.name.endswith('_test.go') or 'papermetrics' in path.parts or 'hacktest' in path.parts:
        continue
    for n, line in enumerate(path.read_text().splitlines(), 1):
        if platform_tables.search(line):
            raise SystemExit(f'layering: {path}:{n} queries a platform table; ask the platform (host/sites/product.go)')
# Edges are thin: a routes package, a feature's routes.go (Access, Sites, Data),
# Data's route core, host/handler and host/mcp read the request, call their module and
# answer; they never write SQL or import a repo package
# (scripts/split/thin-edges-allow.txt lists the exceptions, with why).
edge_dirs = ['host/access/accesshttp/*.go', 'host/access/*/routes.go', 'host/sites/siteshttp/*.go', 'host/sites/*/routes.go', 'host/data/*/routes.go',
             'host/data/{http,routes,site_storage*,notify,turnstile}.go', 'host/handler/*.go', 'host/mcp/*.go']
def edge_files(pattern):
    m = re.match(r'(.*)\{([^}]*)\}(.*)', pattern)
    if m:
        return [f for alt in m[2].split(',') for f in edge_files(m[1] + alt + m[3])]
    return sorted(pathlib.Path('.').glob(pattern))
allowed = set()
for line in pathlib.Path('scripts/split/thin-edges-allow.txt').read_text().splitlines():
    if line.strip() and not line.startswith('#'):
        path, what, _ = (part.strip() for part in line.split('|', 2))
        allowed.add((path, what))
sql_text = re.compile(r'(?is)\b(?:SELECT\s+[\w\s,.*()=$\']{1,300}?\bFROM\s+\w|INSERT\s+INTO\s+\w|UPDATE\s+\w+\s+SET\s+\w|DELETE\s+FROM\s+\w)')
repo_import = re.compile(r'"github\.com/vsriram/simple-host/((?:host|products)/[\w/]*?\brepo(?:/[\w/]+)?)"')
for pattern in edge_dirs:
    for path in edge_files(pattern):
        if path.name.endswith('_test.go'):
            continue
        text = path.read_text()
        name = str(path)
        if (name, 'sql') not in allowed:
            for literal in re.findall(r'`[^`]*`|"(?:[^"\\\n]|\\.)*"', text):
                if sql_text.search(literal):
                    n = text[:text.index(literal)].count('\n') + 1
                    raise SystemExit(f'layering: {name}:{n} writes SQL at the edge; put it in the owning module (its repo) and call the module')
        for imported in repo_import.findall(text):
            if (name, 'import:' + imported) not in allowed:
                raise SystemExit(f'layering: {name} imports {imported}; call the owning module instead of its repo')
PY
go run ./scripts/layering
