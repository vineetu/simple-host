-- w3 hosted (2026-09-30): network use on the admin page.
--
-- traffic_daily and site_traffic_daily: bytes and requests served, from the
-- nginx `shanalytics` log, whose lines gain a ninth field, $bytes_sent
-- (deploy/prod/nginx-analytics-logformat.conf). kind is 'pages' (a site's
-- files) or 'api' (its /v1 calls). traffic_daily counts every line, site or
-- not; site_traffic_daily the lines whose host is a site's. Pruned with
-- ANALYTICS_RETENTION_DAYS.
--
-- net_usage_daily and net_counter_state: the box's own bytes in and out of
-- its main interface, from /proc/net/dev growth between samples
-- (internal/netusage), so month totals survive reboots and counter resets.
--
-- Apply BEFORE deploying the build that reads it (the server refuses to start
-- without it). Idempotent; new empty tables only.
CREATE TABLE IF NOT EXISTS traffic_daily (
  day      DATE NOT NULL,
  kind     TEXT NOT NULL,
  bytes    BIGINT NOT NULL DEFAULT 0,
  requests BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (day, kind)
);
CREATE TABLE IF NOT EXISTS site_traffic_daily (
  site_id  UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  day      DATE NOT NULL,
  kind     TEXT NOT NULL,
  bytes    BIGINT NOT NULL DEFAULT 0,
  requests BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, day, kind)
);
CREATE INDEX IF NOT EXISTS site_traffic_daily_day_idx ON site_traffic_daily (day);
CREATE TABLE IF NOT EXISTS net_usage_daily (
  day      DATE NOT NULL,
  iface    TEXT NOT NULL,
  rx_bytes BIGINT NOT NULL DEFAULT 0,
  tx_bytes BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (day, iface)
);
CREATE TABLE IF NOT EXISTS net_counter_state (
  iface          TEXT PRIMARY KEY,
  boot_id        TEXT NOT NULL,
  rx_bytes       BIGINT NOT NULL,
  tx_bytes       BIGINT NOT NULL,
  counting_since TIMESTAMPTZ NOT NULL,
  sampled_at     TIMESTAMPTZ NOT NULL
);
