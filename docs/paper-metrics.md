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
| Box CPU, memory, load | FSE resources | `paper_process_sample` | built |
| Postgres and nginx memory | FSE resources | `paper_process_sample` columns read 0 under the production sandbox (`ProtectProc=invisible`); `systemctl show postgresql nginx -p MemoryCurrent`, or the load-test box | manual |
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
| Simple Hack: events, teams, team sites, judges, submissions, minute/second activity, storage per event | FSE second deployment | `paper_hack_event`, `paper_hack_minute`, Hack-only `paper_daily` counts; methodology below | built (pending deployment) |
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
floor. nginx serves TLS and proxies platform/team traffic, but custom-domain `try_files` can serve files directly. `site_file` measures only requests that reach Go, not all hosted-file traffic.

**Process samples.** Every 5 minutes: this process's CPU (user + system from `getrusage`,
divided by wall time since the previous sample, so 1.0 is one whole core), RSS (`VmRSS`), Go
heap, goroutines, open file descriptors; summed RSS of every `postgres` and every `nginx`
process (Postgres workers share their buffer pool, so this overstates Postgres; report it as an
upper bound; on simple-host.app the service runs with `ProtectProc=invisible`, so these two read 0 there and the paper takes them from `systemctl show postgresql nginx -p MemoryCurrent` by hand or from the load-test box); box CPU from `/proc/stat` in cores, `MemTotal`, `MemAvailable`, 1-minute load.
The first sample after a start only primes the CPU baseline. Two services share the box
(simple-host and simple-hack, plus unrelated sites): the per-process numbers are clean, the box
numbers describe a different, shared-host quantity, not an attributed platform bound.

**Restarts and downtime.** Every sample carries the process start time, so distinct start
times are restarts (each deploy is one). A day's samples should be 288; missing five-minute
slots (`sample_minutes_missing_day`) bound downtime from the inside. `sh-uptime-watch` checks
`/healthz` from the box every few minutes and only keeps alert state, so its alerts (Signal) are
the external record.

**Daily snapshot.** Once per UTC day, for the day before: 58 counts in `paper_daily`.
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

**Retention and privacy.** The Host tables hold no personal data and are kept with no end date; Hack activity slots have a 400-day retention and event snapshots retain daily history. The
Privacy page says so (2026-10-31). The sources they are computed from keep their own retention
(API per-route counts 30 days, analytics 400 days).

**Measurement cost.** `BenchmarkWrap` on this box (aarch64, 4 cores): about 0.4 µs and one
32-byte allocation per request, against a request that costs tens of microseconds or more. The
database sees one small upsert a minute, one insert every 5 minutes, 58 queries a night, and one
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

## Simple Hack

Definition version **4**, implemented and pending deployment (2026-10-31). Version
1 was the unshipped five-minute/ordinal/p99 design; version 2 was the unshipped
minute/histogram design. Version 3 fixes restart baselines, panic cleanup, idle-slot
omission, derived-count suppression, relative phase export and disabled collection.
Version 4 adds daily dependency suppression, relative-only event exports with
decimal-string keys, sparse pooled histograms, four counter shards, and
origin-change phase deduplication. No earlier Hack rows have been deployed or relabelled. The existing Host tables, legacy buckets and sampling remain
unchanged (collection definition 1). The new shared `LogHistogram` and `paper_run`
are reusable; a Host histogram switch is reserved for workspace-3.

### Collection and provenance

Every process with collection enabled registers one `paper_run`.
`PAPER_METRICS=off` writes nothing, including no provenance row. Enabled runs record:
UTC start, Host/Hack role, embedded Git revision and dirty flag, Go version, SHA-256
of the running binary, definition version, bucket scheme, GOMAXPROCS/CPU count,
and cgroup memory.max/cpu.max when readable. Missing revision, executable digest
or cgroup limits are NULL rather than invented. Cgroup values come from the
process's `/proc/self/cgroup` location under `/sys/fs/cgroup`, with namespace-root
fallback. This does not yet collect per-unit resource samples.

The config hash uses a fixed whitelist of parsed non-secret settings: limits,
EVENTS, WRITE_AUTH_MODE, PERSON_HOSTS, SITE_HOSTS and PAPER_METRICS. It never
serialises the whole config, credentials, a DSN, arbitrary environment variables,
or names/addresses. Only the digest is stored. Hack minute, histogram, phase,
snapshot and daily-companion rows carry `run_id`; the legacy Host tables gain no
columns. Hack daily values still appear in `paper_daily`; `paper_hack_daily`
provides their provenance without changing that shared table.

`paper_hack_key` is the sole source-ID exception: an internal event-ID-to-random-key
lookup with first captured committed open time, synthetic flag and creation run. The source
ID is never exported; this lookup is deleted with the event. Anonymous histories
retain the random key and do not renumber when another event is deleted.

`paper_hack_event` keeps one snapshot per UTC day and random event key, including
history. Each scalar query fails independently (its column becomes NULL); writes
are atomic. The snapshot is current state when taken, not reconstructed history.
Keys, publication status and first-deploy time use existing database records;
disk sizes and version counts share exactly the Host footprint walk (regular
files only, no symlink following, live sites only).

| Event metric | Methodology |
|---|---|
| `event_key` | Cryptographically random signed 64-bit key exported as a decimal string, stable for the lifetime of the event; the source-ID lookup is private and never exported. |
| `run_id` | Process-start provenance row in `paper_run`. |
| `synthetic` | True for e2e-/mock-/mock2-/local- event addresses or a creating account whose username starts e2e-; classification stays true once detected. |
| `stage` | Current lifecycle enum from `events.stage`. |
| `duration_hours` | `(ends_at - starts_at)` in hours; NULL when either is unset. |
| `expected_participants` | Event's expected count; NULL when unset. |
| `participants` | Participant membership rows whose approval status is approved; joins requiring no approval already receive that status. |
| `organisers` | Membership rows with role organiser. |
| `judges` | Membership rows with role judge; departed judges are absent, while their retained scores still count below. |
| `applications` | All current participant membership rows, including approved, pending and rejected statuses; withdrawn rows are absent. |
| `applications_approved` | Current participant membership rows with approved status. |
| `teams` | Current `event_teams` row count. |
| `teams_with_entry` | Teams with an `event_entries` row, including a partially filled entry. |
| `teams_kept_after_leave` | Teams with `event_teams.kept_after_leave = true`, including empty preserved teams. |
| `team_size_p50` | Discrete median of membership counts per team, including empty teams as zero. |
| `team_size_max` | Largest membership count per team; zero with no teams. |
| `team_sites` | Live sites belonging to the event's holding account, excluding its event-ID-named website. |
| `team_site_bytes_sum` | Sum of team-site `total_bytes` using the shared weekly footprint disk walk; KV is separate. |
| `team_site_bytes_p50` | Discrete median of those byte sizes; zero with no team sites. |
| `team_site_versions_sum` | Number of retained team-site `vN` directories, using exactly the footprint naming rules. |
| `event_storage_bytes` | KV `pg_column_size(value)` plus `runtime/sqlite` and `runtime/files` bytes across all live sites of the holding account, including the event website. |
| `submissions` | Current `event_entries` row count, including partial entries. |
| `entries_with_code_url` | Entry rows with a nonempty code URL; URLs are never copied. |
| `entries_with_video` | Entry rows with a nonempty video URL; URLs are never copied. |
| `scores` | Current `event_scores` row count, including scores retained after a judge leaves. |
| `score_criteria` | Current `rubric_criteria` row count. |
| `assignments` | Current `event_assignments` row count. |
| `conflicts` | Current `event_conflicts` row count. |
| `votes` | Current `event_votes` row count; voter email is never selected. |
| `announcements` | Current `event_announcements` row count; titles and bodies are never selected. |
| `results_published` | Whether the event timestamp or the existing `event_results` publication row exists. |
| `deploys_total` | All retained `vN` directories across live event-owned sites, including the event website; pruning removes directories, so this is retained versions rather than an all-time deployment counter. |
| `minutes_create_to_first_deploy` | Earliest retained completed team-site `versions.created_at` (`active`/`ready`) minus `events.starts_at`, rounded to the nearest ten minutes; NULL if either is unavailable. No historical opened-at field exists, so scheduled start is a proxy for opening; pruning version records can remove the original first deployment. |










### Minute activity and mergeable latency

An event is active in open, building, closed or judging, and during results from
publication until strictly less than 24 hours later. Publication uses
`events.results_published_at`, falling back to the existing `event_results`
publication timestamp used by the app. Active events get 60-second UTC slots,
only when they contain an attributed request, write or deploy; idle minutes and
inactive events get no rows. Synthetic events are also
collected and marked so analyses can explicitly exclude them.

| Activity metric | Methodology |
|---|---|
| `minute_offset` | Exported rounded minutes from events.starts_at to the internally stored UTC start slot. NULL when starts_at or its private lookup is unavailable; absolute slot timestamps are never exported. |
| `slot_secs` | Always 60; no inactive/hourly rows are currently kept. |
| `requests` | Resolved event API/page or team-site requests, including failures; health, OPTIONS and held-open MCP streams excluded. |
| `writes` | Accepted mutating-method event API requests (including judging/scoring), or committed storage mutations, once per outer request. MCP uses its replay status, not transport 200. |
| `data_writes`, `deploys` | Committed KV/SQLite/file mutation requests (a subset of writes) and newly committed/promoted team-site versions; queries, download links, website deploys and rollbacks excluded. |
| `active_members` | Distinct authenticated event members in an in-memory set for the start slot; only the count is stored. This is activity, never concurrency. |
| `inflight_max` | Sampled maximum of the event's atomic requests-in-flight gauge, updated on entry/attribution. Cached event URLs and team hosts attach before routing/auth. Other site forms attach when already resolved, so their pre-resolution time is outside this gauge. Never sum maxima to estimate simultaneous concurrency across events. |
| `arrivals_1s`, `writes_1s` | Sixty exact one-second bins for starts and accepted write starts. A write completing later is assigned back to its start second. Merge adjacent minutes for 10/60/300-second burst measures. |
| `peak_rps_1s` | Maximum of the sixty arrival bins, not minute requests divided by 60. |

A pending request pins its start slot until completion. Flushes detach only
completed, quiescent slots, so long requests do not move to a completion slot or
split a histogram. Metadata is immutable on the read path and counters are
atomic; cached event/site/member associations refresh once a minute and may be stale until a successful refresh. Verified request paths reuse their membership lookup. A lock is used only to admit/rotate event slots. First admission (including
idle events) wins the 200-event cap per minute. Late attribution to an already
flushed slot, a process crash, cap exclusions and failed metric writes can lose
samples; they never retry or fail a business request. The unfinished slot is not
saved on shutdown. Minute and histogram rows expire after 400 days.

`paper_hack_latency` / `hack_latency` stores one nonempty histogram per
(run, start minute, event key, route class, status group 2xx/3xx/4xx/5xx), separate
from activity totals to prevent accidental duplication of people/arrivals.
MCP histogram status groups describe HTTP transport status, which may be 200 even for a tool error; mutation counts use actual replay outcomes and commit-path markers.
It stores exact `n`, `sum_us`, `max_us`, `bytes_out`, `idx_offset` and `counts INT8[]`, plus per-class/status `arrivals_1s` and `writes_1s`;
**no percentile is stored**. Bucket scheme `exp2_scale3_us_v1` has base 2^(1/8),
index ceil(log2(us)*8)-1, with <=1 microsecond in bucket -1. Durations are first truncated to integer microseconds; the error guarantee applies to those integer samples. The trimmed contiguous
span retains interior zero buckets. The shared type supports all positive int64
microseconds, including requests above 134 seconds. Merge offset-aligned counts,
add n/sum, take max(max), then compute quantiles from the merged distribution.
The harmonic bucket midpoint has at most 4.33% relative error above 1 microsecond;
the zero bucket has at most 1 microsecond absolute error. Report p99 only with
n >= 1,000 and p99.9 only with n >= 10,000 per merged cell; otherwise report n/max.
The histogram is server time, excluding nginx queueing, TLS and client/network time.

Absent event-minute rows mean zero requests/writes/deploys during healthy
collection in that event's active window; collection failures can also lose rows
and must be checked against run history/logs. Idle admitted slots are discarded
at drain; periodic refresh no longer admits empty slots. Panicked handlers are recorded in their start minute as 5xx for metrics,
and deferred cleanup releases both in-flight and pending-slot references while
preserving the panic. Distinct members use an exact, sharded immutable-map set:
existing-member reads take no lock; first-seen insertions copy only that shard.
Sets are cleared after their start slot quiesces. Four Hack counter shards hold
pending references and lazy class/status cells. Each arrival/write increments one
class/status second bin; drain sums bins into event-wide arrays and totals,
removing duplicate event/cell atomic adds. Histograms allocate 32-bucket chunks
only for observed ranges. Cells and cleared chunks return to pools after merging
or database flush (including failed flushes); storage is reused across minutes.
INT4 second bins retain the database's existing signed-32-bit count range.
Quiescent shards merge at drain/flush. No
change is made to Host's collection or buckets.

### Phases

`paper_hack_phase` / `hack_phases` captures committed stage changes in the stage
API and leave/end path. Request hooks enqueue a captured time and a non-blocking
background refresh nudge; they perform no SQL and never wait on the refresh lock.
The queue holds 256 transitions. If full, the periodic refresh observes the latest
stage, but intermediate transitions can be lost. Queued notices can be lost on process shutdown; the next refresh compares the
latest stored stage with the current database stage.

Refresh seeds each event from its newest persisted lifecycle stage (ordered by
internal recorded_at, excluding deadline/publication/voting markers). A fresh
collector does not interpret an empty cache as a change. An event with no history
gets exactly one `baseline=true, observed=false` row: its first observed state,
**not a stage transition or its historical transition time**. Later refreshes,
including restarts hours apart, record only a different persisted stage.
`observed=true` means a genuine change discovered by refresh, whose capture time
may lag by a minute; false with baseline=false means a committed-path transition
or a stored timestamp marker. Draft/open/building/closed/judging/results/archived
are fixed lifecycle enums. Commit-path captures take priority over refresh rows
for the same source change. Internal `origin_change` identifies the source stage
update (`events.updated_at` passed by the committed path); key locking and a unique
(event_key, stage, origin_change) prevent a refresh/hook pair from creating two
rows across a minute boundary. An exact hook upgrades a refresh observation,
while a later real return to the same stage has its own origin. Timestamp markers
use their stored source timestamp as origin_change. None of these absolute
identity/order timestamps is exported.

The timeline also records submission-deadline, judging-lock, results-publication
and voting-open/close timestamps. Changed planned timestamps remain distinct
markers, not a complete scheduling revision history. Exports contain only
`minute_offset`, rounded minutes since starts_at, or since the first captured
committed open when starts_at is unset. Absolute phase/origin/recording timestamps
are stored internally for ordering and date filtering and are **never exported**.
An unknown historical origin gives a NULL offset; refresh seeing an already-open
event never invents its opening time. Baseline offsets locate the baseline
observation only and must not shade a historical phase boundary.

### Daily counts

The 18 Hack counts from definition 1 are retained: events total/by each stage,
events created during the UTC day, approved participants, judges, teams, entries,
scores, votes, applications joined through the application flow during the day,
live team sites and published event websites. They run only on the Hack collector;
a failing query skips that metric. The histogram design adds `paper_tables_bytes`, summed
`pg_total_relation_size` of all paper tables in the current schema, including their
indexes/TOAST. The data dictionary gives each metric's unit and exact query.

### Release rules and dictionary

All exports remain admin-only. On the Hack collector, every people count **below 5, including zero**, is
NULL with a companion `<column>_suppressed=true` flag (daily uses
`value_suppressed`). This covers expected/actual participants, organisers, judges,
applications/approved applications, active members, votes and team sizes, plus
account-count metrics. When judges are below five or missing, assignments,
scores and conflicts also export NULL with their own suppression flags. If either
application total is below five, missing, or applications minus approved is below
five, both totals are suppressed to prevent subtraction revealing a small count.
The daily set applies the same rules per day: hidden/unknown hack_judges_total
also hides scores, assignments and conflicts. Application total/approved pairs
are hidden when either is small/unknown or their difference is below five; an
unpaired application metric is unknown as a pair and is hidden. Dates remain in
platform-wide daily rows, which carry no event_key.

A missing source value itself stays NULL with flag false; dependency suppression
can hide a known paired value. Host exports retain their original columns,
metadata and values without these Hack rules. JSON uses
null; CSV uses an empty field. The rule also applies with `include_small=1`.

All per-event exports (snapshots, minutes, histograms and phases) omit events with
fewer than five teams by default. Snapshots use that row's teams; other series use
the latest retained snapshot. The explicit admin override `include_small=1` bypasses
only event-size filtering, never people suppression. It is for operator inspection,
not publication permission. Owner approval and organiser consent still belong in
the paper's release record; the admin export does not obtain or grant consent.

No row carrying `event_key` contains any absolute timestamp or date. Event
snapshots omit snapshot_day and created_week; minute/latency series expose
minute_offset from starts_at; phase rows expose only their relative offset.
Storage retains its timestamps/dates, and from/to still filter them internally.
All exported event keys are decimal strings in both JSON and CSV, preserving all
64 bits in JavaScript without a numeric round trip. Minute/latency offsets are
NULL after source-event deletion or with an unset starts_at. Phase offsets keep
the already-recorded historical origin/fallback. These distinct cases must not
be silently aligned to the Unix epoch.

Hack JSON tables and summary identify `metric_def_version`; new row exports also
carry it as a CSV column via `paper_run`. Legacy histogram tables on the Hack export are
labelled definition 1 / `legacy_16_upper_us_v1` and must not be merged with Hack's
scale-3 arrays. `runs` includes all producers referenced by data in the export window even if they started earlier; it never exports config contents or the key lookup. Phase date filters use internal capture timestamps, including with include_small=1; the response contains only relative offsets.
The complete [data dictionary](paper-metrics-dictionary.md) lists every exported
column, units, definitions and versions, including the unchanged Host sets.

`SH_BASE=https://simple-hack.app bash scripts/paper-metrics-export.sh` exports
`hack_events`, `hack_minutes`, `hack_latency`, `hack_phases` and `runs` as well as
the existing sets. Keep the Hack admin key in SH_KEY, outside command history.

### Measurement cost and remaining figure requirements

Measure steady serial and parallel paths with
`go test ./internal/papermetrics -run '^$' -bench BenchmarkHackWrap -benchtime 500000x -count=3`.
Parallel workers target the same event/member, stressing shared gauges, histogram
cells and member-set lookups. Parallel ns/op is aggregate wall-time throughput,
not a per-thread response-time distribution. These microbenchmarks do not replace
the review's on/off open-loop A/B load test.

Final definition-4 measurements (2026-10-31 UTC): linux/arm64, Go 1.27.3,
GOMAXPROCS=4. Other verification jobs from this task finished before timing;
the shared host's other work was uncontrolled. Cache and temporary paths were
under build/. Exact commands:

```sh
GOMAXPROCS=4 go test ./internal/papermetrics -run '^$' -bench BenchmarkHackWrap -benchtime 500000x -count=3
GOMAXPROCS=4 go test ./internal/papermetrics -run '^$' -bench BenchmarkHackWrap -benchtime 500000x -count=8
```

Each cell is median [min–max] ns/op. Parallel values measure aggregate throughput,
not per-thread latency. All paths measured 344 B/op and two allocations (request
copy plus its lifetime-safe context); no pooling of escaping request contexts.

| Path | count=3 | count=8 |
| --- | ---: | ---: |
| Serial event | 818.90 [657.1–1137.0] | 697.50 [572.6–1009.0] |
| Serial team_site | 1388.00 [1139.0–1481.0] | 2086.50 [734.2–2541.0] |
| Serial team_write | 724.10 [666.6–792.8] | 1402.50 [918.5–2246.0] |
| Parallel event | 757.40 [659.8–912.9] | 1110.30 [759.6–1711.0] |
| Parallel team_site | 861.50 [713.4–959.9] | 683.00 [466.4–796.7] |
| Parallel team_write | 746.60 [702.6–1069.0] | 523.45 [462.9–629.3] |

**The strict 1 µs gate remains unmet:** 23 of 66 repeat means exceed 1,000 ns/op;
the maximum is 2,541 ns/op. This report does not select only favorable trials.
Shared in-flight atomics, request/context allocations, classification and timers
remain in the wrapper; measurements also include allocator/GC and host scheduling
costs. Load averages are context, not proof of which cause explains an excursion.
No end-to-end on/off A/B or worst-case per-request guarantee is established.

Recorded uptime samples (1/5/15-minute load averages):

```text
count=3 before  22:15:04 up 62 days, 15:19,  7 users,  load average: 8.91, 9.12, 7.62
count=3 after  22:15:14 up 62 days, 15:19,  7 users,  load average: 7.76, 8.87, 7.55
count=8 before  22:15:14 up 62 days, 15:19,  7 users,  load average: 7.76, 8.87, 7.55
count=8 after  22:15:41 up 62 days, 15:20,  7 users,  load average: 8.47, 8.94, 7.61
```

All individual measurements, retained here because build/ is ignored:

```csv
path,repeats,ns_per_op_samples
serial_event,3,"1137.0, 818.9, 657.1"
serial_event,8,"801.7, 684.4, 710.6, 594.0, 854.4, 669.3, 572.6, 1009.0"
serial_team_site,3,"1388.0, 1481.0, 1139.0"
serial_team_site,8,"734.2, 1170.0, 2115.0, 2058.0, 2177.0, 2541.0, 2121.0, 1605.0"
serial_team_write,3,"724.1, 666.6, 792.8"
serial_team_write,8,"2246.0, 2188.0, 2125.0, 1391.0, 918.5, 1196.0, 946.8, 1414.0"
parallel_event,3,"912.9, 659.8, 757.4"
parallel_event,8,"1640.0, 1468.0, 1711.0, 1231.0, 759.6, 794.9, 989.6, 824.0"
parallel_team_site,3,"861.5, 713.4, 959.9"
parallel_team_site,8,"667.7, 579.2, 717.8, 698.3, 755.9, 796.7, 636.3, 466.4"
parallel_team_write,3,"746.6, 1069.0, 702.6"
parallel_team_write,8,"629.3, 582.5, 518.8, 468.5, 462.9, 528.1, 468.7, 536.1"
```

The supplied R2 baseline/component benchmarks also ran on this final code with
the provided refute2 overlay and `-run '^$' -bench BenchmarkR2 -benchtime 500000x -count=1`.
These single-trial component figures are diagnostic, not subtractions from the
wrapper measurements: bare event/team-file/team-write 8.516/11.71/16.25 ns/op;
Host wrapper 262.0/310.8/277.4 ns/op; attach+finish 114.4 ns/op serial and 163.5
ns/op parallel. The context component permits compiler elimination of the unused
request copy, so its allocation result is not the real wrapper's cost.

Memory: the supplied `TestR2SlotMemory` ran unchanged, with its 16 logical shard
inputs mapped to four physical shards, four classes, two status groups, and a
constant 1 ms sample per cell. Retained heap after GC measured **32 KiB/event at
20 events** and **34 KiB/event at 200 events (6.5 MiB total)**, below the 64 KiB
counter-buffer target and the review's roughly 695 KiB/event baseline. These
figures exclude baseline collector memory, in-flight request/context allocations
and member-set growth (the repro has no member IDs). Wider observed latency
ranges or more active class/status pairs need more chunks. The single-event
GC delta was negative, an artifact of process-wide heap baselines rather than
negative storage; use the larger seeded cohorts. Cell/chunk reuse across minutes
is separately checked by `TestHackCellReuseIsEmpty`; race tests check conservation
and exact members across concurrent drains. This is retained heap, not peak RSS
or an app-wide bound under MemoryMax=768M.

Commit-independent SHA-256 hashes of the final metric sources follow; all entries
in the larger local build/round4-final-source.sha256 manifest verified before and
after both wrapper commands and the R2 component benchmarks. Manifest SHA-256:
`43098d5141ad38e517d5384b1eb4afb60d1809814937357e63963803d89ecd7d`.

```text
066c3af576f2d1ee4a40d9241ba15831be090d140009692c2eaeaf5a93e0149e  internal/papermetrics/hack_counter.go
8d492ca5b2de8c2305f6595b2796652039c2a524be7f7f1a5c1e86fa87ff423f  internal/papermetrics/histogram.go
7d355088fe93145b2766c1c76036e8e5f2c04fb58652ed0e895a07baeb670cf7  internal/papermetrics/hack_members.go
38aeed46522f32bfa954044c6a3bc7fb4803322817f03d666cb910c85ed4103f  internal/papermetrics/papermetrics.go
7e0b5074546cd8af7179daf3e721d055eff0d9d4c622dda30a6069dae086d93f  internal/papermetrics/classify.go
1b282ed68a7de6d18512763c05b38a3c264dc38c93f2ccbf01a35c27b75ced08  internal/papermetrics/hack_metadata.go
3b1eeee3fe5a5895c05aa9e09f3c2e78145dafcdf3fb9bb3c9c91d085b1d6e0e  internal/papermetrics/hack_export.go
04b49eba092dedd5c221f75bea9c66fae0bf9c85be1a3db3e83a4b5b6d7a4925  internal/papermetrics/export.go
8f3d1f8fed83c81461d1641fba571c71319427aba34dbbe75b0104956f63a31f  internal/papermetrics/store.go
cda9c654b923a440462f0387d22ce7d60d9189368c36b12f16d3a378fac5119c  internal/papermetrics/hack_regression_test.go
```

All round-3 timings are superseded.

Storage was measured with the reviewer's `TestRefuteStorageSize` reproduction:
1,440 populated minute summaries and four class/status cells per minute, each
with a 64-bin span and two 60-element arrival/write arrays. PostgreSQL
`pg_total_relation_size` (including indexes/TOAST after VACUUM ANALYZE) is about
**9.01 MiB per 24-hour active event-day** (9,445,376 bytes in this run), rather than the earlier 7.9 MiB arithmetic
estimate. At 200 events with that traffic all day this is about **1.76 GiB/day**,
or about 704 GiB over 400 days, before WAL/backups/replicas. Snapshot/phase/run rows add
small costs. Empty event-minutes are now omitted, so storage drops with the
fraction of minutes that actually have traffic; an idle active event generates
no minute or histogram rows. This is a seeded workload measurement, not a
production growth rate. Wider histograms or more class/status cells cost more;
observed `paper_tables_bytes` deltas should replace this estimate for deployment.

These additions support minute/second burst and event-latency analyses. The
review's per-unit cgroup/PG samples, nginx edge histograms, runtime GC/scheduler
histograms, controlled load tests and dated pricing are separate work. Whole-box
usage is not an attributed platform resource or cost bound. Hack-wide daily
totals include synthetic accounts/events; use the per-event flag for real-event analyses. nginx serves custom-domain
files directly through try_files; those requests remain outside Go timers.

### Operating

These are future operator commands from a reviewed, merged checkout; none was
executed during this work. The owner must rebase this worktree onto current
origin/main first: its shared Git metadata is read-only to this worker, so
`git fetch origin main` could not update FETCH_HEAD. Keep the deployment lock
through migration and restart.

The single command below builds with full revision provenance, applies the
still-undeployed zs migration as postgres to both databases, transfers all seven
tables and the run sequence to each app role, runs migrate -mark and verifies
migration status, backs up/installs the binary, restarts both services, checks
public health/site routes and refreshes owner knowledge. Apply/ownership/mark
must precede deploying the new binary. Do not run until the coordinator accepts
the final measurement and test results.

```sh
mkdir -p build && sudo systemd-run --quiet --wait --pipe --collect -p MemoryMax=2G -p WorkingDirectory="$PWD" --uid="$(id -u)" --gid="$(id -g)" -E HOME="$HOME" -E PATH="$PATH" -E GOCACHE="$(go env GOCACHE)" -E GOMODCACHE="$(go env GOMODCACHE)" bash -euc 'CGO_ENABLED=0 go build -ldflags "-X github.com/vsriram/simple-host/internal/buildinfo.Version=$(git describe --tags --always --dirty) -X github.com/vsriram/simple-host/internal/buildinfo.Commit=$(git rev-parse HEAD) -X github.com/vsriram/simple-host/internal/buildinfo.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o build/simple-host.hack-paper-metrics.new ./cmd/server' && flock /tmp/simple-host-deploy.lock bash -euo pipefail -c 'new="$PWD/build/simple-host.hack-paper-metrics.new"; for pair in simplehost:/etc/simple-host.env simplehack:/etc/simple-hack.env; do dbn=${pair%%:*}; envf=${pair#*:}; sudo -u postgres psql -q -v ON_ERROR_STOP=1 -d "$dbn" -1 -f db/migrations/zs-hack-paper-metrics.sql; sudo -u postgres psql -q -v ON_ERROR_STOP=1 -d "$dbn" -c "ALTER TABLE paper_hack_event OWNER TO $dbn; ALTER TABLE paper_hack_minute OWNER TO $dbn; ALTER TABLE paper_hack_latency OWNER TO $dbn; ALTER TABLE paper_hack_phase OWNER TO $dbn; ALTER TABLE paper_hack_key OWNER TO $dbn; ALTER TABLE paper_hack_daily OWNER TO $dbn; ALTER TABLE paper_run OWNER TO $dbn; ALTER SEQUENCE paper_run_run_id_seq OWNER TO $dbn"; sudo systemd-run --quiet --wait --pipe --collect -p EnvironmentFile="$envf" "$new" migrate -mark zs-hack-paper-metrics.sql; status=$(sudo systemd-run --quiet --wait --pipe --collect -p EnvironmentFile="$envf" "$new" migrate -status); printf "%s\n" "$status"; if printf "%s\n" "$status" | grep -q "^pending"; then exit 1; fi; done; sudo cp -p /usr/local/bin/simple-host "/usr/local/bin/simple-host.bak-$(date -u +%Y%m%d-%H%M%S)"; sudo install -m 755 -o root -g root "$new" /usr/local/bin/simple-host; sudo systemctl restart simple-host simple-hack; for url in https://simple-host.app/healthz https://simple-hack.app/healthz https://simple-host.app/ https://simple-hack.app/ https://madurai-idly.vineetu.simple-host.app/ https://vineetu.simple-host.app/ https://vineetsriram.com/ https://sf-fog.today/; do curl -fsS --retry 5 --retry-connrefused --retry-delay 1 "$url" >/dev/null; done; test "$(curl -s -o /dev/null -w "%{http_code}" https://sites.simple-host.app/vineetu/madurai-idly/)" = 302; curl -fsSI https://sites.simple-host.app/vineetu/madurai-idly/ | tr -d "\r" | grep -qi "^location: https://madurai-idly.vineetu.simple-host.app/$"; sudo systemctl start sh-brain-sync'
```
