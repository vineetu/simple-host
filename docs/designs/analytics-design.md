# Server-side per-site visitor analytics — design

## Goal
Every user sees, per site, **how many people visit** and the trend over time — computed
**server-side** (no client JS beacon required), shown in the dashboard with a small graph.

## Why nginx logs are the source
Static pages are served by **nginx directly off disk** (the Go app never sees a page view).
All three serving paths go through nginx, so its access log is the one ground truth:
- content host `sites.simple-host.app/<handle>/<site>/…`
- legacy `<site>.simple-host.app/…`
- custom domain `<domain>/…`

So: **nginx access log → periodic Go ingester → aggregate tables → owner-scoped API → dashboard.**

## Pipeline

### 1. nginx — a dedicated, parseable analytics log
Add a `log_format analytics` emitting TAB-separated: `ts \t host \t status \t method \t uri \t remote_addr`.
Write it to `/var/log/simple-host/analytics.log` (dir owned/readable by the `simplehost` service
user, so the app can read it without root). `access_log … analytics` on the content-host, legacy,
and custom-domain server blocks. logrotate keeps it bounded (copytruncate so the app's offset stays valid → actually use size-based reset carefully; see Offsetting).

### 2. Ingester (in-app goroutine, every 5 min)
A goroutine in the Go app (runs as `simplehost`, already has DB) that:
- reads new lines since a persisted byte offset (see Offsetting),
- keeps only **page views**: method GET, status 2xx/3xx, and the URI is a *document* — path ends
  in `/` or `.html`, OR has no file extension. Exclude `/v1/…`, `/internal/…`, `/.well-known/…`,
  and asset extensions (css js mjs png jpg jpeg gif svg ico webp woff woff2 ttf map json xml txt pdf mp4 …),
- **attributes** each view to a `site_id`:
  - host == content host → parse `/<handle>/<site>/…` → GetUserByHandle → GetSiteByUser → id
  - host == `<label>.<siteDomain>` (legacy) → GetSiteIDByName(label)
  - else (custom domain) → GetSiteByCustomDomain(host) → id
  - unresolved → skip,
- hashes the client IP: `sha256(daily_salt + ip)` truncated — never store raw IPs (privacy),
- upserts aggregates (batched per run).

### 3. Schema (additive)
```sql
CREATE TABLE site_view_daily (           -- one row per site per day
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  day     DATE NOT NULL,
  views   BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, day)
);
CREATE TABLE site_visitor_daily (        -- distinct-visitor set (hashed IP), for uniques
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  day     DATE NOT NULL,
  ip_hash BYTEA NOT NULL,
  PRIMARY KEY (site_id, day, ip_hash)
);
CREATE TABLE analytics_ingest_state (    -- resumable log ingestion
  logfile TEXT PRIMARY KEY,
  offset_bytes BIGINT NOT NULL DEFAULT 0,
  inode  BIGINT NOT NULL DEFAULT 0        -- detect rotation (offset reset)
);
```
`views` increments always; `site_visitor_daily` insert-on-conflict-do-nothing, uniques =
`count(*)` per (site,day). Prune both view/visitor tables > 400 days on ingest.

### 4. API — owner-scoped
`GET /v1/sites/{sitename}/analytics?days=30` (auth; admin can read any via the same owner check
they already have). Returns:
```json
{ "range_days": 30,
  "totals": { "views": 1234, "visitors": 456 },
  "daily": [ { "day": "2026-07-11", "views": 42, "visitors": 30 }, … ] }
```
`visitors` = distinct ip_hash count. Add to openapi + a capability line in llms.txt/skills.

### 5. Dashboard
On each site card, a compact line: **“1,234 views · 456 visitors (30d)”** plus an inline **SVG
sparkline** of daily views (no chart library). Fetched from the analytics endpoint on card render
(lazy / on expand to avoid N calls on first paint — fetch per card when visible).

## Offsetting / rotation
Persist `(offset_bytes, inode)` per logfile. On each run: stat the file; if inode changed or size <
stored offset → rotation happened → reset offset to 0 (and, if we can find the rotated file, read
its tail too — v2; v1 just resumes from 0, accepting a small gap at rotation). copytruncate in
logrotate keeps the same inode but truncates → detect size < offset → reset to 0.

## Privacy
- Never store raw IPs; only `sha256(salt+ip)`, salt = a server secret (ADMIN_API_KEY-derived) that
  **rotates daily** (so hashes aren't linkable across days → not long-term tracking).
- Aggregates only; no per-request retention beyond the daily distinct-hash set (pruned at 400d).
- Bot filtering (v1, light): skip requests whose UA is empty or matches an obvious-bot regex — add
  `ua` to the log_format for this. (Optional; note if deferred.)

## Non-goals (v1)
Referrers, geo, per-path breakdown, real-time, sessions. (Design leaves room: the log has host+uri.)

## Review fixes folded in (adversarial critique)
- **P0 transactional offset:** the ingest-state offset UPDATE happens in the SAME tx as the
  view/visitor upserts → a crash re-processes at most one uncommitted batch, never double-counts
  the non-idempotent `views` counter.
- **P0 rotation:** track `(inode, offset)`; NO copytruncate — logrotate uses `create 0644
  simplehost simplehost` + reopen; on inode-change or size<offset, drain the rotated `.1` file
  from the old offset then start the new inode at 0.
- **P0 count only 200/304 GET documents; exclude 301/302/307/308** so trailing-slash redirects
  aren't double-counted; drop HEAD/OPTIONS.
- **P0 isolation:** ingest goroutine `recover()`s per run, bounded per-run line cap, statement
  timeout on the tx; never on the serving/request hot path; disabled unless `ANALYTICS_LOG` set.
- **P0 attribution via per-run CACHED maps** (handle→user_id, (user_id,name)→id, name→oldest id,
  custom_domain→id) — no DB query per line. `access_log analytics` scoped ONLY to the 3 serving
  vhosts (not the dashboard/skills/probes).
- **P1:** UTC everywhere (log `$time_iso8601` UTC, day bucket, and daily salt from the same UTC
  date); analytics log `buffer=off` (atomic lines); malformed/torn lines fail-soft (skip, continue);
  collapse to one upsert per (site,day) per run; batched visitor `INSERT … ON CONFLICT DO NOTHING`;
  bounded prune. Metric labeled "views (incl. bots)" for v1; `ua` logged for future filtering.
- **P2:** API resolves via the caller's user_id (`GetSiteByUser`), NOT global name; `daily` array
  is zero-filled dense for the range so the sparkline has a point per day.
- "unique visitors" = **distinct per UTC day** (daily-rotating salt); the dashboard must not imply
  monthly uniques.

## Rollout
1. schema migration (additive). 2. nginx log_format + access_log + log dir + logrotate.
3. app ingester goroutine + config (ANALYTICS_LOG path; default off if unset → safe).
4. API + openapi. 5. dashboard sparkline. 6. verify with real hits; 7. docs.
