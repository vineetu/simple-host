# Server-side per-site visitor analytics — design

**Status — 2026-09-01: SHIPPED. This document describes the live v2 design.**
It began as the v1 proposal and has been corrected in place, so read it as a
description of what runs, not as a plan. Two things changed after v1 and both
invalidate anything you may remember from the original: every bucket is now
**classified** (`person` / `bot` / `infra`, plus `unknown` for history recorded
before the classifier existed), and buckets are **hourly**, not daily.

The authoritative contract for the HTTP shape is
`internal/handler/static/openapi.yaml`. The code is `internal/analytics/`
(`ingest.go`, `classify.go`) and `internal/db/analytics.go` (read paths); the
tables are in `db/schema.sql` and `db/migrations/analytics-v2.sql`. If this
document and any of those disagree, they are right and this is stale — fix it
here rather than "fixing" the code to match.

## Goal
Every user sees, per site, **how many people visit** and the trend over time — computed
**server-side** (no client JS beacon required), shown in the dashboard split by who was asking, so
"people" is not quietly padded with crawlers and uptime probes.

## Why nginx logs are the source
Static pages are served by **nginx directly off disk** (the Go app never sees a page view).
All three serving paths go through nginx, so its access log is the one ground truth:
- content host `sites.simple-host.app/<handle>/<site>/…`
- legacy `<site>.simple-host.app/…`
- custom domain `<domain>/…`

So: **nginx access log → periodic Go ingester → aggregate tables → owner-scoped API → dashboard.**

## Pipeline

### 1. nginx — a dedicated, parseable analytics log
A `log_format` emitting seven TAB-separated fields:
`ts \t host \t status \t method \t uri \t remote_addr \t user_agent`.
The seventh field is what makes classification possible; it is not optional. (Live, the format is
named `shanalytics` in `/etc/nginx/conf.d/analytics-logformat.conf` — the nginx side is not in this
repo.) Lines written before the format grew that field still parse, they just classify as `bot`
(empty UA).

Write it to `/var/log/simple-host/analytics.log` (world-readable, so the app reads it without
root; nginx owns the file as `www-data`). `access_log … shanalytics` on the content-host, legacy,
and custom-domain server blocks — those three only, not the dashboard/skills/probe vhosts.
logrotate keeps it bounded — `create`, never `copytruncate`, because
the ingester tracks its position by (inode, byte offset) and uses the inode change as the signal to
drain `.log.1` first; see `deploy/prod/logrotate-analytics.conf` and Offsetting below.

### 2. Ingester (in-app goroutine, every 5 min)
A goroutine in the Go app (runs as `simplehost`, already has DB) that:
- reads new lines since a persisted byte offset (see Offsetting),
- keeps only **page views**: method GET, status **200 or 304 only** (not 2xx/3xx at large — a
  redirect is not a view, and a scanner spraying 404s never reaches any column), and the URI is a
  *document* — path ends in `/`, `.html` or `.htm`, OR the last segment has no extension. Exclude
  `/v1/…`, `/internal/…`, `/.well-known/…`, and asset extensions (css js mjs png jpg jpeg gif svg
  ico webp avif woff woff2 ttf otf eot map json xml txt pdf mp4 webm mp3 wav zip wasm),
- **attributes** each view to a `site_id`, using per-run cached maps rather than a query per line:
  - host == content host → parse `/<handle>/<site>/…` → handle→user_id → (user_id,name)→site_id
  - host == `<label>.<siteDomain>` (legacy) → label → oldest site_id with that name
  - else (custom domain) → lower(custom_domain)→site_id
  - unresolved → skip,
- **classifies** the request (`internal/analytics/classify.go`) into `person` / `bot` / `infra`,
  from remote_addr + User-Agent + path. See Classification below,
- hashes the client IP: `sha256(stable_salt + ip)` truncated to 16 bytes — never store raw IPs
  (privacy). The salt does **not** rotate; see Privacy,
- upserts aggregates (batched per run), one row per (site, hour, class).

### 2b. Classification — who was asking
Every bucket carries a class, so "how many people came" has an honest answer instead of one number
dominated by monitoring. `Classify(remoteAddr, ua, uri)` decides in this order:

1. **`infra`** — loopback remote_addr (the local nginx-directory probe, ~every 30s per site), or a
   UA matching a hosted uptime checker (UptimeRobot, Pingdom, StatusCake, …). This is *our own*
   automation, kept separate from third-party crawlers rather than lumped in with them.
2. **`bot`** — empty/`-` UA, a UA that is itself a URL (an exploit-scanner calling card), a UA
   substring match (crawlers, AI scrapers, SEO suites, HTTP libraries, headless browsers, link
   unfurlers, security scanners), or a path nothing legitimate on a static host asks for
   (`/wp-login`, `/.env`, `/xmlrpc.php`, …).
3. **`person`** — the default. Defaulting to person means an unrecognised crawler inflates the human
   count rather than silently deleting real traffic; an inflated bot column would hide people from
   the owner, which is the worse direction to be wrong in.

A fourth class, **`unknown`**, exists only on the read side. It carries the pre-classifier
`site_view_daily` / `site_visitor_daily` history: real traffic whose source logs have scrolled away,
so splitting it now would be invention. It is reported as its own series and folded into nothing.

### 3. Schema (additive)
Current storage is **hourly and class-split**. The two `*_daily` tables below are the original
pre-classifier storage: no longer written, still read for days before the first classified day and
served as the `unknown` class. **Do not drop them** — on a deployment that predates the classifier
they hold the only surviving record of that period.
```sql
-- Current storage -------------------------------------------------------
CREATE TABLE site_view_hourly (          -- one row per site per UTC hour per class
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  hour    TIMESTAMPTZ NOT NULL,          -- UTC, truncated to the hour
  class   TEXT NOT NULL,                 -- 'person' | 'bot' | 'infra'
  views   BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, hour, class)
);
CREATE INDEX site_view_hourly_hour_idx ON site_view_hourly (hour);

CREATE TABLE site_visitor_hourly (       -- one row per distinct visitor per (site, hour, class)
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  hour    TIMESTAMPTZ NOT NULL,
  class   TEXT NOT NULL,
  ip_hash BYTEA NOT NULL,
  PRIMARY KEY (site_id, hour, class, ip_hash)
);
CREATE INDEX site_visitor_hourly_hour_idx ON site_visitor_hourly (hour);

CREATE TABLE analytics_ingest_state (    -- resumable log ingestion
  logfile      TEXT PRIMARY KEY,
  offset_bytes BIGINT NOT NULL DEFAULT 0,
  inode        BIGINT NOT NULL DEFAULT 0,  -- detect rotation (offset reset)
  updated_at   TIMESTAMPTZ DEFAULT now()
);

-- Pre-classifier history: read-only, served as class 'unknown'. DO NOT DROP.
CREATE TABLE site_view_daily (
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  day     DATE NOT NULL,
  views   BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, day)
);
CREATE TABLE site_visitor_daily (
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  day     DATE NOT NULL,
  ip_hash BYTEA NOT NULL,
  PRIMARY KEY (site_id, day, ip_hash)
);
```
`views` increments always; `site_visitor_hourly` is insert-on-conflict-do-nothing. Uniques are
`COUNT(DISTINCT ip_hash)` **over whatever window is asked for** — not `count(*)` of a per-day set,
and not a sum of the per-bucket numbers. Prune both hourly tables > 400 days
(`pruneRetentionDays`) on ingest.

### 4. API — owner-scoped
Two endpoints, both `X-API-Key`, both owner-scoped in SQL. `days` defaults to 30, clamped 1..365.

**`GET /v1/sites/{sitename}/analytics?days=30`** — one site. Resolved via the caller's `user_id`
(`GetSiteByUser`), never by global name, so it is genuinely owner-scoped: the admin key reads the
admin's own sites here, not anyone else's. Returns:
```json
{ "range_days": 30,
  "classified_from": "2026-08-09",
  "totals":   { "person": {"views": 1234, "visitors": 456},
                "bot":    {"views": 3210, "visitors": 890},
                "infra":  {"views": 86400, "visitors": 2},
                "unknown":{"views": 77,   "visitors": 12} },
  "last_24h": { "person": {…}, "bot": {…}, "infra": {…}, "unknown": {…} },
  "daily":  [ { "day":  "2026-08-31",           "person": {…}, "bot": {…}, "infra": {…}, "unknown": {…} }, … ],
  "hourly": [ { "hour": "2026-08-31T14:00:00Z", "person": {…}, "bot": {…}, "infra": {…}, "unknown": {…} }, … ] }
```
There is **no top-level `views` or `visitors`**. Every bucket — `totals`, `last_24h`, each `daily`
entry, each `hourly` entry — is the same four-class split of `{views, visitors}`. `daily` is
zero-filled dense over the window (oldest → newest); `hourly` is always exactly 24 buckets ending
with the hour in progress. `classified_from` is the first UTC day the split exists for; earlier days
report under `unknown`, and the field is omitted when nothing has been classified yet. All four
class keys are always present; `unknown` is non-zero only in `totals` and `daily`, since the legacy
history it carries is daily-only (never in `hourly` or `last_24h`).

**`GET /v1/analytics/sites?days=30[&all=1]`** — every site the caller owns, in one call, ordered by
`person.views` descending. It exists because a dashboard cannot sort site cards by traffic without
every site's numbers before the first card renders, and one-request-per-site cannot provide that.
`all=1` widens the scope to every site on the instance and is **admin-only**: a non-admin gets 403
rather than a silently narrowed list. Returns:
```json
{ "range_days": 30,
  "sites": [ { "name": "eb2-wait", "person": {…}, "bot": {…}, "infra": {…}, "unknown": {…} }, … ] }
```
It is `/v1/analytics/sites`, not `/v1/sites/analytics`, because the latter would collide with a site
actually named "analytics".

Both are in `openapi.yaml` (source of truth) and `openapi.json`, with a capability line in
llms.txt/skills.

### 5. Dashboard
Each site card carries an analytics strip, fetched lazily per card via `IntersectionObserver` so a
long site list does not fire N requests on first paint:

- **People / Bots** cells side by side — views large, unique visitors underneath ("456 visitors ·
  30d"). Reading them against each other is the whole point; a single blended number is what this
  design exists to stop.
- **Infra** and **Earlier** (`unknown`) cells appear only when non-zero, muted, with a tooltip
  saying what they are. Infra is monitoring, not audience; `unknown` predates the classifier and
  cannot be broken down after the fact.
- A **24-hour stacked bar chart** (people over bots, one column per hour, inline HTML/CSS — no chart
  library) from `hourly`. Infra is deliberately excluded from it: a flat ~120/hour probe would
  flatten every real signal.
- When `person`, `bot` and `unknown` views are all zero the strip says "No visits yet" (plus "· only
  monitoring traffic" when infra is non-zero) rather than reporting probe hits as visits.

The site list is ordered by traffic from `GET /v1/analytics/sites?days=30` (admins add `&all=1`),
with sort modes people / bots / all traffic / name. That request is ordering data only: if it fails
the list falls back to API order rather than showing nothing.

## Offsetting / rotation
Persist `(offset_bytes, inode)` per logfile. On each run: stat the file; if the inode changed or
size < stored offset → rotation happened. On an inode change the ingester **drains the rotated
`.log.1` from the stored offset first**, then reads the new inode from 0, so nothing is dropped at
the rotation boundary. A same-inode truncate has no `.1` and goes straight to active@0. A run is
capped at 200k lines; hitting the cap mid-rotated-file persists progress into `.1` so the next run
continues there. logrotate must therefore use `create` + `delaycompress`, **not** `copytruncate`
(same inode, and lines written between copy and truncate are lost) — see
`deploy/prod/logrotate-analytics.conf`.

## Privacy
- Never store raw IPs; only `sha256(visitor_salt + ip)` truncated to 16 bytes, where
  `visitor_salt = hex(sha256(server_secret + "|visitor"))` and the secret (ADMIN_API_KEY) never
  leaves the server.
- **The salt is stable — it does not rotate daily, and that is deliberate.** A per-day salt makes a
  visitor's hash unlinkable across days, which also makes unique counting impossible: the same
  person on Monday and Tuesday looks like two people, so every range total becomes "sum of daily
  uniques" and permanently overstates the audience. A stable salt is what makes "unique visitors
  over the last 30 days" a real number. Do not "restore" daily rotation without also deleting every
  cross-day visitor claim in the API and the dashboard.
- Aggregates only; no per-request retention beyond the hourly distinct-hash set (pruned at 400d,
  `pruneRetentionDays`).
- Bot filtering is **implemented, not deferred**: `user_agent` is logged as the seventh field and
  every row is classified person / bot / infra at ingest (`internal/analytics/classify.go`).
  Nothing is dropped for being automation — bots and probes are counted and reported in their own
  columns, which is what lets `person` be trusted.

## Non-goals (v1)
Referrers, geo, per-path breakdown, real-time, sessions. (Design leaves room: the log has host+uri.)

## Review fixes folded in (adversarial critique)
- **P0 transactional offset:** the ingest-state offset UPDATE happens in the SAME tx as the
  view/visitor upserts → a crash re-processes at most one uncommitted batch, never double-counts
  the non-idempotent `views` counter.
- **P0 rotation:** track `(inode, offset)`; NO copytruncate — logrotate uses `create` (`0644
  www-data root`) + `delaycompress` and a `USR1` reopen; on inode-change or size<offset, drain the
  rotated `.1` file from the old offset then start the new inode at 0.
- **P0 count only 200/304 GET documents; exclude 301/302/307/308** so trailing-slash redirects
  aren't double-counted; drop HEAD/OPTIONS.
- **P0 isolation:** ingest goroutine `recover()`s per run, per-run line cap (200k), 60s context
  timeout on the whole run; never on the serving/request hot path; disabled unless `ANALYTICS_LOG`
  is set.
- **P0 attribution via per-run CACHED maps** (handle→user_id, (user_id,name)→id, name→oldest id,
  custom_domain→id) — no DB query per line. The analytics `access_log` is scoped ONLY to the 3
  serving vhosts (not the dashboard/skills/probes).
- **P1:** UTC everywhere (log `$time_iso8601` UTC, hour bucket, day rollup); analytics log
  `buffer=off` (atomic lines); malformed/torn lines fail-soft (skip, continue); collapse to one
  upsert per (site,hour,class) per run; batched visitor `INSERT … ON CONFLICT DO NOTHING`; bounded
  prune. `ua` is logged and classified, so the metric is split by class rather than labelled "views
  (incl. bots)".
- **P2:** API resolves via the caller's user_id (`GetSiteByUser`), NOT global name; `daily` array
  is zero-filled dense for the range so the trend has a point per day, and `hourly` is always 24
  dense buckets.
- "unique visitors" = **`COUNT(DISTINCT ip_hash)` over the window asked for**, made possible by the
  stable salt. `totals.*.visitors` is therefore a real 30-day unique count, **not** the sum of the
  `daily` figures: someone who visited on five days is one unique in `totals` and five in `daily`,
  and both are correct for what they measure. (This inverts the original v1 note, which said
  uniques were distinct-per-UTC-day under a daily-rotating salt and that the dashboard must not
  imply monthly uniques. That salt design was abandoned precisely because it made a monthly unique
  impossible to compute; monthly uniques are now the number the dashboard shows.) The usual IP
  caveats stand and always will: a shared office NAT reads as one visitor, a phone moving between
  wifi and cellular reads as two.

## Rollout (done)
1. schema migration (additive). 2. nginx log_format + access_log + log dir + logrotate.
3. app ingester goroutine + config (ANALYTICS_LOG path; default off if unset → safe).
4. API + openapi. 5. dashboard strip (class cells + 24h bars). 6. verify with real hits; 7. docs.

v2 added on top of that: the seventh log field (`user_agent`), the classifier, the hourly
class-split tables, `GET /v1/analytics/sites`, and `cmd/analytics-rebuild` for replaying the live
log into the new tables.
