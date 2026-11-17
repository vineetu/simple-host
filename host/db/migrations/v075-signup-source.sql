-- v0.7.5 (2026-09-29): where each account came from, for the admin page.
--
-- signup_source is set once, when the account is created, and never changed:
--   website            email code, sign-in link or Google from simple-host.app's own pages
--   connector:<name>   created on the /mcp OAuth consent page (ChatGPT, Claude, Grok, or
--                      the name a dynamic client registered with)
--   agent              POST /v1/auth/verify from outside the site's pages; signup_agent
--                      names it (X-Simple-Host-Client, else "skill <X-Skill-Version>",
--                      else the User-Agent's product token such as "curl")
--   visitor            first signed in on a hosted page (visitor sign-in)
--   admin              issued by the operator (POST /v1/admin/users)
--   reviewer           the app-store reviewer sign-in
-- signup_method is email | google | github | issued | password. No IP is ever stored.
-- Accounts from before this file stay NULL ("Unknown (before tracking)"), except
-- the obvious cases below, which are marked signup_inferred.
--
-- Apply BEFORE deploying the build that reads it (the server refuses to start
-- without it). Idempotent: nullable columns and a constant default are
-- catalog-only; each UPDATE touches only rows still NULL.
BEGIN;
-- Never queue behind a long lock on a live users table: fail fast and retry.
SET LOCAL lock_timeout = '5s';

ALTER TABLE users ADD COLUMN IF NOT EXISTS signup_source TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS signup_agent TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS signup_method TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS signup_inferred BOOLEAN NOT NULL DEFAULT FALSE;

-- Accounts an organiser issued.
UPDATE users SET signup_source = 'admin', signup_method = 'issued', signup_inferred = TRUE
 WHERE signup_source IS NULL AND NOT COALESCE(is_admin, FALSE) AND event_account;

-- An app connected within 60 minutes of the account being created: the
-- account was made on that app's consent page.
UPDATE users u
   SET signup_source = 'connector:' || left(g.client_name, 60),
       signup_method = CASE WHEN EXISTS (
         SELECT 1 FROM oauth_identities i
          WHERE i.user_id = u.id AND i.provider = 'google'
            AND i.created_at <= u.created_at + interval '10 minutes') THEN 'google' END,
       signup_inferred = TRUE
  FROM (SELECT DISTINCT ON (gr.user_id) gr.user_id, c.client_name
          FROM oauth_grants gr
          JOIN oauth_clients c ON c.client_id = gr.client_id
          JOIN users uu ON uu.id = gr.user_id
         WHERE gr.created_at <= uu.created_at + interval '60 minutes'
         ORDER BY gr.user_id, gr.created_at) g
 WHERE u.id = g.user_id AND u.signup_source IS NULL AND NOT COALESCE(u.is_admin, FALSE);

-- A Google identity linked within 10 minutes of the account: a Google sign-up
-- from the site.
UPDATE users u SET signup_source = 'website', signup_method = 'google', signup_inferred = TRUE
 WHERE u.signup_source IS NULL AND NOT COALESCE(u.is_admin, FALSE)
   AND EXISTS (SELECT 1 FROM oauth_identities i
                WHERE i.user_id = u.id AND i.provider = 'google'
                  AND i.created_at <= u.created_at + interval '10 minutes');

COMMIT;
