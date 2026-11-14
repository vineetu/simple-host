# Golden recordings

There is no checked-in baseline for the golden harness. The reference is a build of the commit now
deployed, recorded with the same harness as the candidate. Nothing is re-recorded to make a
comparison pass.

## The gate, step by step

Use Node 22 with `node:zlib.crc32`, `psql`, `openssl`, and the programs you want to compare. The
recorder (`run.mjs` and `lib.mjs`) starts both products locally. Without `--hack`, it runs Hack
from the Host program with `EVENTS=hosted`. A base recording of both products takes about 35 to 60
seconds.

Use disposable Postgres at `127.0.0.1:55412`; the default connection prefix is
`postgres://postgres:postgres@127.0.0.1:55412`. Set `GOLDEN_PG_URL` to use another local instance.
It must have the form `postgres://user:password@localhost:port` or use `127.0.0.1` instead of
`localhost`. Include an explicit port, with no database path, query or fragment. Port `5432` is
refused because it is shared production Postgres.

1. Build the deployed commit in its own checkout for the reference. Build the branch for the
   candidate. In each checkout, use the build part of the standard line in
   `CLAUDE.md`, “Build, test, deploy”, from the repo root:

   ```sh
   sudo systemd-run --quiet --wait --pipe --collect -p MemoryMax=2G -p WorkingDirectory=$PWD --uid=$(id -u) --gid=$(id -g) -E HOME=$HOME -E PATH=$PATH -E GOCACHE=$(go env GOCACHE) -E GOMODCACHE=$(go env GOMODCACHE) bash -c 'CGO_ENABLED=0 go build -ldflags "-X github.com/vsriram/simple-host/internal/buildinfo.Version=$(git describe --tags --always --dirty) -X github.com/vsriram/simple-host/internal/buildinfo.Commit=$(git rev-parse --short HEAD) -X github.com/vsriram/simple-host/internal/buildinfo.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o /tmp/simple-host.new ./cmd/server'
   ```

   Save each result before building the next program:
   `cp /tmp/simple-host.new /tmp/reference.bin` in the deployed checkout, then
   `cp /tmp/simple-host.new /tmp/candidate-host.bin` in the branch checkout.
   Any build works: the harness normalizes build version and commit at the specific response
   fields listed below. Do not build the reference from the candidate checkout.

2. Record the reference with every scenario its program can run. Skip the scenarios added by
   the branch's slices. For example, when the branch adds `rules-on`:

   ```sh
   node scripts/golden/run.mjs --host /tmp/reference.bin --out /tmp/golden-reference --scenario all --skip-scenario rules-on
   ```

   Replace `rules-on` with the actual added names, separated by commas. Omit `--skip-scenario`
   when there are none. Run `base`, `mixed-store` and `hosted-pages` against the reference too; unknown scenario names fail.

   For individual requests approved as additions to an existing scenario, pass
   `--skip-request PRODUCT/SCENARIO/NAME[,...]` only to the reference recorder. For R2-S4b,
   these are `mixed-site-export`, `mixed-people` and `mixed-visitor-emails` in `mixed-store`,
   for each product. Every other request still runs. `check-all.sh --golden --slice R2-S4b`
   derives these exact selectors from the six addition approvals automatically. When that
   slice is already in the reference, leave it out of `--slice` so both sides run the requests.

3. Record the candidate with all scenarios:

   ```sh
   node scripts/golden/run.mjs --host /tmp/candidate-host.bin --out /tmp/golden-candidate --scenario all
   ```

   When Host and Hack are separate programs, supply `--hack` too:

   ```sh
   node scripts/golden/run.mjs --host /tmp/candidate-host.bin --hack /tmp/candidate-hack.bin --out /tmp/golden-candidate --scenario all
   ```

   Use `--hack` for the reference too if its products are separate. Output folders must be
   absent or empty. The recorder rejects positional arguments and repeated flags.

4. Check the candidate's inventory coverage:

   ```sh
   node scripts/golden/coverage.mjs /tmp/golden-candidate
   ```

5. Compare the recordings. Select one slice ID or a comma-separated list of stacked slice IDs:

   ```sh
   node scripts/golden/diff.mjs /tmp/golden-reference /tmp/golden-candidate --slice D2-S3
   node scripts/golden/diff.mjs /tmp/golden-reference /tmp/golden-candidate --slice D2-S5a,D2-S5b
   ```

6. For intended differences, request a draft, review it against both recordings and the slice,
   replace every TODO reason, and write `approved/<slice-id>.json`:

   ```sh
   node scripts/golden/diff.mjs /tmp/golden-reference /tmp/golden-candidate --slice D2-S3 --propose
   ```

   Run the comparison again without `--propose`. Never approve unexplained noise.
   See [Comparing two recordings](#comparing-two-recordings) and the
   [approval entry format](approved/README.md).

The recorder exits `0` on completion, `1` on recording failure or interruption, and `2` on usage
errors. Coverage exits `0` on success, `1` on a failed check, and `2` on usage errors. The
comparator exits `0` for identical or exactly approved recordings, `1` for comparison failure, and
`2` for usage, recording integrity or invalid approval files.

## Checking determinism

Record the same program twice into fresh folders, then compare every file:

```sh
node scripts/golden/run.mjs --host /tmp/candidate-host.bin --out /tmp/golden-first --scenario all
node scripts/golden/run.mjs --host /tmp/candidate-host.bin --out /tmp/golden-second --scenario all
diff -r /tmp/golden-first /tmp/golden-second
```

Add the same `--hack` argument to both commands when needed. `diff -r` must print nothing. If it
prints differences, fix the harness first. Normalize only unavoidable variation, as narrowly as
possible, and document the rule here with its reason. Never approve noise.

Use `GOLDEN_TEST_DELAY_MS=25000` before a recorder command to wait after initial readiness and
before the first request, including startup observation, on each product. This deliberately lets the
20-second metrics timer fire and helps expose timing-dependent recordings.

The supplied programs have no clock-shift setting. The harness does not simulate a next-day run by
changing the machine clock. Unit tests use two different today values to verify that relative date
windows match. The recorder rejects runs that cross UTC midnight, so seed data cannot straddle daily
buckets.

## What a recording contains

Each product and scenario gets its own disposable database, temporary data directory (including
`GEOIP_DIR`), port, placeholder names and exact replacement map. Each program runs its own
`migrate -init`; the checkout schema is never used.

A format-2 recording has this layout:

```text
manifest.json
host.json
hack.json
host.NAME.json                 # when a named Host scenario is selected
hack.NAME.json                 # when a named Hack scenario is selected
text/host/<sanitized-name>.txt
text/hack/<sanitized-name>.txt
text/host.NAME/<sanitized-name>.txt
text/hack.NAME/<sanitized-name>.txt
```

The manifest lists each run's product, scenario, JSON file, text folder and request count. It
contains no clock, program path or build identity. Run files contain `format`, `product`,
`scenario` and `requests`. Requests stay in replay order. Each record has `name`, `method`,
`path`, `status`, selected `headers` and `body_kind` (`empty`, `json`, `text` or `binary`). JSON
records retain the complete normalized `body`; binary records retain its hash and length. JSON and
text records have `text` metadata with `file`, `sha256` and byte `length`.

Every JSON/text body has a full text file; nothing is truncated. `renderJSON` exposes multiline
strings and embedded JSON as readable lines. The retained JSON body keeps the string value after
normalization. The rendering is for reading diffs, not parsing as JSON.

ZIP downloads become JSON bodies with sorted entry names, attributes, hashes, lengths and full UTF-8
text. Before normalization, the harness checks CRC-32, declared sizes, local-header consistency and
bit-3 data descriptors, with or without their optional signature. Binary ZIP entries and other
binary downloads retain byte hashes and lengths. ZIP compression and entry timestamps are omitted.
Text requires valid UTF-8 and a supported MIME type: `text/*`, JSON or `+json`, XML or `+xml`,
JavaScript, YAML, form data, or SVG. With no MIME type, valid UTF-8 also counts as text. Invalid
UTF-8 always counts as binary.

Fixed anonymous GETs use `GET /path`. State-dependent reads have descriptive names. MCP calls use
shared dispatch naming: `tools/call <tool> <qualifier>`. A `run_tool` name includes its target
immediately after `run_tool`. Settings and mail use `admin-settings` and `mail-outbox`, with the
same name and collision checks as HTTP requests.

Text filenames replace characters outside `[A-Za-z0-9._-]` with `_`. Duplicate request names or
filename collisions fail. JSON object keys sort recursively, including mail, while preserving
`__proto__`. Placeholders belong to the request where a value first appears and preserve later
references to it. Event join/judge codes belong to `event-create`. Mail is normalized last, after
HTTP identifiers are known.

## Comparing two recordings

Compare the two format-2 folders with Node 22. See the gate above for recording commands:

```sh
node scripts/golden/diff.mjs /tmp/golden-reference /tmp/golden-candidate
node scripts/golden/diff.mjs /tmp/golden-reference /tmp/golden-candidate --slice R3-S1a
node scripts/golden/diff.mjs /tmp/golden-reference /tmp/golden-candidate --slice R3-S1a,R3-S1b
node scripts/golden/diff.mjs /tmp/golden-reference /tmp/golden-candidate --slice R3-S1a --propose
```

Without `--slice`, every difference fails. Only slice files selected with `--slice` contribute
approvals. See [approved/README.md](approved/README.md) for the entry format. The reference always
comes from the deployed commit; earlier deployed slices' files stay as history. Never re-record to
hide a change. For fixture tests, `--approved-dir DIR` selects a different approval directory. The
default `--require host,hack` requires both products’ nonempty base runs in both folders; use
`--require host` for Host-only fixtures.

A failure identifies product, scenario, request, path and both values, for example:

```text
host/base request="GET /v1/skills" path=body.count: unapproved difference
  old: 4
  new: 3
FAIL: 1 problem(s)
```

Text changes print `-` removed lines and `+` added lines. Mismatched approvals, unused approvals,
overlaps and reordered requests fail too. Success prints `ok: <n> requests in <m> runs, identical`,
or lists approved differences with reasons and an approved-difference count. Whole scenario changes
also print the files and request count for review; attach those files to the pull request and read
them line by line.

Record the candidate with `--scenario all`. Record the reference with
`--scenario all --skip-scenario <each scenario added by the slices named in --slice>`, using one
comma-separated `--skip-scenario` list for multiple scenarios. For example:

```sh
node scripts/golden/run.mjs --host /tmp/reference.bin --out /tmp/golden-reference --scenario all --skip-scenario rules-on
node scripts/golden/run.mjs --host /tmp/candidate-host.bin --out /tmp/golden-candidate --scenario all
```

A whole-scenario `added` entry requires the scenario to be absent from the reference folder. A
`removed` entry requires it to be absent from the candidate folder. If both folders contain the
scenario, compare request by request. The reference may omit an added scenario its program cannot
run. For a removal, the candidate must stop recording it (drop it from the scenario table).

The comparator prints one line per approved difference and a count per slice file. When several
slice IDs are selected, each line names the approval file and entry that supplied it. Long
structural JSON diagnostics include both absolute run JSON paths for full values and both
`text.file` paths as a readable view; embedded JSON string normalization can erase differences in
that view. Long text diagnostics include both absolute `text.file` paths. Headers and binary bodies
point to run JSON instead. Approval mismatches also identify the approval file and entry number so
truncated expected values can be inspected. Text diagnostics show the first 40 changed lines, count
the remaining lines, and give the same paths when truncated.

The comparator exits `0` for identical or exactly approved recordings, `1` for comparison failure,
and `2` for usage, recording integrity or invalid approval files.

It checks both folders' integrity before comparison. It verifies each text file's SHA-256 and byte
length, and rejects invalid UTF-8. JSON text must equal `renderJSON(body)` exactly. Text metadata
must have exactly `file`, `sha256`, and `length`; recording artifacts must be regular files and
directories must be real directories (symlinks, FIFOs, sockets and devices are rejected before
reading), with real-path containment. Every recorded run, base or named, must contain at least one
request.

Text hashes, lengths and filenames are derived: JSON structural changes or an exact text approval
account for visible body changes, without separate metadata approvals.

For JSON on both sides, a text approval owns only leaves absent on one side, or leaves where neither
side is `rendersLossy` and their `renderJSON` outputs differ. Changed embedded JSON strings,
including scalar documents, require structural pins even when visibly changed; negative zero and
non-finite numbers are also lossy. A text entry may sit beside those pins and must still list the
whole rendered line difference, including the lossy leaves’ lines.

Missing text counts as zero lines. Kind transitions require separate kind and structural body pins,
and proposals include them.

`--propose` sends a draft approval document to stdout and diagnostics to stderr; it never writes
files and still exits 1 for unapproved changes. Review each entry and replace all TODO reasons
before adding it to the slice's JSON file.

The comparator cannot know which named scenarios should exist: a scenario missing from both folders
is not detected. Record the candidate with `--scenario all` and the reference with every scenario
its program can run, and have the reviewer check the `N requests in M runs` line against the
scenario table.

An exact-value approval is a pin. At its own path, a present literal `"__absent__"` cannot be
approved. A hand-written enclosing body object or array pin can include that literal as an ordinary
nested string; at the body root or a whole header value there is no supported enclosing path.
`--propose` reports
`cannot propose: the value is the reserved string __absent__; pin an enclosing object by hand` on
stderr and emits no entry for that difference. It never promotes to a parent; other differences are
still proposed and the comparison stays failed (exit 1).

## Adding a request

Add a request after the state it needs exists. Use `at(name, method, path, { apiKey, body })`;
uploads use `raw` and `contentType`. Give state-dependent reads descriptive names. Use
`qualifier: true` to keep a deliberate name for an anonymous fixed GET. Keep names unique after
filename sanitization too.

Use `mcp(tool, args, apiKey, qualifier)` for a direct tool call. The shared naming logic also
handles MCP calls sent through `at`, including the target of `run_tool`. Register short random
codes under the name of the response that first returns them, before using them in later paths. Use
the context's `registerSecret(category, raw)` helper.

## Adding a scenario

The `SCENARIOS` table in `run.mjs` contains `base` (the default), `mixed-store` and `hosted-pages`.
`hosted-pages` checks Host's landing page, old hackathons addresses and sitemap with
`SITE_DOMAIN=simple-host.app`, using a loopback server and a throwaway database.
It pins the hosted navigation and redirects that the self-hosted base fixture cannot cover.
`--scenario all`
selects the table; comma-separated names select exactly those scenarios. `--skip-scenario` subtracts
a comma-separated list. Unknown or repeated names fail, and `base` cannot be skipped. Named runs use
`host.NAME.json` and `text/host.NAME/`, likewise Hack.

Each callback receives
`{ instance, at, mcp, mail, namer, exact, calls, usedNames, registerSecret }`. A scenario may have
an empty outbox; there is no minimum mail count.

For example, a future `rules-on` table entry can reuse the base state and add its own requests:

```js
'rules-on': {
  env: { host: { STORAGE_RULES: 'on' }, hack: { STORAGE_RULES: 'on' } },
  host: async context => {
    await seedAndCallHost(context); // reuse base requests and prerequisite state
    await context.at('rules-on-health', 'GET', '/healthz', { qualifier: true });
    await context.mcp('list_sites', {}, context.instance.adminKey, 'rules-on admin');
  },
  hack: async context => {
    await seedAndCallHack(context);
    await context.at('rules-on-health', 'GET', '/healthz', { qualifier: true });
    await context.mcp('hack_list_events', {}, context.instance.adminKey, 'rules-on admin');
  },
}
```

For the branch gate, record the candidate with `--scenario all`. Once `rules-on` exists, record the
reference with `--scenario all --skip-scenario rules-on`. For several new scenarios, use one
comma-separated skip list. Review the whole added scenario against its absence from the reference.
After deployment, both programs record it and compare its requests normally.

## Coverage report

`coverage.mjs` checks inventory routes without path parameters and MCP tools. This is **not endpoint
coverage**. Parameterized route counts are informational. Methods follow ServeMux: `ANY` accepts all
methods, `GET` accepts `HEAD`, a trailing slash matches a subtree, and `{$}` is exact. Any direct
`tools/call` touches its tool, including a target error. A `run_tool` target counts when dispatched,
but not when the wrapper refuses it. A `tools/list` response alone does not cover tools.

`coverage-covered.txt` is the generated, sorted list of touched items that are not automatic. Fixed
anonymous parameter-free GETs are automatic. Other touched fixed routes and tools belong in the
covered list. Each uncovered item needs a reason in `coverage-exceptions.txt`. Missing, stale,
duplicate or unreasoned exceptions fail.

The unit test in `tests/coverage.test.mjs` partitions the real generated inventory into exactly one
of automatic, covered or exceptions. `make check` runs it through its existing test glob, so new
unaccounted inventory fails without a recording. The folder check verifies text hashes and byte
lengths and requires its touched list to equal the checked-in covered list.

```sh
node scripts/golden/coverage.mjs /tmp/golden-candidate --print
node scripts/golden/coverage.mjs /tmp/golden-candidate --write-covered
node scripts/golden/coverage.mjs /tmp/golden-candidate
node --test scripts/golden/tests/*.test.mjs
```

`--print` lists uncovered items for review. Regenerate covered only after reviewing newly touched
items and removing stale exceptions.

## What is recorded

Both products record OpenAPI JSON/YAML, llms.txt, features.json, sitemap, hackathons, public pages,
the skills catalog, every catalogued skill file and its ZIP, authenticated admin usage including
background task status, and settings command output. Host records deployment, version, visibility,
passcode, account export and KV/SQLite/files storage replay. Both products exercise MCP personal and
deploy-only connections with screens off/on, initialization instructions, tool lists and resources.
Hack also exercises the real team-site key in that matrix. Host has no Hack team-site connection
mode.

Each registry family has a direct `tools/call` (the table names requests):

| Product | Registry family | Request |
| --- | --- | --- |
| Host | sites | `tools/call list_sites` |
| Host | storage | `tools/call storage_get_kv` (seeded theme = dark) |
| Host | access | `tools/call list_site_viewers` (read only) |
| Host | imports | `tools/call get_import_report` |
| Host | accounts | `tools/call who_am_i` |
| Host | discovery | `tools/call find_tools`; `tools/call run_tool list_versions` |
| Host | notifications | `tools/call list_notifications` |
| Hack | hack-events | `tools/call hack_get_event` |
| Hack | hack-entries | `tools/call hack_get_rubric` |
| Hack | hack-app-only | `tools/call hack_show_organiser` |
| Hack | storage | `tools/call hack_event_storage_list_resources` |
| Hack | sites | `tools/call get_site team-site` (live team key) |
| Hack | notifications | `tools/call list_notifications` |
| screen | access | `tools/call passcode_screen` |

Calls with unavailable prerequisites keep their actual errors: missing import, event storage without
a custom website, notifications without an OAuth grant, and the app-only passcode screen without an
ephemeral public key. The screen call reaches the tool and records its invalid-public-key refusal;
it does not fabricate a browser key exchange. Hack has no discovery family: direct `find_tools` and
`run_tool` calls record unknown-tool refusals. Coverage does not count a refused wrapper's target as
called.

Both products deploy `_redirects` and fetch its redirect and a plain address without `.html`
(SIM-440). Host lists a site on its profile and reads the public feed (SIM-441); Hack records the
refusal to change team-site visibility. Hack creates and reads submitted entries/cards (SIM-442),
messages in both directions (SIM-443), and an announcement with multiline text (SIM-444). Host
records its unavailable Hack route responses. Deploy-only and other-site team-key refusals are
recorded, as is a formerly valid team key after the organiser removes its person (this binary
revokes the key and answers `invalid_api_key`).

SIM-448 records a Host site created private in its first request (201), an anonymous page refusal
(403; the local HTTP SSO start refuses), real emailed-code form start/verify requests, the named
viewer's page (200), and a signed-in outsider's page (403). No visitor session or provider is
fabricated. Hack keeps its `no_personal_sites` creation refusal (403). Host's passcode sequence
records the anonymous gate (401), wrong passcode (401), correct passcode (303), and unlocked
contents (200).

After the SIM-448 named-viewer code verification, Host also records both site-session identity GET
aliases, visitor logout, and a session read using the old cookie after logout. Both viewer and
outsider page results are recorded.

### Mail outbox and completion

The local HTTPS provider sink accepts the real payload via a local CA and CONNECT proxy. Mail
retains every provider field, including `from`, recipients, `cc`/`bcc`, `reply_to`, headers,
tags, full text and full HTML when sent. Attachments retain metadata plus decoded-byte SHA-256 and
length. HTML also has a derived hash.

For sorting, the harness projects each message through a copy of the known identifier map. It
allocates unseen identifiers in semantic key order, then sorts normalized messages canonically. Raw
random IDs never break ties. It filters no subjects.

Hack actors sign in through the real emailed-code API. Admin-created accounts are marked
`event_account` by `app/accounts/account_lifecycle.go` and are ineligible for notice mail
(`repository/hackevents/notice_delivery.go`), so they cannot exercise these deliveries. Host retains
admin account seeding and real named-viewer/outsider emailed-code sessions.

`hackhttp/hack_content.go` runs content/notice delivery at startup and every minute.
`app/hack/events/notices.go:NoticeMailTiming` specifies a one-minute undo delay and fifteen-minute
grouping window. There is no environment override.

After mutations, the harness makes pending queues eligible in the disposable database. It moves
pending notice `created_at` back two minutes and prior sent timestamps back sixteen minutes. It
restarts the server to trigger the existing startup drain, then waits until pending notices,
receipts and announcements settle before the next mutation. This controls eligibility without
changing the mail payload or fabricating delivery. Stale notices may still be skipped by the
application. Receipts, sign-in codes and sign-in alerts have explicit waits. The replay also removes
a Team C member while building to exercise the team-removal notice.

The captured classes are sign-in codes, sign-in alerts, entry receipts, team takedown, site
restoration, messages in both directions, judging-open and team-removal notices (22 Hack messages
and two Host messages). The harness includes everything received in the outbox. These counts
describe base; scenarios have no implicit two-message requirement.

At each restart and finalization, the server gets `SIGTERM`. It must exit itself with code `0`
within 30 seconds while the sink and proxy stay available. An already exited server, abnormal exit
or required `SIGKILL` fails the recording. Cleanup may force termination, but that run still fails.
The harness captures the final outbox only after confirmed graceful completion and a bounded quiet
period.

## Known limits

SIM-449 records the Host sequence: delete, admin take-down, owner trash restore, then page read. The
final page returns 410.

Hack Team A serves (200), is deleted (204), then taken down while deleted (200). Its actual Recently
deleted restore is attempted through REST (403 `team_key_scope` with the team key, 404 with the
member key) and MCP `restore_site` (HTTP 200 tool error wrapping the same 403). The status says
`exists: false`, `taken_down: true`, and the page stays 404. `teamRoutes` in
`internal/auth/scope.go` does not allow POST site restore; the member's account is not the separate
team's holding account. There is no public admin trash restore route.

The harness cannot obtain a Hack credential that can exercise a successful restore of the deleted
site to check its old take-down state. Team keys and team-site connections get `team_key_scope`. A
member gets 404 because the site belongs to the event's holding account. Team-site connections mint
scoped internal credentials too (`connector.go:internalKeyFor`, `auth/scope.go:teamGate`). The
recording captures the take-down while deleted and the restore refusals; it cannot capture a
restored page.

The only routes reaching `app/sites.RestoreTrashed` are the owner's
`POST /v1/sites/{sitename}/restore` (recorded on Host) and the mailed idle-cleanup restore link
(`GET` confirmation and `POST /v1/idle/restore`, not recorded). See `handler/deleted.go`,
`handler/idle.go` and `app/sites/deleted.go`; MCP `restore_site` replays the same owner REST route.

Team B is deployed while building, then taken down live. Deleting that suspended site through the
admin API is itself refused (403 `site_suspended`): `sites.Service.Delete` refuses a suspended site
before `Trash`. The lift and REST/MCP trash-restore attempts are recorded, then status and page
(200). It never entered Recently deleted, so this records the blocking behavior. It does not
exercise a successful restore after trashing. Names explicitly identify each attempt.

The Hack catalog advertises `agents/openai.yaml` for join-hackathon, judge-hackathon, run-hackathon,
website-deploy and website-deploy-builder, but those five individual HTTP routes return 404 today.
The harness records those 404s; the supported ZIPs additionally expose every entry's full text in
the recording.

Authenticated paper-metrics is omitted because it contains runtime latency and byte observations.
Large authenticated tar backups are omitted; Host's account ZIP is recorded. Anonymous refusal
responses remain, including Hack's account ZIP refusal.

The mail proxy is not a network sandbox. Direct sockets, DNS and transports that ignore proxy
variables can bypass it. Dynamic response patterns cannot distinguish a fixed ID, checksum or
timestamp from a generated value of the same form.

The comparator cannot know which scenarios should exist. A scenario missing from both folders is
invisible to it. Record all candidate scenarios and every reference scenario its program can run,
then check the reported request/run counts against `SCENARIOS`.

## What is normalized and why

`RESPONSE_CLASSES` and `responseClass` in `lib.mjs` classify responses by request name,
independently of HTTP method or authentication. The first matching rule wins.

| Class | Request names |
| --- | --- |
| Static | Automatic fixed `GET /path` inventory names except the dynamic overrides below; skill files and skill ZIP entries; `tools/list ...`, `initialize ...`, `resources/list ...`; `admin-settings`; `tools/call find_tools ...`, `tools/call get_page_recipe ...`, `tools/call hack_get_page_recipe ...` |
| Dynamic | `admin-usage`, `GET /v1/admin/usage`, `GET /v1/admin/usage as admin`; `event-public-page`; `GET /v1/u/owner/showcase.json`; all remaining descriptive seeded-state reads/mutations |

The public event page contains stage/publication instants; the showcase feed contains site
creation/update instants. These seeded-state responses need dynamic rules. Repeated recordings
compare the fixed anonymous GETs and documentation classes too. No extra runtime exception is needed
for MCP or settings documents. Dates such as `2026-10-12` in settings and `2026-10-01`/
`2026-11-02` in Hack tool schemas stay literal.

Each rule below removes a specific source of run variation while keeping observable behavior:

- **Local addresses and paths:** static responses replace only the instance base URL, bare host:port
  and temporary directory, plus nonces as described below. Dynamic responses use the full exact
  replacement map too. Ports and folders change on every run.

- **HTML/CSP nonces:** both classes replace generated nonce values with `<nonce>`. These
  response-specific security values cannot repeat meaningfully.

- **Sitemaps:** only names matching `GET /sitemap*.xml` replace `lastmod` with `<ts>` and put
  adjacent XML tags on separate lines. Modification times vary; separate lines make diffs readable.
  Other XML/SVG bodies keep whitespace and CDATA. Other static UUIDs, checksums, dates and
  timestamps stay literal.

- **Generated identifiers:** dynamic responses and account-export entries replace UUIDs, `shk_`
  keys, bare 32–64 hex tokens, Hack `u-` hex handles and signed preview tokens with request-scoped
  placeholders. Registered join/judge/invite/team codes use the exact map. This preserves equality
  of references while removing random values.

- **ISO instants:** dynamic responses and account-export entries replace generated ISO timestamps
  with `<ts>`. Seeded 2020 event start/end dates and their midnight ISO forms stay visible so
  date-handling changes are detectable.

- **Calendar dates:** the complete relative-date request list is `GET /v1/admin/growth as admin`,
  for both products. Only this request substitutes dates in the run's UTC window, from 400 days
  before today through tomorrow, using `<today>`, `<today-1>`, `<today+1>`, etc. This keeps
  bucket relationships across days. `internal/app/operations/apigrowth.go` builds the current and
  comparison windows using the database day from `internal/repository/operations/api.go:APIToday`.
  Other recorded usage/analytics answers contain counts and ISO instants, not clock-derived calendar
  date strings. All other responses retain calendar dates. A UTC midnight crossing fails the run.

- **Credential fields:** in dynamic JSON and account-export JSON entries, nonempty strings under
  `passcode` or `last4` become `<volatile>` because their generated values vary. Objects, arrays,
  numbers, booleans, empty strings and null keep their types and values. These names are not
  exclusively credentials, so schema objects must remain visible.

- **Usage measurements:** only the three named admin usage responses in the class table replace
  numeric network/disk measurement leaves with `<n>`. Configured `allowance_gb` and `alert_pct`
  stay visible, as do structure, key sets, nulls and nonnumeric leaves. Numeric byte quantities in
  the root message become `<measurement>`. These measurements depend on the machine and runtime.

- **Disk state:** in those admin usage responses the root `status` (`ok`, `filling` or `full`)
  becomes `<disk-status>`, and the root `message` becomes `<disk-message>` when it is one of the
  three sentences `internal/capacity` writes for those states. The server picks the state and the
  sentence from how full the machine's disk is at that moment, so a reference and a candidate
  recorded minutes apart on a busy box could differ with no change in the program. Any other
  status value or wording is left as it is and compared; nothing else in the response is touched.

- **Background status:** in those admin usage responses, timestamp strings become `<ts>` and numeric
  pending counts become `<n>`. The explicit timer group below uses `<timer>` for enabled tasks'
  `last_attempt` and `last_progress`, even when null. Its two queue counters use `<timer>` too.
  This removes tick timing races. Other null/type distinctions survive, including error timestamp
  nullness. Names, order, enabled/running, errors and configured intervals remain visible.

- **Observation traffic:** only `GET /v1/admin/api-analytics as admin.self_calls_today` and
  `GET /v1/admin/growth as admin.self_calls` subtract the exact count of successful startup
  observation requests. Polling is harness work; all replay calls and error totals remain.

- **Build identity:** only `GET /.well-known/agent-card.json`,
  `GET /.well-known/mcp/server-card.json` and `GET /mcp/server-card` normalize root `version`. The
  three named admin usage responses normalize root `version` and `commit`. Only nonempty strings
  change, to `<version>` or `<commit>`. Empty strings, nulls, numbers and structures survive. There
  are no program-version exact substitutions. Plugin, skill, OpenAPI and MCP protocol versions
  remain visible; `initialize` response’s `serverInfo.version` is a bundled skill version. Build
  identity changes
  between builds and is not product content.

- **Judging order:** `judging-dashboard` sorts teams/judges by seeded names.
  `judging-settings-select-organisers`, `judging-settings-preserve-organisers` and
  `judging-settings-selected-get` sort judges and selected organisers by seeded names. Their SQL
  order uses random IDs; all rows and fields survive.

- **Human event times:** only `event-public-page` masks the human times labelled `Entries due` and
  `Results published`. Stage closure and result publication set these to now; markup and labels
  remain visible.

- **Account-export dates:** only the account-export ZIP root and its `Content-Disposition` filename
  use `<date>` for the generated date. This is separate from relative calendar substitution. Static
  skill/plugin ZIP filenames stay literal.

- **ETags:** selected response headers keep ETag presence as `<etag>`, since body-derived values
  can reflect normalized content.

- **Cookies:** nonempty values become `<value>`; empty values stay empty. Parseable HTTP-date
  `Expires` values become `<http-date>`. Passcode cookie names mask their site-ID-derived suffix.
  Other names and attributes stay visible, subject to timestamp/identifier rules. Lists sort so
  header arrival order does not matter. Selected headers include content type, cache control,
  location, cookies, robots, authentication, disposition and CSP.

- **Sign-in codes in mail:** only sign-in subject/text/HTML replace the six-digit code as a
  standalone token or inside a link. Envelope, headers and attachment names are untouched by this
  code rule. Codes never enter a global digit map, so unrelated numbers remain visible.

- **Sign-in alert times:** the `New sign-in to your Simple Host account` subject scopes replacement
  of its human `When:` line with `<ts>`, before dynamic normalization and HTML hashing. The line
  contains a clock value outside the ISO timestamp format.

- **JSON and ZIP order:** object keys sort safely, preserving `__proto__`. ZIP raw entries sort
  before placeholder allocation, so archive order cannot change identifier assignment. Derived
  hashes are never normalized again. ZIP descriptions keep permissions/attributes and full text,
  while omitting compression and entry timestamps because packaging varies.

- **Mail order:** each message is projected through a copy of the known identifier map. Unseen
  identifiers are allocated in semantic key order; normalized messages then sort canonically.
  Arrival order and raw random IDs cannot break ties. No subject is filtered.

### Startup observation

Before recording the first request on every process start, the harness polls authenticated usage
without recording those requests. It waits for progress from both startup tasks:

| Startup progress awaited | Code |
| --- | --- |
| `operations.network_usage` | `internal/app/operations/network.go:networkUsageWorker.Start`; `netusage.Sample` persists the first sample |
| `operations.paper_metrics` | `internal/app/operations/paper_worker.go:PaperMetrics.Start`; `papermetrics.InitializeRun` registers provenance |

The harness counts successful observation requests per instance. Only
`GET /v1/admin/api-analytics as admin.self_calls_today` and
`GET /v1/admin/growth as admin.self_calls` subtract that exact count, so extra polls cannot alter
replay traffic totals; all replay calls and error totals remain visible. Startup task
null-versus-timestamp distinctions remain visible; a missing startup progress report times out and
fails. The other startup scans have empty queues in a new database and need not report progress.

These timer tasks use `<timer>` for `last_attempt` and `last_progress` while enabled, including null
values. Disabled tasks retain nulls. The timer-populated pending counter of `mail.submissions` and
`notifications.delivery` also uses `<timer>`, including before its first tick. Other numeric
pending values remain `<n>` and null pending remains null.

| Timer task names | Code |
| --- | --- |
| `operations.api_metrics` | `internal/app/operations/apimetrics.go:flushLoop` (20-second flush; startup prune/backfill need not make progress) |
| `accounts.connector_sweep` | `internal/handler/connector.go:SweepWorker` (hourly) |
| `accounts.visitor_auth` | `internal/app/accounts/visitor_auth_worker.go:Start` (first expiry after 30 seconds) |
| `http.connector.caches`, `http.oauth.caches`, `http.site.caches`, `http.user.caches` | `internal/handler/http_caches.go:Start` (periodic eviction) |
| `http.hack.limiters` | `products/hack/hackhttp/limiter_workers.go:Start` (ten minutes) |
| `mail.submissions`, `notifications.delivery` | `internal/handler/kinds.go:SubmissionEmailOwner`, `internal/handler/notify.go:Start`; `app/notifications` delivery trackers (periodic queue claims) |
| `operations.geoip_watch` | `internal/geoip/geoip.go:WatchWorker` (minute; initial missing-file error is retained) |
| `operations.saved_data_sweep` | `internal/handler/saveddata.go:Start` (empty startup scan, later periodic maintenance) |
| `hack.event_cleanup` | `products/hack/hackhttp/event_cleanup_workers.go:Start` (ten-minute initial delay) |
| `sites.hack_sweep` | `internal/handler/hack_mode.go:startHackSweep` (empty startup scan, then minute ticks) |
| `sites.address_families`, `sites.domain_checks`, `sites.idle_cleanup`, `sites.preview_expiry` | `internal/handler/familyhost.go`, `domaincheck.go`, `idle.go`, `site.go` (delayed scans; disabled in base) |

Other reported tasks retain their status nullness. `notifications.retention` sweeps synchronously
before readiness. `sites.trash_purge` starts on an empty throwaway database.
`hack.content_delivery` has explicit queue draining/restarts; `notifications.emit` is
request-driven.
`operations.analytics_ingest`, `operations.image_scan`, `sites.cert_requests` and
`sites.event_dns_sweep` are disabled by the base configuration. No additional periodic-work setting
is needed for this configuration.

## Safety

The replay uses local programs and disposable databases. It changes only those databases.
Immediately after `CREATE DATABASE`, it sets the database timezone to `UTC` and asserts that
`SHOW timezone` returns `UTC` before migration. Servers, migration/settings commands and `psql` get
`TZ=UTC`. OpenSSL inherits the harness environment. Database URLs are built structurally. `psql`
uses `-X`, a minimal environment and a deadline. Diagnostics redact database URLs and keys.

Server children get a clean environment with no real provider keys or configured webhooks, and
`EVENT_NAME_PEER` is explicitly empty. Accounts and recipients use `example.com` or `.invalid`.
Servers bind loopback; native HTTP preserves virtual `Host` headers. The proxy intercepts and
reports only HTTP(S) traffic that uses it. Empty external configuration and invented recipients keep
this replay local; the proxy does not enforce a network sandbox.

The local mail sink uses a local CA and CONNECT proxy to receive real provider payloads. All
proxy/CA setup finishes before either product starts. The sink and proxy stay available through
graceful server completion and the final quiet period.

On completion, errors or signals, the harness stops children, closes the sink/proxy and their
sockets, removes temporary data/GeoIP and mail certificate directories, and drops disposable
databases. Failed database drops produce warnings. It keeps recording output for inspection.
Shutdown prevents further requests or restarts. Cleanup may force termination, but that cannot turn
a failed graceful completion into a successful recording.

## Deadlines

| Operation | Deadline |
| --- | --- |
| Each recorded HTTP exchange | 15 seconds |
| Readiness polling | 10 seconds |
| Migration | 30 seconds |
| Settings command | 15 seconds |
| Each `psql` call | 15 seconds |
| Each of the three OpenSSL calls per sink | 15 seconds |
| Startup progress observation | 15 seconds |
| Mail arrival wait | 5 seconds |
| Mail quiet check | 5 seconds, seeking 350 ms without arrivals |
| Graceful server stop | 30 seconds; the server must exit itself with code 0 |
| Failure cleanup stop | 5 seconds before forced termination |

Queue draining first queries and ages the queue, with up to 15 seconds for each `psql` call. It then
restarts the server: graceful stop up to 30 seconds, readiness up to 10 seconds and startup
observation up to 15 seconds. Queue polling has a 10-second deadline; a synchronous `psql` call
already in flight can add up to 15 seconds to that phase. A quiet check follows. There is no
server-lifetime or whole-replay timeout.

### R2-S4b mixed-store exports

`mixed-store` also records site export, people and visitor-email lookup (the last
two remain unavailable on Hack). `mixed-site-export` expands the tar.gz and
compares every member's name and full normalized text, including `storage/kv.json`;
archive header mtimes are omitted, as with ZIP metadata. The fixture contains only
text files. This uses the local `tar` command. The six new requests are the only
R2-S4b approvals; the base scenario stays unchanged.
