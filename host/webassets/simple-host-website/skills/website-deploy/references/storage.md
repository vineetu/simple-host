# Site storage resources: KV, SQLite and files

Each site can have owner-defined resources: KV for named JSON values, SQLite for related records
and queries, files for photos and downloads. Resource names are unique across kinds and a kind
cannot change. Create resources and SQLite tables before publishing pages that use them. Pages
use same-origin REST and site-scoped visitor sign-in; they never contain an owner key.

## Row rules

Follow the description of `storage_set_resource` that this server gives. If it
does not accept `rules`, the server is older and its description says what to
send. A resource of any kind saved without rules is owner-only. File buckets take
the same rules, over `path` and `visitor_id`; see the file section below.

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
Strings use single/double quotes with JSON escapes; lists hold 1–64 strings
or numbers of one type. Text compares as case-sensitive bytes. NULL or a wrong
storage type makes both a comparison and its negation false; test NULL explicitly.
Columns match schema names case-insensitively; use `row.in` for a reserved word.
The full grammar and its limits:
[storage-rules.md](https://github.com/vineetu/simple-host/blob/main/docs/designs/storage-rules.md).

Bindings: nullable `auth.id`, boolean `auth.signed_in`, `auth.role`
(`anon`, `visitor`, `owner`), boolean `auth.ai`, and `auth.teams` (a text list).
`auth.checked` (the spam check passed) exists only where a spam check is
configured and may appear only in an add check. Every OR alternative of a
permissive edit/delete/all using must require `auth.signed_in`,
`visitor_id == auth.id`, or `auth.role == "owner"` as an AND term; a guard only
in check is not enough.

Bookings, orders, applications: signed-in people add, each reads their own, the
owner reads and changes all.

```json
{"kind":"sqlite","rules":{"version":1,"policies":[
  {"command":"read","using":"visitor_id == auth.id || auth.role == \"owner\""},
  {"command":"add","check":"auth.signed_in"},
  {"command":"edit","using":"auth.role == \"owner\""},
  {"command":"delete","using":"auth.role == \"owner\""}
]},"site_passcode":"inherit"}
```

- Owner-maintained public data: read `using: "true"` and an all policy `using: "auth.role == \"owner\""`.
- Owner-only inbox: that owner policy plus add `check: "true"`.
- Personal editable notes: all `using: "visitor_id == auth.id || auth.role == \"owner\""`, `check: "auth.signed_in"`.
- `auth.signed_in` alone shares rows among everyone signed in; it is not per-person privacy.
- Moderated posts: a checked add plus
  `{"command":"read","restrictive":true,"using":"auth.role == \"owner\" || approved_at is not null"}`.
  A table holds posts only when its rules name a TEXT `approved_at` column; visitor adds store
  NULL and owner paths stamp the time.

SQLite `tables: {"orders": {"rules": {...}}}` replaces the resource's rules for that table.
Omit `tables` to keep existing table rules; `{}` removes them. Saving only `site_passcode`
keeps rules. `site_passcode` is `inherit` by default; `off` lets visitors reach the resource
without the site passcode. Responses carry `rules_status`, `rules_errors` and table sets; repair
`invalid_rules` using the `rule_error` clause and position. Never widen access to fix a refusal.

## Pages

- SQLite: `SH.storage.sqlite(db).table(t).list/get/add/edit/delete`. `list` takes `where`
  (equality, at most three fields), `order`, `desc`, `limit` (default 100, max 500) and `after`;
  follow `next_after` with the same order. `SH.storage.toObjects(result)` gives objects with
  `mine`; other visitors' authors are blanked.
- KV: `.get`, `.set`, `.keys`, `.delete`. Rules may name `key`, `value`, `visitor_id`,
  `created_at`, `updated_at`.
- Files: `SH.storage.files(name).put(path, blob)`, `.list()`, `await .url(path)` for an `<img src>`.
- The server stamps `visitor_id` and timestamps and assigns IDs; it ignores client-sent ones.
  Edit and delete need read visibility of the row; hidden and missing rows both return 404.
- The owner signed in on the site's own address gets `auth.role == "owner"` for saved data only;
  `SH.me()` returns `site_owner: true`.
- Use `id INTEGER PRIMARY KEY` rowid tables. Foreign keys are enforced; a reference must point at
  a row the visitor can read. Triggers that write other rows are refused.
- UNIQUE columns, KV keys and file paths are global within their resource, so a failed add can
  reveal that a value exists: use random names, never emails, as keys.
- Treat `sign_in_required` (401), `forbidden` or `site_locked` (403), `resource_not_found` (404),
  validation errors (400), `storage_busy` (503) and `site_full` (507) as the server's decision.

## Owner tools and REST

Owner REST calls use the same paths with the owner's key (`references/rest-api.md`). A page uses
relative URLs on the site's own address with `credentials:'same-origin'` and `X-SH-CSRF: 1` on writes.

| Task | Connector | REST suffix after `/v1/sites/{site}/storage` |
|---|---|---|
| List resources and rules | `storage_list_resources` | `GET /resources` |
| Usage | `storage_get_usage` | `GET /usage` |
| Create or change a resource | `storage_set_resource(site,name,body)` | `PUT /resources/{name}` |
| Delete a resource and its data | `storage_delete_resource` | `DELETE /resources/{name}` |
| List KV keys | `storage_list_kv_keys` | `GET /kv/{name}/keys?prefix=&after=&limit=` |
| Read, set, remove a KV value | `storage_get_kv`, `storage_put_kv`, `storage_delete_kv` | `GET`, `PUT` (`{"value": <JSON>}`), `DELETE /kv/{name}/keys/{key}` |
| Read-only SQL | `storage_sql_query(site,name,sql,params?)` | `POST /sqlite/{name}/query` with `{sql,params}` |
| Change rows | `storage_sql_execute` | `POST /sqlite/{name}/execute` |
| Change schema | `storage_sql_schema` | `POST /sqlite/{name}/schema` |
| Visitor table rows | (pages) | `GET`/`POST /sqlite/{name}/tables/{table}/rows`, `GET`/`PATCH`/`DELETE .../rows/{id}` |
| List files | `storage_list_file_objects` | `GET /files/{name}/objects?prefix=&after=&limit=` |
| Upload, read, remove a file | `storage_put_file` (base64, under 1 MiB), `storage_delete_file` | `PUT` raw bytes, `GET`, `DELETE /files/{name}/objects/{path...}` |
| Download link for the owner | `storage_file_download_link` (ten minutes) | `POST /files/{name}/download-link` |

SQL takes one statement per call with positional `?` parameters; never build SQL from visitor
text. A refused statement returns a `sql_*` code that says what to change. Query answers have
`columns` and `rows`; execute returns `changes` and `last_insert_id`.

## Limits

KV and SQLite share **10,000,000 bytes per website**; files have a separate **10 MB**
allowance; one upload is under 1,000,000 bytes; a bucket holds 1000 files. Check
`storage_get_usage` before large writes; a write over the allowance returns `site_full` and
changes nothing. File storage survives publishes and rollbacks. Stored images render inline;
other types download as attachments.

Resize phone photos in the browser before upload: decode with `createImageBitmap`, draw to a
canvas no wider than about 1600 px, and encode WebP or JPEG at about 0.8 quality, lowering it
until the file fits (about 350,000 bytes leaves room for several photos). Show a preview; if it
still does not fit, ask for a smaller photo. Leave PDFs and other files untouched.

<a id="shop-with-orders"></a>

## Each person's records

People add records; each signed-in person sees only their own, with status and history. The
owner sees and updates all. People ask for changes by adding linked change rows, so the
original and its history stay. Use it for orders, RSVPs, bookings, applications, support
requests and survey answers people revisit.

Create an `orders` SQLite resource with the author-or-owner rules above
(`storage_set_resource(site,"orders",body)`), then two `storage_sql_schema` calls:

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

The server adds an indexed `visitor_id TEXT` to both. The foreign key makes sure a change row
points at the same customer's order (`invalid_reference` otherwise).

```js
await SH.requireSignIn();
const db = SH.storage.sqlite('orders');
const orders = db.table('orders'), changes = db.table('order_changes');
const receipt = await orders.add({item: 'Chai spice', quantity: 2});
await changes.add({order_id: receipt.last_insert_id, kind: 'change',
                   details: 'Please change the quantity to 3'});
const mine = await orders.list({order: 'id', desc: 1, limit: 50});
const history = await changes.list({order: 'id', limit: 100});
// Only this customer's rows. Match history to orders by order_id in the page.
```

The owner reads everything with `storage_sql_query` and sets a status with
`storage_sql_execute` (`UPDATE orders SET status=? WHERE id=?`, params `["packed",17]`), or on
an admin page (recipe `admin`) with `orders.edit(17, {status:'packed'})`. A row's `visitor_id`
in connector results is an opaque reference for this site; to answer "who placed order 12?",
pass it to `storage_visitor_emails` and tell the person; never write the email into a page or
saved data. Bookings work the same way: a `bookings` resource with `bookings` and
`booking_changes` tables.

The owner can add a row for one signed-in person (a coach assigning a program): on the page,
`table.add({...}, {for: visitorId})` with the pick list from `SH.storage.people()`; from chat,
`storage_sql_execute` with `for` (a `visitor_id` or the email they signed in with), `table` and
`values` instead of `sql`. The row is theirs, with `written_by` set to `"owner"`. `for` needs
author-or-owner read rules. Worked example: recipe `people`.

## File buckets take the same rules

File rules use the same words, over one row per file: `path` and `visitor_id` (the uploader).
Read lists and opens files, add uploads a new path, edit replaces a file, delete removes one.
These rules let anyone view, signed-in visitors upload, and authors remove their own; for
photos only the sender and the owner see, make the read rule
`visitor_id == auth.id || auth.role == "owner"`. File rules cannot use `auth.checked`.

```json
{"kind":"files","site_passcode":"inherit","rules":{"version":1,"policies":[{"name":"read","command":"read","using":"true"},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"delete","command":"delete","using":"visitor_id == auth.id"},{"name":"owner","command":"all","using":"auth.role == \"owner\""}]}}
```

```js
await SH.requireSignIn();
await SH.storage.files('photos').put(crypto.randomUUID() + '.webp', preparedPhoto);
```
