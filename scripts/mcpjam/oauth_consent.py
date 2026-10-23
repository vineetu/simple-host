#!/usr/bin/env python3
"""Run an interactive `mcpjam oauth ...` command and approve its consent page
as the fixture's test account, the way the page's own script does.

Usage: BASE=https://localhost:18443 SH_TEST_KEY=... oauth_consent.py <mcpjam command with --print-url>
"""
import json, os, re, ssl, subprocess, sys, threading, urllib.parse, urllib.request

BASE = os.environ["BASE"]
KEY = os.environ["SH_TEST_KEY"]
ctx = ssl.create_default_context(cafile=os.environ.get("NODE_EXTRA_CA_CERTS"))


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None


opener = urllib.request.build_opener(urllib.request.HTTPSHandler(context=ctx), NoRedirect)
proc = subprocess.Popen(sys.argv[1:], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
# Drain stdout alongside stderr so a chatty child never blocks.
stdout = []
reader = threading.Thread(target=lambda: stdout.append(proc.stdout.read()), daemon=True)
reader.start()
url = None
for line in proc.stderr:
    m = re.search(r"(https://\S+/oauth/authorize\?\S+)", line)
    if m:
        url = m.group(1)
        break
    sys.stderr.write(line)
if not url:
    proc.wait(timeout=300)
    sys.exit("oauth_consent: the command printed no consent URL")
page = opener.open(url, timeout=30).read().decode()
csrf = json.loads(re.search(r'id="connect-data">(.*?)</script>', page, re.S).group(1))["csrf"]
body = json.dumps({"query": urllib.parse.urlparse(url).query, "csrf": csrf, "decision": "allow"}).encode()
decision = urllib.request.Request(BASE + "/oauth/authorize/decision", data=body, method="POST", headers={
    "Content-Type": "application/json", "X-API-Key": KEY, "Origin": BASE, "Sec-Fetch-Site": "same-origin"})
redirect_to = json.loads(opener.open(decision, timeout=30).read())["redirect_to"]
# The callback is the CLI's own loopback listener.
try:
    urllib.request.urlopen(redirect_to, timeout=30).read()
except Exception:
    pass
err = proc.stderr.read()
proc.wait(timeout=300)
reader.join(timeout=30)
sys.stderr.write(err)
sys.stdout.write("".join(stdout))
sys.exit(proc.returncode)
