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

# Owner /v1/sites/{sitename} routes (except public state/collections/me and OPTIONS)
# must be wrapped with auth.Middleware. A missed wrap is how a site session
# cookie on a custom domain would escalate (UNIFY.md credential boundary).
echo "== owner routes wrapped with authMiddleware =="
unwrapped=$(grep -rhoE 'mux\.Handle(Func)?\("[A-Z]+ /v1/sites/[^"]+"[^)]*' internal/handler \
  | grep -vE '/state"|/me"|/visitor/auth|/collections/\{coll\}' \
  | grep -v authMiddleware || true)
if [ -n "$unwrapped" ]; then
  echo "$unwrapped" | sed 's/^/  FAIL: owner route missing authMiddleware: /'
  fail=1
else
  echo "  ok — listed /v1/sites owner routes pass through authMiddleware"
fi

# Major capabilities that should be discoverable in the LLM/agent docs. Wording
# varies across docs, so match the CONCEPT with a regex rather than an exact
# string.
#
# Held as "name<TAB>regex" lines rather than an associative array: macOS still
# ships bash 3.2, where `declare -A` is a syntax error and `set -u` then aborts
# the script before it ever reports success.
echo "== capability coverage (warn-only) =="
caps='state	state
collections	collection
templates	template
comments	comments\.js
feedback	feedback\.js
visitor-sign-in	visitor.auth|visitor_auth_required|X-SH-CSRF
analytics	analytics'

# A skill may be split into SKILL.md + references/*.md; a capability documented in
# a reference is still discoverable, so search the whole skill directory.
for doc in "$LLMS" "$SKILL_DEPLOY" "$SKILL_BUILD"; do
  [ -e "$doc" ] || { echo "  warn: missing doc: $doc"; continue; }
  case "$doc" in
    */SKILL.md) scope=$(dirname "$doc"); label=$(basename "$(dirname "$doc")") ;;
    *)          scope="$doc";            label=$(basename "$doc") ;;
  esac
  while IFS=$(printf '\t') read -r name pattern; do
    [ -z "$name" ] && continue
    grep -qriE "$pattern" "$scope" || echo "  warn: $label doesn't mention '$name'"
  done <<<"$caps"
done

echo
# ── openapi.json must be a byte-for-byte derivative of openapi.yaml ──
# Both are embedded and both are served (/openapi.yaml and /openapi.json), but
# only the yaml was ever checked. openapi.json silently fell a whole contract
# behind — old response shape, two endpoints missing — while this script printed
# "docs in sync ✓". Anything generating a client from the json got the old shape.
echo "== openapi.json matches openapi.yaml =="
OPENAPI_JSON=internal/handler/static/openapi.json
if [ ! -e "$OPENAPI_JSON" ]; then
  echo "  FAIL: missing $OPENAPI_JSON"
  fail=1
elif ! command -v python3 >/dev/null 2>&1; then
  echo "  FAIL: python3 unavailable — cannot compare openapi.json to openapi.yaml"
  fail=1
else
  if python3 - "$OPENAPI" "$OPENAPI_JSON" <<'PY'
import json, sys, yaml
with open(sys.argv[1]) as f:
    spec_yaml = yaml.safe_load(f)
try:
    with open(sys.argv[2]) as f:
        spec_json = json.load(f)
except Exception as exc:
    print(f"  openapi.json is not valid JSON: {exc}")
    sys.exit(1)
if spec_yaml == spec_json:
    sys.exit(0)
only_yaml = sorted(set(spec_yaml.get("paths", {})) - set(spec_json.get("paths", {})))
only_json = sorted(set(spec_json.get("paths", {})) - set(spec_yaml.get("paths", {})))
for p in only_yaml:
    print(f"  missing from openapi.json: {p}")
for p in only_json:
    print(f"  stale in openapi.json (not in yaml): {p}")
if not only_yaml and not only_json:
    print("  paths match but the specs differ (descriptions/schemas out of sync)")
sys.exit(1)
PY
  then
    echo "  ok — openapi.json is in sync with openapi.yaml"
  else
    echo "  regenerate it:"
    echo "    python3 -c \"import json,yaml; json.dump(yaml.safe_load(open('$OPENAPI')), open('$OPENAPI_JSON','w'), indent=2, ensure_ascii=False)\""
    fail=1
  fi
fi

echo
# ── the analytics parser inlined in index.html and showcase.html ──
# showcase.html is served from the apex AND the content host, so the parser is
# inlined in both pages rather than linked. Two copies only stay identical if
# divergence breaks something, which is this.
echo "== inlined analytics parser =="
if bash scripts/inline-analytics-parser.sh --check; then
  echo "  ok — both pages carry web/analytics-parse.js verbatim"
else
  fail=1
fi
if command -v node >/dev/null 2>&1; then
  if out=$(node web/analytics-parse.test.js 2>&1); then
    echo "  ok — $(tail -n1 <<<"$out") parser fixture tests"
  else
    echo "$out" | sed 's/^/  /'
    fail=1
  fi
else
  # Fail closed. These fixtures are the regression test for a bug that shipped
  # silently; "not run" must not read as "passed".
  echo "  FAIL: node unavailable — parser fixture tests could not run"
  fail=1
fi

echo
if [ "$fail" -ne 0 ]; then
  echo "DRIFT DETECTED — update openapi.yaml (source of truth) to match the routes."
  exit 1
fi
echo "docs in sync ✓"
