-- Change sign-in email (POST /v1/me/email, /v1/me/email/verify) and sign-in
-- alert emails (users.signin_alerts, PATCH /v1/me {"signin_alerts": false}).
--
-- APPLY AS THE ROLE IN DB_DSN (the role that owns users), before the new
-- binary starts (VerifySchema refuses to boot without these). Additive and
-- safe while the previous binary runs: a constant-default column is a catalog
-- change only, and the two tables are new. Safe to re-run.
BEGIN;

ALTER TABLE users ADD COLUMN IF NOT EXISTS signin_alerts BOOLEAN NOT NULL DEFAULT TRUE;

-- One pending sign-in email change per account: the code sent to the new
-- address, as a SHA-256 hash.
CREATE TABLE IF NOT EXISTS email_changes (
  user_id    UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  new_email  TEXT NOT NULL,
  code_hash  TEXT NOT NULL,
  attempts   INT NOT NULL DEFAULT 0,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Sign-in alerts already sent: at most one per account, browser/app summary
-- and UTC day. Rows older than a couple of days are pruned on the next alert.
CREATE TABLE IF NOT EXISTS signin_alerts_sent (
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  summary TEXT NOT NULL,
  day     DATE NOT NULL,
  PRIMARY KEY (user_id, summary, day)
);

COMMIT;
