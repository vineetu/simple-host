#!/usr/bin/env bash
# Render into a throwaway tree; never repair checked-in outputs during a check.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
bash "$root/deploy/shared/render.sh" --out "$work"
failed=0
while IFS= read -r -d '' template; do
  name=${template#"$root/deploy/shared/templates/"}
  name=${name%.in}
  if ! diff -u "$root/$name" "$work/$name"; then
    echo "stale shared settings: $name (run bash deploy/shared/render.sh)" >&2
    failed=1
  fi
done < <(find "$root/deploy/shared/templates" -type f -name '*.in' -print0)
(( failed == 0 ))
echo 'shared deployment settings: rendered files agree'
