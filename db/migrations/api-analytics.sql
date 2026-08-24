-- Per-endpoint API traffic analytics for the admin page (additive; safe to re-run).
-- Go middleware → in-memory aggregates → 20s flush into these daily tables.
-- Raw caller IPs are kept deliberately (abuse tracking) but pruned after 30 days.

CREATE TABLE IF NOT EXISTS api_request_daily (
  day    DATE NOT NULL,
  route  TEXT NOT NULL,      -- normalized: "METHOD /v1/pattern", bounded cardinality
  status SMALLINT NOT NULL,
  calls  BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (day, route, status)
);

CREATE TABLE IF NOT EXISTS api_ip_daily (
  day        DATE NOT NULL,
  ip         TEXT NOT NULL,
  calls      BIGINT NOT NULL DEFAULT 0,
  last_route TEXT NOT NULL DEFAULT '',
  last_seen  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (day, ip)
);

-- One-time geo cache per IP, resolved lazily in the background (ip-api.com).
CREATE TABLE IF NOT EXISTS ip_geo (
  ip          TEXT PRIMARY KEY,
  country     TEXT NOT NULL DEFAULT '',
  city        TEXT NOT NULL DEFAULT '',
  org         TEXT NOT NULL DEFAULT '',
  resolved_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- App DB role needs access (tables above may be created as a superuser).
GRANT ALL ON api_request_daily, api_ip_daily, ip_geo TO simplehost;
