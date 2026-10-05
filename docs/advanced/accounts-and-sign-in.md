# Accounts and sign-in

**Owners** (people who publish) sign in with an emailed six-digit code or with Google. There are
no passwords. Signing in gives the browser or the agent an API key; an owner can name, list and
revoke keys, and "Sign out everywhere" replaces every key and disconnects every AI app. Each
new sign-in sends an alert email (owners can turn it off). A sign-in email change is confirmed
from both addresses, and the old address gets an undo link. A key can be deploy-only (for CI:
create, update, roll back and list sites), may carry a fixed expiry, and stops working after
`KEY_IDLE_EXPIRY_DAYS` unused.

**AI apps** connect over the connector at `/mcp` with OAuth: the person signs in once in the
window the app opens, and the app keeps a short access token it refreshes.

**Visitors** to a site on its own address can sign in with Google or an emailed code, so what
they save is theirs and private lists stay private. One sign-in covers one site.

Email codes need [email](email.md) set up; Google needs an OAuth client whose redirect URI is
`<PUBLIC_BASE_URL>/v1/auth/oauth/google/callback`. With neither, only the admin key signs in.

<!-- settings:group=accounts -->
| Setting | Default | Allowed | What it does |
|---|---|---|---|
| `SIGNIN_CODE_TTL_MINUTES` | `15` | 5–60 minutes | How long an emailed sign-in code (and link) or email-change code works. **Security-sensitive.** |
| `MAX_KEYS_PER_ACCOUNT` | `50` | 1–1000 keys | API keys one account may create from the Keys panel. |
| `KEY_IDLE_EXPIRY_DAYS` | `180` | 0–3650 days; `0` = never | An API key unused this long stops working (counted from its last use, or its creation). 0: keys work until revoked. **Security-sensitive.** |
| `HANDLE_RENAME_EVERY_DAYS` | `30` | 7–365 days | Once something is published, how often an account may change its handle. |
| `EMAIL_CHANGE_UNDO_DAYS` | `7` | 1–90 days | How long the undo link sent to the old address after a sign-in email change works. **Security-sensitive.** |
| `SHOWCASE_BIO_MAX_LENGTH` | `280` | 1–2000 characters | Maximum plain-text bio length on the public personal showcase, in characters. |
| `VISITOR_SESSION_DAYS` | `30` | 1–365 days | How long a visitor stays signed in on a site's own address, however active. **Security-sensitive.** |
| `VISITOR_SESSION_IDLE_DAYS` | `14` | 1–365 days | How long a visitor sign-in lasts unused. Not longer than VISITOR_SESSION_DAYS. **Security-sensitive.** |
| `OAUTH_ACCESS_TTL_MINUTES` | `60` | 5–1440 minutes | How long an AI app's access token works before it refreshes. **Security-sensitive.** |
| `OAUTH_REFRESH_TTL_DAYS` | `90` | 1–365 days | How long an AI app stays connected without being used. **Security-sensitive.** |
| `OAUTH_UNUSED_CLIENT_DAYS` | `30` | 1–365 days | An AI app registration that never connected, or went unused, is removed after this many days. |
| `RATE_LIMIT_SIGNIN_IP` | `20,5s` | stricter freely; loosest `80,1.25s` | Sign-in and email-change requests per address. **Security-sensitive.** |
| `RATE_LIMIT_SIGNIN_EMAIL` | `5,50s` | stricter freely; loosest `20,12.5s` | Sign-in codes sent to one email address. **Security-sensitive.** |
| `RATE_LIMIT_VISITOR_OAUTH` | `20,5s` | stricter freely; loosest `80,1.25s` | Visitor Google sign-ins per address. **Security-sensitive.** |
| `RATE_LIMIT_VISITOR_AUTH` | `20,5s` | stricter freely; loosest `80,1.25s` | Visitor email-code sign-ins per address. **Security-sensitive.** |
| `RATE_LIMIT_VISITOR` | `20,5s` | stricter freely; loosest `80,1.25s` | Finishing a visitor sign-in and signing out, per address. **Security-sensitive.** |
| `RATE_LIMIT_HANDLE_CHECK` | `30,2s` | any (warns past 10× looser) | Address availability checks while someone types a handle (GET /v1/handles/check), per address. |
| `RATE_LIMIT_OAUTH_REGISTER` | `10,6m` | stricter freely; loosest `40,1m30s` | AI app registrations on the connector, per address. **Security-sensitive.** |
| `RATE_LIMIT_OAUTH_AUTHORIZE` | `30,2s` | stricter freely; loosest `120,500ms` | AI app sign-in requests on the connector, per address. **Security-sensitive.** |
| `RATE_LIMIT_OAUTH_TOKEN` | `30,2s` | stricter freely; loosest `120,500ms` | AI app token requests on the connector, per address. **Security-sensitive.** |
| `ADMIN_API_KEY` | none | secret | The admin's key. install.sh generates it and shows it once. **Required.** **Security-sensitive.** |
| `GOOGLE_OAUTH_CLIENT_ID` | none | text | Google sign-in for owners and visitors: the OAuth client ID. Set with the secret, or neither. |
| `GOOGLE_OAUTH_CLIENT_SECRET` | none | secret | Google sign-in: the OAuth client secret. **Security-sensitive.** |
| `GITHUB_OAUTH_CLIENT_ID` | none | text | An optional second visitor sign-in provider: its OAuth client ID. Set with the secret, or neither. |
| `GITHUB_OAUTH_CLIENT_SECRET` | none | secret | The second provider's OAuth client secret. **Security-sensitive.** |
| `REVIEW_ACCOUNT_EMAIL` | none | text | A plugin reviewer's account that may sign in with a password on the connector. Set with the hash, or neither. |
| `REVIEW_ACCOUNT_PASSWORD_HASH` | none | secret | That reviewer account's password hash. **Security-sensitive.** |
| `SIGNUP_BLOCKED_COUNTRIES` | none | text | ISO-3166 alpha-2 country codes (comma-separated) that may not sign in or sign up (email-code or OAuth), resolved locally (GEOIP_DIR), never sent to a third party. An API key keeps working from anywhere (that is not a sign-in); an unresolved country is always allowed. Empty: off. |
<!-- /settings -->

The sign-in rate limits can be made stricter freely, but at most four times looser than the
default; anything looser stops the server at startup.

## Recipes

**Stricter sign-in.**

```
SIGNIN_CODE_TTL_MINUTES=5
RATE_LIMIT_SIGNIN_IP=10,10s
RATE_LIMIT_SIGNIN_EMAIL=3,2m
VISITOR_SESSION_DAYS=7
VISITOR_SESSION_IDLE_DAYS=2
OAUTH_REFRESH_TTL_DAYS=30
```

**Google sign-in only on a small box.** Create an OAuth client (type "Web application") with the
redirect URI above, set `GOOGLE_OAUTH_CLIENT_ID` and `GOOGLE_OAUTH_CLIENT_SECRET`, and leave
`RESEND_API_KEY` empty.
