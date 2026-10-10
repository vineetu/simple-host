-- Named viewers (2026-10-10): who can open a site. access 'anyone' (the
-- default: anyone with the address, or with the passcode when one is set) or
-- 'specific' (only the owner and the named viewers, signed in with visitor
-- sign-in on the site's own address). A site never has both a passcode and
-- named viewers. The `passcode` marker file next to `current` marks either
-- gate for the servers that read files straight from disk.
ALTER TABLE sites ADD COLUMN IF NOT EXISTS access TEXT NOT NULL DEFAULT 'anyone';
CREATE TABLE IF NOT EXISTS site_viewers (
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  email TEXT NOT NULL,
  added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (site_id, email)
);
-- Who signed in on which site (2026-10-10), recorded by the server whenever a
-- visitor session starts: the owner's lookup from a record's visitor_id to an
-- email (handler/site_storage_visitors.go) answers only for these people,
-- never for an id an owner typed into their own tables. Backfilled from the
-- sessions still on record and from the KV and file writers the server
-- stamped.
CREATE TABLE IF NOT EXISTS site_visitor_signins (
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  first_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (site_id, user_id)
);
INSERT INTO site_visitor_signins (site_id, user_id, first_at)
  SELECT site_id, user_id, min(created_at) FROM visitor_sessions GROUP BY site_id, user_id
  ON CONFLICT DO NOTHING;
INSERT INTO site_visitor_signins (site_id, user_id)
  SELECT DISTINCT k.site_id, u.id FROM site_storage_kv k JOIN users u ON u.id::text = k.writer_id
  WHERE k.writer_id <> ''
  ON CONFLICT DO NOTHING;
INSERT INTO site_visitor_signins (site_id, user_id)
  SELECT DISTINCT f.site_id, u.id FROM site_storage_files f JOIN users u ON u.id::text = f.writer_id
  WHERE f.writer_id <> ''
  ON CONFLICT DO NOTHING;
