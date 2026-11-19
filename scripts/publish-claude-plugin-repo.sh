#!/bin/bash
# Publish the Simple Host Claude plugin to the standalone repo github.com/vineetu/simple-host-plugin,
# which is what the Claude plugin directory lists. The skills come from the one source the
# server serves (host/webassets/simple-host-website/skills) and the version from its
# plugin.json; plugins/simple-host holds only the manifest, .mcp.json and icon.
# Keeps that repo's README.md, LICENSE, mcp.json and .claude-plugin/marketplace.json; updates
# the Agent Plugins plugin.json (GitHub Copilot) version; bumps the marketplace version.
# DRY_RUN=1 shows what would change; REPO=<path or URL> clones another target.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
SRC="$ROOT/plugins/simple-host"
SKILLS="$ROOT/host/webassets/simple-host-website/skills"
VERSION_FROM="$ROOT/host/webassets/simple-host-website/.claude-plugin/plugin.json"
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
git clone -q "${REPO:-https://github.com/vineetu/simple-host-plugin.git}" "$TMP/repo"
cd "$TMP/repo"
rm -rf skills .mcp.json .claude-plugin/plugin.json .claude-plugin/icon.png
mkdir -p .claude-plugin
cp -R "$SKILLS" skills
cp "$SRC/.mcp.json" .
cp "$SRC/.claude-plugin/plugin.json" .claude-plugin/plugin.json
cp "$SRC/.claude-plugin/icon.png" .claude-plugin/icon.png
VERSION_FROM="$VERSION_FROM" python3 - <<'PY'
import json, os
p='.claude-plugin/plugin.json'; d=json.load(open(p))
d['version']=json.load(open(os.environ['VERSION_FROM']))['version']
d['repository']='https://github.com/vineetu/simple-host-plugin'
json.dump(d,open(p,'w'),indent=2); open(p,'a').write('\n')
ap='plugin.json'; a=json.load(open(ap)); a['version']=d['version']; a['description']=d['description']
json.dump(a,open(ap,'w'),indent=2); open(ap,'a').write('\n')
m='.claude-plugin/marketplace.json'; mk=json.load(open(m))
for pl in mk['plugins']:
    if pl['name']=='simple-host': pl['version']=d['version']; pl['description']=d['description']
json.dump(mk,open(m,'w'),indent=2); open(m,'a').write('\n')
PY
claude plugin validate . >/dev/null
claude plugin validate .claude-plugin/plugin.json >/dev/null
if git diff --quiet && git diff --cached --quiet && [ -z "$(git status --porcelain)" ]; then echo "simple-host-plugin already up to date"; exit 0; fi
V=$(python3 -c "import json;print(json.load(open('.claude-plugin/plugin.json'))['version'])")
if [ -n "${DRY_RUN:-}" ]; then echo "simple-host-plugin would change to $V:"; git status --short; exit 0; fi
git add -A
git -c user.name='Vineet Sriram' -c user.email='vineetu@gmail.com' commit -q -m "Simple Host plugin $V (from github.com/vineetu/simple-host)"
git tag -f "v$V" >/dev/null
git push -q origin main
git push -q -f origin "v$V"
echo "published simple-host-plugin $V"
