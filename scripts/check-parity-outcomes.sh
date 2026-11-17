#!/usr/bin/env bash
# Check references offline, or run the existing two-product replay and browsers.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
if [[ ${1:-} != --run ]]; then
  exec python3 scripts/parity/check.py "$@"
fi
[[ $# == 1 ]] || { echo 'usage: check-parity-outcomes.sh [--run | --matrix FILE | --recordings DIRECTORY]' >&2; exit 2; }
python3 scripts/parity/check.py
# A parity run owns only databases made by the existing disposable fixtures.
# Require an explicit server so the golden harness never uses its old default.
: "${TEST_PG_URL:?Set TEST_PG_URL to a bare disposable Postgres server URL.}"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
CGO_ENABLED=0 go build -o "$work/" ./cmd/simple-host ./cmd/simple-hack
GOLDEN_PG_URL="$TEST_PG_URL" node scripts/golden/run.mjs \
  --host "$work/simple-host" --hack "$work/simple-hack" --out "$work/golden" --scenario all
python3 scripts/parity/check.py --recordings "$work/golden"
# Uses the existing explicit browser exceptions, including owner-dashboard's
# missing fixture; never supplies credentials or a URL for a running service.
bash scripts/split/check-all.sh --browser --no-cap
