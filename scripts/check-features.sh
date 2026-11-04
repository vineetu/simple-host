#!/usr/bin/env bash
# Check generated inventories and feature placement, then OpenAPI audiences.
set -eu
cd "$(dirname "$0")/.."
go run ./scripts/inventory check
python3 scripts/check-openapi-coverage.py
