#!/usr/bin/env bash
# SIM-394: runs the real organiser-assistant eval set
# (products/hack/hackhttp/testdata/assistant_eval.json) against the real
# configured model, through a disposable Postgres
# (scripts/test-integration.sh runs products/hack/hackhttp's TestAssistantEval).
# Fails the build on any case failure; "I'm not sure" plus a real link is a
# pass (owner requirement). Never prints a secret: ASSISTANT_API_KEY is
# only ever passed through as an environment variable, never echoed.
#
# Needs ASSISTANT_BASE_URL and ASSISTANT_MODEL already set in the caller's
# environment (usually ASSISTANT_API_KEY and ASSISTANT_REASONING_EFFORT
# too, matching the live server's own settings) -- this script never
# invents or hardcodes them. Without them, there is nothing to check
# against, so it skips rather than failing the build.
#
# A deploy usually keeps those in a root-owned env file instead of the
# shell environment. --from-env-file FILE (or ASSISTANT_ENV_FILE=FILE)
# loads ASSISTANT_* from it via `sudo grep`, without ever printing the
# file's contents:
#   ASSISTANT_ENV_FILE=/etc/simple-hack.env bash scripts/check-hack-assistant-eval.sh
set -uo pipefail
cd "$(dirname "$0")/.."

env_file="${ASSISTANT_ENV_FILE:-}"
if [ "${1:-}" = "--from-env-file" ]; then
  env_file="${2:?--from-env-file needs a path}"
  shift 2
fi
if [ -n "$env_file" ]; then
  while IFS='=' read -r key value; do
    [ -n "$key" ] || continue
    export "$key=$value"
  done < <(sudo grep '^ASSISTANT_' "$env_file")
fi

if [ -z "${ASSISTANT_BASE_URL:-}" ] || [ -z "${ASSISTANT_MODEL:-}" ]; then
  echo "check-hack-assistant-eval: ASSISTANT_BASE_URL/ASSISTANT_MODEL not set; skipping (nothing configured to check against)"
  exit 0
fi

report=$(mktemp)
trap 'rm -f "$report"' EXIT

TEST_PKGS=./products/hack/hackhttp \
  ASSISTANT_EVAL=1 \
  ASSISTANT_EVAL_REPORT="$report" \
  ASSISTANT_BASE_URL="$ASSISTANT_BASE_URL" \
  ASSISTANT_MODEL="$ASSISTANT_MODEL" \
  ASSISTANT_API_KEY="${ASSISTANT_API_KEY:-}" \
  ASSISTANT_REASONING_EFFORT="${ASSISTANT_REASONING_EFFORT:-}" \
  bash scripts/test-integration.sh -run '^TestAssistantEval$'
status=$?

echo
echo "== hack organiser assistant eval: per-case results =="
if [ -s "$report" ]; then
  cat "$report"
else
  echo "(no report written -- the test likely failed before reaching any case; see output above)"
fi

exit "$status"
