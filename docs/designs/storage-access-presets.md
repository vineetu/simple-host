# Storage access presets

> **Status: Proposed (2026-10-10), not built.** Design only, on branch `design/storage-access-presets`. Nothing here is implemented or merged. Showcase page: https://storage-presets.vineetu.simple-host.app/. Builds on `docs/designs/site-storage-primitives.md` (today's read/write/write_mode contract), which stays the source of truth until this ships.

## The ask

The owner's direction (2026-10-10):

- Keep the model small: a fixed set of policies, not a rule language like PocketBase rules or Supabase row-level security.
- Make the policies generic, so each one covers many kinds of site rather than one use case.
- The capability must be unbreakable from the page. The server enforces every policy, whatever the AI writes in the page. Picking the wrong preset is the AI's mistake; no preset can be bypassed.
- 10 MB for tables and key-value data, as well as files. (Shipped separately on 2026-10-10; see Limits.)

## Today

Each KV namespace, SQLite database, and file bucket has three settings:

| Setting | Values |
|---|---|
| `read` | `anyone`, `signed-in`, `owner`, `own` |
| `write` | `anyone`, `signed-in`, `owner` |
| `write_mode` | `full`, `add` |

That covers the common cases, but it has three gaps:

1. **Add, edit, and delete are one switch.** `write` plus `write_mode` can say "anyone may change anything" or "people may only add". It cannot say "people add, and each person may delete their own entry", or "everyone edits, only the owner deletes".
2. **A policy covers the whole database.** A public products table and private orders need two databases, so an order cannot reference a product with a foreign key, and the owner cannot join them in one query.
3. **Full-mode SQLite gives visitors SQL.** On a `full` database with visitor read or write, the page sends parameterized SQL to `/query` and `/execute`. An authorizer guards it, but it is the one place a visitor's request is still a program rather than a fixed operation.

Measured on 2026-10-10: hosted Simple Host has **0** storage resources (372 sites), and hosted Simple Hack has **0**. Every site that saves data still uses the older state and collections APIs. That leaves room to define the model cleanly now. Compatibility matters for the API contract, skills already installed in people's AI apps, and small boxes, not for live hosted data.

## The model

Every resource (and, for SQLite, every table) has an **access matrix**: four actions, each given one "who" value.

### The four actions

| Action | Covers | KV | SQLite table | Files |
|---|---|---|---|---|
| **read** | list and view | list keys, get a key | list rows, get a row | list paths, get a file |
| **add** | create a new entry | put a key that does not exist yet | insert a row | put a path that does not exist yet |
| **edit** | change an existing entry | put a key that exists | update columns of a row | replace the bytes at a path that exists |
| **delete** | remove an entry | delete a key | delete a row | delete a path |

The server decides whether a KV or file `PUT` is an add or an edit, by looking inside the same transaction. The page cannot choose.

### The five "who" values

From narrowest to widest:

| Value | Who it lets in |
|---|---|
| **nobody** | No page request at all. Only the owner's own tools (their key, the connector, the dashboard). |
| **owner** | The site owner. |
| **own** | The person who added that entry, plus the owner. Needs sign-in. |
| **signed-in** | Anyone signed in on this site, plus the owner. |
| **anyone** | Anyone, signed in or not. |

Two rules hold for every value:

- **The owner's tools always have full control.** The owner's API key and the connector bypass the matrix for reading, correcting, exporting, and deleting. The matrix governs requests from the site's pages. This is today's behaviour and does not change.
- **Each value includes the ones narrower than it, except nobody.** `signed-in` includes `own` people, and `own` includes the owner.

**Phase 1 note on owner versus nobody.** Hosted pages never hold an owner credential, so in phase 1 "the owner" means the owner's tools, and `owner` and `nobody` behave the same for page requests. They are kept as two values because they mean different things once the owner can act from their own signed-in session on the site (decision D1 below): `owner` would then allow it, and `nobody` would not. Agents and docs should already pick the one that states the intent.

### What is allowed and what is refused

5 × 5 × 5 × 5 = 625 matrices. Four rules refuse the unsafe or meaningless ones and leave **161** valid matrices. The server checks these rules when the owner saves a policy (`400 invalid_access` with the rule's code and a plain hint), so a bad matrix never exists.

| Rule | Refused | Why |
|---|---|---|
| **R1. No anonymous changes.** | `edit` or `delete` set to `anyone` | An anonymous visitor has no identity to hold to account, and could overwrite or wipe the whole resource. Anonymous **add** is allowed (contact forms), anonymous edit and delete are not. |
| **R2. Add is never own.** | `add: own` | Nothing is owned before it exists. Use `signed-in`; the server records who added each entry. |
| **R3. Own needs signed-in adders.** | any action `own` while `add: anyone` | An anonymous entry has no author, so "own" could never apply to it, and it would silently become owner-only. |
| **R4. You cannot change what you cannot see.** | `edit` or `delete` wider than `read` | Editing or deleting blind lets a visitor probe for entries they may not read. |

Rules counted over all 625 matrices (a matrix can break more than one): R4 refuses 350, R1 225, R2 125, R3 61.

Combinations that are allowed and worth knowing:

- `read: nobody` (or `owner`) with `add: anyone`: a drop box. People send; only the owner's tools read.
- `read: anyone` with `delete: own`: a public wall where authors can take back their own post.
- `edit: signed-in` with `delete: owner`: people collaborate, only the owner removes.
- Everything `nobody`: data the page never touches, filled and read only through the owner's tools.

## The presets

Seven named presets, each a fixed matrix. An agent sets `"preset": "records"`; the server stores the matrix. A custom matrix is allowed within the four rules, but the skills and recipes steer agents to a preset first.

| # | Preset (`id`) | read | add | edit | delete |
|---|---|---|---|---|---|
| 1 | Public content (`public`) | anyone | owner | owner | owner |
| 2 | Inbox (`inbox`) | owner | anyone | owner | owner |
| 3 | Wall (`wall`) | anyone | signed-in | owner | own |
| 4 | Each person's records (`records`) | own | signed-in | owner | owner |
| 5 | Personal data (`personal`) | own | signed-in | own | own |
| 6 | Shared board (`board`) | signed-in | signed-in | signed-in | owner |
| 7 | Private (`private`) | owner | owner | owner | owner |

`private` is the default for a new resource, as owner-only is today.

Two small departures from the examples in the brief, both within its intent:

- **Wall deletes are `own`, not `owner`.** The owner can still delete any post (`own` includes the owner), and a guest can also take back their own comment, photo, or RSVP. That is what people expect from a guestbook or a "who's coming" list, and it is still safe: the delete carries `WHERE visitor_id = caller`.
- **Inbox has one variant, not two presets.** "Signed-in adds" is the same matrix with `add: signed-in`, written as `{"preset":"inbox","add":"signed-in"}`. Any preset accepts single-action overrides; the result is checked against R1 to R4 and reported as `custom` with the base preset named.

### What each preset covers

| Preset | Real uses |
|---|---|
| **Public content** | menus, catalogs, price lists, opening hours, event schedules, team pages, FAQs, portfolios, a gallery the owner fills, downloads, blog posts, announcements, product photos, a reading list |
| **Inbox** | contact forms, feedback, surveys, quote requests, bug reports, newsletter sign-ups, anonymous tips, suggestion boxes, event enquiries, volunteer offers |
| **Wall** | guestbooks, comments, reviews, a public RSVP list, wedding wishes, a visitor photo gallery, shout-outs, a public "who's coming", community recommendations, a leaderboard of submitted scores |
| **Each person's records** | shop orders, bookings, appointments, RSVPs with a status, job and club applications, hackathon registrations, support tickets, homework submissions, a waitlist with "my place in line", insurance or refund claims, class enrolments |
| **Personal data** | profiles, saved settings, drafts, wishlists, notes, bookmarks, favourites, a reading tracker, a habit log, a cart that follows the person across devices, a personal journal |
| **Shared board** | team task lists, potlucks, sign-up sheets with swaps, a shared shopping list, a club roster, group trip planning, a family chore chart, a class reading list, a small kanban |
| **Private** | admin data, inventory, internal notes, configuration, content drafts before they go public, a CRM, the owner's own analytics, price costings |

One site often uses several presets: a bakery has `public` products, `records` orders, and an `inbox` for enquiries.

## SQLite: per table, with a database default

**Decision: the matrix applies per table.** The database resource keeps its name, its file, and its place in the 10 MB allowance. It carries a default matrix, and each table may set its own:

```json
PUT /v1/sites/{site}/storage/resources/shop
{"kind": "sqlite", "preset": "private",
 "tables": {"products": {"preset": "public"},
            "orders":   {"preset": "records"},
            "order_changes": {"preset": "records"}}}
```

A table without an entry uses the database default. A table the owner creates later starts on that default, so it is never more open than the owner chose.

Why per table rather than per database:

- One database can hold public products and private orders, so `orders.product_id REFERENCES products(id)` works, and the owner can join them in one query.
- It costs nothing in safety. With visitor SQL gone (below), every visitor request names exactly one table, so the table's matrix is the whole decision.
- KV namespaces and file buckets have no tables; their matrix is the resource's.

**Foreign keys respect read access.** When a visitor adds or edits a row that references another table, the parent row must exist and be readable by that visitor under the parent table's `read`. Otherwise the answer is `404 invalid_reference`, the same as a missing parent, so a reference cannot be used to test whether a private row exists. This generalises today's own-parent check.

## Enforcement: no visitor SQL

Under this model **a visitor never sends SQL, on any database**. Pages use fixed routes; the server builds every statement.

| Action | Route | Statement the server builds |
|---|---|---|
| read (list) | `GET .../sqlite/{db}/tables/{t}/rows?order=&desc=&limit=&after=&where.{col}=` | `SELECT {cols} FROM "t" WHERE [visitor_id = :caller AND] [col = ? ...] ORDER BY ... LIMIT ?` |
| read (view) | `GET .../tables/{t}/rows/{id}` | `SELECT {cols} FROM "t" WHERE rowid = ? [AND visitor_id = :caller]` |
| add | `POST .../tables/{t}/rows` | `INSERT INTO "t" (cols..., visitor_id, created_at) VALUES (?, ..., :caller, :now)` |
| edit | `PATCH .../tables/{t}/rows/{id}` | `UPDATE "t" SET col = ?, ... [, updated_at = :now] WHERE rowid = ? [AND visitor_id = :caller]` |
| delete | `DELETE .../tables/{t}/rows/{id}` | `DELETE FROM "t" WHERE rowid = ? [AND visitor_id = :caller]` |

The bracketed `visitor_id = :caller` is added when the action's value is `own`. `:caller` comes from the site session cookie, never from the request body.

What makes this hold whatever the page does:

1. **The policy lives on the server.** The page sends an action and values. It cannot send, widen, or name a policy.
2. **Identifiers are checked, values are bound.** Table and column names are matched against the real schema (`PRAGMA table_info`) and quoted. Every value is a bound parameter. Unknown columns are refused, not ignored.
3. **Server-owned columns.** `id`, `visitor_id`, `created_at`, and `updated_at` are set by the server and refused if a visitor tries to write them. An edit can never move a row to another person.
4. **Ownership is checked in the statement itself.** `own` edits and deletes put `visitor_id = caller` in the same `UPDATE` or `DELETE`, so there is no check-then-act gap. Zero rows changed answers `404`, the same as a missing row, so another person's row is indistinguishable from none.
5. **List filters are equality only**, on real columns, at most three, bound as parameters. No operators, no expressions, no sort on anything but a real column.
6. **Triggers and side effects are confined.** Visitor statements run under the SQLite authorizer that permits only the target table; a trigger writing elsewhere fails the statement, as add-only inserts do today.
7. **The existing gates still run first,** in today's order: the site's named viewers or passcode, CSRF (`X-SH-CSRF: 1`) on every change, sign-in where the value needs it, the suspended-account check, and the per-site visitor write buckets.
8. **KV and files use the same rules.** On add the server stamps `visitor_id` and `created_at` on the key or object; `own` edits and deletes compare the stored `visitor_id`; an edit by a `signed-in` visitor on a board keeps the original author.

The owner's tools keep raw parameterized SQL (`/query`, `/execute`, `/schema`): the owner is not a visitor.

**How we prove it.** A table-driven test enumerates all 625 matrices × four callers (anonymous, signed-in, author, owner via tools) × four actions × three kinds, and checks the server's answer against one reference table. The same table, as JSON, drives the explorer on the showcase page, so the page cannot describe a rule the server does not enforce. Like every storage and permissions change, the build gets a security review before it ships (CLAUDE.md).

## Compatibility

The old fields stay accepted and are translated on write. Responses carry both forms.

| Today (`read`, `write`, `write_mode`) | New matrix (read / add / edit / delete) | Preset |
|---|---|---|
| X, `owner`, `full` | X / owner / owner / owner | `public` if X is anyone, `private` if X is owner |
| X, `signed-in`, `full` | X / signed-in / signed-in / signed-in, then R4 narrows edit and delete to X | `board` when X is signed-in, except delete |
| X, W, `add` | X / W / owner / owner | `inbox` (owner, anyone), `wall` (anyone, signed-in) apart from delete, `records` (own, signed-in) |
| X, `anyone`, `full` | **refused for new resources** (R1) | none |

Notes:

- **`write: anyone` with `full` is the one combination the new model drops.** It lets an anonymous visitor overwrite or delete anything. New requests get `400 invalid_access` with code `anonymous_change` and a hint to use `inbox` or `wall`. An existing resource with that setting (possible on a small box) keeps working as `legacy` until the owner changes it; the dashboard and `storage_list_resources` label it.
- **Visitor SQL on existing full-mode databases.** Hosted has none. On a small box, an existing `full` database with visitor read or write keeps its raw routes, marked `legacy`, until the owner saves any new policy on it. New databases never get them.
- **The response** includes `preset` (or `custom`, or `legacy`), `access` (the matrix), `tables` for SQLite, and the old `read`, `write`, `write_mode` whenever the matrix can be expressed in them, so an older skill reading the response is not confused.
- **The browser helpers grow, not change.** `SH.storage.sqlite(name).table(t).add()` and `.list()` keep working; `.get(id)`, `.edit(id, values)`, and `.delete(id)` are added. KV and files keep `get`, `set`, `put`, `url`, `list`; `delete` is added where missing.
- **Simple Hack keeps its current model** (resource-wide `read` and `write`, full mode only) in phase 1, as it keeps its own allowance. Presets there are a separate decision.
- **Enterprise** has no per-site storage routes; PARITY.md records this as `different on purpose`, as today.

## Limits

Shipped 2026-10-10, independent of the presets:

| Allowance | Simple Host | Simple Hack |
|---|---|---|
| KV and SQLite together, per website | 10,000,000 bytes (10 MB), `SITE_STORAGE_MAX_BYTES` | 1,000,000 bytes, pooled with files |
| Files, per website | 10,000,000 bytes (10 MB), `SITE_STORAGE_FILES_MAX_BYTES` | in the pool above |
| One file upload | 1,000,000 bytes, `SITE_STORAGE_FILE_MAX_BYTES` | same |
| One SQLite query result | 1,000,000 bytes, `SITE_STORAGE_SQL_RESULT_MAX_BYTES` | same |

`GET /v1/sites/{site}/storage/usage` and `storage_get_usage` report the live limits.

## API sketch

```json
PUT /v1/sites/{site}/storage/resources/{name}
{"kind": "kv" | "sqlite" | "files",
 "preset": "public" | "inbox" | "wall" | "records" | "personal" | "board" | "private",
 "read": "...", "add": "...", "edit": "...", "delete": "...",   // optional single-action overrides
 "tables": {"<table>": {"preset": "...", "read": "..."}},     // SQLite only
 "site_passcode": "inherit" | "off"}
```

Connector: `storage_set_resource` takes the same fields; its description lists the seven presets with one line each. `get_page_recipe` topics map to presets: `form` uses `inbox`, `records` uses `records`, `gallery` uses `wall` on files.

## Decisions for the owner

- **D1. May the owner act from the site itself?** Preset 4 reads "the owner reads all and edits status". In phase 1 that happens through the owner's AI tools, as today. The server can already recognise the owner signed in on their own site (named viewers let the owner in that way), so `owner` could also cover that session and allow an owner-only admin page inside the site. The cost: if the page renders a visitor's text unsafely (for example, `innerHTML` on a comment), a malicious visitor's script would run with the owner's rights in the owner's browser, which is worse than today, where it runs only with the victim visitor's rights. **Recommendation:** ship phase 1 without it; consider it later, with a security review, limited to read and edit.
- **D2. Should Simple Hack adopt presets?** Recommendation: not in phase 1. Hack sites are short-lived event sites, and the current model has had no reported gap there.

Everything else above is an engineering choice made in this design: per-table SQLite, the four rules, the route shapes, the compatibility translation, and the test plan.

## Phases

1. Matrix storage (additive columns on `site_storage_resources`, plus a per-table table), validation, translation of the old fields, the new routes, the browser helpers, and the reference-table test. Owner tools unchanged.
2. Skills, recipes, `llms.txt`, OpenAPI, connector descriptions, and the dashboard's Storage panel move to presets. FEATURES.md §23, PARITY.md, and the CHANGELOG are updated in the same commit.
3. Security review, then deploy, then browser checks on a live site per preset.
