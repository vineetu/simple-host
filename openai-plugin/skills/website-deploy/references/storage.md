# Site storage: KV, SQLite and files

On Simple Host, the older state, collection and declared-data APIs are deprecated. Use them only to maintain an existing site that depends on their behavior. New sites should use owner-defined KV, SQLite and file resources. `signed-in` alone gives shared access; choose `read:own` for visitor-specific reads and `write_mode:add` for new-only writes. Simple Hack websites expose only KV, SQLite and files; event signup stays on the trusted Simple Hack apex.


Use the connected `storage_*` tools to configure and maintain a site's new
storage resources. Keep each site's keys, tables and paths in its own design;
there is no required RSVP or shop schema. A resource name is unique across
the three kinds, and its kind cannot later change. Pages use same-origin
`/v1/sites/{site}/storage/...` requests with their site-scoped visitor session.
The hosted `auth.js` helper exposes `SH.storage` for these resources.

Create a resource with `storage_set_resource(site,name,body)`:

```json
{"kind":"kv","read":"anyone","write":"owner","site_passcode":"inherit"}
```

`kind` is `kv`, `sqlite` or `files`. The independent `read` and `write`
policies are `anyone`, `signed-in` or `owner`; read also supports `own`.
Both default to `owner`; `write_mode` defaults to full and may be add.
`site_passcode` defaults to `inherit`, requiring visitors to unlock an existing
site passcode, or can be `off` to bypass it for this resource. Anonymous
writes work only with `write=anyone`. `signed-in` gives **every** signed-in
visitor the chosen access to **every** key, row or file in that resource.
Choose read=own with write_mode=add for each visitor’s own records. Owner-only
reads keep submissions visible only to the owner; legacy Personal and Submissions
remain for existing sites that use them. Ask before making a resource public, allowing anonymous writes or
permanently deleting a resource.

| Task | Connected tool |
|---|---|
| List policies, check usage, configure or delete a resource | `storage_list_resources`, `storage_get_usage`, `storage_set_resource`, `storage_delete_resource` |
| List, read, set or remove KV entries | `storage_list_kv_keys`, `storage_get_kv`, `storage_put_kv`, `storage_delete_kv` |
| Query rows, change rows or change schema | `storage_sql_query`, `storage_sql_execute`, `storage_sql_schema` |
| List, upload, remove or link to file objects | `storage_list_file_objects`, `storage_put_file`, `storage_delete_file`, `storage_file_download_link` |

Host KV/SQLite share **1,000,000 bytes per website**; files get **10 MB** separately. Hack retains its original pool across all three kinds. `storage_get_usage` returns used, limit and
remaining bytes with a breakdown. Deployed website files and legacy saved data
have separate limits. Rejected growth returns `site_full` without changing
the existing data. Files persist across website publishes and rollbacks.
The connected inline file upload is capped at 1 MiB decoded, so plan small
objects and check remaining space first. Download links expire after ten
minutes. Stored HTML and scripts download as attachments rather than running
as part of the site.

For a phone-photo upload page, resize and compress in the browser before
calling `SH.storage.files('photos').put(path, preparedPhoto)`. Use canvas or
`createImageBitmap` to keep aspect ratio and encode as WebP or JPEG. Aim well
below the site's remaining allowance, show a preview and compressed byte size,
and refuse the original if it is still too large. Leave PDFs and other binary
files unchanged unless the application explicitly defines a conversion.

SQLite takes one statement per call with bound `?` parameters. Query is
read-only; execute changes rows; schema changes are owner-only. Do not
concatenate visitor text into SQL. The server checks which statements and
resources each operation may touch.

On the page's own site origin, use the hosted `SH` helper for visitor sign-in
when policy requires it. Visitor writes need the normal same-site session,
`Origin` and `X-SH-CSRF: 1`; the helper supplies them. Handle
`sign_in_required`, `forbidden`, `site_locked`, `resource_not_found` and
`site_full` as server decisions. Do not broaden a policy merely to make a
failed write succeed.

Older state, collections and declared-data APIs remain supported on Simple Host for existing sites but are deprecated. Their
atomic operations, undo/history, notifications and private visitor behavior
do not automatically migrate to these whole-resource policies. Do not copy
private old data into a new resource without the owner's direction.

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
that the parent exists and has the same visitor ID, in the insert transaction;
missing parents return 404 `invalid_reference`, other visitors’ parents return
403 `invalid_reference`. Each reference uses an indexed parent lookup. Nullable
foreign keys follow SQLite’s NULL semantics. Read orders and history separately. Own databases require `visitor_id TEXT` in every table;
new tables created through the owner schema route receive it and an index.
Choosing own reads on an existing database without it is refused. Schema changes
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
transaction (404 for a missing order, 403 for another customer's order).

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
