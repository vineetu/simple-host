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
routes=$(grep -rh --exclude='*_test.go' -oE 'mux\.Handle(Func)?\("[A-Z]+ /v1/[^"]+"' internal/ cmd/ \
  | sed -E 's/.*"([A-Z]+) (\/v1\/[^"]+)"/\1 \2/' \
  | grep -vE '^OPTIONS ' \
  | grep -vE ' /v1/setup/' \
  | awk '{print $2}' | sed -E 's/\{([[:alnum:]_]+)\.\.\.\}/{\1}/g' | sort -u)

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
# cookie on a custom domain would escalate (docs/history/UNIFY.md credential boundary).
# .../collections/{coll}/items/{id} (private-list edit/delete) is excluded on
# purpose: privateManager authorizes it itself (owner or admin key, or the
# owner's own visitor session on the site's own address; 404 for anyone else).
echo "== owner routes wrapped with authMiddleware =="
unwrapped=$(grep -rh --exclude='*_test.go' -oE 'mux\.Handle(Func)?\("[A-Z]+ /v1/sites/[^"]+"[^)]*' internal/handler \
  | grep -vE '/state"|/me"|/visitor/auth|/collections/\{coll\}"|/collections/\{coll\}/items/\{id\}"' \
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
visitor-sign-in	visitor.auth|visitor_auth_required|custom_domain_required|auth\.js|X-SH-CSRF
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

# ── served assets that name the host must go through the rewriter ──
# An instance on another domain has to describe itself. Any static asset that
# names simple-host.app but is NOT in rewrittenAssets would tell a hackathon's
# participants to publish to the public instance instead of their own.
echo "== assets naming the host are rewritten =="
listed=$(sed -n '/^var rewrittenAssets = \[\]string{/,/^}/p' internal/handler/instancehost.go \
  | grep -oE '"[^"]+"' | tr -d '"')
missing=""
for f in internal/handler/static/*; do
  base=$(basename "$f")
  case "$base" in swagger-ui*) continue ;; esac
  grep -q "simple-host\.app" "$f" 2>/dev/null || continue
  grep -qxF "$base" <<<"$listed" && continue
  # Pages served through serveStaticPage are templates, not agent-facing docs;
  # they are allowed to name the canonical host in prose.
  case "$base" in admin.html|analytics.html|notfound.html|showcase.html|index.html|features.html|architecture.html|privacy.html|terms.html|support.html|docs.html|enterprise.html|enterprise-brief.html|enterprise-architecture.html|hackathons.html|setup.html) continue ;; esac
  missing="$missing $base"
done
if [ -n "$missing" ]; then
  echo "  FAIL: names simple-host.app but is not in rewrittenAssets:$missing"
  fail=1
else
  echo "  ok — every agent-facing asset naming the host is rewritten"
fi

# ── a skill fetched over the web must be able to reach its own references ──
# SKILL.md is served at /skills/<name>/SKILL.md but references live under
# /v1/skills/<name>/references/. An agent resolving a relative path against the
# first gets a 404, so every reference must also be cited by full URL. Codex
# following the hackathon skill hit exactly this and could read none of them.
echo "== skill references are reachable by URL =="
missing=""
for skill in simple-host-website/skills/*/; do
  name=$(basename "$skill")
  [ -d "$skill/references" ] || continue
  for ref in "$skill"references/*.md; do
    base=$(basename "$ref")
    grep -q "v1/skills/$name/references/$base" "$skill/SKILL.md" "$skill"references/*.md 2>/dev/null \
      || missing="$missing $name/$base"
  done
done
if [ -n "$missing" ]; then
  echo "  FAIL: reference cited only by relative path, unreachable over the web:$missing"
  fail=1
else
  echo "  ok — every skill reference is cited by full URL somewhere"
fi

# ── the canonical docs give the site address, not an older form ──
# Owner decision 2026-09-26: a site's address is https://<site>.<handle>.simple-host.app/.
# Two older forms still work and redirect: the shared path form
# sites.simple-host.app/<handle>/<site>/ and the person-path form
# <handle>.simple-host.app/<site>/ (which is also the brief fallback for a person
# whose certificate is not issued yet). A line may name either only while saying
# so: the shared form needs "old", "legacy" or "redirect"; the person-path form
# needs one of those or "fallback", "briefly", "brand-new", "new account",
# "until" or "pending". run-hackathon is exempt — event instances keep the path
# model on sites.<their-domain>/<handle>/<project>/.
echo "== docs use the site address =="
canon_docs=(simple-host-website/skills "$LLMS" "$OPENAPI" internal/handler/static/*.html openai-plugin/skills internal/mcp/instructions.go internal/mcp/outputs.go internal/mcp/tools.go internal/handler/generate.go)
old_addr='sites\.(simple-host\.app|<[^>/]+>|\{[^}/]+\}|&lt;[^/]+&gt;)/(<|\{|&lt;)'
offending=$(grep -rnE "$old_addr" "${canon_docs[@]}" 2>/dev/null \
  | grep -v '^simple-host-website/skills/run-hackathon/' \
  | grep -viE 'old|legacy|redirect' || true)
person_path='(<handle>|\{handle\}|&lt;handle&gt;)\.simple-host\.app/(<(site|sitename|name)>|\{(site|sitename|name)\}|&lt;(site|sitename|name)&gt;)'
offending_pp=$(grep -rnE "$person_path" "${canon_docs[@]}" 2>/dev/null \
  | grep -v '^simple-host-website/skills/run-hackathon/' \
  | grep -viE 'old|legacy|redirect|fallback|briefly|brand-new|new account|until|pending' || true)
if [ -n "$offending" ]; then
  echo "$offending" | cut -c1-200 | sed 's/^/  FAIL: shared path form as the address (say "old"\/"legacy"\/"redirect" or use <site>.<handle>.simple-host.app): /'
  fail=1
fi
if [ -n "$offending_pp" ]; then
  echo "$offending_pp" | cut -c1-200 | sed 's/^/  FAIL: person-path form as the address (say "old"\/"redirect"\/"fallback"\/"briefly" or use <site>.<handle>.simple-host.app): /'
  fail=1
fi
missing_site=""
for doc in "$LLMS" "$OPENAPI" "$SKILL_DEPLOY" "$SKILL_BUILD" simple-host-website/skills/connect-domain/SKILL.md internal/mcp/instructions.go; do
  grep -qE '<(site|sitename|name)>\.<handle>\.simple-host\.app' "$doc" || missing_site="$missing_site $doc"
done
if [ -n "$missing_site" ]; then
  echo "  FAIL: does not give the site address <site>.<handle>.simple-host.app:$missing_site"
  fail=1
fi
if [ -z "$offending$offending_pp$missing_site" ]; then
  echo "  ok — canonical docs give <site>.<handle>.simple-host.app; older forms appear only as old/redirect/fallback"
fi

# ── FEATURES.md places every route and MCP tool ──
bash scripts/check-features.sh || fail=1

echo
if [ "$fail" -ne 0 ]; then
  echo "DRIFT DETECTED — update openapi.yaml (source of truth) to match the routes."
  exit 1
fi
echo "docs in sync ✓"
