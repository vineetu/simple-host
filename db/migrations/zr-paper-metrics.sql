-- Paper metrics (SIM-335, 2026-10-31): aggregate-only numbers for the research
-- papers on this deployment. No person, address, site name or path is stored.
-- Methodology: docs/paper-metrics.md. Written by internal/papermetrics.

-- Server-side time per route class per UTC day, as a histogram.
-- le_us is the bucket's upper bound in microseconds; -1 is the overflow bucket.
CREATE TABLE IF NOT EXISTS paper_latency_daily (
  day      DATE NOT NULL,
  class    TEXT NOT NULL,
  le_us    BIGINT NOT NULL,
  requests BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (day, class, le_us)
);
CREATE TABLE IF NOT EXISTS paper_class_daily (
  day           DATE NOT NULL,
  class         TEXT NOT NULL,
  requests      BIGINT NOT NULL DEFAULT 0,
  server_errors BIGINT NOT NULL DEFAULT 0,
  total_us      BIGINT NOT NULL DEFAULT 0,
  bytes_out     BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (day, class)
);
-- Every five minutes: this process, Postgres, nginx and the box.
CREATE TABLE IF NOT EXISTS paper_process_sample (
  at                  TIMESTAMPTZ PRIMARY KEY,
  started_at          TIMESTAMPTZ NOT NULL,
  proc_cpu_cores      DOUBLE PRECISION NOT NULL DEFAULT 0,
  proc_rss_bytes      BIGINT NOT NULL DEFAULT 0,
  go_heap_bytes       BIGINT NOT NULL DEFAULT 0,
  goroutines          INTEGER NOT NULL DEFAULT 0,
  open_fds            INTEGER NOT NULL DEFAULT 0,
  pg_rss_bytes        BIGINT NOT NULL DEFAULT 0,
  nginx_rss_bytes     BIGINT NOT NULL DEFAULT 0,
  box_cpu_cores       DOUBLE PRECISION NOT NULL DEFAULT 0,
  box_cpus            INTEGER NOT NULL DEFAULT 0,
  box_mem_total_bytes BIGINT NOT NULL DEFAULT 0,
  box_mem_avail_bytes BIGINT NOT NULL DEFAULT 0,
  load1               DOUBLE PRECISION NOT NULL DEFAULT 0
);
-- Counts as of the end of each UTC day (internal/papermetrics dailyMetrics).
CREATE TABLE IF NOT EXISTS paper_daily (
  day    DATE NOT NULL,
  metric TEXT NOT NULL,
  value  DOUBLE PRECISION NOT NULL DEFAULT 0,
  PRIMARY KEY (day, metric)
);
-- One row per live site per week, with no site or owner identifier.
CREATE TABLE IF NOT EXISTS paper_site_footprint (
  week              DATE NOT NULL,
  versions          INTEGER NOT NULL DEFAULT 0,
  live_files        INTEGER NOT NULL DEFAULT 0,
  live_bytes        BIGINT NOT NULL DEFAULT 0,
  total_bytes       BIGINT NOT NULL DEFAULT 0,
  html_bytes        BIGINT NOT NULL DEFAULT 0,
  js_bytes          BIGINT NOT NULL DEFAULT 0,
  css_bytes         BIGINT NOT NULL DEFAULT 0,
  media_bytes       BIGINT NOT NULL DEFAULT 0,
  other_bytes       BIGINT NOT NULL DEFAULT 0,
  sqlite_bytes      BIGINT NOT NULL DEFAULT 0,
  file_store_bytes  BIGINT NOT NULL DEFAULT 0,
  kv_bytes          BIGINT NOT NULL DEFAULT 0,
  kv_rows           BIGINT NOT NULL DEFAULT 0,
  legacy_rows       BIGINT NOT NULL DEFAULT 0,
  views_30d         BIGINT NOT NULL DEFAULT 0,
  age_days          INTEGER NOT NULL DEFAULT 0,
  days_since_update INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS paper_site_footprint_week_idx ON paper_site_footprint (week);
