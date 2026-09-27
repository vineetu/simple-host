# Observability

- **Health:** `GET /healthz` answers 200 when the server is up.
- **Startup log:** the release and commit, every setting changed from its default, and a
  `WARNING` for a rate limit set far looser than its default or a `RATE_LIMIT_*` name that is not
  a setting (a typo). On a small box: `cd /opt/simple-host && sudo docker compose logs app`.
- **Admin page (`/admin`, admin key):** disk use, the running release, every account and site,
  API traffic by route and caller, and the saved-data watch.
- **Visit analytics** per site come from the web server's access log, not a tracking script.
  Visitor addresses are hashed with a salt; where a caller is comes from a database on the
  server, never a lookup elsewhere.

<!-- settings:group=observability -->
| Setting | Default | Allowed | What it does |
|---|---|---|---|
| `ANALYTICS_PAGES_PER_SITE_DAY` | `200` | 10–10000 pages | Distinct pages a site's Top pages keeps per day; views of further new pages that day are counted together as (other). |
| `ANALYTICS_REFERRERS_PER_SITE_DAY` | `100` | 10–10000 domains | Distinct referring domains a site keeps per day; further new domains that day are counted together as (other). |
| `ANALYTICS_LOG` | none | text | The web server's access log that visit analytics are read from. Empty: no analytics. |
| `ANALYTICS_SALT` | none | secret | Salt for the hashed visitor addresses in analytics. Empty: derived from ADMIN_API_KEY. **Security-sensitive.** |
| `GEOIP_DIR` | `<DATA_DIR>/../geoip` | text | Where the local location databases are. Missing files mean blank locations, never a lookup elsewhere. |
<!-- /settings -->
