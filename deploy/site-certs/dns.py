#!/usr/bin/env python3
"""simple-host-site-certs-dns ensure <handle> <zone> <ip>

Make sure <handle>.<zone> and *.<handle>.<zone> have explicit A records at
Vercel DNS, then wait until every authoritative nameserver answers them.
Reads VTOKEN/TEAM from /etc/certbot-vercel/vercel-dns.env. Never prints the
token.
"""
import json
import re
import subprocess
import sys
import time
import urllib.parse
import urllib.request

ENV = "/etc/certbot-vercel/vercel-dns.env"
API = "https://api.vercel.com"
LABEL = re.compile(r"^[a-z0-9]([a-z0-9-]{0,37}[a-z0-9])?$")


def env():
    out = {}
    with open(ENV) as f:
        for line in f:
            line = line.strip()
            if "=" in line and not line.startswith("#"):
                k, v = line.split("=", 1)
                out[k.strip().removeprefix("export ").strip()] = v.strip().strip("'\"")
    return out


def call(method, path, token, team, body=None):
    q = "&" if "?" in path else "?"
    url = f"{API}{path}{q}teamId={urllib.parse.quote(team)}" if team else f"{API}{path}"
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Authorization", "Bearer " + token)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.load(r)


def records(zone, token, team):
    out, until = [], None
    for _ in range(50):
        path = f"/v4/domains/{zone}/records?limit=100" + (f"&until={until}" if until else "")
        d = call("GET", path, token, team)
        out += d.get("records", [])
        until = (d.get("pagination") or {}).get("next")
        if not until:
            return out
    return out


def dig(name, ns):
    r = subprocess.run(["dig", "+short", "+norecurse", "+time=3", "+tries=1", "A", name, "@" + ns],
                       capture_output=True, text=True)
    return r.stdout.split()


def main():
    if len(sys.argv) != 5 or sys.argv[1] != "ensure":
        print(__doc__, file=sys.stderr)
        return 2
    handle, zone, ip = sys.argv[2], sys.argv[3], sys.argv[4]
    if not LABEL.match(handle) or not re.match(r"^[0-9]{1,3}(\.[0-9]{1,3}){3}$", ip):
        print("dns: bad arguments", file=sys.stderr)
        return 2
    e = env()
    token, team = e.get("VTOKEN", ""), e.get("TEAM", "")
    if not token:
        print("dns: no token", file=sys.stderr)
        return 1
    have = {(r.get("type"), r.get("name")) for r in records(zone, token, team)}
    # The handle first: once *.<handle> exists, <handle> itself stops being
    # covered by the zone wildcard, so it must already have its own record.
    for name in (handle, "*." + handle):
        if ("A", name) in have:
            continue
        call("POST", f"/v2/domains/{zone}/records", token, team,
             {"type": "A", "name": name, "value": ip, "ttl": 60})
        print(f"dns: created A {name}.{zone}")
    nss = subprocess.run(["dig", "+short", "NS", zone], capture_output=True, text=True).stdout.split()
    nss = [n.rstrip(".") for n in nss] or ["ns1.vercel-dns.com", "ns2.vercel-dns.com"]
    deadline = time.time() + 180
    while True:
        ok = all(ip in dig(n, ns) for ns in nss for n in (f"{handle}.{zone}", f"probe.{handle}.{zone}"))
        if ok:
            return 0
        if time.time() > deadline:
            print(f"dns: {handle}.{zone} not answered everywhere after 180s", file=sys.stderr)
            return 1
        time.sleep(5)


if __name__ == "__main__":
    sys.exit(main())
