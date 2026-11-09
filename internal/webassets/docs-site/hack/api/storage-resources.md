# Storage resources

Owner-declared KV, SQLite and durable file resources.

## GET /v1/hack/events/{slug}/website/storage/{rest}

<div data-operation="GET /v1/hack/events/{slug}/website/storage/{rest}"></div>

Organiser-only alias for the matching /v1/sites/{sitename}/storage route, with {rest...} holding the resource path after /storage/. The event slug maps to the custom website's immutable event-ID site. Read-only access continues after archive; a removed organiser loses access. Browser visitors instead use /v1/sites/{slug}/storage on the custom event website's own origin, under the resource's preset; an organiser signed in there has owner rights for saved data until the event ends.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| rest | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The matching storage route response |
| 404 | Event |

## POST /v1/hack/events/{slug}/website/storage/{rest}

<div data-operation="POST /v1/hack/events/{slug}/website/storage/{rest}"></div>

Same suffix, body and policy as the corresponding /v1/sites/{sitename}/storage POST route. Organiser-only; archived or taken-down events refuse changes.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| rest | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The matching storage route response |
| 404 | Event |

## PUT /v1/hack/events/{slug}/website/storage/{rest}

<div data-operation="PUT /v1/hack/events/{slug}/website/storage/{rest}"></div>

Same suffix, body and policy as the corresponding /v1/sites/{sitename}/storage PUT route. Only a current organiser may write; archived or taken-down events refuse changes.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| rest | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The matching storage route response |
| 404 | Event |

## PATCH /v1/hack/events/{slug}/website/storage/{rest}

<div data-operation="PATCH /v1/hack/events/{slug}/website/storage/{rest}"></div>

Same suffix, body and policy as PATCH /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows/{id}. Organiser-only; archived or taken-down events refuse changes.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| rest | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The matching storage route response |
| 404 | Event |

## DELETE /v1/hack/events/{slug}/website/storage/{rest}

<div data-operation="DELETE /v1/hack/events/{slug}/website/storage/{rest}"></div>

Same suffix and policy as the corresponding /v1/sites/{sitename}/storage DELETE route. Organiser-only; archived or taken-down events refuse changes.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| rest | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The matching storage route response |
| 404 | Event |

## POST /v1/sites/{sitename}/storage/files/{name}/download-link

<div data-operation="POST /v1/sites/{sitename}/storage/files/{name}/download-link"></div>

Mint a ten-minute scoped full-byte download link (owner only)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| name | path | string | yes |  |
| Idempotency-Key | header | string |  | Opaque key for a create request. For 24 hours, the same authenticated account, POST path and query, key and exact request body replay the original handler status and body, including errors, without executing it again. A different body under that key returns 409 idempotency_key_reused. Concurrent identical requests wait for the first response. Stored only in this server's memory; a restart clears it. Authentication and rate limiting still apply to replays. A replay does not extend a minted link's expiry; use a new key to request a new link. Storage row creates support owner credentials and signed-in visitors; anonymous writes ignore this header. Storage replay also checks current resource permissions and is scoped to the resolved site. |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {url,expires_at}; bearer URL remains private and is rechecked when used |
| 409 | Idempotency-Key reused with a different body (idempotency_key_reused). |

## GET /v1/sites/{sitename}/storage/files/{name}/objects

<div data-operation="GET /v1/sites/{sitename}/storage/files/{name}/objects"></div>

Checked against the read value. Visitors get mine per item; the owner gets visitor_id. Own lists use the writer/path index with LIMIT before opening files. Temporary dot objects are excluded. Exact legacy prefixes and cursors remain accepted; cursors follow scanned metadata even if files are missing.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| name | path | string | yes |  |
| prefix | query | string |  |  |
| after | query | string |  |  |
| limit | query | integer |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | items: [{path,bytes,content_type,mine\|visitor_id}], next_after cursor |

## GET /v1/sites/{sitename}/storage/files/{name}/objects/{path}

<div data-operation="GET /v1/sites/{sitename}/storage/files/{name}/objects/{path}"></div>

Own reads return only the signed-in visitor's file; other paths return 404. The path may contain slash-separated segments. Only validated raster bytes render inline; other types are attachments with nosniff.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| name | path | string | yes |  |
| path | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Raw file bytes |
| 404 | file_not_found |

## PUT /v1/sites/{sitename}/storage/files/{name}/objects/{path}

<div data-operation="PUT /v1/sites/{sitename}/storage/files/{name}/objects/{path}"></div>

The server decides add (a new path, checked against add, published without replacing) or edit (an existing path, checked against edit; own edits only by the author, who stays the author). A caller who may add but not edit gets 409 file_exists. The owner's tools can always replace. File growth beyond the separate website allowance returns site_full with photo-shrinking tips. Empty files count toward SITE_STORAGE_FILES_MAX_OBJECTS (1000 per bucket by default); bucket_full is 507. New upload paths are normalized to NFC; reads and deletes accept exact legacy paths. Paths allow at most 16 segments.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| name | path | string | yes |  |
| path | path | string | yes |  |

**Request body:** application/octet-stream

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {path,bytes,content_type} |
| 507 | site_full |

## DELETE /v1/sites/{sitename}/storage/files/{name}/objects/{path}

<div data-operation="DELETE /v1/sites/{sitename}/storage/files/{name}/objects/{path}"></div>

Checked against delete; own deletes only the author's file (404 otherwise); a caller who may only add gets 403 add_only.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| name | path | string | yes |  |
| path | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | deleted: true |

## GET /v1/sites/{sitename}/storage/kv/{name}/keys

<div data-operation="GET /v1/sites/{sitename}/storage/kv/{name}/keys"></div>

Own lists return only the signed-in visitor's keys. Visitors get mine true/false per item; the owner's tools and the owner signed in on the site get visitor_id.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| name | path | string | yes |  |
| prefix | query | string |  |  |
| after | query | string |  |  |
| limit | query | integer |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | items: [{key,value,mine\|visitor_id}], next_after cursor |
| 403 | forbidden or site_locked |

## GET /v1/sites/{sitename}/storage/kv/{name}/keys/{key}

<div data-operation="GET /v1/sites/{sitename}/storage/kv/{name}/keys/{key}"></div>

Own reads return only the signed-in visitor's key; other keys return 404. Visitors get mine; the owner gets visitor_id.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| name | path | string | yes |  |
| key | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {key,value} |
| 404 | key_not_found or resource_not_found |

## PUT /v1/sites/{sitename}/storage/kv/{name}/keys/{key}

<div data-operation="PUT /v1/sites/{sitename}/storage/kv/{name}/keys/{key}"></div>

The server decides add (the key does not exist; checked against add, created with ON CONFLICT DO NOTHING) or edit (it exists; checked against edit, own edits carry the author check in the UPDATE and keep the original author). A caller who may add but not edit gets 409 key_exists on an existing key. The owner's tools can always replace. New keys are normalized to NFC; exact legacy names remain readable and deletable. Visitor writes use the site's own origin and X-SH-CSRF header; anonymous writes require write=anyone. Visitor writes are limited per site/IP and per site/visitor by RATE_LIMIT_STORAGE_IP and RATE_LIMIT_STORAGE_VISITOR (429); reads consume no write tokens. Writes wait SITE_STORAGE_WRITE_LOCK_WAIT_MS (default 2000) for the deploy lock before 503 storage_busy with Retry-After.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| name | path | string | yes |  |
| key | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| value | any | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {key,value} |
| 507 | site_full |

## DELETE /v1/sites/{sitename}/storage/kv/{name}/keys/{key}

<div data-operation="DELETE /v1/sites/{sitename}/storage/kv/{name}/keys/{key}"></div>

Checked against delete. Own deletes carry visitor_id = caller in the statement and answer 404 for another person's key; a caller who may only add gets 403 add_only.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| name | path | string | yes |  |
| key | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | deleted: true |

## GET /v1/sites/{sitename}/storage/resources

<div data-operation="GET /v1/sites/{sitename}/storage/resources"></div>

On Simple Host each resource carries preset (public, inbox, wall, comments, records, personal, board, private, custom with based_on, or legacy), access {read,add,edit,delete}, tables (SQLite, per-table preset and access), approve_first (SQLite), and the older read, write and write_mode whenever the matrix can be expressed in them. legacy marks a resource saved before presets that keeps its old rules until the owner saves a policy on it. Simple Hack answers with read, write, write_mode and site_passcode only.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | resources: [{name,kind,preset,access:{read,add,edit,delete},tables,read,write,write_mode,site_passcode}] |
| 403 | forbidden |

## PUT /v1/sites/{sitename}/storage/resources/{name}

<div data-operation="PUT /v1/sites/{sitename}/storage/resources/{name}"></div>

Kind is immutable. Simple Host: set preset (public, inbox, wall, comments, records, personal, board, private; default private) with optional single-action overrides read, add, edit, delete, or preset custom with all four. Each action takes nobody, owner, own, signed-in or anyone; add also takes checked (anyone who passes the Cloudflare Turnstile spam check: the comments preset, read anyone / add checked / edit owner / delete owner). The server refuses (400 invalid_access, with rule) edit or delete anyone (anonymous_change), add own (add_own), own anywhere with add anyone or checked (own_needs_sign_in), checked on read, edit or delete (checked_add_only), and edit or delete wider than read (change_wider_than_read). checked is refused on files resources, and with 400 spam_check_unavailable on a server without TURNSTILE_SITE_KEY and a secret (Simple Hack included). approve_first (SQLite, needs a checked add) holds posts added through the check: the server adds approved_at TEXT to those tables (rows already there count as approved), visitors see and reach only rows with approved_at set, and the owner approves with UPDATE ... SET approved_at = datetime('now'). Omit approve_first to keep it. SQLite: the preset is the database default; tables sets a preset per table (omit to keep the current table presets, {} to clear). Tables whose preset uses own or lets visitors add get the server-owned visitor_id TEXT column and index. The older read, write and write_mode are still accepted and translated (write anyone with full is refused as anonymous_change; omitting write_mode on update keeps the mode); do not mix them with preset fields. Switching a legacy SQLite database pages wrote with SQL to a preset that uses own needs empty tables (visitor_id_required). Owner here means the owner's tools and the owner signed in on the site's own address through visitor sign-in; nobody means the owner's tools only. Simple Hack accepts only read and write (anyone, signed-in, owner) with full mode, as before presets.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| name | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| add | string, one of `nobody`, `owner`, `signed-in`, `checked`, `anyone` |  |  |
| approve_first | boolean |  |  |
| delete | string, one of `nobody`, `owner`, `own`, `signed-in` |  |  |
| edit | string, one of `nobody`, `owner`, `own`, `signed-in` |  |  |
| kind | string, one of `kv`, `sqlite`, `files` | yes |  |
| preset | string, one of `public`, `inbox`, `wall`, `comments`, `records`, `personal`, `board`, `private`, `custom` |  |  |
| read | string, one of `nobody`, `owner`, `own`, `signed-in`, `anyone` |  |  |
| site_passcode | string, one of `inherit`, `False` |  |  |
| tables | object |  |  |
| write | string, one of `anyone`, `signed-in`, `owner` |  | older field |
| write_mode | string, one of `full`, `add` |  | older field |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Existing resource policy changed; the resource as listed |
| 201 | Resource created; the resource as listed |
| 400 | invalid_access (with rule), invalid_resource, visitor_id_required |
| 403 | forbidden |
| 409 | resource_kind_conflict, or tables_present (older fields on a database with per-table presets) |

## DELETE /v1/sites/{sitename}/storage/resources/{name}

<div data-operation="DELETE /v1/sites/{sitename}/storage/resources/{name}"></div>

Permanently delete a resource and its data (owner only)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| name | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | deleted: true |
| 403 | forbidden |

## POST /v1/sites/{sitename}/storage/sqlite/{name}/execute

<div data-operation="POST /v1/sites/{sitename}/storage/sqlite/{name}/execute"></div>

Owner only on any database with a preset; visitors receive 403 fixed_routes_required. A legacy full-mode database saved before presets (and every Simple Hack database) keeps visitor SQL under its write policy. A statement that reads rows also needs read permission. Visitor writes require same-origin CSRF header. All SQLite executions share SITE_STORAGE_SQL_CONCURRENCY (0 selects CPU count); SITE_STORAGE_ACQUIRE_WAIT_MS (default 250) bounds queue waiting before 503 with Retry-After. Owner operations retain SITE_STORAGE_OWNER_TIMEOUT_MS (default 5000).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| name | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| params | array of any |  | Positional JSON scalar values |
| sql | string | yes | One SQLite statement; use bound question-mark parameters. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {changes,last_insert_id} |
| 400 | Every SQL refusal says what happened and what to do, under a stable code (full list on the StorageSQL schema): sql_request, sql_params, sql_syntax, sql_multiple_statements, sql_param_count, sql_not_available, sql_use_query, sql_use_execute, sql_use_schema, sql_no_such_table, sql_no_such_column, sql_no_such_function, sql_not_null_failed, sql_check_failed, sql_foreign_key_failed, sql_constraint_failed, sql_timeout, sql_value_too_large, sql_error |
| 403 | fixed_routes_required, or sql_visitor_rule (a legacy visitor statement reads rows the read rule does not allow; the message names the rule) |
| 409 | sql_unique_failed: a row with that unique value exists; the message names the column |
| 507 | site_full or sqlite_full |

## POST /v1/sites/{sitename}/storage/sqlite/{name}/query

<div data-operation="POST /v1/sites/{sitename}/storage/sqlite/{name}/query"></div>

Owner only on any database with a preset; visitors receive 403 fixed_routes_required and use the table rows routes. A legacy full-mode database saved before presets (and every Simple Hack database) keeps visitor SQL under its read policy until the owner sets a preset. The SQLite engine authorizer enforces read-only access. Results are bounded, with columns and rows. All SQLite executions share SITE_STORAGE_SQL_CONCURRENCY (0 selects CPU count); SITE_STORAGE_ACQUIRE_WAIT_MS (default 250) bounds queue waiting before 503 with Retry-After. Owner operations retain SITE_STORAGE_OWNER_TIMEOUT_MS (default 5000).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| name | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| params | array of any |  | Positional JSON scalar values |
| sql | string | yes | One SQLite statement; use bound question-mark parameters. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | columns: [...], rows: [[...]] |
| 400 | Every SQL refusal says what happened and what to do, under a stable code (full list on the StorageSQL schema): sql_request, sql_params, sql_syntax, sql_multiple_statements, sql_param_count, sql_not_available, sql_use_query, sql_use_execute, sql_use_schema, sql_no_such_table, sql_no_such_column, sql_no_such_function, sql_not_null_failed, sql_check_failed, sql_foreign_key_failed, sql_constraint_failed, sql_timeout, sql_value_too_large, sql_error, or result_too_large |
| 403 | fixed_routes_required (visitor on a preset database) |

## POST /v1/sites/{sitename}/storage/sqlite/{name}/schema

<div data-operation="POST /v1/sites/{sitename}/storage/sqlite/{name}/schema"></div>

New tables whose preset uses own or lets visitors add (every table of a legacy own database) receive visitor_id TEXT and its index; changes removing the column from such a table roll back. All SQLite executions share SITE_STORAGE_SQL_CONCURRENCY (0 selects CPU count); SITE_STORAGE_ACQUIRE_WAIT_MS (default 250) bounds queue waiting before 503 with Retry-After. Owner operations retain SITE_STORAGE_OWNER_TIMEOUT_MS (default 5000).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| name | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| params | array of any |  | Positional JSON scalar values |
| sql | string | yes | One SQLite statement; use bound question-mark parameters. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | changes: integer |
| 400 | Every SQL refusal says what happened and what to do, under a stable code (full list on the StorageSQL schema): sql_request, sql_params, sql_syntax, sql_multiple_statements, sql_param_count, sql_not_available, sql_use_query, sql_use_execute, sql_use_schema, sql_no_such_table, sql_no_such_column, sql_no_such_function, sql_not_null_failed, sql_check_failed, sql_foreign_key_failed, sql_constraint_failed, sql_timeout, sql_value_too_large, sql_error, visitor_id_required, or server_column (a CREATE TRIGGER/VIEW or ALTER naming written_by; SIM-358, 2026-11-06, a name match on the statement text, not a parser -- a positional INSERT elsewhere naming no column is unaffected, by design) |
| 409 | table_preset_rename |

## GET /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows

<div data-operation="GET /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows"></div>

Checked against the table's read value (its own preset, or the database's). Equality filters where.{column}=value (at most three, real columns only) are bound values. Visitors see their own visitor_id and null for other people's, plus mine per row; visitor_id cannot be filtered or ordered by visitors. Own reads use WHERE visitor_id = ? with the stable site-session identity. Table and order columns are checked against real schema; values are bound. Default order is an unshadowed rowid alias ascending; ordinary rowid tables only. Cursor includes order value and rowid, bound on the next query with matching order/desc. Owner sees all. No visitor SQL accepted. HEAD is a read. Visitor tables are limited to 100 columns and the complete response to SITE_STORAGE_SQL_RESULT_MAX_BYTES; visitor queries time out after one second. Reads do not lock writes or deploys.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| name | path | string | yes |  |
| table | path | string | yes |  |
| order | query | string |  |  |
| desc | query | integer, one of `0`, `1` |  |  |
| limit | query | integer |  |  |
| after | query | string |  |  |
| where | query | object |  | where.{column}=value, equality, at most three |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | columns, rows, mine (visitors) and opaque next_after cursor; own reads include only the signed-in visitor’s rows |
| 400 | invalid_rows: unknown table, invalid/injected column, order, cursor or scalar value |
| 401 | sign_in_required |
| 403 | forbidden, csrf_required or site_locked |

## POST /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows

<div data-operation="POST /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows"></div>

Checked against the table's add value. One JSON object of real column names and scalar values. The server builds INSERT OR ABORT with bound values; visitor-supplied primary keys and rowids are refused. SQLite assigns the IDs and RETURNING supplies the result. Visitor visitor_id, created_at and updated_at are ignored; visitor_id is filled from the site session and created_at with UTC time when the columns exist. Declared SQLite foreign keys must point at a row the visitor may read under the parent table's read value (for own, a row they added), checked inside the insert transaction. Missing and unreadable parents both return 404 invalid_reference; the insert is rolled back. Optional references must be all NULL; partial composite NULLs are refused. A trigger or foreign-key action that writes another table refuses the insert (403). No visitor SQL accepted. Where add is checked (the comments preset), a caller other than the owner sends the one-time Turnstile token in X-SH-Turnstile (auth.js does it on its own); without one 403 spam_check_required, a failed, reused, expired or mismatched one 403 spam_check_failed, Cloudflare unreachable 403 spam_check_unavailable. The token must have been solved in the frame at GET /v1/turnstile/frame for this host and resource. Such a post is at most SPAM_CHECKED_POST_MAX_BYTES (413 post_too_large) with at most SPAM_CHECKED_POST_MAX_LINKS links (400 too_many_links), and RATE_LIMIT_SPAM_CHECKED_IP and RATE_LIMIT_SPAM_CHECKED_SITE apply (429). The same holds for adding a KV key. The query param for (owner, or the owner signed in on the site's own address, only; SIM-358, 2026-11-06, review fixes 2026-11-06) adds this row for one other signed-in visitor instead of the caller, by their visitor_id or the email they signed in with, only on a table whose read is own (403 for_requires_own on any other table, checked on the table's own matrix). It is refused (404 visitor_unknown) if they have never signed in on this site, or (400 visitor_email_ambiguous) if more than one signed-in person currently matches that email; there is no pre-sign-in email binding. The server ignores any visitor_id or written_by the caller sends and sets them itself, visitor_id to that visitor and written_by (added lazily, under the write lock, the first time a table is used this way; 409 written_by_blocked if a trigger or view appears to depend on the table) to owner, otherwise the empty string, which is also written_by's value whenever for is absent, including a plain owner add. A row added this way does not fire record.added to the owner's own subscriptions. Host only, 404 on Simple Hack and event storage.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| Idempotency-Key | header | string |  | Opaque key for a create request. For 24 hours, the same authenticated account, POST path and query, key and exact request body replay the original handler status and body, including errors, without executing it again. A different body under that key returns 409 idempotency_key_reused. Concurrent identical requests wait for the first response. Stored only in this server's memory; a restart clears it. Authentication and rate limiting still apply to replays. A replay does not extend a minted link's expiry; use a new key to request a new link. Storage row creates support owner credentials and signed-in visitors; anonymous writes ignore this header. Storage replay also checks current resource permissions and is scoped to the resolved site. |
| sitename | path | string | yes |  |
| name | path | string | yes |  |
| table | path | string | yes |  |
| for | query | string |  | Owner (or owner-on-site) only. A visitor_id, or the email they signed in with, naming who this row is for. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | changes and last_insert_id |
| 400 | invalid_rows: unknown table, invalid/injected column, order, cursor or scalar value; visitor_email_ambiguous: for's email currently matches more than one signed-in person |
| 401 | sign_in_required |
| 403 | forbidden, csrf_required, site_locked, or for_requires_own (for on a table whose read is not own) |
| 404 | invalid_reference (parent missing or belongs to another visitor; identical response) or visitor_unknown (for names someone who has never signed in on this site) |
| 409 | row_conflict (generic uniqueness or constraint refusal), written_by_blocked (for''s lazy column upgrade refused because a trigger or view appears to depend on this table), or written_by_tampered (a trigger changed visitor_id or written_by as a side effect of this for insert; rolled back, SIM-358 round 2) |
| 429 | rate_limited |
| 503 | storage_busy |
| 507 | site_full or sqlite_full: quota precheck or engine allocation failure |

## GET /v1/sites/{sitename}/storage/usage

<div data-operation="GET /v1/sites/{sitename}/storage/usage"></div>

The default limit is 1,000,000 bytes pooled across this website's KV, SQLite and raw-file resources. Deployed assets and historical data have separate limits.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {used_bytes,limit_bytes,remaining_bytes,files_used_bytes,files_limit_bytes,files_remaining_bytes,breakdown:{kv_bytes,sqlite_bytes,files_bytes}} |
| 403 | forbidden |
