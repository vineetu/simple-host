# Site storage primitives

Status: Add/own, order history and the separate file allowance shipped on hosted Simple Host after both security reviews (2026-10-06). Live verification covered two signed-in customers, the owner, 150-photo public/signed-in galleries, NFC uploads and the 10 MB file pool; all throwaway accounts were deleted. Earlier primitives shipped on hosted Simple Host and Simple Hack and available for single-instance installations (owner decision 2026-10-02; production verified 2026-10-02). The public [visual guide](https://storage-api-plan.vineetu.simple-host.app/) explains the API; this file records its implementation contract. No existing saved-data route or policy is removed.

## Scope

Simple Host hosted and single-instance small-box sites get exactly three additive resources: JSON key–value namespaces, SQLite databases, and raw-file buckets. The owner or an owner-authorized connector names and configures each resource. Site authors choose keys, SQL tables, and paths; the platform adds no RSVP/order/guestbook model or server-side code. Hosted Simple Hack team sites and custom event websites use the same local SQLite engine and resource policies, with current team or organiser scope. The separate Enterprise replica/S3 stack remains deferred. The older state, Page info, Submissions, Personal, and Shared board routes keep their existing behavior.

SQLite uses ncruces/go-sqlite3's CGO-free compiled-Go engine. The initial interpreter-based implementation was replaced without changing the SQL API, database files, or the Enterprise replica/S3 deferral. Hosted systemd services retain their executable-memory restriction.

A resource name is unique across the three kinds and cannot change kind.
Creation defaults to `read=owner`, `write=owner`, `write_mode=full`,
`site_passcode=inherit`. `inherit` requires the existing site passcode unlock;
`off` bypasses it. Owner credentials bypass the passcode but remain scoped.
Anonymous writes require an explicit `write=anyone`. Legacy state and
collections keep their existing behavior.

Add/own and order history are hosted / small box only, not Enterprise or Simple Hack.
Simple Hack retains full-mode resource-wide policies.

`write_mode` is `full` (the default, preserving existing resources) or `add`.
`read` is `anyone`, `signed-in`, `owner`, or `own`; `write` remains `anyone`,
`signed-in`, or `owner`. `own` requires a signed-in visitor, including for
inserts, and combines with `write_mode=add` when visitors may write. An owner
credential retains full control. With `add`, visitors create new KV keys and
file paths only (409 `key_exists` / `file_exists` on a collision), and cannot
delete them (403 `add_only`). `own` filters individual reads and lists by the
stable signed-in visitor ID stored by the server; old unattributed objects stay
owner-only under own reads. Anonymous own access returns 401 `sign_in_required`.

On SQLite databases in add or own mode, raw `/query` and `/execute` are
owner-only (403 `fixed_routes_required` for visitors). Pages use
`POST /sqlite/{name}/tables/{table}/rows` with a JSON object of column values,
and `GET /sqlite/{name}/tables/{table}/rows?order=&desc=1&limit=&after=`.
The server validates tables and columns against the real schema, quotes
identifiers, binds values, and stamps `visitor_id` on visitor inserts. A supplied
`visitor_id` is ignored and replaced with the session identity. If the table has
`created_at`, the server stamps it with UTC time, ignoring a client value.
For add-only inserts in own-read databases, declared SQLite foreign keys check
that the parent exists and has the same visitor ID or a NULL/empty owner
identity, in the insert transaction; missing and other visitors’ parents both
return 404 `invalid_reference`. Each reference uses an indexed parent lookup. Optional
foreign keys must be all NULL; partial composite NULLs are refused. Read orders and history separately. Own databases require `visitor_id TEXT` in every table;
new tables created through the owner schema route receive it and an index.
Choosing own reads on an existing database without it or with any earlier
non-own rows is refused. Schema changes
that remove it roll back. Own rows use one query with `WHERE visitor_id = ?`.
Reads return `{columns,rows,next_after}`; the opaque cursor includes the order
value and rowid, so equal values paginate without skipping. Defaults are rowid
ascending and 100 rows (maximum 500); fixed routes need a normal rowid table.
Pass the returned cursor with the same order and direction. Owner SQL remains
parameterized and bounded as before. Add-only visitor inserts also refuse trigger-driven writes. No visitor SQL, temporary views, or
per-person database objects are introduced.

Simple Host KV and SQLite share **1,000,000 bytes per website**
(`SITE_STORAGE_MAX_BYTES`). Files have a separate **10,000,000 bytes per website**
(`SITE_STORAGE_FILES_MAX_BYTES`, decimal 10 MB). The usage response keeps
`used_bytes`, `limit_bytes`, `remaining_bytes` for KV/SQLite and adds
`files_used_bytes`, `files_limit_bytes`, `files_remaining_bytes`, retaining the
three-kind `breakdown`. The existing single-upload cap remains 1,000,000 bytes
(`SITE_STORAGE_FILE_MAX_BYTES`); shrink photos to about 1600 px WebP before
uploading. SQLite WAL, deployed website files and legacy saved data retain their
separate accounting. Simple Hack keeps its existing 1,000,000-byte pool across
all three kinds and its per-upload cap; on Hack the first three usage fields
still describe that pool.

On Simple Hack, a current team key or selected-team connector manages only that team site through the same `/v1/sites/{team}/storage` routes; current membership and the existing deadline, archive and take-down write gates still apply. A current organiser manages a custom event website through `/v1/hack/events/{slug}/website/storage/{rest...}` or `hack_event_storage_*` tools. Its visitor page uses `/v1/sites/{slug}/storage` on the event host, which resolves the slug to the immutable event-ID site. Other event-host `/v1/` routes stay refused. Team and organiser file links recheck current membership at download.

## REST contract

All site storage routes begin `/v1/sites/{sitename}/storage`. The browser uses them on its own site origin so its visitor session and unlock cookie stay on that host. Owner API keys and connectors use the trusted apex. The handler resolves the site from the request host/owner scope before looking up the resource; names on another site's host are unavailable. Cross-origin browser requests receive no CORS grant. Visitor writes require the same-site `Origin` and `X-SH-CSRF: 1` convention used by the hosted helper; scripts using an owner key may omit `Origin`.

| Method/path suffix | Body | Result | Permission |
|---|---|---|---|
| `GET /resources` | — | `{resources:[{name,kind,read,write,write_mode,site_passcode}]}` | owner |
| `PUT /resources/{name}` | `{kind,read,write,write_mode,site_passcode}` | resource JSON; 201 first create, 200 update | owner |
| `DELETE /resources/{name}` | — | `{deleted:true}` | owner; explicit permanent resource removal |
| `GET /kv/{name}/keys?prefix=&after=&limit=` | — | `{items:[{key,value}],next_after}` | resource read |
| `GET /kv/{name}/keys/{key}` | — | `{key,value}` | resource read |
| `PUT /kv/{name}/keys/{key}` | `{value:<JSON>}` | `{key,value}` | resource write |
| `DELETE /kv/{name}/keys/{key}` | — | `{deleted:true}` | resource write |
| `POST /sqlite/{name}/tables/{table}/rows` | column-value object | `{changes,last_insert_id}` | resource write; add-only visitors use this fixed INSERT |
| `GET /sqlite/{name}/tables/{table}/rows?order=&desc=&limit=&after=` | — | `{columns,rows,next_after}` | resource read; own filters server-side |
| `POST /sqlite/{name}/query` | `{sql,params:[JSON scalars]}` | `{columns:[...],rows:[[...]]}` | resource read |
| `POST /sqlite/{name}/execute` | same | `{changes,last_insert_id}` | resource write; DML only, read access additionally required if SQL reads existing rows |
| `POST /sqlite/{name}/schema` | same | `{changes}` | owner; DDL only |
| `GET /files/{name}/objects?prefix=&after=&limit=` | — | `{items:[{path,bytes,content_type}],next_after}` | resource read |
| `GET /files/{name}/objects/{path...}` | — | raw bytes | resource read |
| `PUT /files/{name}/objects/{path...}` | raw bytes with `Content-Type` | `{path,bytes,content_type}` | resource write |
| `DELETE /files/{name}/objects/{path...}` | — | `{deleted:true}` | resource write |
| `POST /files/{name}/download-link` | `{path}` | `{url,expires_at}` | owner; scoped ten-minute bearer link for connector file reads |

JSON operations accept one value/statement per request, bound positional SQL parameters, and reject trailing SQL statements. Each SQLite statement is atomic. A future multi-statement transaction route requires its own explicit contract; one may not simulate it by concatenating untrusted SQL. Query rows are bounded and return typed JSON scalar values; BLOBs are encoded explicitly, not confused with text. SQLite's connection authorizer, not string matching, denies `ATTACH`, `DETACH`, extension/file functions, schema changes from `/execute`, and writes from `/query`, including triggers and PRAGMAs. The database filename is derived from the resolved site ID and validated resource name, never a supplied path. No SQL route can access another resource, the host filesystem, or files-bucket contents. Full-mode SQL keeps its existing database-wide policies; add/own visitors use only the fixed row routes described above.

File GET validates stored MIME from bytes. PNG/JPEG/WebP/GIF may render inline; HTML, SVG, scripts, unknown types and downloads use `Content-Disposition: attachment`, `X-Content-Type-Options: nosniff`, and no execution privilege. Upload paths are normalized relative paths with no symlinks, traversal or reserved runtime names. Authenticated/download-link reads recheck the current site/resource/owner state before returning bytes. The site can display an allowed image at its own origin; file storage is not part of the deployed website version.

Errors use the existing JSON `{error,code}` shape: `invalid_*` 400, `sign_in_required` 401, `forbidden` or `site_locked` 403, `resource_not_found` 404, `resource_kind_conflict` 409, and `site_full` 507. Missing/invalid keys and paths return 404/400 without revealing another site's resource. Allowances and usage fields are described above. KV counts UTF-8 key bytes plus normalized JSONB
value text, SQLite counts checkpointed main-database bytes, and files count raw
object bytes. Growth checks refuse `site_full` before replacing existing data.
No silent response truncation. File refusals suggest about 1600 px WebP photos.

## Storage and lifecycle

Resource declarations, KV writer IDs and file writer IDs live in additive PostgreSQL tables, keyed by the immutable site ID. SQLite and raw files live under `DATA_DIR/by-id/<owner>/<site>/runtime/`, outside `vN` and `current`; a publish or version rollback cannot replace them. Existing site rename/trash/restore/account deletion moves or removes this whole directory, while the PostgreSQL foreign keys preserve declarations/KV until permanent deletion. SQLite WAL is checkpointed before exports, and export includes the database, raw files and a declaration manifest; owner/account exports and the backup checker include them too. Runtime writes invalidate disk-usage cache. A new site restore does not resurrect data after permanent deletion. Enterprise currently relies on S3 and replicas, so local SQLite portability/locking is explicitly unsupported there pending a separate design.

## Size evidence, not a guarantee

Disposable benchmark recorded in `docs/history/site-storage-benchmark-2026-10-02.json` used 10,000 deterministic JSON records (1,836,932 logical payload bytes). A current-style PostgreSQL JSONB collection table plus indexes occupied 4,210,688 bytes; a candidate per-site SQLite text table plus index occupied 2,375,680 bytes after checkpoint, with 2,406,112 transient WAL bytes during insert. Ten records used 65,536 bytes of PostgreSQL relation/index space versus a 12,288-byte SQLite file and 24,752 transient WAL bytes. Two hundred synthetic 100 KiB already-compressed-size photo objects used 20,480,000 logical and 20,492,288 physical bytes as raw files. The schemas are representative, not identical; PostgreSQL remains in service, and WAL, per-file allocation, dual data during migration, replicas and backups add space. Photos dominate this example, so no blanket saving is promised.

## Compatibility and deprecation path

The old APIs remain supported without a removal date or automatic migration. Owner docs, helper docs and connector guidance will prefer these three primitives for new sites and label the old state/collection routes as compatibility APIs. A migration guide maps shared JSON state/Page info/Shared boards to KV or SQLite, and public file URLs to buckets. Submissions and Personal cannot be mechanically mapped to one resource-wide policy without losing per-person visibility; their existing rows and privacy rules remain intact. State atomic operations, ETags, history/undo, notifications and collection semantics also require explicit application redesign. An owner must choose new schema, policy and copy strategy before a migration; the platform will not auto-publicize or remove old private data. Removal is considered only after usage inventory, export/restore proof, owner-controlled migration tools, parity and real client compatibility evidence, and a separately recorded decision. No sunset date is set.

## Shop with orders

Create an `orders` SQLite resource with `storage_set_resource(site,"orders",body)`
(or owner PUT):

```json
{"kind":"sqlite","read":"own","write":"signed-in","write_mode":"add","site_passcode":"inherit"}
```

Through owner `storage_sql_schema`, create these tables in two calls in that
same database:

```sql
CREATE TABLE orders (id INTEGER PRIMARY KEY, item TEXT NOT NULL,
                     quantity INTEGER NOT NULL, status TEXT DEFAULT 'placed',
                     created_at TEXT)
```

```sql
CREATE TABLE order_changes (
  id INTEGER PRIMARY KEY,
  order_id INTEGER NOT NULL REFERENCES orders(id),
  kind TEXT NOT NULL CHECK (kind IN ('change','note','cancel_request')),
  details TEXT NOT NULL,
  created_at TEXT
)
```

Both tables inherit add-only writes and own reads from the resource. The server
adds indexed `visitor_id TEXT`, stamps `created_at` on visitor inserts, and
ignores client-sent identity and timestamps. A declared foreign key checks that
the referenced order exists and belongs to the same customer, in the insert
transaction (404 for both missing and another customer's orders).

Customers never rewrite or delete a placed order. They customise it by adding
a change, a note, a cancellation request or a requested new quantity to
`order_changes`; the original order and change history remain. The page shows
“My orders”, each current status and its history, using two reads and matching
`order_changes.order_id` to `orders.id` in the page. There is no `include=` option.

```html
<script>window.SH_CONFIG = {site: 'pantry'};</script>
<script src="https://simple-host.app/auth.js" defer></script>
```

```js
await SH.requireSignIn();
const db = SH.storage.sqlite('orders');
const orders = db.table('orders');
const changes = db.table('order_changes');
const receipt = await orders.add({item: 'Chai spice', quantity: 2});
await changes.add({order_id: receipt.last_insert_id, kind: 'change',
                   details: 'Please change the quantity to 3'});
const mine = await orders.list({order: 'id', desc: 1, limit: 50});
const history = await changes.list({order: 'id', limit: 100});
// Both results contain only this customer's rows. Match history by order_id.
// Follow each next_after cursor with the same order and direction.
```

The owner sees all orders and history through `storage_sql_query`. The owner's
view applies or acknowledges requests and updates the order's status or stage
with `storage_sql_execute`, for example `UPDATE orders SET status=? WHERE id=?`
with params `["packed",17]`. Keep the history when handling a request. Use the
trusted owner dashboard/connector; never put an owner key in a page.

Create a `photos` file bucket with signed-in add-only writes. Choose
`read:"anyone"` for photos that visitors may view, or `read:"owner"` for photos
only the shop owner reads:

```json
{"kind":"files","read":"anyone","write":"signed-in","write_mode":"add","site_passcode":"inherit"}
```

```js
await SH.requireSignIn();
// preparedPhoto is a resized WebP Blob, below the single-upload cap.
await SH.storage.files('photos').put(crypto.randomUUID() + '.webp', preparedPhoto);
```

A page may instead call relative REST routes with `credentials:'same-origin'`
and `X-SH-CSRF: 1` for POST/PUT. The server takes identity from the site session.
Preserve the form on a refusal; never widen access to make a save work.

## Intentional edition difference and checks

The order-history design is hosted / small box only, not Enterprise or Simple
Hack. Both repositories keep byte-identical PARITY.md copies. Their parity
checks, docs-sync and CI run the shared comparator's regression check. During
a branch rollout, only the explicitly marked Site storage primitives row may
differ; unrelated documentation drift and growing gaps still fail. The weekly
Signal watcher reads both main branches and uses the same comparison. Run it
with SH_PARITY_CHECK_ONLY=1 to verify without sending or changing live state.
The watcher regression in make check uses local fixtures and sends no messages.


Storage security (2026-10-06, both reviews completed; hosted deployment verified):
Visitor inserts use `INSERT OR ABORT`; visitors cannot supply any primary-key or
rowid value. Use `id INTEGER PRIMARY KEY` so the server assigns IDs. The inserted
ID comes from `RETURNING`, including for reference checks, never connection-global
insert state. `foreign_keys=ON` is explicit on every SQLite connection. Declare
foreign keys for linked history; undeclared application links are not checked.
Own/add references may target this visitor's rows or owner-created catalog rows
with NULL/empty `visitor_id`; another visitor's and missing parents both return
404 `invalid_reference`. Optional links must be entirely NULL; partly NULL
composite references are refused. Parents without `visitor_id TEXT` fail closed.

Omitting `write_mode` on an existing resource preserves its mode. Switching a
non-own SQLite database to own requires every application table to be empty,
even if earlier rows already carry a `visitor_id`: raw full-mode identities are
not trusted. Clear/migrate those rows deliberately through owner tools first.
Existing own databases keep their stamped rows during policy edits. Unattributed
KV/files remain owner-only under own reads; full-mode visitor overwrites record
the latest writer, while owner overwrites preserve attribution.

Visitor writes use `RATE_LIMIT_STORAGE_IP` (120,100ms) per site and client IP
and `RATE_LIMIT_STORAGE_VISITOR` (60,200ms) per site and signed-in visitor.
Reads, including POST `/query`, have no new rate limit; a 150-photo public
gallery loads without consuming write tokens. Owner credentials are exempt.
Reads never take the deploy mutex. Bodies are consumed before locking writes
and quota checks. `SITE_STORAGE_WRITE_LOCK_WAIT_MS` defaults to 2000; writes
during deploy wait briefly, then return 503 `storage_busy` with Retry-After.
Owner operations have `SITE_STORAGE_OWNER_TIMEOUT_MS` (5000); visitor writes
have `SITE_STORAGE_VISITOR_WRITE_TIMEOUT_MS` (2000), and visitor SQLite queries
have `SITE_STORAGE_VISITOR_QUERY_TIMEOUT_MS` (1000). All SQLite executions,
including owner queries and policy validation, share a process-wide semaphore:
`SITE_STORAGE_SQL_CONCURRENCY=0` selects the CPU count. When full,
`SITE_STORAGE_ACQUIRE_WAIT_MS` (250) bounds waiting, then returns 503 with
Retry-After. Writers always take the site lock before the SQLite slot. A visitor's fixed route permits at most 100
columns; the complete JSON query result is bounded by
`SITE_STORAGE_SQL_RESULT_MAX_BYTES`. GET and HEAD are reads.

KV allowance counts UTF-8 key bytes plus normalized JSONB value bytes.
`SITE_STORAGE_FILES_MAX_OBJECTS` defaults to 1000 committed objects per bucket,
including empty/tiny files (507 `bucket_full`). File paths allow at most 16
segments. Own file lists use the existing `(site,resource,writer,path)` index
and LIMIT before opening files; temporary `.upload-*` objects never appear.
New keys and upload paths are normalized to NFC, including macOS decomposed
names. Reads, deletes, listing prefixes and cursors accept exact legacy names.
Own-list cursors follow scanned metadata so missing files cannot end paging early. SQLite uniqueness failures use a generic 409 `row_conflict`; schema
names are not reflected to visitors. 507 `sqlite_full` describes engine
allocation failures separately from the quota precheck's `site_full`.

UNIQUE columns, KV keys and file paths are global within their table/resource.
A failed create can reveal a collision; use opaque random identifiers or a
visitor-specific namespace, never sensitive emails as unique keys. The server
does not silently change owner-defined global uniqueness. `auth.js` retains raw
query/execute for legacy full-mode compatibility; they are owner-only on add/own
resources, and new visitor pages use `table().add/list`. `site_passcode:off` is
an explicit owner choice: public resources stay accessible without unlocking.

The shared clientIP helper still trusts the last X-Forwarded-For entry (N4).
Deploy behind a local proxy that appends the real client address and prevents
direct access to the app port; bare unproxied installs can spoof IP buckets.
