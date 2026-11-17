-- Sign-ups by source (2026-10-15): the outside link credited with a sign-up,
-- as remembered by the first-party arrival cookie for up to 30 days: the
-- /?from= value, the utm_* parameters, the linking site's host and when the
-- browser landed. NULL when the account is not credited to one. Never an IP.
ALTER TABLE users ADD COLUMN IF NOT EXISTS signup_detail JSONB;
