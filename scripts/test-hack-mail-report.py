#!/usr/bin/env python3
import importlib.util
import json
import threading
import tempfile
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path


spec = importlib.util.spec_from_file_location("hack_mail_report", Path(__file__).with_name("hack-mail-report.py"))
watch = importlib.util.module_from_spec(spec)
spec.loader.exec_module(watch)


class Provider(BaseHTTPRequestHandler):
    denied = False
    requests = []

    def do_GET(self):
        self.requests.append(self.path)
        if self.denied:
            self.send_response(403)
            self.end_headers()
            self.wfile.write(b'{"error":"secret recipient@example.com"}')
            return
        if "after=host-only" in self.path:
            rows = [
                {"id": "hack-1", "from": "Simple Hack <hack@example.com>", "created_at": "2026-10-01T12:00:00Z", "last_event": "bounced", "to": ["secret@example.com"]},
                {"id": "hack-2", "from": "hack@example.com", "created_at": "2026-10-01T12:01:00Z", "last_event": "delivered", "subject": "private code 654321"},
            ]
            more = False
        else:
            rows = [{"id": "host-only", "from": "hack@example.com", "created_at": "2026-10-01T12:02:00Z", "last_event": "bounced", "to": ["host@example.com"]}]
            more = True
        raw = json.dumps({"object": "list", "has_more": more, "data": rows}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(raw)

    def log_message(self, *_):
        pass


class MailReportTests(unittest.TestCase):
    def test_sender_environment_reads_only_sender(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "hack.env"
            path.write_text("RESEND_API_KEY=private-key\nMAIL_FROM=Simple Hack <hack@example.com>\n")
            self.assertEqual(watch.sender_from_env_file(path), "Simple Hack <hack@example.com>")

    def test_local_counts_and_provider_pagination(self):
        local, ids = watch.parse_journal("""hack_mail_send outcome=accepted id=hack-1
hack_mail_send outcome=accepted id=hack-2
hack_mail_send outcome=failed reason=http_403
hack_mail_send outcome=accepted id=unavailable
""")
        self.assertEqual(local, {"accepted": 3, "failed": 1, "accepted_without_id": 1})
        self.assertEqual(ids, {"hack-1", "hack-2"})
        server = HTTPServer(("127.0.0.1", 0), Provider)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            Provider.requests = []
            Provider.denied = False
            result = watch.provider_snapshot(ids, "private-key", "hack@example.com", f"http://127.0.0.1:{server.server_port}")
            self.assertEqual((result["status"], result["bounced"], result["matched"], result["pages"]), ("complete", 1, 2, 2))
            self.assertIn("after=host-only", Provider.requests[1])
            self.assertNotIn("secret", json.dumps(result))
            self.assertNotIn("654321", json.dumps(result))
            partial = watch.provider_snapshot(ids | {"hack-missing"}, "private-key", "hack@example.com", f"http://127.0.0.1:{server.server_port}")
            self.assertEqual((partial["status"], partial["unmatched"], partial["bounced"]), ("partial", 1, 1))
            with tempfile.TemporaryDirectory() as folder:
                key_file = Path(folder) / "monitor.key"
                env_file = Path(folder) / "hack.env"
                env_file.write_text("RESEND_API_KEY=send-only-secret\nMAIL_FROM=Simple Hack <hack@example.com>\n")
                original = watch.journal_counts
                watch.journal_counts = lambda hours: ({"accepted": 2, "failed": 1, "accepted_without_id": 0}, ids)
                try:
                    missing = watch.report(24, key_file, sender_env_file=env_file, api_base=f"http://127.0.0.1:{server.server_port}")
                    self.assertEqual(missing["provider"], {"status": "unavailable", "reason": "monitor_key_not_configured", "bounced": None})
                    key_file.write_text("private-monitor-key\n")
                    enabled = watch.report(24, key_file, sender_env_file=env_file, api_base=f"http://127.0.0.1:{server.server_port}")
                    self.assertEqual((enabled["provider"]["status"], enabled["provider"]["bounced"]), ("complete", 1))
                    self.assertNotIn("private-monitor-key", json.dumps(enabled))
                    self.assertNotIn("send-only-secret", json.dumps(enabled))
                finally:
                    watch.journal_counts = original
            service = Path(__file__).parent.parent / "deploy/hack/sh-hack-mail-watch.service"
            command = service.read_text()
            self.assertIn("--monitor-key-file /etc/simple-hack-resend-monitor.key", command)
            self.assertIn("--from-env-file /etc/simple-hack.env", command)
            Provider.denied = True
            result = watch.provider_snapshot(ids, "private-key", "hack@example.com", f"http://127.0.0.1:{server.server_port}")
            self.assertEqual(result, {"status": "unavailable", "reason": "permission_denied", "http_status": 403, "bounced": None})
        finally:
            server.shutdown()
            thread.join()
            server.server_close()


if __name__ == "__main__":
    unittest.main()
