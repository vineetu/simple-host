#!/usr/bin/env bash
# Regenerate the Claude plugin's skills from the single source of truth.
#
#   bash scripts/sync-claude-plugin.sh
#
# plugins/simple-host/ is the "Simple Host" plugin listed in the root
# .claude-plugin/marketplace.json (skills + the remote connector). Its skills
# are plain copies of simple-host-website/skills — never edit them in place.
# Copies, not symlinks: a plugin installed from the marketplace is copied into
# the user's plugin cache, and symlinks leaving the plugin root are rejected.
# The plugin version tracks the skills version in
# simple-host-website/.claude-plugin/plugin.json.
#
# scripts/check-claude-plugin.sh fails if either drifts.
set -euo pipefail
cd "$(dirname "$0")/.."

SRC=simple-host-website/skills
DST=plugins/simple-host/skills
SKILLS="website-deploy website-deploy-builder connect-domain"

rm -rf "$DST"
mkdir -p "$DST"
for s in $SKILLS; do
  cp -R "$SRC/$s" "$DST/$s"
done

version=$(python3 -c 'import json; print(json.load(open("simple-host-website/.claude-plugin/plugin.json"))["version"])')
python3 - "$version" <<'PY'
import json, sys
p = "plugins/simple-host/.claude-plugin/plugin.json"
with open(p) as f:
    man = json.load(f)
man["version"] = sys.argv[1]
with open(p, "w") as f:
    json.dump(man, f, indent=2, ensure_ascii=True)
    f.write("\n")
m = ".claude-plugin/marketplace.json"
with open(m) as f:
    mk = json.load(f)
for e in mk["plugins"]:
    if e["name"] in ("simple-host", "website-deploy"):
        e["version"] = sys.argv[1]
with open(m, "w") as f:
    json.dump(mk, f, indent=2, ensure_ascii=True)
    f.write("\n")
PY
echo "synced $DST ($SKILLS) at version $version"
