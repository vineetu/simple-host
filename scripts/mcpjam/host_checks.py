#!/usr/bin/env python3
"""The Simple Host connector check against the fixture from fixture.sh
(PRODUCT=host, the rewritten service in next/): tools/list as an ordinary
user's AI app sees it (51 tools, each with annotations and an outputSchema),
one read call, one publish whose page is then served, and one old tool name
answered through next/compat. Usage: host_checks.py <FIX dir> <port>"""
import json, sys, urllib.request

FIX, PORT = sys.argv[1], sys.argv[2]
KEY = open(f"{FIX}/user.key").read().strip()
TOOLS = 51
failed = []


def rpc(method, params=None):
    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params or {}}).encode()
    req = urllib.request.Request(f"http://127.0.0.1:{PORT}/mcp", data=body, headers={
        "Host": "localhost", "Content-Type": "application/json", "Accept": "application/json, text/event-stream",
        "X-API-Key": KEY, "MCP-Protocol-Version": "2025-11-25"})
    return json.loads(urllib.request.urlopen(req, timeout=60).read())


def call(name, args):
    res = rpc("tools/call", {"name": name, "arguments": args}).get("result") or {}
    if res.get("isError"):
        raise RuntimeError(f"{name}: " + " ".join(c.get("text", "") for c in res.get("content", [])))
    return res.get("structuredContent") or {}


def check(name, fn):
    try:
        note = fn()
        print(f"PASS {name}" + (f": {note}" if note else ""))
    except Exception as e:
        print(f"FAIL {name}: {e}")
        failed.append(name)


def listing():
    tools = rpc("tools/list")["result"]["tools"]
    assert len(tools) == TOOLS, f"{len(tools)} tools, want {TOOLS}"
    no_ann = [t["name"] for t in tools if not t.get("annotations")]
    no_out = [t["name"] for t in tools if not t.get("outputSchema")]
    assert not no_ann, f"no annotations: {no_ann}"
    assert not no_out, f"no outputSchema: {no_out}"
    return f"{len(tools)} tools, all with annotations and outputSchema"


def served(url, text):
    host = url.split("//", 1)[1].split("/", 1)[0].split(":")[0]
    req = urllib.request.Request(f"http://127.0.0.1:{PORT}/", headers={"Host": host})
    page = urllib.request.urlopen(req, timeout=30).read().decode()
    assert text in page, f"{host} does not serve {text!r}"
    return host


def publish():
    res = call("publish_site", {"site": "mcpjam-cafe", "files": {"index.html": "<!DOCTYPE html><title>Corner Cafe</title><h1>Corner Cafe</h1>"}})
    return "served at " + served(res["url"], "Corner Cafe")


def old_name():
    res = call("create_site", {"site": "mcpjam-old", "files": {"index.html": "<h1>Old name works</h1>"}})
    return "create_site published, served at " + served(res["url"], "Old name works")


def read():
    names = json.dumps(call("list_sites", {}))
    assert "mcpjam-cafe" in names and "mcpjam-old" in names, "list_sites misses the published sites"
    return "list_sites shows both sites"


init = rpc("initialize", {"protocolVersion": "2025-11-25", "capabilities": {}, "clientInfo": {"name": "connector-check", "version": "1"}})
print("server:", init["result"]["serverInfo"])
check("tools/list", listing)
check("publish", publish)
check("old tool name through compat", old_name)
check("read call", read)
sys.exit(1 if failed else 0)
