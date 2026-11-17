#!/usr/bin/env bash
# Edit templates and versions.env, then run this script. Outputs remain usable
# on their own: installers never source a file from a different release.
# Helm and provider recipes join this renderer in their own packaging slices.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
case ${1:-} in
  '') output=$root ;;
  --out) [[ $# == 2 ]] || exit 2; output=$2 ;;
  *) echo 'usage: render.sh [--out DIRECTORY]' >&2; exit 2 ;;
esac
python3 - "$root" "$output" <<'PY'
from pathlib import Path
import re
import sys

root, output = map(Path, sys.argv[1:])
shared = root / 'deploy/shared'
values = {}
for line in (shared / 'versions.env').read_text().splitlines():
    if line and not line.startswith('#'):
        key, value = line.split('=', 1)
        values[key] = value
for template in sorted((shared / 'templates').rglob('*.in')):
    name = str(template.relative_to(shared / 'templates'))[:-3]
    text = re.sub(r'@([A-Z][A-Z0-9_]*)@', lambda m: values[m[1]], template.read_text())
    dest = output / name
    dest.parent.mkdir(parents=True, exist_ok=True)
    dest.write_text(text)
PY
