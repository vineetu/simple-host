-- v0.7.5 (2026-09-29): per-site deploy retention. keep_versions is how many
-- deploys of this site are kept: 0 = the instance setting (KEEP_VERSIONS),
-- N >= 1 = the newest N plus always the live one. The owner sets it with
-- PUT /v1/sites/{sitename}/keep-versions; every later deploy prunes to it.
--
-- Apply BEFORE deploying the build that reads it (the server refuses to start
-- without it). Idempotent; a NOT NULL column with a constant default is a
-- catalog-only change (no table rewrite).
ALTER TABLE sites ADD COLUMN IF NOT EXISTS keep_versions INTEGER NOT NULL DEFAULT 0;
