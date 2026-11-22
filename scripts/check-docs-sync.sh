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

OPENAPI=host/webassets/static/openapi.yaml
LLMS=host/webassets/static/llms.txt
SKILL_DEPLOY=host/webassets/simple-host-website/skills/website-deploy/SKILL.md
SKILL_BUILD=host/webassets/simple-host-website/skills/website-deploy-builder/SKILL.md

fail=0
python3 scripts/check-hack-setup.py || fail=1
python3 scripts/build-hack-navigation.py --check || fail=1

# Registered /v1 routes from the Go source (method+path), minus OPTIONS preflight
#. Setup operations are accounted for as internal.
routes=$(go run ./scripts/inventory routes \
  | awk -F '\t' '$1 != "OPTIONS" && $2 ~ /^\/v1\// {print $2}' \
  | sed -E 's/\{([[:alnum:]_]+)\.\.\.\}/{\1}/g' | sort -u)

# Paths documented in the two authored sources (platform spec and Hack part).
documented=$(python3 - <<'PYDOC'
import yaml
from pathlib import Path
paths = set()
for name in ('host/webassets/static/openapi.yaml', 'products/hack/webassets/openapi.part.yaml'):
    paths.update(path for path in (yaml.safe_load(Path(name).read_text()).get('paths') or {}) if path.startswith('/v1/'))
print('\n'.join(sorted(paths)))
PYDOC
)

python3 scripts/tests/compose-openapi.test.py || fail=1

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

# Owner /v1/sites/{sitename} routes (except the visitor /me and sign-in
# routes, and OPTIONS) must be wrapped with auth.Middleware. A missed wrap is
# how a site session cookie on a custom domain would escalate
# (docs/history/UNIFY.md credential boundary).
# .../push/* (SIM-360) is excluded the same way /storage/ is: a signed-in
# visitor subscribes/unsubscribes/mutes their own device too, so these go
# through h.passcodeGate (TestPasscodeRouteTable enforces that wrap) and
# each handler checks ownership itself (host/data/push/routes.go), exactly the
# storage-route pattern. The owner-only device/rule management routes
# (push/devices, push/rules) are still wrapped with authMiddleware.
echo "== owner routes wrapped with authMiddleware =="
unwrapped=$(grep -rh --exclude='*_test.go' -oE 'mux\.Handle(Func)?\("[A-Z]+ /v1/sites/[^"]+"[^)]*' host/handler host/web host/access host/data host/sites \
  | grep -vE '/me"|/visitor/auth|/storage/|/push/(vapid-key|subscribe|unsubscribe|mute)"' \
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
caps='storage	storage|SH\.storage
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
# ── the analytics parser inlined in index.html and showcase.html ──
# showcase.html is served from the apex AND the content host, so the parser is
# inlined in both pages rather than linked. Two copies only stay identical if
# divergence breaks something, which is this.
echo "== inlined analytics parser =="
if bash scripts/inline-analytics-parser.sh --check; then
  echo "  ok — both pages carry host/webassets/analytics-parse.js verbatim"
else
  fail=1
fi
if command -v node >/dev/null 2>&1; then
  if out=$(node host/webassets/analytics-parse.test.js 2>&1); then
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

# auth.js's API address per page host (the moving-base copy is run by
# TestAuthJSBases in host/handler).
echo "== auth.js picks the API address =="
if command -v node >/dev/null 2>&1; then
  if out=$(node host/webassets/auth-base.test.js 2>&1); then
    echo "  ok — $(tail -n1 <<<"$out" | sed 's/^# //')"
  else
    echo "$out" | sed 's/^/  /'
    fail=1
  fi
else
  echo "  FAIL: node unavailable — auth.js address tests could not run"
  fail=1
fi

# ── served assets that name the host must go through the rewriter ──
# An instance on another domain has to describe itself. Any static asset that
# names simple-host.app but is NOT in rewrittenAssets would tell a hackathon's
# participants to publish to the public instance instead of their own.
echo "== assets naming the host are rewritten =="
listed=$(sed -n '/^var RewrittenAssets = \[\]string{/,/^}/p' host/web/instancehost.go \
  | grep -oE '"[^"]+"' | tr -d '"')
missing=""
for f in host/webassets/static/*; do
  base=$(basename "$f")
  grep -q "simple-host\.app" "$f" 2>/dev/null || continue
  grep -qxF "$base" <<<"$listed" && continue
  # Pages served through serveStaticPage are templates, not agent-facing docs;
  # they are allowed to name the canonical host in prose. features.json is the
  # data behind features.html (scripts/build-features-page.py), same rule.
  case "$base" in features.json|admin.html|analytics.html|notfound.html|showcase.html|index.html|features.html|pricing.html|architecture.html|privacy.html|terms.html|support.html|report.html|enterprise.html|enterprise-brief.html|enterprise-architecture.html|hackathons.html|hack-home.html|hack-story.html|host-story.html|hack-get-started.html|setup.html|costs.html) continue ;; esac
  missing="$missing $base"
done
if [ -n "$missing" ]; then
  echo "  FAIL: names simple-host.app but is not in rewrittenAssets:$missing"
  fail=1
else
  echo "  ok — every agent-facing asset naming the host is rewritten"
fi

# ── the canonical docs give the site address, not an older form ──
# Owner decision 2026-09-26: a site's address is https://<site>.<handle>.simple-host.app/.
# Owner decision 2026-10-09 pauses the base move; pages, spec, connector
# and skills all write addresses under simple-host.app.
# Two older forms still work and redirect: the shared path form
# sites.simple-host.app/<handle>/<site>/ and the person-path form
# <handle>.simple-host.app/<site>/ (which is also the brief fallback for a person
# whose certificate is not issued yet). A line may name either only while saying
# so: the shared form needs "old", "legacy" or "redirect"; the person-path form
# needs one of those or "fallback", "briefly", "brand-new", "new account",
# "until" or "pending". run-hackathon is exempt — event instances keep the path
# model on sites.<their-domain>/<handle>/<project>/.
echo "== docs use the site address =="
canon_docs=(host/webassets/simple-host-website/skills "$LLMS" "$OPENAPI" host/webassets/static/*.html host/mcp/instructions.go host/mcp/outputs.go host/mcp/tools.go )
old_addr='sites\.(simple-host\.app|<[^>/]+>|\{[^}/]+\}|&lt;[^/]+&gt;)/(<|\{|&lt;)'
offending=$(grep -rnE "$old_addr" "${canon_docs[@]}" 2>/dev/null \
  | grep -v '^host/webassets/simple-host-website/skills/run-hackathon/' \
  | grep -viE 'old|legacy|redirect' || true)
person_path='(<handle>|\{handle\}|&lt;handle&gt;)\.simple-host\.(app|site)/(<(site|sitename|name)>|\{(site|sitename|name)\}|&lt;(site|sitename|name)&gt;)'
offending_pp=$(grep -rnE "$person_path" "${canon_docs[@]}" 2>/dev/null \
  | grep -v '^host/webassets/simple-host-website/skills/run-hackathon/' \
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
for doc in "$LLMS" "$OPENAPI" "$SKILL_DEPLOY" "$SKILL_BUILD" host/webassets/simple-host-website/skills/connect-domain/SKILL.md host/mcp/instructions.go; do
  grep -qE '<(site|sitename|name)>\.<handle>\.simple-host\.(app|site)' "$doc" || missing_site="$missing_site $doc"
done
if [ -n "$missing_site" ]; then
  echo "  FAIL: does not give the site address <site>.<handle>.simple-host.app:$missing_site"
  fail=1
fi
if [ -z "$offending$offending_pp$missing_site" ]; then
  echo "  ok — canonical docs give <site>.<handle>.simple-host.app; older forms appear only as old/redirect/fallback"
fi

# ── Simple Hack onboarding agrees with its docs and retained downloads ──
echo "== Simple Hack get started =="
python3 - <<'PY' || fail=1
from html.parser import HTMLParser
from pathlib import Path

class Onboarding(HTMLParser):
    def __init__(self):
        super().__init__()
        self.other = False
        self.links = []
        self.command = ''
        self.in_command = False
    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == 'details' and attrs.get('id') == 'other-installs':
            assert 'open' not in attrs, 'Other ways to install must start collapsed'
            self.other = True
        if tag == 'a':
            self.links.append((attrs.get('href', ''), self.other))
        if tag == 'code' and attrs.get('id') == 'install-command':
            self.in_command = True
    def handle_endtag(self, tag):
        if tag == 'details':
            self.other = False
        if tag == 'code':
            self.in_command = False
    def handle_data(self, data):
        if self.in_command:
            self.command += data

page = Path('products/hack/webassets/static/hack-get-started.html').read_text()
parsed = Onboarding()
parsed.feed(page)
assert parsed.command and parsed.command in Path('README.md').read_text(), 'README install command differs from onboarding'
assert 'tree/main/products/hack/webassets/hack-toolkit' in parsed.command, 'Install only the hosted Hack skill set'
required = ['/skills.zip', '/hack-skills.zip']
required += ['/v1/skills/' + name + '/SKILL.md' for name in ['run-hackathon', 'join-hackathon', 'judge-hackathon', 'website-deploy', 'website-deploy-builder']]
for url in required:
    assert (url, True) in parsed.links, f'{url} missing from collapsed other installs'
assert all(other for url, other in parsed.links if url.startswith('/v1/skills/')), 'Skill reading belongs under other installs'
for doc in ['README.md', 'docs/FEATURES.md', 'ARCHITECTURE.md', 'INTENT.md', 'host/webassets/static/llms.txt']:
    if doc != 'host/webassets/static/llms.txt':
        assert '/get-started' in Path(doc).read_text(), f'{doc} must describe hosted onboarding'
print('  ok — five-skill install command, onboarding docs and tucked-away downloads agree')
PY

# Local settings tables, defaults, small-box passthrough and generated copy.
# Enterprise sources are compared separately by check-release.
echo "== settings docs (local only) =="
python3 scripts/settings_docs.py --check --local-only || fail=1
bash scripts/check-profiles.sh || fail=1

echo
if [ "$fail" -ne 0 ]; then
  echo "DRIFT DETECTED. A FAIL about a route: document it in host/webassets/static/openapi.yaml"
  echo "(or products/hack/webassets/openapi.part.yaml for Simple Hack). Then, for any FAIL, run: make inventory"
  exit 1
fi
echo "docs in sync ✓"
