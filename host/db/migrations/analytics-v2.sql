-- Analytics v2: hourly buckets + a three-way traffic class (additive; safe to re-run).
--
-- Why this exists. v1 stored one views row per (site, day) with no notion of who
-- was asking. On this box that made the numbers meaningless: nginx-directory
-- probes every site's "/" every ~30s from 127.0.0.1, which is 2,880 "views" and
-- 1 "visitor" per site per day -- roughly 97% of the recorded traffic for the
-- busiest sites. v2 separates real people from crawlers from our own monitoring,
-- and buckets by hour so "the last 24 hours" is answerable at all.
--
-- class is one of:
--   'person' -- no bot signature; the number the owner actually cares about
--   'bot'    -- crawlers, AI scrapers, SEO tools, security scanners, HTTP libs
--   'infra'  -- our own monitoring: loopback probes and uptime checkers
--
-- Sizing: 12 sites x 24 hours x 3 classes is under 900 view rows/day, so hourly
-- granularity is kept for the full retention window rather than rolled up.

CREATE TABLE IF NOT EXISTS site_view_hourly (
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  hour    TIMESTAMPTZ NOT NULL,   -- UTC, truncated to the hour
  class   TEXT NOT NULL,          -- 'person' | 'bot' | 'infra'
  views   BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, hour, class)
);

CREATE INDEX IF NOT EXISTS site_view_hourly_hour_idx ON site_view_hourly (hour);

-- One row per distinct visitor per (site, hour, class). Uniques are counted with
-- COUNT(DISTINCT ip_hash) over whatever window is being asked for.
--
-- The ingest salt is stable (not rotated per day), so an ip_hash identifies the
-- same visitor across the whole retention window. That is what makes a real
-- "unique visitors over 30 days" count possible: COUNT(DISTINCT ip_hash) over a
-- range is genuine uniques, not the sum of the per-day figures.
CREATE TABLE IF NOT EXISTS site_visitor_hourly (
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  hour    TIMESTAMPTZ NOT NULL,
  class   TEXT NOT NULL,
  ip_hash BYTEA NOT NULL,
  PRIMARY KEY (site_id, hour, class, ip_hash)
);

CREATE INDEX IF NOT EXISTS site_visitor_hourly_hour_idx ON site_visitor_hourly (hour);

-- App DB role needs access (tables above may be created as a superuser).
GRANT ALL ON site_view_hourly, site_visitor_hourly TO simplehost;

-- DO NOT DROP site_view_daily / site_visitor_daily (from analytics.sql).
--
-- They are no longer written to, but they are the ONLY surviving record of
-- traffic before the classifier existed (2026-07-11 → 2026-08-08 on this
-- instance). The raw logs that period came from have already rotated away, so
-- nothing can rebuild it. The analytics API reads these tables for any day
-- before `classified_from` and reports them under the `unknown` class; dropping
-- them silently deletes ~29 sites' first month of history.
