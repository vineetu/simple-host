-- Sign-in email change, hardened after review (2026-09-27): the change needs
-- a code sent to the CURRENT address as well as the one sent to the new
-- address, and the notice to the old address carries a 7-day undo link.
--
-- APPLY AS THE ROLE IN DB_DSN, after w2-account-signin-email.sql and before
-- the new binary starts (VerifySchema refuses to boot without these).
-- Additive: a nullable column and a new table. Safe to re-run.
ALTER TABLE email_changes ADD COLUMN IF NOT EXISTS old_code_hash TEXT;

-- One row per applied change: the undo link's token as a SHA-256 hash, what
-- the address was and became. Used once, or expired after 7 days.
CREATE TABLE IF NOT EXISTS email_change_undos (
  token_hash TEXT PRIMARY KEY,
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  old_email  TEXT NOT NULL,
  new_email  TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL,
  used_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS email_change_undos_user_idx ON email_change_undos (user_id);
