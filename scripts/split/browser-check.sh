#!/usr/bin/env bash
# One browser check, with a disposable integration fixture when available.
set -u
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." || exit 2
# name|package|test|fixture env|runner (one row per check)
checks=(
'hack-navigation|./internal/hackhttp|TestServeHackNavigationBrowser|HACK_NAVIGATION_FIXTURE|hack-navigation'
'hack-checklists|./internal/hackhttp|TestServeHackNavigationBrowser|HACK_NAVIGATION_FIXTURE|hack-checklists'
'hack-forms|./internal/hackhttp|TestServeHackFormsBrowser|HACK_FORMS_FIXTURE|hack-forms'
'hack-presets|./cmd/server|TestServeHackPresetsBrowser|HACK_PRESETS_FIXTURE|hack-presets'
'named-viewers|./internal/handler|TestServeVisitorRecordsBrowser|VISITOR_RECORDS_FIXTURE|named-viewers'
'signin-card||||signin-card'
'owner-dashboard||||owner-dashboard'
)
if [[ $# == 1 && $1 == --list ]]; then
    for row in "${checks[@]}"; do printf '%s\n' "${row%%|*}"; done
    exit 0
fi
name=${1:-}
reason= tmp= fixture= fixture_pid= node_pid= node_group=
# Read again at shutdown: readiness may have completed while interrupted.
stop_fixture() {
    local url
    url=$(python3 - "$fixture" 2>/dev/null <<'PYURL'
import json, sys
print(json.load(open(sys.argv[1]))['url'])
PYURL
    ) || url=
    if [[ -n $url ]]; then
        curl --silent --max-time 5 --request POST "${url%/}/_fixture/stop" >/dev/null 2>&1 || true
    fi
}
# Wait for a process or group without blocking signal traps in a foreground wait.
wait_gone() {
    local target=$1 deadline=$((SECONDS + $2))
    while kill -0 -- "$target" 2>/dev/null && ((SECONDS < deadline)); do
        [[ -z $fixture_pid ]] || stop_fixture
        sleep 1
    done
    ! kill -0 -- "$target" 2>/dev/null
}
fixture_cleanup_ok() {
    local warning
    warning=$(sed -n '/^warning: failed to/{p;q;}' "$tmp/fixture.log")
    if [[ -n $warning ]]; then
        reason="fixture cleanup failed: $warning"
        return 1
    fi
}
cleanup() {
    local status=$?
    trap - EXIT INT TERM
    if [[ -n $node_group ]]; then
        kill -TERM -- "-$node_group" 2>/dev/null || true
        if ! wait_gone "-$node_group" 10; then
            kill -KILL -- "-$node_group" 2>/dev/null || true
        fi
        if wait_gone "-$node_group" 10; then node_group=; else status=1; fi
        wait "$node_pid" 2>/dev/null || true
    fi
    if [[ -n $fixture_pid ]]; then
        stop_fixture
        wait_gone "$fixture_pid" 60 || true
        if kill -0 -- "-$fixture_pid" 2>/dev/null; then
            kill -TERM -- "-$fixture_pid" 2>/dev/null || true
            if ! wait_gone "-$fixture_pid" 30; then
                kill -KILL -- "-$fixture_pid" 2>/dev/null || true
                printf 'browser-check: fixture process group %s had to be killed; check for a leftover sh-it-* container or sh_it_* database\n' "$fixture_pid"
                status=1
            fi
        fi
        wait "$fixture_pid" 2>/dev/null || status=1
        fixture_cleanup_ok || status=1
    fi
    if ((status == 0)); then
        [[ -z $tmp ]] || rm -rf -- "$tmp"
        printf 'browser-check %s: PASS\n' "$name"
    else
        [[ -z $tmp ]] || printf 'browser-check logs: %s\n' "$tmp"
        printf 'browser-check %s: FAIL (%s)\n' "$name" "${reason:-exit $status}"
    fi
    exit "$status"
}
trap cleanup EXIT
trap 'reason=interrupted; exit 130' INT
trap 'reason=terminated; exit 143' TERM
if [[ $# != 1 ]]; then reason='usage: browser-check.sh <name> | --list'; exit 2; fi
found=0
for row in "${checks[@]}"; do
    IFS='|' read -r check pkg test fixture_env runner <<<"$row"
    if [[ $check == "$name" ]]; then found=1; break; fi
done
if (( ! found )); then reason='unknown check (use --list)'; exit 2; fi
if [[ $runner == owner-dashboard ]]; then
    # No builder for the owner/174-sites and bee-01 fixture exists in the repo.
    if [[ -z ${BASE:-} || -z ${OWNER_KEY_FILE:-} || -z ${OTHER_KEY_FILE:-} ]]; then
        echo 'check-all-failure: no-fixture'
        reason='no fixture in the repo: set BASE, OWNER_KEY_FILE, OTHER_KEY_FILE'
        exit 3
    fi
fi
if [[ ! ${PLAYWRIGHT_DIR+x} && -d /tmp/tsx/node_modules ]]; then export PLAYWRIGHT_DIR=/tmp/tsx/node_modules; fi
if [[ ! ${CHROMIUM+x} && -x /usr/local/bin/chromium ]]; then export CHROMIUM=/usr/local/bin/chromium; fi
# owner-dashboard uses PLAYWRIGHT (an index.mjs path), rather than PLAYWRIGHT_DIR.
if [[ $runner == owner-dashboard && ! ${PLAYWRIGHT+x} && -n ${PLAYWRIGHT_DIR:-} ]]; then
    export PLAYWRIGHT="${PLAYWRIGHT_DIR%/}/playwright/index.mjs"
fi
tmp=$(mktemp -d "${TMPDIR:-/tmp}/browser-check.${name}.XXXXXXXX") || { reason='mktemp failed'; exit 1; }
fixture=$tmp/fixture.json
mkdir -p "$tmp/shots" || { reason='shots directory failed'; exit 1; }
export NAVIGATION_SHOTS="$tmp/shots"
if [[ -n $pkg ]]; then
    setsid env TEST_COUNT=1 "$fixture_env=$fixture" "TEST_PKGS=$pkg" bash scripts/test-integration.sh \
        -run "^${test}$" -timeout 35m >"$tmp/fixture.log" 2>&1 &
    fixture_pid=$!
    deadline=$((SECONDS + 300))
    ready=0
    while ((SECONDS < deadline)); do
        if ! kill -0 "$fixture_pid" 2>/dev/null; then reason='fixture exited before ready'; break; fi
        if [[ -s $fixture ]] && python3 - "$fixture" >/dev/null 2>&1 <<'PY'
import json, sys
json.load(open(sys.argv[1]))
PY
        then ready=1; break; fi
        sleep 1
    done
    if (( ! ready )); then
        reason=${reason:-fixture readiness timed out after 300 seconds}
        tail -n 60 "$tmp/fixture.log" >&2
        exit 1
    fi
fi
run() {
    setsid timeout -k 30 1200 node "scripts/e2e-${runner}.mjs" "$@" &
    node_pid=$!
    node_group=$node_pid
    wait "$node_pid"
    local status=$?
    if ! kill -0 -- "-$node_group" 2>/dev/null; then node_group=; fi
    if [[ -n $node_group ]]; then
        reason="$runner left processes running"
        exit 1
    fi
    node_pid=
    if ((status != 0)); then reason="$runner ${stage:-script} exited $status"; exit "$status"; fi
}
case $runner in
    named-viewers)
        stage=publish; run "$fixture" publish --grant --keep --shots "$tmp/shots"
        stage=check; run "$fixture" check --keep --shots "$tmp/shots"
        stage=dashboard; run "$fixture" dashboard --shots "$tmp/shots"
        ;;
    hack-presets) run "$fixture" --shots "$tmp/shots" ;;
    signin-card|owner-dashboard) run ;;
    # Checklists resets stage/results/conflicts itself; the fixture already has
    # two submitted entries. It needs no state left behind by navigation.
    *) run "$fixture" ;;
esac

if [[ -n $fixture_pid ]]; then
    stop_fixture
    if ! wait_gone "$fixture_pid" 120; then
        reason='fixture did not exit within 120 seconds after the script passed'
        exit 1
    fi
    wait "$fixture_pid"
    status=$?
    if ((status != 0)); then
        reason="fixture exited $status after the script passed"
        exit 1
    fi
    fixture_cleanup_ok || exit 1
    # Cleanup still checks for surviving processes in the fixture's group.
fi
