-- Event hostnames handed to a hackathon organiser, pointing at THEIR server.
--
-- Rows exist so teardown knows which records to delete. A record left pointing
-- at a released cloud address is a subdomain takeover, so expires_at gives a
-- sweep something to act on when an organiser never tears down.
CREATE TABLE IF NOT EXISTS event_domains (
  name         TEXT NOT NULL,
  domain       TEXT NOT NULL,
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  ip           TEXT NOT NULL,
  record_ids   TEXT[] NOT NULL DEFAULT '{}',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at   TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (name, domain)
);
CREATE INDEX IF NOT EXISTS event_domains_expiry_idx ON event_domains (expires_at);
CREATE INDEX IF NOT EXISTS event_domains_user_idx ON event_domains (user_id);

