# Storage and saved data

## Create storage and choose access
[What this is](/features.html#storage-presets)


1. Read the site's current resources with `GET /v1/sites/{sitename}/storage/resources`.
2. Create or update one resource.

      **In chat**
“Add a private notes table where each visitor can read and edit only their own notes.”   **API**
Send `PUT /v1/sites/{sitename}/storage/resources/notes` with `{"kind":"sqlite","preset":"personal"}`. Use `kv` or `files` for those resource kinds.   
3. For SQLite, send owner DDL to `POST /v1/sites/{sitename}/storage/sqlite/notes/schema`: `{"sql":"CREATE TABLE notes (id INTEGER PRIMARY KEY, visitor_id TEXT, text TEXT)"}`.
4. Choose `private` for owner-only data, `inbox` for submissions, `public` for owner-written public data, or `personal` for each visitor's editable records. Confirm access before saving a new preset.


## Write to storage from your own code
[What this is](/features.html#storage-rows)


1. [Load auth.js and mount sign-in](/docs/start/pages/). Use `SH.storage.sqlite('notes').table('notes').add({text:'Hello'})` for a visitor insert.
2. Use `id INTEGER PRIMARY KEY`; never supply a visitor row's primary key or `visitor_id`. Declare foreign keys and make the parent readable to the visitor.
3. Read with `.list()` or `.get(id)`; change with `.edit(id, values)` or `.delete(id)`. Check the returned error code.
4. Before a larger write, read `GET /v1/sites/{sitename}/storage/usage`.


<details>
<summary>SQLite inserts and references</summary>

Use entirely `NULL` optional composite references, never partly `NULL`. Keep `visitor_id TEXT` on parents. Treat unreadable and missing parents alike as `invalid_reference`. Remove cross-table triggers and cascades from tables visitors edit or delete.

</details>

<details>
<summary>Existing resources and migration</summary>

Omit `write_mode` to preserve it. Empty or migrate legacy full-mode SQLite tables with owner tools before switching to own access. Keep unattributed legacy rows outside visitor-owned views.

</details>

<details>
<summary>Rate limits and execution</summary>

Retry `storage_busy` after a short wait; honor `Retry-After` when supplied. Check `SITE_STORAGE_*` execution settings in the [settings reference](https://github.com/vineetu/simple-host/tree/main/docs/advanced). Keep tables accessed by visitors to at most 100 total columns, including server-managed columns. Limit query results.

</details>

<details>
<summary>Quotas and names</summary>

Limit file paths to 16 segments; check bucket object allowance, including empty files. Use NFC names for new writes; keep exact legacy names for reads and deletion. Handle `sqlite_full` separately from `site_full`.

</details>

<details>
<summary>Global uniqueness and passcodes</summary>

Use random identifiers or visitor-specific namespaces for global UNIQUE columns, KV keys, and file paths. Use table routes for visitor SQLite access, not raw SQL. Set `site_passcode: "off"` only when the owner chooses public resources without unlocking.

</details>

## Errors you may see

| Code | Means | Do this |
|---|---|---|
| `use_custom_domain` (400 or 401) | Shared address refuses visitor writes | Open the site on its own address |
| `invalid_reference` (404) | Parent missing or unreadable | Choose a readable parent |
| `row_conflict` (409) | Row cannot satisfy a table constraint | Check required fields, unique values, and check constraints |
| `storage_busy` (503) | Storage is busy | Retry after a short wait; honor `Retry-After` when supplied |
| `site_full` (507) | Storage allowance reached | Remove unused data |

## Next
- [Notifications](/docs/guides/notifications/)
- [Sign-in and access](/docs/guides/access/)
