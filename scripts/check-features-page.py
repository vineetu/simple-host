#!/usr/bin/env python3
"""The public Features page cannot fall behind FEATURES.md.

Fails when:
  - a numbered FEATURES.md section ("## N. Title") is claimed by no area's `sections` and is
    not listed in `not_shown`;
  - a FEATURES.md bold-lead feature (a line inside a numbered section that opens with
    **Lead**, except "Status:")
    appears in no feature's `covers` and is not listed in `not_shown`;
  - a `covers` or `not_shown` entry names a feature ID or section FEATURES.md no longer has;
  - the data breaks the page's rules (duplicate ids, a one-liner over 140 characters, a
    missing field, an unknown edition or status value, a link outside the allowed public
    targets, a dash the voice rules ban);
  - internal/webassets/static/features.html is not what build-features-page.py renders from
    features.json.

Leads are matched by their stable FEATURES.md anchors, never by display text.

Run: python3 scripts/check-features-page.py   (also run by scripts/check-docs-sync.sh)
"""
import json
import os
import re
import subprocess
import sys

ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
os.chdir(ROOT)
DATA = "internal/webassets/static/features.json"

ALLOWED_PREFIXES = (
    "/install.html", "/docs.html", "/pricing", "/photos", "/hackathons", "/enterprise",
    "/costs", "/setup", "/terms", "/privacy.html", "/support", "/report", "/recipes/",
    "/llms.txt", "/auth.md", "/architecture.html", "/skills/",
    "https://simple-hack.app/", "https://github.com/vineetu/simple-host/blob/main/FEATURES.md",
)
EDITION_VALUES = {"yes", "no", "partial", "planned"}
STATUS_VALUES = {"live", "flag", "planned", "deprecated"}
REQUIRED = ("id", "name", "line", "how", "editions", "status", "covers")
TEXT_FIELDS = ("name", "line", "who", "how", "example", "where", "limits", "editions_note", "status_note")


def parse_features_md(path="FEATURES.md"):
    """Numbered sections and bold leads, keyed by their stable anchor IDs."""
    sections, leads = {}, {}
    cur = None
    pending = None
    for line in open(path, encoding="utf-8").read().splitlines():
        anchor = re.search(r' <a id="([a-z0-9]+(?:-[a-z0-9]+)*)"></a>$', line)
        text = line[:anchor.start()] if anchor else line
        m = re.match(r"^## (\d+)\. (.+?)\s*$", text)
        if m:
            cur = int(m.group(1))
            sections[cur] = m.group(2).strip()
            continue
        if text.startswith("## "):
            cur = None
            continue
        if cur is None:
            continue
        if pending is not None:
            fid, lead = pending
            lead += " " + text.strip()
            if "**" in text:
                title = re.match(r"^\*\*(.+?)\*\*", lead).group(1).strip()
                leads[fid] = (cur, title)
                pending = None
            else:
                pending = (fid, lead)
            continue
        if text.startswith("**") and not text.startswith("**Status:"):
            if anchor is None:
                raise ValueError("bold lead has no feature ID: " + text)
            fid = anchor.group(1)
            if "**" not in text[2:]:
                pending = (fid, text)
            else:
                title = re.match(r"^\*\*(.+?)\*\*", text).group(1).strip()
                leads[fid] = (cur, title)
    return sections, leads


def main():
    fails = []
    data = json.load(open(DATA, encoding="utf-8"))
    sections, leads = parse_features_md()
    if not sections or not leads:
        print("FAIL: FEATURES.md parse found %d sections and %d leads; the pattern needs updating" % (len(sections), len(leads)))
        sys.exit(1)

    claimed_sections, covered = {}, {}
    ids = set()
    for area in data["areas"]:
        for n in area.get("sections", []):
            claimed_sections.setdefault(n, []).append(area["id"])
        for f in area["features"]:
            fid = f.get("id", "?")
            if fid in ids:
                fails.append("duplicate feature id %s" % fid)
            ids.add(fid)
            for k in REQUIRED:
                if k not in f:
                    fails.append("%s: missing field %s" % (fid, k))
            if len(f.get("line", "")) > 140:
                fails.append("%s: line is %d characters (max 140)" % (fid, len(f["line"])))
            for k in TEXT_FIELDS:
                v = f.get(k, "")
                if "—" in v or "–" in v:
                    fails.append("%s: %s contains an em or en dash" % (fid, k))
                if re.search(r"[\U0001F300-\U0001FAFF☀-➿]", v):
                    fails.append("%s: %s contains an emoji" % (fid, k))
            for k, v in f.get("editions", {}).items():
                if k not in ("host", "hack", "enterprise", "smallbox") or v not in EDITION_VALUES:
                    fails.append("%s: bad edition %s=%s" % (fid, k, v))
            if f.get("status") not in STATUS_VALUES:
                fails.append("%s: bad status %r" % (fid, f.get("status")))
            if f.get("status") in ("flag", "deprecated") and not f.get("status_note"):
                fails.append("%s: status %s needs a status_note" % (fid, f["status"]))
            for d in f.get("docs", []):
                if not d.get("href", "").startswith(ALLOWED_PREFIXES):
                    fails.append("%s: link outside the allowed public targets: %s" % (fid, d.get("href")))
            for feature_id in f.get("covers", []):
                if feature_id not in leads:
                    fails.append("%s: covers %r, which is not a FEATURES.md bold-lead ID" % (fid, feature_id))
                covered.setdefault(feature_id, []).append(fid)

    not_shown_leads, not_shown_sections = set(), set()
    for item in data.get("not_shown", []):
        if not item.get("why"):
            fails.append("not_shown entry without a why: %r" % item)
        if "lead" in item:
            nl = item["lead"]
            if nl not in leads:
                fails.append("not_shown names a bold-lead ID FEATURES.md no longer has: %r" % item["lead"])
            if nl in covered:
                fails.append("lead both covered and not_shown: %r" % item["lead"])
            not_shown_leads.add(nl)
        if "section" in item:
            if item["section"] not in sections:
                fails.append("not_shown names a section FEATURES.md no longer has: %r" % item["section"])
            not_shown_sections.add(item["section"])

    for n, title in sorted(sections.items()):
        if n not in claimed_sections and n not in not_shown_sections:
            fails.append("FEATURES.md section %d (%s) is in no area's sections and not in not_shown" % (n, title))
    for n in claimed_sections:
        if n not in sections:
            fails.append("area claims FEATURES.md section %d, which does not exist" % n)
    for nl, (sec, text) in sorted(leads.items(), key=lambda kv: kv[1][0]):
        if nl not in covered and nl not in not_shown_leads:
            fails.append("FEATURES.md §%d lead not covered by features.json: %s" % (sec, text))
    for nl, fids in covered.items():
        if len(fids) > 1:
            fails.append("lead covered by more than one feature (%s): %s" % (", ".join(fids), leads.get(nl, (None, nl))[1]))

    r = subprocess.run([sys.executable, "scripts/build-features-page.py", "--check"], capture_output=True, text=True)
    if r.returncode != 0:
        fails.append(r.stdout.strip() or r.stderr.strip())

    print("== features page (features.json) covers FEATURES.md ==")
    if fails:
        for f in fails:
            print("  FAIL: " + f)
        print("  add the feature to internal/webassets/static/features.json (or list it under not_shown with a why), then run python3 scripts/build-features-page.py")
        sys.exit(1)
    nfeat = sum(len(a["features"]) for a in data["areas"])
    print("  ok — %d areas, %d features cover %d FEATURES.md sections and %d bold leads; features.html is current" % (len(data["areas"]), nfeat, len(sections), len(leads)))


if __name__ == "__main__":
    main()
