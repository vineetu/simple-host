-- Bind sign-out confirmation to its form and the browser that opened it.
ALTER TABLE sso_signouts ADD COLUMN IF NOT EXISTS form_hash TEXT;
ALTER TABLE sso_signouts ADD COLUMN IF NOT EXISTS form_cookie_hash TEXT;
