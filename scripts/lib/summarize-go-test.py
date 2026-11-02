#!/usr/bin/env python3
"""Summarise `go test -json` output for scripts/test-integration.sh.

Reads one JSON-lines file (path given as argv[1]) produced by
`go test -json`, prints:
  - a per-package ok/FAIL/SKIP line
  - the full captured output of every failing test (and, for a package that
    failed outright -- a build failure, a panic in TestMain, anything not
    attributed to one test -- that package's own output, plus any
    build-output lines go recorded for it)
  - pass/fail/skip totals
  - the list of tests that EVER skipped for a DB-related reason (DB_DSN
    unset, or postgres unreachable) in any iteration, even under
    `-count=N` where a later iteration passed -- these are what this
    script exists to catch, because they mean the "integration" run
    silently ran with no database at least once.
  - the count of other (non-DB) skips, e.g. tests that need network access

This is deliberately paranoid about being fed a stream that *isn't*
`go test -json` output (wrong flags, a disabled -json, a truncated pipe):
zero parsed JSON events, or zero passing tests, is itself a failure rather
than being reported as a clean, empty, all-OK run. Non-JSON lines are never
silently dropped -- they're printed so the actual cause (a go tool error, a
runtime crash that bypassed the JSON wrapper) is visible instead of being
reported as nothing but a nonzero go test exit code.

Exit status is nonzero if:
  - any test failed, in any iteration
  - any test, or any package, skipped (or printed) a DB-related reason in
    any iteration
  - go test itself exited nonzero (argv[2], the recorded exit code)
  - zero JSON events were parsed, or zero tests passed
"""
import json
import re
import sys

DB_SKIP_RE = re.compile(r"DB_DSN|postgres not reachable", re.IGNORECASE)


def finalize_test_iteration(test_state, action):
    """Close out one run/pass|fail|skip cycle for a single test, folding its
    result into sticky, cross-iteration counters."""
    output = "".join(test_state["cur_output"])
    test_state["cur_output"] = []
    test_state["last_result"] = action
    if action == "fail":
        test_state["fail_outputs"].append(output)
    elif action == "skip":
        if DB_SKIP_RE.search(output):
            test_state["db_skip_outputs"].append(output)
        else:
            test_state["other_skips"] += 1
    return output


def main():
    if len(sys.argv) < 2:
        print("usage: summarize-go-test.py <go-test-json-file> [go-test-exit-code]", file=sys.stderr)
        return 2
    path = sys.argv[1]
    go_test_exit = int(sys.argv[2]) if len(sys.argv) > 2 else 0

    events_parsed = 0
    non_json_lines = []

    packages = {}  # package -> last pass/fail/skip action seen at package level
    pkg_output = {}  # package -> [output str, ...] (package-level "output" events only)
    build_output = {}  # ImportPath -> [output str, ...] (build-output events)
    build_failed = set()  # ImportPath with a build-fail event

    tests = {}  # (package, test) -> state dict, insertion-ordered

    with open(path, "r", encoding="utf-8", errors="replace") as f:
        for raw_line in f:
            line = raw_line.rstrip("\n")
            if not line.strip():
                continue
            try:
                rec = json.loads(line)
            except ValueError:
                non_json_lines.append(line)
                continue
            events_parsed += 1

            action = rec.get("Action")
            pkg = rec.get("Package")
            test = rec.get("Test")

            if pkg is None and "ImportPath" in rec:
                # A build failure: these events are keyed by ImportPath
                # (package import path + ".test"), not Package, and never
                # carry a Test field.
                importpath = rec["ImportPath"]
                if action == "build-output":
                    build_output.setdefault(importpath, []).append(rec.get("Output", ""))
                elif action == "build-fail":
                    build_failed.add(importpath)
                continue

            if test:
                key = (pkg, test)
                state = tests.setdefault(key, {
                    "cur_output": [],
                    "last_result": None,
                    "fail_outputs": [],
                    "db_skip_outputs": [],
                    "other_skips": 0,
                })
                if action == "run":
                    # Start of a fresh iteration (relevant under -count>1,
                    # where the same (package, test) key repeats with its
                    # own independent run/output/result cycle). Resetting
                    # here is what keeps a DB skip in iteration 1 from being
                    # silently overwritten by a pass in iteration 2.
                    state["cur_output"] = []
                elif action == "output":
                    state["cur_output"].append(rec.get("Output", ""))
                elif action in ("pass", "fail", "skip"):
                    finalize_test_iteration(state, action)
            else:
                # Package-level event.
                if action == "output":
                    pkg_output.setdefault(pkg, []).append(rec.get("Output", ""))
                elif action in ("pass", "fail", "skip"):
                    packages[pkg] = action

    # ---- guard against a stream that isn't real `go test -json` output ----
    if events_parsed == 0:
        print("FAIL: parsed zero JSON events from go test output.", file=sys.stderr)
        if non_json_lines:
            print("== non-JSON output ==", file=sys.stderr)
            for line in non_json_lines[:200]:
                print(line, file=sys.stderr)
        print("(Did -json get disabled, or was the output stream empty/truncated?)", file=sys.stderr)
        return 1

    n_pass = n_fail = n_skip = 0
    db_skip_tests = []  # (key, [output, ...])
    other_skip_total = 0
    failed_tests = []  # (key, [output, ...])

    for key, state in tests.items():
        # last_result may be None if a "run" never resolved (e.g. the test
        # binary crashed mid-test); that's a failure too, not silence.
        result = state["last_result"]
        if result == "pass":
            n_pass += 1
        elif result == "fail":
            n_fail += 1
        elif result == "skip":
            n_skip += 1
        elif result is None and (state["fail_outputs"] or state["db_skip_outputs"] or state["cur_output"]):
            # Never reached a terminal action -- treat as a failure so a
            # mid-test crash can't disappear.
            n_fail += 1
            state["fail_outputs"].append("".join(state["cur_output"]) or "(test never reached a pass/fail/skip result)")

        if state["fail_outputs"]:
            failed_tests.append((key, state["fail_outputs"]))
        if state["db_skip_outputs"]:
            db_skip_tests.append((key, state["db_skip_outputs"]))
        other_skip_total += state["other_skips"]

    # Package-level output or a package-level skip can also carry a
    # DB-related reason (e.g. a TestMain that skips the whole package
    # before attributing it to any one test); catch that too.
    db_skip_packages = []
    for pkg, lines in pkg_output.items():
        text = "".join(lines)
        if DB_SKIP_RE.search(text):
            db_skip_packages.append((pkg, text))

    print("== per-package result ==")
    all_pkgs = sorted(set(packages) | set(pkg_output))
    for pkg in all_pkgs:
        result = packages.get(pkg)
        if result == "pass":
            label = "ok  "
        elif result == "skip":
            label = "SKIP"
        else:
            # fail, or no explicit package result at all (e.g. a build
            # failure where only the .test-suffixed ImportPath got a
            # build-fail event) -- treat as failing, not silently omitted.
            label = "FAIL"
        print(f"{label} {pkg}")

    if build_failed or build_output:
        print()
        print("== build failures ==")
        for importpath in sorted(set(build_failed) | set(build_output)):
            print(f"--- build failed: {importpath} ---")
            sys.stdout.write("".join(build_output.get(importpath, [])))
            print()

    if failed_tests:
        print()
        print("== failing tests (full output, every iteration) ==")
        for key, outputs in failed_tests:
            pkg, name = key
            for i, output in enumerate(outputs):
                suffix = f" [iteration {i + 1}]" if len(outputs) > 1 else ""
                print(f"--- FAIL: {name} ({pkg}){suffix} ---")
                sys.stdout.write(output)
                print()

    failed_packages_without_test_detail = [
        pkg for pkg, result in packages.items() if result == "fail"
    ]
    if failed_packages_without_test_detail:
        print()
        print("== package-level output for failed packages ==")
        for pkg in sorted(failed_packages_without_test_detail):
            print(f"--- package output: {pkg} ---")
            sys.stdout.write("".join(pkg_output.get(pkg, [])))
            print()

    if non_json_lines:
        print()
        print(f"== non-JSON lines in go test output ({len(non_json_lines)}) ==")
        for line in non_json_lines[:200]:
            print(line)

    print()
    print("== totals ==")
    print(
        f"pass={n_pass} fail={n_fail} skip={n_skip} "
        f"(db-related-skip={len(db_skip_tests) + len(db_skip_packages)}, other-skip={other_skip_total})"
    )

    exit_code = 0
    if go_test_exit != 0:
        print(f"go test itself exited {go_test_exit}", file=sys.stderr)
        exit_code = 1
    if n_pass == 0:
        print("FAIL: zero tests passed -- treating as a broken run, not a clean one.", file=sys.stderr)
        exit_code = 1
    if n_fail:
        exit_code = 1
    if build_failed:
        exit_code = 1

    if db_skip_tests or db_skip_packages:
        print()
        print("== DB-RELATED SKIPS (these must run against a real database, not skip) ==")
        for key, outputs in db_skip_tests:
            pkg, name = key
            for output in outputs:
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
        for pkg, text in db_skip_packages:
            reason = ""
            for out_line in text.splitlines():
                stripped = out_line.strip()
                if stripped and DB_SKIP_RE.search(stripped):
                    reason = stripped
                    break
            print(f"  [package] {pkg}: {reason}")
        exit_code = 1

    return exit_code


if __name__ == "__main__":
    sys.exit(main())
