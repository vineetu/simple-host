#!/usr/bin/env python3
"""End-to-end check of what an OpenAI plugin reviewer will do, over real HTTP.

Usage (local or production, after REVIEW_ACCOUNT_* and OPENAI_APPS_CHALLENGE are set):
  BASE=https://simple-host.app REVIEW_EMAIL=... REVIEW_PASSWORD=... python3 scripts/e2e-reviewer.py

Steps: domain-verification token -> discovery (RFC 9728 / 8414) -> dynamic
registration -> consent page offers "Reviewer sign-in" -> reviewer sign-in with
email + password only -> Allow -> code -> token (PKCE, resource=/mcp) ->
tools/list (every tool carries all three hints) -> who_am_i -> create_site ->
the page is served at the returned URL -> list_sites carries no
account ids -> delete_site cleans up. Also: a wrong password is refused.
Set EXPECT_CHALLENGE to the token to compare it exactly.

It registers as "ChatGPT" (CLIENT_NAME overrides) so it sees what the
reviewer's ChatGPT sees: every tool listed individually, no find_tools or
run_tool. Then it walks the submission's test cases tool by tool against the
seeded demo sites and checks that no default result carries the account
email, a display name, or an internal account ID (2026-10-28 rejection).
"""
import base64, hashlib, json, os, re, secrets, sys, urllib.error, urllib.parse, urllib.request

BASE = os.environ.get("BASE", "http://localhost:18080").rstrip("/")
EMAIL, PASSWORD = os.environ["REVIEW_EMAIL"], os.environ["REVIEW_PASSWORD"]
REDIRECT = "https://chatgpt.com/connector_platform_oauth_redirect"
ok_count = 0


def req(method, url, body=None, headers=None, form=None):
    h = dict(headers or {})
    data = None
    if form is not None:
        data = urllib.parse.urlencode(form).encode()
        h.setdefault("Content-Type", "application/x-www-form-urlencoded")
    elif body is not None:
        data = json.dumps(body).encode()
        h.setdefault("Content-Type", "application/json")
    r = urllib.request.Request(url if url.startswith("http") else BASE + url, data=data, method=method, headers=h)

    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *a, **k):
            return None

    try:
        resp = urllib.request.build_opener(NoRedirect).open(r)
        return resp.status, dict(resp.headers), resp.read()
    except urllib.error.HTTPError as e:
        return e.code, dict(e.headers), e.read()


def check(cond, label, extra=""):
    global ok_count
    if not cond:
        print("FAIL", label, extra)
        sys.exit(1)
    ok_count += 1
    print("ok  ", label)


st, h, body = req("GET", "/.well-known/openai-apps-challenge")
ctype = h.get("Content-Type", "")
check(st == 200 and ctype.startswith("text/plain") and body.strip() and b"{" not in body, "challenge token served as plain text", f"{st} {ctype}")
if os.environ.get("EXPECT_CHALLENGE"):
    check(body.decode() == os.environ["EXPECT_CHALLENGE"], "challenge token matches exactly")

st, h, _ = req("POST", "/mcp", {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}})
wa = h.get("WWW-Authenticate") or h.get("Www-Authenticate") or ""
check(st == 401 and "resource_metadata=" in wa, "unauthenticated /mcp -> 401 with resource metadata")
prm = json.loads(req("GET", re.search(r'resource_metadata="([^"]+)"', wa).group(1))[2])
resource = prm["resource"]
asm = json.loads(req("GET", prm["authorization_servers"][0] + "/.well-known/oauth-authorization-server")[2])
check("S256" in asm["code_challenge_methods_supported"] and asm.get("authorization_response_iss_parameter_supported") is True, "authorization server metadata: S256 + iss")

st, _, b = req("POST", asm["registration_endpoint"], {"client_name": os.environ.get("CLIENT_NAME", "ChatGPT"), "redirect_uris": [REDIRECT], "token_endpoint_auth_method": "none"})
check(st == 201, "dynamic client registration")
client_id = json.loads(b)["client_id"]

verifier = secrets.token_urlsafe(48)
challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b"=").decode()
state = secrets.token_urlsafe(12)
q = urllib.parse.urlencode({"response_type": "code", "client_id": client_id, "redirect_uri": REDIRECT, "code_challenge": challenge,
                            "code_challenge_method": "S256", "state": state, "resource": resource, "scope": "sites"})
st, _, page = req("GET", "/oauth/authorize?" + q)
data = json.loads(re.search(rb'<script type="application/json" id="connect-data">(.*?)</script>', page).group(1))
check(st == 200 and data.get("reviewer_signin") is True, "consent page offers Reviewer sign-in")
check(b'id="reviewer-form"' in page, "consent page carries the reviewer form")

same_origin = {"Origin": BASE}
st, _, b = req("POST", "/oauth/reviewer-signin", {"email": EMAIL, "password": PASSWORD + "x"}, same_origin)
check(st == 401 and b"api_key" not in b, "wrong password refused")
st, _, b = req("POST", "/oauth/reviewer-signin", {"email": EMAIL, "password": PASSWORD}, same_origin)
check(st == 200, "reviewer signs in with email + password only", b[:200])
key = json.loads(b)["api_key"]

st, _, b = req("POST", "/oauth/authorize/decision", {"query": q, "csrf": data["csrf"], "decision": "allow"}, {**same_origin, "X-API-Key": key})
check(st == 200, "Allow")
back = urllib.parse.urlparse(json.loads(b)["redirect_to"])
params = urllib.parse.parse_qs(back.query)
check(params["state"][0] == state and params["iss"][0] == asm["issuer"], "redirect carries state and iss")
st, _, b = req("POST", asm["token_endpoint"], form={"grant_type": "authorization_code", "code": params["code"][0], "redirect_uri": REDIRECT,
                                                     "client_id": client_id, "code_verifier": verifier, "resource": resource})
check(st == 200, "code -> tokens")
access = json.loads(b)["access_token"]


def rpc(method, p=None):
    st, _, b = req("POST", "/mcp", {"jsonrpc": "2.0", "id": 1, "method": method, "params": p or {}},
                   {"Authorization": "Bearer " + access, "MCP-Protocol-Version": "2025-06-18"})
    return st, json.loads(b)


def call(name, args):
    _, r = rpc("tools/call", {"name": name, "arguments": args})
    return r["result"]


st, r = rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "reviewer-e2e", "version": "1"}})
check(st == 200 and r["result"]["instructions"], "initialize")
st, r = rpc("tools/list")
tools = r["result"]["tools"]
missing = [t["name"] for t in tools if not all(isinstance(t.get("annotations", {}).get(k), bool) for k in ("readOnlyHint", "destructiveHint", "openWorldHint"))]
check(not missing, f"all {len(tools)} tools carry readOnlyHint/destructiveHint/openWorldHint", missing)
for t in tools:
    a = t["annotations"]
    print(f"      {t['name']:<18} readOnly={a['readOnlyHint']!s:<5} destructive={a['destructiveHint']!s:<5} openWorld={a['openWorldHint']}")

names = {t["name"] for t in tools}
check(not names & {"find_tools", "run_tool"} and len(tools) >= 56, f"ChatGPT sees {len(tools)} tools individually, no generic executor")

UUID = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}", re.I)


def private(label, result):
    """A default result carries no account email, display name or account UUID."""
    body = json.dumps(result)
    check(EMAIL.lower() not in body.lower() and "display_name" not in body and not UUID.search(body), f"{label}: no personal identifiers", body[:300])


me = call("who_am_i", {})
check(not me["isError"] and me["structuredContent"]["email"] == "", "who_am_i leaves the email out by default")
private("who_am_i", me)
me = call("who_am_i", {"include_email": True})
check(not me["isError"] and me["structuredContent"]["email"] == EMAIL.lower(), "who_am_i gives the email when asked")

# A run that stopped half way leaves its throwaway site behind; remove it first.
for s in call("list_sites", {})["structuredContent"].get("sites", []):
    if s.get("name", "").startswith("reviewer-e2e-"):
        call("delete_site", {"site": s["name"], "confirm_name": s["name"]})

site = "reviewer-e2e-" + secrets.token_hex(3)
html = "<!DOCTYPE html><html><head><meta charset=utf-8><title>Reviewer e2e</title></head><body><h1>Published through the connector</h1></body></html>"
res = call("create_site", {"site": site, "files": {"index.html": html}})
check(not res["isError"], "create_site", res["content"][0]["text"])
url = res["structuredContent"]["url"]
if os.environ.get("CONTENT"):  # local runs: the content host is a separate static server
    url = os.environ["CONTENT"].rstrip("/") + urllib.parse.urlparse(url).path + ("current/" if os.environ.get("CONTENT_CURRENT") else "")
st, _, b = req("GET", url)
check(st == 200 and b"Published through the connector" in b, "the published page is served at the returned URL", url)

res = call("list_sites", {})
body = json.dumps(res)
check(not any(k in body for k in ('"id"', "user_id", "owner_username")), "list_sites carries no internal ids")
private("list_sites", res)
res = call("create_site", {"site": site, "files": {"index.html": html}})
check(res["isError"] and "update_site" in res["content"][0]["text"], "create_site never overwrites")
res = call("delete_site", {"site": site, "confirm_name": site})
check(not res["isError"], "delete_site cleans up")

# The submission's positive cases, as the tool calls ChatGPT makes for them.
listed = {x["name"] for x in call("list_sites", {})["structuredContent"].get("sites", [])}
check({"garden-party-rsvp", "feedback-survey", "pickle-shop"} <= listed, "the seeded demo sites are there", sorted(listed))
for name in ("garden-party-rsvp", "feedback-survey", "pickle-shop"):
    private("get_site " + name, call("get_site", {"site": name}))
    private("storage_list_resources " + name, call("storage_list_resources", {"site": name}))

res = call("storage_sql_query", {"site": "pickle-shop", "name": "orders", "sql": "SELECT items, total_cents, status FROM orders"})
check(not res["isError"] and res["structuredContent"]["response"].get("rows"), "orders: items, totals and status", res["content"][0]["text"][:300])
private("orders query", res)
res = call("storage_sql_query", {"site": "garden-party-rsvp", "name": "rsvps", "sql": "SELECT COUNT(*) AS n FROM rsvps"})
check(not res["isError"], "RSVP count", res["content"][0]["text"][:300])
private("RSVP count", res)
res = call("storage_sql_query", {"site": "garden-party-rsvp", "name": "rsvps", "sql": "SELECT * FROM rsvps"})
check(not res["isError"], "all RSVP columns", res["content"][0]["text"][:300])
private("RSVP rows (SELECT *)", res)
res = call("site_analytics", {"site": "feedback-survey"})
check(not res["isError"], "visitor numbers")
private("site_analytics", res)
private("list_site_viewers", call("list_site_viewers", {"site": "feedback-survey"}))

# Change a heading on feedback-survey, check it is live, then roll back.
page = call("read_site_file", {"site": "feedback-survey", "path": "index.html"})
check(not page["isError"], "read feedback-survey index.html")
html_now = page["structuredContent"]["content"]
h1 = re.search(r"<h1[^>]*>(.*?)</h1>", html_now, re.S).group(1)
before = call("list_versions", {"site": "feedback-survey"})["structuredContent"]["versions"]
live = next(v["version"] for v in before if v["live"])
res = call("update_site", {"site": "feedback-survey", "keep_other_files": True, "edits": [{"path": "index.html", "find": h1, "replace": "Tell us how we did"}]})
check(not res["isError"], "update_site changes the heading", res["content"][0]["text"][:300])
private("update_site", res)
survey_url = res["structuredContent"]["url"]
st, _, b = req("GET", survey_url)
check(st == 200 and b"Tell us how we did" in b, "the new heading is live")
res = call("rollback_site", {"site": "feedback-survey", "version": live})
check(not res["isError"], "rollback_site puts the old heading back", res["content"][0]["text"][:300])
st, _, b = req("GET", survey_url)
check(st == 200 and b"Tell us how we did" not in b, "the old heading is live again")
print(f"\nall {ok_count} checks passed")
