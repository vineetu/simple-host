# Storage and backups

A server keeps everything in two places:

- **Postgres:** accounts, keys, sites, versions, saved data and lists, domains.
- **A folder on disk (`DATA_DIR`):** every site's files, one folder per kept version,
  runtime SQLite databases, file buckets and any SQLite key-value namespaces.

On a small box both are Docker volumes (`simple-host_db` and `simple-host_sites`), and
`/opt/simple-host/.env` holds the admin key and the database password, which only that file
knows.

<!-- settings:group=storage -->
| Setting | Default | Allowed | What it does |
|---|---|---|---|
| `VISITOR_AI_WRITES_PER_DAY` | `200` | 1–100000 writes | Visitor connector write attempts per person and site per UTC day, shared across apps and restarts. **Security-sensitive.** |
| `SITE_STORAGE_FILES_MAX_OBJECTS` | `1000` | 1–100000 objects | Maximum committed file objects per bucket, including empty files; overwrites do not add an object. |
| `SITE_STORAGE_SITE_INFLIGHT` | `1` | 1–4 operations | Concurrent SQLite row operations per site (1 to 4); the site slot and global gate share one acquire wait. |
| `SITE_STORAGE_SQL_CONCURRENCY` | `0` | 0–1024 queries | Concurrent SQLite executions across the process; defaults to the CPU count. |
| `SITE_STORAGE_ACQUIRE_WAIT_MS` | `250` | 1–5000 milliseconds | Wait for a SQLite execution slot before returning 503 with Retry-After. |
| `SITE_STORAGE_WRITE_LOCK_WAIT_MS` | `2000` | 1–10000 milliseconds | Wait for the site write/deploy lock before returning 503 with Retry-After. |
| `SITE_STORAGE_OWNER_TIMEOUT_MS` | `5000` | 1–30000 milliseconds | Owner SQLite query, schema and write timeout. |
| `SITE_STORAGE_VISITOR_WRITE_TIMEOUT_MS` | `2000` | 1–5000 milliseconds | Visitor storage write timeout. |
| `SITE_STORAGE_VISITOR_QUERY_TIMEOUT_MS` | `1000` | 1–5000 milliseconds | Visitor SQLite query timeout. |
| `RATE_LIMIT_STORAGE_IP` | `120,100ms` | any (warns past 10× looser) | Visitor storage writes per site and client IP; owner credentials are exempt. |
| `RATE_LIMIT_STORAGE_VISITOR` | `60,200ms` | any (warns past 10× looser) | Storage writes per site and signed-in visitor; limits add-only flooding. |
| `STORAGE_SWITCH_AT` | none | text | RFC3339 UTC retirement instant for storage presets and older saved-data APIs. Empty means no switch date set. Requires STORAGE_RULES=on. Set the environment (Compose) or values file (Helm) and restart to change it. **Security-sensitive.** |
| `STORAGE_RULES` | `off` | `off` / `on` | Accept owner-written row rules for SQLite databases and moved key-value namespaces. Saved rules remain effective when this switch is off. Set the environment (Compose) or values file (Helm) and restart to change it. **Security-sensitive.** |
| `KV_STORE` | `postgres` | `postgres` / `sqlite` | Store for newly created key-value namespaces. Existing namespaces follow their saved marker. Restart after changing the environment or Helm values. |
| `DB_DSN` | none | secret | The Postgres connection string. install.sh sets it for the bundled database. **Required.** **Security-sensitive.** |
| `DB_MAX_OPEN_CONNS` | `25` | at least 1 connections | Maximum open Postgres connections per server process; excess requests wait. |
| `DB_MAX_IDLE_CONNS` | `5` | at least 0 connections | Maximum idle Postgres connections per server process; zero keeps none. |
| `DB_CONN_MAX_LIFETIME_MINUTES` | `30` | at least 1 minutes | Maximum lifetime of each pooled Postgres connection. |
| `DB_CONN_MAX_IDLE_MINUTES` | `5` | at least 1 minutes | Maximum idle time of each pooled Postgres connection. |
| `MIGRATE_ON_START` | `false` | `true` / `false` | true makes the server build an empty database from the embedded schema.sql and apply every pending migration before it listens (the same as `simple-host migrate -init`). For Kubernetes, where there is no psql and no install command; two pods starting at once are safe (an advisory lock). |
| `DATA_DIR` | `./data/sites` | text | The folder that holds every site's files and versions. |
| `SITE_STORAGE_MAX_BYTES` | `10000000` | 1–10737418240 bytes | Durable byte budget per site for KV and SQLite (Hack defaults to 1000000, pooled with files); SQLite WAL and backups need additional temporary disk. |
| `SITE_STORAGE_FILES_MAX_BYTES` | `10000000` | 1–10737418240 bytes | Separate raw-file allowance per website on Simple Host; Hack retains SITE_STORAGE_MAX_BYTES pooled allowance. |
| `SITE_STORAGE_FILE_MAX_BYTES` | `1000000` | 1–10737418240 bytes | Largest individual raw-file upload for a site storage bucket. |
| `SITE_STORAGE_SQL_RESULT_MAX_BYTES` | `1000000` | 1–67108864 bytes | Largest JSON result from one site SQLite query. |
<!-- /settings -->

## Recipes

**Back up a small box** (from `/opt/simple-host`, into the current folder):

```
sudo docker compose exec -T db pg_dump -U simplehost simplehost | gzip > db-$(date +%F).sql.gz && sudo docker run --rm -v simple-host_sites:/sites -v "$PWD":/out alpine tar czf /out/sites-$(date +%F).tar.gz -C /sites . && sudo cp .env env-$(date +%F).bak
```

Keep all three together: the `.env` backup is what lets a restored database be opened.

After a key-value namespace moves to SQLite, its data lives at
`runtime/kv/<namespace>.sqlite`. The Postgres dump and matching data directory
are one backup: restore them together. Stop the install before taking the pair
for a consistent snapshot. A live SQLite copy must include its `-wal` file;
copying the main file alone can lose committed writes. Stopping first avoids
copying files while they change. Site exports combine both KV stores into
`storage/kv.json` and omit the internal `runtime/kv/` files.

Keep `KV_STORE=postgres` until the move and rehearsal are ready. This release
adds export, deletion and authorship support; changing the flag does not move
existing namespaces.

## Checking and moving key-value namespaces

Run `kv-move` with the currently installed program (`/usr/local/bin/simple-host`
in the standard installs), using that install's environment,
including `DB_DSN` and `DATA_DIR`, and as the user the server runs as. Keep
`KV_STORE=postgres` until the move and the rollback rehearsal have been approved.

`kv-move` refuses, changing nothing, when the user running it does not own
`DATA_DIR`: files created by another user (root, for example) could not be
opened by the server afterwards.

```text
simple-host kv-move --dry-run --scratch /tmp/kv-check --log /tmp/kv-dry-run.jsonl
simple-host kv-move --apply --log /tmp/kv-apply.jsonl
simple-host kv-move --verify --log /tmp/kv-verify.jsonl
simple-host kv-move --rollback --log /tmp/kv-rollback.jsonl
```

Repeat `--site <id>` to select sites; omitting it selects every key-value
namespace, including sites in Recently deleted. Other resource kinds are
unaffected.

Dry run can run beside the server. It reads a repeatable Postgres snapshot,
builds and checks temporary files under `--scratch`, and removes those files
afterwards. Its JSON-lines report gives entry counts, logical bytes, copy
hashes, whether Postgres's key order equals byte order, and the number of keys
that differ only by case. Keys, values and authors are not included in the log.
A copy difference exits 3; other failures exit 1. A namespace whose Postgres
order differs will list in byte order after moving, so old page cursors may
skip or repeat keys.

Apply requires every server using the data directory to be stopped. Servers
hold a shared lock on `DATA_DIR/.maintenance.lock` for their lifetime; apply
takes an exclusive lock without waiting and exits 2 if it cannot. A server
also refuses to start while apply holds that lock. The kernel releases the
lock if the process dies; leave the lock file in place. The file is empty and
world-readable, and a server opens it read-only, so a lock file created by
another user does not stop the server. If the server cannot read the file,
its error gives the file's path, owner and mode.

Apply and rollback also require every server using the **database** to be
stopped, whichever data directory it serves from. A server holds a shared
Postgres advisory lock on its database while it runs; apply and rollback take
that lock exclusively without waiting and exit 2 if they cannot, and a server
refuses to start while a command holds it. Postgres releases the lock when
the process's connection ends.

### The database and the data directory must be the same install

Each install has an identity: one random id, saved in the file
`DATA_DIR/.install-id` and in the single row of the `install_identity` table.
The server creates both at start when neither exists, and never changes
them. Back up and restore the database and the data directory together and
the pair stays matched.

Apply, rollback and verify refuse, changing nothing and exiting 1, unless both
copies exist and are equal. This stops a command given one install's `DB_DSN`
and a different install's `DATA_DIR` from moving data into the wrong place.
Dry run is not tied to a data directory and does not check.

The identity does **not** tell a data directory from a copy of it. A copy
(a rehearsal's scratch copy, a restored backup in another path) holds the same
`.install-id` and passes the check. The database lock above stops a command
pointed at a copy only while a server is running on that database; with the
server stopped, nothing does. Before every `--apply` or `--rollback`, check
that `DATA_DIR` in the env file the command reads is the directory the server
serves from, not a copy.

When neither copy exists, start the server once on that database and data
directory, stop it, and run the command again.

When exactly one copy exists, or the two differ, the server writes a
`WARNING: INSTALL IDENTITY` line to its log at each start and keeps serving.
Find out which half was replaced before doing anything else: the usual causes
are a restored database with an older data directory, or an env file that
points at another install. Only when you are certain the two belong together,
with the server stopped, make them agree by hand: copy the id from the
`install_identity` row into `DATA_DIR/.install-id` (one line, owned by the
server's user), or the other way round.

For each namespace, apply builds a file without a WAL, compares every key,
normalized JSON value, author and timestamp, checks integrity and the logical
byte count, and writes the copy proof to the log. It syncs and renames the
file and syncs the containing directories through the site directory, then
changes the store marker and deletes the Postgres rows in one
transaction. Check the command's exit status: the copy proof alone does not
mean the final transaction committed. An unresolved site directory is
reported and left on Postgres. Re-running apply removes interrupted copies
for Postgres namespaces and skips namespaces already moved.

Verify is read-only and can run beside the server after later writes. It
checks the SQLite file, JSON values, entry and byte totals, and absence of
Postgres rows for moved namespaces. It does not compare with the old source
content. A leftover file for a Postgres namespace is a warning. A namespace
changing during verification is retried once; a second change is reported
as a warning, not corruption. Other health failures exit nonzero.

Moving data on a live install is a separate operator step. Take the paired
Postgres and data-directory backup while stopped before applying. This
command includes rollback; production cutover remains a separate approved step.
Once a namespace moves, use a program that understands its marker.

Rollback also requires the exclusive maintenance lock. It checkpoints and checks
each SQLite namespace, inserts every current entry into Postgres and reads those
rows back in the same transaction. It compares values, authors and both timestamps
before changing the marker to `postgres`. A mismatch or failed JSON cast rolls
the transaction back and leaves the namespace on SQLite. Later writes, edits
and deletes are preserved. Repeat `--site` to limit the rollback to selected sites.

Two kinds of namespace are left as they are:

- **A namespace with saved rules is refused.** Rules are enforced only on the
  SQLite store, so moving it back would stop enforcing them. The error and the
  log line name the site and the namespace, neither the file nor the database
  is touched for it, every other namespace is still rolled back, and the
  command exits 1.
- **A namespace that already has rows in Postgres is skipped.** Nothing is
  changed and it is not a failure: the command exits 0 when that is all it
  reports. The log line's status says which case it is. `skipped: already
  restored` means the namespace is already on Postgres. `skipped: still on
  SQLite; Postgres already holds rows for it (run --verify)` means it was
  **not** rolled back and is still served from SQLite. Exit 0 therefore does
  not prove every namespace is back on Postgres: read the log as well as the
  exit status, and run `--verify` afterwards, which reports a namespace still
  marked `sqlite` that has Postgres rows.

After commit the old file is retained as `<namespace>.sqlite.rolledback-<UTC time>`
and its directory is synced, so a crash cannot bring the old name back.
If the rename fails, the leftover active file is ignored by the Postgres marker;
a later apply removes it and rebuilds from Postgres. A kill between commit and
rename has the same safe result. Repeating rollback skips namespaces already on
Postgres. Set `KV_STORE=postgres` before restarting if new namespaces should use
Postgres too.

### Rehearse the cutover and both ways back

Run `scripts/kv-move-rehearsal.sh` once for each install, passing its program,
product (`host` or `hack`), a custom-format dump from the stopped install and the
matching whole data-directory copy. `TEST_PG_URL` supplies a bare disposable
Postgres server URL; the script creates and drops only its own database. Inputs
are read only. It uses fresh local credentials, removes copied notification
subscriptions and saved setup hostnames on the scratch database, and inherits no
provider credentials. The scratch environment supplies its local hostname.

The script restores the copies, starts a loopback instance, dry-runs, checks free
space (data plus dump plus 20%), stops, takes the paired backup with sizes and
SHA-256 hashes, applies, verifies and restarts with `KV_STORE=sqlite`. It compares
all key-value records and live-site owner reads, usage and exported `kv.json`,
and exercises page and owner-tool writes, reads and deletes on an invented site.
It compares the usage tool's decoded response with its pre-stop response. Apply
must leave every key-value namespace on SQLite and the Postgres KV table empty;
rollback must return every namespace, including newly created ones, to Postgres.
It then makes new keys, edits, deletes, a new namespace and a namespace
deletion, verifies, stops, rolls back, restarts and compares again. Finally it
restores the paired dump and files onto an emptied scratch directory and compares
with the backup-time state. This last restore deliberately loses the later
changes; it is a last resort, never an overlay onto the current directory.

The output directory retains JSON-lines move logs, per-step wall times, backup
sizes/hashes and key-value read throughput before and after moving (100 reads
with one and four clients on one site, `SITE_STORAGE_SITE_INFLIGHT=1`). Scratch
databases, file copies and backups are deleted on exit. Keep the output from both
installs for the cutover review. A rehearsal on invented fixtures alone does not
establish that production copies have passed.

The upgrade journal and step list are not available in this release. When they
land, the move registers as `OptIn` (`upgrade --with kv-move`), with progress per
namespace, not a remembered install-wide completion. Every attempt must inspect
the current markers and Postgres rows; successful verification must also require
zero Postgres namespaces and rows. Standalone apply/rollback take their own lock;
the future upgrade command holds it across its steps instead.

## Converting presets to row rules

`migrate-rules` is a command for the person who runs the install. It turns the
preset on a SQLite database or key-value namespace into row rules that decide
the same, and can put the preset back. It is not offered to site owners: there
is no API route, AI tool or recipe for it. File buckets keep presets and are
never read or changed by it.

Run it with the installed program and that install's environment (`DB_DSN`,
`DATA_DIR` and the spam-check settings), as the user the server runs as.

```text
simple-host migrate-rules --dry-run --plan rules-plan.json --account <handle>
simple-host migrate-rules --dry-run --plan rules-plan.json --all
simple-host migrate-rules --apply   --plan rules-plan.json
simple-host migrate-rules --revert  --plan rules-plan.json
simple-host migrate-rules --revert  --site <site id> --resource <name>
```

Repeat `--account` for several accounts. Sites in Recently deleted are included.

Apply and revert with `--plan` require a regular file owned by the command's
user or root, with no group or other write permission. Its directories must
not be writable by others unless the sticky bit is set (as on `/tmp`). The
plan must record the same commit as the running program. Use the same program
for the dry run, apply and plan-based revert. A refused plan changes no resource.

**Dry run.** The server may be running. Nothing is changed: Postgres is only
read, each SQLite database is opened read-only, no saved row is read, and no
request is sent. For every SQLite or key-value resource still on a preset it
works out the rules the preset converts to, checks that they fit the tables
that exist, and compares, for each kind of caller (not signed in, signed in,
the author, the owner signed in on the site, a visitor's AI, each with and
without a passed spam check) and each action, what the preset allows today
with what the rules would allow. It prints one line for each answer that would
change:

```text
account site resource table caller action old new
```

A resource with such a line is not converted. Neither is a resource that
"needs a person", which is listed with the reason:

- a database or namespace saved before presets that pages could write freely,
  where saving rules would clear recorded authors or require an empty database;
- a database that pages still read with their own SQL, which stops when a
  resource has rules;
- a setting that lets anyone edit or delete, which no rule may say;
- converted rules that the existing tables cannot take, for example a
  `visitor_id` or `approved_at` column that is not text.

The owner of such a resource writes its rules by saving them in the ordinary
way. The dry run writes the plan file, readable only by the user who ran it. For
each resource to convert the plan holds a fingerprint of its settings, a digest
of its table columns, its complete present settings, the rules it converts to
and a SHA-256 of the entry. Exit codes: 0 when everything in scope can be
converted, 3 when something would change or needs a person (the plan still
lists what can be converted), 1 when a resource could not be examined (no plan
is written).

**Apply.** Stop the server first: apply takes the data directory's
maintenance lock and exits 2 while a server or another maintenance command
holds it. For each resource in the plan it checks again, before changing
anything, that the settings and table columns are exactly what the dry run
saw and that the rules it would write are the plan's. A resource that an owner
saved, or whose tables changed, since the dry run is skipped as "changed since
the dry run" with nothing recorded; run a new dry run for it. A key-value
namespace still stored in Postgres is skipped until it has been moved (see
above). Then, in order:

1. the present settings are recorded on the resource (`rules_converted_from`);
2. the database gets the columns a rules save adds, the author column and its
   index and the approval stamp where the rules name them, and nothing is
   cleared or removed;
3. in one transaction, the rules replace the preset.

If the command stops after step 1 or step 2 the resource still works under its
preset for everyone; run `--apply` again with the same plan. Applying a plan
twice changes nothing. Exit 0 when every resource is converted, 1 when any was
skipped or failed. The additive SQLite step is not held to the site's storage
allowance.

**Revert.** Also with the server stopped. For each resource the plan names, or
the one named by `--site` and `--resource`, the recorded settings and table
presets are restored in one transaction and the record is cleared. A resource
whose rules are no longer the ones apply wrote (its owner saved different
rules, or went back to a preset) is skipped as "rules were changed after
conversion"; its owner decides by an ordinary save. Columns added to SQLite
stay; presets use or ignore them as before.

A program older than the one that first enforces rules must never be installed
over converted resources: it would not apply their rules. Revert every
converted resource before going back that far.


**What fills the disk.** At the default allowances, 2,000 fully used hosted sites
can add 40 GB of logical KV/SQLite and file storage (10 MB + 10 MB per site),
beyond deployed versions. Budget extra for filesystem metadata, Postgres, SQLite
WAL, backups and version retention. Storage quotas are shared within a site;
visitor rate limits slow filling them but do not reserve space for other visitors.

For deployed versions: set `KEEP_VERSIONS` (see
[Sites and versions](sites-and-versions.md)). The admin page shows disk use.

## Postgres client pool

`DB_DSN` selects the database. Each server process bounds its own `database/sql` pool.
Go clamps `DB_MAX_IDLE_CONNS` to the open limit.

Unset or empty values use these defaults; invalid values stop startup with the variable name.
The open limit of 25 is sized for two instances of this binary sharing one 100-connection
Postgres role, leaving headroom for other services on the box. These are client settings;
they do not change Postgres server configuration. Set them in each instance's environment
and restart that instance to apply them.

## Storage execution settings

`SITE_STORAGE_SQL_CONCURRENCY=0` uses the CPU count. See the table above for
slot waits, write/deploy-lock waits and owner/visitor execution timeouts.
The durable allowance excludes deployment versions, legacy saved data and transient SQLite WAL.
The website file allowance still applies to each raw-file upload.

Without `TRUSTED_PROXY_CIDRS` the client address is the last `X-Forwarded-For` entry from any peer. Keep the app behind a local proxy that appends the transport client address and restrict direct access to the app port; an unproxied install set up that way has no trustworthy IP write limit. With `TRUSTED_PROXY_CIDRS` set (Kubernetes, a load balancer in front), the header is believed only from those networks and a direct connection from anywhere else is its own client.

Kubernetes sets trusted proxy networks to the cluster pod and node ranges; see
[Kubernetes](../kubernetes.md). The same client address controls the country gate.


## Retirement notices

The operator can recompute the two notice lists without stopping the server:

```text
simple-host migrate-rules --list-recipients --out recipients.json --legacy-log legacy-calls.log
```

Run with that install’s `DB_DSN` and `DATA_DIR`. Run as root for a root-only
report; the output file is replaced with mode 0600 and the terminal prints
only counts. The command is read-only and sends nothing. Omit `--legacy-log`
when no call log has been collected. Supply only the `legacy_storage_call`
lines collected since R4-S3 was deployed, including rotated logs. Each running
server logs `legacy_storage_call site=<id>` at most once per site per UTC day.
This includes sites whose calls store no rows.

`presets` contains each account holding a site with a SQLite or key-value
resource still using presets, with one description per resource and table
settings included. Conversion of an account’s earlier resources never excludes
its later ones. File buckets are excluded. `older_saved_data` contains owners
with stored state, collections or declared data, or call-log evidence. It is
empty on Simple Hack, whose older routes already answer 410. Resources held by
an event go in `held_by_an_event`, with the event label and no mail recipient.
The startup resource total includes these event-held resources too. Recompute
both lists immediately before each send and reminder.

Drafts await the owner’s approval in
[the notice draft](../history/storage-rules-notice-draft.md). The site export
keeps saved state, declared data and every collection except other people’s
Personal records, which it has never contained. Each person’s own Personal
records stay in that person’s account export, with every item they submitted
on someone else’s site and its data. This does not widen owner access.

`STORAGE_SWITCH_AT` is an RFC3339 UTC instant. Empty means no date set; startup
refuses a date unless `STORAGE_RULES=on`. Change the environment in Compose or
`storageSwitchAt` in Helm values and restart. Setting the date in this release
adds a warning to successful preset saves during the notice period, for SQLite
and key-value resources only. It does not retire routes or change access in
this slice. The server logs the number still using presets once at startup.

Wording comes from JSON templates. To change it without rebuilding, copy
`internal/app/storage/storage-notice.json` from the source into
`DATA_DIR/storage-notice.json` (outside all site directories), edit it and
restart. The fields are `presets`, `legacy`, `save_warning`, `rules_guide` and
`moving_guide`; `<DATE>`, `<RULES_GUIDE_URL>` and `<MOVING_GUIDE_URL>` are filled
from the setting and template data. Site/resource lines in the drafts remain
placeholders for the separately approved send step. Missing override uses the
bundled draft. Invalid JSON stops startup and the recipient command.
