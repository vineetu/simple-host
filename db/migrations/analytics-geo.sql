-- Analytics geo: per-country visitor counts (additive; safe to re-run).
--
-- Country is resolved ON THIS BOX from a local range table. Visitor IPs are
-- never sent anywhere: the published privacy page says only API-caller IPs
-- reach a third party, and a lookup service for visitor addresses would make
-- that false. The raw IP still leaves no trace in the database -- it is
-- resolved in memory during ingest and only the two-letter country survives.
--
-- Applying this file is safe against the running server: the new column has a
-- default and the old binary names its columns explicitly. Order of operations:
--
--   psql -f db/migrations/analytics-geo.sql     -- before the new binary; the
--                                                  ingester errors out (without
--                                                  advancing its offset) until
--                                                  ip_country_ranges exists
--   ip-country-load                             -- fill the ranges, ~717k rows
--   systemctl restart simple-host               -- new traffic gets a country
--   analytics-rebuild                           -- backfill: the log still has
--                                                  remote_addr, so history can
--                                                  be re-resolved
--
-- Default dataset: DB-IP IP-to-Country Lite (CC BY 4.0, monthly, no account).
-- Attribution: "IP Geolocation by DB-IP" <https://db-ip.com>. The file is not
-- vendored in the repo -- it is a few MB and changes every month, so the loader
-- fetches it (or takes -source pointing at a local copy). Re-run it monthly.

-- Non-overlapping [start_ip, end_ip] ranges, IPv4 and IPv6 in one table
-- (Postgres inet sorts all IPv4 below all IPv6, so one index serves both).
-- Keyed on start_ip because that is what makes the lookup one index descent:
-- the containing range is the LAST range starting at or below the address.
CREATE TABLE IF NOT EXISTS ip_country_ranges (
  start_ip INET NOT NULL,
  end_ip   INET NOT NULL,
  country  TEXT NOT NULL,   -- ISO-3166 alpha-2
  PRIMARY KEY (start_ip)
);

-- Per-country daily traffic, split by class like every other analytics table.
-- 'XX' means the address was not in the range table; the row is kept rather
-- than dropped so country totals reconcile with the site totals.
CREATE TABLE IF NOT EXISTS site_geo_daily (
  site_id  UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  day      DATE NOT NULL,   -- UTC
  country  TEXT NOT NULL,
  class    TEXT NOT NULL,   -- 'person' | 'bot' | 'infra'
  views    BIGINT NOT NULL DEFAULT 0,
  visitors BIGINT NOT NULL DEFAULT 0,   -- distinct ip_hash for that day
  PRIMARY KEY (site_id, day, country, class)
);
CREATE INDEX IF NOT EXISTS site_geo_daily_day_idx ON site_geo_daily (day);

-- The country of each distinct visitor, so uniques can be counted per country
-- the same way they are counted everywhere else (COUNT(DISTINCT ip_hash) over
-- the window asked for). Without it a 30-day figure would be the sum of daily
-- uniques, which double-counts everyone who came back.
--
-- Existing rows keep 'XX' until `analytics-rebuild` replays the log.
ALTER TABLE site_visitor_hourly
  ADD COLUMN IF NOT EXISTS country TEXT NOT NULL DEFAULT 'XX';

-- App DB role needs access (tables above may be created as a superuser).
GRANT ALL ON ip_country_ranges, site_geo_daily TO simplehost;
