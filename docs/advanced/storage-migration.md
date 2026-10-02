# Moving a site to storage resources

Status: implementation in progress. This is a migration plan, not an instruction
to move existing data now. The existing state, collection and declared-kind APIs
remain supported. There is no removal date, automatic conversion or deletion.

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
| Submissions (`entries`) and private collections | Keep the existing API unless a new design implements each visitor's own visibility, edit and withdrawal rules | A database-wide `signed-in` read would let every signed-in visitor see all rows. The owner-only resource policy would prevent a visitor from seeing their own row. Notification digests and one-per-person rules also do not transfer. |
| Personal (`mine`) | Keep the existing API unless a separate application design preserves each account's private record and history | A shared KV namespace or SQLite database with `signed-in` read is not private per person. Owner tooling must not acquire existing Personal records. |
| Files inside a deployed website version | Storage files only for mutable, durable objects | Deployment files are versioned and roll back with a publish. Storage objects persist across publish and rollback; changing a public asset URL or cache behavior needs an explicit design. |

Before any owner-directed migration, identify the exact names in use, define the
new resource schema and policy, export a recoverable copy, and test read/write
behavior as an anonymous visitor, a signed-in visitor, and the owner. Copy only
the records whose privacy can be preserved; keep the old API available until
the owner checks the result. Do not turn an old private collection or Personal
name into a public resource merely to make it readable from a page.

An operator's adoption inventory should count sites, legacy route calls and
resource kinds/policies over a stated period. Record aggregate counts and
failure rates, not record values, visitor emails, SQL text, keys or file bytes.
Use those counts with export/restore proof and real client compatibility before
proposing any retirement of an old endpoint. Any removal would need a separate
owner decision; this plan sets none. The Enterprise replicated/S3 product needs
its own SQLite and file-storage parity assessment before these resources can be
described as available there.
