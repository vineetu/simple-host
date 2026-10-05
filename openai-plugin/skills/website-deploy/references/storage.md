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
`visitor_id` is refused. Own databases require `visitor_id TEXT` in every table;
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

Create `orders` with `storage_set_resource(site,"orders",body)` (or owner PUT):

```json
{"kind":"sqlite","read":"own","write":"signed-in","write_mode":"add","site_passcode":"inherit"}
```

Through owner `storage_sql_schema`, create the table:

```sql
CREATE TABLE orders (id INTEGER PRIMARY KEY, item TEXT NOT NULL,
                     quantity INTEGER NOT NULL, status TEXT DEFAULT 'placed')
```

The server adds `visitor_id TEXT` and its index. Each customer sees their own
orders and the owner's latest status. The owner sees all with
`storage_sql_query` (`SELECT id,item,quantity,status FROM orders ORDER BY id DESC`)
and changes stages with `storage_sql_execute`, SQL
`UPDATE orders SET status=? WHERE id=?`, params `["packed",17]`.
Use the trusted owner dashboard/connector; never put an owner key in a page.
Visitors cannot update or delete orders with this policy. Their form and receipt
viewer use only fixed routes:

```html
<script>window.SH_CONFIG = {site: 'pantry'};</script>
<script src="https://simple-host.app/auth.js" defer></script>
```

```js
await SH.requireSignIn();
const orders = SH.storage.sqlite('orders').table('orders');
const receipt = await orders.add({item: 'Chai spice', quantity: 2});
const mine = await orders.list({order: 'id', desc: 1, limit: 50});
// mine.columns + mine.rows are only this customer's orders and latest status.
// To continue, pass mine.next_after as after with the same order and desc.
```

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

A page may instead call the relative REST routes with `credentials:'same-origin'`
and `X-SH-CSRF: 1` for POST/PUT. Its own host supplies the sign-in cookie; the
server takes identity from that session. Treat refusals as failures, preserve
the form, and never widen a policy to make a save work.
