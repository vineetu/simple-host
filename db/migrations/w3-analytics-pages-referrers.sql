-- w3 hosted (2026-09-27): top pages and top referring domains per site in the
-- owner's Analytics tab. People only, views per day. The referrer is kept as a
-- domain alone: the nginx `shanalytics` log line gains an eighth field with
-- just the referring host (deploy/prod/nginx-analytics-logformat.conf), and the
-- ingester reads 7- and 8-field lines alike.
--
-- Apply BEFORE deploying the build that reads it (the server refuses to start
-- without it). Idempotent; new empty tables only.
CREATE TABLE IF NOT EXISTS site_page_daily (
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  day     DATE NOT NULL,
  path    TEXT NOT NULL,
  views   BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, day, path)
);
CREATE INDEX IF NOT EXISTS site_page_daily_day_idx ON site_page_daily (day);
CREATE TABLE IF NOT EXISTS site_referrer_daily (
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  day     DATE NOT NULL,
  domain  TEXT NOT NULL,
  views   BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, day, domain)
);
CREATE INDEX IF NOT EXISTS site_referrer_daily_day_idx ON site_referrer_daily (day);
