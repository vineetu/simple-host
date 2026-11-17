#!/usr/bin/env bash
# D4-S12 fixtures run the public checker against matrices and replay responses.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."
python3 - <<'PY'
import json
from collections import defaultdict
from pathlib import Path
import subprocess
import tempfile

for fixture in sorted(Path('scripts/parity/fixtures').glob('*.json')):
    case = json.loads(fixture.read_text())
    matrix = json.loads(Path('scripts/parity/outcomes.json').read_text())
    row = next(r for r in matrix['products'][case['product']] if r['step'] == case['step'])
    if 'value' in case:
        if case['value'] is None:
            del row[case['surface']]
        else:
            row[case['surface']] = case['value']
    with tempfile.TemporaryDirectory() as work:
        work = Path(work)
        path = work / 'matrix.json'
        path.write_text(json.dumps(matrix))
        args = ['--matrix', str(path)]
        if 'response' in case:
            # Start with successful responses for every referenced outcome,
            # then substitute the fixture's response at its exact reference.
            runs = defaultdict(list)
            for product, rows in matrix['products'].items():
                for outcome in rows:
                    for surface in ('api', 'mcp'):
                        ref = outcome[surface]
                        if 'check' not in ref:
                            continue
                        response = {'name': ref['check'], 'status': 204 if surface == 'api' else 200,
                                    'body': {'jsonrpc': '2.0', 'result': {'content': []}}}
                        if (product, outcome['step'], surface) == (case['product'], case['step'], case['surface']):
                            response.update(case['response'])
                        runs[product, ref['scenario']].append(response)
            manifest = {'runs': []}
            for (product, scenario), requests in runs.items():
                filename = f'{product}.{scenario}.json'
                (work / filename).write_text(json.dumps({'product': product, 'scenario': scenario,
                                                       'requests': requests}))
                manifest['runs'].append({'file': filename})
            (work / 'manifest.json').write_text(json.dumps(manifest))
            args.extend(['--recordings', str(work)])
        result = subprocess.run(['bash', 'scripts/check-parity-outcomes.sh', *args],
                                capture_output=True, text=True)
    if case['error']:
        assert result.returncode == 1 and case['error'] in result.stderr, (fixture, result)
    else:
        assert result.returncode == 0, (fixture, result)
    print(f'PASS: {fixture.stem}')
PY
