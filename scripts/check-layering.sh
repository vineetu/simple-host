#!/usr/bin/env bash
# Import contract: docs/architecture/target.md §2 and scripts/layering/rules.txt.
set -e
cd "$(dirname "$0")/.."
go run ./scripts/layering
