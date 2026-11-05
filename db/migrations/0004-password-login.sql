-- account_passwords gets its own normalized login column (trimmed,
-- lower-cased), separate from users.username. Self sign-up (password.go
-- CreatePasswordAccount) now gives users.username a synthetic, never-email
-- value instead, so none of the many places across the codebase that treat
-- users.username as a verified email (visitor/saver email resolution, Hack
-- voting identity, sign-in alert mail, Hack notice/announcement delivery,
-- named-viewer sign-in history) can be fooled by a self-chosen login that
-- looks like someone else's address. Admin-created password accounts are
-- unaffected: users.username still equals the login there, since the admin
-- vouches for that address.
--
-- Existing rows (created before this migration, when users.username WAS the
-- login for every password account) are backfilled from users.username so
-- nothing already signed up loses its login.
ALTER TABLE account_passwords ADD COLUMN IF NOT EXISTS login TEXT;
UPDATE account_passwords p SET login = lower(u.username)
  FROM users u WHERE p.user_id = u.id AND p.login IS NULL;
ALTER TABLE account_passwords ALTER COLUMN login SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS account_passwords_login_idx ON account_passwords (login);
