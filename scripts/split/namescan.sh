#!/usr/bin/env bash
# Report only: findings do not fail. Run from any directory.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."
exec go run ./scripts/split/boundary -mode names "$@"
