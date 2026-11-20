-- Site editors (TM-S2): a person may publish, update and roll back one site
-- of an account (a person's or a team's) without belonging to it, and is that
-- site's owner for its saved data. Keyed by the site's name, so an editor may
-- create the site, and the grant survives a restore from Recently deleted;
-- renaming the site moves its editors and removing it for good drops them.
--
-- Additive and idempotent: one new table. Safe while the previous program is
-- running: it never reads it.

CREATE TABLE IF NOT EXISTS site_editors (
  account_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  site_name  TEXT NOT NULL,
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  added_by   UUID REFERENCES users(id) ON DELETE SET NULL,
  added_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (account_id, site_name, user_id)
);
CREATE INDEX IF NOT EXISTS site_editors_user_idx ON site_editors (user_id);
