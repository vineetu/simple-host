#!/usr/bin/env bash
# Package membership is proof; strings are supporting evidence only.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
if [[ ${1:-} == --root && $# == 2 ]]; then
    root=$2
elif [[ $# != 0 ]]; then
    echo 'usage: check-binary.sh [--root fixture-directory]' >&2
    exit 2
fi
cd "$root"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
go list -buildvcs=false -deps ./cmd/simple-host > "$work/packages"
if grep -E '^github.com/vsriram/simple-host/products/' "$work/packages"; then
    echo 'binary boundary: forbidden product package in simple-host' >&2
    exit 1
fi
go build -buildvcs=false -o "$work/simple-host" ./cmd/simple-host
strings "$work/simple-host" | grep -Eio 'simple[ -]?hack|hackathon' > "$work/strings" || test "$?" -eq 1
# Deliberate legacy text and unrelated matches are documented; unlike the
# package proof, the number of strings varies with compiler/linker output.
python3 - "$work/strings" scripts/allow/binary-strings.txt <<'PY'
import pathlib, re, sys
patterns = []
for line in pathlib.Path(sys.argv[2]).read_text().splitlines():
    if not line or line.startswith('#'):
        continue
    pattern, reason = line.split(' | ', 1)
    if not reason.strip():
        raise SystemExit('binary string exception needs a reason')
    patterns.append(re.compile(pattern, re.I))
matches = pathlib.Path(sys.argv[1]).read_text().splitlines()
unknown = sorted({s for s in matches if not any(p.fullmatch(s) for p in patterns)})
print(f'binary boundary ok: no product packages; supporting string scan: {len(matches)} matches')
if unknown:
    print('Unexplained supporting strings: ' + ', '.join(unknown))
PY
