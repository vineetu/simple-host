# Paper metrics: what we measure for "The Thin Server" and how

Status: collection live 2026-10-31 (SIM-335). Code: `internal/papermetrics`. Export:
`GET /v1/admin/paper-metrics` (admin only) or `bash scripts/paper-metrics-export.sh`.

Two papers draw on the same data. HotOS 2027 (5 pages, due 1 Feb 2027) argues the thesis with a
few strong numbers: the per-site marginal cost, one latency table, and the load test. FSE 2027
Industry (10 pages, due 23 Jan 2027) is a deployment report and needs production data over time,
a methodology section and threats to validity. Reviewers of deployment reports (and of the
serverless measurement papers this one will be read against, such as Shahrad et al., "Serverless
in the Wild", ATC 2020) expect distributions rather than means, a stated collection window, how
idle the long tail is, and what the measurement itself cost.

Only public, open-source Simple Host is described (SIM-335 caution). Nothing below stores a
person, an IP (hashed or shortened), a path or a site name.

## Metric list

Status: **have** = collected before today; **built** = collected from 2026-10-31; **hack spec** =
Simple Hack side, spec in `jobs/papermetrics-hack-spec.md`; **load test** = from the planned
synthetic run; **manual** = gathered by hand for the paper.

| Metric | Paper section | Source | Status |
|---|---|---|---|
| Sites, accounts, new per day, accounts with a live site | FSE deployment; HotOS intro | `paper_daily` (`sites_live`, `accounts`, `sites_new`, `accounts_new`, `accounts_with_live_site`); history before 2026-10-31 from `created_at` (deleted sites missing) | built (have for creation dates) |
| Active sites: viewed by people in 1, 7, 30 days; updated in 30 days | FSE deployment; HotOS idle tail | `paper_daily` (`sites_viewed_*`, `sites_updated_*`) from `site_view_hourly` | built |
| Requests per site per day (the idle-tail distribution) | HotOS cost; FSE workload | `site_traffic_daily` (requests, bytes, per site and kind) | have |
| Page views by people vs bots | FSE workload | `site_view_hourly`, `paper_daily` (`page_views_person_day`, `page_views_bot_day`) | have |
| Requests and server time per route class, p50/p90/p99/p99.9 | HotOS table; FSE performance | `paper_latency_daily` + `paper_class_daily` (set `latency`) | built |
| Bytes served, pages vs API, and box network in/out | FSE workload; HotOS cost | `traffic_daily`, `net_usage_daily`, `paper_class_daily.bytes_out` | have |
| API calls per route and status (storage read/write mix, deploys, MCP) | FSE workload; HotOS "server only stores" | `api_request_daily` (30-day retention), long-lived daily totals in `paper_daily` (`api_data_reads_day`, `api_data_writes_day`, `api_deploys_day`, `api_mcp_calls_day`) | have (30 d) + built (kept) |
| API calls by kind and caller country | FSE deployment | `api_growth_daily` | have |
| Per-site disk: live, all versions, by kind (HTML, JS, CSS, media), SQLite, file store, KV | HotOS marginal cost; FSE storage | `paper_site_footprint`, weekly, anonymous (sets `footprint`, `footprint_summary`) | built |
| Idle-site marginal cost: disk per site with no views in 30 days; memory and CPU per idle site | HotOS core claim | footprint (`views_30d = 0`), and the load test for memory and CPU | built (disk) + load test |
| Server process CPU and RSS over time vs number of sites | HotOS; FSE resources | `paper_process_sample` (5 min) joined to `paper_daily.sites_live` | built |
| Postgres and nginx RSS, box CPU, memory, load | FSE resources | `paper_process_sample` | built |
| Database size, KV rows and bytes, legacy rows | FSE storage | `paper_daily` (`db_bytes`, `kv_rows`, `kv_value_bytes`, `legacy_rows`) | built |
| Feature use: saved data (KV, SQLite, files), visitor sign-in, named viewers, passcodes, custom domains, notifications | FSE adoption; HotOS "rules, not code" | `paper_daily` (`sites_with_*`, `sites_access_specific`, `sites_custom_domain`) | built |
| Access rules in use: resources readable or writable by anyone, add-only with spam check, signed-in, own-reads | HotOS security; FSE security | `paper_daily` (`resources_*`) | built |
| Certificates: custom-domain certs live, per-person wildcards, families | FSE operations | `paper_daily` (`sites_custom_domain_cert_live`, `certs_person_wildcard`, `certs_family`) | built |
| Agent use: connector accounts and grants active in 30 days, MCP calls per day | FSE "agents as the developer" | `paper_daily` (`connector_*`, `api_mcp_calls_day`); AgentCat for per-tool counts | built + have (AgentCat) |
| MCP calls per site | FSE agents | not attributable server-side without a new per-site counter; use AgentCat per-tool counts | gap, deferred |
| Attack surface: routes, routes needing auth, auth paths | HotOS security; FSE security | `openapi.yaml` (count paths and `security`) at paper time; `GET /v1/admin/paper-metrics?set=api_routes` for routes actually called | have (from repo) |
| Refused requests: 401/403, 429, 5xx | FSE security and reliability | `paper_daily` (`api_denied_401_403_day`, `api_refused_429_day`, `api_errors_5xx_day`) | built |
| Suspended sites and accounts, abuse reports, takedowns | FSE security | `paper_daily` (`sites_suspended`, `accounts_suspended`); reports from the support mailbox | built + manual |
| Incidents | FSE lessons | owner's notes, CHANGELOG, memory notes | manual |
| Restarts (deploys) and downtime | FSE operations | `paper_process_sample.started_at` (each start is a restart), `paper_daily` (`process_restarts_day`, `sample_minutes_missing_day`); external view from `sh-uptime-watch` (alerts only) | built |
| Deploys of the platform | FSE operations | `git log` on main and `/usr/local/bin/simple-host.bak-*` timestamps | have (repo) |
| Backups: size and time | FSE operations | `agentbox-backup` journal (`journalctl -u agentbox-backup`) | have (journal) |
| Cost per month | HotOS; FSE | OCI bill (box, block volume), domain and certificate costs | manual |
| Client-side work: page weight, JS bytes per real site | HotOS "clients compute" | footprint (`html_bytes`, `js_bytes`, `live_bytes`) | built |
| Client-side work: CPU time and time to interactive on a low-end Android | HotOS | Lighthouse with mobile CPU throttling on a sample of real sites, and one real low-end phone | manual |
| Simple Hack: events, teams, team sites, judges, submissions, peak concurrency, storage per event | FSE second deployment | spec in `jobs/papermetrics-hack-spec.md`; the Hack instance already writes every table above | hack spec |
| 1M sites on 1 CPU / 1 GB: requests per second, idle-site marginal memory, CPU and disk | HotOS headline | load test plan below | load test |

## Methodology per metric

**Route classes and server time.** A middleware outside the host router times each request from
the handler's first line to its return, so it is server time (including writing the body to
nginx over loopback), not network time and not TLS. Class by path, then host: `/mcp` POST is
`mcp`, `/mcp` GET is `mcp_stream` (a held-open stream; its time is a duration, not latency, and
must be left out of latency tables); OAuth, sign-in, SSO and a site's `/me` are `auth`; a
site's `/storage/`, `/collections`, `/state` and `/data` are `data_read` (GET/HEAD) or
`data_write`; other `/v1` is `api_read` or `api_write` (deploys are `api_write`); a request on
the platform's own host is `platform_page`; anything else is `site_file` (every status,
including 304 and 404). Health probes and CORS preflights are not timed. Histogram upper bounds
are 50 µs to 10 s, log-spaced (16 buckets plus overflow). Percentiles interpolate linearly
inside a bucket, so they are accurate to the bucket width; the overflow bucket reports its 10 s
floor. nginx serves TLS and proxies everything to the Go server, so `site_file` here is the
Go server's cost of a file, which is the thin-server claim.

**Process samples.** Every 5 minutes: this process's CPU (user + system from `getrusage`,
divided by wall time since the previous sample, so 1.0 is one whole core), RSS (`VmRSS`), Go
heap, goroutines, open file descriptors; summed RSS of every `postgres` and every `nginx`
process (Postgres workers share their buffer pool, so this overstates Postgres; report it as an
upper bound); box CPU from `/proc/stat` in cores, `MemTotal`, `MemAvailable`, 1-minute load.
The first sample after a start only primes the CPU baseline. Two services share the box
(simple-host and simple-hack, plus unrelated sites): the per-process numbers are clean, the box
numbers are an upper bound for the platform.

**Restarts and downtime.** Every sample carries the process start time, so distinct start
times are restarts (each deploy is one). A day's samples should be 288; missing five-minute
slots (`sample_minutes_missing_day`) bound downtime from the inside. `sh-uptime-watch` checks
`/healthz` from the box every few minutes and only keeps alert state, so its alerts (Signal) are
the external record.

**Daily snapshot.** Once per UTC day, for the day before: about 60 counts in `paper_daily`.
Counts with a day in the query (new accounts, views, API calls) are exact for that day; counts
of current state (sites with a passcode, storage resources) are as of the moment the snapshot
ran, usually just after midnight UTC, so they describe the end of that day within an hour. A
query that fails on an instance without the table skips only that metric.

**Weekly footprint.** Once per week (week starts Monday, UTC), one row per live site, walking
the site's directory: `current/` is the live copy (split by extension), `vN/` folders are kept
versions, `runtime/sqlite` and `runtime/files` are saved data on disk; KV is the summed
`pg_column_size` of its values; views are person-class views in the last 30 days. The row has no
site id, owner or name and is returned in random order, so it cannot be joined back to a site.
`total_bytes` includes every version and runtime data.

**Retention and privacy.** These tables hold no personal data and are kept with no end date. The
Privacy page says so (2026-10-31). The sources they are computed from keep their own retention
(API per-route counts 30 days, analytics 400 days).

**Measurement cost.** `BenchmarkWrap` on this box (aarch64, 4 cores): about 0.4 µs and one
32-byte allocation per request, against a request that costs tens of microseconds or more. The
database sees one small upsert a minute, one insert every 5 minutes, ~60 queries a night, and one
directory walk a week (the same walk the admin usage page already does). Re-measure with
`go test ./internal/papermetrics -bench . -benchtime 500000x`.

**Threats to validity, to state in the paper.** One box and one operator; a young deployment
(first site July 2026); most sites are small and many are demos; bots are separated by
User-Agent class, which undercounts disguised bots; server time excludes TLS and the network;
the box hosts unrelated services; per-route counts before 2026-10-01 are gone (30-day
retention) and before this date there is no latency or process history.

## Load test plan (not run yet)

Goal: the HotOS headline. One 1-CPU / 1 GB VM serves one million sites; report requests per
second for the realistic mix, latency percentiles per class, and the marginal cost of an idle
site (memory, CPU, disk, database).

1. **Box.** A fresh 1 OCPU / 1 GB VM (OCI Ampere A1, the same architecture as production) with
   the release binary, Postgres 16 and nginx from `deploy/install/install.sh`. A separate load
   generator VM in the same region so the client does not share the CPU. Tear both down after.
2. **Synthetic sites.** A generator script (to be written, `scripts/loadtest/`) creates accounts
   and sites directly in Postgres and on disk, drawn from the measured footprint distribution
   (`set=footprint`): sizes, file counts, versions kept, and the share with KV, SQLite or file
   storage and its size. Steps of 1k, 10k, 100k, 300k and 1M sites. Files are hard-linked from a
   pool of real-shaped templates so disk reflects metadata cost rather than duplicated bytes;
   report both with and without hard links.
3. **Idle cost.** At each step, with no traffic, record after 10 minutes: Go RSS and heap,
   Postgres size and RSS, inodes and disk used, CPU over 10 minutes (background loops, sweeps),
   and boot time. Marginal idle cost = slope between steps. The claim holds if memory and idle
   CPU are flat in the number of sites and disk and database grow linearly with small slopes.
4. **Request mix.** Replay the production mix from `set=latency` and `set=api_routes`: per
   class shares of `site_file`, `data_read`, `data_write`, `auth`, `mcp`; site popularity from
   `site_traffic_daily` (Zipf fitted to real per-site requests). Tool: `k6` or `vegeta` with an
   open-loop arrival rate, through nginx with TLS as in production, plus a second run straight to
   the Go port to separate TLS cost.
5. **Measure.** Sustained requests per second at p99 under 100 ms for files and 250 ms for
   writes; CPU and RSS from `paper_process_sample` on the test box (the same collector runs
   there), latency from the load generator and from `paper_latency_daily`. Also a cold cache
   run (drop caches) at 1M sites to show the disk side.
6. **Report.** One figure: memory and idle CPU against sites (log x). One table: req/s and
   p50/p99 per class at 1M sites. One line: dollars per idle site per month from the VM price.

Do not run against production. Estimated cost: two small VMs for a day.
