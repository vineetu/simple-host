#!/usr/bin/env bash
# Regenerate docs/advanced/settings.json from the code, then the settings
# tables in docs/advanced/*.md and the setup helper's copies under
# internal/handler/static/setup/ (the enterprise copy from an enterprise
# checkout: ENTERPRISE_REPO=/path/to/simple-host-enterprise, else
# ../simple-host-enterprise or /tmp/ent-wt/advanced).
# `make check` fails until this has been run after a setting changes.
set -euo pipefail
cd "$(dirname "$0")/.."
go run ./cmd/server settings --json > docs/advanced/settings.json.new
mv docs/advanced/settings.json.new docs/advanced/settings.json
python3 scripts/settings_docs.py
