#!/usr/bin/env python3
"""Recipient-free Simple Hack send counts and optional Resend status snapshot."""

import argparse
import email.utils
import json
import re
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
from collections import Counter
from pathlib import Path


API_BASE = "https://api.resend.com"
SEND_LINE = re.compile(r"hack_mail_send outcome=(accepted|failed) (?:id=([A-Za-z0-9-]+)|reason=([a-z0-9_]+))")
MAX_PAGES = 50


def journal_counts(hours):
    result = subprocess.run(
        ["journalctl", "-u", "simple-hack.service", "--since", f"{hours} hours ago", "-o", "cat", "--no-pager"],
        text=True, capture_output=True, check=True,
    )
    return parse_journal(result.stdout)


def parse_journal(content):
    counts = Counter()
    ids = set()
    for line in content.splitlines():
        match = SEND_LINE.search(line)
        if not match:
            continue
        outcome, mail_id, _ = match.groups()
        counts[outcome] += 1
        if outcome == "accepted":
            if mail_id and mail_id != "unavailable":
                ids.add(mail_id)
            else:
                counts["accepted_without_id"] += 1
    return {"accepted": counts["accepted"], "failed": counts["failed"],
            "accepted_without_id": counts["accepted_without_id"]}, ids


def sender_address(value):
    return email.utils.parseaddr(value)[1].lower()


def sender_from_env_file(path):
    try:
        for line in Path(path).read_text().splitlines():
            if line.startswith("MAIL_FROM="):
                return line.split("=", 1)[1].strip().strip('"\'')
    except OSError:
        pass
    return ""


def provider_snapshot(ids, key, sender, api_base=API_BASE):
    if not ids:
        return {"status": "unavailable", "reason": "no_tracked_sends", "bounced": None}
    expected = sender_address(sender)
    if not expected:
        return {"status": "unavailable", "reason": "sender_required", "bounced": None}
    remaining = set(ids)
    events = Counter()
    after = ""
    for page_number in range(MAX_PAGES):
        query = urllib.parse.urlencode({"limit": 100, **({"after": after} if after else {})})
        request = urllib.request.Request(
            api_base + "/emails?" + query,
            headers={"Authorization": "Bearer " + key, "Accept": "application/json"},
        )
        try:
            with urllib.request.urlopen(request, timeout=10) as response:
                payload = json.load(response)
        except urllib.error.HTTPError as exc:
            reason = "permission_denied" if exc.code == 403 else "invalid_monitor_key" if exc.code == 401 else "provider_http_error"
            return {"status": "unavailable", "reason": reason, "http_status": exc.code, "bounced": None}
        except (urllib.error.URLError, ValueError, OSError):
            return {"status": "unavailable", "reason": "provider_request_failed", "bounced": None}
        rows = payload.get("data") if isinstance(payload, dict) else None
        if not isinstance(rows, list):
            return {"status": "unavailable", "reason": "invalid_provider_response", "bounced": None}
        for row in rows:
            if not isinstance(row, dict) or row.get("id") not in remaining:
                continue
            if sender_address(str(row.get("from", ""))) != expected:
                return {"status": "unavailable", "reason": "sender_mismatch", "bounced": None}
            if not row.get("created_at") or not row.get("last_event"):
                return {"status": "unavailable", "reason": "invalid_provider_response", "bounced": None}
            remaining.remove(row["id"])
            events[str(row["last_event"])] += 1
        if not remaining or not payload.get("has_more"):
            break
        if not rows or not isinstance(rows[-1], dict) or not rows[-1].get("id") or rows[-1]["id"] == after:
            return {"status": "unavailable", "reason": "invalid_provider_pagination", "bounced": None}
        after = rows[-1]["id"]
    return {"status": "complete" if not remaining else "partial", "matched": len(ids) - len(remaining),
            "unmatched": len(remaining), "pages": page_number + 1,
            "bounced": events["bounced"], "complained": events["complained"],
            "last_events": dict(sorted(events.items()))}


def report(hours, key_file=None, sender=None, sender_env_file=None, api_base=API_BASE):
    local, ids = journal_counts(hours)
    if key_file is None:
        provider = {"status": "unavailable", "reason": "monitor_key_not_configured", "bounced": None}
    else:
        try:
            key = Path(key_file).read_text().strip()
        except OSError:
            provider = {"status": "unavailable", "reason": "monitor_key_unreadable", "bounced": None}
        else:
            if not key:
                provider = {"status": "unavailable", "reason": "empty_monitor_key", "bounced": None}
            else:
                if not sender and sender_env_file:
                    sender = sender_from_env_file(sender_env_file)
                provider = provider_snapshot(ids, key, sender or "", api_base)
    if local["accepted_without_id"] and provider["status"] == "complete":
        provider["status"] = "partial"
        provider["reason"] = "accepted_without_id"
    return {"product": "simple_hack", "window_hours": hours, "send_requests": local, "provider": provider}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--hours", type=int, default=24, help="journal lookback, 1–168 hours")
    parser.add_argument("--monitor-key-file", help="private Resend full-access key file (manual provider check only)")
    parser.add_argument("--from", dest="sender", help="exact Simple Hack sender address required with a monitoring key")
    parser.add_argument("--from-env-file", help="read only MAIL_FROM from the private Simple Hack environment file")
    args = parser.parse_args()
    if not 1 <= args.hours <= 168:
        parser.error("--hours must be between 1 and 168")
    if args.monitor_key_file and not (args.sender or args.from_env_file):
        parser.error("--from or --from-env-file is required with --monitor-key-file")
    try:
        print(json.dumps(report(args.hours, args.monitor_key_file, args.sender, args.from_env_file), sort_keys=True))
    except (OSError, subprocess.CalledProcessError):
        print(json.dumps({"product": "simple_hack", "status": "unavailable", "reason": "journal_unavailable"}))
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
