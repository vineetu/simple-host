# Storage

## hack_event_storage_list_resources

<div data-tool="hack_event_storage_list_resources"></div>

**Event website: List storage resources**

List a site's saved-data resources (KV, SQLite, files) with each one's name, kind, preset, and access (who may read, add, edit, and delete), plus each SQLite table's own preset. preset legacy marks a resource saved before presets that keeps its old rules until you set a preset on it. Call it before reading or changing saved data, to get the exact resource name. It does not list a files resource's uploads: hack_event_storage_list_file_objects does (through run_tool if it is not in your tool list). Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string | yes | The event's name (slug) on Simple Hack. |

## hack_event_storage_get_usage

<div data-tool="hack_event_storage_get_usage"></div>

**Event website: Read site storage usage**

Show how much of a site's saved-data allowance is used: on Simple Host, KV and SQLite together (10,000,000 bytes) and files separately (10 MB); on Simple Hack, one 1,000,000-byte pool for all three. The site's own pages and images are counted elsewhere (list_sites). Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string | yes | The event's name (slug) on Simple Hack. |

## hack_event_storage_set_resource

<div data-tool="hack_event_storage_set_resource"></div>

**Event website: Create a storage resource or set its preset**

Create a saved-data resource on a site, or set who may read, add, edit, and delete in it. kind: kv (one JSON value per key), sqlite (tables), or files (uploads); it cannot change later. Pick a preset; the server enforces it on every request from the site's pages, whatever the page code does. The presets (read / add / edit / delete): public (anyone / owner / owner / owner): menus, catalogues, price lists, opening hours, schedules, FAQs, a gallery you fill. inbox (owner / anyone / owner / owner): quote requests, contact forms, waitlists, sign-ups, applications, feedback; anyone sends without signing in, only you read. wall (anyone / signed-in / owner / own): guestbooks, comments, reviews, a public RSVP or who's-coming list; authors can take back their own. records (own / signed-in / owner / owner): bookings, appointments, table reservations (even a "booking request"), orders, support tickets; the person signs in when they send, sees only their own and its status, you see all and set a status. personal (own / signed-in / own / own): profiles, saved settings, wishlists, notes, bookmarks, a cart that follows the person. board (signed-in / signed-in / signed-in / owner): potluck and sign-up sheets, shared task or shopping lists, a club roster; everyone signed in edits, only you delete. private (all owner, the default): admin data, inventory, internal notes, drafts. Lookalikes: a sign-up sheet people change is board, but one where each person adds and removes only their own entry is wall; a form only you read is inbox, but one where each person sees their own submissions is records; a wishlist only its owner sees is personal, but a wishlist others may view is wall; a gallery you fill is public on files, one visitors add to is wall on files. "own" means the signed-in person who added the entry; "owner" means the event's organisers: these hack_event_storage_* tools, and an organiser signed in on the event website's own address with the email they use on Simple Hack, where a page can act as an admin page (all questions, mark one answered). That stops when the event ends. "nobody" keeps data to these tools only. For sqlite, the preset is the database default and tables can each have their own: {"kind":"sqlite","preset":"private","tables":{"products":{"preset":"public"},"orders":{"preset":"records"}}}. A preset takes single-action overrides, checked by the server, e.g. {"preset":"inbox","add":"signed-in"}; preset custom sets all four. The server refuses: edit or delete anyone; add own; own anywhere with add anyone; edit or delete wider than read. Create the resource, and its tables with hack_event_storage_sql_schema, before publishing the page that uses it; hack_get_page_recipe has a complete page for each job (booking, inbox, public, portal, and the rest). Ask the person before making data readable or changeable by more people. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | The resource's kind and preset, e.g. {"kind": "sqlite", "preset": "records"}. |
| event | string | yes | The event's name (slug) on Simple Hack. |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |

## hack_event_storage_delete_resource

<div data-tool="hack_event_storage_delete_resource"></div>

**Event website: Delete storage resource**

Remove a resource and everything saved in it, for good. Ask the person to confirm this exact resource first. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string | yes | The event's name (slug) on Simple Hack. |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |

## hack_event_storage_list_kv_keys

<div data-tool="hack_event_storage_list_kv_keys"></div>

**Event website: List KV keys**

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

**Event website: Read KV value**

Read one key's JSON value from a KV resource. The answer is {key, value}. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string | yes | The event's name (slug) on Simple Hack. |
| key | string | yes | The key within the KV resource, e.g. menu or settings. |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |

## hack_event_storage_put_kv

<div data-tool="hack_event_storage_put_kv"></div>

**Event website: Set KV value**

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

**Event website: Delete KV value**

Delete one key and its value from a KV resource. Ask first. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string | yes | The event's name (slug) on Simple Hack. |
| key | string | yes | The key within the KV resource, e.g. menu or settings. |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |

## hack_event_storage_sql_query

<div data-tool="hack_event_storage_sql_query"></div>

**Event website: Query SQLite resource**

Read rows from a SQLite resource with a SELECT, as the owner: it sees every person's rows, whatever the preset. Read-only; put values in params with ? placeholders; on Simple Host, visitor_id values from results can be passed back as parameters. Use it to show the person their orders, entries, or messages. No PRAGMA: list tables and columns with SELECT name, sql FROM sqlite_schema. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string | yes | The event's name (slug) on Simple Hack. |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |
| params | array of any |  | Values for the ? placeholders, in order. Omit or [] when there are none. |
| sql | string | yes | One SQLite statement, with ? placeholders for values. |

## hack_event_storage_sql_execute

<div data-tool="hack_event_storage_sql_execute"></div>

**Event website: Execute SQLite statement**

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

**Event website: Change SQLite schema**

Create or change tables in a SQLite resource: CREATE TABLE, CREATE INDEX, CREATE TRIGGER, ALTER TABLE, one statement per call. Use IF NOT EXISTS for supported CREATE statements. Check the current schema with SELECT name, sql FROM sqlite_schema (no PRAGMA) before repeating ALTER TABLE. Use id INTEGER PRIMARY KEY and a created_at TEXT column (and updated_at TEXT where rows are edited); the server fills them. Do not declare visitor_id: on every table whose preset uses own or lets visitors add (inbox, wall, records, personal, board) the server adds an indexed visitor_id column itself. A foreign key with ON DELETE CASCADE stops pages deleting the parent row, since that would change another table; leave cascades out of tables pages delete from. Ask before a change that drops or reshapes data. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string | yes | The event's name (slug) on Simple Hack. |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |
| params | array of any |  | Values for the ? placeholders, in order. Omit or [] when there are none. |
| sql | string | yes | One SQLite statement, with ? placeholders for values. |

## hack_event_storage_list_file_objects

<div data-tool="hack_event_storage_list_file_objects"></div>

**Event website: List stored files**

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

**Event website: Upload stored file**

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

**Event website: Delete stored file**

Delete one file from a files resource. Ask first. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string | yes | The event's name (slug) on Simple Hack. |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |
| path | string | yes | The file's path within the files resource, e.g. photos/cake.webp (no leading slash). |

## hack_event_storage_file_download_link

<div data-tool="hack_event_storage_file_download_link"></div>

**Event website: Get stored file download link**

Make a private link, good for ten minutes, to download one stored file in full, for example a photo a visitor uploaded, so the person can open it. Do not post it anywhere public. Organiser only on this event's custom website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string | yes | The event's name (slug) on Simple Hack. |
| name | string | yes | The resource's name (storage_list_resources shows the names; storage_set_resource creates one). |
| path | string | yes | The file's path within the files resource, e.g. photos/cake.webp (no leading slash). |

## hack_get_page_recipe

<div data-tool="hack_get_page_recipe"></div>

**Page code for bookings, forms, menus, client portals, and visitor uploads**

Return the exact owner setup and a complete, working page for a common job, so you do not have to guess the API. Pick the topic from what the person asked for: booking (any booking, appointment, or table reservation, even a "booking request": the customer signs in with their email when they book and sees their bookings and status; the owner confirms from chat), inbox (a quote request, contact form, waitlist, sign-up, volunteer or job application form, feedback: anyone sends without signing in, only the owner reads), public (a menu, prices, opening hours, or stock list the owner changes from chat and everyone reads), portal (a client portal or client documents: one private page per client, opened only to that client's email), comments (a guestbook, comments, reviews, or questions anyone posts without signing in, after a spam check; the owner removes or approves posts), wall (reviews, comments, community posts everyone reads; people sign in to post, and can take back their own), board (a shared list people sign in to edit together: shifts, tasks, a sign-up sheet), personal (things each visitor keeps and changes for themselves: favourites, measurements, a checklist, tracking what they applied for), private (the owner's own notes or log on the site), records (a shop with a cart and orders), admin (an owner page on the site that lists all orders or bookings and sets a status), gallery (photos visitors upload that everyone sees), wordpress (moving the person's WordPress site here from their own export: the import link, the export clicks, and which Simple Host feature replaces each WordPress feature). Visitors never make an account or a password: visitor sign-in is an emailed code or Google, and only where the job needs it. Call it before writing any page that saves what visitors send, shows saved data, or takes visitors' uploads; then run its setup calls, adapt its page to the person's site, and publish. On Simple Hack the recipe starts with a note on who the owner is there (the team, or the event's organisers) and which tools to use; topic portal is not available there, nor comments or guestbook (no spam check there: use wall, where people sign in to post); topic people does not exist there at all (for and the people pick list are Host only).

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| site | string |  | The site's name, so the page code comes back filled in. Optional before the site exists. |
| topic | string | yes | booking, inbox, public, portal, wall, comments, board, personal, private, records, admin, people, gallery, or wordpress. reservation and appointment are booking; quote, waitlist, contact, and form are inbox; menu is public; guestbook is comments. |
