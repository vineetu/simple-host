-- w2-sites (2026-09-27): a renamed site keeps its old names. Links to an old
-- name (<old>.<handle>.<domain>, <handle>.<domain>/<old>/, sites.<domain>/<handle>/<old>/)
-- 302 to the site's current address until a site with that name exists again
-- (a new site always wins). One row per (account, old name); a rename chain
-- keeps every name pointing at the same site. Purging the site, or erasing the
-- account, removes its rows (cascade); a site in Recently deleted is skipped.
--
-- Apply BEFORE deploying the build that reads it (the server refuses to start
-- without it). Idempotent; a new empty table takes no lock on existing ones
-- beyond the brief foreign-key reference.
CREATE TABLE IF NOT EXISTS site_name_aliases (
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  site_id    UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, name)
);
CREATE INDEX IF NOT EXISTS idx_site_name_aliases_site ON site_name_aliases (site_id);
