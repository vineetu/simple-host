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


HACK_MARKERS = {
    "[[hashack]]": "<!--sh:hashack-->",
    "[[/hashack]]": "<!--/sh:hashack-->",
    "[[nohack]]": "<!--sh:nohack-->",
    "[[/nohack]]": "<!--/sh:nohack-->",
    # The plain hosted/self pair (hostedBlocks, not hasHackBlocks): true
    # only of the two real hosted services, regardless of edition -- for a
    # clause naming simple-host.app/simple-hack.app as the two REAL hosted
    # deployments (a reserved name, a historical fact about Claude's
    # directory), which is simply untrue of any self-hosted install, Hack
    # edition included.
    "[[hosted]]": "<!--sh:hosted-->",
    "[[/hosted]]": "<!--/sh:hosted-->",
    "[[self]]": "<!--sh:self-->",
    "[[/self]]": "<!--/sh:self-->",
}


def rich(s):
    """Escape text; `code` spans become <code>, **bold** spans become <b>.

    The <b> form matters: limitstext.go rewrites sentences such as
    "after <b>15 minutes</b>" to the limits an install runs with, so a limit
    stated on this page must be written with the exact phrase listed there.

    [[hashack]]...[[/hashack]] and [[nohack]]...[[/nohack]] become the raw
    <!--sh:hashack--> markers chrome.go's hasHackBlocks filters on (see
    is_hack_only): a feature most editions share, but whose text has one
    clause true only where Simple Hack exists (a reserved name, a
    simple-hack.app link) and, where the sentence needs it, a
    self-hosted-Host-edition alternative. [ and ] are not HTML-escaped, so
    this substitution runs after escaping without touching anything else."""
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
    for token, marker in HACK_MARKERS.items():
        text = text.replace(token, marker)
    return text


def is_hack_only(f):
    """True for a feature Host explicitly does not have but Hack does (the
    "hack-*" rows): on a self-hosted Host edition (EVENTS=off, no Hack
    feature at all) this detail -- full of simple-hack.app links and a
    description of a service this install does not run -- is both
    irrelevant and a hosted-domain leak. Wrapped in <!--sh:hashack-->, which
    chrome.go's hasHackBlocks keeps on the two real hosted services and any
    Hack-edition instance (self-hosted or not), and drops only there."""
    eds = f.get("editions", {})
    return eds.get("host") == "no" and eds.get("hack") in ("yes", "partial")


def render_feature(f):
    pill = ""
    if f["status"] in PILL:
        pill = ' <span class="st %s">%s</span>' % (f["status"], PILL[f["status"]])
    lines = []
    # self-host hides itself on every self-hosted instance (sh:hosted); a
    # feature Host doesn't have but Hack does (is_hack_only) hides only on
    # a self-hosted Host edition (sh:hashack). Both are glued onto the
    # opening/closing <details> line, never a standalone marker line:
    # hasHackBlocks/hostedBlocks only remove the marker text itself, so a
    # marker alone on its own line would leave a blank line behind
    # wherever this feature is KEPT (every hosted page, verified
    # byte-identical by scripts/golden and shots/hosted-diff.mjs).
    open_markers, close_markers = "", ""
    if f["id"] == "self-host":
        open_markers += "<!--sh:hosted-->"
        close_markers = "<!--/sh:hosted-->" + close_markers
    # self-host (above) already hides itself on every self-hosted instance;
    # this is the narrower, Host-edition-only gate.
    hack_only = f["id"] != "self-host" and is_hack_only(f)
    if hack_only:
        open_markers += "<!--sh:hashack-->"
        close_markers = "<!--/sh:hashack-->" + close_markers
    lines.append(open_markers + '      <details class="feat" id="%s">' % html.escape(f["id"]))
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
        doc_links = []
        for d in f["docs"]:
            a = '<a href="%s">%s</a>' % (html.escape(d["href"], quote=True), html.escape(d["label"]))
            # A feature most editions share can still carry one Simple
            # Hack-only doc link (its own get-started or companies page);
            # drop just that link, not the whole feature, on a self-hosted
            # Host edition (is_hack_only already hides the whole block for
            # a feature that is Hack-only to begin with).
            if not hack_only and "simple-hack.app" in d["href"]:
                a = "<!--sh:hashack-->" + a + "<!--/sh:hashack-->"
            doc_links.append(a)
        meta.append('<span class="docs"><b>Read more:</b> %s</span>' % " ".join(doc_links))
    lines.append('          <p class="meta">%s</p>' % " ".join(meta))
    lines.append('        </div>')
    lines.append('      </details>' + close_markers)
    return "\n".join(lines)


def render(data):
    def intro_p(s):
        # The Simple Hack mention ("runs hackathons on simple-hack.app") is
        # cross-product content a self-hosted Host edition should not show
        # (see is_hack_only); every other intro line describes Simple Host
        # itself and stays unconditionally.
        p = '      <p class="lede">%s</p>' % rich(s)
        if "simple-hack.app" in s:
            # Glued onto the same line as the content, not a standalone
            # marker line: hasHackBlocks only removes the marker text, so a
            # marker alone on its own line would leave a blank line behind
            # wherever the content is KEPT (every hosted page, verified
            # byte-identical by scripts/golden and shots/hosted-diff.mjs).
            return "<!--sh:hashack-->" + p + "<!--/sh:hashack-->"
        return p
    intro = "\n".join(intro_p(s) for s in data["intro"])
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
