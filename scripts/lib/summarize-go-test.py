#!/usr/bin/env python3
"""Summarise `go test -json` output for scripts/test-integration.sh.

Reads one JSON-lines file (path given as argv[1]) produced by
`go test -json`, prints:
  - a per-package ok/FAIL line
  - the full captured output of every failing test
  - pass/fail/skip totals
  - the list of tests skipped for a DB-related reason (DB_DSN unset, or
    postgres unreachable) -- these are what this script exists to catch,
    because they mean the "integration" run silently ran with no database.
  - the count of other (non-DB) skips, e.g. tests that need network access

Exit status is nonzero if any test failed, or if any test had a DB-related
skip. A plain `go test` process failure (e.g. a build error) is passed in
separately via argv[2] (the recorded exit code of the `go test` invocation)
and also makes this exit nonzero.
"""
import json
import re
import sys

DB_SKIP_RE = re.compile(r"DB_DSN|postgres not reachable", re.IGNORECASE)


def main():
    if len(sys.argv) < 2:
        print("usage: summarize-go-test.py <go-test-json-file> [go-test-exit-code]", file=sys.stderr)
        return 2
    path = sys.argv[1]
    go_test_exit = int(sys.argv[2]) if len(sys.argv) > 2 else 0

    # package -> "pass" | "fail" (final package-level result)
    packages = {}
    # (package, test) -> {"result": str|None, "output": [str, ...]}
    tests = {}
    order = []

    with open(path, "r", encoding="utf-8", errors="replace") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
            except ValueError:
                # Non-JSON line (shouldn't happen with -json, but don't die on it).
                continue
            pkg = rec.get("Package", "")
            test = rec.get("Test")
            action = rec.get("Action")
            if test:
                key = (pkg, test)
                if key not in tests:
                    tests[key] = {"result": None, "output": []}
                    order.append(key)
                if action == "output":
                    tests[key]["output"].append(rec.get("Output", ""))
                elif action in ("pass", "fail", "skip"):
                    tests[key]["result"] = action
            else:
                if action in ("pass", "fail"):
                    packages[pkg] = action

    n_pass = n_fail = n_skip = 0
    db_skips = []
    other_skips = 0
    failed_tests = []

    for key in order:
        info = tests[key]
        result = info["result"]
        if result == "pass":
            n_pass += 1
        elif result == "fail":
            n_fail += 1
            failed_tests.append(key)
        elif result == "skip":
            n_skip += 1
            output = "".join(info["output"])
            if DB_SKIP_RE.search(output):
                db_skips.append((key, output))
            else:
                other_skips += 1
        # result is None for parent tests of subtests in some edge cases;
        # ignore those (the subtest itself carries the real result).

    print("== per-package result ==")
    for pkg in sorted(packages):
        result = packages[pkg]
        label = "ok  " if result == "pass" else "FAIL"
        print(f"{label} {pkg}")

    if failed_tests:
        print()
        print("== failing tests (full output) ==")
        for key in failed_tests:
            pkg, name = key
            print(f"--- FAIL: {name} ({pkg}) ---")
            sys.stdout.write("".join(tests[key]["output"]))
            print()

    print()
    print("== totals ==")
    print(f"pass={n_pass} fail={n_fail} skip={n_skip} (db-related-skip={len(db_skips)}, other-skip={other_skips})")

    exit_code = 0
    if go_test_exit != 0:
        print(f"go test itself exited {go_test_exit}", file=sys.stderr)
        exit_code = 1
    if n_fail:
        exit_code = 1
    if db_skips:
        print()
        print("== DB-RELATED SKIPS (these must run against a real database, not skip) ==")
        for (pkg, name), output in db_skips:
            # Prefer the line that actually names the reason (e.g.
            # "netusage_test.go:133: DB_DSN not set") over "=== RUN ...",
            # which is always the first line of any test's output.
            reason = ""
            for out_line in output.splitlines():
                stripped = out_line.strip()
                if stripped and DB_SKIP_RE.search(stripped):
                    reason = stripped
                    break
            if not reason:
                for out_line in output.splitlines():
                    stripped = out_line.strip()
                    if stripped:
                        reason = stripped
                        break
            print(f"  {name} ({pkg}): {reason}")
        exit_code = 1

    return exit_code


if __name__ == "__main__":
    sys.exit(main())
