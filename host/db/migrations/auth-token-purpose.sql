-- 2026-09-05: bind email sign-in codes to their purpose and, for hosted-page
-- (visitor) sign-in, to one site. Before this, a code emailed for a guestbook
-- sign-in on site A was the same token the dashboard's /v1/auth/verify
-- accepted, so a phished visitor code yielded the person's full API key, and
-- it could be verified on any site. Review finding (Codex + Grok), same day.
-- Additive and safe to apply before deploying the binary that uses it:
-- existing rows and the old binary both mean 'dashboard'.
ALTER TABLE auth_tokens
  ADD COLUMN IF NOT EXISTS purpose TEXT NOT NULL DEFAULT 'dashboard',
  ADD COLUMN IF NOT EXISTS site_id UUID REFERENCES sites(id) ON DELETE CASCADE;
ALTER TABLE auth_tokens DROP CONSTRAINT IF EXISTS auth_tokens_purpose_check;
ALTER TABLE auth_tokens
  ADD CONSTRAINT auth_tokens_purpose_check
  CHECK (purpose IN ('dashboard', 'visitor') AND ((purpose = 'visitor') = (site_id IS NOT NULL)));
CREATE INDEX IF NOT EXISTS auth_tokens_email_purpose_idx ON auth_tokens (email, purpose, site_id, created_at DESC);
