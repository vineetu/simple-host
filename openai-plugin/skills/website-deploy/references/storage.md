# Site storage: KV, SQLite and files

Use the connected `storage_*` tools to configure and maintain a site's new
storage resources. Keep each site's keys, tables and paths in its own design;
there is no required RSVP or shop schema. A resource name is unique across
the three kinds, and its kind cannot later change. Pages use same-origin
`/v1/sites/{site}/storage/...` requests with their site-scoped visitor session.
The hosted `auth.js` helper exposes `SH.storage` for these resources.

Create a resource with `storage_set_resource(site,name,body)`:

```json
{"kind":"kv","read":"anyone","write":"owner","site_passcode":"inherit"}
```

`kind` is `kv`, `sqlite` or `files`. The independent `read` and `write`
policies are `anyone`, `signed-in` or `owner`; both default to `owner`.
`site_passcode` defaults to `inherit`, requiring visitors to unlock an existing
site passcode, or can be `off` to bypass it for this resource. Anonymous
writes work only with `write=anyone`. `signed-in` gives **every** signed-in
visitor the chosen access to **every** key, row or file in that resource.
These are not row-level or per-person permissions. Use the existing Personal
kind for each visitor's private record and private Submissions for owner-only
forms. Ask before making a resource public, allowing anonymous writes or
permanently deleting a resource.

| Task | Connected tool |
|---|---|
| List policies, check usage, configure or delete a resource | `storage_list_resources`, `storage_get_usage`, `storage_set_resource`, `storage_delete_resource` |
| List, read, set or remove KV entries | `storage_list_kv_keys`, `storage_get_kv`, `storage_put_kv`, `storage_delete_kv` |
| Query rows, change rows or change schema | `storage_sql_query`, `storage_sql_execute`, `storage_sql_schema` |
| List, upload, remove or link to file objects | `storage_list_file_objects`, `storage_put_file`, `storage_delete_file`, `storage_file_download_link` |

The default allowance is **1,000,000 bytes per website**, pooled across KV,
SQLite and file resources. `storage_get_usage` returns used, limit and
remaining bytes with a breakdown. Deployed website files and legacy saved data
have separate limits. Rejected growth returns `site_full` without changing
the existing data. Files persist across website publishes and rollbacks.
The connected inline file upload is capped at 1 MiB decoded, so plan small
objects and check remaining space first. Download links expire after ten
minutes. Stored HTML and scripts download as attachments rather than running
as part of the site.

For a phone-photo upload page, resize and compress in the browser before
calling `SH.storage.files('photos').put(path, preparedPhoto)`. Use canvas or
`createImageBitmap` to keep aspect ratio and encode as WebP or JPEG. Aim well
below the site's remaining allowance, show a preview and compressed byte size,
and refuse the original if it is still too large. Leave PDFs and other binary
files unchanged unless the application explicitly defines a conversion.

SQLite takes one statement per call with bound `?` parameters. Query is
read-only; execute changes rows; schema changes are owner-only. Do not
concatenate visitor text into SQL. The server checks which statements and
resources each operation may touch.

On the page's own site origin, use the hosted `SH` helper for visitor sign-in
when policy requires it. Visitor writes need the normal same-site session,
`Origin` and `X-SH-CSRF: 1`; the helper supplies them. Handle
`sign_in_required`, `forbidden`, `site_locked`, `resource_not_found` and
`site_full` as server decisions. Do not broaden a policy merely to make a
failed write succeed.

Older state, collections and declared-data APIs remain supported. Their
atomic operations, undo/history, notifications and private visitor behavior
do not automatically migrate to these whole-resource policies. Do not copy
private old data into a new resource without the owner's direction.
