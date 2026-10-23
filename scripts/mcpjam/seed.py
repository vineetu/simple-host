#!/usr/bin/env python3
"""Seed the fixture's test account for the eval suite: site eval-cafe with a
public kv resource `menu`, a sqlite resource `bookings` and a files resource
`photos`. Usage: seed.py <user.key path>"""
import json, sys, urllib.request

KEY = open(sys.argv[1]).read().strip()


def call(name, args):
    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": name, "arguments": args}}).encode()
    req = urllib.request.Request("http://127.0.0.1:18190/mcp", data=body, method="POST", headers={
        "Content-Type": "application/json", "X-API-Key": KEY, "MCP-Protocol-Version": "2025-06-18"})
    result = json.loads(urllib.request.urlopen(req).read())["result"]
    if result.get("isError"):
        sys.exit(f"{name}: {result['content'][0]['text']}")


call("create_site", {"site": "eval-cafe", "files": {"index.html": "<!DOCTYPE html><title>Corner Cafe</title><h1>Corner Cafe</h1><p>Open 8am to 6pm.</p>"}})
call("storage_set_resource", {"site": "eval-cafe", "name": "menu", "body": {"kind": "kv", "preset": "public"}})
call("storage_put_kv", {"site": "eval-cafe", "name": "menu", "key": "today", "value": {"special": "Tomato soup"}})
call("storage_set_resource", {"site": "eval-cafe", "name": "bookings", "body": {"kind": "sqlite", "preset": "records"}})
call("storage_sql_schema", {"site": "eval-cafe", "name": "bookings", "sql": "CREATE TABLE IF NOT EXISTS bookings (id INTEGER PRIMARY KEY, guest TEXT, party_size INTEGER, created_at TEXT)"})
call("storage_set_resource", {"site": "eval-cafe", "name": "photos", "body": {"kind": "files", "preset": "public"}})
print("seeded eval-cafe")
