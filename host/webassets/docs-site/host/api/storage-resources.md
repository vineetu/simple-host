# Storage resources

Owner-declared KV, SQLite and durable file resources.

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

## GET /v1/sites/{sitename}/storage/people

<div data-operation="GET /v1/sites/{sitename}/storage/people"></div>

Owner only: the owner's key or connector token, or the owner signed in on the site's own address (SH.me().site_owner; SH.storage.people() on the page). Lists only this site's people who have saved data here (a SQLite row, a KV key, or a stored file) or are named viewers — never everyone who has merely signed in (the privacy rule). Each entry's visitor_id, when present, is a usable `for` target; a named viewer who has not signed in yet is listed by email only (visitor_id omitted, signed_in false), not yet usable.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | people: [{visitor_id?, email?, named_viewer, has_data, signed_in}] |
| 403 | forbidden (not the owner) |

## GET /v1/sites/{sitename}/storage/resources

<div data-operation="GET /v1/sites/{sitename}/storage/resources"></div>

Every resource carries rules (StoragePolicySet), rules_status (rules or invalid), rules_errors and, for sqlite, tables ({table: {rules}}). A resource saved before rules, with no stored rules, is owner-only and is listed with the owner-only rule set.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | resources: [{name,kind,site_passcode,rules,rules_status,rules_errors,tables}] (tables for sqlite only) |
| 403 | forbidden |

## PUT /v1/sites/{sitename}/storage/resources/{name}

<div data-operation="PUT /v1/sites/{sitename}/storage/resources/{name}"></div>

Kind is immutable. Every kind takes rules (StoragePolicySet); sqlite also takes tables ({table: {rules}}), whose rules override the default set (omit tables to keep the overrides, send tables {} to clear them). Creating a resource without rules stores the owner-only rule set (read, add, edit and delete need auth.role == "owner"); saving an existing one without rules keeps its rules, so a site_passcode-only save preserves them. Presets were removed: preset, read, add, edit, delete, write, write_mode or approve_first on a body or table entry return 400 invalid_resource. Null rules are treated as absent; an empty policies array denies visitors. Bodies over 256 KiB return 400 invalid_rules with clause size. File rules compile against files(path, visitor_id), visitor_id being the uploader: read covers listing and opening, add a new path, edit replacing a file, delete removing one; auth.checked is refused on files (400 invalid_rules). Spam-checked adds use auth.checked in an add check (refused with 400 spam_check_unavailable on a server without TURNSTILE_SITE_KEY and a secret); holding posts until approved is a read policy such as auth.role == "owner" || approved_at is not null. Responses carry rules, rules_status and rules_errors. The owner's tools bypass rules; the owner signed in on the site's own address is a visitor whose auth.role is owner.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| name | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| kind | string, one of `kv`, `sqlite`, `files` | yes |  |
| rules | object |  |  |
| site_passcode | string, one of `inherit`, `off` |  |  |
| tables | object |  | sqlite only; each table entry carries only rules |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Existing resource policy changed; the resource as listed |
| 201 | Resource created; the resource as listed |
| 400 | invalid_resource (including any field of the removed presets), visitor_id_required, invalid_rules (with rule_error and rules_errors), spam_check_unavailable |
| 403 | forbidden |
| 409 | resource_kind_conflict |

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

Owner only; visitors receive 403 fixed_routes_required and use the table rows routes. All SQLite executions share SITE_STORAGE_SQL_CONCURRENCY (0 selects CPU count); SITE_STORAGE_ACQUIRE_WAIT_MS (default 250) bounds queue waiting before 503 with Retry-After. Owner operations retain SITE_STORAGE_OWNER_TIMEOUT_MS (default 5000).

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
| 403 | fixed_routes_required (a visitor) |
| 409 | sql_unique_failed: a row with that unique value exists; the message names the column |
| 507 | site_full or sqlite_full |

## POST /v1/sites/{sitename}/storage/sqlite/{name}/query

<div data-operation="POST /v1/sites/{sitename}/storage/sqlite/{name}/query"></div>

Owner only; visitors receive 403 fixed_routes_required and use the table rows routes. The SQLite engine authorizer enforces read-only access. Results are bounded, with columns and rows. All SQLite executions share SITE_STORAGE_SQL_CONCURRENCY (0 selects CPU count); SITE_STORAGE_ACQUIRE_WAIT_MS (default 250) bounds queue waiting before 503 with Retry-After. Owner operations retain SITE_STORAGE_OWNER_TIMEOUT_MS (default 5000).

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
| 403 | fixed_routes_required (a visitor) |

## POST /v1/sites/{sitename}/storage/sqlite/{name}/schema

<div data-operation="POST /v1/sites/{sitename}/storage/sqlite/{name}/schema"></div>

New tables whose rules name visitor_id or let visitors add receive visitor_id TEXT and its index; changes removing the column from such a table roll back. All SQLite executions share SITE_STORAGE_SQL_CONCURRENCY (0 selects CPU count); SITE_STORAGE_ACQUIRE_WAIT_MS (default 250) bounds queue waiting before 503 with Retry-After. Owner operations retain SITE_STORAGE_OWNER_TIMEOUT_MS (default 5000).

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
| 409 | table_preset_rename (a table with its own rules is renamed before the new name has rules) |

## GET /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows

<div data-operation="GET /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows"></div>

Checked against the table's read rules (its own rules, or the database's). Equality filters where.{column}=value (at most three, real columns only) are bound values. Visitors see their own visitor_id and null for other people's, plus mine per row; visitor_id cannot be filtered or ordered by visitors. Own reads use WHERE visitor_id = ? with the stable site-session identity. Table and order columns are checked against real schema; values are bound. Default order is an unshadowed rowid alias ascending; ordinary rowid tables only. Cursor includes order value and rowid, bound on the next query with matching order/desc. Owner sees all. No visitor SQL accepted. HEAD is a read. Visitor tables are limited to 100 columns and the complete response to SITE_STORAGE_SQL_RESULT_MAX_BYTES; visitor queries time out after one second. Reads do not lock writes or deploys.

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

Checked against the table's add rules. One JSON object of real column names and scalar values. The server builds INSERT OR ABORT with bound values; visitor-supplied primary keys and rowids are refused. SQLite assigns the IDs and RETURNING supplies the result. Visitor visitor_id, created_at and updated_at are ignored; visitor_id is filled from the site session and created_at with UTC time when the columns exist. Declared SQLite foreign keys must point at a row the visitor may read under the parent table's read value (for own, a row they added), checked inside the insert transaction. Missing and unreadable parents both return 404 invalid_reference; the insert is rolled back. Optional references must be all NULL; partial composite NULLs are refused. A trigger or foreign-key action that writes another table refuses the insert (403). No visitor SQL accepted. Where the add check uses auth.checked (spam-checked adds), a caller other than the owner sends the one-time Turnstile token in X-SH-Turnstile (auth.js does it on its own); without one 403 spam_check_required, a failed, reused, expired or mismatched one 403 spam_check_failed, Cloudflare unreachable 403 spam_check_unavailable. The token must have been solved in the frame at GET /v1/turnstile/frame for this host and resource. Such a post is at most SPAM_CHECKED_POST_MAX_BYTES (413 post_too_large) with at most SPAM_CHECKED_POST_MAX_LINKS links (400 too_many_links), and RATE_LIMIT_SPAM_CHECKED_IP and RATE_LIMIT_SPAM_CHECKED_SITE apply (429). The same holds for adding a KV key. The query param for (owner, or the owner signed in on the site's own address, only; SIM-358, 2026-11-06, review fixes 2026-11-06) adds this row for one other signed-in visitor instead of the caller, by their visitor_id or the email they signed in with, only on a table whose read rules limit rows to the row's visitor or the owner (403 for_requires_own on any other table). It is refused (404 visitor_unknown) if they have never signed in on this site, or (400 visitor_email_ambiguous) if more than one signed-in person currently matches that email; there is no pre-sign-in email binding. The server ignores any visitor_id or written_by the caller sends and sets them itself, visitor_id to that visitor and written_by (added lazily, under the write lock, the first time a table is used this way; 409 written_by_blocked if a trigger or view appears to depend on the table) to owner, otherwise the empty string, which is also written_by's value whenever for is absent, including a plain owner add. A row added this way does not fire record.added to the owner's own subscriptions.

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

## GET /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows/{id}

<div data-operation="GET /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows/{id}"></div>

Checked against the table's read rules; own reads add visitor_id = caller, so another person's row is 404 like a missing one. Simple Host only.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| name | path | string | yes |  |
| table | path | string | yes |  |
| id | path | integer | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {columns,row,mine} |
| 404 | row_not_found |

## PATCH /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows/{id}

<div data-operation="PATCH /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows/{id}"></div>

Checked against the table's edit rules. A JSON object of real column names and scalar values; id, primary keys, visitor_id, created_at and updated_at are refused (400 server_column). The server builds UPDATE OR ABORT ... WHERE rowid = ? [AND visitor_id = caller] and stamps updated_at when the column exists; zero rows changed is 404. Foreign keys are checked as on add. A trigger or foreign-key action that writes another table refuses the edit (403). Visitor writes need the site's own origin and X-SH-CSRF.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| name | path | string | yes |  |
| table | path | string | yes |  |
| id | path | integer | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {changes} |
| 400 | invalid_rows or server_column |
| 401 | sign_in_required |
| 403 | forbidden |
| 404 | row_not_found or invalid_reference |

## DELETE /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows/{id}

<div data-operation="DELETE /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows/{id}"></div>

Checked against the table's delete value. DELETE ... WHERE rowid = ? [AND visitor_id = caller]; zero rows is 404. A foreign-key action (ON DELETE CASCADE) or trigger that writes another table refuses the delete (403).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| name | path | string | yes |  |
| table | path | string | yes |  |
| id | path | integer | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | deleted: true |
| 403 | forbidden |
| 404 | row_not_found |

## GET /v1/sites/{sitename}/storage/usage

<div data-operation="GET /v1/sites/{sitename}/storage/usage"></div>

Host KV/SQLite share 10000000 bytes per website; files have a separate 10000000-byte allowance. The single-upload default remains 1000000 bytes. Deployed website assets and legacy saved data have separate limits. SQLite main database bytes are counted after checkpoint; transient WAL is excluded. KV key bytes count too. File buckets have at most SITE_STORAGE_FILES_MAX_OBJECTS (default 1000) committed objects, including empty files.

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

## GET /v1/sites/{sitename}/storage/visitors

<div data-operation="GET /v1/sites/{sitename}/storage/visitors"></div>

Owner only (key or connector). Turns visitor_id values from this site's saved data (the visitor_id column of read-own SQLite tables; `visitor_id` on KV keys and stored files, which only the owner's reads show) into the email each person signs in with, and, with email instead of (or combined with) id (SIM-358, 2026-11-06), the other direction: the visitor_id they signed in as, for a `for` request or ResolveOwnerFor directly. Only people who signed in on this site as visitors resolve (recorded by the server when a visitor session starts, never read from the site's own tables; `found` false otherwise; no pre-sign-in email binding). 1 to 100 ids and/or emails total, comma-separated or repeated.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| id | query | string |  | visitor_id values, comma-separated (or repeat id=) |
| email | query | string |  | Email addresses, comma-separated (or repeat email=). Combine with id, or give this alone. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | visitors: [{visitor_id?, email?, found}], one entry per id then per email, in that order |
| 400 | invalid_visitor_id |
| 403 | forbidden (not the owner) |
