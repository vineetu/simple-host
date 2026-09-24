#!/usr/bin/env python3
"""End-to-end drive of the Simple Host connector over real HTTP (no browser).

Usage: BASE=http://localhost:18080 CONTENT=<static server over DATA_DIR/handles> ADMIN_API_KEY=... BIN=<server binary> python3 scripts/e2e-connector.py

Discovery -> DCR -> authorize page -> consent decision (as the page's JS does)
-> code -> token (PKCE) -> /mcp initialize, tools/list, deploy_site -> fetch the
published page from disk-served content -> refresh rotation + reuse detection
-> revocation -> REST /v1 with the bearer token.
"""
import base64, hashlib, json, os, re, secrets, sys, urllib.parse, urllib.request, urllib.error

BASE = os.environ.get("BASE", "http://localhost:18080")
ADMIN = os.environ["ADMIN_API_KEY"]
CONTENT = os.environ.get("CONTENT", "http://localhost:18081")
ok_count = 0

def req(method, url, body=None, headers=None, form=None, follow=False):
    h = dict(headers or {})
    data = None
    if form is not None:
        data = urllib.parse.urlencode(form).encode(); h.setdefault("Content-Type", "application/x-www-form-urlencoded")
    elif body is not None:
        data = json.dumps(body).encode(); h.setdefault("Content-Type", "application/json")
    r = urllib.request.Request(url if url.startswith("http") else BASE + url, data=data, method=method, headers=h)
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *a, **k): return None
    opener = urllib.request.build_opener() if follow else urllib.request.build_opener(NoRedirect)
    try:
        resp = opener.open(r)
        return resp.status, dict(resp.headers), resp.read()
    except urllib.error.HTTPError as e:
        return e.code, dict(e.headers), e.read()

def check(cond, label, extra=""):
    global ok_count
    if not cond:
        print("FAIL", label, extra); sys.exit(1)
    ok_count += 1
    print("ok  ", label)

def pkce():
    v = secrets.token_urlsafe(48)
    c = base64.urlsafe_b64encode(hashlib.sha256(v.encode()).digest()).rstrip(b"=").decode()
    return v, c

# accounts
RUN = secrets.token_hex(3)
st, _, b = req("POST", "/v1/admin/users", {"emails": [f"carol-{RUN}@example.com", f"dave-{RUN}@example.com"]}, {"X-API-Key": ADMIN})
created = {u["username"].split("-")[0]: u for u in json.loads(b)["created"]}
if not created:  # rerun: rotate to get keys is not possible; use /v1/auth? just fail loudly
    print("accounts already exist; reset the database"); sys.exit(1)
carol, dave = created["carol"], created["dave"]
DSITE, ESITE, GSITE = "daves-site-"+RUN, "connector-e2e-"+RUN, "gpt-site-"+RUN

# 1. unauthenticated /mcp -> 401 + WWW-Authenticate with resource_metadata
st, h, _ = req("POST", "/mcp", {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}})
wa = h.get("WWW-Authenticate") or h.get("Www-Authenticate")
check(st == 401 and 'resource_metadata="' + BASE + '/.well-known/oauth-protected-resource/mcp"' in wa, "401 + WWW-Authenticate resource_metadata", wa)
prm_url = re.search(r'resource_metadata="([^"]+)"', wa).group(1)
st, _, b = req("GET", prm_url); prm = json.loads(b)
check(prm["resource"] == BASE + "/mcp", "protected resource metadata")
st, _, b = req("GET", prm["authorization_servers"][0] + "/.well-known/oauth-authorization-server"); asm = json.loads(b)
check(asm["code_challenge_methods_supported"] == ["S256"] and asm["registration_endpoint"], "authorization server metadata")

# 2. DCR as a public client (Claude/ChatGPT style)
redirect = "https://chat.example.com/oauth/callback"
st, _, b = req("POST", asm["registration_endpoint"], {"client_name": "E2E Chat", "redirect_uris": [redirect], "token_endpoint_auth_method": "none", "grant_types": ["authorization_code", "refresh_token"], "response_types": ["code"]})
client = json.loads(b)
check(st == 201 and client["client_id"].startswith("shc_") and "client_secret" not in client, "dynamic client registration (public)")
cid = client["client_id"]
st, _, b = req("POST", asm["registration_endpoint"], {"redirect_uris": ["http://evil.example.com/cb"]})
check(st == 400, "DCR refuses non-loopback http redirect")

# 3. authorize page
verifier, challenge = pkce()
state = secrets.token_urlsafe(16)
q = {"response_type": "code", "client_id": cid, "redirect_uri": redirect, "code_challenge": challenge,
     "code_challenge_method": "S256", "state": state, "scope": "sites", "resource": BASE + "/mcp"}
qs = urllib.parse.urlencode(q)
st, h, page = req("GET", asm["authorization_endpoint"] + "?" + qs)
page = page.decode()
check(st == 200 and "frame-ancestors 'none'" in h.get("Content-Security-Policy", ""), "consent page served with frame-ancestors none")
data = json.loads(re.search(r'<script type="application/json" id="connect-data">(.*?)</script>', page).group(1))
check(data["ok"] and data["client_name"] == "E2E Chat" and data["redirect_host"] == "chat.example.com", "consent data: app name + return host")
check('class="sh-header"' in page and "site.css" in page, "consent page carries the shared chrome")

# wrong redirect_uri -> shown to the person, NOT redirected
st, h, _ = req("GET", asm["authorization_endpoint"] + "?" + urllib.parse.urlencode({**q, "redirect_uri": "https://attacker.example/cb"}))
check(st == 400 and "Location" not in h, "unregistered redirect_uri: error page, no redirect")
# plain PKCE -> redirected with error
st, h, _ = req("GET", asm["authorization_endpoint"] + "?" + urllib.parse.urlencode({**q, "code_challenge_method": "plain"}))
check(st == 302 and "error=invalid_request" in h["Location"] and "state=" + state in h["Location"], "plain PKCE refused via redirect with state")
st, h, _ = req("GET", asm["authorization_endpoint"] + "?" + urllib.parse.urlencode({k: v for k, v in q.items() if not k.startswith("code_challenge")}))
check(st == 302 and "error=invalid_request" in h["Location"], "missing PKCE refused")

def consent(query, csrf, key, decision="allow", origin=BASE):
    return req("POST", "/oauth/authorize/decision", {"query": query, "csrf": csrf, "decision": decision},
               {"X-API-Key": key, "Origin": origin, "Sec-Fetch-Site": "same-origin"})

st, _, b = consent(qs, "bogus.token", carol["api_key"])
check(st == 403, "decision without a valid CSRF token refused")
st, _, b = consent(qs, data["csrf"], carol["api_key"], origin="https://evil.example")
check(st == 403, "cross-origin decision refused")
st, _, b = consent(qs, data["csrf"], ADMIN)
check(st == 403, "admin key cannot connect apps")
st, _, b = consent(qs, data["csrf"], carol["api_key"], decision="deny")
loc = json.loads(b)["redirect_to"]
check("error=access_denied" in loc and "state=" + state in loc, "Cancel -> access_denied with state")
st, _, b = consent(qs, data["csrf"], carol["api_key"])
loc = json.loads(b)["redirect_to"]
lq = urllib.parse.parse_qs(urllib.parse.urlparse(loc).query)
check(loc.startswith(redirect + "?") and lq["state"] == [state] and lq["iss"] == [BASE], "Allow -> code + state + iss at the registered redirect")
code = lq["code"][0]

tok = asm["token_endpoint"]
# wrong verifier -> fails AND burns the code
st, _, b = req("POST", tok, form={"grant_type": "authorization_code", "code": code, "redirect_uri": redirect, "client_id": cid, "code_verifier": pkce()[0]})
check(st == 400 and json.loads(b)["error"] == "invalid_grant", "PKCE mismatch refused")
st, _, b = req("POST", tok, form={"grant_type": "authorization_code", "code": code, "redirect_uri": redirect, "client_id": cid, "code_verifier": verifier})
check(st == 400, "a code is spent even by a failed redemption")

# fresh code, wrong redirect_uri
def fresh_code():
    v, c = pkce()
    qq = {**q, "code_challenge": c}
    qqs = urllib.parse.urlencode(qq)
    _, _, pg = req("GET", asm["authorization_endpoint"] + "?" + qqs)
    csrf = json.loads(re.search(r'id="connect-data">(.*?)</script>', pg.decode()).group(1))["csrf"]
    _, _, bb = consent(qqs, csrf, carol["api_key"])
    return v, urllib.parse.parse_qs(urllib.parse.urlparse(json.loads(bb)["redirect_to"]).query)["code"][0]

v, code = fresh_code()
st, _, b = req("POST", tok, form={"grant_type": "authorization_code", "code": code, "redirect_uri": redirect + "x", "client_id": cid, "code_verifier": v})
check(st == 400 and json.loads(b)["error"] == "invalid_grant", "wrong redirect_uri at token refused")

v, code = fresh_code()
st, h, b = req("POST", tok, form={"grant_type": "authorization_code", "code": code, "redirect_uri": redirect, "client_id": cid, "code_verifier": v, "resource": BASE + "/mcp"})
tokens = json.loads(b)
check(st == 200 and tokens["token_type"] == "Bearer" and tokens["expires_in"] == 3600 and h.get("Cache-Control") == "no-store", "code -> access + refresh tokens")
at, rt = tokens["access_token"], tokens["refresh_token"]
st, _, b = req("POST", tok, form={"grant_type": "authorization_code", "code": code, "redirect_uri": redirect, "client_id": cid, "code_verifier": v})
check(st == 400, "code reuse refused")
st, _, b = req("POST", "/mcp", {"jsonrpc": "2.0", "id": 1, "method": "ping"}, {"Authorization": "Bearer " + at})
check(st == 401, "code reuse revoked the tokens the first redemption issued")

# again, cleanly
v, code = fresh_code()
st, _, b = req("POST", tok, form={"grant_type": "authorization_code", "code": code, "redirect_uri": redirect, "client_id": cid, "code_verifier": v, "resource": BASE + "/mcp"})
tokens = json.loads(b); at, rt = tokens["access_token"], tokens["refresh_token"]

def rpc(method, params=None, token=None, id_=1, headers=None):
    hh = {"Authorization": "Bearer " + (token or at), "Accept": "application/json, text/event-stream", "MCP-Protocol-Version": "2025-06-18"}
    hh.update(headers or {})
    st, h, b = req("POST", "/mcp", {"jsonrpc": "2.0", "id": id_, "method": method, "params": params or {}}, hh)
    return st, json.loads(b) if b else None

st, r = rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "e2e", "version": "1"}}, headers={"MCP-Protocol-Version": ""})
check(st == 200 and r["result"]["protocolVersion"] == "2025-06-18" and "relative links" in r["result"]["instructions"].lower().replace("relative links only", "relative links"), "initialize (2025-06-18) with instructions")
st, _, _ = req("POST", "/mcp", {"jsonrpc": "2.0", "method": "notifications/initialized"}, {"Authorization": "Bearer " + at})
check(st == 202, "notifications/initialized -> 202")
st, r = rpc("tools/list")
names = [t["name"] for t in r["result"]["tools"]]
check({"who_am_i", "list_sites", "deploy_site", "delete_site", "get_state", "update_state", "read_collection"} <= set(names), "tools/list: " + ", ".join(names))
st, r = rpc("tools/call", {"name": "who_am_i", "arguments": {}})
check(r["result"]["structuredContent"]["email"] == carol["username"], "who_am_i is the consenting person")

html = "<!DOCTYPE html><html><head><title>E2E</title><link rel=stylesheet href=css/s.css></head><body><h1>Hello from the connector</h1></body></html>"
st, r = rpc("tools/call", {"name": "deploy_site", "arguments": {"site": ESITE, "mode": "create", "files": {"index.html": html, "css/s.css": "h1{color:teal}"}}})
res = r["result"]
check(not res["isError"] and res["structuredContent"]["active_version"] == 1, "deploy_site created the site: " + res["content"][0]["text"])
url = res["structuredContent"]["url"]
path = urllib.parse.urlparse(url).path  # /<handle>/<site>/
handle, site = path.strip("/").split("/")
st, _, body = req("GET", CONTENT + "/" + handle + "/" + site + "/current/index.html")
check(st == 200 and b"Hello from the connector" in body, "published page is on disk and served: " + url)

st, r = rpc("tools/call", {"name": "deploy_site", "arguments": {"site": ESITE, "mode": "create", "files": {"index.html": html}}})
check(r["result"]["isError"] and "409" in r["result"]["content"][0]["text"], "mode=create refuses to overwrite")
st, r = rpc("tools/call", {"name": "deploy_site", "arguments": {"site": ESITE, "files": {"index.html": html.replace("Hello", "Hello again")}}})
check(r["result"]["structuredContent"]["active_version"] == 2, "mode=auto publishes v2")
st, r = rpc("tools/call", {"name": "read_site_file", "arguments": {"site": ESITE, "path": "index.html"}})
check("Hello again" in r["result"]["content"][0]["text"], "read_site_file reads the live version")
st, r = rpc("tools/call", {"name": "rollback_site", "arguments": {"site": ESITE, "version": 1}})
check(not r["result"]["isError"] and r["result"]["structuredContent"]["active_version"] == 1, "rollback_site")
st, r = rpc("tools/call", {"name": "update_state", "arguments": {"site": ESITE, "ops": [{"op": "inc", "path": "count", "by": 2}]}})
check(not r["result"]["isError"] and r["result"]["structuredContent"]["state"]["count"] == 2, "update_state ops")
st, r = rpc("tools/call", {"name": "add_to_collection", "arguments": {"site": ESITE, "collection": "rsvps", "item": {"name": "Ann"}}})
check(not r["result"]["isError"], "add_to_collection")
st, r = rpc("tools/call", {"name": "read_collection", "arguments": {"site": ESITE, "collection": "rsvps"}})
check(r["result"]["structuredContent"]["items"][0]["data"]["name"] == "Ann", "read_collection")
st, r = rpc("tools/call", {"name": "get_site", "arguments": {"site": ESITE}})
check(len(r["result"]["structuredContent"]["files"]) == 2, "get_site lists files")

# Permissions: dave's site cannot be touched through carol's token
st, _, b = req("POST", "/v1/sites/"+DSITE+"/files", {"files": {"index.html": "<p>dave</p>"}}, {"X-API-Key": dave["api_key"]})
check(st == 201, "dave deploys daves-site over REST")
st, r = rpc("tools/call", {"name": "delete_site", "arguments": {"site": DSITE, "confirm_name": DSITE}})
check(r["result"]["isError"] and "404" in r["result"]["content"][0]["text"], "delete_site on another person's site refused (404, as REST)")
st, r = rpc("tools/call", {"name": "deploy_site", "arguments": {"site": DSITE, "mode": "replace", "files": {"index.html": "x"}}})
check(r["result"]["isError"], "replace on another person's site refused")
st, _, b = req("DELETE", "/v1/sites/"+DSITE+"", None, {"X-API-Key": carol["api_key"]})
check(st == 404, "(same refusal over REST with carol's key)")
st, r = rpc("tools/call", {"name": "delete_site", "arguments": {"site": ESITE, "confirm_name": "wrong"}})
check(r["result"]["isError"] and "nothing was deleted" in r["result"]["content"][0]["text"], "delete_site needs matching confirm_name")

# /mcp-audience token is not a REST credential
st, _, b = req("GET", "/v1/sites", None, {"Authorization": "Bearer " + at})
check(st == 401, "an /mcp-audience token is refused on /v1")

# refresh rotation + reuse detection
st, _, b = req("POST", tok, form={"grant_type": "refresh_token", "refresh_token": rt, "client_id": cid})
t2 = json.loads(b)
check(st == 200 and t2["refresh_token"] != rt, "refresh rotates")
st, r = rpc("ping", token=t2["access_token"])
check(st == 200, "new access token works")
st, _, b = req("POST", tok, form={"grant_type": "refresh_token", "refresh_token": rt, "client_id": cid})
check(st == 400 and json.loads(b)["error"] == "invalid_grant", "reused refresh token refused")
st, r = rpc("ping", token=t2["access_token"])
check(st == 401, "reuse revoked the whole family (new access token dead)")
st, _, b = req("POST", tok, form={"grant_type": "refresh_token", "refresh_token": t2["refresh_token"], "client_id": cid})
check(st == 400, "reuse revoked the whole family (new refresh token dead)")

# revocation + connected apps
v, code = fresh_code()
_, _, b = req("POST", tok, form={"grant_type": "authorization_code", "code": code, "redirect_uri": redirect, "client_id": cid, "code_verifier": v, "resource": BASE + "/mcp"})
t3 = json.loads(b)
st, _, b = req("GET", "/v1/me/connections", None, {"X-API-Key": carol["api_key"]})
conns = json.loads(b)["connections"]
check(len(conns) == 1 and conns[0]["name"] == "E2E Chat", "connected apps lists the app")
st, _, _ = req("POST", "/oauth/revoke", form={"token": t3["access_token"], "client_id": cid})
st, r = rpc("ping", token=t3["access_token"])
check(st == 401, "revoked access token rejected")
st, _, b = req("POST", tok, form={"grant_type": "refresh_token", "refresh_token": t3["refresh_token"], "client_id": cid})
t4 = json.loads(b)
check(st == 200, "refresh still works after revoking only the access token")
st, _, _ = req("DELETE", "/v1/me/connections/" + cid, None, {"X-API-Key": carol["api_key"]})
check(st == 204, "disconnect app")
st, r = rpc("ping", token=t4["access_token"])
check(st == 401, "disconnect kills live tokens")

# Confidential GPT-style client, no PKCE, REST with bearer
import subprocess
out = subprocess.run([os.environ["BIN"], "oauth-client", "create", "--name", "Simple Host GPT", "--redirect-uri", "https://chatgpt.com/aip/g-e2e/oauth/callback", "--no-pkce"], capture_output=True, text=True, check=True).stdout
gid = re.search(r"client_id:\s+(\S+)", out).group(1); gsecret = re.search(r"client_secret:\s+(\S+)", out).group(1)
gredirect = "https://chatgpt.com/aip/g-e2e/oauth/callback"
gq = {"response_type": "code", "client_id": gid, "redirect_uri": gredirect, "scope": "sites", "state": "gptstate"}
gqs = urllib.parse.urlencode(gq)
st, _, pg = req("GET", "/oauth/authorize?" + gqs)
csrf = json.loads(re.search(r'id="connect-data">(.*?)</script>', pg.decode()).group(1))["csrf"]
_, _, b = consent(gqs, csrf, carol["api_key"])
gcode = urllib.parse.parse_qs(urllib.parse.urlparse(json.loads(b)["redirect_to"]).query)["code"][0]
st, _, b = req("POST", tok, form={"grant_type": "authorization_code", "code": gcode, "redirect_uri": gredirect, "client_id": gid, "client_secret": "wrong"})
check(st == 401 and json.loads(b)["error"] == "invalid_client", "confidential client: wrong secret refused")
st, _, b = req("POST", tok, form={"grant_type": "authorization_code", "code": gcode, "redirect_uri": gredirect, "client_id": gid, "client_secret": gsecret})
gt = json.loads(b)
check(st == 200, "GPT client (client_secret_post, no PKCE) gets tokens")
st, _, b = req("GET", "/v1/sites", None, {"Authorization": "Bearer " + gt["access_token"]})
check(st == 200 and any(s["name"] == ESITE for s in json.loads(b)["data"]), "bearer token acts as carol on REST /v1/sites")
st, _, b = req("POST", "/v1/sites/gpt-site/files", {"files": {"index.html": "<p>gpt</p>"}}, {"Authorization": "Bearer " + gt["access_token"]})
check(st == 201, "bearer token deploys over REST")
st, _, b = req("DELETE", "/v1/sites/"+DSITE+"", None, {"Authorization": "Bearer " + gt["access_token"]})
check(st == 404, "bearer token cannot delete another person's site over REST")
st, h, b = req("GET", "/v1/sites", None, {"Authorization": "Bearer shat_notarealtoken"})
check(st == 401 and "invalid_token" in (h.get("WWW-Authenticate") or h.get("Www-Authenticate") or ""), "bad bearer on REST -> 401 invalid_token")
basic = base64.b64encode((gid + ":" + gsecret).encode()).decode()
st, _, b = req("POST", tok, form={"grant_type": "refresh_token", "refresh_token": gt["refresh_token"]}, headers={"Authorization": "Basic " + basic})
check(st == 200, "confidential client refresh with client_secret_basic")
gt2 = json.loads(b)
# PKCE downgrade: public-client-required PKCE still enforced for DCR client (covered above);
# key rotation disconnects apps
st, _, b = req("POST", "/v1/me/api-key/rotate", None, {"X-API-Key": carol["api_key"]})
check(st == 200, "rotate carol's key")
st, _, b = req("GET", "/v1/sites", None, {"Authorization": "Bearer " + gt2["access_token"]})
check(st == 401, "rotating the API key revoked the app's tokens")

print(f"\nALL {ok_count} CHECKS PASSED")
