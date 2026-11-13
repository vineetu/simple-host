#!/usr/bin/env bash
# Pass the Host golden recording's text/host folder, or set GOLDEN_TEXT_DIR.
# Missing input is an error, never an empty/green public-text report.
set -euo pipefail
text_dir=${1:-${GOLDEN_TEXT_DIR:-}}
if [[ -z $text_dir || $# -gt 1 ]]; then
    echo 'usage: bash scripts/check-public-text.sh GOLDEN_RUN/text/host' >&2
    exit 2
fi
text_dir=$(cd "$text_dir" && pwd)
cd "$(dirname "${BASH_SOURCE[0]}")/.."
exec go run ./scripts/split/boundary -mode public-text -text "$text_dir"
