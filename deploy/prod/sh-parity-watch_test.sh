#!/usr/bin/env bash
# Exercise the real monitor without fetching, state changes or messaging.
set -euo pipefail
cd "$(dirname "$0")/../.."
task_dir=$(mktemp -d)
trap 'rm -rf "$task_dir"' EXIT
mkdir "$task_dir/input" "$task_dir/state"
cp PARITY.md "$task_dir/input/hosted.PARITY.md"
cp PARITY.md "$task_dir/input/enterprise.PARITY.md"
grep -E '^\|' PARITY.md | grep -F '`gap' | awk -F'|' '{gsub(/^ +| +$/,"",$2); print $2}' | sort > "$task_dir/state/gaps.last"
python3 - "$task_dir/input/enterprise.PARITY.md" <<'PY'
from pathlib import Path
import sys
p=Path(sys.argv[1]);s=p.read_text();p.write_text('\n'.join(line for line in s.splitlines() if not line.startswith('| Site storage primitives (2026-10-02) |'))+'\n')
PY
check() {
 SH_PARITY_CHECK_ONLY=1 SH_PARITY_STATE="$task_dir/state" SH_PARITY_INPUT_DIR="$task_dir/input" bash deploy/prod/sh-parity-watch.sh
}
check > "$task_dir/quiet.log"
grep -q 'ok: equivalent=yes' "$task_dir/quiet.log"
! grep -q 'check failed:\|sent:' "$task_dir/quiet.log"
printf '\nUnrelated drift\n' >> "$task_dir/input/enterprise.PARITY.md"
if check > "$task_dir/drift.log"; then echo 'FAIL: unrelated drift was hidden'; exit 1; fi
grep -q 'check failed:' "$task_dir/drift.log"
cp PARITY.md "$task_dir/input/enterprise.PARITY.md"
printf '\n| Unexpected gap | missing | present | `gap → hosted` |\n' >> "$task_dir/input/hosted.PARITY.md"
if check > "$task_dir/gap.log"; then echo 'FAIL: new gap was hidden'; exit 1; fi
grep -q 'Parity gaps grew' "$task_dir/gap.log"
echo 'PASS parity monitor: intentional storage differences quiet; unrelated drift and new gaps detected; no messages sent'
