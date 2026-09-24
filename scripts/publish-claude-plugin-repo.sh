#!/bin/bash
# Publish plugins/simple-host to the standalone repo github.com/vineetu/simple-host-plugin,
# which is what the Claude plugin directory lists. Run after a skills/plugin release
# (after scripts/sync-claude-plugin.sh). Keeps that repo's README.md, LICENSE, mcp.json and
# .claude-plugin/marketplace.json; updates the Agent Plugins plugin.json (GitHub Copilot) version; replaces the plugin files; bumps the marketplace version.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
SRC="$ROOT/plugins/simple-host"
bash "$ROOT/scripts/check-claude-plugin.sh" >/dev/null
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
git clone -q https://github.com/vineetu/simple-host-plugin.git "$TMP/repo"
cd "$TMP/repo"
rm -rf skills .mcp.json .claude-plugin/plugin.json
cp -R "$SRC/skills" "$SRC/.mcp.json" .
cp "$SRC/.claude-plugin/plugin.json" .claude-plugin/plugin.json
python3 - <<'PY'
import json
p='.claude-plugin/plugin.json'; d=json.load(open(p))
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
if git diff --quiet && git diff --cached --quiet && [ -z "$(git status --porcelain)" ]; then echo "simple-host-plugin already up to date"; exit 0; fi
V=$(python3 -c "import json;print(json.load(open('.claude-plugin/plugin.json'))['version'])")
git add -A
git -c user.name='Vineet Sriram' -c user.email='vineetu@gmail.com' commit -q -m "Simple Host plugin $V (from github.com/vineetu/simple-host plugins/simple-host)"
git tag -f "v$V" >/dev/null
git push -q origin main
git push -q -f origin "v$V"
echo "published simple-host-plugin $V"
