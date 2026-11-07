#!/usr/bin/env bash
# Node is only needed here. Go embeds the committed output without running this.
set -euo pipefail
cd "$(dirname "$0")/.."
required_node=22.23.4
actual_node=$(node --version 2>/dev/null || true)
if [[ "$actual_node" != "v$required_node" ]]; then
  echo "Docs build requires Node $required_node (found ${actual_node:-none})." >&2
  exit 1
fi
export ASTRO_TELEMETRY_DISABLED=1
npm ci --prefix docs/site --no-audit --no-fund
for brand in host hack; do
  python3 scripts/docs-site.py prepare "$brand"
  rm -rf "internal/webassets/docs-site/$brand"
  (cd docs/site && BRAND="$brand" npm run build)
  python3 scripts/docs-site.py externalize "$brand"
done
python3 scripts/docs-site.py record
python3 scripts/docs-site.py check
