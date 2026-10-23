# Tools by job, versions, previews, rollback, offline, delete, exports and visitor numbers

Moved here from SKILL.md unchanged.

## Tools

| Need | Tool |
|---|---|
| Who am I, my handle and address | `who_am_i` |
| My sites / one site and its files | `list_sites`, `get_site`; use `query` on `list_sites` to find a site by description, with date filters and `limit`/`offset` for paging |
| Read a file of a site | `read_site_file` |
| Publish a new site | `create_site` |
| Change an existing site | `update_site` (read its files first) |
| Versions, a preview link, undo a bad publish, how many to keep | `list_versions`, `preview_version`, `rollback_site`, `set_keep_versions` |
| Rename, list on public page, delete | `rename_site`, `set_visibility`, `delete_site` |
| Take offline or back online (keeps everything) | `set_site_offline` |
| Only named people can open a whole site (by email) | `grant_site_viewer`, `list_site_viewers`, `revoke_site_viewer`, `set_site_access` |
| Put a passcode on a whole site or change it; show it; take it off; make every visitor enter it again | `set_site_passcode`, `get_site_passcode`, `remove_site_passcode`, `sign_out_site_visitors` |
| Keep a site up even if nobody visits it | `keep_site` |
| Undo a delete (within 7 days) | `list_deleted_sites`, `restore_site` |
| Public page: choose a home site, bio, showcase pin and order | `set_home_page`, `set_bio`, `set_showcase_site` |
| A shorter address (optional) | `connect_domain` (free `<name>.simple-host.app`, or their own domain), `domain_status`, `remove_domain` |
| Visitors | `site_analytics` (report the `person` numbers) |
| Download a copy of a site | `export_site` (a link that works for 10 minutes; give it to the person) |
| Exact page code for a storage job | `get_page_recipe` (a topic per job: `booking`, `inbox`, `public`, `portal`; per preset: `wall`, `comments`, `board`, `personal`, `private`, `records`; plus `admin` and `gallery`) |
| New KV, SQLite or file resource and its preset | `storage_set_resource`, `storage_list_resources`, `storage_delete_resource`, `storage_get_usage` |
| KV values | `storage_get_kv`, `storage_put_kv`, `storage_list_kv_keys`, `storage_delete_kv` |
| SQLite tables and rows, as the owner | `storage_sql_schema`, `storage_sql_query`, `storage_sql_execute` |
| Files | `storage_put_file`, `storage_list_file_objects`, `storage_file_download_link`, `storage_delete_file` |

To publish a new site use `create_site`; to change an existing site use `update_site` (read
its files first). `create_site` never overwrites an existing site.

For new saved data, first choose the site's own schema and whether a KV, SQLite or files
resource fits. Configure each resource's independent whole-resource read and write policy
with `storage_set_resource`. KV/SQLite share **10,000,000 bytes per website** and files have a separate **10 MB** allowance;
check `storage_get_usage` before large writes. A resource can inherit the site's passcode
or deliberately bypass it. `signed-in` grants every signed-in visitor access to the whole
resource; choose `read:own` with add-only writes for each person's records. Existing sites may
still hold data from the older state, collection and declared-data APIs; those routes keep
working but the connector no longer offers tools for them (see "Existing sites only" below).
Read `references/storage.md` before wiring storage into a page. Compress phone photos in the
browser before file uploads.

## Changing an existing site

A publish **replaces the whole site**: any file you do not send stops existing.

1. `list_sites` for the exact name, then `get_site` for its file list.
2. `read_site_file` for every text file; change only what was asked.
3. Send **all** files back with `update_site`.

Binary files are only described by `read_site_file`, not returned. If the site has images or
fonts you do not have the bytes for, tell the person before publishing that those files would
be dropped, and ask them to provide them again or agree to losing them.

Every version is kept. If a change went wrong, `list_versions`, confirm the version with the
person, then `rollback_site`. For a big change (a redesign), offer to let them look first:
`update_site` with `publish: false` stores the version without making it live and returns a
`preview_url` (it works for anyone who has it, for one hour); give only them the link, and when they are happy make it live
with `rollback_site`. `preview_version` makes a link for any kept version. Renaming (`rename_site`) changes the address; links to the old one
redirect to the new one until a new site takes the old name. When an event is over or a form
must stop taking entries, `set_site_offline` (after the person confirms) shows "This site is
offline" at every address and stops visitor saves, reads of its data and sign-in, keeping everything; `offline: false` undoes it. `delete_site` takes the site offline with every version and all its
saved data: call it only after the person has explicitly confirmed deleting that specific site
in this conversation, and name what goes offline when you ask. It stays in Recently deleted for
7 days (`list_deleted_sites`, `restore_site` brings it back exactly as it was), then it is gone
for good, and its name stays taken until then. The person can change their handle (the
`<handle>` in every address) under "Your address" on their Simple Host page; old addresses
redirect to the new one.
