# Site storage resources: KV, SQLite and files

On Simple Host, the older state, collection and declared-data APIs are deprecated. Use them only to maintain an existing site that depends on their behavior. New sites should use owner-defined KV, SQLite and file resources. Use `read:own` for per-person reads and `write_mode:add` for new-only writes; `signed-in` alone remains shared. Simple Hack websites expose only KV, SQLite and files; event signup stays on the trusted Simple Hack apex.


Use these resources for new Simple Host sites that need saved data. Keep each
application's keys, tables and paths in the site's own design; there is no
predefined RSVP, shop or guestbook schema. A resource name is unique across the
three kinds, and its kind cannot later change. The owner creates and configures
it with a Simple Host connector or an owner API key on `https://simple-host.app`.
Pages call the same REST paths **on their own site origin** so the visitor's
site-scoped sign-in and passcode cookie apply. These resources are not available
to Enterprise replicated/S3 storage. Simple Hack team sites and custom event
websites have the same resource API and per-site allowance.

On Simple Hack, use the signed-in connector and the separate reviewed Simple Hack
website-deploy skill. Select the team before team storage tools; organisers use
`hack_event_storage_*` for a custom event website. A custom event page calls
`/v1/sites/{event}/storage/...` on its own host. Simple Hack has only KV, SQLite
and file website storage. Resource access covers all keys, rows and files.

Declare a resource with `storage_set_resource(site,name,body)` or
`PUT /v1/sites/{site}/storage/resources/{name}`:

```json
{"kind":"kv","read":"anyone","write":"owner","site_passcode":"inherit"}
```

`kind` is `kv`, `sqlite` or `files`. `read` and `write` are independent:
`anyone`, `signed-in` or `owner`, each defaulting to `owner`. `site_passcode`
is `inherit` by default (a visitor must also unlock an existing site passcode)
or `off` (this resource deliberately bypasses that passcode). An owner key
bypasses the passcode but remains owner-scoped. Anonymous writes work **only**
when the owner explicitly chooses `write=anyone`. A signed-in policy gives
every signed-in visitor access to the resource. It does not grant row-level or
per-person privacy. The old private Submissions and Personal APIs remain
available only on Simple Host for existing sites; they are deprecated for new work. Ask the owner before making an existing resource public, opening
anonymous writes, or permanently deleting it.

| Task | Connector | REST suffix after `/v1/sites/{site}/storage` |
|---|---|---|
| List resources and policies | `storage_list_resources(site)` | `GET /resources` (owner) |
| Check storage allowance | `storage_get_usage(site)` | `GET /usage` (owner); returns `used_bytes`, `limit_bytes`, `remaining_bytes` and `breakdown` by KV, SQLite and files |
| Create or change a resource | `storage_set_resource(site,name,body)` | `PUT /resources/{name}` (owner) |
| Permanently delete a resource | `storage_delete_resource(site,name)` | `DELETE /resources/{name}` (owner) |
| List KV entries | `storage_list_kv_keys(site,name,prefix?,after?,limit?)` | `GET /kv/{name}/keys?prefix=&after=&limit=` |
| Read, set, remove a KV value | `storage_get_kv`, `storage_put_kv`, `storage_delete_kv` with `site,name,key` (`value` on put) | `GET`, `PUT`, `DELETE /kv/{name}/keys/{key}`; PUT body `{ "value": <JSON> }` |
| Read-only SQL query | `storage_sql_query(site,name,sql,params?)` | `POST /sqlite/{name}/query` with `{sql,params}` |
| Change SQL rows | `storage_sql_execute(site,name,sql,params?)` | `POST /sqlite/{name}/execute` with `{sql,params}` |
| Change SQL schema | `storage_sql_schema(site,name,sql,params?)` | `POST /sqlite/{name}/schema` (owner) |
| List file objects | `storage_list_file_objects(site,name,prefix?,after?,limit?)` | `GET /files/{name}/objects?prefix=&after=&limit=` |
| Upload or remove a file | `storage_put_file(site,name,path,content_base64,content_type)`, `storage_delete_file(site,name,path)` | `PUT` raw bytes with `Content-Type`, `DELETE /files/{name}/objects/{path...}` |
| Download a file | `storage_file_download_link(site,name,path)` for the owner | Browser or direct REST: `GET /files/{name}/objects/{path...}`; connector mints a scoped ten-minute link rather than returning base64 bytes |

The connector's inline file upload is capped at 1 MiB decoded; use direct REST
for larger files. File storage is durable across website publishes and rollbacks;
it is separate from the versioned website files. Stored raster images may render
inline only after the server validates their bytes; other types download as
attachments. Do not use a file object as a way to serve arbitrary HTML or JS
under the site's origin.

KV/SQLite share **1,000,000 bytes per website**; Host files get a separate **10 MB** allowance. Hack retains the pooled allowance across all three kinds. Check `storage_get_usage` or owner-only `GET /usage`
before large writes. The response separates `kv_bytes`, `sqlite_bytes` (the
main database after checkpoint) and `files_bytes`; SQLite's transient WAL is
excluded. This allowance does not include deployed website files or legacy
saved data, which retain their own limits. A rejected growth write returns
`site_full` without changing the stored value or object.

For a page that lets people upload phone photos, resize and compress the image
in the browser before sending its bytes to the files API. Decode with
`createImageBitmap`, draw to a canvas at a smaller width and height while
preserving aspect ratio, and encode as WebP or JPEG with a suitable quality.
Choose a target well below the site's remaining allowance so more than one
image can fit. Show a preview and the compressed byte size; if it is still too
large, explain that the visitor must choose a smaller image or reduce quality.
Leave PDFs and other binary files untouched unless the application explicitly
defines a conversion. The raw-file API stores the bytes it receives.

For example, a browser page can prepare a selected photo before calling
`SH.storage.files('photos').put(path, preparedPhoto)`. Its 350,000-byte target
fits several photos in the separate 10 MB file allowance; choose a lower target if
`/usage` shows less space remains. Show the returned file in an image preview
and display its `size` before uploading. If preparation throws, show its message
beside the file input and do not upload the original photo.

```js
async function preparePhoto(file, maxBytes = 350000) {
  if (!(file instanceof Blob) || !file.type.startsWith('image/'))
    throw new TypeError('Choose a photo');
  if (!('createImageBitmap' in window))
    throw new Error('This browser cannot resize photos');
  const image = await createImageBitmap(file);
  try {
    const maxEdge = Math.max(image.width, image.height);
    if (!maxEdge) throw new Error('Photo has no dimensions');
    const firstScale = Math.min(1, 1600 / maxEdge);
    for (let shrink = 1; shrink >= 0.2; shrink *= 0.8) {
      const canvas = document.createElement('canvas');
      canvas.width = Math.max(1, Math.round(image.width * firstScale * shrink));
      canvas.height = Math.max(1, Math.round(image.height * firstScale * shrink));
      const ctx = canvas.getContext('2d');
      if (!ctx) throw new Error('This browser cannot draw photos');
      ctx.drawImage(image, 0, 0, canvas.width, canvas.height);
      for (const quality of [0.82, 0.68, 0.54]) {
        const blob = await new Promise((resolve, reject) => canvas.toBlob(
          b => b ? resolve(b) : reject(new Error('Photo encoding failed')),
          'image/webp', quality));
        if (blob.type !== 'image/webp')
          throw new Error('This browser cannot encode WebP photos');
        if (blob.size <= maxBytes) {
          const name = (file.name || 'photo').replace(/\.[^.]+$/, '') + '.webp';
          return new File([blob], name, {type: 'image/webp'});
        }
      }
    }
    throw new Error('Photo is still too large; choose a smaller image');
  } finally {
    image.close();
  }
}
```

SQLite accepts one statement per call with bound positional `?` parameters.
`/query` is read-only; `/execute` changes rows; `/schema` is owner-only for
schema changes. The server's SQLite authorizer enforces operation and resource
boundaries, including nested statements. The query answer has `columns` and
`rows` (BLOB cells are `{ "base64": "..." }`); execute returns `changes` and
`last_insert_id`. Design tables and
indexes for the actual site; never concatenate visitor text into SQL.

For browser calls, use relative `/v1/sites/{site}/storage/...` URLs on the
site's own address. Use `SH.requireSignIn()` when the chosen policy requires
sign-in and the existing hosted auth helper for the site's session. Visitor
writes send the same-site `Origin` and `X-SH-CSRF: 1`; owner tool calls use their
existing credential. Treat `sign_in_required` (401), `forbidden` or
`site_locked` (403), `resource_not_found` (404), validation errors (400), and
`site_full` (507) as server decisions. Do not retry a write with a broader
policy merely to make it succeed.

On Simple Host, older `/state`, `/collections` and `/data` APIs remain
supported for existing sites but are deprecated for new work. Their atomic
operations, history, visitor edits and withdrawal do not transfer; read=own
supports each visitor’s own reads. These routes are unavailable on Simple Hack. Do not copy private
data into a broader resource without an owner-directed migration.

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
