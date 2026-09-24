#!/usr/bin/env python3
"""Fill the plugin reviewer's demo account with sample sites and data.

The OpenAI directory asks for "a fully featured demo account that includes
sample data". This signs in as the reviewer account (the same email+password
the reviewer will use), publishes every site under openai-plugin/demo-sites/,
and saves the sample RSVPs, survey responses and orders from seed.json into
them, so list_sites, read_collection, get_state and site_analytics have
something real to show.

Usage (after REVIEW_ACCOUNT_EMAIL / REVIEW_ACCOUNT_PASSWORD_HASH are set and
the server restarted):
  BASE=https://simple-host.app REVIEW_EMAIL=... REVIEW_PASSWORD=... python3 scripts/seed-reviewer-demo.py

Re-running publishes a new version of each site and appends the sample items
again (collections are append-only), so run it once.
"""
import json, os, pathlib, sys, urllib.error, urllib.parse, urllib.request

BASE = os.environ.get("BASE", "https://simple-host.app").rstrip("/")
EMAIL, PASSWORD = os.environ["REVIEW_EMAIL"], os.environ["REVIEW_PASSWORD"]
ROOT = pathlib.Path(__file__).resolve().parent.parent / "openai-plugin" / "demo-sites"


def req(method, path, body=None, headers=None):
    data = json.dumps(body).encode() if body is not None else None
    h = {"Content-Type": "application/json", **(headers or {})}
    r = urllib.request.Request(BASE + path, data=data, method=method, headers=h)
    try:
        with urllib.request.urlopen(r) as resp:
            return resp.status, json.loads(resp.read() or b"null")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="replace")


st, body = req("POST", "/oauth/reviewer-signin", {"email": EMAIL, "password": PASSWORD}, {"Origin": BASE})
if st != 200:
    sys.exit(f"reviewer sign-in failed: {st} {body}")
key = body["api_key"]
auth = {"X-API-Key": key}

seed = json.loads((ROOT / "seed.json").read_text())
for site_dir in sorted(p for p in ROOT.iterdir() if p.is_dir()):
    site = site_dir.name
    files = {str(f.relative_to(site_dir)): f.read_text(encoding="utf-8") for f in sorted(site_dir.rglob("*")) if f.is_file()}
    st, out = req("POST", f"/v1/sites/{site}/files", {"files": files}, auth)
    if st == 409:
        st, out = req("PUT", f"/v1/sites/{site}/files", {"files": files}, auth)
    if st not in (200, 201):
        sys.exit(f"{site}: publish failed {st} {out}")
    print(f"published {site} v{out['active_version']}: {out['site_url']}")

st, me = req("GET", "/v1/me", headers=auth)
handle = me["handle"]
# Site data routes are Origin-gated; the shared content host is the origin
# every site accepts (as the connector's own tools do).
content_origin = "https://sites." + urllib.parse.urlparse(BASE).hostname
if os.environ.get("CONTENT_ORIGIN"):
    content_origin = os.environ["CONTENT_ORIGIN"]
data_headers = {**auth, "Origin": content_origin}
for site, parts in seed.items():
    for coll, items in parts.get("collections", {}).items():
        for item in items:
            st, out = req("POST", f"/v1/u/{handle}/sites/{site}/collections/{coll}", item, data_headers)
            if st not in (200, 201):
                sys.exit(f"{site}/{coll}: append failed {st} {out}")
        print(f"{site}: {len(items)} items in {coll}")
    if "state" in parts:
        st, out = req("PUT", f"/v1/u/{handle}/sites/{site}/state", parts["state"], data_headers)
        if st not in (200, 201):
            sys.exit(f"{site}: state failed {st} {out}")
        print(f"{site}: state set")
print("done")
