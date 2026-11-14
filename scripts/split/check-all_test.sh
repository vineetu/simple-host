#!/usr/bin/env bash
set -u
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P) || exit 1
runner=$repo/scripts/split/check-all.sh
tmp=$(mktemp -d "${TMPDIR:-/tmp}/check-all-test.XXXXXXXX") || exit 1
trap 'rm -rf -- "$tmp"' EXIT
failures=0
check() {
    local label=$1
    shift
    if "$@"; then echo "ok - $label"; else echo "FAIL - $label"; failures=$((failures+1)); fi
}
run() {
    local expected=$1 status
    shift
    rm -rf -- "$tmp/out"
    bash "$runner" --no-cap --steps-file "$tmp/steps" --known-red "$tmp/red" --out "$tmp/out" --quiet "$@" >"$tmp/report" 2>&1
    status=$?
    if [[ $status != "$expected" ]]; then cat "$tmp/report"; return 1; fi
}
has() { [[ $(cat "$1") == *"$2"* ]]; }
: >"$tmp/red"
printf '%s\n' '# comment' '' 'true' 'printf done' >"$tmp/steps"
check 'all pass with empty known-red' run 0
check 'summary has one row per step' test "$(wc -l <"$tmp/out/summary.tsv")" -eq 2
printf '%s\n' 'false' "printf later > '$tmp/later'" >"$tmp/steps"
check 'unlisted failure fails gate' run 1
check 'later step still runs' test -f "$tmp/later"
printf '%s\n' 'false | * | D0 | failure' >"$tmp/red"
check 'listed failure passes gate' run 0
printf '%s\n' 'true | * | D0 | former failure' >"$tmp/red"
printf '%s\n' 'true' >"$tmp/steps"
check 'passing listed entry is stale' run 1
check 'stale report' has "$tmp/report" 'Stale known-red entries (listed but passing now) (1):'
# Fake output is printed by printf, so no real language test runners execute.
printf '%s\n' "printf '%s\\n' '--- FAIL: TestA (0.00s)' ' detail with tab' '    --- FAIL: TestA/sub (0.00s)'; exit 1" >"$tmp/steps"
: >"$tmp/red"
check 'fake Go failure fails gate' run 1
check 'Go retains parent and subtest' test "$(cut -f5 "$tmp/out/summary.tsv")" = 'TestA,TestA/sub'
ident=$(cut -f1 "$tmp/out/summary.tsv")
printf '%s | * | D0 | wildcard\n' "$ident" >"$tmp/red"
check 'wildcard does not cover named failure' run 1
printf '%s | TestA | D0 | detail\n%s | TestA/sub | D0 | detail\n' "$ident" "$ident" >"$tmp/red"
check 'named Go failure matches' run 0
check 'failure detail is retained' has "$tmp/report" 'detail with tab'
printf '%s\n' "printf '%s\\n' '--- FAIL: TestSummary (pkg/a) ---' 'summary detail' '--- FAIL: TestSummary/sub (pkg/a) [iteration 2] ---' '--- build failed: pkg/b ---' 'FAIL pkg/c [build failed]' '--- package output: pkg/d ---' '== DB-RELATED SKIPS (2) ==' '  TestDB (pkg/e): no DB' '  [package] pkg/f: missing DB' ''; exit 1" >"$tmp/steps"
: >"$tmp/red"
check 'summariser fails gate when unlisted' run 1
check 'summariser names' test "$(cut -f5 "$tmp/out/summary.tsv")" = 'pkg/a:TestSummary,pkg/a:TestSummary/sub,build:pkg/b,build:pkg/c,package:pkg/d,dbskip:pkg/e:TestDB,dbskip:package:pkg/f'
printf '%s\n' "printf '%s\\n' 'not ok 1 - top title # TODO later' '  ---' '  error: one-line error' '  ...' '    not ok 2 - nested title'; exit 1" >"$tmp/steps"
check 'fake Node TAP fails gate' run 1
check 'nested TAP failure retained and TODO ignored' test "$(cut -f5 "$tmp/out/summary.tsv")" = 'top title > nested title'

printf '%s\n' true false >"$tmp/steps"
printf '%s\n' 'false | * | D0 | fails' >"$tmp/red"
check 'only ignores unrun known-red entries' run 0 --only true
check 'partial run is reported' has "$tmp/report" 'Partial run'
check 'only runs selected failing step' run 0 --only false
check 'repeatable only' run 0 --only true --only false
check 'unknown only is an error' run 2 --only absent
check 'skip ignores unrun known-red entries' run 0 --skip false
check 'skip leaves step out and runs the rest' test "$(cut -f1 "$tmp/out/results.tsv")" = true
check 'skipped known-red entry is not stale' has "$tmp/report" 'Stale known-red entries (listed but passing now) (0):'
check 'skip partial run is reported' has "$tmp/report" 'Partial run (--only/--skip): steps that did not run are left out of the known-red comparison.'
check 'unknown skip is an error' run 2 --skip absent
check 'skip conflicts with only' run 2 --skip false --only true
check 'only conflicts with skip' run 2 --only true --skip false
check 'repeatable skip rejects no steps' run 2 --skip true --skip false


cat >"$tmp/steps" <<'EOF'
test -z "$(gofmt -l internal/)"
go build ./...
go vet ./...
go test ./...
node --test fake.cjs
bash scripts/check-html.sh
sh scripts/check-shell.sh
python3 scripts/check.py
node scripts/check.cjs
make target
$(MAKE) other
printf    hello
bash scripts/check-html.sh
bash scripts/check-prices-age.sh || echo WARNING
EOF
check 'list exits without executing' run 0 --list
cat >"$tmp/expected" <<'EOF'
gofmt
go build
go vet
go test
node --test
scripts/check-html.sh
scripts/check-shell.sh
scripts/check.py
scripts/check.cjs
make target
make other
printf hello
scripts/check-html.sh#2
scripts/check-prices-age.sh
EOF
cut -f1 "$tmp/report" >"$tmp/actual"
check 'list ids and duplicate suffix' cmp -s "$tmp/expected" "$tmp/actual"
printf '%s\n' 'bad line' >"$tmp/red"
printf '%s\n' true >"$tmp/steps"
check 'malformed baseline is an error' run 2
printf '%s\n' 'true | * | D0 | old' 'true | * | D1 | duplicate' >"$tmp/red"
check 'duplicate baseline is an error' run 2
: >"$tmp/red"
printf '%s\n' 'false || echo WARNING' >"$tmp/steps"
check 'warning fallback passes' run 0
printf '%s\n' "printf '%s\\n' \"\$FRESH_DB\"" >"$tmp/steps"
bash_fresh_default() { (unset FRESH_DB; run 0); }
check 'fresh database default is exported' bash_fresh_default
logs=("$tmp/out"/*.log)
check 'fresh database has worktree hash' has "${logs[0]}" 'sh_freshcheck_'
cat >"$tmp/detail.sh" <<'EOF'
printf '%s\n' 'prefix --- FAIL: TestDetail (0.00s)' 'message | pipe'
exit 1
EOF
printf 'sh %s/detail.sh\n' "$tmp" >"$tmp/steps"
check 'prefixed Go failure' run 1
check 'failure detail sanitizes pipes' has "$tmp/report" '| TestDetail | ? | message / pipe'
printf '%s\n' 'printf \' '  continued' >"$tmp/steps"
check 'continued recipe lines join' run 0
: >"$tmp/steps"
check 'empty steps are an error' run 2
# Explicit ids preserve commands and use the same duplicate suffixes.
: >"$tmp/red"
printf '%s\n' 'custom := true' 'custom := printf done' >"$tmp/steps"
check 'explicit ids execute' run 0
check 'explicit duplicate id' test "$(cut -f1 "$tmp/out/summary.tsv")" = "$(printf 'custom\ncustom#2')"
check 'slice needs golden' run 2 --slice X
check 'browser conflicts with steps-file' run 2 --browser
browser_list() {
    bash "$runner" --no-cap --browser --list >"$tmp/report" 2>&1 || return 1
    cut -f1 "$tmp/report" >"$tmp/actual"
    printf 'browser:%s\n' hack-navigation hack-checklists hack-forms hack-presets named-viewers signin-card owner-dashboard >"$tmp/expected"
    cmp -s "$tmp/expected" "$tmp/actual"
}
check 'browser lists seven explicit ids' browser_list
mkdir -p "$tmp/golden" "$tmp/bin"
export CHECK_ALL_GOLDEN_DIR="$tmp/golden"
export GOLDEN_REF_BIN="$tmp/bin/reference host"
printf '#!/bin/sh\nexit 0\n' >"$GOLDEN_REF_BIN"
chmod +x "$GOLDEN_REF_BIN"
: >"$tmp/golden/run.mjs"
: >"$tmp/golden/diff.mjs"
printf '%s\n' true >"$tmp/steps"
check 'old golden harness rejected' run 2 --golden
check 'old harness diagnostic' has "$tmp/report" 'check-all: --golden needs the D0-S3a/D0-S3b golden harness (scripts/golden/run.mjs --host ...), which is not in this tree'
printf '%s\n' '// --host' >"$tmp/golden/run.mjs"
cat >"$tmp/bin/go" <<'EOF'
#!/usr/bin/env bash
printf 'go [%s]' "$CGO_ENABLED" >>"$STUB_LOG"
printf ' <%s>' "$@" >>"$STUB_LOG"
printf '\n' >>"$STUB_LOG"
EOF
cat >"$tmp/bin/node" <<'EOF'
#!/usr/bin/env bash
printf node >>"$STUB_LOG"
printf ' <%s>' "$@" >>"$STUB_LOG"
printf '\n' >>"$STUB_LOG"
if [[ $1 == */diff.mjs ]]; then exit "${DIFF_STATUS:-0}"; fi
EOF
chmod +x "$tmp/bin/go" "$tmp/bin/node"
export PATH="$tmp/bin:$PATH" STUB_LOG="$tmp/calls"
unset GOLDEN_REF_HACK_BIN GOLDEN_HACK_BIN
: >"$STUB_LOG"
check 'golden stub comparison passes' run 0 --golden --slice X
cat >"$tmp/expected" <<EOF
go [0] <build> <-o> <$tmp/out/golden/candidate.bin> <./cmd/server>
node <$tmp/golden/run.mjs> <--host> <$GOLDEN_REF_BIN> <--out> <$tmp/out/golden/ref> <--scenario> <all>
node <$tmp/golden/run.mjs> <--host> <$tmp/out/golden/candidate.bin> <--out> <$tmp/out/golden/cand> <--scenario> <all>
node <$tmp/golden/diff.mjs> <$tmp/out/golden/ref> <$tmp/out/golden/cand> <--slice> <X>
EOF
check 'golden order and exact arguments without hacks' cmp -s "$tmp/expected" "$STUB_LOG"
mkdir -p "$tmp/golden/approved"
printf '%s\n' '{"slice":"Y","entries":[{"product":"host","scenario":"new-one","reason":"r","change":"added"},{"product":"hack","scenario":"new-one","reason":"r","change":"added"},{"product":"host","scenario":"old-one","reason":"r","change":"removed"}]}' >"$tmp/golden/approved/Y.json"
: >"$STUB_LOG"
check 'golden with an added scenario passes' run 0 --golden --slice X,Y
check 'reference skips the added scenario' has "$STUB_LOG" "<--out> <$tmp/out/golden/ref> <--scenario> <all> <--skip-scenario> <new-one>"
check 'candidate records every scenario' has "$STUB_LOG" "<--out> <$tmp/out/golden/cand> <--scenario> <all>"
export GOLDEN_REF_HACK_BIN="$tmp/reference hack" GOLDEN_HACK_BIN="$tmp/candidate hack"
: >"$STUB_LOG"
check 'golden with hack overrides passes' run 0 --golden
check 'reference hack argument' has "$STUB_LOG" "<--host> <$GOLDEN_REF_BIN> <--hack> <$GOLDEN_REF_HACK_BIN> <--out>"
check 'candidate hack argument' has "$STUB_LOG" "<--host> <$tmp/out/golden/candidate.bin> <--hack> <$GOLDEN_HACK_BIN> <--out>"
export DIFF_STATUS=1
check 'golden diff failure fails gate' run 1 --golden
check 'golden diff is unexpected' has "$tmp/report" "Unexpected failures (not in known-red.txt) (1):
golden:diff | * |"
check 'only selects golden id' run 1 --golden --only golden:diff
check 'only ran diff' test "$(cut -f1 "$tmp/out/results.tsv")" = golden:diff
check 'golden listing' run 0 --golden --list
check 'listing includes golden diff' has "$tmp/report" golden:diff
# Additional failure identities must stay unexpected even beside a known one.
fake_log() {
    printf "case := cat '%s/fake'; exit %s\n" "$tmp" "${1:-1}" >"$tmp/steps"
}
printf '%s\n' '--- FAIL: TestA (github.com/vsriram/simple-host/pkg) ---' '--- FAIL: Raw (0.01s)' '--- FAIL: TestA/old (pkg) [iteration 2] ---' >"$tmp/fake"
fake_log
printf '%s\n' 'case | pkg:TestA | D0 | known' 'case | pkg:TestA/old | D0 | known' >"$tmp/red"
check 'summariser headers win over raw output' run 0
printf '%s\n' '--- FAIL: TestA/new (pkg) ---' >>"$tmp/fake"
check 'new summariser subtest remains unexpected' run 1
check 'new summariser identity' has "$tmp/report" 'case | pkg:TestA/new | ? |'
for indent in '' '    '; do
    printf '%s\n' "${indent}--- FAIL: TestA (0.01s)" "${indent}--- FAIL: TestA/old (0.01s)" "${indent}--- FAIL: TestA/new (0.01s)" "${indent}FAIL github.com/vsriram/simple-host/pkg 0.1s" >"$tmp/fake"
    check 'plain Go package attribution and new subtest' run 1
    check 'plain Go new identity' has "$tmp/report" 'case | pkg:TestA/new | ? |'
done
printf '%s\n' '# Subtest: suite' '    not ok 1 - old' '    not ok 2 - new' '    not ok 3 - deferred # TODO later' '    not ok 4 - skipped # SKIP later' 'not ok 1 - suite' >"$tmp/fake"
printf '%s\n' 'case | suite | D0 | known' 'case | suite > old | D0 | known' >"$tmp/red"
check 'new nested TAP remains unexpected' run 1
check 'nested TAP path' has "$tmp/report" 'case | suite > new | ? |'
printf '%s\n' 'check-all-failure: named|failure' >"$tmp/fake"
printf '%s\n' 'case | named%7Cfailure | D0 | known' >"$tmp/red"
check 'declared failure with pipe round trips' run 0
fake_log 137
check 'abnormal exit beside known name fails' run 1
check 'abnormal exit identity' has "$tmp/report" 'case | exit:137 | ? |'
fake_log
check 'non quiet matches quiet' run 0 --quiet
# Invoke without the helper's --quiet to cover tee and PIPESTATUS.
bash "$runner" --no-cap --steps-file "$tmp/steps" --known-red "$tmp/red" --out "$tmp/loud" >"$tmp/loud-report" 2>&1
check 'non quiet gate passes' test "$?" -eq 0
check 'quiet and non quiet identities match' cmp -s "$tmp/out/summary.tsv" "$tmp/loud/summary.tsv"
: >"$tmp/red"
printf '%s\n' 'x := true' 'x := false' 'x#2 := false' >"$tmp/steps"
check 'final ids execute distinctly' run 1
check 'final ids unique' test "$(cut -f1 "$tmp/out/results.tsv" | sort -u | wc -l)" -eq 3
printf "break := rm '%s/out/results.tsv'; mkdir '%s/out/results.tsv'\n" "$tmp" "$tmp" >"$tmp/steps"
check 'missing results row is infrastructure error' run 2
check 'missing row diagnostic' has "$tmp/report" 'results are incomplete (0 of 1 steps recorded)'
printf '%s\n' 'first := printf "error first\\nerror last\\n"; exit 1' >"$tmp/steps"
check 'unnamed failure fails' run 1
check 'first failure detail wins' has "$tmp/report" 'first | * | ? | error first'
printf '%s\n' 'golden:diff := true' >"$tmp/steps"
check 'golden ids share collision allocation' run 0 --golden --list
check 'golden diff gets distinct id' has "$tmp/report" 'golden:diff#2'
# The first command replaces the next log path with a directory.
printf "log-write := mkdir '%s/out/02-next.log'\nnext := true\n" "$tmp" >"$tmp/steps"
check 'quiet log write failure is infrastructure error' run 2
check 'log write failure diagnostic' has "$tmp/report" 'failed to write step logs or results.tsv'
# These stubs exercise the cap command without invoking sudo or systemd.
cat >"$tmp/bin/sudo" <<'EOF'
#!/bin/bash
printf 'sudo'; printf ' <%s>' "$@"; printf '\n'
[[ $1 == -n ]] && exit 0
shift
exec "$@"
EOF
cat >"$tmp/bin/systemd-run" <<'EOF'
#!/bin/bash
printf 'systemd-run'; printf ' <%s>' "$@"; printf '\n'
EOF
chmod +x "$tmp/bin/sudo" "$tmp/bin/systemd-run"
TEST_COUNT=7 TEST_PKGS=./fake TMPDIR="$tmp" CHECK_ALL_NO_CAP=0 PGPASSWORD='private$value' bash "$runner" --steps-file "$tmp/steps" --known-red "$tmp/red" --out "$tmp/capped" >"$tmp/cap-report" 2>&1
check 'stub capped command succeeds' test "$?" -eq 0
check 'cap disables argument expansion' has "$tmp/cap-report" '<--expand-environment=no>'
check 'cap sets memory limit' has "$tmp/cap-report" '<MemoryMax=2G>'
check 'cap forwards variable by name' has "$tmp/cap-report" '<-E> <PGPASSWORD>'
check 'cap forwards TEST_COUNT by name' has "$tmp/cap-report" '<-E> <TEST_COUNT>'
check 'cap forwards TEST_PKGS by name' has "$tmp/cap-report" '<-E> <TEST_PKGS>'
check 'cap forwards TMPDIR by name' has "$tmp/cap-report" '<-E> <TMPDIR>'
check 'cap inner run disables recursion' has "$tmp/cap-report" '<--no-cap>'
check 'cap does not expose secret value' test "$(cat "$tmp/cap-report")" = "$(sed 's/private\$value/EXPOSED/g' "$tmp/cap-report")"
# Hidden summariser exits must not disappear behind the wrapper's exit 1.
printf '%s\n' 'case | pkg:TestA | D0 | known' >"$tmp/red"
printf '%s\n' '--- FAIL: TestA (pkg) ---' 'go test itself exited 137' >"$tmp/fake"
fake_log
check 'hidden abnormal Go exit fails beside known test' run 1
check 'hidden abnormal Go exit identity' has "$tmp/report" 'case | go-test-exit:137 | ? |'
sed -i 's/exited 137/exited 1/' "$tmp/fake"
check 'ordinary Go failure adds no exit identity' run 0
fake_log 124
check 'timeout exit fails beside known test' run 1
check 'timeout exit identity' has "$tmp/report" 'case | exit:124 | ? |'
fake_log 125
check 'timeout launcher exit fails beside known test' run 1
check 'timeout launcher exit identity' has "$tmp/report" 'case | exit:125 | ? |'
fake_log
: >"$tmp/red"
printf '%s\n' 'not ok 1 - title \# TODO literal' 'not ok 2 - deferred # TODO later' 'not ok 3 - back\\slash' >"$tmp/fake"
check 'escaped TAP hash remains a failure' run 1
check 'TAP unescapes hash and backslash; real TODO ignored' test "$(cut -f5 "$tmp/out/summary.tsv")" = 'title # TODO literal,back\slash'
printf '%s\n' '# Subtest: suite' '    not ok 1 - child' 'not ok 2 - suite > child' 'not ok 3 - a | b' 'not ok 4 - a / b' >"$tmp/fake"
check 'distinct TAP names fail independently' run 1
check 'TAP component encoding avoids collisions' test "$(cut -f5 "$tmp/out/summary.tsv")" = 'suite > child,suite %3E child,a %7C b,a / b'
sed -n '/^case | /p' "$tmp/report" | sort -u >"$tmp/red"
check 'printed encoded identities paste into baseline' run 0
: >"$tmp/red"
printf '%s\n' '--- FAIL: TestA (0.01s)' 'FAIL another query' >"$tmp/fake"
check 'non-package FAIL text leaves Go name bare' run 1
check 'non-package attribution' test "$(cut -f5 "$tmp/out/summary.tsv")" = TestA
printf '%s\n' 'FAIL pkg/path' >>"$tmp/fake"
check 'package at end of line attributes Go test' run 1
check 'end-of-line package identity' test "$(cut -f5 "$tmp/out/summary.tsv")" = pkg/path:TestA
printf '%s\n' '--- FAIL: TestA' 'ok pkg/other' 'FAIL pkg/path' >"$tmp/fake"
check 'truncated test before ok package remains bare' run 1
check 'ok package stops attribution' test "$(cut -f5 "$tmp/out/summary.tsv")" = TestA
printf '%s\n' '--- FAIL: TestA' >"$tmp/fake"
check 'truncated Go failure at end of log counts' run 1
check 'truncated Go identity' test "$(cut -f5 "$tmp/out/summary.tsv")" = TestA
if ((failures)); then echo "FAIL: $failures cases"; exit 1; fi
echo 'All check-all tests passed.'
