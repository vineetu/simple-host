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
including `DB_DSN` and `DATA_DIR`. Keep `KV_STORE=postgres` until the move
and the separate rollback rehearsal have been approved.

```text
simple-host kv-move --dry-run --scratch /tmp/kv-check --log /tmp/kv-dry-run.jsonl
simple-host kv-move --apply --log /tmp/kv-apply.jsonl
simple-host kv-move --verify --log /tmp/kv-verify.jsonl
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
lock if the process dies; leave the lock file in place.

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
command does not provide rollback yet; that and the rehearsal are the next
step. Once a namespace moves, use a program that understands its marker.

**What fills the disk.** At the default allowances, 2,000 fully used hosted sites
can add 40 GB of logical KV/SQLite and file storage (10 MB + 10 MB per site),
beyond deployed versions. Budget extra for filesystem metadata, Postgres, SQLite
WAL, backups and version retention. Storage quotas are shared within a site;
visitor rate limits slow filling them but do not reserve space for other visitors.

For deployed versions: set `KEEP_VERSIONS` (see
[Sites and versions](/docs/self-hosting/settings/sites-and-versions.md)). The admin page shows disk use.

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
[Kubernetes](/docs/self-hosting/kubernetes.md). The same client address controls the country gate.
