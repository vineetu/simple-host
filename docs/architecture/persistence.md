# Persistence and consistency

This is the current storage map for the shared binary. Simple Host and hosted
Simple Hack use PostgreSQL, but run as separate instances with separate databases
and data roots. The edition controls limits and API availability; it does not
select a second persistence engine. See [deployment profiles](../operations/deployment-profiles.md)
for instance layouts and migration owners, and [Hack domain](hack-domain.md) for
Hack transitions and transaction boundaries.

## Stores and ownership

| Store | Contents | Implementation and workflow owner |
|---|---|---|
| PostgreSQL | Accounts, sites, versions, policies, KV, legacy data, Hack records, outboxes, metrics | Area packages in `internal/repository/*`; application services orchestrate workflows |
| Per-resource SQLite | A site's tables, indexes, views and triggers | `internal/storage/runtime_*.go` implements mechanics; `internal/app/storage` owns policy, quotas and mutations |
| Deploy files | Retained version trees and the live serving copy | `internal/storage/disk.go`, called by `internal/app/sites.Service` |
| Runtime files | Uploaded bucket objects and SQLite database files | `internal/storage` mechanics and `internal/app/storage.Service`; image scanning also removes flagged bytes |
| KV | Namespaced keys and JSONB values | PostgreSQL `site_storage_kv`, through `internal/repository/storage` and `internal/app/storage` |

PostgreSQL remains the main database in both editions. SQLite is local runtime
storage for a website, not a replacement for PostgreSQL. Enterprise replicas/S3
are outside this local SQLite contract; see
[site storage primitives](../designs/site-storage-primitives.md).

SQL belongs to the area repositories; the retired `internal/db` compatibility
package has no remaining callers. Startup schema verification lives in
`internal/repository/operations/schemacheck.go`. See the [package contract](target.md).

The pinned CGO-free `ncruces/go-sqlite3` driver uses compiled Go translated from
its SQLite WebAssembly build. It needs neither runtime code generation nor
writable-executable memory, so it fits the hosted systemd restrictions.
Evidence: `go.mod`, the pinned dependency's `wrap.go` (`sqlite3_wasm.New`), and
`internal/storage/runtime_sqlite.go`.

## Identities, resources and tables

`db/schema.sql` is the canonical table/foreign-key definition. Some definitions
are extended by later `ALTER TABLE` statements in that same file; read those too.

| Records | Relationship and purpose |
|---|---|
| `users` | Owners, visitors and admin share identities; handle and display name, event-account flag, suspension and sign-in alert preference live here |
| `api_keys` | Several keys per user; only hex SHA-256 hashes are stored, with full/deploy scope and optional expiry |
| `handle_aliases`, `legacy_hostnames` | Old handles and retired per-name hosts participate in the shared namespace |
| `sites` | Belongs to a user; name, active version number, legacy state JSONB/version, domain/status, listing visibility and allowed origins |
| `versions` | Upload records belonging to a site, with version number, disk path, status, archive hash and author |
| `site_name_aliases` | A renamed site's old names; a new site taking an old name wins over the alias |
| `site_viewers` | Named viewer emails keyed to the immutable site; distinct from listing visibility and storage access policy |
| `address_families`, `family_cert_requests` | Claimed family addresses and certificate request accounting |
| `domain_cert_requests`, `event_domains` | Custom-domain issuer daily cap and hackathon hostname bindings |
| `site_storage_resources` | Site/name identifies a KV, SQLite or files resource, its legacy policy fields, access matrix, preset and passcode inheritance |
| `site_storage_tables` | SQLite table-specific matrices; absent overrides use the resource default |
| `site_storage_kv` | Resource/key identifies a JSONB value with writer and timestamps |
| `site_storage_files` | Resource/path identifies file authorship metadata; bytes live on disk |
| `collection_items`, `collection_settings` | Legacy lists, boards, Personal records and private submissions; kind, version, private flag and submitter metadata |
| `data_history`, `idempotency_keys` | Saved-data history for 30-day undo, and legacy mutation replay protection |
| `data_watch`, `site_savers` | Legacy visitor-write counters, permitted savers and blocks |
| `email_changes`, `email_change_undos`, `signin_alerts_sent` | Sign-in email changes, undo and alert records |
| `auth_tokens` | Expiring email codes, purpose-bound and site-bound for visitor flows; expired rows are purged |
| `oauth_identities`, `oauth_states` | Google/external sign-in identity links and pending OAuth state |
| `visitor_sessions`, `visitor_establish_tokens`, `site_visitor_signins` | Site-scoped sessions, one-time hand-off and sign-in evidence for owner email lookup |
| `oauth_clients`, `oauth_grants`, `oauth_codes`, `oauth_tokens` | Connector clients, grants, codes and hashed bearer credentials |
| `sso_passes`, `sso_consents`, `sso_hops`, `sso_signouts` | Apex/site sign-in hand-off and sign-out state |
| `instance_config` | Persisted setup answers |
| `site_notifications`, `site_events`, `site_event_deliveries` | Subscriptions, ordered event log and per-subscription delivery work |

Hack's table groups are `events`, `event_members`, `event_teams`, `event_entries`,
`event_team_keys`, `event_used_names`, content/announcement/delivery records,
registration tracks/applications, team requests, organiser invites, notices,
entry reminders, rubric criteria, assignments, conflicts, scores, votes and
results. `events.account_id` identifies the holding account whose sites host
team and event websites; membership is a separate relationship, not site
ownership transferred to every participant. SQL lives in
`repository/hackevents`, `repository/hackteams` and `repository/hackentries`. See [Hack domain](hack-domain.md).

Analytics and operational records also belong to the database:

- `site_view_hourly`, `site_visitor_hourly`, `site_geo_daily`, `site_page_daily`,
  `site_referrer_daily`, `analytics_ingest_state`: log-derived traffic and cursor.
- `site_view_daily`, `site_visitor_daily`: retained legacy tables, never newly
  written and never to be dropped; pruning uses analytics retention (default
  400 days). See `internal/analytics/ingest.go` retention cleanup.
- `api_request_daily`, `api_ip_daily`: API counts and caller addresses truncated
  to IPv4 /24 or IPv6 /48; default retention is 30 days. Self-call and growth
  aggregates use `api_self_daily` and `api_growth_daily`.
- `ip_country_ranges`: local reference data for country counts. Visitor IPs
  never go to a third-party lookup service.
- `traffic_daily`, `site_traffic_daily`, `net_usage_daily`, `net_counter_state`:
  transfer accounting; `owner_visitor_marks`: owner-traffic classification.
- `paper_*` tables: measurement records; `image_scan_hashes`, `image_scan_queue`,
  `image_scan_matches`: scan cache, queued work and findings.

Evidence: `db/schema.sql`; `internal/app/operations/apimetrics.go` (`pruneOld`);
`internal/analytics/ingest.go`; `internal/repository/sites/sites.go`
(`DropOldSiteName`); `internal/repository/storage/access.go` (`LoadTables`).

## Disk layout and lifecycle

`DATA_DIR` is the configurable root; hosted Host uses `/srv/simple-host/sites`.
The important paths below it are:

```text
by-id/<user-id>/<site>/
  v<n>/                  retained upload
  current/               live serving copy (a directory, not a symlink)
  runtime/sqlite/         resource database files
  runtime/files/          bucket objects
  domain-redirect        custom-address redirect marker
  suspended              suspension marker
handles/<handle>         symlink to ../by-id/<user-id>
domains/<domain>         symlink to ../by-id/<user-id>/<site>
```

`DiskStorage.WriteFiles` builds `.v<n>.tmp`, then renames it to `v<n>`.
`UpdateCurrent` copies that tree to `.current.tmp` while the previous version
continues serving, removes the old `current`, then renames the copy into place.
There is a short remove/rename gap; this is not an atomic symlink exchange.
`EnsureHandleLink` and `BindDomain` create the address symlinks.
Evidence: `internal/storage/disk.go`, functions named above.

Rename, trash and restore move the site's directory, so runtime resources travel
with it. Permanent deletion removes it. Resource declarations and KV are keyed
by site ID and follow PostgreSQL lifecycle rules rather than path names.
Evidence: `internal/storage/disk.go` (`RenameSite`, `DeleteSite`),
`internal/storage/trash.go`, `internal/app/sites/deleted.go`.

The suspension marker mirrors site or owner suspension and is reconciled at
boot. Go checks it when serving; nginx and Caddy check it when reading disk and
route to `/internal/suspended`. List on my profile is stored in sites.visibility (`public` = on, `unlisted` = off, default off).
It only controls the profile page. Legacy unlisted marker files are inert and removed once
when disk storage opens, including in Recently deleted; missing files are tolerated and the
count is logged. Every open site can be found by search engines. Evidence:
`internal/storage/disk.go`, `internal/handler/seo.go`.

## Publication and rollback

`internal/app/sites/service.go` owns the ordering through `Service.Create`,
`Service.Update`, `activateVersion` and `Rollback`:

1. Take account/site locks and perform the transaction-time gates. Updates lock
   the site row, and the user row where account file caps need serialization.
2. Allocate a version number and insert its `versions` row in the transaction.
3. Write the complete version tree to disk. It is not yet served by `current`.
4. For `publish=false`, mark the version ready and commit; do not replace
   `current`. For a published deploy, mark it active, update
   `sites.active_version`, and commit before promoting the serving copy.
5. Replace `current`, ensure address links/certificate requests, then prune and
   notify where the workflow calls for them.

First creation publishes v1. A staged upload can later be activated through the
rollback/activation operation. An `uploading` version is ineligible for that
operation. Uploading files and committing PostgreSQL are separate operations:
a failed transaction can leave disk files, and a failed promotion after commit
can leave metadata ahead of the serving copy. These are not one ACID transaction.

Rollback takes the site mutex, checks eligibility and suspension, locks the site
row, then rechecks the transaction gate. It updates the active-version fields,
rebuilds `current` from the selected retained tree, and commits. If commit fails,
it attempts to put the previous serving tree back and logs a failed compensation.
It does not restore KV, SQLite, raw files, legacy state or history. Those stores
live outside deployment versions. A rollback to the current version is a no-op.

The deploy-version writer is `internal/storage/disk.go`, used through site
workflows. It is not the only writer beneath a site directory: runtime resources
are written by `app/storage` using `internal/storage` helpers; the image-scan
worker can remove flagged deploy/runtime images (`internal/app/operations/imagescan.go`).
Do not treat an upload mutex as a global filesystem lock across processes.

## Lock scope and quota accounting

| Scope | Mechanism and evidence |
|---|---|
| Account/site within one process | `internal/handler/site.go:lockSite` uses `uploadLocks` keyed by user/name; `storagecaps.go:lockAccountFiles` uses the empty site name for the account cap lock |
| Runtime resource writes | `internal/app/storage/service.go:writeLock` uses the injected shared mutex map, bounded wait/write contexts and a resource recheck under lock |
| PostgreSQL site/user mutation | `repository/sites/sites.go:LockSiteForUpdate`, `LockUserHandle`, `LockUserForFileCaps` use row locks within the caller's transaction |
| Domain/namespace claims | Transaction advisory locks in `internal/repository/sites/domains.go`, `internal/repository/sites/store_families.go` and `repository/accounts/accounts.go:LockPlatformName` serialize competing claims |
| SQLite capacity | `internal/storage/sqlite_gate.go:SQLiteGate` bounds concurrent work; SQLite transactions/authorizers control individual statements |
| Migrations and delivery | Database locks/leases described below; these work across processes |

Storage policy checks and quota checks belong to `internal/app/storage`, not the
HTTP/MCP caller. Presets store four actions and may have SQLite table overrides;
owner tools bypass the page matrix, while owner visitor sessions receive only
storage-data authority. See `access.go`, `matrix.go` and `rows.go` in that package.

`usage.go:MeasureSiteStorage` supplies both owner reports and mutation budgets:

| Counter | Charged bytes |
|---|---|
| KV | UTF-8 key bytes plus PostgreSQL-normalized JSONB value text |
| SQLite | Main database file allocation after checkpoint; not logical row payload |
| Files | Raw object lengths, also counted per bucket for object limits |

Host defaults to a 10,000,000-byte KV+SQLite pool and a separate
10,000,000-byte files pool. Hack defaults to one 1,000,000-byte pool covering all
three. Limits are configurable; `GetStorageUsage` reports counters, actual
limits and remaining bytes. WAL, deployment versions and legacy saved data are
outside these runtime pools. The original shared-10-MB-pool description is
superseded by `usage.go:BudgetUsed`, `FilesLimitBytes` and edition configuration.

KV checks normalized size before writing. Files measure replacement growth under
the site lock, stage bytes and publish with rename/link, recording authorship in
PostgreSQL; these steps are not a cross-store transaction. SQLite writes enforce
page budgets and use transactions; fixed row writes commit before checkpointing,
so a checkpoint error is logged rather than turning a committed write into a
failed response. Runtime writes invalidate disk-usage caches.
Evidence: `app/storage/kv.go`, `files.go:PutFile`, `row_write.go:finishTableWrite`,
`internal/storage/runtime_sql_cap.go`, `runtime_row_mechanics.go`.

Deployment site/account file caps are separate: `app/sites/filecaps.go`
(`checkFileCaps`) projects the live copy, incoming version and retained versions
before writing; `internal/storage/footprint.go` measures disk footprint.
Do not substitute runtime storage usage for that deployment calculation.

## Outbox and delivery leases

`site_notifications` stores webhook/MCP Events subscriptions; MCP subscriptions
are grant-scoped. Signing secrets and custom header tokens are AES-256-GCM sealed
under `PASSCODE_ENC_KEY`. Associated data is `notification:<id>` for signing
secrets and `notification:<id>:header` for header tokens. Values are opened only
when needed for signing, sending or refresh comparison. New subscriptions need
the encryption key; startup can seal old plaintext records as a data-only sweep.
Evidence: `internal/handler/notify_secrets.go`, `passcode.go`,
`internal/repository/notifications/notifications.go:SaveNotification`, `SealNotificationSecrets`.

`emitSiteEvent` locks the site row, appends `site_events` with increasing `seq`,
and inserts matching `site_event_deliveries` in one PostgreSQL transaction.
Subscription creation/replay uses the same site lock. Events are append-only
until retention cleanup. The outbox insert is separate from the originating
storage mutation: services release write locks before emitting notifications.
There is no atomic PostgreSQL/SQLite/file/outbox commit.

Delivery runs through `internal/app/notifications/delivery.go` (`Deliver`,
`ProcessDelivery`), started by `internal/handler/notify.go:StartNotifications`; SQL is in `internal/repository/notifications/notifications.go`:

- `ClaimNotificationDelivery` expires old leases, counting them as timeout
  attempts; selects due eligible work with `FOR UPDATE OF d SKIP LOCKED`; and
  increments attempts while leasing it. The lease is send timeout plus 30 seconds.
- Processing rechecks subscription state/expiry, site availability, ownership or
  Hack membership, and lease ownership before sending. Work has the lease deadline.
- Owner concurrency or URL throttling defers by two seconds and reverses the
  attempt count, only while the claim still holds its unexpired lease.
- Retry delays are 30 seconds, two minutes, five minutes, then seven minutes.
  Attempts/history settings cap retries. HTTP 410/413 and refused addresses are
  terminal; unavailable sites are skipped. Failure windows can suspend subscriptions.
- `FinishNotificationDelivery` compares the lease token before recording a result,
  preventing a stale worker overwriting a newer claim. Network delivery can still
  repeat after an uncertain response; the event ID supports receiver deduplication.

Hack also has persisted mail work (announcements, receipts, notices and reminders),
not a second site-webhook engine. See [Hack domain](hack-domain.md) and
`products/hack/repository/hackevents/notice_delivery.go` for its claims and the
person/kind transaction advisory lock used for notice batches.

## Schema installation and upgrades

Fresh databases use `db/schema.sql`. `migrations.Init` creates it only when the
`users` table is absent; `Apply` then runs pending tracked files even on that fresh
schema. Thus every new migration must be idempotent against the canonical schema.
For upgrades, apply schema changes before starting a binary that requires them;
startup's required-column check refuses an incompatible schema.

`db/migrations/migrations.go` defines the exact rules:

- Edit `schema.sql` and add plain SQL named `NNNN-name.sql` using the next free
  number. Keep it additive, defaulted and safe for the old binary; no long table
  rewrites or `CREATE INDEX CONCURRENTLY` inside the migration transaction.
- `Baseline` is frozen, never executed or tracked. `Legacy` is the frozen ordered
  list of older tracked names. Do not rename old files or extend either list.
- Apply frozen legacy files first, then numbered files numerically, not a global
  lexical sort. A pending number at or below the newest applied number is refused,
  including conflicts recorded by a newer build. Renumber the new migration.
- `locked` holds a connection-scoped PostgreSQL advisory lock across each
  operation. Each applied file and its `schema_migrations` row share a transaction;
  the row is inserted before SQL so older files with their own COMMIT retain it.
- `migrate -status` shows apply order; `migrate -mark` records manually applied
  tracked SQL. `MIGRATE_ON_START=true` runs initialization and pending migrations
  before listening; it defaults off. Secret sealing is separate from schema work.

See [deployment profiles](../operations/deployment-profiles.md) for who runs each
step under systemd, Compose and Kubernetes; do not infer it from the edition.

## Backup and recovery boundary

Back up PostgreSQL and the matching data directory together: rows alone lack
SQLite and uploaded/deployed bytes; files alone lack identity, policy, KV, domain
and version metadata. Preserve encryption configuration through the operator's
secret backup process too, or sealed passcodes/notification secrets are unusable.
A binary rollback does not roll back schema or user data.

Owner site/account exports include runtime files, resource declarations, KV and
file authorship alongside legacy recovery data. SQLite is checkpointed before
exporting main files; WAL/SHM sidecars are excluded. These exports are not a full
instance database backup or all retained deployment versions. Evidence:
`internal/app/sites/export.go:writeSiteArchive`, `writeStorageArchive`,
`internal/storage/export_sqlite.go`; see [deployment profiles](../operations/deployment-profiles.md).

## Legacy adapter and design references

`internal/app/legacy.Adapter` is the shared compatibility workflow layer for
state, collections/private submissions, declared kinds/data, Personal `mine`,
shared `board`, savers, history/undo, watch and notification behavior. Its SQL is
in `internal/repository/legacy`. HTTP and
MCP reuse this adapter; there is no separate MCP legacy persistence engine.

Host keeps these APIs because live sites can depend on their versions, origin
checks, privacy and undo behavior. Cutoff eligibility is centralized in
`app/legacy/cutoff.go`. Hack rejects the classified old site/handle/admin paths
with `410 legacy_storage_removed` through `products/hack/hackhttp/legacy_storage.go`,
including connector REST replay, while preserving historical records in owner
recovery exports. The rejection gate does not delete data.

Grandfathered visitor raw SQL is a separate compatibility path in the current
SQLite API, including Hack: an existing qualifying full-mode resource without a
matrix can retain it until its policy changes. New matrices use fixed row routes.
See `internal/rules/storage` and `app/storage/legacy_sql.go`.

Retirement requires an inventory of dependent sites, proven export/restore,
owner-controlled migration and recovery support, parity/client compatibility
evidence, and a separately recorded owner decision. Low traffic or completion
of cleanup is not sufficient. There is no removal date or automatic data rewrite.

Existing designs remain useful with these boundaries:

- [State storage](../designs/state-storage.md): inline `sites.state` JSONB and its
  optimistic version are current; segmentation/blob tiers are proposals, not
  implemented stores. Dated production sizes are historical observations.
- [Site storage primitives](../designs/site-storage-primitives.md): resource
  isolation, runtime layout, export and size evidence remain useful. The early
  resource-wide policy/API sketches and shared Host pool are superseded by current
  matrices, fixed row routes and separate Host file budget described above.
- [Storage access presets](../designs/storage-access-presets.md): matrices,
  per-table policy and compatibility explain the current model. Early statements
  postponing Hack presets are superseded: Hack has presets too. The later
  `checked` add policy/comments preset extends the original four-rule matrix;
  `rules/storage` and `app/storage` are the current enforcement locations.
