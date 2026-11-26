#!/usr/bin/env python3
"""Build (and optionally publish) the Simple Hack UX atlas.

    python3 docs/ux-atlas/build.py --check    # read-only offline checks
    python3 docs/ux-atlas/build.py            # regenerate catalogue.json, build dist/
    python3 docs/ux-atlas/build.py --publish  # build, then publish to the atlas site

The atlas is spec.json (the design of each screen), features.json (every
capability and its screen), changelog.json, and catalogue.json: every screen
and state of the app's catalogue (web/hack), grouped by role, each with its
390 and 1280 picture from web/hack/tests/baseline. A normal build regenerates
catalogue.json from the catalogue (needs web/node_modules) and writes the
pictures into dist/pictures as WebP (needs Pillow), a third of the PNGs' size.

Checks: every feature has a screen, screens name known features and valid
FEATURES.md anchors, every spec screen has catalogue states (or is listed in
SERVER_ONLY), and catalogue.json matches the baseline pictures exactly.
"""
import argparse, io, json, os, re, shutil, subprocess, sys, tarfile, urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.normpath(os.path.join(HERE, "../.."))
BASELINE = os.path.join(REPO, "web/hack/tests/baseline")
DIST = os.path.join(HERE, "dist")
SITE = "simple-hack-ux-atlas"
WIDTHS = (390, 1280)
# Spec screens the catalogue has no picture of: the server's indexed directory page.
SERVER_ONLY = {"public-directory"}
GROUPS = [
    ("organiser", "Organiser"),
    ("participant", "Participant"),
    ("judge", "Judge"),
    ("shared", "Everyone"),
    ("visitor", "Visitors and link pages"),
    ("site", "Marketing and status pages"),
]


def load(name):
    with open(os.path.join(HERE, name)) as f:
        return json.load(f)


def picture(state_id):
    """The baseline name of a catalogue state (Playwright turns the dot into a dash)."""
    return state_id.replace(".", "-")


def catalogue_states():
    """Every catalogue state and server page, read from the app's own catalogue."""
    out = subprocess.run(["npx", "vite-node", "-c", "hack/vite.config.ts", "hack/tests/atlas-entries.ts"],
                         cwd=os.path.join(REPO, "web"), check=True, capture_output=True, text=True).stdout
    return json.loads(out.strip().splitlines()[-1])


def group(states, spec):
    """catalogue.json: states under their screen, screens under their role, in spec order."""
    known = {s["id"]: s for s in spec["screens"] + spec["public_pages"]}
    order = {sid: i for i, sid in enumerate(known)}
    screens = {}
    for st in states:
        sid = st["id"].split(".")[0]
        if st.get("page"):
            sid = "public-event-page" if sid.startswith("event-") else "public-home" if sid == "home" else "site-pages"
        screens.setdefault(sid, []).append({"id": st["id"], "title": st["title"], "pic": picture(st["id"])})
    titles = {"signin": "Sign in", "site-pages": "Marketing and status pages"}
    roles = {"signin": "shared", "site-pages": "site"}
    out = []
    for role, label in GROUPS:
        ids = sorted((sid for sid in screens if (known.get(sid, {}).get("role") or roles.get(sid)) == role),
                     key=lambda sid: (order.get(sid, len(order)), sid))
        out.append({"role": role, "label": label, "screens": [
            {"id": sid, "title": known.get(sid, {}).get("title") or titles.get(sid, sid), "states": screens[sid]} for sid in ids]})
    missing = sorted(set(screens) - {s["id"] for g in out for s in g["screens"]})
    if missing:
        raise SystemExit("catalogue screens with no role: " + ", ".join(missing))
    return {"groups": out}


def problems(spec, feats, cat):
    screens = spec.get("screens", []) + spec.get("public_pages", [])
    placed = {fid for s in screens for fid in s.get("features", [])}
    with open(os.path.join(REPO, "docs/FEATURES.md")) as f:
        anchors = set(re.findall(r'<a id="([a-z0-9]+(?:-[a-z0-9]+)*)"></a>', f.read()))
    shown = {s["id"] for g in cat["groups"] for s in g["screens"]}
    listed = {st["pic"] for g in cat["groups"] for s in g["screens"] for st in s["states"]}
    pics = {re.sub(r"-\d+\.png$", "", n) for n in os.listdir(BASELINE) if n.endswith(".png")}
    lacking = sorted(p for p in listed for w in WIDTHS if not os.path.exists(os.path.join(BASELINE, f"{p}-{w}.png")))
    return [
        ("features with no screen", [x["id"] for x in feats if x["id"] not in placed]),
        ("screens list unknown features", sorted(placed - {x["id"] for x in feats})),
        ("features missing a valid FEATURES.md feature anchor", [x["id"] for x in feats if x.get("feature") not in anchors]),
        ("spec screens with no catalogue state", [s["id"] for s in screens if s["id"] not in shown and s["id"] not in SERVER_ONLY]),
        ("states missing a 390 or 1280 picture", lacking),
        ("baseline pictures not in catalogue.json (run build.py)", sorted(pics - listed)),
    ]


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--check", action="store_true", help="check without writing or publishing")
    mode.add_argument("--publish", action="store_true", help="build and publish")
    args = parser.parse_args()
    spec, feats = load("spec.json"), load("features.json")
    if args.check:
        cat = load("catalogue.json")
    else:
        cat = group(catalogue_states(), spec)
        with open(os.path.join(HERE, "catalogue.json"), "w") as f:
            f.write(json.dumps(cat, indent=1, ensure_ascii=False) + "\n")
    n_screens = sum(len(g["screens"]) for g in cat["groups"])
    n_states = sum(len(s["states"]) for g in cat["groups"] for s in g["screens"])
    print(f"{n_screens} screens, {n_states} states, {len(feats)} features")
    failed = False
    for label, xs in problems(spec, feats, cat):
        if xs:
            failed = True
            print(f"  {label}: {len(xs)}: {', '.join(xs[:20])}{' …' if len(xs) > 20 else ''}")
    if failed or args.check:
        return 1 if failed else 0

    from PIL import Image  # only the build needs it

    shutil.rmtree(DIST, ignore_errors=True)
    os.makedirs(os.path.join(DIST, "pictures"))
    for n in ("index.html", "spec.json", "features.json", "changelog.json", "catalogue.json"):
        shutil.copy(os.path.join(HERE, n), DIST)
    for st in (st for g in cat["groups"] for s in g["screens"] for st in s["states"]):
        for w in WIDTHS:
            im = Image.open(os.path.join(BASELINE, f"{st['pic']}-{w}.png")).convert("RGB")
            if im.height > 16383:  # WebP's limit: a very long page is scaled down to fit
                im = im.resize((im.width * 16383 // im.height, 16383))
            im.save(os.path.join(DIST, "pictures", f"{st['pic']}-{w}.webp"), "WEBP", quality=75)
    if args.publish:
        publish()


def publish():
    cfg = json.load(open(os.path.expanduser("~/.website-deploy/config.json")))
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as t:
        for root, _, names in os.walk(DIST):
            for n in names:
                p = os.path.join(root, n)
                t.add(p, arcname=os.path.relpath(p, DIST))
    req = urllib.request.Request(
        cfg["base_url"].rstrip("/") + f"/v1/sites/{SITE}?create=1", data=buf.getvalue(), method="PUT",
        headers={"X-API-Key": cfg["api_key"], "X-Skill-Version": "0.27.18", "Content-Type": "application/gzip"})
    d = json.load(urllib.request.urlopen(req, timeout=300))
    print("published version", d.get("active_version"), d.get("site_url"))


if __name__ == "__main__":
    sys.exit(main())
