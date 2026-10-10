#!/usr/bin/env python3
"""Fill the plugin reviewer's demo account with sample sites and data.

The OpenAI directory asks for "a fully featured demo account that includes
sample data". This signs in as the reviewer account (the same email+password
the reviewer will use), publishes every site under openai-plugin/demo-sites/,
and saves the sample RSVPs, survey responses and orders from seed.json into
them, so list_sites, site_analytics and storage_sql_query have something
real to show.

All three demo sites now use KV/SQLite/files storage resources instead of
the deprecated state/collections APIs ("storage" in seed.json, one entry per
resource): the seeder creates each resource (owner-authenticated; X-API-Key
alone authenticates as the owner for the storage routes, no Origin needed),
its schema, and seeds rows with owner storage_sql_execute, since a visitor
identity is needed to seed per-person "own"/"owner" rows and the seeder has
none. The rows are owner-attributed (no visitor_id), which the owner can
still see in full with storage_sql_query regardless of the resource's read
policy. Each resource's row shape is different (pickle-shop/orders,
garden-party-rsvp/rsvps, feedback-survey/responses), so ROW_BUILDERS below
has one small function per (site, resource) turning a seed.json row into the
INSERT statement and params for that resource's schema.

The older "collections"/"state" seeding path is kept for any future
existing-site demo that still depends on those deprecated APIs, but nothing
in seed.json uses it right now.

Usage (after REVIEW_ACCOUNT_EMAIL / REVIEW_ACCOUNT_PASSWORD_HASH are set and
the server restarted):
  BASE=https://simple-host.app REVIEW_EMAIL=... REVIEW_PASSWORD=... python3 scripts/seed-reviewer-demo.py

Re-running publishes a new version of each site and inserts the sample rows
again (storage writes are add-only here), so run it once.
"""
import json, os, pathlib, sys, urllib.error, urllib.parse, urllib.request

BASE = os.environ.get("BASE", "https://simple-host.app").rstrip("/")
EMAIL, PASSWORD = os.environ["REVIEW_EMAIL"], os.environ["REVIEW_PASSWORD"]
ROOT = pathlib.Path(__file__).resolve().parent.parent / "openai-plugin" / "demo-sites"


def _pickle_shop_orders(row):
    sql = ("INSERT INTO orders (name, contact, items, total_cents, status, created_at) "
           "VALUES (?, ?, ?, ?, ?, datetime('now'))")
    return sql, [row["name"], row["contact"], json.dumps(row["lines"]), round(row["total"] * 100), row["status"]]


def _garden_party_rsvps(row):
    sql = "INSERT INTO rsvps (name, attending, guests, dietary, created_at) VALUES (?, ?, ?, ?, datetime('now'))"
    return sql, [row["name"], row["attending"], row["guests"], row["dietary"]]


def _feedback_survey_responses(row):
    sql = ("INSERT INTO responses (visit, ordered, taste, service, wait, again, improve, created_at) "
           "VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'))")
    return sql, [row["visit"], json.dumps(row["ordered"]), row["taste"], row["service"], row["wait"], row["again"], row["improve"]]


ROW_BUILDERS = {
    ("pickle-shop", "orders"): _pickle_shop_orders,
    ("garden-party-rsvp", "rsvps"): _garden_party_rsvps,
    ("feedback-survey", "responses"): _feedback_survey_responses,
}


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
    # New-style KV/SQLite/files storage resources (owner-authenticated, no
    # Origin needed: X-API-Key alone authenticates as the owner for the
    # storage routes). Rows are seeded with raw owner SQL, so they carry no
    # visitor_id; the owner still sees them all with storage_sql_query.
    for name, res in parts.get("storage", {}).items():
        st, out = req("PUT", f"/v1/sites/{site}/storage/resources/{name}", res["resource"], auth)
        if st not in (200, 201):
            sys.exit(f"{site}/{name}: resource failed {st} {out}")
        st, out = req("POST", f"/v1/sites/{site}/storage/sqlite/{name}/schema", {"sql": res["schema"]}, auth)
        if st != 200:
            sys.exit(f"{site}/{name}: schema failed {st} {out}")
        build_row = ROW_BUILDERS[(site, name)]
        for row in res.get("rows", []):
            sql, params = build_row(row)
            st, out = req("POST", f"/v1/sites/{site}/storage/sqlite/{name}/execute", {"sql": sql, "params": params}, auth)
            if st != 200:
                sys.exit(f"{site}/{name}: row insert failed {st} {out}")
        print(f"{site}: {len(res.get('rows', []))} rows in {name} (resource + schema set)")
print("done")
