# Saved data

**Deprecated, existing sites only.** New sites use storage resources (KV, SQLite, files) — see [storage-migration.md](storage-migration.md) and [storage-and-backups.md](storage-and-backups.md).

This page describes the existing state, collection and declared-kind API,
which remains supported. New Simple Host sites may instead use owner-declared
KV, SQLite and files resources with independent whole-resource read/write
policies. See [the storage migration plan](storage-migration.md) before moving
an existing site's data; its private Submissions, Personal records, atomic
operations and undo do not automatically map to a shared resource.

Every site gets a small backend in the same upload: one shared JSON document (page data) with
atomic operations, and lists visitors add to (RSVPs, votes, sign-ups). A list can be private: only
signed-in visitors add; the owner reads all, and each visitor reads their own.

**Kinds.** Each data name is one kind, and the kind decides who reads and who changes what:
**Page info** (the owner writes, everyone reads), **Submissions** (visitors send entries; private
to the owner by default; each visitor sees, changes and withdraws their own; an optional email
digest to the owner), **Personal** (one private record per signed-in visitor) and **Shared
board** (a list a group edits item by item). A name nobody declared is **Shared**, as before the
kinds; `SAVED_DATA_DEFAULT_KIND=declare_first` makes new sites take no saves under an undeclared
name. The owner can limit who may save on a site to listed emails and `@domains`, and block
people. The `SAVED_DATA_*` settings below set each kind's sizes and counts.

**Undo.** Every change to page data, and every deleted or edited list item, is kept for
`SAVED_DATA_UNDO_DAYS` and can be restored by the site's owner. History past its size cap is
thinned oldest first, always keeping each item's first change of every day. Unlike the dates in
Cleanup and retention, shortening the undo window applies to what is already kept at the next
sweep.

**Limits.** A site's live saved data may not grow past `SAVED_DATA_SITE_MAX_MB`; writes that do
not grow it always go through. Reads and list additions are rate limited per address.

**The watch.** The admin page counts, per site and day, the visitor writes a later step may
tighten (still counted; not enforced yet), so an operator can see who would be affected first.

**Who may write.** `WRITE_AUTH_MODE=on` makes every page save need a signed-in visitor or the
owner's key.

<!-- settings:group=data -->
| Setting | Default | Allowed | What it does |
|---|---|---|---|
| `RATE_LIMIT_STATE` | `60,1s` | any (warns past 10× looser) | Saved-data and list writes per client. |
| `SAVED_DATA_UNDO_DAYS` | `30` | 1–365 days | How long every change to saved data, and every deleted list item, can be restored. |
| `SAVED_DATA_HISTORY_MAX_MB` | `20` | 1–10240 MB | A site's saved-data history above this is thinned, oldest first. |
| `SAVED_DATA_SITE_MAX_MB` | `50` | 1–10240 MB | Largest a site's live saved data may grow. |
| `SAVED_DATA_SNAPSHOT_EVERY` | `50` | 1–10000 changes | History keeps a full copy of a site's data at least this often. |
| `SAVED_DATA_SWEEP_MINUTES` | `15` | 1–1440 minutes | How often expired history and deleted items are removed. |
| `SAVED_DATA_WATCH_DAYS` | `7` | 1–365 days | The window the admin page's saved-data watch reports. |
| `SAVED_DATA_WATCH_INC_MAX` | `10` | 1–1000000000 count | A visitor increment larger than this counts as large in the watch. |
| `SAVED_DATA_WATCH_ITEM_KB` | `16` | 1–64 KB | A list item larger than this counts as large in the watch. |
| `SAVED_DATA_WATCH_KEEP_DAYS` | `90` | 1–3650 days | Watch counts older than this are removed. |
| `SAVED_DATA_IDEMPOTENCY_HOURS` | `24` | 1–720 hours | How long a retried write gets its first answer back instead of applying twice. |
| `SAVED_DATA_IDEMPOTENCY_MAX_PER_SITE` | `10000` | 100–1000000 keys | Retry keys remembered per site; the oldest past this are dropped. |
| `SAVED_DATA_READ_PER_SEC` | `30` | 1–10000 reads | Saved-data reads per second per site and address. |
| `SAVED_DATA_READ_BURST` | `60` | 1–100000 reads | Saved-data reads allowed at once above that rate. |
| `SAVED_DATA_APPEND_PER_MIN` | `30` | 1–10000 items | List items one address may add per minute without the owner's key. |
| `SAVED_DATA_APPEND_BURST` | `30` | 1–100000 items | List items allowed at once above that rate. |
| `SAVED_DATA_CONTENT_MAX_KB` | `1024` | 1–10240 KB | Largest one Page info document may be. |
| `SAVED_DATA_CONTENT_NAMES_MAX` | `20` | 1–1000 names | Page info names one site may declare. |
| `SAVED_DATA_ENTRY_MAX_KB` | `16` | 1–64 KB | Largest one new Submissions entry may be. |
| `SAVED_DATA_ENTRIES_MAX` | `10000` | 1–1000000 entries | Live entries one Submissions name may hold. |
| `SAVED_DATA_WITHDRAW_UNDO_MINUTES` | `10` | 1–1440 minutes | How long a visitor can bring back an entry they withdrew. |
| `SAVED_DATA_NOTIFY_EACH_MINUTES` | `10` | 1–1440 minutes | "Email me: each" sends at most one email per name this often, listing what arrived. |
| `SAVED_DATA_NOTIFY_DAILY_HOURS` | `24` | 1–720 hours | "Email me: daily" sends at most one digest per name this often. |
| `SAVED_DATA_SAVERS_MAX` | `500` | 1–100000 entries | Emails and domains in one site's who-may-save and block lists together. |
| `SAVED_DATA_ENTRIES_NAMES_MAX` | `50` | 1–1000 names | Submissions names one site may declare. |
| `SAVED_DATA_PERSONAL_MAX_KB` | `64` | 1–1024 KB | Largest one person's record in one Personal name may be. |
| `SAVED_DATA_PERSONAL_NAMES_MAX` | `20` | 1–1000 names | Personal names one site may declare. |
| `SAVED_DATA_BOARD_ITEM_MAX_KB` | `16` | 1–64 KB | Largest one Shared board item may be. |
| `SAVED_DATA_BOARD_MAX` | `2000` | 1–1000000 items | Live items one Shared board may hold. |
| `SAVED_DATA_BOARD_NAMES_MAX` | `20` | 1–1000 names | Shared board names one site may declare. |
| `SAVED_DATA_PERSONAL_PEOPLE_MAX` | `1000` | 1–1000000 people | People who may hold a record in one Personal name. |
| `SAVED_DATA_BOARD_WRITES_PER_MIN` | `30` | 1–10000 writes | Shared board adds, changes and deletes one signed-in person may make per minute, on top of the per-address rate. |
| `SAVED_DATA_DEFAULT_KIND` | `shared` | `shared` / `declare_first` | What a name no one declared is on a site made after the kinds. shared: anyone reads it and signed-in visitors save to it. declare_first: it takes no saves until the owner declares it. Older sites are shared either way. **Security-sensitive.** |
| `WRITE_AUTH_MODE` | `log` | `off` / `log` / `on` | on: page saves need a signed-in visitor or the owner's key. log: allowed, and logged. **Security-sensitive.** |
<!-- /settings -->

## Recipes

**Shorter retention of saved-data history.**

```
SAVED_DATA_UNDO_DAYS=7
SAVED_DATA_HISTORY_MAX_MB=5
```

**Smaller per-site data on a small disk.**

```
SAVED_DATA_SITE_MAX_MB=10
```
