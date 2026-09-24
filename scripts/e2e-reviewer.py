#!/usr/bin/env python3
"""End-to-end check of what an OpenAI plugin reviewer will do, over real HTTP.

Usage (local or production, after REVIEW_ACCOUNT_* and OPENAI_APPS_CHALLENGE are set):
  BASE=https://simple-host.app REVIEW_EMAIL=... REVIEW_PASSWORD=... python3 scripts/e2e-reviewer.py

Steps: domain-verification token -> discovery (RFC 9728 / 8414) -> dynamic
registration -> consent page offers "Reviewer sign-in" -> reviewer sign-in with
email + password only -> Allow -> code -> token (PKCE, resource=/mcp) ->
tools/list (every tool carries all three hints) -> who_am_i -> create_site ->
the page is served at the returned URL -> read_collection/list_sites carry no
internal ids -> delete_site cleans up. Also: a wrong password is refused.
Set EXPECT_CHALLENGE to the token to compare it exactly.
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

st, _, b = req("POST", asm["registration_endpoint"], {"client_name": "Reviewer e2e", "redirect_uris": [REDIRECT], "token_endpoint_auth_method": "none"})
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

me = call("who_am_i", {})
check(not me["isError"] and me["structuredContent"]["email"] == EMAIL.lower(), "who_am_i is the reviewer")

site = "reviewer-e2e-" + secrets.token_hex(3)
html = "<!DOCTYPE html><html><head><meta charset=utf-8><title>Reviewer e2e</title></head><body><h1>Published through the connector</h1></body></html>"
res = call("create_site", {"site": site, "files": {"index.html": html}})
check(not res["isError"], "create_site", res["content"][0]["text"])
url = res["structuredContent"]["url"]
if os.environ.get("CONTENT"):  # local runs: the content host is a separate static server
    url = os.environ["CONTENT"].rstrip("/") + urllib.parse.urlparse(url).path + ("current/" if os.environ.get("CONTENT_CURRENT") else "")
st, _, b = req("GET", url)
check(st == 200 and b"Published through the connector" in b, "the published page is served at the returned URL", url)

res = call("add_to_collection", {"site": site, "collection": "rsvps", "item": {"name": "Reviewer", "attending": True}})
check(not res["isError"], "add_to_collection")
res = call("read_collection", {"site": site, "collection": "rsvps"})
body = json.dumps(res)
check(res["structuredContent"]["items"][0]["data"]["name"] == "Reviewer" and '"id"' not in body, "read_collection returns the data, no row ids")
res = call("list_sites", {})
body = json.dumps(res)
check(not any(k in body for k in ('"id"', "user_id", "updated_at", "owner_username")), "list_sites carries no internal ids or bookkeeping")
res = call("create_site", {"site": site, "files": {"index.html": html}})
check(res["isError"] and "update_site" in res["content"][0]["text"], "create_site never overwrites")
res = call("delete_site", {"site": site, "confirm_name": site})
check(not res["isError"], "delete_site cleans up")
print(f"\nall {ok_count} checks passed")
