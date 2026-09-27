#!/usr/bin/env python3
# A fake OpenAI-compatible model backend for scripts/e2e-setup-check.js: answers
# every chat.completions call with canned setup-check findings for the product
# named in the last message (one of them with an out-of-range suggestion the
# server must drop), and appends each request body to <log>.
#   python3 scripts/e2e-setup-check-sidecar.py <port> <log>
import json, http.server, sys
FIND = {
 "enterprise": {"findings":[
  {"severity":"warn","settings":["MAX_ARCHIVE_BYTES","UPLOAD_CONCURRENCY"],"message":"8 uploads of up to 500 MB at once need about 4 GB of memory per pod, twice the default 2 GiB limit. Lower the concurrency or raise the pod's memory.","suggest":{"UPLOAD_CONCURRENCY":"2"}},
  {"severity":"warn","settings":["MAX_ARCHIVE_BYTES"],"message":"The ingress must accept bodies of at least 500 MB; the package's ingress allows 128m, so large uploads would fail there."},
  {"severity":"info","settings":["SESSION_TTL"],"message":"A 24-hour sign-in is longer than the 8 hours the pages promise; a leaver's browser stays signed in longer.","suggest":{"SESSION_TTL":"8h"}},
  {"severity":"warn","settings":["UPLOAD_CONCURRENCY"],"message":"This one suggests a value out of range and must be dropped.","suggest":{"UPLOAD_CONCURRENCY":"999"}}]},
 "small-box": {"findings":[
  {"severity":"warn","settings":["MAX_ARCHIVE_MB"],"message":"1000 MB uploads on a 1 GB box risk running out of memory and disk.","suggest":{"MAX_ARCHIVE_MB":"200"}},
  {"severity":"info","settings":["SAVED_DATA_UNDO_DAYS"],"message":"Owners can undo saved-data changes for only 3 days instead of 30."}]},
}
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        user = body['messages'][-1]['content']
        with open(sys.argv[2], 'a') as f: f.write(json.dumps(body)[:200000] + "\n")
        product = json.loads(user).get('product', 'small-box')
        out = {"choices":[{"message":{"role":"assistant","content":json.dumps(FIND[product])},"finish_reason":"stop"}]}
        b = json.dumps(out).encode()
        self.send_response(200); self.send_header('Content-Type','application/json'); self.send_header('Content-Length',str(len(b))); self.end_headers(); self.wfile.write(b)
    def log_message(self, *a): pass
http.server.HTTPServer(('127.0.0.1', int(sys.argv[1])), H).serve_forever()
