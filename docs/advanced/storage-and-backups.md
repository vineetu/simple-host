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
| `DB_DSN` | none | secret | The Postgres connection string. install.sh sets it for the bundled database. **Required.** **Security-sensitive.** |
| `DATA_DIR` | `./data/sites` | text | The folder that holds every site's files and versions. |
<!-- /settings -->

## Recipes

**Back up a small box** (from `/opt/simple-host`, into the current folder):

```
sudo docker compose exec -T db pg_dump -U simplehost simplehost | gzip > db-$(date +%F).sql.gz && sudo docker run --rm -v simple-host_sites:/sites -v "$PWD":/out alpine tar czf /out/sites-$(date +%F).tar.gz -C /sites . && sudo cp .env env-$(date +%F).bak
```

Keep all three together: the `.env` backup is what lets a restored database be opened.

**What fills the disk.** Versions, not sites: set `KEEP_VERSIONS` (see
[Sites and versions](sites-and-versions.md)). The admin page shows disk use.
