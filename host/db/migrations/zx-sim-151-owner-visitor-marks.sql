-- Owner visit marks (SIM-151, 2026-11-02): the addresses an account was seen
-- signed in from, so the ingester can file the owner's own visits as class
-- 'owner' and leave them out of People. Same ip_hash as site_visitor_hourly.
CREATE TABLE IF NOT EXISTS owner_visitor_marks (
  user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  ip_hash   BYTEA NOT NULL,            -- same salt and truncation as site_visitor_hourly.ip_hash
  last_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, ip_hash)
);
