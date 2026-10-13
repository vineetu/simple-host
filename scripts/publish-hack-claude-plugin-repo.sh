#!/bin/bash
# Publish the Simple Hack Claude plugin to the standalone repo github.com/vineetu/simple-hack-plugin,
# which is what the Claude plugin directory lists. Run after a hack-toolkit release
# (after python3 hack-toolkit/build.py). Keeps that repo's README.md, LICENSE and
# .claude-plugin/marketplace.json; replaces the skills, .mcp.json, plugin.json and icon from
# hack-toolkit/; bumps the marketplace version.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
KIT="$ROOT/hack-toolkit"
SKILLS="run-hackathon join-hackathon judge-hackathon website-deploy website-deploy-builder"
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
git clone -q https://github.com/vineetu/simple-hack-plugin.git "$TMP/repo"
cd "$TMP/repo"
rm -rf skills .mcp.json .claude-plugin/plugin.json .claude-plugin/icon.png
mkdir -p skills
for s in $SKILLS; do cp -R "$KIT/skills/$s" skills/; done
cp "$KIT/metadata/claude-mcp.json" .mcp.json
cp "$KIT/metadata/claude-plugin.json" .claude-plugin/plugin.json
cp "$KIT/metadata/claude-icon.png" .claude-plugin/icon.png
python3 - <<'PY'
import json
d=json.load(open('.claude-plugin/plugin.json'))
m='.claude-plugin/marketplace.json'; mk=json.load(open(m))
for pl in mk['plugins']:
    if pl['name']=='simple-hack': pl['version']=d['version']; pl['description']=d['description']
json.dump(mk,open(m,'w'),indent=2); open(m,'a').write('\n')
PY
claude plugin validate . >/dev/null
claude plugin validate .claude-plugin/plugin.json >/dev/null
claude plugin validate .claude-plugin/marketplace.json >/dev/null
if [ -z "$(git status --porcelain)" ]; then echo "simple-hack-plugin already up to date"; exit 0; fi
V=$(python3 -c "import json;print(json.load(open('.claude-plugin/plugin.json'))['version'])")
if [ -n "${DRY_RUN:-}" ]; then echo "simple-hack-plugin would change to $V:"; git status --short; exit 0; fi
git add -A
git -c user.name='Vineet Sriram' -c user.email='vineetu@gmail.com' commit -q -m "Simple Hack plugin $V (from github.com/vineetu/simple-host hack-toolkit)"
git tag -f "v$V" >/dev/null
git push -q origin main
git push -q -f origin "v$V"
echo "published simple-hack-plugin $V"
