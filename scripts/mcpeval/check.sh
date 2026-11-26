#!/usr/bin/env bash
# The connector check, then one summary.
#   PRODUCT=host|hack bash scripts/mcpeval/check.sh      (make connector-check)
# host: the rewritten Simple Host (next/cmd/simple-host-next): build, fixture,
# scripts/mcpjam/host_checks.py against /mcp (51 tools with annotations and
# outputSchema, a read, a publish, an old name through next/compat), next/mcp's
# tool tests (outputs, hints and the ChatGPT kit's tool list) and the kit package.
# hack (cmd/simple-hack): build, fixture, MCPJam conformance, tool annotation
# tests, kit consistency (the submission tool lists and build-openai-plugin.sh)
# and the eval scenarios (run.py). NO_EVALS=1, or no `claude` CLI, skips the
# evals and only measures the tool listing.
# DB_DSN names an empty database for the fixture; without it a throwaway Docker
# Postgres is used. Its server also holds next's test databases and the evals'.
set -uo pipefail
cd "$(dirname "$0")/../.." || exit 1
PRODUCT=${PRODUCT:-host}
case "$PRODUCT" in host | hack) ;; *) echo "PRODUCT must be host or hack" >&2; exit 1 ;; esac
pkg=./products/hack/mcp
export FIX=${FIX:-/tmp/connector-check-$PRODUCT} BIN=${BIN:-/tmp/connector-check-$PRODUCT/simple-$PRODUCT}
export PORT=${PORT:-18190} TLS_PORT=${TLS_PORT:-18443}
rm -rf "$FIX" && mkdir -p "$FIX"
container=
cleanup() {
  [ -f "$FIX/server.pid" ] && kill $(cat "$FIX/server.pid" "$FIX/proxy.pid" 2>/dev/null) 2>/dev/null
  [ -n "$container" ] && docker rm -f -v "$container" >/dev/null 2>&1
}
trap cleanup EXIT
if [ -z "${DB_DSN:-}" ]; then
  pw=$(openssl rand -hex 8)
  container=$(docker run -d --rm -e POSTGRES_PASSWORD="$pw" -p 127.0.0.1::5432 postgres:16-alpine -c max_connections=1000) || exit 1
  pgport=$(docker port "$container" 5432/tcp | head -1 | sed 's/.*://')
  until psql "postgres://postgres:$pw@127.0.0.1:$pgport/postgres" -XAtc 'select 1' >/dev/null 2>&1; do sleep 1; done
  export DB_DSN="postgres://postgres:$pw@127.0.0.1:$pgport/postgres?sslmode=disable"
fi
export EVAL_PG_URL=${EVAL_PG_URL:-${DB_DSN%%\?*}}
EVAL_PG_URL=${EVAL_PG_URL%/*}

rows=()
step() { # step <name> <command...>
  local name=$1 log="$FIX/step-${1// /-}.log"; shift
  if "$@" >"$log" 2>&1; then rows+=("PASS|$name"); else rows+=("FAIL|$name (see $log)"); fi
}
if [ "$PRODUCT" = host ]; then
  step build env CGO_ENABLED=0 go -C next build -o "$BIN" ./cmd/simple-host-next
  step fixture bash scripts/mcpjam/fixture.sh
  step "connector checks" python3 scripts/mcpjam/host_checks.py "$FIX" "$PORT"
  kill $(cat "$FIX/server.pid") 2>/dev/null; rm -f "$FIX"/*.pid
  step "tool tests" env SH_TEST_POSTGRES="${EVAL_PG_URL}/postgres?sslmode=disable" go -C next test ./mcp -run 'TestEveryToolsOutputAndHints|TestSubmissionToolsMatchListing' -count=1
  step "kit package" bash scripts/build-openai-plugin.sh host
else
  step build env CGO_ENABLED=0 go build -o "$BIN" ./cmd/simple-hack
  step fixture bash scripts/mcpjam/fixture.sh
  step "MCPJam conformance" bash scripts/mcpjam/checks.sh
  step "annotation tests" go test "$pkg" -run "TestHackToolAnnotationsFollowTheRule" -count=1
  step "kit tool list" go test "$pkg" -run "TestHackSubmissionToolsMatchChatGPTListing" -count=1
  step "kit package" bash scripts/build-openai-plugin.sh hack
  kill $(cat "$FIX/server.pid" "$FIX/proxy.pid") 2>/dev/null; rm -f "$FIX"/*.pid
  evals=(--size-only)
  [ -z "${NO_EVALS:-}" ] && command -v claude >/dev/null && evals=()
  step evals python3 scripts/mcpeval/run.py hack "${evals[@]}"
fi

echo
echo "connector check: $PRODUCT"
printf '%s\n' "${rows[@]}" | awk -F'|' '{printf "  %-4s  %s\n", $1, $2}'
[ -f "$FIX/step-evals.log" ] && grep -h -E '^# |^vs baseline' "$FIX/step-evals.log"
printf '%s\n' "${rows[@]}" | grep -q '^FAIL' && exit 1
exit 0
