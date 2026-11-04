# Target architecture

## 1. Purpose and status

This is the target, not a description of code already shipped. `ARCHITECTURE.md` describes today. This is the plan of record for the owner-approved cleanup of 2026-11-02, based on the review and `PLAN.md`. Source landmarks were checked at `8c72b441`, after phase 1. Drafted by Astra, reviewed and adopted by the cleanup lead on 2026-11-02. Keep one binary and one shared implementation; Host and hosted Hack remain separate instances with separate databases and file roots. Each move must ship independently. Preserve API responses, screens, credentials, transaction boundaries and stored data, except for the explicitly approved classic-app retirement. Extract workflows through the existing handlers; do not replace the application in one pass.

| Cleanup step | Work and governing section |
|---|---|
| 2.1 | Adopt this target; subsequent changes follow it. |
| 2.2 | Move sign-in, then retire the classic Hack app (§7). |
| 2.3 | Replace bundle rewriting with esbuild and platform adapters (§7). |
| 3.1 | Inject one immutable Edition; remove global policy (§3, §10). |
| 3.2 | Separate Hack transport and workflows on shared services (§2, §10). |
| 3.3 | Enforce the import allowlist and shrinking transition exceptions (§2). |
| 4.1 | Extract entries, judging and results (§4, §5, §10). |
| 4.2 | Extract events, members and teams (§4, §5, §10). |
| 4.3 | Extract sites, deploys and storage; retain compatibility (§5, §8, §10). |
| 4.4 | Group MCP capabilities around one inventory (§6, §10). |
| 5.1 | Generate route/tool inventories and share feature IDs (§6, §9). |
| 5.2 | Document settings and deployment profiles, including migration ownership (§3, §9). |
| 5.3 | Make the current architecture and four focused guides match shipped code (§9). |
| 5.4 | Complete worker lifecycle and admin progress reporting (§5). |

## 2. Package map and import contract

Keep names that already express a useful boundary. `internal/handler` remains the shared HTTP transport; `internal/mcp` remains MCP. Introduce `internal/hackhttp` for Hack HTTP. A new transport directory hierarchy would add moves without improving these boundaries.

```text
cmd/server/                  composition root, startup, shutdown, operator commands
internal/
  handler/                   shared HTTP: sites, storage, accounts, connector, admin
  hackhttp/                  Hack HTTP, event hosts, public pages and signed-in app
  httpx/                     response mapping, route registration, common HTTP helpers
  auth/                      HTTP credential/context and scope middleware
  mcp/                       JSON-RPC, family registrars, discovery, MCP Apps assets
  webassets/                 embedded static pages and static/hack modules
  app/
    errors/                  small service error values shared by transports
    accounts/                identity, sessions, scopes, account lifecycle
    sites/                   deploys, versions, addresses, visibility, deletion, imports
    storage/                 KV, SQLite and runtime-file workflows
    legacy/                  the single Host saved-data compatibility adapter
    hack/events/             events, organisers, stage transitions, event content
    hack/teams/              teams, team admin and links, team finding
    hack/members/            leaving, registration, applications and tracks
    hack/entries/            entries, settling, judging, voting and results
    notifications/           webhooks, MCP Events and mail delivery
    operations/              admin reporting, analytics, usage and image-scan orchestration
  repository/
    sqlkit/                  Querier and transaction-opening primitive
    accounts/ sites/ storage/ legacy/ hackevents/ hackteams/ hackentries/ notifications/ operations/
  model/                     shared records and value types, without SQL or transport
  rules/
    auth/ sites/ storage/ hackevents/ hackteams/ hackentries/
  edition/                   immutable product policy and instance limits
  config/                    environment parsing, defaults and settings catalog
  workers/                   lifecycle/status types, not a scheduler or queue framework
  sitelocks/                 one shared account/site lock set per instance
  storage/                   disk operations, including runtime files and SQLite files
  analytics/ netusage/ papermetrics/   measurement and aggregation helpers
  email/ eventdns/ oauth/ imagescan/   external provider clients
  brain/ buildinfo/ capacity/ geoip/ tarball/ wpimport/   existing focused helpers
  testdb/ geoip/geoiptest/     test fixtures only
```

Packages appear only when the first code moves into them. Do not commit empty skeletons or placeholder interfaces ahead of the step that needs them. Repositories are area packages, not one package per table. (Step 4.2 split Hack events into three app packages, events, teams and members, and two repositories, hackevents and hackteams, because one package would have held about 4,000 lines; hackteams may read hackevents, and hackevents may reuse hackentries' eligibility SQL.) `model` receives existing shared records as they move out of `db`; do not create a second permanent set of identical structs. Transport DTOs and application request/result types stay with their consumers. Pure rule inputs can be smaller values than stored records.

| Package or group | Responsibility; forbidden work |
|---|---|
| `cmd/server` | Construct dependencies and register transports; no product workflow implementation. |
| `handler`, `hackhttp`, `auth`, `mcp` | Decode/authenticate at the boundary, call services, encode; no SQL, transactions or disk mutation. |
| `httpx` | Shared protocol mechanics and registration metadata; no application orchestration. |
| `app/accounts` | Resolve principals and scoped authority, including team/event-website scope; no HTTP request types. |
| `app/sites` | Own publication and site lifecycle, using shared auth and storage services; no Hack service dependency. |
| `app/storage` | Enforce resource access, quotas and mutations; no browser/MCP decisions. |
| `app/legacy` | Preserve old Host contracts through typed operations; no new product capabilities. |
| `app/hack/{events,teams,members,entries}` | Own Hack workflows on shared services; never reach back into handlers. |
| `app/notifications` | Own delivery queues, leases, retries and templates; no transport invocation. |
| `app/operations` | Own operator workflows and background measurement/scanning; no HTTP response writing. |
| `repository/*` | Execute SQL and map rows; never import app services, transports, disk storage or provider clients. |
| `storage` | Implement filesystem and per-site SQLite mechanics; no Postgres, actor policy or app imports. |
| `rules/*`, `model` | Deterministic decisions and values; no SQL, HTTP, filesystem, provider calls or ambient clock. |
| Other infrastructure | Focused mechanisms; no product service imports. Dependencies are limited below. |

The following is the final production allowlist, matching the `[final]` rows in `scripts/layering/rules.txt`. Paths are relative to `internal/`; `@/` prefixes module-root paths. Braces expand to exact alternatives; `/**` means a package and its descendants; `!` excludes a destination from that row. Multiple matching rows are unioned. A missing row allows no internal imports. External/standard-library imports are outside this table; an import check alone cannot prove that handlers contain no SQL.

| Importing path pattern | Allowed dependency paths |
|---|---|
| `@/cmd/server` | `/**`, `!testdb`, `!geoip/geoiptest`, `@/db`, `@/db/migrations` |
| `{handler,hackhttp}` | `app/**`, `auth`, `httpx`, `edition`, `model`, `webassets`, `buildinfo`, `workers` |
| `handler` | `mcp` |
| `auth` | `app/accounts`, `httpx`, `edition`, `model`, `rules/auth`, `rules/sites` |
| `hackhttp` | `rules/sites` |
| `mcp` | `app/**`, `edition`, `model`, `httpx`, `buildinfo`, `brain`, `webassets`, `workers` |
| `httpx` | `app/errors`, `model`, `edition` |
| `app/{accounts,sites,storage,legacy,notifications,operations}` | `app/errors`, `model`, `edition`, `workers`, `repository/sqlkit` |
| `app/hack/{events,teams,members,entries}` | `app/errors`, `model`, `edition`, `workers`, `repository/sqlkit` |
| `app/accounts` | `repository/{accounts,sites,hackevents}`, `rules/{auth,hackevents}`, `oauth`, `email`, `geoip` |
| `app/sites` | `app/{accounts,storage,notifications}`, `repository/{sites,accounts}`, `rules/{sites,auth}`, `storage`, `sitelocks`, `tarball`, `wpimport`, `eventdns`, `capacity` |
| `app/storage` | `app/{accounts,notifications}`, `repository/{storage,sites}`, `rules/{storage,auth}`, `storage`, `sitelocks` |
| `app/legacy` | `app/{accounts,storage,notifications}`, `repository/{legacy,sites}`, `rules/{storage,auth}`, `sitelocks` |
| `app/hack/events` | `app/{accounts,sites,storage,notifications}`, `app/hack/entries`, `repository/{hackevents,hackteams,hackentries,accounts,sites}`, `rules/{hackevents,hackentries,auth}`, `storage`, `capacity` |
| `app/hack/{teams,members}` | `app/{accounts,sites,storage,notifications}`, `app/hack/{events,entries}`, `repository/{hackevents,hackteams,hackentries,accounts,sites}`, `rules/{hackevents,hackteams,sites,auth}` |
| `app/hack/entries` | `app/{accounts,sites,storage,notifications}`, `repository/{hackentries,hackevents,hackteams,sites}`, `rules/{hackentries,hackevents}`, `storage` |
| `app/notifications` | `app/accounts`, `repository/{notifications,accounts,hackevents,hackentries,legacy}`, `email`, `rules/{hackevents,hackentries}` |
| `app/operations` | `app/{accounts,sites,storage}`, `repository/{operations,accounts,sites,hackevents,hackentries,legacy}`, `storage`, `analytics`, `netusage`, `papermetrics`, `geoip`, `imagescan`, `email`, `capacity`, `buildinfo` |
| `repository/{accounts,sites,storage,legacy,hackevents,hackteams,hackentries,notifications,operations}` | `repository/sqlkit`, `model`, `rules/{auth,sites,storage,hackevents,hackteams,hackentries}` |
| `repository/hackteams` | `repository/hackevents` |
| `repository/hackevents` | `repository/hackentries` |
| `rules/{auth,sites,storage,hackevents,hackteams,hackentries}` | `model` |
| `edition` | `config` |
| `storage` | `model` |
| `{analytics,netusage,papermetrics}` | `model`, `workers` |
| `geoip` | `workers` |
| `{app/errors,model,config,workers,sitelocks,repository/sqlkit,webassets}` | None |
| `{brain,buildinfo,capacity,email,eventdns,imagescan,oauth,tarball,wpimport,testdb,geoip/geoiptest}` | None |
| `@/cmd/analytics-rebuild` | `analytics`, `config` |
| `@/cmd/ip-country-load` | None |
| `@/scripts/layering` | None |
| `@/scripts/inventory` | `mcp` |
| `mcp/apps/gentool` | `mcp/apps/bundlegen` |

`app/errors` is a small error-value package, not a framework. Cross-service calls follow the directed rows above. A needed reverse dependency is expressed as a narrow interface owned by the consumer and wired in `main`, not an import cycle. For example, sites can enqueue image scans through an injected interface implemented by operations. App interfaces must not mention handler types. Shared site operations must not call Hack HTTP to decide permission.

Test imports use the production allowance plus the exact package under test, `testdb`, and `geoip/geoiptest`. MCP tests additionally allow `mcp/apps/bundlegen` for the bundle freshness test. Integration fixtures in transport `_test.go` files may also import `config`, `repository/**`, `storage`, `sitelocks`, and `email` to construct a real instance. These additions never apply to production files. `testdb` is never a runtime dependency. Existing exceptional test edges must be named individually, not hidden under `internal/**`.

The layering check reads Go's package graph, including `Imports`, `TestImports` and `XTestImports`, against `scripts/layering/rules.txt`. Normalize synthetic test package IDs and check nested packages independently. Check packages in this module, not third-party internals. Commands and module-local generators have explicit dependency rows in the final table.

Step 5.4 completed the import migration: `[transitional]` is empty, and the final table applies without exceptions. During earlier steps, exact transitional edges carried removal steps; those retired edges are not precedents. New edges must match the final table or receive an explicit architecture review. The old regex bundler is gone; the 2.3 esbuild generator uses the final `mcp/apps/gentool -> mcp/apps/bundlegen` allowance.

| Existing package | Destination |
|---|---|
| `handler` | Splits into shared transport, `hackhttp`, app services, rules, repository calls and `webassets`; the package shrinks throughout. |
| `db` (retired in step 5.4) | Split into area repositories and `model`; SQL constants stay with their area. All temporary aliases/forwarders were removed. |
| `auth` | HTTP middleware stays; `scope.go` decisions move to accounts/rules; database access moves to repositories. |
| `mcp` | Stays; tool families are files in this package, not mutually dependent subpackages. |
| `mcp/apps/bundlegen`, `mcp/apps/gentool` | Replaced in 2.3 by the pinned asset-generation command; generated assets stay in `mcp/apps`. |
| `storage` | Stays; receives runtime-file/SQLite mechanics from `site_storage*.go`. |
| `config` | Stays; loses active-limit globals. `edition` holds the constructed policy. |
| `analytics`, `netusage`, `papermetrics` | Measurement helpers stay; SQL moves to `repository/operations`, loop orchestration to `app/operations` by 5.4. |
| `email`, `eventdns`, `oauth`, `imagescan` | Stay as provider clients; workflow code currently in handlers moves to the relevant app service. |
| `brain`, `buildinfo`, `capacity`, `geoip`, `tarball`, `wpimport` | Stay focused helpers. `tarball` gets explicit extraction limits; `geoip` gets joined watching. |
| `testdb`, `geoip/geoiptest` | Stay test-only. |

## 3. One Edition per instance

Use `edition.Edition`, constructed by `edition.New(cfg)` after saved instance settings have been resolved and before normal routes or workers are constructed. Setup mode uses its own setup handler and exits as today. The constructor derives product policy from `cfg.Events == "hosted"`; self-hosted Host with event DNS enabled is still Host. No build tags or second binary.

Store the following fields privately. Return scalar/value copies from accessors, and copy any maps/slices at the boundary. There are no setters. `Kind` is for composition, presentation and diagnostics; services ask named policy questions instead of spreading `Kind == Hack` throughout the tree.

| Private field / value type | Policy question and evidence in today's tree |
|---|---|
| `kind Kind` (`Host`, `Hack`) | Which product is this? `hack_mode.go`, `main.go`'s `hosted` branch. |
| `routes RoutePolicy` | Hosted event routes/hosts versus personal-site UI; Host-only brain and visitor tools; `ui.go`, `personhost.go`, `visitor_tools.go`. |
| `storage StoragePolicy` | Legacy route exposure; owner interpretation; Host separate data/file allowances versus Hack combined pool; `site_storage_access.go`, `site_storage.go`, `site_storage_files.go`. |
| `identity IdentityPolicy` | User-chosen versus random handles; personal site creation; holding accounts; team/event-website credentials and frozen writes; `handles.go`, `hack_sites.go`, `auth/scope.go`. |
| `notifications NotificationPolicy` | Shared visitor events, Host site lifecycle events, Hack membership scoping; `notify.go`, `notify_routes.go`, `notify_mcp.go`. |
| `presentation PresentationPolicy` | Product name, public base, chrome, asset/skill selection and API audience; `chrome.go`, `basetext.go`, `hack_instance.go`, `discovery.go`. |
| `limits config.Limits` | TTLs, retention, rates, quotas, Hack caps and worker settings; exact validated values, not another defaults catalog. |
| `addresses AddressPolicy` | Configured host modes/bases; replace implicit instance-host globals. |
| `upload UploadPolicy` | Archive ceiling, currently read by `handler/site.go:init` from `MAX_ARCHIVE_MB`; move parsing into config without changing defaults or override semantics. |

Examples of policy accessors: `Routes().HostedEvents`, `Storage().LegacyRoutes`, `Identity().TeamKeys`, `Identity().PersonalSites`, `Storage().CombinedPool`, and `Limits()`. Policy structs have a fixed small vocabulary, not one boolean per call site. Dynamic event membership, deadlines, readiness markers and site presets are database/filesystem facts, never fields frozen into Edition. Secrets and live dependencies are separate constructor arguments.

`main` passes the same `*edition.Edition` to service, transport and worker constructors. A repository or low-level helper needing a TTL gets that scalar/options value from its caller, not the whole application config. Replace `ApplyLimits` fan-out to `config.SetActive`, `db.SetLimits`, `mcp.SetLimits` and `tarball.SetMaxEntries` with instance-bound values. Formatting of those limits uses the same snapshot as enforcement.

Tests construct Host and Hack Editions with explicit `config.DefaultLimits()` and overrides, then two application fixtures in one process. Neither fixture calls `SetHackMode`, `SetTeamKeys` or `SetActive`. Product-dependent asset caches, chrome, instance URL and MCP address/limit text are instance-owned too; merely moving the bool would leave mixed-edition tests unsafe. Keep fixed immutable catalogs global.

Migration in 3.1: add constructor plumbing first; move routes/presentation, accounts/scopes, sites/storage, notifications, then limits and helper callers. For unmoved code, temporary startup setters retain today's behavior, but converted code never reads them. Remove setters, global caches keyed by implicit product, `hackMode`, `HackMode()` and `teamKeys` after the last caller moves. The review's 127 occurrences are a baseline, not a completion counter. Completion means no runtime policy globals or tests mutating them remain in these paths.

## 4. Rules, services, repositories and errors

Pure rules take values and return decisions. Examples are effective deadlines, entry completeness, judgeability, assignment planning and result arithmetic. Pass `now time.Time` when a rule needs time. App services read current state, authorize the operation, acquire locks, open transactions, call rules, write through repositories and emit existing background work. Repositories execute queries and expose explicit locking reads. Handlers decode, call a service and encode; middleware authentication is not a substitute for service authorization.

Worked example: `handler/hack_judging.go:entryJudgeable` checks `Exists && (SubmittedAt.Valid || SubmitSettledAt.Valid) && !ExcludedAt.Valid`. `db/hack_entries.go:EntryJudgeableSQL` expresses the same predicate. `EligibleJudgeTeams` and scoring queries use it; the open-assignment queue additionally filters in Go. Incomplete settled entries remain judgeable. No entry row, or an excluded entry, is not judgeable. The stage-transition path `settleEntriesTx` first calls `startSiteOnlyEntries` for frozen teams with a live site but no entry. The periodic `SettleDueEntries` instead enumerates existing unsettled entries. Preserve that distinction during extraction.

- Move the value predicate to `rules/hackentries.Judgeable`, together with completeness rules from `hack_entry_rules.go` and pure arithmetic from `hack_judging_math.go`.
- `app/hack/entries` owns settling, eligibility, assignments, scoring and results. It supplies the queue, progress and confirmation counts from the same eligible set, including conflicts and assignment mode.
- `repository/hackentries` keeps an SQL predicate when filtering before pagination/counting is necessary. This is a query implementation of the rule, not a second policy authority. Reuse one small table of existing rule cases to compare Go and SQL results; do not fetch entire events merely to avoid SQL.
- `static/hack/screens/participant-entry.js` interprets status/content and required/optional/off fields; judge screens already consume `row.incomplete`. There is no exact JS function named `entryJudgeable` in these modules. Remove eligibility inference, not ordinary rendering. The server is authoritative; JS and MCP consume its answer.
- Preserve existing response fields and meanings first (`missing`, status, queue membership, `incomplete`, counts). If an answer is missing, add a documented field without removing old fields, then switch consumers in a separate compatible change. Do not silently change a response shape as part of extraction.

Services return typed errors with stable domain code, safe message and structured details where the current response has them. They do not return HTTP status or write JSON. `httpx` maps those errors to the existing HTTP status/body/headers. MCP's existing REST replay receives that same mapping; a direct service adapter uses it to preserve MCP error text and structured content. Keep the current not-found versus forbidden distinctions, error precedence, status codes and omitted fields; no new uniform envelope. SQL/provider errors are logged with context and mapped to the existing internal-error response.

## 5. Transactions, locks and workers

The app method owns `BeginTx`, commit and rollback. Move the existing `db.Querier` shape from `db/queries.go` to `repository/sqlkit`; both `*sql.DB` and `*sql.Tx` implement it. Pass the same transaction to every repository operation in a workflow. A method requiring a lock accepts `*sql.Tx` explicitly. A small injected transaction opener can wrap `*sql.DB`; no generic unit-of-work framework. Existing repository functions that open transactions move their orchestration outward without changing boundaries. Cross-service work inside an existing transaction uses an explicit `...InTx` method; it never opens a nested transaction.

Preserve these actual orders, rather than inventing one universal ordering:

| Current flow | Required order and destination |
|---|---|
| `hack_administration.go:acceptOrganiserInvite` | Resolve invite's event without locks; event `FOR UPDATE`; re-lock/revalidate invite and inviter membership; then mutation. Keep `invalid_code` precedence over ended-event checks. Moves to events. |
| `hack_leave.go:postLeaveEvent` | Event `FOR UPDATE`, then `LeaveSelf` membership and affected team work in the same transaction. Preserve creator handover, score retention and frozen empty-team behavior. Moves to events. |
| `hack_teams_admin.go:lockCallerTeam`, `lockCallerAndTargetTeam` | Event `FOR SHARE`, member rows sorted by `user_id` when two are involved, then team. Re-read stage and membership under lock. Moves to hackevents repository/events service. |
| `setStage` | Event `FOR UPDATE` before team locks; keep settling and stage mutation within their current boundaries. Moves to events, calling entries with the same transaction where needed. |
| `site.go:commitCreate`, `commitSiteUpdate` | Account file mutex before site mutex. Create's Hack gate precedes the user-row lock; update locks the capped account and site before its Hack gate. Preserve each sequence, including membership/team share locks. Moves to sites. |
| `site.go:renameSite` | Two site mutexes in sorted name order; disk rename with existing DB-failure compensation. Moves to sites. |
| `site_storage.go:storageWriteLock` | Consume body first; bounded acquisition of the same site mutex used by deploys; recheck resource fingerprint; retain timeout and `resource_changed` behavior. Moves to storage service. |

`sitelocks` contains the shared instance lock set, replacing separate maps on handlers. `db.TeamWriteStateFor(..., true)` holds the team row `FOR SHARE`; deadline pinning holds it `FOR UPDATE`. Keep that serialization. Do not reorder site, member and team locks to match the event-only table during a mechanical move. Any discovered lock-order repair is a separately reviewed behavior fix.

New deploy ordering comes from `commitCreateLocked` and `commitSiteUpdateRespond`: write a non-live version directory; update version/active metadata and commit; then promote `current`. An unpublished version is marked ready without promotion. Preserve marker/link updates and post-publication pruning/notifications. A failed promotion after commit is still a possible partial failure; extraction does not make Postgres and disk atomic.

`setActiveVersion` deliberately has different ordering today: hold the site lock, update metadata inside the transaction, switch `current`, then commit; on commit failure restore the previous version. Preserve that compensation path. Do not silently unify rollback and new-deploy publication order during 4.3.

Runtime files have different ordering in `site_storage_files.go:storageFiles`: write a temporary file, sync and close it. An edit records/preserves author metadata before rename. A create uses a create-only hard link, then writes author metadata; failed metadata insertion removes the new destination. Keep own-reader visibility, conflict behavior, cleanup and notification timing. Move these primitives into `storage`, with orchestration in `app/storage`. Keep SQLite's own transaction/locking behavior and runtime data outside deploy-version replacement; do not promise a transaction across SQLite, Postgres and disk.

Every background loop has one owner. Constructors become inert. The composition root starts enabled owners, retains their handles, and calls `Stop()` then `Wait()` before closing dependencies. `Start(ctx)`, idempotent `Stop()`, `Wait()` and a small `Status()` are the common contract, not a new scheduling system. Cancellation must interrupt waits and reach in-flight operations. `Wait` includes child goroutines and asynchronous emit/enqueue work.

| Real startup source today | Target owner |
|---|---|
| `connector.StartSweep` | Accounts: connector client/token/session expiry. |
| `hack.StartCleanup` | `hackhttp` limiter owner, using shared transport limiter helpers. |
| `EventDomainHandler.StartSweep` | Sites: event DNS claim expiry. |
| `geo.Watch` | GeoIP object, retained and joined by main. |
| `NewAPIMetrics` starts `flushLoop` | Operations: API aggregate flush and retention. |
| `analytics.Ingester.Start` | Operations: nginx-log ingestion. |
| `StartSiteCertRequests` | Sites: certificate request backfill/sweep. |
| `StartHackSweep`, `SiteHandler.hackSweep`, main's `hackSweep` callback | Events owns the sweep; calls sites for orphan trash/markers/certs and entries for settling. Preserve pinning and cadence. |
| `HackHandler.StartEventCleanup` | Events: ended-event retention, warnings and removal. |
| `HackHandler.StartContentDelivery` | Notifications: content/receipt mail, `queueEntryReminders`, `deliverNoticeMail`, preserving their order and retry semantics. |
| `StartDeletedSitePurge`, `StartIdleCleanup` | Sites: trash retention and idle-account cleanup. |
| `StartSavedDataSweep` | Operations: legacy retention/idempotency/watch cleanup; HTTP limiter eviction moves to its transport owner. |
| `StartSubmissionEmails` | Notifications: legacy submission mail. Preserve existing eligibility and startup on both editions. |
| `StartNotifications` | Notifications: delivery workers, retention, leases and async emits. |
| `StartAddressFamilies` | Sites: family verification and routing refresh. |
| `StartNetworkUsage` / `netusage.Sampler.Start` | Operations: network sampling and alerts. |
| `imageScan.Start` | Operations: image scan jobs and child batches; `imagescan` remains the provider client. |
| `papermetrics.Collector.Start` | Operations: process samples and paper snapshots. |
| `NewSiteHandler`: seven limiter cleanups and `createIdempotency.startCleanup` | Shared HTTP limiter/cache owner; preserve expiry intervals. |
| `NewSiteHandler`: `startExpirySweep`, `startDomainChecks` | Sites: preview expiry and custom-domain verification, with current enable conditions. |
| `NewUserHandler` and registration-created limiter cleanups | Their transport owner; includes the unlock limiter created by `SiteHandler.Register`. |

Shutdown first stops accepting work and drains HTTP requests, then stops/joins workers and flushes owned buffers before DB/file resources close. Give workers a separately controlled context so the signal cannot prematurely stop work needed by draining requests. On a deadline, report which owner did not stop; do not claim a successful join. The setup-only HTTP server keeps its separate lifecycle. External certificate issuers and the deploy-watcher are system services, not goroutines to join here.

Step 5.4 exposes enabled/running, last attempt, last progress, last error and pending count through existing admin/status surfaces. For periodic scans without a queue, report pending as unavailable, not a fabricated zero. Queue workers report actual pending work. Logs name the operation and cause without secrets or recipient lists. A received tick is not progress.

Tests use existing `RunOnce`/sweep functions when possible. Tests that start a worker cancel and join it in cleanup before database cleanup; register cleanups in the correct LIFO order. Phase 1's `testdb.Main` and `testdb.Fresh` remain the fixtures. Do not introduce sleeps, retries or a new testing framework. Current `WaitNotifications` is useful but does not cover all workers; phase 1 did not finish production lifecycle ownership.

## 6. REST, MCP and inventories

Both transports reach the same app services. Keep one `http.ServeMux`. Today `mcp/tools.go:call.do` replays through the gated REST handler supplied by `main`; keep that bridge while workflows move. It preserves scope, origin, rate-limit, idempotency and compatibility gates. Direct service calls are permitted only when their adapter supplies the equivalent principal and preserves those checks. Splitting tools in 4.4 does not require replacing this bridge.

Use family registrar functions in `internal/mcp`: sites/deploys/versions; access/domains; current storage; legacy storage; accounts/connections; Hack events/members/teams; Hack entries/judging/results; notifications; imports/photos; admin/brain; app-only screen tools. Move existing declarations from `tools.go`, `storage_tools.go`, `hack_*tools.go`, `notification_tools.go` and peers into those families without changing tool names or schemas.

One registry entry holds name, schema, annotations, edition/capability visibility, handler and feature ID. `Tools()`, compact discovery, flat listing, `find_tools`, `run_tool` and direct dispatch derive from it. Register hidden/app-only tools too, with their actual visibility; discovery filtering must not authorize direct calls. Preserve Host legacy tools, Hack credential restrictions and existing client-specific listings.

In 5.1, HTTP registrations go through a thin recording wrapper over `ServeMux.Handle/HandleFunc`; retain method/path and middleware order. Record aliases, edition/audience and feature IDs. Generate route and tool indexes from registrations for each Edition, without needing a live database. Do not scrape four source files or infer routes from docs. `openapi.yaml` remains the API contract; inventories check coverage rather than invent request schemas. FEATURES, atlas and public feature records share stable feature IDs; prose and design prototypes remain authored. Offline checks must not depend on chart downloads or publication services.

## 7. Frontend and generated assets

The source Hack app is currently `internal/handler/static/hack/`: `app.js`, `nav.js`, `api.js`, `shell.js`, shared helpers and one module per screen. Keep handwritten HTML/CSS/ES modules and the existing theme. In 2.2, implement `/signin` in this app before deleting `static/hack-app.html`; remove `hack_ui.go`'s classic cookie, `?ui=classic`, `new`/`next` aliases and fallback links. Preserve sign-in, invite returns and supported app routes. This deletion is explicitly approved, not a general screen redesign.

In 2.3, shared screens receive a small platform object through their existing context. Define navigation/current route/hash, invite read/accept state, and form-action binding there. The browser implementation uses history/location and native forms; the MCP implementation uses its frame router and button actions where sandboxed form submission is unavailable. Keep API access through the existing API abstraction, backed by REST or tool calls. `ctx.alive()` guards and cancellation belong in source screens on both platforms, not generated patches.

`internal/mcp/apps/bundlegen/{bundle,patches}.go` used to rewrite imports and exact text for navigation, invites, form events and stale-render guards with a hand-rolled regex transform. 2.3 replaced it with one pinned esbuild version (its Go API, called from `bundlegen`) and a reproducible generation command (`internal/mcp/apps/gentool`); `patches.go` is gone, and the calls it used to patch in now live as ordinary source in the real screens, routed through a small platform adapter (`internal/handler/static/hack/platform.js`, `internal/mcp/apps/frame/platform.js`) for the two sides that differ. The generation dependency is pinned in `go.mod`, resolved from the module proxy like any other dependency, never a floating binary; CI regenerates and checks the diff. The runtime Go build embeds checked-in assets and needs no Node or esbuild (`go list -deps ./cmd/server` carries no esbuild import).

Generated `mcp/apps/hack-{judge,organiser}-bundle.js` and `hack-{judge,organiser}-kit.css` must not be hand-edited. Their sources are shared screens, `kit.css`, and the MCP entry/adapters (`hack-*-view.js`, bridge/stub code as converted). Preserve existing bundle freshness and browser smoke coverage; replace generator-specific assertions when the generator changes. Likewise regenerate OpenAPI JSON, public features HTML and synced plugin copies from their established sources.

Extract embedded assets to `webassets` as part of the transport split, in a mechanical commit with generator/embed paths updated together. Until then, main can pass the current embedded filesystem to `hackhttp`; Hack must not import the old handler package just to read files. Keep the `static/hack` layout within the asset tree so screen ownership remains recognizable.

## 8. One legacy Host storage adapter

`app/legacy.Adapter` owns typed compatibility operations for state, collections/private submissions, declared data/kinds, personal `mine` records, shared `board` records, savers, history/undo and their notification/watch behavior. Its constructor takes Edition, repositories and shared auth/storage/notification services. HTTP handlers and MCP legacy families call this same adapter; do not create a second legacy engine for MCP. Keep old data tables and recovery exports.

The route inventory comes from `hack_legacy_storage.go:parseLegacyStoragePath`: `state`, `collections`, `data`, `savers`, `history`, `allowed-origins`, `allow-anonymous-writes` under both `/v1/sites/{site}` and `/v1/u/{handle}/sites/{site}`; admin collection aliases; `/v1/data-notify/stop`; `/v1/admin/data-watch`. Move `legacy_cutoff.go` policy with this adapter. Preserve site cutoff exemptions, origin checks, CSRF, optimistic versions, pagination and error payloads.

Only Host constructs/mounts the adapter. Hack retains a small transport rejection gate with today's `410 legacy_storage_removed` response on the classified paths, including MCP replay. Removing the handlers must not turn those responses into 404s. Hack recovery exports remain available as today. The gate is not a second storage adapter. Legacy retention and submission-mail loops currently start on both editions; operations/notifications retain that maintenance without mounting the adapter on Hack.

Grandfathered visitor SQL is a compatibility behavior inside the current SQLite API, not one of those old paths. `site_storage_access.go:legacyVisitorSQL` permits a pre-preset full-mode SQLite resource with no access matrix and qualifying visitor read/write. `site_storage_sql.go:storageSQL` uses it without a Hack exclusion. Preserve that existing behavior in `rules/storage` and `app/storage` for both editions; Host's legacy adapter delegates to the same rule when needed. Hack never mounts old saved-data APIs, but this refactor must not revoke an existing Hack SQLite resource's compatibility. New resources use presets/fixed row routes. Setting a preset ends grandfathering as today.

Retirement is an owner decision after identifying dependent sites and agreeing migration/recovery support. Neither low usage nor completion of cleanup authorizes removing APIs, compatibility data or the intentionally retained analytics tables.

## 9. Where to add a feature

- New Hack endpoint: put decoding/encoding and registration in `hackhttp`; implement the operation in events or entries; add pure rules and repository queries only where needed. Use shared sites/storage/accounts services. Never add workflow code back to `SiteHandler`.
- New MCP tool: add it to its capability registrar and the single registry; reuse the existing service/REST path and error mapping. Declare visibility and feature ID once, including direct-call behavior.
- New Host storage capability: use `app/storage`, `rules/storage`, `repository/storage` and disk primitives in `storage`; add HTTP/MCP adapters. Use `app/legacy` only to preserve an old contract.
- New migration: update `db/schema.sql`, add the next free `NNNN-what.sql` and follow `db/migrations/migrations.go`. Freeze old filenames. `Tracked()` runs the frozen legacy list before numbered files; “above” means numbered apply order, not lexical order relative to `zz-` names. Preserve schema checks and the real prior-release upgrade fixture.
- New worker: choose its app owner, inject dependencies, keep construction inert, expose lifecycle/status and register it in main. Reuse a single-pass method in tests; join it before fixture teardown.

Documentation obligations remain those in `CLAUDE.md` until changed by the lead: feature changes update FEATURES, CHANGELOG and PARITY; user-facing routes update OpenAPI and applicable llms/skills; Hack UI changes begin in the atlas. Regenerate derived assets rather than editing copies. Structural moves update the current architecture map when they land; do not claim the target is already implemented. Update INTENT only for an actual product decision, and this target only for an agreed architectural change.

Step 5.3 consolidates the current entry plus four focused guides: Hack domain/state (extend `docs/hack-event-lifecycle.md`), persistence/consistency, frontend/generated assets, and contributor tests/deploys. Link existing operations/configuration material instead of duplicating it. Step 5.2 covers hosted systemd's pre-deploy migration, Compose/install migration ownership and Kubernetes `MIGRATE_ON_START`, plus binary rollback and paired DB/file recovery. This cleanup adds no deployment system.

## 10. Move order and completion invariants

| Step | First small move | Must remain identical | Done when |
|---|---|---|---|
| 3.1 | Add Edition to constructors; convert one route/presentation cluster. | Edition selection, route visibility, branding, scope errors and configured limits. | All policy readers use injected values; global mode/team-key/active-limit setters are gone; Host and Hack fixtures coexist. |
| 3.2 | Extract a small Hack read endpoint and its dependencies into `hackhttp`; retain forwarding registration while needed. | Paths, middleware order, response encoding, event-host boundaries, current transactions. | Hack HTTP/workflows live under `hackhttp` and `app/hack/*`; no Hack package imports `handler`; shared features are invoked through services/interfaces. |
| 4.1 | Move `entryJudgeable` and existing entry-rule cases; then move one queue/assignment workflow with its queries. | Settled incomplete entries, exclusion, deadlines, counts, conflicts, tie/ranking behavior, lock/transaction boundaries. | Entries/judging/results orchestration is in its service, SQL in its repository, pure math/rules independent; JS/MCP use server decisions. |
| 4.2 | Move `acceptOrganiserInvite` with its repository locking reads; then leave/team/stage flows individually. | Event-before-invite/member locks, sorted multi-member locks, error precedence, creator handover, retained scores, queue side effects. | Events/members/teams/content workflows contain no transport dependencies; Hack handlers contain no SQL. |
| 4.3 | Extract runtime-file disk primitives without reordering them; move one storage operation, then deploy/version flows. | Shared mutexes, quota checks, tx boundaries, new-deploy commit-before-promotion, active-version compensation, file author/publish order, all Host legacy behavior. | Sites/storage/legacy services own orchestration; handlers have no SQL/disk mutations; one Host legacy adapter and Hack rejection gate remain. |
| 4.4 | Move one group of existing `Tool` declarations into a registrar; assemble it through the registry. | Names, schemas, annotations, client/edition listings, hidden tools, direct dispatch, credentials and result/error shapes. | Discovery and dispatch use one inventory; no parallel name switch or hand-maintained tool list remains. |

Historical step-4.3 staging retained Host account, site, key, notification and visitor dependencies on `internal/db`; step 5.4 retired that package after all callers moved to services and repositories.

Step 3.2 establishes ownership, not finished purity: move Hack workflows into app packages incrementally, initially retaining cohesive internal helpers. Steps 4.1 and 4.2 separate rules and repository details inside those owners. Never pass `ResponseWriter` into an app service to make a package move compile. If a workflow cannot yet move cleanly, leave a documented forwarding seam in the old transport until its next bounded extraction.

Carry existing tests with each move. Run the affected checks and DB-backed coverage with phase 1 fixtures; add a test only where moved logic lacks coverage. Do not multiply every REST assertion into a new service and MCP suite. Each commit keeps a working registration path; do not leave half-wired services awaiting the next deployment. No schema/data rewrite is needed merely to change package ownership. Delete transitional imports and forwarders as their last caller moves.

By 5.4, remove remaining `db` forwarders and metric/helper SQL exceptions as well as unowned loops. The final import table then applies without transition allowances. This completion work must not be mistaken for permission to enlarge the scope of earlier steps.

## 11. Owner questions and observed differences

No owner answer is needed to start. Legacy API retirement remains explicitly undecided. Any later proposal to stop grandfathered visitor SQL on Hack needs its own owner decision; the behavior-preserving choice here is to keep it.

Source details qualify the plan's shorthand. Phase 1 isolated test processes and added targeted worker joining, but `NewSiteHandler`, `NewAPIMetrics` and several main-started loops still lack complete lifecycle ownership. The Go/SQL judgeability predicate is duplicated exactly; JavaScript repeats related status interpretation and already consumes some server answers. Numbered migrations are appended after a frozen legacy list, not sorted above legacy filenames lexically. New deploys promote after commit, but active-version changes promote before commit with compensation. These are accounted for above, not reasons to restart phase 1.
