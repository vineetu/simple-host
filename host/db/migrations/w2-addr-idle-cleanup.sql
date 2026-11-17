-- Idle-site cleanup (owner decision 2026-09-27; internal/handler/idle.go).
-- Additive columns with constant defaults: no table rewrite, no long lock.
--
-- idle_keep       the owner's Keep flag: never warned or removed
-- idle_kept_at    the last "Keep it" (resets the idle clock)
-- idle_warned_at  the owner was emailed that the site is idle
-- idle_removed_at the cleanup moved the site to Recently deleted
-- idle_token_hash SHA-256 of the token the emailed links carry
ALTER TABLE sites ADD COLUMN IF NOT EXISTS idle_keep BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS idle_kept_at TIMESTAMPTZ;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS idle_warned_at TIMESTAMPTZ;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS idle_removed_at TIMESTAMPTZ;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS idle_token_hash BYTEA;
