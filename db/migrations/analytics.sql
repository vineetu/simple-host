-- Per-site visitor analytics (additive; safe to re-run).
-- nginx access log → Go ingester → aggregates; owner-scoped API reads these.

CREATE TABLE IF NOT EXISTS site_view_daily (
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  day     DATE NOT NULL,
  views   BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, day)
);

CREATE TABLE IF NOT EXISTS site_visitor_daily (
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  day     DATE NOT NULL,
  ip_hash BYTEA NOT NULL,
  PRIMARY KEY (site_id, day, ip_hash)
);

CREATE TABLE IF NOT EXISTS analytics_ingest_state (
  logfile      TEXT PRIMARY KEY,
  offset_bytes BIGINT NOT NULL DEFAULT 0,
  inode        BIGINT NOT NULL DEFAULT 0,
  updated_at   TIMESTAMPTZ DEFAULT now()
);
