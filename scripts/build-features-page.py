#!/usr/bin/env python3
"""Render host/webassets/static/features.html from features.json.

features.json is the one source for the public Features page (areas, features, editions,
status, links). This script fills scripts/features-page.tmpl.html and writes the page.
scripts/check-features-page.py fails when the committed page is stale or the data misses a
FEATURES.md section or bold-lead feature.

Run:  python3 scripts/build-features-page.py
"""
import copy
from pathlib import Path
import html
import json
import os
import re
import sys

ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
DATA = os.path.join(ROOT, "host/webassets/static/features.json")
TMPL = os.path.join(ROOT, "scripts/features-page.tmpl.html")
OUT = os.path.join(ROOT, "host/webassets/static/features.html")

EDITIONS = [("host", "Simple Host"), ("enterprise", "Enterprise"), ("smallbox", "Small box")]
ED_WORD = {"yes": "yes", "no": "no", "partial": "partly", "planned": "planned"}
STATUS_WORD = {"live": "live", "flag": "off by default", "planned": "planned", "deprecated": "existing sites only"}
PILL = {"flag": "Off by default", "planned": "Planned", "deprecated": "Existing sites only"}


MARKERS = {"[[hosted]]": "<!--sh:hosted-->", "[[/hosted]]": "<!--/sh:hosted-->",
           "[[self]]": "<!--sh:self-->", "[[/self]]": "<!--/sh:self-->"}


def rich(s, markers=None):
    """Escape prose, retaining code, bold text and configured block markers."""
    parts = re.split(r"(`[^`]+`|\*\*[^*]+\*\*)", s)
    out = []
    for p in parts:
        if len(p) > 1 and p.startswith("`") and p.endswith("`"):
            out.append("<code>" + html.escape(p[1:-1]) + "</code>")
        elif len(p) > 3 and p.startswith("**") and p.endswith("**"):
            out.append("<b>" + html.escape(p[2:-2]) + "</b>")
        else:
            out.append(html.escape(p))
    text = "".join(out)
    for token, marker in (markers or MARKERS).items():
        text = text.replace(token, marker)
    return text


def render_feature(f, options=None):
    options = options or {}
    format_text = rich
    rich_text = lambda text: format_text(text, options.get("markers"))
    pill = ""
    if f["status"] in PILL:
        pill = ' <span class="st %s">%s</span>' % (f["status"], PILL[f["status"]])
    lines = []
    prefix_lines, suffix_lines = [], []
    open_markers, close_markers = "", ""
    if f["id"] == "self-host":
        prefix_lines.append("<!--sh:hosted-->")
        suffix_lines.append("<!--/sh:hosted-->")
    product_only = f["id"] != "self-host" and f["id"] in options.get("product_features", [])
    if product_only:
        open_markers += options["open_marker"]
        close_markers = options["close_marker"] + close_markers
    lines.extend(prefix_lines)
    lines.append(open_markers + '      <details class="feat" id="%s">' % html.escape(f["id"]))
    lines.append('        <summary><span class="fname">%s</span>%s<span class="fline">%s</span></summary>' % (rich_text(f["name"]), pill, rich_text(f["line"])))
    lines.append('        <div class="fbody">')
    lines.append('          <p class="how">%s</p>' % rich_text(f["how"]))
    if f.get("example"):
        lines.append('          <p class="ex"><b>Example.</b> %s</p>' % rich_text(f["example"]))
    dl = []
    for key, label in (("who", "Who"), ("where", "Where"), ("limits", "Limits")):
        if f.get(key):
            cls = ' class="lim"' if key == "limits" else ""
            dl.append('            <dt>%s</dt><dd%s>%s</dd>' % (label, cls, rich_text(f[key])))
    if dl:
        lines.append('          <dl class="fm">')
        lines.extend(dl)
        lines.append('          </dl>')
    eds = []
    for key, label in options.get("editions", EDITIONS):
        v = f["editions"].get(key, "no")
        eds.append('<li class="%s"><b>%s</b>%s</li>' % (v, label, ED_WORD[v]))
    lines.append('          <ul class="eds" aria-label="Editions">%s</ul>' % "".join(eds))
    if f.get("editions_note"):
        lines.append('          <p class="ednote">%s</p>' % rich_text(f["editions_note"]))
    status = STATUS_WORD[f["status"]]
    if f.get("status_note"):
        status += ". " + rich_text(f["status_note"]).rstrip(".")
    meta = ['<span class="status"><b>Status:</b> %s.</span>' % status]
    if f.get("docs"):
        doc_links = []
        for d in f["docs"]:
            a = '<a href="%s">%s</a>' % (html.escape(d["href"], quote=True), html.escape(d["label"]))
            if d.get("host_only"):
                a = a.replace('<a ', '<a data-host-only ', 1)
            if not product_only and any(host in d["href"] for host in options.get("product_links", [])):
                a = options["open_marker"] + a + options["close_marker"]
            doc_links.append(a)
        meta.append('<span class="docs"><b>Read more:</b> %s</span>' % " ".join(doc_links))
    lines.append('          <p class="meta">%s</p>' % " ".join(meta))
    lines.append('        </div>')
    lines.append('      </details>' + close_markers)
    lines.extend(suffix_lines)
    return "\n".join(lines)


def render(data, options=None, template=TMPL):
    options = options or {}
    rich_text = lambda text: rich(text, options.get("markers"))
    def intro_p(index, text):
        p = '      <p class="lede">%s</p>' % rich_text(text)
        if index in options.get("marked_intro", []):
            return options["open_marker"] + p + options["close_marker"]
        return p
    intro = "\n".join(intro_p(i, text) for i, text in enumerate(data["intro"]))
    tree = ['        <div class="root"><b>%s</b><span>%s</span></div>' % (rich_text(data["root"]["name"]), rich_text(data["root"]["line"])),
            '        <ol class="branches">']
    for a in data["areas"]:
        cls = ' class="%s"' % options['branch_classes'][a['id']] if a["id"] in options.get("branch_classes", {}) else ""
        tag = ('<span class="tag">%s</span>' % rich_text(a["tag"])) if a.get("tag") else ""
        tree.append('          <li%s><a class="node" href="#%s"><b>%s</b><span class="n">%d</span><span class="l">%s</span>%s</a></li>'
                    % (cls, a["id"], rich_text(a["title"]), len(a["features"]), rich_text(a["line"]), tag))
    tree.append('        </ol>')
    areas = []
    for a in data["areas"]:
        for al in a.get("aliases", []):
            if al != a["id"]:
                areas.append('    <span class="alias" id="%s"></span>' % html.escape(al))
        areas.append('    <section class="area" id="%s">' % html.escape(a["id"]))
        areas.append('      <div class="areahead"><h2>%s</h2><a class="up" href="#map-title">Back to the map</a></div>' % rich_text(a["title"]))
        areas.append('      <p class="lede">%s</p>' % rich_text(a["line"]))
        if a.get("more"):
            areas.append('      <p>%s</p>' % rich_text(a["more"]))
        areas.append('      <div class="feats">')
        for f in a["features"]:
            areas.append(render_feature(f, options))
        areas.append('      </div>')
        areas.append('    </section>')
    tmpl = Path(template).read_text()
    page = tmpl.replace("<!--features:intro-->", intro).replace("<!--features:tree-->", "\n".join(tree)).replace("<!--features:areas-->", "\n".join(areas))
    return page


PRODUCT = Path(ROOT) / "products/hack/webassets"


def feature_data():
    """The product part applies explicit JSON Pointer sets/inserts/removals."""
    base = json.loads(Path(DATA).read_text())
    part = json.loads((PRODUCT / "features.part.json").read_text())
    composed = copy.deepcopy(base)
    for patch in part["overrides"]:
        tokens = [token.replace("~1", "/").replace("~0", "~") for token in patch["path"][1:].split("/")]
        node = composed
        for token in tokens[:-1]:
            node = node[int(token)] if isinstance(node, list) else node[token]
        key = int(tokens[-1]) if isinstance(node, list) else tokens[-1]
        if patch["op"] == "insert":
            node.insert(key, copy.deepcopy(patch["value"]))
        elif patch["op"] == "remove":
            del node[key]
        else:
            node[key] = copy.deepcopy(patch["value"])
    for area in composed["areas"]:
        for feature in area["features"]:
            editions = feature["editions"]
            feature["editions"] = {key: editions[key] for key, _ in part["render"]["editions"] if key in editions}
    return base, composed, part["render"]


def generated():
    base, product, options = feature_data()
    return {
        Path(OUT): render(base),
        PRODUCT / "static/features.json": json.dumps(product, indent=1, ensure_ascii=True) + "\n",
        PRODUCT / "static/features.html": render(product, options, PRODUCT / "features-page.tmpl.html"),
    }


def main():
    stale = []
    for path, body in generated().items():
        if "--check" in sys.argv:
            if not path.exists() or path.read_text() != body:
                stale.append(str(path.relative_to(Path(ROOT).resolve())))
        else:
            path.write_text(body)
    if stale:
        sys.exit("stale feature files; run python3 scripts/build-features-page.py: " + ", ".join(stale))
    print("  ok features pages match their authored sources")


if __name__ == "__main__":
    main()
