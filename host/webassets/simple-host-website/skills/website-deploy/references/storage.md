# Saved data: namespaces, rules, owner SQL, the page helper

A site keeps data in **namespaces**. Each namespace is one SQLite file (made the first time you
use its name) with tables, named values (`_values`) and stored file records (`_files`). The owner
can run any SQL on it. Everyone else (pages, visitors, their AI apps) uses the row, value and file
routes, under the rules. Create tables before publishing pages that use them. Pages never hold a
key.

## Owner SQL: `list_tables`, `query_sql`, `run_sql`

Each takes `site` and `namespace`. `list_tables` lists the tables and the space used. `query_sql`
runs one statement that only reads (`SELECT`, `WITH ... SELECT`), with positional `?` and
`params`; anything that would change data is refused. `run_sql` runs one statement that changes
(`CREATE TABLE`, `ALTER`, `INSERT`, `UPDATE`, `DELETE`, `DROP`), making the namespace if new.
Never build SQL from visitor text. Use `id INTEGER PRIMARY KEY`. The server adds `visitor_id` (who added the row, an opaque
reference) and timestamps to a table, and ignores ones a page sends. In tool answers it shows as
`you` or `visitor-` and 8 hex characters, one label per person on the site in every answer
(any column, alias or expression), and rows, values and stored files say `mine` when they are the
caller's. A label in `where`, `params` or the SQL means that person. To answer "show my bookings"
use `query_sql`. To fix a row use `run_sql` with an `UPDATE`. Deleting rows or dropping a table is destructive:
ask first.

## Rules: `set_data_rules`

`set_data_rules` with `namespace` and `rules`: table name (also `_values`, `_files`) to
`{"version": 1, "policies": [...]}`. No rules: only the owner can touch the table. A policy has a
`command` (`read`, `add`, `edit`, `delete` or `all`), an optional `name`, and:

| Command | `using` (stored row) | `check` (the new row) |
|---|---|---|
| `read` | required | not allowed |
| `add` | not allowed | required |
| `edit` | required | optional, defaults to `using` |
| `delete` | required | not allowed |
| `all` | required for read, edit, delete | optional |

Policies of a command OR together. A policy marked `"restrictive": true` ANDs on top and narrows
the grant. No matching policy: refused. Expressions use the table's columns, the bindings below,
`true`, `false`, `&&`, `||`, `!`, parentheses, `==`, `!=`, `in`, `contains`, `is null`,
`is not null` and `lower()`. Bindings: `auth.id` (nullable), `auth.signed_in`, `auth.role`
(`"anon"`, `"visitor"`, `"owner"`) and `auth.checked` (the spam check passed; only in an `add`
check, and only with `spam_check: true`, which makes anonymous adds pass the spam check). A
mistake is refused with the clause and position: fix it, never widen access to get past it.
Pick the narrowest rule that does the job, and ask the person first when it widens who can read
or write.

Patterns (each is a table's value):

- Inbox, only the owner reads, anyone sends:
  `{"version":1,"policies":[{"name":"send","command":"add","check":"true"}]}`
- Public data the owner keeps (menu, prices):
  `[{"command":"read","using":"true"},{"command":"all","using":"auth.role == \"owner\""}]`
- Each signed-in person adds and reads their own, the owner reads and changes all (bookings,
  orders, RSVPs):
  `[{"command":"read","using":"visitor_id == auth.id || auth.role == \"owner\""},{"command":"add","check":"auth.signed_in"},{"command":"edit","using":"auth.role == \"owner\""},{"command":"delete","using":"auth.role == \"owner\""}]`
- `auth.signed_in` alone shares rows among everyone signed in; it is not per-person privacy.
- Every edit/delete/all `using` must require `auth.signed_in`, `visitor_id == auth.id` or
  `auth.role == "owner"`.

For `_files`, rules read over `path` and `visitor_id` (the uploader): read lists and opens, add
uploads a new path, edit replaces, delete removes. Always use `get_recipe` for the whole setup of
a job: it carries tested rules, SQL and a working page.

## Reading and fixing data from chat, with your rights

These call the same routes pages use, with your rights, on any site you may open. Each takes
`site` and `namespace`:

- Rows (`table`): `list_rows` (`where` as column -> value, `order`, `desc`, `limit`, `after`),
  `read_row`, `add_row` (`row`), `change_row` (`id`, `row`), `delete_row` (`id`).
- Values: `list_values` (`prefix`), `read_value`, `set_value` (`key`, `value`), `delete_value`.
- Stored files: `list_files` (`prefix`), `save_file` (`path`, `content_base64`, `content_type`),
  `delete_file` (`path`). For the person's own photos use `create_upload_link` instead.

For the whole picture the owner uses `query_sql`; these show what a visitor would get.
Deleting is final: ask first.

## In a page

```html
<script>window.SH_CONFIG = { site: "<site>" };</script>
<script src="https://simple-host.app/auth.js"></script>
```

- `SH.me()` who is signed in; `SH.signIn()`, `SH.signOut()`, `SH.requireSignIn()`,
  `SH.mount('#box')` shows the sign-in box.
- Rows: `SH.storage.sqlite(ns).table(t)` with `.add(row)`, `.list({where, order, desc, limit,
  after})`, `.get(id)`, `.edit(id, changes)`, `.delete(id)`.
- Values: `SH.storage.kv(ns).keys()`, `.get(k)`, `.set(k, v)`, `.delete(k)`.
- Files: `SH.storage.files(ns).list()`, `.get(path)`, `.put(path, data)`, `.upload(file)`,
  `await .url(path)` for an `<img src>`, `.delete(path)`.
- `SH.storage.toObjects(result)` turns a SQL result into objects. `query` and `execute` work only
  for the owner signed in on the site.
- Await `SH.requireSignIn()` before a save that needs a person. Show the error when a save fails;
  never claim success.
- Treat `sign_in_required` (401), `forbidden` (403), not found (404, also for rows the person may
  not read) and limit refusals as the server's decision.
- UNIQUE columns, value keys and file paths are global within a namespace, so a failed add can
  reveal that a value exists: use random names, never emails, as keys.

## Photos

The photo picker (`create_upload_link`, namespace `photos` unless you name another) shrinks photos on the person's device
and stores them in that namespace's file store; a new `photos` namespace is readable by anyone who can open the site and written only by the owner. A page shows them with
`SH.storage.files(ns).url(path)`; visitor uploads follow the `_files` rules.
