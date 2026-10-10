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
