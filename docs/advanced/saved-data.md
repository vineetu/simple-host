# Saved data

Every site gets a small backend in the same upload: one shared JSON document (page data) with
atomic operations, and lists visitors add to (RSVPs, votes, sign-ups). A list can be private: only
signed-in visitors add, only the owner reads.

**Undo.** Every change to page data, and every deleted or edited list item, is kept for
`SAVED_DATA_UNDO_DAYS` and can be restored by the site's owner. History past its size cap is
thinned oldest first, always keeping each item's first change of every day. Unlike the dates in
Cleanup and retention, shortening the undo window applies to what is already kept at the next
sweep.

**Limits.** A site's live saved data may not grow past `SAVED_DATA_SITE_MAX_MB`; writes that do
not grow it always go through. Reads and list additions are rate limited per address.

**The watch.** The admin page counts, per site and day, the kinds of visitor writes a later
release will tighten, so an operator can see who would be affected first.

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
