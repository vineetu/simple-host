# Storage and backups

A server keeps everything in two places:

- **Postgres:** accounts, keys, sites, versions, saved data and lists, domains.
- **A folder on disk (`DATA_DIR`):** every site's files, one folder per kept version.

On a small box both are Docker volumes (`simple-host_db` and `simple-host_sites`), and
`/opt/simple-host/.env` holds the admin key and the database password, which only that file
knows.

<!-- settings:group=storage -->
| Setting | Default | Allowed | What it does |
|---|---|---|---|
| `SITE_STORAGE_FILES_MAX_OBJECTS` | `1000` | 1–100000 objects | Maximum committed file objects per bucket, including empty files; overwrites do not add an object. |
| `SITE_STORAGE_SQL_CONCURRENCY` | `0` | 0–1024 queries | Concurrent SQLite executions across the process; defaults to the CPU count. |
| `SITE_STORAGE_ACQUIRE_WAIT_MS` | `250` | 1–5000 milliseconds | Wait for a SQLite execution slot before returning 503 with Retry-After. |
| `SITE_STORAGE_WRITE_LOCK_WAIT_MS` | `2000` | 1–10000 milliseconds | Wait for the site write/deploy lock before returning 503 with Retry-After. |
| `SITE_STORAGE_OWNER_TIMEOUT_MS` | `5000` | 1–30000 milliseconds | Owner SQLite query, schema and write timeout. |
| `SITE_STORAGE_VISITOR_WRITE_TIMEOUT_MS` | `2000` | 1–5000 milliseconds | Visitor storage write timeout. |
| `SITE_STORAGE_VISITOR_QUERY_TIMEOUT_MS` | `1000` | 1–5000 milliseconds | Visitor SQLite query timeout. |
| `RATE_LIMIT_STORAGE_IP` | `120,100ms` | any (warns past 10× looser) | Visitor storage writes per site and client IP; owner credentials are exempt. |
| `RATE_LIMIT_STORAGE_VISITOR` | `60,200ms` | any (warns past 10× looser) | Storage writes per site and signed-in visitor; limits add-only flooding. |
| `DB_DSN` | none | secret | The Postgres connection string. install.sh sets it for the bundled database. **Required.** **Security-sensitive.** |
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

**What fills the disk.** At the default allowances, 2,000 fully used hosted sites
can add 40 GB of logical KV/SQLite and file storage (10 MB + 10 MB per site),
beyond deployed versions. Budget extra for filesystem metadata, Postgres, SQLite
WAL, backups and version retention. Storage quotas are shared within a site;
visitor rate limits slow filling them but do not reserve space for other visitors.

For deployed versions: set `KEEP_VERSIONS` (see
[Sites and versions](sites-and-versions.md)). The admin page shows disk use.
