# Paper metrics data dictionary

Schema/definition version 4 (2026-10-31), implemented and pending deployment.
Legacy Host collection and exports remain version 1; this revision does not migrate its
histograms or samples. Hack JSON tables carry metric_def_version (and a bucket_scheme
for histogram sets). Summary metadata names both schemes; new Hack CSV rows carry
metric_def_version and run_id. Version 4 retains the Hack-only daily provenance/suppression columns; the Host
daily export is exactly day/metric/value, with no additional JSON metadata. Host
export shapes and values remain unchanged from origin/main.

Hack people counts <5, including zero, are NULL plus a true suppression flag.
Scores/assignments/conflicts are also suppressed when judges are <5 or unknown,
in both per-event rows and the Hack daily set (independently per day).
Both application totals are hidden when either is <5/unknown or their difference
is <5; a missing daily counterpart also hides the known application total. Absolute phase/origin timestamps are never exported; only offsets are. Internal origin_change deduplicates a source stage update
across refresh/hook minute boundaries; it is never exported.
Absent event-minute rows mean zero in a healthy active collection window; outages
can also lose rows. PAPER_METRICS=off writes no rows, including no paper_run. JSON null
is an empty CSV field. include_small=1 only overrides the five-team event filter;
it does not override suppression or grant publication consent. The private
paper_hack_key mapping has **no export set**. Event keys are anonymous random signed 64-bit values exported as decimal strings
in JSON/CSV, preserving JavaScript precision. No row with event_key contains an
absolute date or timestamp. Minute/latency series expose only offsets from
starts_at; missing/deleted source origins give NULL. Snapshots omit both stored
creation-week and snapshot-day fields. Platform daily rows keep dates but have
no event_key. These are export-only privacy changes; stored times are retained.

Histogram estimates use the harmonic midpoint of scale-3 exponential buckets,
with <=4.33% relative error above 1us and <=1us absolute error in the zero bucket.
Merge counts, not percentiles. n>=1000 is required for p99, n>=10000 for p99.9;
below that report n/max. Legacy Host quantiles have only the old bucket-width
precision. The complete methodology is [paper-metrics.md](paper-metrics.md).

Host envelopes retain their original fields and have no definition/bucket metadata.
Hack JSON envelope fields: set identifies a fixed set name; from/to are inclusive UTC
filter dates; columns names the ordered row fields; rows holds those values;
metric_def_version selects these definitions; bucket_scheme, when present,
selects the histogram interpretation. No envelope field contains source IDs.

## api_routes

| Column | Unit | Definition | metric_def_version |
|---|---|---|---|
| `route` | route template | Registered API route template, never a request URL/path or event address. | 1 |
| `status` | HTTP status | Exact status in legacy per-route API counters. | 1 |
| `calls` | requests | API calls summed over the exported date range. | 1 |

## daily

The extra columns below apply only to Hack. Host returns `day`, `metric`, `value`
with definition 1 values and no suppression.

| Column | Unit | Definition | metric_def_version |
|---|---|---|---|
| `day` | UTC date | Day whose flows are counted; state totals describe collection time, normally just after midnight the next day. | 4 |
| `metric` | enum | Fixed daily metric name. Exact queries and units are listed below. | 4 |
| `value` | metric dependent | Daily value; people metrics below 5 export NULL with value_suppressed=true. | 4 |
| `run_id` | opaque integer | Process-start key referencing paper_run; not a person or event identifier. | 4 |
| `metric_def_version` | version | Definition version recorded by the producing run; 4 for new Hack data, 1 for unchanged Host collection. | 4 |
| `value_suppressed` | boolean | True means the value is hidden by the below-five or dependency suppression rules above. False with NULL means missing source data. | 4 |

## footprint

| Column | Unit | Definition | metric_def_version |
|---|---|---|---|
| `week` | UTC date | Monday of the anonymous weekly footprint cohort. | 1 |
| `versions` | directories | Retained vN directories counted by the disk walk. | 1 |
| `live_files` | files | Regular files under current/; symlinks are not followed. | 1 |
| `live_bytes` | bytes | Regular files under current/. | 1 |
| `total_bytes` | bytes | All regular files under the live site directory, including retained versions and runtime files; KV is separate. | 1 |
| `html_bytes` | bytes | Live .html/.htm file sizes. | 1 |
| `js_bytes` | bytes | Live .js/.mjs file sizes. | 1 |
| `css_bytes` | bytes | Live CSS file sizes. | 1 |
| `media_bytes` | bytes | Live media/font file sizes by the extension list in footprint.go. | 1 |
| `other_bytes` | bytes | Other live-file sizes. | 1 |
| `sqlite_bytes` | bytes | Regular files under runtime/sqlite. | 1 |
| `file_store_bytes` | bytes | Regular files under runtime/files. | 1 |
| `kv_bytes` | bytes | Sum of pg_column_size(value) for the site’s KV values. | 1 |
| `kv_rows` | records | KV rows in the live site. | 1 |
| `legacy_rows` | records | Non-deleted legacy collection items in the live site. | 1 |
| `views_30d` | page views | Person-class site views in the preceding 30 days, not distinct people. | 1 |
| `age_days` | days | Site age when the weekly walk ran. | 1 |
| `days_since_update` | days | Days since the site updated when the weekly walk ran. | 1 |

## footprint_summary

| Column | Unit | Definition | metric_def_version |
|---|---|---|---|
| `week` | UTC date | Monday of the anonymous weekly footprint cohort. | 1 |
| `sites` | sites | Number of anonymous live-site rows in the weekly cohort. | 1 |
| `idle_sites` | sites | Sites with zero person-class views in the preceding 30 days. | 1 |
| `live_bytes_p50` | bytes | Discrete median live-site bytes in the weekly cohort. | 1 |
| `live_bytes_p90` | bytes | Discrete p90 live-site bytes in the weekly cohort. | 1 |
| `live_bytes_p99` | bytes | Discrete p99 live-site bytes in the weekly cohort. | 1 |
| `live_bytes_max` | bytes | Largest live-site byte count in the weekly cohort. | 1 |
| `total_bytes_sum` | bytes | Sum of total_bytes over the weekly cohort. | 1 |
| `js_bytes_p50` | bytes | Discrete median live JavaScript bytes. | 1 |
| `js_bytes_p90` | bytes | Discrete p90 live JavaScript bytes. | 1 |
| `html_bytes_p50` | bytes | Discrete median live HTML bytes. | 1 |
| `data_bytes_p50` | bytes | Discrete median SQLite+file-store+KV bytes. | 1 |
| `data_bytes_p99` | bytes | Discrete p99 SQLite+file-store+KV bytes. | 1 |
| `sites_with_data` | sites | Sites with KV/SQLite/files or legacy saved records. | 1 |
| `idle_total_bytes_mean` | bytes | Rounded mean total_bytes among sites with no person views in 30 days. | 1 |
| `versions_p50` | directories | Discrete median retained-version count per weekly cohort. | 1 |

## hack_events

| Column | Unit | Definition | metric_def_version |
|---|---|---|---|
| `event_key` | decimal string | Random event key stable across snapshots/series; only the internal key lookup knows the source event ID. | 4 |
| `run_id` | opaque integer | Process-start key referencing paper_run; not a person or event identifier. | 4 |
| `synthetic` | boolean | Sticky test-event classification using address prefixes e2e-/mock-/mock2-/local- or creator username prefix e2e-. | 4 |
| `stage` | enum | Current lifecycle enum from `events.stage`. | 4 |
| `duration_hours` | hours | `(ends_at - starts_at)` in hours; NULL when either is unset. | 4 |
| `expected_participants` | count | Event's expected count; NULL when unset. | 4 |
| `participants` | count | Participant membership rows whose approval status is approved; joins requiring no approval already receive that status. | 4 |
| `organisers` | count | Membership rows with role organiser. | 4 |
| `judges` | count | Membership rows with role judge; departed judges are absent, while their retained scores still count below. | 4 |
| `applications` | count | All current participant membership rows, including approved, pending and rejected statuses; withdrawn rows are absent. | 4 |
| `applications_approved` | count | Current participant membership rows with approved status. | 4 |
| `teams` | count | Current `event_teams` row count. | 4 |
| `teams_with_entry` | count | Teams with an `event_entries` row, including a partially filled entry. | 4 |
| `teams_kept_after_leave` | count | Teams with `event_teams.kept_after_leave = true`, including empty preserved teams. | 4 |
| `team_size_p50` | count | Discrete median of membership counts per team, including empty teams as zero. | 4 |
| `team_size_max` | count | Largest membership count per team; zero with no teams. | 4 |
| `team_sites` | count | Live sites belonging to the event's holding account, excluding its event-ID-named website. | 4 |
| `team_site_bytes_sum` | bytes | Sum of team-site `total_bytes` using the shared weekly footprint disk walk; KV is separate. | 4 |
| `team_site_bytes_p50` | bytes | Discrete median of those byte sizes; zero with no team sites. | 4 |
| `team_site_versions_sum` | count | Number of retained team-site `vN` directories, using exactly the footprint naming rules. | 4 |
| `event_storage_bytes` | bytes | KV `pg_column_size(value)` plus `runtime/sqlite` and `runtime/files` bytes across all live sites of the holding account, including the event website. | 4 |
| `submissions` | count | Current `event_entries` row count, including partial entries. | 4 |
| `entries_with_code_url` | count | Entry rows with a nonempty code URL; URLs are never copied. | 4 |
| `entries_with_video` | count | Entry rows with a nonempty video URL; URLs are never copied. | 4 |
| `scores` | count | Current `event_scores` row count, including scores retained after a judge leaves. | 4 |
| `score_criteria` | count | Current `rubric_criteria` row count. | 4 |
| `assignments` | count | Current `event_assignments` row count. | 4 |
| `conflicts` | count | Current `event_conflicts` row count. | 4 |
| `votes` | count | Current `event_votes` row count; voter email is never selected. | 4 |
| `announcements` | count | Current `event_announcements` row count; titles and bodies are never selected. | 4 |
| `results_published` | boolean | Whether the event timestamp or the existing `event_results` publication row exists. | 4 |
| `deploys_total` | count | All retained `vN` directories across live event-owned sites, including the event website; pruning removes directories, so this is retained versions rather than an all-time deployment counter. | 4 |
| `minutes_create_to_first_deploy` | minutes | Earliest retained completed team-site `versions.created_at` (`active`/`ready`) minus `events.starts_at`, rounded to the nearest ten minutes; NULL if either is unavailable. No historical opened-at field exists, so scheduled start is a proxy for opening; pruning version records can remove the original first deployment. | 4 |
| `metric_def_version` | version | Definition version recorded by the producing run; 4 for new Hack data, 1 for unchanged Host collection. | 4 |
| `expected_participants_suppressed` | boolean | True means the value is hidden by the below-five or dependency suppression rules above. False with NULL means missing source data. | 4 |
| `participants_suppressed` | boolean | True means the value is hidden by the below-five or dependency suppression rules above. False with NULL means missing source data. | 4 |
| `organisers_suppressed` | boolean | True means the value is hidden by the below-five or dependency suppression rules above. False with NULL means missing source data. | 4 |
| `judges_suppressed` | boolean | True means the value is hidden by the below-five or dependency suppression rules above. False with NULL means missing source data. | 4 |
| `applications_suppressed` | boolean | True means the value is hidden by the below-five or dependency suppression rules above. False with NULL means missing source data. | 4 |
| `applications_approved_suppressed` | boolean | True means the value is hidden by the below-five or dependency suppression rules above. False with NULL means missing source data. | 4 |
| `team_size_p50_suppressed` | boolean | True means the value is hidden by the below-five or dependency suppression rules above. False with NULL means missing source data. | 4 |
| `team_size_max_suppressed` | boolean | True means the value is hidden by the below-five or dependency suppression rules above. False with NULL means missing source data. | 4 |
| `votes_suppressed` | boolean | True means the value is hidden by the below-five or dependency suppression rules above. False with NULL means missing source data. | 4 |
| `assignments_suppressed` | boolean | True when a known assignment count is hidden because the judge count is below five or unavailable. | 4 |
| `scores_suppressed` | boolean | True when a known score count is hidden because the judge count is below five or unavailable. | 4 |
| `conflicts_suppressed` | boolean | True when a known conflict count is hidden because the judge count is below five or unavailable. | 4 |

## hack_latency

| Column | Unit | Definition | metric_def_version |
|---|---|---|---|
| `run_id` | opaque integer | Process-start key referencing paper_run; not a person or event identifier. | 4 |
| `minute_offset` | minutes | Rounded offset of the internally stored UTC request-start slot from events.starts_at, resolved through the private lookup at export. NULL for missing starts_at or deleted source event. Completion never moves the sample; no absolute time is exported. | 4 |
| `event_key` | decimal string | Random event key stable across snapshots/series; only the internal key lookup knows the source event ID. | 4 |
| `synthetic` | boolean | Sticky test-event classification using address prefixes e2e-/mock-/mock2-/local- or creator username prefix e2e-. | 4 |
| `class` | enum | Route class: site_file, data_read/write, auth, mcp, api_read/write, platform_page; legacy Host also has held-open mcp_stream. | 4 |
| `status_group` | enum | HTTP 2xx, 3xx, 4xx or 5xx, stored separately so successful and failed latency distributions do not mix. | 4 |
| `n` | samples | Exact number of histogram observations in this event/class/status/start-minute cell. | 4 |
| `sum_us` | microseconds | Exact sum of integer-microsecond server durations; add across cells/windows. | 4 |
| `max_us` | microseconds | Exact maximum integer-microsecond server duration, maintained by atomic CAS; merge with max. | 4 |
| `bytes_out` | bytes | Response-body bytes written by Go; excludes TLS/nginx overhead and nginx-only files. | 4 |
| `idx_offset` | bucket index | Index of the first array element; -1 is <=1us. Positive buckets use ceil(log2(us)*8)-1. | 4 |
| `counts` | int64 counts | Trimmed contiguous span of exponential buckets at idx_offset; includes interior zeros. Align offsets and sum counts to merge. | 4 |
| `arrivals_1s` | 60 request counts | One element per second (0..59) of the UTC start minute; event-wide in hack_minutes, class/status-scoped in hack_latency. | 4 |
| `writes_1s` | 60 request counts | Accepted writes by request-start second, including writes completed later; event-wide in hack_minutes, class/status-scoped in hack_latency. | 4 |
| `metric_def_version` | version | Definition version recorded by the producing run; 4 for new Hack data, 1 for unchanged Host collection. | 4 |

## hack_minutes

| Column | Unit | Definition | metric_def_version |
|---|---|---|---|
| `run_id` | opaque integer | Process-start key referencing paper_run; not a person or event identifier. | 4 |
| `minute_offset` | minutes | Rounded offset of the internally stored UTC request-start slot from events.starts_at, resolved through the private lookup at export. NULL for missing starts_at or deleted source event. Completion never moves the sample; no absolute time is exported. | 4 |
| `slot_secs` | seconds | 60, the collection-window width. | 4 |
| `event_key` | decimal string | Random event key stable across snapshots/series; only the internal key lookup knows the source event ID. | 4 |
| `synthetic` | boolean | Sticky test-event classification using address prefixes e2e-/mock-/mock2-/local- or creator username prefix e2e-. | 4 |
| `requests` | requests | Number of requests in the class/day or event/start slot. Hack includes failed requests and excludes health, OPTIONS and MCP streams. | 4 |
| `writes` | requests | Accepted mutating-method event API requests (actual replay status for MCP) or committed storage requests, counted once per outer request; deploys are separate. | 4 |
| `data_writes` | requests | Committed KV/SQLite/file mutation requests attributed to their start slot; query/download-link operations excluded. | 4 |
| `deploys` | versions | Successful newly committed/promoted team-site versions, excluding rollbacks and event websites. | 4 |
| `active_members` | members | Distinct authenticated members with an attributed request in the start slot; activity, not concurrency. | 4 |
| `inflight_max` | requests | Sampled maximum of the event-wide atomic in-flight gauge; cached routes cover the handler, fallback forms begin at resolution. | 4 |
| `peak_rps_1s` | requests/second | Largest of the 60 one-second arrival counts in this minute; not average requests per second. | 4 |
| `arrivals_1s` | 60 request counts | One element per second (0..59) of the UTC start minute; event-wide in hack_minutes, class/status-scoped in hack_latency. | 4 |
| `writes_1s` | 60 request counts | Accepted writes by request-start second, including writes completed later; event-wide in hack_minutes, class/status-scoped in hack_latency. | 4 |
| `metric_def_version` | version | Definition version recorded by the producing run; 4 for new Hack data, 1 for unchanged Host collection. | 4 |
| `active_members_suppressed` | boolean | True means the value is hidden by the below-five or dependency suppression rules above. False with NULL means missing source data. | 4 |

## hack_phases

| Column | Unit | Definition | metric_def_version |
|---|---|---|---|
| `run_id` | opaque integer | Process-start key referencing paper_run; not a person or event identifier. | 4 |
| `event_key` | decimal string | Random event key stable across snapshots/series; only the internal key lookup knows the source event ID. | 4 |
| `synthetic` | boolean | Sticky test-event classification using the defined event/creator prefixes, captured with the phase. | 4 |
| `stage` | enum | draft/open/building/closed/judging/results/archived, or submission_deadline/judging_locked/results_published/voting_opens/voting_closes. | 4 |
| `minute_offset` | minutes | Rounded offset from starts_at or first captured open; NULL if neither origin is known. | 4 |
| `observed` | boolean | True for an actual persisted-stage difference found by refresh (may lag a minute). False for baseline rows, committed-path captures or stored timestamp markers; read with baseline. | 4 |
| `metric_def_version` | version | Definition version recorded by the producing run; 4 for new Hack data, 1 for unchanged Host collection. | 4 |
| `baseline` | boolean | True only for an event’s first state observation with no prior lifecycle history; never a stage change. Its offset locates the observation, not the historical transition. | 4 |

## latency

| Column | Unit | Definition | metric_def_version |
|---|---|---|---|
| `day` | UTC date | Day whose flows are counted; state totals describe collection time, normally just after midnight the next day. | 1 |
| `class` | enum | Route class: site_file, data_read/write, auth, mcp, api_read/write, platform_page; legacy Host also has held-open mcp_stream. | 1 |
| `requests` | requests | Number of requests in the class/day or event/start slot. Hack includes failed requests and excludes health, OPTIONS and MCP streams. | 1 |
| `server_error_pct` | percent | 5xx requests / all requests, multiplied by 100. | 1 |
| `bytes_out` | bytes | Response-body bytes written by Go; excludes TLS/nginx overhead and nginx-only files. | 1 |
| `mean_ms` | milliseconds | Legacy total server time / requests. | 1 |
| `p50_ms` | milliseconds | Legacy bucket-interpolated median; resolution is the old bucket width, not the scale-3 error guarantee. | 1 |
| `p90_ms` | milliseconds | Legacy bucket-interpolated 90th percentile. | 1 |
| `p99_ms` | milliseconds | Legacy bucket-interpolated 99th percentile; use only with n>=1000. | 1 |
| `p999_ms` | milliseconds | Legacy bucket-interpolated 99.9th percentile; use only with n>=10000. | 1 |

## latency_buckets

| Column | Unit | Definition | metric_def_version |
|---|---|---|---|
| `day` | UTC date | Day whose flows are counted; state totals describe collection time, normally just after midnight the next day. | 1 |
| `class` | enum | Route class: site_file, data_read/write, auth, mcp, api_read/write, platform_page; legacy Host also has held-open mcp_stream. | 1 |
| `le_us` | microseconds | Legacy histogram upper bound; -1 marks its unbounded overflow bucket. Not exponential scale-3 counts. | 1 |
| `requests` | requests | Number of requests in the class/day or event/start slot. Hack includes failed requests and excludes health, OPTIONS and MCP streams. | 1 |

## process

| Column | Unit | Definition | metric_def_version |
|---|---|---|---|
| `at` | UTC timestamp | Time the legacy process/box sample was taken. | 1 |
| `started_at` | UTC timestamp | Process start used for restart/provenance grouping. | 1 |
| `proc_cpu_cores` | cores | Process user+system CPU delta divided by elapsed wall time; 1.0 is one core. | 1 |
| `proc_rss_bytes` | bytes | Self VmRSS; a resident-set size, not cgroup memory. | 1 |
| `go_heap_bytes` | bytes | Go HeapAlloc at the sample. | 1 |
| `goroutines` | goroutines | Go goroutine count at sample time. | 1 |
| `open_fds` | descriptors | Count of open process file descriptors. | 1 |
| `pg_rss_bytes` | bytes | Summed visible Postgres RSS (shared pages double counted); 0 when production sandbox hides those processes. | 1 |
| `nginx_rss_bytes` | bytes | Summed visible nginx RSS; 0 when production sandbox hides those processes. | 1 |
| `box_cpu_cores` | cores | Whole-box busy CPU delta, not platform-attributed CPU. | 1 |
| `box_cpus` | logical CPUs | CPU count visible to the Go process. | 1 |
| `box_mem_total_bytes` | bytes | Whole-box MemTotal. | 1 |
| `box_mem_avail_bytes` | bytes | Whole-box MemAvailable. | 1 |
| `load1` | load | Whole-box one-minute load average, not per-service load. | 1 |

## process_daily

| Column | Unit | Definition | metric_def_version |
|---|---|---|---|
| `day` | UTC date | Day whose flows are counted; state totals describe collection time, normally just after midnight the next day. | 1 |
| `samples` | samples | Number of five-minute process samples in the day. | 1 |
| `restarts` | starts | Distinct process-start timestamps minus one within that day (legacy export convention). | 1 |
| `proc_cpu_mean` | cores | Mean sampled process CPU share in the day. | 1 |
| `proc_cpu_p99` | cores | 99th percentile of sampled process CPU share, computed on export. | 1 |
| `proc_rss_median` | bytes | Median self RSS over the day. | 1 |
| `proc_rss_max` | bytes | Maximum sampled self RSS over the day. | 1 |
| `pg_rss_median` | bytes | Median legacy summed visible Postgres RSS; not per-DB memory. | 1 |
| `nginx_rss_median` | bytes | Median legacy summed visible nginx RSS. | 1 |
| `box_cpu_mean` | cores | Mean sampled whole-box CPU share in the day; not service attribution. | 1 |
| `box_mem_used_median` | bytes | Median whole-box MemTotal minus MemAvailable; not service memory. | 1 |
| `goroutines_median` | goroutines | Median sampled goroutine count in the day. | 1 |

## runs

| Column | Unit | Definition | metric_def_version |
|---|---|---|---|
| `run_id` | opaque integer | Process-start key referencing paper_run; not a person or event identifier. | 4 |
| `started_at` | UTC timestamp | Process start used for restart/provenance grouping. | 4 |
| `role` | enum | host or hack process role. | 4 |
| `vcs_revision` | Git revision | Embedded source revision, or NULL if unavailable; never inferred from a production checkout. | 4 |
| `vcs_modified` | boolean | Embedded dirty-build flag. | 4 |
| `go_version` | version | Go runtime/toolchain version. | 4 |
| `binary_sha256` | hex SHA-256 | Digest of the running executable; NULL if unreadable. | 4 |
| `config_hash` | hex SHA-256 | Hash of only whitelisted parsed non-secret settings; never a hash of keys, credentials or the complete environment. | 4 |
| `metric_def_version` | version | Definition version recorded by the producing run; 4 for new Hack data, 1 for unchanged Host collection. | 4 |
| `bucket_scheme` | enum | exp2_scale3_us_v1 for new Hack histograms; legacy_16_upper_us_v1 for unchanged Host collection. | 4 |
| `gomaxprocs` | logical CPUs | runtime.GOMAXPROCS at process registration. | 4 |
| `num_cpu` | logical CPUs | runtime.NumCPU at process registration. | 4 |
| `cgroup_memory_max` | bytes or max | Readable process cgroup memory.max; NULL if unavailable. | 4 |
| `cgroup_cpu_max` | quota_us period_us, or max period_us | Readable process cgroup cpu.max; NULL if unavailable. | 4 |

## Daily metric names and values

Each query below is the exact definition of value for that metric. $1 is the UTC
flow-count day; queries without $1 read current state. Host state counts are
collection definition 1; Hack metrics and paper_tables_bytes are definition 4.
The Hack-only provenance companion stamps their run without changing Host tables.

| Metric | Unit | Definition (SQL) | metric_def_version |
|---|---|---|---|
| `accounts` | count | `SELECT count(*) FROM users WHERE created_at < $1::date + 1` | 1 |
| `accounts_new` | count | `SELECT count(*) FROM users WHERE created_at >= $1::date AND created_at < $1::date + 1` | 1 |
| `accounts_with_live_site` | count | `SELECT count(DISTINCT user_id) FROM sites WHERE deleted_at IS NULL` | 1 |
| `sites_live` | count | `SELECT count(*) FROM sites WHERE deleted_at IS NULL` | 1 |
| `sites_new` | count | `SELECT count(*) FROM sites WHERE created_at >= $1::date AND created_at < $1::date + 1` | 1 |
| `sites_updated_day` | count | `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND updated_at >= $1::date AND updated_at < $1::date + 1` | 1 |
| `sites_updated_30d` | count | `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND updated_at >= $1::date - 29` | 1 |
| `sites_offline` | count | `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND offline_at IS NOT NULL` | 1 |
| `sites_viewed_day` | count | `SELECT count(DISTINCT site_id) FROM site_view_hourly WHERE class = 'person' AND hour >= $1::date AND hour < $1::date + 1` | 1 |
| `sites_viewed_7d` | count | `SELECT count(DISTINCT site_id) FROM site_view_hourly WHERE class = 'person' AND hour >= $1::date - 6 AND hour < $1::date + 1` | 1 |
| `sites_viewed_30d` | count | `SELECT count(DISTINCT site_id) FROM site_view_hourly WHERE class = 'person' AND hour >= $1::date - 29 AND hour < $1::date + 1` | 1 |
| `page_views_person_day` | count | `SELECT COALESCE(sum(views), 0) FROM site_view_hourly WHERE class = 'person' AND hour >= $1::date AND hour < $1::date + 1` | 1 |
| `page_views_bot_day` | count | `SELECT COALESCE(sum(views), 0) FROM site_view_hourly WHERE class = 'bot' AND hour >= $1::date AND hour < $1::date + 1` | 1 |
| `sites_with_storage` | count | `SELECT count(DISTINCT r.site_id) FROM site_storage_resources r JOIN sites s ON s.id = r.site_id WHERE s.deleted_at IS NULL` | 1 |
| `sites_with_kv` | count | `SELECT count(DISTINCT r.site_id) FROM site_storage_resources r JOIN sites s ON s.id = r.site_id WHERE s.deleted_at IS NULL AND r.kind = 'kv'` | 1 |
| `sites_with_sqlite` | count | `SELECT count(DISTINCT r.site_id) FROM site_storage_resources r JOIN sites s ON s.id = r.site_id WHERE s.deleted_at IS NULL AND r.kind = 'sqlite'` | 1 |
| `sites_with_files_store` | count | `SELECT count(DISTINCT r.site_id) FROM site_storage_resources r JOIN sites s ON s.id = r.site_id WHERE s.deleted_at IS NULL AND r.kind = 'files'` | 1 |
| `sites_with_legacy_data` | count | `SELECT count(DISTINCT c.site_id) FROM collection_items c JOIN sites s ON s.id = c.site_id WHERE s.deleted_at IS NULL AND c.deleted_at IS NULL` | 1 |
| `sites_with_visitor_signin` | count | `SELECT count(DISTINCT v.site_id) FROM site_visitor_signins v JOIN sites s ON s.id = v.site_id WHERE s.deleted_at IS NULL` | 1 |
| `visitor_signins_total` | count | `SELECT count(*) FROM site_visitor_signins WHERE first_at < $1::date + 1` | 1 |
| `sites_access_specific` | count | `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND access = 'specific'` | 1 |
| `sites_with_named_viewers` | count | `SELECT count(DISTINCT v.site_id) FROM site_viewers v JOIN sites s ON s.id = v.site_id WHERE s.deleted_at IS NULL` | 1 |
| `sites_with_passcode` | count | `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND passcode_enc IS NOT NULL` | 1 |
| `sites_custom_domain` | count | `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND custom_domain IS NOT NULL` | 1 |
| `sites_custom_domain_cert_live` | count | `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND custom_domain IS NOT NULL AND domain_cert_status = 'live'` | 1 |
| `address_families` | count | `SELECT count(*) FROM address_families` | 1 |
| `certs_person_wildcard` | count | `SELECT count(*) FROM domain_cert_requests` | 1 |
| `certs_family` | count | `SELECT count(*) FROM family_cert_requests` | 1 |
| `sites_with_notifications` | count | `SELECT count(DISTINCT site_id) FROM site_notifications` | 1 |
| `connector_grants_active_30d` | count | `SELECT count(*) FROM oauth_grants WHERE last_used_at >= $1::date - 29` | 1 |
| `connector_accounts_active_30d` | count | `SELECT count(DISTINCT user_id) FROM oauth_grants WHERE last_used_at >= $1::date - 29` | 1 |
| `storage_resources` | count | `SELECT count(*) FROM site_storage_resources` | 1 |
| `resources_read_anyone` | count | `SELECT count(*) FROM site_storage_resources WHERE acc_read = 'anyone'` | 1 |
| `resources_add_anyone` | count | `SELECT count(*) FROM site_storage_resources WHERE acc_add = 'anyone'` | 1 |
| `resources_add_checked` | count | `SELECT count(*) FROM site_storage_resources WHERE acc_add = 'checked'` | 1 |
| `resources_add_signed_in` | count | `SELECT count(*) FROM site_storage_resources WHERE acc_add = 'signed-in'` | 1 |
| `resources_own_reads` | count | `SELECT count(*) FROM site_storage_resources WHERE acc_read = 'own'` | 1 |
| `sites_suspended` | count | `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND suspended_at IS NOT NULL` | 1 |
| `accounts_suspended` | count | `SELECT count(*) FROM users WHERE suspended_at IS NOT NULL` | 1 |
| `kv_rows` | count | `SELECT count(*) FROM site_storage_kv` | 1 |
| `kv_value_bytes` | bytes | `SELECT COALESCE(sum(pg_column_size(value)), 0) FROM site_storage_kv` | 1 |
| `legacy_rows` | count | `SELECT count(*) FROM collection_items WHERE deleted_at IS NULL` | 1 |
| `db_bytes` | bytes | `SELECT pg_database_size(current_database())` | 1 |
| `api_calls_day` | count | `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date` | 1 |
| `api_errors_5xx_day` | count | `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND status >= 500` | 1 |
| `api_refused_429_day` | count | `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND status = 429` | 1 |
| `api_denied_401_403_day` | count | `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND status IN (401, 403)` | 1 |
| `api_mcp_calls_day` | count | `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND route = 'POST /mcp'` | 1 |
| `api_data_reads_day` | count | `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND route ~ '/storage/|/collections|/state|/data$|/data/' AND (route LIKE 'GET %' OR route LIKE 'HEAD %')` | 1 |
| `api_data_writes_day` | count | `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND route ~ '/storage/|/collections|/state|/data$|/data/' AND route !~ '^(GET|HEAD) '` | 1 |
| `api_deploys_day` | count | `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND status < 300 AND route ~ '^(POST|PUT) /v1/sites/\{sitename\}(/files)?$'` | 1 |
| `bytes_out_pages_day` | bytes | `SELECT COALESCE(sum(bytes), 0) FROM traffic_daily WHERE day = $1::date AND kind = 'pages'` | 1 |
| `bytes_out_api_day` | bytes | `SELECT COALESCE(sum(bytes), 0) FROM traffic_daily WHERE day = $1::date AND kind = 'api'` | 1 |
| `requests_pages_day` | count | `SELECT COALESCE(sum(requests), 0) FROM traffic_daily WHERE day = $1::date AND kind = 'pages'` | 1 |
| `net_tx_bytes_day` | bytes | `SELECT COALESCE(sum(tx_bytes), 0) FROM net_usage_daily WHERE day = $1::date` | 1 |
| `net_rx_bytes_day` | bytes | `SELECT COALESCE(sum(rx_bytes), 0) FROM net_usage_daily WHERE day = $1::date` | 1 |
| `process_restarts_day` | count | `SELECT count(DISTINCT started_at) FROM paper_process_sample WHERE at >= $1::date AND at < $1::date + 1 AND started_at >= $1::date` | 1 |
| `sample_minutes_missing_day` | minutes | `SELECT GREATEST(0, 1440 - 5 * count(*)) FROM paper_process_sample WHERE at >= $1::date AND at < $1::date + 1` | 1 |
| `paper_tables_bytes` | bytes | `SELECT COALESCE(sum(pg_total_relation_size(oid)),0) FROM pg_class WHERE relnamespace=current_schema()::regnamespace AND relkind='r' AND relname LIKE 'paper\_%'` | 4 |
| `hack_events_total` | count | `SELECT count(*) FROM events` | 4 |
| `hack_events_created_day` | count | `SELECT count(*) FROM events WHERE created_at >= $1::date AND created_at < $1::date+1` | 4 |
| `hack_participants_total` | count | `SELECT count(*) FROM event_members WHERE role='participant' AND approval_status='approved'` | 4 |
| `hack_judges_total` | count | `SELECT count(*) FROM event_members WHERE role='judge'` | 4 |
| `hack_teams_total` | count | `SELECT count(*) FROM event_teams` | 4 |
| `hack_entries_total` | count | `SELECT count(*) FROM event_entries` | 4 |
| `hack_scores_total` | count | `SELECT count(*) FROM event_scores` | 4 |
| `hack_votes_total` | count | `SELECT count(*) FROM event_votes` | 4 |
| `hack_applications_day` | count | `SELECT count(*) FROM event_members WHERE role='participant' AND joined_via='application' AND joined_at >= $1::date AND joined_at < $1::date+1` | 4 |
| `hack_team_sites_live` | count | `SELECT count(*) FROM sites s JOIN events e ON e.account_id=s.user_id WHERE s.deleted_at IS NULL AND s.name<>e.id::text` | 4 |
| `hack_event_websites_published` | count | `SELECT count(*) FROM events e JOIN sites s ON s.user_id=e.account_id AND s.name=e.id::text WHERE s.deleted_at IS NULL AND s.active_version>0` | 4 |
| `hack_events_by_stage_draft` | events | `SELECT count(*) FROM events WHERE stage='draft'` | 4 |
| `hack_events_by_stage_open` | events | `SELECT count(*) FROM events WHERE stage='open'` | 4 |
| `hack_events_by_stage_building` | events | `SELECT count(*) FROM events WHERE stage='building'` | 4 |
| `hack_events_by_stage_closed` | events | `SELECT count(*) FROM events WHERE stage='closed'` | 4 |
| `hack_events_by_stage_judging` | events | `SELECT count(*) FROM events WHERE stage='judging'` | 4 |
| `hack_events_by_stage_results` | events | `SELECT count(*) FROM events WHERE stage='results'` | 4 |
| `hack_events_by_stage_archived` | events | `SELECT count(*) FROM events WHERE stage='archived'` | 4 |
