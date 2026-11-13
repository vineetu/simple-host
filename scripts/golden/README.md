# Golden recordings

Record the deployed commit's supplied binary and the candidate with the same harness,
then compare their folders. There is no checked-in response baseline. Use Node 22
(with `node:zlib.crc32`), `psql`, and `openssl`; do not rebuild the reference from
this checkout.

```sh
node scripts/golden/run.mjs --host /tmp/reference.bin --out /tmp/reference
node scripts/golden/run.mjs --host /tmp/candidate-host.bin --hack /tmp/candidate-hack.bin --out /tmp/candidate
node scripts/golden/coverage.mjs /tmp/candidate
node scripts/golden/diff.mjs /tmp/reference /tmp/candidate --slice D0-S3a
node --test scripts/golden/tests/*.test.mjs
```

Without `--hack`, the Host binary also runs Hack with `EVENTS=hosted`. Output
folders must be absent or empty. Each product/scenario gets its own disposable
database, temporary data directory (including GEOIP_DIR), port, namer and exact
replacement map. Each binary runs its own `migrate -init`; the checkout schema
is never used. Positional recording arguments are rejected.

Postgres defaults to `postgres://postgres:postgres@127.0.0.1:55412`.
`GOLDEN_PG_URL` must have that prefix shape: postgres scheme, user, password,
localhost or 127.0.0.1, explicit port, and no database path, query or fragment.
Port 5432 is refused because it is shared production Postgres. URLs are built
structurally; psql uses `-X`, a minimal environment, and a deadline. Diagnostics
redact database URLs and keys. Only the disposable databases are changed. Immediately after CREATE DATABASE, the
harness runs ALTER DATABASE SET timezone TO 'UTC', then asserts SHOW timezone
returns UTC before migration. Every server, command and psql process gets TZ=UTC.

The children get a clean environment, no real provider keys, no configured
webhooks, and `EVENT_NAME_PEER` explicitly empty. Accounts and mail recipients
use example.com or .invalid. Servers bind loopback; native HTTP preserves virtual
Host headers. The mail proxy intercepts and reports only HTTP(S) traffic using
the proxy. It is **not a network sandbox**: direct sockets, DNS and transports
that ignore proxy variables can bypass it. The empty external configuration and
invented recipients keep this replay local.

Each HTTP exchange has a 15-second deadline; readiness has a ten-second
polling deadline. Migration has 30 seconds; settings, each psql call and each of
the three OpenSSL calls have 15 seconds. Startup worker observation has a
15-second polling deadline. Mail arrival and quiet checks each have five seconds.
Queue draining first queries/ages the queue (each psql call up to 15 seconds),
then restarts (graceful stop up to 30 seconds, readiness up to ten seconds, worker
observation up to 15 seconds), then polls for ten seconds; a synchronous psql
call already in flight may add up to 15 seconds to that polling phase. There
is no server-lifetime or whole-replay timeout. GOLDEN_TEST_DELAY_MS=25000 sleeps
after initial readiness and before the first request (including worker
observation) on each product; this is a determinism test aid that deliberately
lets the 20-second metrics timer fire. Errors and signals stop children and tear down resources;
shutdown prevents further requests or restarts.

## Output and names

The format-2 manifest lists `hack.json` and `host.json`, product/scenario names,
request counts, and text folders. It contains no clock, binary path or build
identity. Requests stay in replay order and retain method, path, status, selected
headers, body kind and the complete normalized JSON value when applicable.
Every JSON/text body has a full `text/<product>/<sanitized-name>.txt` file and its
SHA-256 and byte length. There is no truncation. JSON text rendering exposes
multiline strings and embedded JSON as readable lines; the retained JSON body
still contains the original string value. The rendering is for diffs, not JSON
parsing. `renderJSON` is unchanged by this pass.

ZIP downloads have JSON bodies with sorted entry names, attributes, hashes,
lengths and full UTF-8 text. CRC-32, declared sizes, local-header consistency and bit-3 data descriptors
(with or without their optional signature) are checked before normalization. Binary entries and other binary downloads use
byte hashes and lengths. Text is accepted only for text/*, JSON/+json, XML/+xml,
JavaScript, YAML and form MIME types, or valid UTF-8 with no MIME type; invalid
UTF-8 is always binary. ZIP compression and entry timestamps are omitted.

Fixed anonymous GETs use `GET /path`. State-dependent reads use descriptive names.
MCP calls all go through the shared dispatch naming logic: `tools/call <tool>
<qualifier>`. Wrapper names include the target after `run_tool`. Settings and
mail use the same collision/name validation as HTTP, as `admin-settings` and
`mail-outbox`. Filenames replace characters outside `[A-Za-z0-9._-]` with `_`;
duplicates or collisions fail. JSON keys are recursively sorted, including mail.
Placeholders are scoped to the request owning their first appearance. Event
join/judge codes belong to `event-create`. Mail is normalized last.

## Scenarios

The default scenario is `base`. `--scenario all` selects the table; comma-separated
names select exactly those scenarios. `--skip-scenario` subtracts names; base
cannot be skipped. Unknown/repeated names fail. Named runs use `host.NAME.json`
and `text/host.NAME/`, likewise Hack. Each callback receives the same context:
`{ instance, at, mcp, mail, namer, exact, calls, usedNames, registerSecret }`.
There is no minimum mail count; a scenario may produce an empty outbox.

A worked example for a future table entry (only base is shipped):

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

Add requests after their prerequisites with `at(name, method, path, {apiKey, body})`.
Uploads use `raw` and `contentType`. Register short random codes under the name of
the response that first returns them, before their later use in paths.
For the branch gate, candidate uses `--scenario all`; reference uses
`--scenario all --skip-scenario rules-on` once that scenario exists. Additional
scenario approvals are reviewed against the absent reference scenario.

## Coverage gate

The gate covers inventory routes without path parameters and MCP tools; it is
not endpoint coverage. Parameterized route counts are informational. Methods
follow ServeMux: ANY accepts all methods, GET accepts HEAD, a trailing slash
matches a subtree, and `{$}` is exact. Any direct tools/call touches its tool,
including target errors. A run_tool target counts when dispatched, not when the
wrapper itself refuses. A tools/list response alone does not cover tools.

`coverage-covered.txt` is the generated sorted nonautomatic touched list. Fixed
anonymous parameter-free GETs are automatic; all other touched fixed routes and
tools belong in covered. Uncovered items need one reasoned entry in
`coverage-exceptions.txt`. Unit tests partition the REAL inventory into exactly
one of automatic, covered, exceptions, so make check's existing test glob catches
new unaccounted inventory without a recording. The folder gate also verifies
text integrity and requires its touched list to equal the checked-in list.

```sh
node scripts/golden/coverage.mjs /tmp/candidate --print
node scripts/golden/coverage.mjs /tmp/candidate --write-covered
node scripts/golden/coverage.mjs /tmp/candidate
```

Regenerate covered only after reviewing newly touched items and removing stale
exceptions. Missing, stale, duplicate or unreasoned exceptions fail.
Authenticated paper-metrics is excluded because it contains runtime latency and
byte observations. Large authenticated tar backups are excluded; Host's account
ZIP is recorded. Anonymous refusal responses remain, including Hack account ZIP.

## Recorded surfaces and current limits

Both editions record OpenAPI JSON/YAML, llms.txt, features.json, sitemap,
hackathons, public pages, the skills catalog, every catalogued skill file and
its ZIP, authenticated admin usage including workers, and settings command
output. Host retains its deployment, version, visibility, passcode, account
export and KV/SQLite/files storage replay. Both products exercise MCP personal
and deploy-only connections with screens off/on, initialization instructions,
tool lists and resources. Hack also exercises the real team-site key in that
matrix. Host has no Hack team-site connection mode.

Each registry family has a direct `tools/call` (the table names requests):

| Product/edition | Registry family | Request |
| --- | --- | --- |
| Host | sites | tools/call list_sites |
| Host | storage | tools/call storage_get_kv (seeded theme = dark) |
| Host | access | tools/call list_site_viewers (read only) |
| Host | imports | tools/call get_import_report |
| Host | accounts | tools/call who_am_i |
| Host | discovery | tools/call find_tools; tools/call run_tool list_versions |
| Host | notifications | tools/call list_notifications |
| Hack | hack-events | tools/call hack_get_event |
| Hack | hack-entries | tools/call hack_get_rubric |
| Hack | hack-app-only | tools/call hack_show_organiser |
| Hack | storage | tools/call hack_event_storage_list_resources |
| Hack | sites | tools/call get_site team-site (live team key) |
| Hack | notifications | tools/call list_notifications |
| screen | access | tools/call passcode_screen |

Calls with unavailable prerequisites keep their actual errors: missing import,
event storage without a custom website, notifications without an OAuth grant,
and the app-only passcode screen without an ephemeral public key. The screen
call reaches the tool and records its invalid-public-key refusal; it does not
fabricate a browser key exchange. Hack has no discovery family: direct
`find_tools` and `run_tool` calls record unknown-tool refusals. Coverage does
not count a refused wrapper's target as called.

Both products deploy `_redirects` and fetch its redirect and a plain address
without `.html` (SIM-440). Host lists a site on its profile and reads the public
feed (SIM-441); Hack records the refusal to change team-site visibility. Hack
creates and reads submitted entries/cards (SIM-442), messages in both directions
(SIM-443), and an announcement with multiline text (SIM-444). Host records its
unavailable Hack route responses. Deploy-only and other-site team-key refusals
are recorded, as is a formerly valid team key after the organiser removes its
person (this binary revokes the key and answers `invalid_api_key`).

SIM-448 records a Host site created private in its first request (201), an
anonymous page refusal (403; the local HTTP SSO start refuses), real emailed-code
form start/verify requests, the named viewer's page (200), and a signed-in
outsider's page (403). No visitor session or provider is fabricated. Hack keeps
its `no_personal_sites` creation refusal (403). Host's passcode sequence records
the anonymous gate (401), wrong passcode (401), correct passcode (303), and
unlocked contents (200).

After the SIM-448 named-viewer code verification, Host also records both
site-session identity GET aliases, visitor logout, and a session read using the
old cookie after logout. The viewer and outsider page results remain recorded.

SIM-449 keeps the Host delete/admin-take-down/owner-trash-restore/page sequence
(final page 410). Hack Team A serves (200), is deleted (204), then taken down
while deleted (200). Its actual Recently deleted restore is attempted through
REST (403 `team_key_scope` with the team key, 404 with the member key) and MCP
`restore_site` (HTTP 200 tool error wrapping the same 403). The status says
`exists: false`, `taken_down: true`, and the page stays 404. `teamRoutes` in
`internal/auth/scope.go` does not allow POST site restore; the member's account
is not the separate team's holding account. There is no public admin trash
restore route. The successful old-value restore regression cannot be reached
with any credential the harness can obtain on Simple Hack: team keys and
team-site connections are refused with `team_key_scope`, while a member gets
404 because the site belongs to the event's holding account. Team-site
connections mint scoped internal credentials too (`connector.go:internalKeyFor`,
`auth/scope.go:teamGate`). The recording therefore holds the take-down while
deleted and the refusals, not a restored page. The only routes reaching
`app/sites.RestoreTrashed` are the owner's `POST /v1/sites/{sitename}/restore`
(recorded on Host) and the mailed idle-cleanup restore link (`GET` confirmation
and `POST /v1/idle/restore`, not recorded). Verified through `handler/deleted.go`,
`handler/idle.go` and `app/sites/deleted.go`; MCP `restore_site` replays the same
owner REST route.

Team B is deployed while building, then taken down live. Deleting that suspended
site through the admin API is itself refused (403 `site_suspended`):
`sites.Service.Delete` refuses a suspended site before `Trash`. The lift and
REST/MCP trash-restore attempts are recorded, then status and page (200). It
never entered Recently deleted, so this records the mirror's blocking behavior,
not a successful mirror regression. Names explicitly identify each attempt.


The Hack catalog advertises `agents/openai.yaml` for join-hackathon,
judge-hackathon, run-hackathon, website-deploy and website-deploy-builder, but
those five individual HTTP routes return 404 today. Their 404 records remain;
the supported ZIPs additionally expose every entry's full text in the recording.

## Mail completion

The local HTTPS provider sink accepts the real payload via a local CA and
CONNECT proxy. Mail retains every provider field, including from, recipients,
cc/bcc, reply_to, headers, tags, full text and full HTML when sent. Attachments
retain metadata plus decoded-byte SHA-256/length; HTML also has a derived hash.
Sorting projects each message through a copy of the existing identifier map;
raw random IDs never break ties. Unseen identifiers are allocated in semantic
key order, then normalized messages are canonically sorted.
No subject is filtered out.

Hack actors sign in through the real emailed-code API. Admin-minted accounts
are deliberately marked event_account by `app/accounts/account_lifecycle.go` and
are ineligible for notice mail (`repository/hackevents/notice_delivery.go`), so
they cannot exercise these deliveries. Host retains admin account seeding and
real named-viewer/outsider emailed-code sessions.

`hackhttp/hack_content.go` runs content/notice delivery at startup and every
minute. `app/hack/events/notices.go:NoticeMailTiming` specifies a one-minute
undo delay and fifteen-minute grouping window; there is no environment override.
After mutations, pending queues in the disposable database are made eligible:
pending notice created_at is moved back two minutes and prior sent timestamps
back sixteen minutes. Restart triggers the existing startup drain; the harness
waits until pending notices, receipts and announcements settle before the next
mutation. This controls eligibility without changing the mail payload or
fabricating delivery. Stale notices may still be skipped by the application.
Receipts, sign-in codes and sign-in alerts have explicit waits. The replay also
removes a Team C member while building to exercise the team-removal notice.
The captured classes are sign-in codes, sign-in alerts, entry receipts, team
takedown, site restoration, messages in both directions, judging-open and
team-removal notices (22 Hack messages and two Host messages). Nothing is
excluded from the received outbox. At each restart and finalization the server gets SIGTERM and must exit itself
with code 0 within 30 seconds while the sink/proxy remain available. An already
exited server, abnormal exit or required SIGKILL fails the recording. Cleanup
may force termination, but never makes that run successful. Only confirmed
graceful producer completion followed by a bounded quiet period permits final
outbox capture. No scenario implicitly owes two messages.

## Normalization rules

Classification is explicit by request name in `RESPONSE_CLASSES` / `responseClass`,
independent of HTTP method or authentication:

| Class | Request names |
| --- | --- |
| Static | Automatic fixed `GET /path` inventory names except the explicit dynamic overrides below; skill files and skill ZIP entries; `tools/list ...`, `initialize ...`, `resources/list ...`; `admin-settings`; `tools/call find_tools ...`, `tools/call get_page_recipe ...`, `tools/call hack_get_page_recipe ...` |
| Dynamic | `admin-usage`, `GET /v1/admin/usage`, `GET /v1/admin/usage as admin`; `event-public-page`; `GET /v1/u/owner/showcase.json`; all remaining descriptive seeded-state reads/mutations |

The two public seeded-state exceptions are the event page's stage/publication
instants and the showcase feed's site creation/update instants. The fixed GET
experiment and the documentation classes are compared in repeated recordings.
Static rules replace only the instance base URL, bare host:port and temporary
directory, plus HTML/CSP nonces. Sitemap requests alone mask lastmod and split
adjacent XML tags. Other UUIDs, checksums, dates and timestamps remain literal;
`2026-10-12` in settings and `2026-10-01` / `2026-11-02` in Hack tool schemas
remain verbatim. No per-request runtime exception was needed in MCP/settings
documents. Other XML/SVG bodies retain whitespace and CDATA.

The complete relative-calendar-date request list is
`GET /v1/admin/growth as admin` (both products). Its clock-derived current and
comparison windows are constructed by `internal/app/operations/apigrowth.go`
using the database day from `internal/repository/operations/api.go:APIToday`.
Other usage/analytics responses in this replay contain counts and ISO instants,
not clock-derived calendar date strings. All other responses retain calendar
dates. Account export root names and its Content-Disposition filename use a
separate, account-export-only `<date>` rule, not `<today-N>`.

Dynamic responses and account-export entries additionally normalize:

- UUIDs, shk_ keys, bare 32–64 hex tokens, Hack u-hex handles and signed preview
  tokens into request-scoped placeholders, preserving equality of references.
  Explicitly registered join/judge/invite/team codes use the same exact map.
  Known limit: fixed ID/checksum/timestamp constants inside dynamic API answers
  cannot be distinguished from generated values by these general patterns.
- Generated ISO timestamps to `<ts>`; seeded 2020 event start/end dates and their
  midnight ISO forms stay visible. Only the date request listed above enables
  calendar substitution in the run's UTC window (400 days before through
  tomorrow): `<today>`, `<today-1>`, etc., retaining bucket relationships.
  A run crossing UTC midnight fails and asks to record again.
- Nonempty string values under passcode/last4 to `<volatile>`. Objects, arrays,
  numbers, booleans, empty strings and null retain their values and types; field
  names are not exclusively credential fields and schemas remain visible.
- Usage network/disk numeric measurement leaves to `<n>` (configured
  network allowance_gb and alert_pct remain visible); structure, key sets,
  nulls and nonnumeric leaves survive. Numeric byte quantities in usage message
  prose become `<measurement>` while wording/status survive. Worker timestamp
  strings become `<ts>` and numeric pending counts `<n>`. Timer workers below
  instead use `<timer>` for last_attempt/last_progress, including null; all
  other null/type distinctions survive. Names, order, enabled/running, errors,
  error timestamp nullness and configured intervals stay visible.
- Only the three server-card GETs normalize root version, and the three named
  admin usage responses normalize nonempty string root version/commit. Nulls,
  empty strings, numbers and structures survive. No BIN-version exact
  substitutions exist. Plugin/skill/OpenAPI/MCP protocol versions remain visible;
  initialize's serverInfo.version is a bundled skill version and stays visible.
- Judging dashboard teams/judges and three judging-settings selections sort by
  seeded names because their SQL order uses random IDs. Rows and fields survive.
- Only event-public-page masks the human times labelled Entries due and Results
  published. Only the account export ZIP root/Content-Disposition filename
  dates become `<date>`; static skill/plugin ZIP filenames stay literal.
- ETag presence becomes `<etag>`. Nonempty cookie values become `<value>`; empty
  values stay empty. HTTP-date Expires becomes `<http-date>`. Passcode cookie
  names mask their site-ID-derived suffix; other names/attributes stay visible.
  Cookie lists sort. Selected headers include content-type/cache-control,
  location, cookies, robots, authentication, disposition and CSP.
- Only sign-in mail subject/text/html replaces its six-digit code as a standalone
  token or inside a link; envelope, headers and attachment names are untouched. The
  sign-in alert subject also scopes normalization of its human `When:` line to
  `<ts>` before dynamic normalization and HTML hashing. Codes never enter a global digit map.

JSON object keys sort safely, preserving __proto__. ZIP raw entries sort before
placeholder allocation; derived hashes are never normalized again. ZIP contents
keep permissions/attributes and full text but omit compression/timestamps.

The supplied binaries have no clock-shift setting. A real next-day run was not
simulated by changing the machine clock. Unit tests use two different today
values and verify identical relative date windows; the runner additionally
rejects midnight crossings so seeding cannot straddle daily buckets.

## Worker observation

Before recording the first request on every process start, the harness polls an
UNRECORDED authenticated usage request for progress from both startup workers:

| Startup progress awaited | Code |
| --- | --- |
| operations.network_usage | internal/app/operations/network.go:networkUsageWorker.Start; netusage.Sample persists the first sample |
| operations.paper_metrics | internal/app/operations/paper_worker.go:PaperMetrics.Start; papermetrics.InitializeRun registers provenance |

Successful observation requests are counted per instance. Only
`GET /v1/admin/api-analytics as admin.self_calls_today` and
`GET /v1/admin/growth as admin.self_calls` subtract that exact count, so extra
polls cannot alter replay traffic totals; all replay calls and error totals
remain visible. Startup-worker null versus timestamp distinctions remain visible; a
missing startup progress report times out and fails. The other startup scans
have empty queues in a new database and need not report progress.

These explicit timer workers use `<timer>` for last_attempt and last_progress
whatever their values while enabled (disabled workers retain nulls). The
timer-populated pending counter of `mail.submissions` and
`notifications.delivery` also uses `<timer>`, including before its first tick.
Other numeric pending values remain `<n>` and null pending remains null.

| Timer worker names | Code |
| --- | --- |
| operations.api_metrics | internal/app/operations/apimetrics.go:flushLoop (20-second flush; startup prune/backfill need not make progress) |
| accounts.connector_sweep | internal/handler/connector.go:SweepWorker (hourly) |
| accounts.visitor_auth | internal/app/accounts/visitor_auth_worker.go:Start (first expiry after 30 seconds) |
| http.connector.caches, http.oauth.caches, http.site.caches, http.user.caches | internal/handler/http_caches.go:Start (periodic eviction) |
| http.hack.limiters | internal/hackhttp/limiter_workers.go:Start (ten minutes) |
| mail.submissions, notifications.delivery | internal/handler/kinds.go:SubmissionEmailOwner, internal/handler/notify.go:Start; app/notifications delivery trackers (periodic queue claims) |
| operations.geoip_watch | internal/geoip/geoip.go:WatchWorker (minute; initial missing-file error is retained) |
| operations.saved_data_sweep | internal/handler/saveddata.go:Start (empty startup scan, later periodic maintenance) |
| hack.event_cleanup | internal/hackhttp/event_cleanup_workers.go:Start (ten-minute initial delay) |
| sites.hack_sweep | internal/handler/hack_sites.go:startHackSweep (empty startup scan, then minute ticks) |
| sites.address_families, sites.domain_checks, sites.idle_cleanup, sites.preview_expiry | internal/handler/familyhost.go, domaincheck.go, idle.go, site.go (delayed scans; disabled in base) |

Other reported workers retain their status nullness: notifications.retention
sweeps synchronously before readiness; sites.trash_purge starts on an empty
throwaway database; hack.content_delivery has explicit queue draining/restarts;
notifications.emit is request-driven. operations.analytics_ingest,
operations.image_scan, sites.cert_requests and sites.event_dns_sweep are disabled
by the base configuration. No additional periodic-work setting is needed.
