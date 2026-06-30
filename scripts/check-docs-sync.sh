#!/usr/bin/env bash
# Docs drift check — keeps the API surface and its docs in sync.
#
# openapi.yaml is the SOURCE OF TRUTH for the HTTP API. This script asserts that
# the set of registered /v1 routes in the Go source matches the paths documented
# in openapi.yaml (a hard failure if they diverge), and warns when a major
# user-facing capability is missing from llms.txt or the skills (those are
# curated prose, so it's a nudge, not a failure).
#
# Run it before building/deploying:  bash scripts/check-docs-sync.sh
set -u
cd "$(dirname "$0")/.."

OPENAPI=internal/handler/static/openapi.yaml
LLMS=internal/handler/static/llms.txt
SKILL_DEPLOY=simple-host-website/skills/website-deploy/SKILL.md
SKILL_BUILD=simple-host-website/skills/website-deploy-builder/SKILL.md

fail=0

# Registered /v1 routes from the Go source (method+path), minus OPTIONS preflight.
routes=$(grep -rhoE 'mux\.Handle(Func)?\("[A-Z]+ /v1/[^"]+"' internal/ cmd/ \
  | sed -E 's/.*"([A-Z]+) (\/v1\/[^"]+)"/\1 \2/' \
  | grep -vE '^OPTIONS ' \
  | awk '{print $2}' | sort -u)

# Paths documented in openapi.yaml (top-level keys under paths:).
documented=$(grep -oE '^  /v1/[^:]+:' "$OPENAPI" | sed -E 's/^  (\/v1\/[^:]+):/\1/' | sort -u)

echo "== routes vs openapi.yaml =="
while read -r p; do
  [ -z "$p" ] && continue
  grep -qxF "$p" <<<"$documented" || { echo "  FAIL: route not in openapi.yaml:  $p"; fail=1; }
done <<<"$routes"
while read -r p; do
  [ -z "$p" ] && continue
  grep -qxF "$p" <<<"$routes" || { echo "  FAIL: openapi.yaml documents a missing route:  $p"; fail=1; }
done <<<"$documented"
[ "$fail" -eq 0 ] && echo "  ok — every /v1 route is documented and vice versa"

# Major capabilities that should be discoverable in the LLM/agent docs. Wording
# varies across docs (e.g. "view-lock" vs "view-password"), so match the CONCEPT
# with a regex rather than an exact string.
echo "== capability coverage (warn-only) =="
declare -A caps=(
  [state]='state'
  [collections]='collection'
  [private-pages]='view-?lock|view.password'
  [templates]='template'
  [comments]='comments\.js'
  [feedback]='feedback\.js'
)
for doc in "$LLMS" "$SKILL_DEPLOY" "$SKILL_BUILD"; do
  for c in "${!caps[@]}"; do
    grep -qiE "${caps[$c]}" "$doc" || echo "  warn: $(basename "$(dirname "$doc")")/$(basename "$doc") doesn't mention '$c'"
  done
done

echo
if [ "$fail" -ne 0 ]; then
  echo "DRIFT DETECTED — update openapi.yaml (source of truth) to match the routes."
  exit 1
fi
echo "docs in sync ✓"
