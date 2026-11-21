#!/usr/bin/env python3
"""Seed the MCPJam fixture (scripts/mcpjam/fixture.sh). Usage: seed.py <host|hack> <FIX dir>

host: for the eval suite, site eval-cafe with a public kv resource `menu`, a
sqlite resource `bookings` and a files resource `photos`, in user.key's account.

hack: run by fixture.sh itself. user.key organises event `mcpjam-hack`, open to
join; participant.key joins it and creates team `Byte Club`; judge.key joins as
its judge; newcomer.key stays out for the participant eval to join with
join.code. A second event, `mcpjam-judging`, is in judging with team `Pixel
Pushers`'s submitted entry and the same judge. Writes event.slug and join.code."""
import json, os, sys, urllib.request

PRODUCT, FIX = sys.argv[1], sys.argv[2]
URL = f"http://127.0.0.1:{os.environ.get('FIXTURE_PORT', '18190')}/mcp"


def key(name):
    return open(os.path.join(FIX, name)).read().strip()


def call(who, name, args):
    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": name, "arguments": args}}).encode()
    req = urllib.request.Request(URL, data=body, method="POST", headers={
        "Content-Type": "application/json", "X-API-Key": key(who), "MCP-Protocol-Version": "2025-06-18"})
    result = json.loads(urllib.request.urlopen(req).read())["result"]
    if result.get("isError"):
        sys.exit(f"{name}: {result['content'][0]['text']}")
    return result.get("structuredContent") or {}


if PRODUCT == "host":
    call("user.key", "create_site", {"site": "eval-cafe", "files": {"index.html": "<!DOCTYPE html><title>Corner Cafe</title><h1>Corner Cafe</h1><p>Open 8am to 6pm.</p>"}})
    call("user.key", "storage_set_resource", {"site": "eval-cafe", "name": "menu", "body": {"kind": "kv", "rules": {"version": 1, "policies": [{"name": "read", "command": "read", "using": "true"}, {"name": "owner", "command": "all", "using": "auth.role == \"owner\""}]}}})
    call("user.key", "storage_put_kv", {"site": "eval-cafe", "name": "menu", "key": "today", "value": {"special": "Tomato soup"}})
    call("user.key", "storage_set_resource", {"site": "eval-cafe", "name": "bookings", "body": {"kind": "sqlite", "rules": {"version": 1, "policies": [{"name": "read", "command": "read", "using": "visitor_id == auth.id"}, {"name": "add", "command": "add", "check": "auth.signed_in"}, {"name": "owner", "command": "all", "using": "auth.role == \"owner\""}]}}})
    call("user.key", "storage_sql_schema", {"site": "eval-cafe", "name": "bookings", "sql": "CREATE TABLE IF NOT EXISTS bookings (id INTEGER PRIMARY KEY, visitor_id TEXT, guest TEXT, party_size INTEGER, created_at TEXT)"})
    call("user.key", "storage_set_resource", {"site": "eval-cafe", "name": "photos", "body": {"kind": "files", "rules": {"version": 1, "policies": [{"name": "read", "command": "read", "using": "true"}]}}})
    print("seeded eval-cafe")
elif PRODUCT == "hack":
    slug = "mcpjam-hack"
    event = call("user.key", "hack_create_event", {
        "slug": slug, "title": "MCPJam Hack", "organiser_name": "MCPJam CI", "contact_email": "mcpjam-ci@example.com",
        "purpose": "A throwaway event for connector checks.", "expected_participants": 10, "starts_at": "2030-01-10", "ends_at": "2030-01-11"})
    call("user.key", "hack_set_event_stage", {"slug": slug, "stage": "open"})
    organiser = event.get("organiser") or {}
    join, judge = organiser.get("join_code"), organiser.get("judge_code")
    if not join or not judge:
        sys.exit("hack_create_event returned no join or judge code")
    call("participant.key", "hack_join_event", {"code": join, "accept_coc": True, "display_name": "Pat Participant"})
    call("participant.key", "hack_create_team", {"slug": slug, "name": "Byte Club"})
    call("judge.key", "hack_join_judge", {"code": judge, "accept_coc": True, "display_name": "Jo Judge"})
    for name, value in (("event.slug", slug), ("join.code", join)):
        with open(os.path.join(FIX, name), "w") as f:
            f.write(value)
    # A second event already in judging, with one submitted entry, for the
    # judge's eval: scoring opens only at the Judging stage.
    done = "mcpjam-judging"
    event = call("user.key", "hack_create_event", {
        "slug": done, "title": "MCPJam Judging", "organiser_name": "MCPJam CI", "contact_email": "mcpjam-ci@example.com",
        "purpose": "A throwaway event in judging for connector checks.", "expected_participants": 10, "starts_at": "2030-01-10", "ends_at": "2030-01-11"})
    call("user.key", "hack_set_event_stage", {"slug": done, "stage": "open"})
    organiser = event.get("organiser") or {}
    call("participant.key", "hack_join_event", {"code": organiser["join_code"], "accept_coc": True})
    call("participant.key", "hack_create_team", {"slug": done, "name": "Pixel Pushers"})
    call("participant.key", "hack_update_entry", {"slug": done, "title": "Lunch Line", "problem": "Queues at the canteen are long.",
                                                  "tagline": "Order ahead from your phone.", "tools": ["HTML", "SQLite"], "maturity": "working",
                                                  "video_url": "https://example.com/lunch-line-demo"})
    call("participant.key", "hack_submit_entry", {"slug": done})
    call("judge.key", "hack_join_judge", {"code": organiser["judge_code"], "accept_coc": True})
    for stage in ("building", "closed", "judging"):
        call("user.key", "hack_set_event_stage", {"slug": done, "stage": stage})
    print(f"seeded {slug}: participant in team Byte Club, one judge; {done}: in judging, one submitted entry")
else:
    sys.exit("usage: seed.py <host|hack> <FIX dir>")
