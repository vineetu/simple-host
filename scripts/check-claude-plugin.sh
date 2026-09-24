#!/usr/bin/env bash
# The Claude plugin (plugins/simple-host) must carry exactly the source skills
# at the source version. Fix drift with: bash scripts/sync-claude-plugin.sh
set -u
cd "$(dirname "$0")/.."

SRC=simple-host-website/skills
DST=plugins/simple-host/skills
SKILLS="website-deploy website-deploy-builder connect-domain"
fail=0

echo "== claude plugin skills match simple-host-website/skills =="
for s in $SKILLS; do
  if [ ! -d "$DST/$s" ]; then
    echo "  FAIL: missing $DST/$s"; fail=1; continue
  fi
  if ! diff -r "$SRC/$s" "$DST/$s" >/dev/null; then
    echo "  FAIL: $DST/$s differs from $SRC/$s"; fail=1
  fi
done
for d in "$DST"/*; do
  [ -e "$d" ] || continue
  case " $SKILLS " in *" $(basename "$d") "*) ;; *) echo "  FAIL: unexpected $d"; fail=1 ;; esac
done
if find plugins/simple-host -type l | grep -q .; then
  echo "  FAIL: symlinks in plugins/simple-host"; fail=1
fi
[ "$fail" -eq 0 ] && echo "  ok — $SKILLS are verbatim copies"

echo "== claude plugin version matches skills version =="
if python3 - <<'PY'
import json, sys
src = json.load(open("simple-host-website/.claude-plugin/plugin.json"))["version"]
plug = json.load(open("plugins/simple-host/.claude-plugin/plugin.json"))
mk = {e["name"]: e for e in json.load(open(".claude-plugin/marketplace.json"))["plugins"]}
bad = []
if plug.get("version") != src: bad.append(f"plugins/simple-host plugin.json {plug.get('version')} != {src}")
for n in ("simple-host", "website-deploy"):
    if n not in mk: bad.append(f"marketplace.json has no {n} entry")
    elif mk[n].get("version") != src: bad.append(f"marketplace.json {n} {mk[n].get('version')} != {src}")
if mk.get("simple-host", {}).get("source") != "./plugins/simple-host": bad.append("marketplace simple-host source != ./plugins/simple-host")
mcp = json.load(open("plugins/simple-host/.mcp.json"))["mcpServers"]
if mcp != {"simple-host": {"type": "http", "url": "https://simple-host.app/mcp"}}: bad.append(f".mcp.json unexpected: {mcp}")
for b in bad: print("  FAIL:", b)
sys.exit(1 if bad else 0)
PY
then echo "  ok — plugin, marketplace and skills agree"; else fail=1; fi

if [ "$fail" -ne 0 ]; then
  echo "CLAUDE PLUGIN DRIFT — run: bash scripts/sync-claude-plugin.sh"
  exit 1
fi
echo "claude plugin in sync ✓"
