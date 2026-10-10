#!/usr/bin/env bash
# Replace only the standing demo owned by the admin. See docs/operations/simple-hack.md.
set +x
set -euo pipefail
trap 'printf "hack-demo-reset: failed at line %s\n" "$LINENO" >&2' ERR

fail() { printf 'hack-demo-reset: %s\n' "$*" >&2; exit 1; }
env_file=${HACK_DEMO_ENV:-/etc/simple-hack.env}
[[ -r "$env_file" ]] || fail "cannot read $env_file"
# The file uses systemd EnvironmentFile syntax (unquoted values may hold spaces and
# angle brackets), so it is read one key at a time, never sourced, and never echoed.
read_env() {
  local value
  value=$(grep -E "^$1=" "$env_file" | head -n 1 | cut -d= -f2-) || true
  value=${value%\"}; value=${value#\"}; value=${value%\'}; value=${value#\'}
  printf '%s' "$value"
}
ADMIN_API_KEY=${ADMIN_API_KEY:-$(read_env ADMIN_API_KEY)}
[[ -n ${ADMIN_API_KEY:-} ]] || fail 'ADMIN_API_KEY is missing'
base=${HACK_DEMO_BASE:-$(read_env HACK_DEMO_BASE)}
base=${base:-https://simple-hack.app}
base=${base%/}
root_slug=${HACK_DEMO_EVENT_SLUG:-$(read_env HACK_DEMO_EVENT_SLUG)}
root_slug=${root_slug:-demo}
[[ "$root_slug" =~ ^[a-z0-9][a-z0-9-]{1,24}[a-z0-9]$ ]] || fail 'HACK_DEMO_EVENT_SLUG must be a lowercase address name of 3 to 26 characters'
for command in curl jq date mktemp; do
  command -v "$command" >/dev/null || fail "missing command: $command"
done
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

request() {
  local method=$1 path=$2 body=${3:-}
  local args=(--silent --show-error --connect-timeout 10 --max-time 120
    --request "$method" --header @- --output "$work/response" --write-out '%{http_code}')
  [[ -z "$body" ]] || args+=(--header 'Content-Type: application/json' --data-binary "$body")
  # The key goes through stdin, never through curl's command arguments.
  if ! status=$(printf 'X-API-Key: %s\n' "$ADMIN_API_KEY" | curl "${args[@]}" "$base$path"); then
    fail "$method $path: request failed"
  fi
}
expect() {
  if [[ "$status" != "$1" ]]; then
    local response
    response=$(<"$work/response")
    response=${response//"$ADMIN_API_KEY"/[redacted]}
    fail "$2: HTTP $status: $response"
  fi
}
delete_demo() {
  local slug=$1
  request DELETE "/v1/hack/events/$slug" "$(jq -nc --arg slug "$slug" '{confirm:$slug}')"
  # Matching confirmation permits deletion at every stage, including archived.
  expect 204 "delete $slug"
  printf 'Retired demo: %s\n' "$slug"
}

request GET "/v1/hack/events/$root_slug"
if [[ "$status" == 200 ]]; then
  delete_demo "$root_slug"
else
  expect 404 "find $root_slug"
fi
# Previous busy demos retain their names forever. Find the admin's weekly demos.
request GET /v1/hack/events
expect 200 'list demos'
jq -r --arg root "$root_slug" '[.[] | select(.role == "organiser") |
  .slug | select(test("^" + $root + "-[0-9]{6}(-[0-9]+)?$"))] | .[]' \
  "$work/response" > "$work/old-slugs" 2>/dev/null || fail 'list demos: invalid JSON response'
while IFS= read -r old_slug; do delete_demo "$old_slug"; done < "$work/old-slugs"

check_name() {
  request GET "/v1/hack/names/$slug"
  expect 200 "check $slug"
  available=$(jq -er '.available | select(type == "boolean") | tostring' "$work/response" 2>/dev/null) \
    || fail "check $slug: missing name availability"
}
slug=$root_slug
check_name
if [[ "$available" != true ]]; then
  week=$(date -u +%G%V)
  slug="$root_slug-$week"
  suffix=1
  while :; do
    check_name
    [[ "$available" != true ]] || break
    suffix=$((suffix + 1))
    slug="$root_slug-$week-$suffix"
  done
  printf 'Slug %s is reserved; using %s. The demo endpoint must resolve the newest %s-* event.\n' "$root_slug" "$slug" "$root_slug"
fi
# Calendar dates use UTC, from this Monday to the next Monday.
today=$(date -u +%F)
weekday=$(date -u +%u)
starts=$(date -u -d "$today $((weekday - 1)) days ago" +%F)
ends=$(date -u -d "$starts +7 days" +%F)
description='A standing demo you can try as a participant or a judge. It resets every Monday.'
body=$(jq -nc --arg slug "$slug" --arg starts "$starts" --arg ends "$ends" --arg description "$description" \
  '{slug:$slug,title:"Simple Hack demo",organiser_name:"Simple Hack",organisation:"Simple Hack",
    contact_email:"support@simple-host.app",purpose:$description,expected_participants:100,
    starts_at:$starts,ends_at:$ends,time_zone:"UTC"}')
request POST /v1/hack/events "$body"
expect 201 "create $slug"
path="/v1/hack/events/$slug"
request PATCH "$path" "$(jq -nc --arg description "$description" \
  '{about:$description,team_size_max:4,coc_text:"",entry_required:["title","description"],gallery_open:true}')"
expect 200 'configure demo'
request PUT "$path/registration" '{"questions":[],"approval_required":false}'
expect 200 'open registration'
request PUT "$path/tracks" '{"tracks":[{"slug":"campus-life","name":"Campus life"},{"slug":"health","name":"Health"}]}'
expect 200 'configure tracks'
request PUT "$path/rubric" '{"criteria":[{"name":"Idea","description":"Score from 1 to 10.","weight":34,"max_points":10},{"name":"Usefulness","description":"Score from 1 to 10.","weight":33,"max_points":10},{"name":"Clarity","description":"Score from 1 to 10.","weight":33,"max_points":10}]}'
expect 200 'configure rubric'
request PATCH "$path/judging/settings" '{"assignment_mode":"open"}'
expect 200 'enable open judging'
# Building permits joining and submitting. Scoring needs no judging stage.
request POST "$path/stage" '{"stage":"building"}'
expect 200 'open demo'
join_url=$(jq -er '.organiser.join_url' "$work/response" 2>/dev/null) || fail 'open demo: missing join URL'
judge_url=$(jq -er '.organiser.judge_url' "$work/response" 2>/dev/null) || fail 'open demo: missing judge URL'
printf 'Demo slug: %s\nJoin URL: %s\nJudge URL: %s\n' "$slug" "$join_url" "$judge_url"
