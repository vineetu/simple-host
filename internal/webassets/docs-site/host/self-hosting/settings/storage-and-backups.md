# Storage and backups

A server keeps everything in two places:

- **Postgres:** accounts, keys, sites, versions, saved data and lists, domains.
- **A folder on disk (`DATA_DIR`):** every site's files, one folder per kept version.

On a small box both are Docker volumes (`simple-host_db` and `simple-host_sites`), and
`/opt/simple-host/.env` holds the admin key and the database password, which only that file
knows.


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
[Sites and versions](/docs/self-hosting/settings/sites-and-versions/)). The admin page shows disk use.

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
[Kubernetes](https://github.com/vineetu/simple-host/blob/main/docs/kubernetes.md). The same client address controls the country gate.
