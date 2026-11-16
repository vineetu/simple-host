# Moving a site to storage resources

The older state, collection and declared-kind APIs are being retired. Each
install's operator sets the switch date with `STORAGE_SWITCH_AT` (an RFC 3339
UTC instant). An unset value means no switch date is set. Set it in the server's
environment or Helm `storageSwitchAt` value and restart.

Simple Hack already refuses the older saved-data APIs. Follow
[Storage and saved data](/docs/guides/storage.md) for storage with rules. Export the
site before changing it. Data does not move automatically.

For a new Simple Host site, choose the smallest flexible primitive that fits the
application: JSON key–value for named documents, SQLite for related records and
queries, or files for durable binary objects. The site author chooses keys,
tables and paths. A resource's independent `read` and `write` policies apply to
the **whole resource**, each set to `anyone`, `signed-in` or `owner`. The owner
creates it with owner-only access by default and may explicitly opt into
anonymous writes. `site_passcode=inherit` follows the site's existing visitor
unlock; `off` exempts that resource from the passcode. A signed-in policy means
any signed-in visitor, not a private row for each person.

| Existing API or behavior | Possible new design | What does not carry over automatically |
|---|---|---|
| `/v1/sites/{site}/state` shared JSON document | KV keys for independent documents, or SQLite for related records | Atomic `inc`/patch operations, ETags, version conflicts, watch behavior and history/undo need new application logic. |
| `/v1/sites/{site}/data/{name}` Page info (`content`) | Owner-write, anyone-read KV value or SQLite resource | Its single-document limits and built-in history/undo do not move with the value. |
| `/v1/sites/{site}/collections/{name}` Shared list or `board` | SQLite table with app-chosen schema, or KV keys | Item IDs, optimistic versions, item-level update/delete, watch/poll behavior and restore windows need redesign. |
| Submissions (`entries`) and private collections | Use SQLite with rules for each visitor's own rows and owner access; follow the storage guide | A database-wide `signed-in` read would let every signed-in visitor see all rows. The owner-only resource policy would prevent a visitor from seeing their own row. Notification digests and one-per-person rules also do not transfer. |
| Personal (`mine`) | Keep each person's private export until a suitable migration is agreed | Storage does not preserve Personal's owner-exclusion guarantee: owner tools, connector access and site exports bypass row rules. Removing an owner policy does not prevent owner access. Do not copy Personal records into storage. |
| Files inside a deployed website version | Storage files only for mutable, durable objects | Deployment files are versioned and roll back with a publish. Storage objects persist across publish and rollback; changing a public asset URL or cache behavior needs an explicit design. |

KV and SQLite share a default 10,000,000-byte allowance per website; Host files
have a separate 10 MB allowance. Hack retains the pool across all three kinds. Owner-only `GET /v1/sites/{site}/storage/usage` (connector:
`storage_get_usage`) reports `used_bytes`, `remaining_bytes`, `limit_bytes`,
`files_used_bytes`, `files_limit_bytes`, `files_remaining_bytes`,
and a `breakdown` of `kv_bytes`, `sqlite_bytes` and `files_bytes`. SQLite counts
the checkpointed main database, with temporary WAL bytes excluded. Website
deployment assets and legacy saved data are outside this allowance and retain
their existing limits. Inspect usage before a large copy; a rejected growth
write leaves the existing data in place.

Before any owner-directed migration, identify the exact names in use, define the
new resource schema and policy, export a recoverable copy, and test read/write
behavior as an anonymous visitor, a signed-in visitor, and the owner. Copy only
the records whose privacy can be preserved; check the result before the configured switch date. Do not turn an old private collection or Personal
name into a public resource merely to make it readable from a page.

An operator's adoption inventory should count sites, legacy route calls and
resource kinds/policies over a stated period. Record aggregate counts and
failure rates, not record values, visitor emails, SQL text, keys or file bytes.
Use those counts with export/restore proof and real client compatibility when
preparing for the configured switch date. The Enterprise replicated/S3 product needs
its own SQLite and file-storage parity assessment before these resources can be
described as available there.

Own reads and add-only writes now support new order designs. They do not add
legacy notifications, history, visitor edits or withdrawal. See the “Each person's records” pattern
in `docs/designs/site-storage-primitives.md`.
