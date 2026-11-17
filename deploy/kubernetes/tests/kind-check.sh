#!/usr/bin/env bash
# Local-image install and Helm upgrade with the same claim, once per product:
#   TEST_PG_URL=... bash deploy/kubernetes/tests/kind-check.sh simple-host
#   TEST_PG_URL=... bash deploy/kubernetes/tests/kind-check.sh simple-hack
# IMAGE skips the build; KIND_CLUSTER chooses a NEW disposable cluster name.
# KEEP=1 retains that cluster for inspection (the test database is still dropped).
set -euo pipefail
cd "$(dirname "$0")/../../.."
product=${1:-simple-hack}
[[ $# -le 1 && ( $product == simple-host || $product == simple-hack ) ]] || { echo 'usage: kind-check.sh [simple-host|simple-hack]' >&2; exit 2; }
for tool in kind kubectl helm docker psql python3; do
  command -v "$tool" >/dev/null || { echo "FAIL: $tool is required" >&2; exit 1; }
done
exec python3 deploy/kubernetes/tests/kind_check.py "$product"
