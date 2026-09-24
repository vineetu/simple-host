#!/usr/bin/env bash
# Build the OpenAI plugin package (ChatGPT + Codex directory, "With MCP").
#
#   bash scripts/build-openai-plugin.sh            # the two zips below
#   FALLBACK=1 bash scripts/build-openai-plugin.sh # also the skills-only fallback
#
# Outputs, all with the plugin files at the ZIP ROOT (the portal accepts the
# plugin root "at the archive root or in one top-level directory"; root is the
# unambiguous one):
#
#   dist/simple-host-openai-plugin.zip   plugin.json, mcp.json, skills/, assets/
#       The whole portable package: the reference artifact, and what a local
#       marketplace install or any upload that takes a full package uses.
#   dist/simple-host-openai-skills.zip   skills/<name>/SKILL.md only (the portal Skills tab upload)
#       The skill bundle for the With MCP draft's Skills tab. The MCP server
#       itself is entered in the portal's MCP tab by URL, never uploaded, and a
#       skills upload must not carry MCP configuration
#       (mcp_configuration_excluded).
#   dist/website-deploy-toolkit-skills-only-fallback.zip   (FALLBACK=1 only)
#       Only if the portal will not let the existing Skills-only listing gain
#       an MCP server: the repo's own skills (connector-first, with the email
#       code fallback), since without the server the connector-only skills
#       would have no tools to call.
#
# Every check the portal documents for the package is run here first, so a
# failure shows up on this box rather than as a portal error code.
set -euo pipefail
cd "$(dirname "$0")/.."
SRC=openai-plugin
OUT=dist
mkdir -p "$OUT"

python3 - "$SRC" <<'PY'
import json, os, re, sys, struct, zlib
src = sys.argv[1]
problems = []
def fail(msg): problems.append(msg)

man = json.load(open(os.path.join(src, "plugin.json")))
mcp = json.load(open(os.path.join(src, "mcp.json")))
if man.get("$schema") != "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json": fail("plugin.json $schema")
if mcp.get("$schema") != "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json": fail("mcp.json $schema")
name = man.get("name", "")
if not re.fullmatch(r"(?!.*(?:--|\.\.))[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?", name) or len(name) > 64: fail("plugin name format")
if not re.fullmatch(r"\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.+-]+)?", man.get("version", "")): fail("version is not semver")
if not man.get("description") or len(man["description"]) > 1024: fail("description missing or > 1024")
i = man["extensions"]["com.openai"]["interface"]
if man.get("author", {}).get("name") != i.get("developerName"): fail("author.name must equal interface.developerName (developer_name_defaulted)")
for k, lim in (("displayName", 30), ("shortDescription", 30), ("developerName", 80)):
    v = i.get(k, "")
    if not v or len(v) > lim or "\n" in v: fail(f"{k} missing, multi-line or > {lim}")
if not i.get("longDescription") or len(i["longDescription"]) > 4000: fail("longDescription")
if i.get("category") not in {"Productivity", "Creativity", "Developer Tools", "Business & Operations", "Data & Analytics", "Communication",
                             "Education & Research", "Security", "Finance", "Healthcare", "Travel", "Entertainment", "Other"}: fail("category")
caps = i.get("capabilities", [])
if len(caps) > 20 or any(not c or len(c) > 120 or "\n" in c for c in caps): fail("capabilities")
prompts = i.get("defaultPrompt", [])
norm = [" ".join(p.split()).lower() for p in prompts]
if len(prompts) > 3 or len(set(norm)) != len(norm) or any(not p or len(p) > 128 or "\n" in p or "@" in p for p in prompts): fail("defaultPrompt")
for k in ("websiteURL", "supportURL", "privacyPolicyURL", "termsOfServiceURL"):
    v = i.get(k, "")
    if not v.startswith("https://") or len(v) > 1024: fail(f"{k} must be https (required for remote MCP)")
if "screenshots" in i: fail("screenshots are only allowed when the MCP server returns UI (screenshots_not_allowed)")
def lum(h):
    c = [int(h[j:j + 2], 16) / 255 for j in (1, 3, 5)]
    c = [x / 12.92 if x <= 0.03928 else ((x + 0.055) / 1.055) ** 2.4 for x in c]
    return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2]
def contrast(a, b):
    la, lb = lum(a), lum(b)
    return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
for k, bg in (("brandColor", "#FFFFFF"), ("brandColorDark", "#212121")):
    v = i.get(k)
    if v and (not re.fullmatch(r"#[0-9A-Fa-f]{6}", v) or contrast(v, bg) < 2): fail(f"{k} format or contrast")
def png_size(path):
    with open(path, "rb") as f:
        head = f.read(24)
    if head[:8] != b"\x89PNG\r\n\x1a\n": return None
    return struct.unpack(">II", head[16:24])
for k in ("composerIcon", "logo"):
    p = i.get(k, "")
    if not p.startswith("./assets/"): fail(f"{k} must be under ./assets/"); continue
    size = png_size(os.path.join(src, p[2:]))
    if not size or size[0] != size[1] or size[0] < 48 or size[0] > 4096: fail(f"{k} must be a square PNG 48..4096 px")
servers = mcp.get("mcpServers", {})
if not servers or any(s.get("type") != "streamable-http" or not s.get("url", "").startswith("https://") for s in servers.values()): fail("mcp.json: one streamable-http https server")

try:
    import yaml
except ImportError:
    yaml = None
skills_dir = os.path.join(src, "skills")
names = set()
for d in sorted(os.listdir(skills_dir)):
    path = os.path.join(skills_dir, d, "SKILL.md")
    if d.startswith(".") or not os.path.isfile(path): fail(f"skills/{d}: not a skill directory"); continue
    text = open(path, encoding="utf-8").read()
    m = re.match(r"---\n(.*?)\n---\n(.*)", text, re.S)
    if not m: fail(f"{d}: front matter"); continue
    fm = yaml.safe_load(m.group(1)) if yaml else dict(l.split(": ", 1) for l in m.group(1).splitlines())
    if fm.get("name") != d: fail(f"{d}: front matter name must equal the directory")
    if not fm.get("description") or len(fm["description"]) > 1024: fail(f"{d}: description")
    if not m.group(2).strip(): fail(f"{d}: empty body")
    if len(f"{name}:{d}") > 64: fail(f"{d}: plugin:skill identity > 64")
    if len(text.encode()) > 256 * 1024: fail(f"{d}: SKILL.md > 256 KiB")
    for bad in ("X-API-Key", "api_key", "curl ", "Claude"):
        if bad in text: fail(f"{d}: mentions {bad!r}; connector skills never ask for keys or use curl, and stay provider-neutral")
    names.add(d)
if not names: fail("no skills")
if problems:
    print("openai-plugin checks FAILED:"); [print("  -", p) for p in problems]; sys.exit(1)
print(f"openai-plugin checks ok: {name} {man['version']}, skills: {', '.join(sorted(names))}")
PY

# Deterministic archives: fixed order, fixed timestamps, no extra attributes,
# no dotfiles, regular files only.
build_zip() { # build_zip <out.zip> <staging dir>
  local out="$1" stage="$2"
  rm -f "$out"
  (cd "$stage" && find . -type f ! -name '.*' | sed 's|^\./||' | LC_ALL=C sort | while read -r f; do touch -h -d '2026-01-01T00:00:00Z' "$f"; echo "$f"; done | zip -q -X -D "$OLDPWD/$out" -@)
}

STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT

mkdir -p "$STAGE/full" "$STAGE/skills"
cp "$SRC/plugin.json" "$SRC/mcp.json" "$STAGE/full/"
cp -R "$SRC/skills" "$SRC/assets" "$STAGE/full/"
# The portal's Skills tab takes only skill folders: "one skill root or one
# directory of skill roots". No plugin.json or assets alongside them.
cp -R "$SRC/skills" "$STAGE/skills/"
build_zip "$OUT/simple-host-openai-plugin.zip" "$STAGE/full"
build_zip "$OUT/simple-host-openai-skills.zip" "$STAGE/skills"

if [ "${FALLBACK:-}" = 1 ]; then
  mkdir -p "$STAGE/fallback/skills"
  cp "$SRC/plugin.json" "$STAGE/fallback/"
  cp -R "$SRC/assets" "$STAGE/fallback/"
  for s in website-deploy website-deploy-builder connect-domain; do cp -R "simple-host-website/skills/$s" "$STAGE/fallback/skills/"; done
  build_zip "$OUT/website-deploy-toolkit-skills-only-fallback.zip" "$STAGE/fallback"
fi

for z in "$OUT"/simple-host-openai-plugin.zip; do
  python3 - "$z" <<'PY'
import sys, zipfile
z = zipfile.ZipFile(sys.argv[1])
names = z.namelist()
assert z.testzip() is None
assert "plugin.json" in names, "plugin.json must be at the archive root"
assert all(not n.startswith("/") and ".." not in n.split("/") and "\\" not in n for n in names)
assert len(names) <= 5000 and sum(i.file_size for i in z.infolist()) < 512 << 20
print(f"{sys.argv[1]}: {len(names)} files, {sum(i.compress_size for i in z.infolist())} bytes compressed")
PY
done
ls -l "$OUT"/*.zip
