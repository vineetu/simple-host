# Site storage resources: KV, SQLite and files

Use owner-defined resources for new sites: KV for named JSON values, SQLite
for related records and queries, files for photos and downloads. Resource names
are unique across kinds and kinds cannot change. Create resources and SQLite
tables before publishing pages that use them. Pages use same-origin REST and
site-scoped visitor sign-in; they never contain an owner key.

## Row rules for SQLite and key-value data

Follow the description of `storage_set_resource` that this server gives. If it
does not accept `rules`, the server is older and its description says what to
send. A KV namespace still in Postgres cannot accept rules. A resource of any
kind saved without rules is owner-only. File buckets take the same rules, over
`path` and `visitor_id`; see the file section below.

Rules have `version: 1` and `policies`. A policy has a `command`, optional
`name`, optional `restrictive: true` (default false), and these clauses:

| Command | `using` | `check` |
|---|---|---|
| `read` | required: stored row | refused |
| `add` | refused | required: stored inserted row |
| `edit` | required: stored row | optional; inherits using |
| `delete` | required: stored row | refused |
| `all` | required for read/edit/delete | optional; inherits using for add/edit |

Permissive policies OR together; restrictive policies AND together, then narrow
that grant. Without an applicable permissive policy, the operation is denied. An empty list
denies every page caller, including owner-on-site; owner tools still work.

Expressions use columns (`visitor_id` or `row.visitor_id`), trusted bindings,
`true`, `false`, `&&`, `||`, `!`, parentheses, `==`, `!=`, `in`, `contains`,
`is null`, `is not null`, and ASCII `lower()`. No SQL or ordering operators.
Strings use single/double quotes with JSON escapes; lists contain 1–64 strings
or numbers of one type. Text compares as case-sensitive bytes. NULL or wrong
storage types make both a comparison and its rewritten negation false; test
NULL explicitly. Columns match schema names case-insensitively; use `row.in`
for a reserved word. Limits and the complete grammar are in
[the language reference](https://github.com/vineetu/simple-host/blob/main/docs/designs/storage-rules.md).

Platform facts: nullable `auth.id`, boolean `auth.signed_in`, `auth.role`
(`anon`, `visitor`, `owner`), boolean `auth.ai`, and `auth.teams` (empty text
list without a product). `auth.checked` is declared only with the spam check
configured and may appear only in an add check. Every OR alternative of a
permissive edit/delete/all using must positively require `auth.signed_in`,
`visitor_id == auth.id`, or `auth.role == "owner"` as an AND term. A guard only
in check is insufficient. Suspended visitors are refused for identity-dependent
permissive policies; a public read of `true` still reads what anyone reads.

For bookings, orders, applications or assignments: signed-in people add, each
reads their own, and the owner reads and changes all. Recipe topics `booking`,
`records` and `people` give complete pages; topic names are unchanged.

```json
{"kind":"sqlite","rules":{"version":1,"policies":[
  {"command":"read","using":"visitor_id == auth.id || auth.role == \"owner\""},
  {"command":"add","check":"auth.signed_in"},
  {"command":"edit","using":"auth.role == \"owner\""},
  {"command":"delete","using":"auth.role == \"owner\""}
]}}
```

For owner-maintained public data, use read `using: "true"` and an all policy
`using: "auth.role == \"owner\""`. For an owner-only inbox, keep that owner
policy and use add `check: "true"`. For personal editable notes use all
`using: "visitor_id == auth.id || auth.role == \"owner\""`,
`check: "auth.signed_in"`. A signed-in condition alone shares rows among people;
it does not give per-person privacy.

SQLite `tables: {"orders": {"rules": {...}}}` replaces the default set for that
table. Omit `tables` to keep existing sets; `{}` removes them. Every kind
refuses preset and read/add/edit/delete fields (`invalid_resource`). Saving only
passcode inheritance keeps rules.
Rules responses have `rules_status`, `rules_errors` and table sets. Repair
`invalid_rules` using `rule_error` clause and position; `rules_invalid` means a
stored set no longer compiles. Do not widen access to fix a refusal.

For moderated posts use a checked add plus a restrictive read policy:
`{"command":"read","restrictive":true,"using":"auth.role == \"owner\" || approved_at is not null"}`.
A table holds posts only when its effective set references TEXT `approved_at`.
Visitor adds store NULL; owner-on-site and owner tools stamp the time, unless
owner tools supply a value. Only those owner paths may edit the stamp. An
unreferenced leftover column is ordinary data. Checked posts get size/link
limits; without a configured spam check use signed-in posting and its recipe.

Pages use `SH.storage.sqlite(db).table(t).list/get/add/edit/delete`.
`SH.storage.toObjects(result)` yields objects with `mine`; other visitors'
authors are blanked. The server stamps identity/timestamps and assigns IDs.
Edit and delete require read visibility of the stored row. Check failures roll
back; hidden and missing rows return the same 404. References must point at
rows readable under the parent table's rules. Raw query/execute are owner tools.

KV offers `.get`, `.set`, `.keys`, `.delete`; a set adds or edits according to
existence and keeps the original author on edit. Rules may name `key`, `value`
(normalized JSON text), `visitor_id`, `created_at`, `updated_at`.
A UNIQUE conflict can reveal a hidden row; a KV add attempt can reveal a key's
existence via `key_exists`. Use opaque random names, never sensitive keys.

The owner signed in on the site's own address uses the same visitor routes
and rules with `auth.role == "owner"`; `SH.me()` returns `site_owner: true`.
This covers saved data only. Write visitor text with `textContent`. Framed
pages and old-version previews never gain these rights. Site passcodes and
named viewers, CSRF, quotas and product gates still apply whatever rules say.
`site_passcode` is `inherit` by default; `off` is an explicit owner choice.

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

KV/SQLite share **10,000,000 bytes per website**; Host files get a separate **10 MB** allowance. Hack retains the pooled allowance across all three kinds. Check `storage_get_usage` or owner-only `GET /usage`
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
A refused statement returns a `sql_*` code and says what to change (the owner's
messages list the real tables or columns); fix that rather than resending it.
`/query` is read-only; `/execute` changes rows; `/schema` is owner-only for
schema changes. The server's SQLite authorizer enforces operation and resource
boundaries, including nested statements. The query answer has `columns` and
`rows` (BLOB cells are `{ "base64": "..." }`); execute returns `changes` and
`last_insert_id`. Design tables and
indexes for the actual site; never concatenate visitor text into SQL.

For browser calls, use relative `/v1/sites/{site}/storage/...` URLs on the
site's own address. Use `SH.requireSignIn()` when the chosen policy requires
sign-in and the existing hosted auth helper for the site's session. The helper
also offers `SH.signIn({provider, returnTo})`, `SH.email.request(email)`,
`SH.email.verify(email, code)` (15-minute code, 3 attempts; the account is
created on first verify) and `SH.signOut()`. Visitor
writes send the same-site `Origin` and `X-SH-CSRF: 1`; owner tool calls use their
existing credential. Treat `sign_in_required` (401), `forbidden` or
`site_locked` (403), `resource_not_found` (404), validation errors (400), and
`site_full` (507) as server decisions. Do not retry a write with a broader
policy merely to make it succeed.

For privacy use the author condition above.

Visitors use fixed table routes with `where` equality filters (at most three),
order, direction, limit and an opaque `after` cursor. Keep order and direction
when following `next_after`. Defaults are rowid ascending and 100 rows (max
500). Use ordinary rowid tables and `id INTEGER PRIMARY KEY`. Optional composite
foreign keys must be entirely NULL. Triggers and cascades that write other
rows are refused. Owner SQL remains parameterized and bounded.

Simple Host KV and SQLite share **10,000,000 bytes per website**
(`SITE_STORAGE_MAX_BYTES`). Files have a separate **10,000,000 bytes per website**
(`SITE_STORAGE_FILES_MAX_BYTES`, decimal 10 MB). The usage response keeps
`used_bytes`, `limit_bytes`, `remaining_bytes` for KV/SQLite and adds
`files_used_bytes`, `files_limit_bytes`, `files_remaining_bytes`, retaining the
three-kind `breakdown`. The existing single-upload cap remains 1,000,000 bytes
(`SITE_STORAGE_FILE_MAX_BYTES`); visitor upload pages shrink photos in the browser. SQLite WAL, deployed website files and legacy saved data retain their
separate accounting. Simple Hack keeps its existing 1,000,000-byte pool across
all three kinds and its per-upload cap; on Hack the first three usage fields
still describe that pool.

<a id="shop-with-orders"></a>

## Each person's records

People add records; each signed-in person sees only their own, with status and
history. The owner sees and updates all records. People request changes by
appending linked change rows, preserving the original record and its history.

Example uses: shop orders, RSVPs and event sign-ups, bookings and appointments, applications (jobs, clubs, hackathons), support requests, homework or assignment submissions, survey answers people can revisit, and a waitlist with “my place in line”.

Use this pattern when someone asks for any of these. Choose resource and table
names that fit the domain (`bookings`, `applications`, and so on), keeping
the author-or-owner read rule above, a status column and a
linked change table in the same database. Declare its foreign key so a person's
change rows can reference only their own record. This pattern is for hosted
Simple Host and small-box installs.

### Worked example: shop orders

Create an `orders` SQLite resource with `storage_set_resource(site,"orders",body)`
(or owner PUT):

```json
{"kind":"sqlite","rules":{"version":1,"policies":[
  {"command":"read","using":"visitor_id == auth.id || auth.role == \"owner\""},
  {"command":"add","check":"auth.signed_in"},
  {"command":"edit","using":"auth.role == \"owner\""},
  {"command":"delete","using":"auth.role == \"owner\""}
]},"site_passcode":"inherit"}
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

Both tables inherit the resource's rules: signed-in add, author-or-owner read, owner edit/delete. The server
adds indexed `visitor_id TEXT`, stamps `created_at` on visitor inserts, and
ignores client-sent identity and timestamps. A declared foreign key checks that
the referenced order exists and belongs to the same customer, in the insert
transaction (404 `invalid_reference` for a missing order and for another customer's order alike).

Customers never rewrite or delete a placed order. They customise it by adding
a change, a note, a cancellation request or a requested new quantity to
`order_changes`; the original order and change history remain. The page shows
“My orders”, each current status and its history, using two reads and matching
`order_changes.order_id` to `orders.id` in the page. There is no `include=` option.

```html
<script>window.SH_CONFIG = {site: 'pantry'};</script>
<script src="https://simple-host.app/auth.js"></script>
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

The owner sees all orders and history through `storage_sql_query`. Each row's
`visitor_id` in connector results is an opaque reference scoped to this site, not an account ID. Pass it directly in `storage_sql_query` or `storage_sql_execute` params for the same site. It lasts until the server restarts; read the row again if needed. Only when the owner asks "who placed order 12?" read
`SELECT visitor_id FROM orders WHERE id = 12` and pass it to
`storage_visitor_emails`, which gives the email they signed in with. Tell the person; never
write the email into a page or into saved data. REST keeps its existing visitor IDs; use those with the owner-only `GET /v1/sites/<site>/storage/visitors?id=<id>` route. The owner's
view applies or acknowledges requests and updates the order's status or stage
with `storage_sql_execute`, for example `UPDATE orders SET status=? WHERE id=?`
with params `["packed",17]`. Keep the history when handling a request. Use the
trusted owner dashboard/connector, or an admin page on the site (`get_page_recipe`
topic `admin`): the owner signed in there with their account email lists every
order (`orders.list({where:{status:'placed'}})`) and sets a status
(`orders.edit(17, {status:'packed'})`). Never put an owner key in a page. The
trade-off of an admin page: while the owner is signed in on their own site, a bug
or a malicious script on its pages could act on that site's data, so write
visitors' text with `textContent`, never `innerHTML`.

### The owner adds a row for one person (SIM-358)

On a table whose permissive read alternatives require the author or owner, the owner may add a row on behalf of one
already-signed-in person instead of waiting for them to add it — a coach
assigning a client's program, a tutor replying to a student's message. The
row's `visitor_id` becomes that person's, exactly as if they had added it
themselves, and a second server-owned column, `written_by`, reads `"owner"`
(otherwise `""`, including a plain add with no `for`) so the person's own
reads never mistake it for their own write:

```js
// On the page, signed in as the owner on the site's own address.
await SH.storage.sqlite('orders').table('orders').add(
  {item: 'House blend, 1 lb'}, {for: pickedVisitorId}
);
```

`storage_sql_execute` reaches the same path from a connector, by giving
`table` and `values` instead of `sql` and `params`:

```json
{"site": "<site>", "name": "orders", "for": "client@example.com",
 "table": "orders", "values": {"item": "House blend, 1 lb"}}
```

`for` requires at least one author alternative and no permissive read
alternative outside author-or-owner access; otherwise it is `for_requires_own`.
`for` takes a `visitor_id` or the email
that person signed in with (matched against their current sign-in email the
same way `storage_visitor_emails`/`SH.me()` do, among people the server
recorded as signed in on this site — never a raw username or OAuth-identity
row alone, and never a guess among more than one match: `visitor_unknown`
(404) if nobody matches, `visitor_email_ambiguous` if more than one does).
There is no email binding before a first sign-in. It never fires
`record.added` to the owner's own webhook subscriptions. `written_by` is
added lazily, under the write lock, the first time a table is used this way
— an existing author-tracked table works without resaving its rules —
and is refused (`written_by_blocked`) rather than risk reshaping a table a
trigger or view depends on; a trigger that got written_by onto the table
some other way, and then rewrites it as a side effect of a `for` insert, is
caught after the fact and the whole add is rolled back
(`written_by_tampered`). The owner's raw SQL console (`storage_sql_execute`
without `for`) refuses a statement naming `written_by`, in `execute` and
`schema` alike, so a new trigger or view cannot name it either — but this
is a name match, not a parser: a positional `INSERT INTO t VALUES (...)`
naming no column is not caught, by design (no SQL parsing was added to try
to catch it). As always, raw SQL can still set `visitor_id` directly too.
Provenance through `written_by` holds for rows added through the table
routes, `auth.js`, and `for`; the raw SQL console remains exactly as
unrestricted on server columns as it already was. Changing a table's rules to grant wider reads exposes an owner-added row
the same as any other row there; confirm the visibility change first. `for`, the pick list, and
`storage_visitor_emails`'s `emails` direction
are Host only (404 on Simple Hack and event storage): named viewers and this
email resolution are not a Simple Hack concept. The owner's pick list of
whom to add for: `SH.storage.people()` on the page, or
`GET /v1/sites/<site>/storage/people` with the owner's key — people who have
saved data here or are named viewers, each confirmed against the server's
own sign-in record, never everyone who has merely signed in and never a raw
table's content taken on trust. `storage_visitor_emails` also takes `emails`
now, resolving each to the reference `for` takes, the same direction REST's
`?email=` does. Worked example: `get_page_recipe` topic `people`.

## File buckets take the same rules

Create a `photos` file bucket. File rules use the same words as key-value rules,
over one row per file: `path` and `visitor_id` (the signed-in uploader). Read
lists and opens files, add uploads a new path, edit replaces a file, delete
removes one. These rules let visitors view the photos, signed-in visitors upload
and authors remove their own; for photos only the sender and the shop owner see,
make the read rule `visitor_id == auth.id || auth.role == "owner"`. File rules
cannot use `auth.checked`: uploads are never spam-checked.

```json
{"kind":"files","site_passcode":"inherit","rules":{"version":1,"policies":[{"name":"read","command":"read","using":"true"},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"delete","command":"delete","using":"visitor_id == auth.id"},{"name":"owner","command":"all","using":"auth.role == \"owner\""}]}}
```

```js
await SH.requireSignIn();
// preparedPhoto is a resized WebP Blob, below the single-upload cap.
await SH.storage.files('photos').put(crypto.randomUUID() + '.webp', preparedPhoto);
```

A page may instead call relative REST routes with `credentials:'same-origin'`
and `X-SH-CSRF: 1` for POST/PUT. The server takes identity from the site session.
Preserve the form on a refusal; never widen access to make a save work.

## The same pattern for bookings

Name the SQLite resource `bookings`, keeping the same policies. Through the
owner schema route, create these tables in two calls:

```sql
CREATE TABLE bookings (id INTEGER PRIMARY KEY, appointment TEXT NOT NULL,
                       status TEXT DEFAULT 'requested', created_at TEXT)
```

```sql
CREATE TABLE booking_changes (
  id INTEGER PRIMARY KEY,
  booking_id INTEGER NOT NULL REFERENCES bookings(id),
  kind TEXT NOT NULL CHECK (kind IN ('change','note','cancel_request')),
  details TEXT NOT NULL,
  created_at TEXT
)
```

The server adds indexed `visitor_id TEXT` to both tables as in the orders
example. People add bookings and linked change rows; “My bookings” reads both
tables and matches `booking_changes.booking_id` to `bookings.id`. Each person
sees only their own status and history. The owner reads all bookings and
changes, and updates status (for example, `confirmed`) through owner SQL.



Visitor inserts use `INSERT OR ABORT` and RETURNING. Client primary-key and
rowid values are refused. Foreign keys are enabled on every connection; hidden
and missing parents both give `invalid_reference`. An existing resource with
untrusted legacy SQL authorship needs deliberate owner migration before rules
are saved; do not turn forged identities into per-person ownership.

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
including owner queries and policy validation, share a per-site slot (one by
default, `SITE_STORAGE_SITE_INFLIGHT`) and a process-wide semaphore:
`SITE_STORAGE_SQL_CONCURRENCY=0` selects the CPU count. When full,
`SITE_STORAGE_ACQUIRE_WAIT_MS` (250) bounds waiting, then returns 503 with
Retry-After. Writers always take the site lock before the SQLite slot. A visitor's fixed route permits at most 100
columns; the complete JSON query result is bounded by
`SITE_STORAGE_SQL_RESULT_MAX_BYTES`. GET and HEAD are reads.

KV allowance counts UTF-8 key bytes plus normalized JSON text bytes.
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
does not silently change owner-defined global uniqueness. `auth.js` retains raw query/execute for legacy compatibility; rules resources
refuse visitor SQL and new visitor pages use `table().add/list`. `site_passcode:off` is
an explicit owner choice: public resources stay accessible without unlocking.

The shared clientIP helper still trusts the last X-Forwarded-For entry (N4).
Deploy behind a local proxy that appends the real client address and prevents
direct access to the app port; bare unproxied installs can spoof IP buckets.
