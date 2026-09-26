#!/usr/bin/env bash
# FEATURES.md coverage check — FEATURES.md is the blast-radius map, so every
# route and MCP tool must be placed in it.
#
# Fails if a registered route path (a literal mux.Handle/HandleFunc pattern in
# cmd/server or internal/handler, tests excluded) or an MCP tool name
# (internal/mcp/tools.go) does not appear in FEATURES.md. Paths are matched on
# their own (FEATURES.md writes "`POST`/`PUT /v1/...`"), bounded so /v1/sites
# is not satisfied by /v1/sites/{sitename}. Patterns built at runtime
# ("GET /"+name, m+" /mcp") are not literals and are skipped; FEATURES.md
# covers them in prose.
#
# Run: bash scripts/check-features.sh  (also run by scripts/check-docs-sync.sh)
set -u
cd "$(dirname "$0")/.."

FEATURES=FEATURES.md
fail=0

echo "== routes and MCP tools are in FEATURES.md =="
[ -e "$FEATURES" ] || { echo "  FAIL: missing $FEATURES"; exit 1; }

paths=$(grep -rhoE --exclude='*_test.go' 'mux\.Handle(Func)?\("[^"]+",' cmd/server internal/handler \
  | sed -E 's/^mux\.Handle(Func)?\("//; s/",$//' \
  | sed -E 's/^[A-Z]+ //' | sort -u)
nroutes=0
while read -r p; do
  [ -z "$p" ] && continue
  nroutes=$((nroutes + 1))
  # Escape regex metacharacters, then require a non-path character (or end of
  # line) after the path.
  re=$(printf '%s' "$p" | sed -E 's/[][\.*^$+?(){}|/]/\\&/g')
  grep -qE "${re}([^A-Za-z0-9_./{}-]|\$)" "$FEATURES" || { echo "  FAIL: route not in $FEATURES:  $p"; fail=1; }
done <<<"$paths"

tools=$(grep -oE 'Name:[[:space:]]*"[a-z_]+"' internal/mcp/tools.go | sed -E 's/.*"([a-z_]+)"/\1/' | sort -u)
ntools=0
while read -r t; do
  [ -z "$t" ] && continue
  ntools=$((ntools + 1))
  grep -qE "\`${t}\`" "$FEATURES" || { echo "  FAIL: MCP tool not in $FEATURES:  $t"; fail=1; }
done <<<"$tools"

if [ "$nroutes" -eq 0 ] || [ "$ntools" -eq 0 ]; then
  echo "  FAIL: found $nroutes routes and $ntools tools — the extraction is broken"
  exit 1
fi
if [ "$fail" -ne 0 ]; then
  echo "  add each one to its feature section in $FEATURES"
  exit 1
fi
echo "  ok — all $nroutes route paths and $ntools MCP tools are placed in $FEATURES"
