#!/usr/bin/env python3
"""Render internal/webassets/static/features.html from features.json.

features.json is the one source for the public Features page (areas, features, editions,
status, links). This script fills scripts/features-page.tmpl.html and writes the page.
scripts/check-features-page.py fails when the committed page is stale or the data misses a
FEATURES.md section or bold-lead feature.

Run:  python3 scripts/build-features-page.py
"""
import html
import json
import os
import re
import sys

ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
DATA = os.path.join(ROOT, "internal/webassets/static/features.json")
TMPL = os.path.join(ROOT, "scripts/features-page.tmpl.html")
OUT = os.path.join(ROOT, "internal/webassets/static/features.html")

EDITIONS = [("host", "Simple Host"), ("hack", "Simple Hack"), ("enterprise", "Enterprise"), ("smallbox", "Small box")]
ED_WORD = {"yes": "yes", "no": "no", "partial": "partly", "planned": "planned"}
STATUS_WORD = {"live": "live", "flag": "off by default", "planned": "planned", "deprecated": "existing sites only"}
PILL = {"flag": "Off by default", "planned": "Planned", "deprecated": "Existing sites only"}


def rich(s):
    """Escape text; `code` spans become <code>, **bold** spans become <b>.

    The <b> form matters: limitstext.go rewrites sentences such as
    "after <b>15 minutes</b>" to the limits an install runs with, so a limit
    stated on this page must be written with the exact phrase listed there."""
    parts = re.split(r"(`[^`]+`|\*\*[^*]+\*\*)", s)
    out = []
    for p in parts:
        if len(p) > 1 and p.startswith("`") and p.endswith("`"):
            out.append("<code>" + html.escape(p[1:-1]) + "</code>")
        elif len(p) > 3 and p.startswith("**") and p.endswith("**"):
            out.append("<b>" + html.escape(p[2:-2]) + "</b>")
        else:
            out.append(html.escape(p))
    return "".join(out)


def render_feature(f):
    pill = ""
    if f["status"] in PILL:
        pill = ' <span class="st %s">%s</span>' % (f["status"], PILL[f["status"]])
    lines = []
    lines.append('      <details class="feat" id="%s">' % html.escape(f["id"]))
    lines.append('        <summary><span class="fname">%s</span>%s<span class="fline">%s</span></summary>' % (rich(f["name"]), pill, rich(f["line"])))
    lines.append('        <div class="fbody">')
    lines.append('          <p class="how">%s</p>' % rich(f["how"]))
    if f.get("example"):
        lines.append('          <p class="ex"><b>Example.</b> %s</p>' % rich(f["example"]))
    dl = []
    for key, label in (("who", "Who"), ("where", "Where"), ("limits", "Limits")):
        if f.get(key):
            cls = ' class="lim"' if key == "limits" else ""
            dl.append('            <dt>%s</dt><dd%s>%s</dd>' % (label, cls, rich(f[key])))
    if dl:
        lines.append('          <dl class="fm">')
        lines.extend(dl)
        lines.append('          </dl>')
    eds = []
    for key, label in EDITIONS:
        v = f["editions"].get(key, "no")
        eds.append('<li class="%s"><b>%s</b>%s</li>' % (v, label, ED_WORD[v]))
    lines.append('          <ul class="eds" aria-label="Editions">%s</ul>' % "".join(eds))
    if f.get("editions_note"):
        lines.append('          <p class="ednote">%s</p>' % rich(f["editions_note"]))
    status = STATUS_WORD[f["status"]]
    if f.get("status_note"):
        status += ". " + rich(f["status_note"]).rstrip(".")
    meta = ['<span class="status"><b>Status:</b> %s.</span>' % status]
    if f.get("docs"):
        links = " ".join('<a href="%s">%s</a>' % (html.escape(d["href"], quote=True), html.escape(d["label"])) for d in f["docs"])
        meta.append('<span class="docs"><b>Read more:</b> %s</span>' % links)
    lines.append('          <p class="meta">%s</p>' % " ".join(meta))
    lines.append('        </div>')
    lines.append('      </details>')
    return "\n".join(lines)


def render(data):
    intro = "\n".join('      <p class="lede">%s</p>' % rich(s) for s in data["intro"])
    tree = ['        <div class="root"><b>%s</b><span>%s</span></div>' % (rich(data["root"]["name"]), rich(data["root"]["line"])),
            '        <ol class="branches">']
    for a in data["areas"]:
        cls = ' class="hackb"' if a["id"] == "hack" else ""
        tag = ('<span class="tag">%s</span>' % rich(a["tag"])) if a.get("tag") else ""
        tree.append('          <li%s><a class="node" href="#%s"><b>%s</b><span class="n">%d</span><span class="l">%s</span>%s</a></li>'
                    % (cls, a["id"], rich(a["title"]), len(a["features"]), rich(a["line"]), tag))
    tree.append('        </ol>')
    areas = []
    for a in data["areas"]:
        for al in a.get("aliases", []):
            if al != a["id"]:
                areas.append('    <span class="alias" id="%s"></span>' % html.escape(al))
        areas.append('    <section class="area" id="%s">' % html.escape(a["id"]))
        areas.append('      <div class="areahead"><h2>%s</h2><a class="up" href="#map-title">Back to the map</a></div>' % rich(a["title"]))
        areas.append('      <p class="lede">%s</p>' % rich(a["line"]))
        if a.get("more"):
            areas.append('      <p>%s</p>' % rich(a["more"]))
        areas.append('      <div class="feats">')
        for f in a["features"]:
            areas.append(render_feature(f))
        areas.append('      </div>')
        areas.append('    </section>')
    tmpl = open(TMPL, encoding="utf-8").read()
    page = tmpl.replace("<!--features:intro-->", intro).replace("<!--features:tree-->", "\n".join(tree)).replace("<!--features:areas-->", "\n".join(areas))
    return page


def main():
    data = json.load(open(DATA, encoding="utf-8"))
    page = render(data)
    if "--check" in sys.argv:
        cur = open(OUT, encoding="utf-8").read() if os.path.exists(OUT) else ""
        if cur != page:
            print("FAIL: internal/webassets/static/features.html is stale; run python3 scripts/build-features-page.py")
            sys.exit(1)
        print("  ok features.html matches features.json")
        return
    with open(OUT, "w", encoding="utf-8") as fh:
        fh.write(page)
    print("wrote %s (%d areas, %d features)" % (os.path.relpath(OUT, ROOT), len(data["areas"]), sum(len(a["features"]) for a in data["areas"])))


if __name__ == "__main__":
    main()
