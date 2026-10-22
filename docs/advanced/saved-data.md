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
| `NOTIFY_MAX_PER_SITE` | `10` | 1–1000 subscriptions | Notification subscriptions allowed per site. |
| `NOTIFY_PER_MINUTE` | `60` | 1–100000 events | Visitor events accepted per site per minute. |
| `NOTIFY_PER_DAY` | `5000` | 1–10000000 events | Visitor events accepted per site per day. |
| `NOTIFY_HISTORY_DAYS` | `7` | 1–365 days | Days of notification events and delivery history retained. |
| `NOTIFY_OWNER_CONCURRENCY` | `5` | 1–200 deliveries | Concurrent notification deliveries allowed per owner across all workers. |
| `NOTIFY_PER_URL_PER_MINUTE` | `120` | 1–100000 attempts | Notification attempts per exact URL per minute across all sites and subscriptions. |
| `NOTIFY_WORKERS` | `20` | 1–200 workers | Concurrent notification delivery workers. |
| `NOTIFY_ATTEMPTS` | `5` | 1–20 attempts | Maximum delivery attempts per event and subscription. |
| `NOTIFY_FIELD_MAX_BYTES` | `2048` | 1–65536 bytes | Maximum bytes in a notification string field before truncation. |
| `NOTIFY_BODY_MAX_BYTES` | `16384` | 4096–262144 bytes | Maximum event body bytes before fields are omitted. |
| `NOTIFY_SUSPEND_ATTEMPTS` | `100` | 1–100000 attempts | Attempts needed before the rolling failure percentage can suspend a subscription. |
| `NOTIFY_SUSPEND_FAILURE_PERCENT` | `95` | 1–100 percent | Failure percentage above which a subscription is suspended. |
| `NOTIFY_TIMEOUT_MS` | `5000` | 1–30000 milliseconds | Total timeout for a notification attempt, in milliseconds. |
| `NOTIFY_DEFAULT_TTL_HOURS` | `168` | 1–8760 hours | Default lifetime of an MCP event subscription, in hours. |
| `NOTIFY_SUSPEND_WINDOW_HOURS` | `1` | 1–168 hours | Rolling window for the suspension failure percentage, in hours. |
| `NOTIFY_SUSPEND_DAYS` | `3` | 1–365 days | Days of continuous failures before a subscription is suspended. |
| `RATE_LIMIT_SPAM_CHECKED_IP` | `10,30s` | stricter freely; loosest `40,7.5s` | Posts on comments-preset resources (anyone posts after the Turnstile spam check), per address across every site. **Security-sensitive.** |
| `RATE_LIMIT_SPAM_CHECKED_SITE` | `60,10s` | stricter freely; loosest `240,2.5s` | Posts on comments-preset resources, per site from everyone together. **Security-sensitive.** |
| `SPAM_CHECKED_POST_MAX_BYTES` | `4000` | 100–65536 bytes | Largest post someone not signed in may add to a comments-preset resource (the JSON of its values). |
| `SPAM_CHECKED_POST_MAX_LINKS` | `2` | 0–50 links | Links (http://, https://, www.) one such post may contain. |
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
| `TURNSTILE_SITE_KEY` | none | text | The Cloudflare Turnstile site key for the spam check on the comments preset (anyone posts without signing in once the check passes). With no site key and secret, the preset is not offered. The widget's hostnames must include this server's own address (PUBLIC_BASE_URL), where the check runs in a small frame. |
| `TURNSTILE_SECRET` | none | secret | The Turnstile secret key that goes with TURNSTILE_SITE_KEY. Prefer TURNSTILE_SECRET_FILE. **Security-sensitive.** |
| `TURNSTILE_SECRET_FILE` | none | text | A file holding the Turnstile secret key (read once at start, used when TURNSTILE_SECRET is empty), for example a systemd credential: LoadCredential=turnstile-secret:/path and TURNSTILE_SECRET_FILE=%d/turnstile-secret. **Security-sensitive.** |
| `WRITE_AUTH_MODE` | `log` | `off` / `log` / `on` | on: page saves need a signed-in visitor or the owner's key. log: allowed, and logged. **Security-sensitive.** |
| `LEGACY_STORAGE_CUTOFF` | `off` | text | YYYY-MM-DD (00:00 UTC that day): sites created on or after this date get 410 legacy_storage_closed from the old saved-state, collections and declared-data routes. Empty or off disables the gate (the default, so self-hosted boxes are unchanged). An unparsable value logs a warning at boot and disables the gate. simple-host.app: 2026-10-12. |
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

**Turn on the comments preset (anyone posts after a spam check).** The preset needs a
Cloudflare Turnstile widget, which is free. Without both keys the server refuses the preset
(400 `spam_check_unavailable`), the dashboard and the connector leave it out, and
`get_page_recipe` sends guestbooks and comments to `wall`, where people sign in to post.

1. In the Cloudflare dashboard, open Turnstile, choose Add widget, name it, and add this
   server's own address as the hostname: the host of `PUBLIC_BASE_URL` (on a small box,
   the dashboard address, not the sites address). Widget mode Managed is fine.
2. Copy the site key and the secret key into the server's environment. On a small box
   installed with `install.sh`, add `TURNSTILE_SITE_KEY=...` and `TURNSTILE_SECRET=...` to
   `/opt/simple-host/.env` (re-running `install.sh` keeps both), then restart:

```
cd /opt/simple-host && sudo docker compose up -d
```

3. The boot log says `spam check: Turnstile on`. Your sites, Manage, Storage now offers Comments.
   Under systemd, prefer `TURNSTILE_SECRET_FILE` with a credential over the plain secret.
