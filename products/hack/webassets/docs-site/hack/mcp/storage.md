# Storage

## hack_event_storage_list_resources

<div data-tool="hack_event_storage_list_resources"></div>

List a site's saved-data resources with their names and kinds. Each resource returns rules, rules_status (rules or invalid), rules_errors when invalid, and per-table rules only for overrides. Missing table overrides inherit the resource rules; an empty policy set denies visitors. Read this before changing saved data or access.

Uploads in a files resource are listed by hack_event_storage_list_file_objects (through run_tool if absent from your tool list). Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string | yes | The event's name (slug) on Simple Hack. |

## hack_event_storage_get_usage

<div data-tool="hack_event_storage_get_usage"></div>

Show how much of a site's saved-data allowance is used: on Simple Host, KV and SQLite together (10,000,000 bytes) and files separately (10 MB); on Simple Hack, one 1,000,000-byte pool for all three. The site's own pages and images are counted elsewhere (list_sites). Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string | yes | The event's name (slug) on Simple Hack. |

## hack_event_storage_set_resource

<div data-tool="hack_event_storage_set_resource"></div>

Create a saved-data resource or set who may read, add, edit and delete. kind: kv (one JSON value per key), sqlite (tables), or files (uploads); it cannot change later.

For SQLite, SQLite-stored key-value data and files, ask who should read and write which data, then supply rules: {version:1, policies:[...]}. Deny by default: without a permissive policy for the command, visitors cannot run it; an empty policies array denies every visitor. Permissive policies combine with OR; restrictive policies combine with AND and cannot grant access alone. Table rules replace the resource rules for that table; tables without an override inherit the resource rules. Send tables:{} when replacing existing table settings with one resource rule set. Without rules, a new resource is owner-only and an existing one keeps its rules. Presets were removed and their fields are refused.

Clause table (command | existing row using | new row check):
read | required | refused
add | refused | required
edit | required | optional, defaults to using
delete | required | refused
all | required for read/edit/delete | optional for add/edit, defaults to using
A command without a matching permissive policy is denied regardless of these clause defaults. Edit and delete also require read visibility. Anonymous edits and deletes are refused. Use fixed page methods SH.storage.sqlite(name).table(t).list/get/add/edit/delete; visitors never send SQL. Bindings are trusted server facts: auth.id, auth.signed_in, auth.role; visitor_id is the row's author. KV rules use key, value and visitor_id; value is the whole JSON text, with no nested JSON access. File rules use path and visitor_id, the signed-in uploader. Owner tools bypass rules; the owner signed in on the site's own address does not: include auth.role == "owner" when that page should act as an admin page.

Three example rule sets:
{"version":1,"policies":[{"name":"read","command":"read","using":"true"},{"name":"manage","command":"all","using":"auth.role == \"owner\"","check":"auth.role == \"owner\""}]}
{"version":1,"policies":[{"name":"send","command":"add","check":"true"},{"name":"manage","command":"all","using":"auth.role == \"owner\"","check":"auth.role == \"owner\""}]}
{"version":1,"policies":[{"name":"read","command":"read","using":"visitor_id == auth.id || auth.role == \"owner\""},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"manage","command":"all","using":"auth.role == \"owner\"","check":"auth.role == \"owner\""}]}

For kind: "files", rules are written the same way: read lists and opens files, add uploads a new path, edit replaces an existing file, delete removes one. Uploads are never spam-checked.

For every resource kind, site_passcode applies alongside rules: inherit requires the site's passcode; off exempts this resource. Omission defaults to inherit, so preserve an existing off exemption when saving.

Create the resource with hack_event_storage_set_resource, then create its tables with hack_event_storage_sql_schema only for SQLite, before publishing the page. Ask before widening access. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | The resource's kind and access rules, e.g. {"kind": "files", "rules": {...}}. |
| event | string | yes | The event's name (slug) on Simple Hack. |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |

## hack_event_storage_delete_resource

<div data-tool="hack_event_storage_delete_resource"></div>

Remove a resource and everything saved in it, for good. Ask the person to confirm this exact resource first. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string | yes | The event's name (slug) on Simple Hack. |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |

## hack_event_storage_list_kv_keys

<div data-tool="hack_event_storage_list_kv_keys"></div>

List the keys in a KV resource, optionally only those starting with a prefix. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| after | string |  | Optional: the next_after value from the previous answer, to get the next page. |
| event | string | yes | The event's name (slug) on Simple Hack. |
| limit | integer |  | Optional: at most this many results (the server caps it). |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |
| prefix | string |  | Optional: only keys or paths that start with this. |

## hack_event_storage_get_kv

<div data-tool="hack_event_storage_get_kv"></div>

Read one key's JSON value from a KV resource. The answer is {key, value}. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string | yes | The event's name (slug) on Simple Hack. |
| key | string | yes | The key within the KV resource, e.g. menu or settings. |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |

## hack_event_storage_put_kv

<div data-tool="hack_event_storage_put_kv"></div>

Set one key in a KV resource to a JSON value, replacing what was there. It changes a page only when the page's code already reads that key (SH.storage.kv(name).get(key)). This is how the owner changes page info a page reads live (a menu, prices, opening hours, settings) without republishing the site. Ask before overwriting a value visitors wrote. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string | yes | The event's name (slug) on Simple Hack. |
| key | string | yes | The key within the KV resource, e.g. menu or settings. |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |
| value | any | yes | The JSON value to store: a string, number, object, array, true, false, or null. |

## hack_event_storage_delete_kv

<div data-tool="hack_event_storage_delete_kv"></div>

Delete one key and its value from a KV resource. Ask first. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string | yes | The event's name (slug) on Simple Hack. |
| key | string | yes | The key within the KV resource, e.g. menu or settings. |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |

## hack_event_storage_sql_query

<div data-tool="hack_event_storage_sql_query"></div>

Read rows from a SQLite resource with a SELECT as the owner: owner tools see every person's rows and bypass row rules. Read-only; put values in params with ? placeholders; on Simple Host, visitor_id values from results can be passed back as parameters. Use it to show the person their orders, entries, or messages. No PRAGMA: list tables and columns with SELECT name, sql FROM sqlite_schema. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string | yes | The event's name (slug) on Simple Hack. |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |
| params | array of any |  | Values for the ? placeholders, in order. Omit or [] when there are none. |
| sql | string | yes | One SQLite statement, with ? placeholders for values. |

## hack_event_storage_sql_execute

<div data-tool="hack_event_storage_sql_execute"></div>

Change rows in a SQLite resource as the owner: INSERT, UPDATE, or DELETE with ? placeholders, for example setting an order's status; on Simple Host, visitor_id values from results can be passed back as parameters. Ask before changing or deleting what visitors sent. Repeating a successful statement can add duplicate rows or apply a change again, so query the affected records before retrying after an uncertain result. No PRAGMA: list tables and columns with SELECT name, sql FROM sqlite_schema. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string | yes | The event's name (slug) on Simple Hack. |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |
| params | array of any |  | Values for the ? placeholders, in order. Omit or [] when there are none. |
| sql | string | yes | One SQLite statement, with ? placeholders for values. |

## hack_event_storage_sql_schema

<div data-tool="hack_event_storage_sql_schema"></div>

Create or change tables in a SQLite resource: CREATE TABLE, CREATE INDEX, CREATE TRIGGER, ALTER TABLE, one statement per call. Use IF NOT EXISTS for supported CREATE statements. Check the current schema with SELECT name, sql FROM sqlite_schema (no PRAGMA) before repeating ALTER TABLE. Use id INTEGER PRIMARY KEY and created_at TEXT (and updated_at TEXT where rows are edited); the server fills them. Do not declare visitor_id: the server ensures the indexed author column when rules reference visitor_id or allow ordinary visitors to add. If rules name approved_at, the server ensures that column for moderation. A table override replaces the resource rule set; install rules for a new table name before renaming a table with its own rules. A foreign key with ON DELETE CASCADE stops pages deleting the parent row, since that would change another table; leave cascades out of tables pages delete from. Ask before a change that drops or reshapes data. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string | yes | The event's name (slug) on Simple Hack. |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |
| params | array of any |  | Values for the ? placeholders, in order. Omit or [] when there are none. |
| sql | string | yes | One SQLite statement, with ? placeholders for values. |

## hack_event_storage_list_file_objects

<div data-tool="hack_event_storage_list_file_objects"></div>

List the files in a files resource, optionally only those under a path prefix. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| after | string |  | Optional: the next_after value from the previous answer, to get the next page. |
| event | string | yes | The event's name (slug) on Simple Hack. |
| limit | integer |  | Optional: at most this many results (the server caps it). |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |
| prefix | string |  | Optional: only keys or paths that start with this. |

## hack_event_storage_put_file

<div data-tool="hack_event_storage_put_file"></div>

Upload one file into a files resource as the owner (base64, under 1 MiB; resize photos first): a gallery photo, a menu PDF, a download. A page shows it with SH.storage.files(name).url(path). Images that are part of the page's design go in create_site or update_site files_base64 instead. Ask before replacing an existing file. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| content_base64 | string | yes | The whole file in standard base64, at most 1 MiB decoded. |
| content_type | string | yes | The file's MIME type, e.g. image/webp, image/jpeg, or application/pdf. |
| event | string | yes | The event's name (slug) on Simple Hack. |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |
| path | string | yes | The file's path within the files resource, e.g. photos/cake.webp (no leading slash). |

## hack_event_storage_delete_file

<div data-tool="hack_event_storage_delete_file"></div>

Delete one file from a files resource. Ask first. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string | yes | The event's name (slug) on Simple Hack. |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |
| path | string | yes | The file's path within the files resource, e.g. photos/cake.webp (no leading slash). |

## hack_event_storage_file_download_link

<div data-tool="hack_event_storage_file_download_link"></div>

Make a private link, good for ten minutes, to download one stored file in full, for example a photo a visitor uploaded, so the person can open it. Do not post it anywhere public. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string | yes | The event's name (slug) on Simple Hack. |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |
| path | string | yes | The file's path within the files resource, e.g. photos/cake.webp (no leading slash). |

## hack_get_page_recipe

<div data-tool="hack_get_page_recipe"></div>

Return the exact owner setup and a complete, working page for a common job, so you do not have to guess the API. Pick the topic from what the person asked for: booking (any booking, appointment, or table reservation, even a "booking request": the customer signs in with their email when they book and sees their bookings and status; the owner confirms from chat), inbox (a quote request, contact form, waitlist, sign-up, volunteer or job application form, feedback: anyone sends without signing in, only the owner reads), public (a menu, prices, opening hours, or stock list the owner changes from chat and everyone reads), portal (a client portal or client documents: one private page per client, opened only to that client's email), comments (a guestbook, comments, reviews, or questions anyone posts without signing in, after a spam check; the owner removes or approves posts), wall (reviews, comments, community posts everyone reads; people sign in to post, and can take back their own), board (a shared list people sign in to edit together: shifts, tasks, a sign-up sheet), personal (things each visitor keeps and changes for themselves: favourites, measurements, a checklist, tracking what they applied for), private (the owner's own notes or log on the site), records (a shop with a cart and orders), admin (an owner page on the site that lists all orders or bookings and sets a status), gallery (photos visitors upload that everyone sees). Visitors never make an account or a password: visitor sign-in is an emailed code or Google, and only where the job needs it. Call it before writing any page that saves what visitors send, shows saved data, or takes visitors' uploads; then run its setup calls, adapt its page to the person's site, and publish. Topic event builds the organiser’s event website from the live public event feed. On Simple Hack the storage recipes start with a note on who the owner is there (the team, or the event's organisers) and which tools to use; topic portal is not available there, nor comments or guestbook (no spam check there: use wall, where people sign in to post); topic people does not exist there at all (for and the people pick list are Host only).

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| site | string |  | The site's name, so the page code comes back filled in. Optional before the site exists. |
| topic | string, one of `event`, `booking`, `inbox`, `public`, `portal`, `wall`, `comments`, `board`, `personal`, `private`, `records`, `admin`, `gallery`, `reservation`, `appointment`, `quote`, `waitlist`, `contact`, `form`, `menu`, `guestbook` | yes | event (the organiser’s live event website), or booking, inbox, public, portal, wall, comments, board, personal, private, records, admin, people, or gallery. reservation and appointment are booking; quote, waitlist, contact, and form are inbox; menu is public; guestbook is comments. |
