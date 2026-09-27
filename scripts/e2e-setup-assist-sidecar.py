#!/usr/bin/env python3
# A fake OpenAI-compatible model backend for scripts/e2e-setup-assist.js. It
# streams (stream: true) canned replies for the setup assistant, chosen from
# the message and choices in the last user message, and answers the setup
# check with no findings. Some replies propose changes the server must drop
# (a looser security value, a no-op), so the page shows only what survives.
# Every request body is appended to <log>.
#   python3 scripts/e2e-setup-assist-sidecar.py <port> <log>
import json, http.server, sys, time

M = "===CHANGES==="

def assist(u):
    msg, product = u.get("message", ""), u.get("product")
    choices = {c["name"]: (c["value"], c["default"]) for c in u.get("choices", [])}
    if u.get("pasted"):
        if product == "small-box":
            return ("The server refuses to start because KEEP_VERSIONS is -1 in its settings file; it must be 0 or more. "
                    "Confirm with: cd /opt/simple-host && sudo docker compose logs --tail 50 app. "
                    "Fix the line in the settings file, then run the installer again; the page can set it to 5 to keep rollback.",
                    {"changes": [{"setting": "KEEP_VERSIONS", "value": "5", "why": "A valid value that keeps rollback."}]})
        return ("The pod cannot reach your identity provider at startup, so it exits and restarts. "
                "Confirm from inside the cluster with a curl pod against the issuer's /.well-known/openid-configuration. "
                "Fix the egress path or OIDC_ISSUER.", {"changes": []})
    if msg.startswith("Clean up"):
        out = []
        for name, (value, default) in sorted(choices.items()):
            if name in ("RATE_LIMIT_STATE", "SESSION_IDLE"):
                out.append({"setting": name, "value": default, "why": "Back to the default: nothing here needs it."})
        text = ("Most of your choices fit together. " + ("One looks unneeded; you can put it back to its default." if out else "Nothing to change."))
        return text, {"changes": out}
    if "200-person" in msg:
        return ("For a 200-person company with Microsoft sign-in, pick Microsoft Entra ID and tighten sessions, API keys and "
                "network approvals. No sizing changes are needed.",
                {"changes": [
                    {"setting": "SESSION_TTL", "value": "4h", "why": "A shorter working session."},
                    {"setting": "SESSION_IDLE", "value": "15m", "why": "Ends unattended sessions sooner."},
                    {"setting": "API_KEY_MAX_DAYS", "value": "30", "why": "Short-lived keys for automation."},
                    {"setting": "NETWORK_ACCESS_APPROVALS", "value": "2", "why": "Two admins approve public sites."},
                    {"setting": "ACCESS_LOG_VISIBILITY", "value": "owner", "why": "Looser: the server must drop this."}],
                 "basics": {"idp": "entra"}})
    if "hackathon" in msg:
        return ("For 150 people in one hall, raise the upload and saved-data limits and cap sites per person; emailed codes are already on.",
                {"changes": [
                    {"setting": "RATE_LIMIT_UPLOAD", "value": "120,2s", "why": "Many people upload from one venue address."},
                    {"setting": "RATE_LIMIT_STATE", "value": "240,250ms", "why": "Busy pages save often."},
                    {"setting": "MAX_SITES_PER_ACCOUNT", "value": "20", "why": "A cap per participant."},
                    {"setting": "RATE_LIMIT_SIGNIN_IP", "value": "500,1s", "why": "Too loose: the server must drop this."}],
                 "basics": {"codes": "true"}})
    if "KEEP_VERSIONS" in msg:
        return "KEEP_VERSIONS is how many versions of each site are kept. The installer keeps 1, so there is no rollback; 0 keeps every version.", {"changes": []}
    return "Sessions last SESSION_TTL (8h by default) and end after SESSION_IDLE (30m) without use.", {"changes": []}

class H(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        with open(sys.argv[2], "a") as f:
            f.write(json.dumps(body)[:400000] + "\n")
        system, user = body["messages"][0]["content"], body["messages"][-1]["content"]
        if system.startswith("You review settings"):
            reply = json.dumps({"findings": []})
            pieces = [reply]
        else:
            text, changes = assist(json.loads(user))
            reply = text + "\n" + M + "\n" + json.dumps(changes)
            words = reply.split(" ")
            pieces = [w + (" " if i < len(words) - 1 else "") for i, w in enumerate(words)]
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()
        def chunk(s):
            b = s.encode()
            self.wfile.write(b"%x\r\n%s\r\n" % (len(b), b))
            self.wfile.flush()
        for p in pieces:
            chunk("data: " + json.dumps({"choices": [{"delta": {"content": p}}]}) + "\n\n")
            time.sleep(0.03)
        chunk("data: " + json.dumps({"choices": [{"delta": {}, "finish_reason": "stop"}]}) + "\n\n")
        chunk("data: [DONE]\n\n")
        self.wfile.write(b"0\r\n\r\n")
    def log_message(self, *a):
        pass

http.server.ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
