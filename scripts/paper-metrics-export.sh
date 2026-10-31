#!/usr/bin/env bash
# Export every paper-metrics set (docs/paper-metrics.md) as CSV, plus the
# summary as JSON. Needs an admin key in SH_KEY (or ~/.sh-vineetu-key).
# One line: bash scripts/paper-metrics-export.sh 2026-10-01 $(date -u +%F) ./paper-data
set -euo pipefail
FROM=${1:-$(date -u -d '89 days ago' +%F)}
TO=${2:-$(date -u +%F)}
OUT=${3:-./paper-metrics-$TO}
# For Hack: SH_BASE=https://simple-hack.app SH_KEY=<Hack admin key> bash scripts/paper-metrics-export.sh
BASE=${SH_BASE:-https://simple-host.app}
KEY=${SH_KEY:-$(cat ~/.sh-vineetu-key)}
mkdir -p "$OUT"
SETS=(daily latency latency_buckets process process_daily footprint footprint_summary api_routes)
if [[ "${BASE%/}" == "https://simple-hack.app" ]]; then
  SETS+=(hack_events hack_minutes hack_latency hack_phases runs)
fi
for set in "${SETS[@]}"; do
  curl -fsS -H "X-API-Key: $KEY" "$BASE/v1/admin/paper-metrics?set=$set&from=$FROM&to=$TO&format=csv" -o "$OUT/$set.csv"
done
curl -fsS -H "X-API-Key: $KEY" "$BASE/v1/admin/paper-metrics?set=latency&all=1&from=$FROM&to=$TO&format=csv" -o "$OUT/latency_all.csv"
curl -fsS -H "X-API-Key: $KEY" "$BASE/v1/admin/paper-metrics?from=$FROM&to=$TO" -o "$OUT/summary.json"
echo "wrote $(ls "$OUT" | wc -l) files to $OUT ($FROM to $TO)"
