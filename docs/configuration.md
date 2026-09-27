# Configuration reference

Every setting, grouped by area and explained in product terms with recipes, is in
[docs/advanced/](advanced/README.md); the setup helper at https://simple-host.app/setup builds
a small box's `.env` from the same list ([docs/advanced/settings.json](advanced/settings.json),
generated from the code). This page is the full reference.

## Operational times and limits

Every operational time and limit Simple Host enforces can be changed per install with an
environment variable. Unset (or blank) means the value in the Default column, which is what
simple-host.app runs, so an install that sets none of them behaves exactly as before.

- **Where they are read.** Once, at startup, in `internal/config/limits.go` (`Knobs()` is the
  list). A value that is not a whole number, is out of range, or does not fit with another
  setting stops the server with an error naming the variable, rather than running with a value
  nobody chose. Every knob that differs from its default is printed once in the startup log
  (`limits changed from the defaults: ...`).
- **Where to set them.** simple-host.app: `/etc/simple-host.env`, then restart. Docker Compose
  and `deploy/install/install.sh` boxes: `/opt/simple-host/.env` (`.env.example` lists them
  commented out; `compose.yaml` passes each one through; the installer keeps them on a re-run),
  then `docker compose up -d`.
- **What they change.** The enforcement and every sentence that states the value: emails, the
  owner app, the landing and feature pages, the privacy page, the admin page, `llms.txt`, the
  OpenAPI spec, the skills served from this install, and the connector's MCP instructions,
  tool descriptions and results. Served pages and documents are written with the defaults and
  rewritten when served (`internal/handler/limitstext.go`, the same way the hostnames are
  rewritten for an instance on another domain); with every knob at its default nothing is
  rewritten. The skills published to the Claude and ChatGPT plugin directories describe
  simple-host.app and keep its values.
- **Units.** The unit is in the name (`_MINUTES`, `_HOURS`, `_DAYS`); counts are plain whole
  numbers. A rate limit is `<burst>,<every>`: that many requests at once, then one more every
  `<every>` (a Go duration: `500ms`, `5s`, `6m`, `1h`). For example `RATE_LIMIT_SIGNIN_IP=20,5s`
  allows 20 sign-in requests from one address at once, then one every 5 seconds. Burst 1 to
  100,000; `<every>` 1ms to 24h, except the security-sensitive limits (see Rate limits).
- **Changes apply to new deletions and warnings.** The date a person was promised is stored
  when the promise is made: a deleted site keeps its purge date (`DELETED_RETENTION_DAYS`), an
  idle-warned site its removal date (`IDLE_GRACE_DAYS`), a failing domain whose owner was
  emailed its disconnect date (`DOMAIN_LAPSE_HOURS`). Shortening one never removes anything
  earlier than its email said; lengthening one does not extend what was already promised.
  Likewise, tightened token and session lifetimes (`OAUTH_*_TTL_*`, `VISITOR_SESSION_*`,
  `PREVIEW_LINK_TTL_MINUTES`, `EXPORT_LINK_TTL_MINUTES`, `SIGNIN_CODE_TTL_MINUTES`) don't
  shorten ones already issued: each carries the expiry it was issued with.
- **Skills and cached copies.** The skills served from this install state its limits, but their
  advertised version (`/skills/version`) follows the skills' text, not the settings, so an
  agent that already installed them keeps the old numbers until it reinstalls. Reinstall the
  skills (or tell people to) after changing a limit they state.

## Accounts and keys

| Variable | Default | Range | What it controls |
|---|---|---|---|
| `SIGNIN_CODE_TTL_MINUTES` | 15 | 5–60 | How long an emailed sign-in code (and link) or email-change code works. |
| `MAX_KEYS_PER_ACCOUNT` | 50 | 1–1000 | API keys one account may create from the Keys panel (`POST /v1/me/keys`, 409 `key_limit`). Sign-in keys are not refused. |
| `HANDLE_RENAME_EVERY_DAYS` | 30 | 7–365 | Once something is published, an account may change its handle once in this many days (429 with `next_change_after`). |
| `EMAIL_CHANGE_UNDO_DAYS` | 7 | 1–90 | How long the undo link sent to the old address after a sign-in email change works. |

## Sites

| Variable | Default | Range | What it controls |
|---|---|---|---|
| `MAX_SITES_PER_ACCOUNT` | 100 | 1–100000 | Sites one non-admin account may hold (sites in Recently deleted count). 403 `site_quota_reached`. |
| `MAX_FILES_PER_SITE` | 50000 | 100–50000 | Files in one upload (it can only be lowered: the upload pipeline and disk are sized for 50,000). When `MAX_ARCHIVE_MB` is set, the smaller of this and one file per 4 KB of that budget applies. |
| `PREVIEW_LINK_TTL_MINUTES` | 60 | 5–10080 | How long a share-preview link to a stored version works. |
| `EXPORT_LINK_TTL_MINUTES` | 10 | 1–60 | How long a site-export download link works. At most an hour: the link is not single-use and the archive holds private collections. |

## Visitors signed in on a site's own address

| Variable | Default | Range | What it controls |
|---|---|---|---|
| `VISITOR_SESSION_DAYS` | 30 | 1–365 | A visitor sign-in's absolute lifetime, and the session cookie's max age. |
| `VISITOR_SESSION_IDLE_DAYS` | 14 | 1–365 | How long a visitor sign-in lasts unused; each use slides it, never past the absolute lifetime. Must not be longer than `VISITOR_SESSION_DAYS`. |

## Connector (AI apps over /mcp)

| Variable | Default | Range | What it controls |
|---|---|---|---|
| `OAUTH_ACCESS_TTL_MINUTES` | 60 | 5–1440 | Access token lifetime. |
| `OAUTH_REFRESH_TTL_DAYS` | 90 | 1–365 | Refresh token lifetime (each refresh issues a new one). |
| `OAUTH_UNUSED_CLIENT_DAYS` | 30 | 1–365 | A dynamically registered app that never connected, or has not been used, is deleted after this many days. |

## Domains and certificates

| Variable | Default | Range | What it controls |
|---|---|---|---|
| `DOMAIN_UNPROVEN_HOURS` | 24 | 1–720 | A bound domain whose DNS never points here is released after this long. |
| `DOMAIN_UNPROVEN_MAX_DAYS` | 7 | 1–90 | A bound domain whose DNS points here but that never goes live (certificate or HTTPS failing) is released after this long. Must not be shorter than `DOMAIN_UNPROVEN_HOURS`. |
| `DOMAIN_LAPSE_WARN_HOURS` | 24 | 1–720 | A verified domain failing every check: its owner is emailed after this long. |
| `DOMAIN_LAPSE_HOURS` | 72 | 2–2160 | ... and it stops being the site's address after this long. Must be longer than `DOMAIN_LAPSE_WARN_HOURS`. Once the owner was emailed, the date in that email holds. |
| `DOMAIN_CHECK_INTERVAL_MINUTES` | 2 | 1–60 | How often the background domain check runs. |
| `DOMAIN_CERTS_PER_ACCOUNT_DAILY` | 5 | 1–1000 | New custom-domain certificates one account may ask for in a rolling day. |
| `EVENT_TTL_DAYS` | 21 | 1–60 | How long a claimed event hostname lives before the sweep removes it (re-claiming extends it). Only where `EVENT_DNS_TOKEN` is set. At most 60, so a forgotten claim does not point a name under this domain at a recycled cloud address for months. |
| `EVENT_MAX_CLAIMS` | 5 | 1–100 | Event hostnames one account may hold at once. |

## Cleanup and retention

| Variable | Default | Range | What it controls |
|---|---|---|---|
| `DELETED_RETENTION_DAYS` | 7 | 1–365 | How long a deleted site stays restorable in Recently deleted (its name stays held until then). Applies to sites deleted after the change. |
| `IDLE_AFTER_DAYS` | 90 | 7–3650 | Idle cleanup (`IDLE_CLEANUP=on`): a site with no visits, deploys or saves for this long gets its owner a warning email. |
| `IDLE_GRACE_DAYS` | 30 | 1–365 | ... and moves to Recently deleted this long after the warning if nothing is done. Applies to warnings sent after the change. |
| `IDLE_REPLY_TO` | support@simple-host.app | an email address | Reply-To of the idle-cleanup emails. |
| `ANALYTICS_RETENTION_DAYS` | 400 | 1–3650 | How long visit analytics aggregates are kept. |
| `API_METRICS_RETENTION_DAYS` | 30 | 1–3650 | How long the admin API-call counts and shortened caller IPs are kept. |

## AI create

| Variable | Default | Range | What it controls |
|---|---|---|---|
| `AI_MAX_JOBS_PER_USER` | 3 | 1–100 | Builds one person may have running at once. Must not be more than `AI_MAX_JOBS`. |
| `AI_MAX_JOBS` | 64 | 1–1000 | Builds running at once on the whole install. |
| `AI_JOB_TIMEOUT_MINUTES` | 8 | 1–8 | How long one build may run. At most 8: the builder page waits 9 minutes for an answer. |

## Saved data

What pages save (page data and lists): how long changes can be undone, the size limits, the
limits on reads and list additions, and the saved-data watch on the admin page. Unlike the
promised dates above, `SAVED_DATA_UNDO_DAYS` applies to what is already kept: shortening it
removes older history and Recently deleted items at the next sweep.

| Variable | Default | Range | What it controls |
|---|---|---|---|
| `SAVED_DATA_UNDO_DAYS` | 30 | 1–365 | Days every change to saved data, and every deleted list item, can be restored by the site owner. |
| `SAVED_DATA_HISTORY_MAX_MB` | 20 | 1–10240 | A site's history above this is thinned, oldest first, keeping each item's first change of every day: at once when a write crosses it (at most once a second per site), and by the sweep. |
| `SAVED_DATA_SITE_MAX_MB` | 50 | 1–10240 | A site's live saved data (page data and list items; not history or Recently deleted). A write that would grow it past this is refused (507 `site_full`); writes that do not grow it always go through. |
| `SAVED_DATA_SNAPSHOT_EVERY` | 50 | 1–10000 | A change made with ops keeps only what it changed in history, with a full copy at least this often and on the first change of each day. |
| `SAVED_DATA_SWEEP_MINUTES` | 15 | 1–1440 | How often expired history and deleted items are removed. |
| `SAVED_DATA_WATCH_DAYS` | 7 | 1–365 | The window the saved-data watch (`GET /v1/admin/data-watch`) reports. |
| `SAVED_DATA_WATCH_INC_MAX` | 10 | 1–1000000000 | A visitor increment larger than this is counted as large by the watch. |
| `SAVED_DATA_WATCH_ITEM_KB` | 16 | 1–64 | A list item larger than this is counted as large by the watch. |
| `SAVED_DATA_WATCH_KEEP_DAYS` | 90 | 1–3650 | Watch counts older than this are removed. Must not be shorter than `SAVED_DATA_WATCH_DAYS`. |
| `SAVED_DATA_IDEMPOTENCY_HOURS` | 24 | 1–720 | How long a write's first answer is replayed for a retry with the same `Idempotency-Key`. |
| `SAVED_DATA_IDEMPOTENCY_MAX_PER_SITE` | 10000 | 100–1000000 | Remembered `Idempotency-Key`s per site; the oldest past this are dropped. |
| `SAVED_DATA_READ_PER_SEC` | 30 | 1–10000 | Saved-data reads per second per site and address (per account for owner keys and the connector). |
| `SAVED_DATA_READ_BURST` | 60 | 1–100000 | Reads allowed at once above that rate. |
| `SAVED_DATA_APPEND_PER_MIN` | 30 | 1–10000 | List items one address may add per minute without the owner's key. |
| `SAVED_DATA_APPEND_BURST` | 30 | 1–100000 | Items allowed at once above that rate. |
| `SAVED_DATA_CONTENT_MAX_KB` | 1024 | 1–10240 | One Page info document (a name declared `content`). Larger saves are refused (413 `item_too_large`). |
| `SAVED_DATA_CONTENT_NAMES_MAX` | 20 | 1–1000 | Page info names one site may declare (409 `too_many_names`). |
| `SAVED_DATA_ENTRY_MAX_KB` | 16 | 1–64 | One new Submissions entry (a name declared `entries`). Older items up to 64 KB are kept. |
| `SAVED_DATA_ENTRIES_MAX` | 10000 | 1–1000000 | Live entries in one Submissions name (409 `list_full`). |
| `SAVED_DATA_WITHDRAW_UNDO_MINUTES` | 10 | 1–1440 | How long a visitor can bring back a Submissions entry they withdrew. |
| `SAVED_DATA_NOTIFY_EACH_MINUTES` | 10 | 1–1440 | "Email me: each" sends at most one email per name this often, counting what arrived since the last. Also how often due emails are checked. |
| `SAVED_DATA_NOTIFY_DAILY_HOURS` | 24 | 1–720 | "Email me: daily" sends at most one digest per name this often. |
| `SAVED_DATA_SAVERS_MAX` | 500 | 1–100000 | Emails and domains in one site's who-may-save and block lists together. |
| `SAVED_DATA_ENTRIES_NAMES_MAX` | 50 | 1–1000 | Submissions names one site may declare (409 `too_many_names`). |
| `SAVED_DATA_DEFAULT_KIND` | shared | shared, declare_first | What a data name is when the site owner never declared it. `shared` (Shared): anyone who can open the site reads it and signed-in visitors save to it, as before the kinds, so older skills, AI create and uploaded pages keep working. `declare_first`: such a name takes no saves (409 `declare_first`) until the owner declares it Page info or Submissions. Sites that existed before the kinds (migration `sd2-saved-data-kinds.sql`) stay Shared either way; every later site follows this setting as it is now, so switching it changes them all. |

## Ask assistants

The two "Ask" assistants on the public pages (`POST /v1/ask`): Simple Host on the features
and architecture pages, Simple Host Enterprise on the three enterprise pages. It runs only when a model backend is set (`LLM_API_KEY`, `LLM_BASE_URL`);
without one the box is not shown, whatever these say. `ASK_ENABLED` is `on` or `off` (also
`true`/`false`, `1`/`0`, `yes`/`no`); anything else stops the server at startup. Answers are
streamed as they are written; the first words must arrive within 20 seconds and the whole
answer within 45, or the reader is told it couldn't answer.

| Variable | Default | Range | What it controls |
|---|---|---|---|
| `ASK_ENABLED` | on | on / off | Whether the box is shown and `/v1/ask` answers. |
| `ASK_BURST` | 5 | 1–50 | Questions one address may ask at once. |
| `ASK_EVERY_SECONDS` | 20 | 1–3600 | Then one more question every this many seconds, per address. |
| `ASK_DAILY_MAX` | 500 | 0–100000 | Questions answered per UTC day across everyone (counted in the database, so a restart keeps the count). 0 answers none. |
| `ASK_MAX_IN_FLIGHT` | 4 | 1–32 | Questions answered at once on the whole install. |
| `ASK_MODEL` | grok-4.7 | a model name | The model the box asks, through the same backend (`LLM_BASE_URL`). Separate from `LLM_MODEL`, which AI create keeps. |
| `ASK_REASONING_EFFORT` | none | none / low / medium / high | Sent to the model as `reasoning_effort`. `none` answers in seconds; higher values think first and answer later. |
| `ASK_MAX_TOKENS` | 300 | 50–4000 | Longest answer, in tokens. A reply cut here ends with "…". |
| `SETUP_CHECK_DAILY_MAX` | 200 | 0–100000 | The setup helper's optional "Check my choices" (`POST /v1/setup/check`): checks answered per UTC day across everyone (counted in the database, table `setup_check_daily`). It runs only where the box does (a model backend and `ASK_ENABLED` on) and uses the same model, reasoning effort, per-address limits and in-flight cap as the box. 0 turns it off; the helper then shows its files without it. |

## Rate limits

Each is `<burst>,<every>` (see Units above). Keys are per client address unless noted.

**Security-sensitive** limits (sign-in and email codes, visitor sign-in, the connector's OAuth
register, authorize and token endpoints) can be made stricter freely but at most 4 times looser
than the default: a burst at most 4 times the default and an `<every>` at least a quarter of it.
Anything looser stops the server at startup with a message naming the ceiling; the Loosest column
gives it. The others keep the wide range, and the startup log prints a `WARNING` for any set more
than 10 times looser than its default. A `RATE_LIMIT_*` variable that is not one of these names
(a typo) is ignored, with a `WARNING` naming it at startup.

| Variable | Default | Loosest | What it limits |
|---|---|---|---|
| `RATE_LIMIT_SIGNIN_IP` | 20,5s | **80,1.25s** (security-sensitive) | Sign-in and email-change requests per address. |
| `RATE_LIMIT_SIGNIN_EMAIL` | 5,50s | **20,12.5s** (security-sensitive) | Sign-in codes aimed at one email address. |
| `RATE_LIMIT_VISITOR_OAUTH` | 20,5s | **80,1.25s** (security-sensitive) | Visitor Google sign-in starts and callbacks per address. |
| `RATE_LIMIT_VISITOR_AUTH` | 20,5s | **80,1.25s** (security-sensitive) | Visitor email-code sign-in per address. |
| `RATE_LIMIT_VISITOR` | 20,5s | **80,1.25s** (security-sensitive) | Finishing a visitor sign-in and signing out, per address. |
| `RATE_LIMIT_UPLOAD` | 30,10s | any (warns past 10×) | Uploads and deploys per client. |
| `RATE_LIMIT_STATE` | 60,1s | any (warns past 10×) | Saved-data and list writes per client. |
| `RATE_LIMIT_SITE_OPS` | 30,2s | any (warns past 10×) | Deleting, changing and restoring sites, and deleting the account, per address. |
| `RATE_LIMIT_EXPORT` | 10,10s | any (warns past 10×) | Export downloads per address. |
| `RATE_LIMIT_DOMAIN_CHECK` | 10,10s | any (warns past 10×) | "Check again" on a domain, per address. |
| `RATE_LIMIT_DOMAIN_CHECK_USER` | 3,30s | any (warns past 10×) | "Check again" on a domain, per account. |
| `RATE_LIMIT_OAUTH_REGISTER` | 10,6m | **40,1m30s** (security-sensitive) | Connector app registrations per address. |
| `RATE_LIMIT_OAUTH_AUTHORIZE` | 30,2s | **120,500ms** (security-sensitive) | Connector authorization requests per address. |
| `RATE_LIMIT_OAUTH_TOKEN` | 30,2s | **120,500ms** (security-sensitive) | Connector token requests per address. |
| `RATE_LIMIT_AI_IP` | 20,12s | any (warns past 10×) | AI create requests per address. |
| `RATE_LIMIT_AI_USER` | 30,10s | any (warns past 10×) | AI create requests per account. |
| `RATE_LIMIT_TRANSCRIBE` | 60,3s | any (warns past 10×) | Voice input, per address and per account (each). |

## Server, sign-in, email and the other settings

These are read by `internal/config/config.go` (and `MAX_ARCHIVE_MB`, `KEEP_VERSIONS` by the
handler) rather than `Knobs()`. `DB_DSN` and `ADMIN_API_KEY` are required; without
`SITE_DOMAIN` the server starts in setup mode. A value in `<angle brackets>` is derived from
another setting.

| Variable | Default | What it controls |
|---|---|---|
| `SITE_DOMAIN` | none (setup mode) | The domain this server lives at. |
| `CONTENT_HOST` | `sites.<SITE_DOMAIN>` | The separate hostname sites are served from. |
| `PUBLIC_BASE_URL` | `https://simple-host.app` | The server's own address, used in emails and sign-in redirects. |
| `ADMIN_API_KEY` | none (required) | The admin's key; the admin is a real account upserted from it at boot. |
| `DB_DSN` | none (required) | Postgres connection string. |
| `DATA_DIR` | `./data/sites` | Where site files and versions live. |
| `PORT` | `8090` | Listen port. |
| `BIND_ADDR` | none (all interfaces) | Listen interface, e.g. `127.0.0.1` behind nginx. |
| `DEPLOY_SCRIPT` | none | A script run after a site goes live. |
| `CNAME_TARGET` | `cname.<SITE_DOMAIN>` | The CNAME target for custom domains. |
| `CUSTOM_DOMAIN_IP` | none | The A record handed out for a bare custom domain. |
| `PERSON_HOSTS` | `off` | `off`, `serve` or `canonical`: `<handle>.<SITE_DOMAIN>` per account. |
| `SITE_HOSTS` | `off` | `off`, `serve` or `canonical`: `<site>.<handle>.<SITE_DOMAIN>` per site (needs `PERSON_HOSTS`). |
| `SITE_CERT_DIR` | none | Per-person certificate hand-off with the root issuer (`deploy/site-certs/`). |
| `DOMAIN_CERT_DIR` | none | Custom-domain certificate hand-off with the root issuer (`deploy/domain-certs/`). |
| `SETUP_PASSWORD` | none | The password a box in setup mode asks for (install.sh generates it). |
| `SETUP_PUBLIC_API` | `https://simple-host.app` | Where a box in setup mode claims a free hostname from. |
| `OPENAI_APPS_CHALLENGE` | none | The OpenAI plugin portal's domain-verification token. |
| `GOOGLE_OAUTH_CLIENT_ID` | none | Google sign-in for owners and visitors (with the secret; exactly one of the pair is treated as off). Redirect URI: `<PUBLIC_BASE_URL>/v1/auth/oauth/google/callback`. |
| `GOOGLE_OAUTH_CLIENT_SECRET` | none | Google sign-in client secret. |
| `GITHUB_OAUTH_CLIENT_ID` | none | An optional second visitor sign-in provider (with the secret). |
| `GITHUB_OAUTH_CLIENT_SECRET` | none | Its client secret. |
| `REVIEW_ACCOUNT_EMAIL` | none | Plugin reviewer's password sign-in on the connector (with the hash; `internal/handler/reviewer.go`). |
| `REVIEW_ACCOUNT_PASSWORD_HASH` | none | Its password hash. |
| `MAX_ARCHIVE_MB` | 100 | Largest upload, and the uncompressed per-site cap derived from it. |
| `KEEP_VERSIONS` | 0 | Deploys kept per site; 0 keeps all (install.sh writes 1). |
| `PREVIEW_ACCOUNTS` | none | Accounts whose sites expire. |
| `PREVIEW_TTL_HOURS` | 48 | ... after this many hours. |
| `WRITE_AUTH_MODE` | `log` | `on`: page saves need a signed-in visitor or a key; `log`; `off`. |
| `EVENT_DNS_TOKEN` | none | Event hostnames (public instance only): the DNS token. |
| `EVENT_DNS_TEAM_ID` | none | Its DNS account. |
| `EVENT_DOMAINS` | none | Zones event hostnames are handed out under, comma-separated. |
| `IDLE_CLEANUP` | off | `on` turns the idle-site cleanup on. |
| `IDLE_CLEANUP_MAX_EMAILS` | 50 | Idle-cleanup emails per run. |
| `IDLE_CLEANUP_EXEMPT_HANDLES` | none | Accounts the idle cleanup never touches. |
| `RESEND_API_KEY` | none | Email via [Resend](https://resend.com); email sign-in is off without it. |
| `MAIL_FROM` | `Simple Host <noreply@simple-host.app>` | The sender of every email. |
| `LLM_PROVIDER` | `grok` | The model backend for AI create and Ask: `grok`, `xai`, `openai`, `deepseek`, `openrouter` or `custom`. |
| `LLM_API_KEY` | none | The backend's key; Ask and AI create run only with a backend set. |
| `LLM_BASE_URL` | none (the provider's) | The backend's address. |
| `LLM_MODEL` | none (the provider's) | The model AI create uses. |
| `VISION_PROVIDER` | `<LLM_PROVIDER>` | The backend that reads attached images in AI create. |
| `VISION_API_KEY` | none | Its key. |
| `VISION_BASE_URL` | none (the provider's) | Its address. |
| `VISION_MODEL` | none (the provider's) | Its model. |
| `TRANSCRIBE_URL` | none | A local speech-to-text service for voice input. |
| `TRANSCRIBE_TICKET_SECRET` | none | Shared with the speech service for live transcription. |
| `ANALYTICS_LOG` | none | The access log visit analytics are read from. |
| `ANALYTICS_SALT` | none (derived from `ADMIN_API_KEY`) | Salt for hashed visitor IPs in analytics. |
| `GEOIP_DIR` | `<DATA_DIR>/../geoip` | Where the local DB-IP Lite databases are. |

## Certificate issuers (root scripts)

The two root certificate issuers in `deploy/` are not part of the service's environment. Each
reads its own conf file, sourced as shell, which is where their limits are changed; the file is
optional and unset values keep the script's defaults.

`/etc/simple-host-domain-certs.conf` (custom domains, `deploy/domain-certs/issue.sh`):

| Variable | Default | What it controls |
|---|---|---|
| `DAILY` | 50 | New custom-domain certificates per rolling 24 hours, for the whole server. |
| `PER_RUN` | 10 | Certificates one run may request. |
| `RETRY_AFTER` | 21600 | Seconds before a failed domain is tried again. |

The per-account cap the app enforces (`DOMAIN_CERTS_PER_ACCOUNT_DAILY`) is separate from, and
normally well under, this server-wide `DAILY`.

`/etc/simple-host-site-certs.conf` (per-person `*.<handle>` addresses, `deploy/site-certs/issue.sh`):

| Variable | Default | What it controls |
|---|---|---|
| `BUDGET` | 40 | New certificates per rolling 7 days. Let's Encrypt allows 50 per registered domain per week; keep it at or under 50. |
| `DAILY` | 12 | New certificates per rolling 24 hours. |
| `PER_RUN` | 6 | Certificates one run may request. |
| `RETRY_AFTER` | 21600 | Seconds before a failed handle is tried again. |

The site-certs issuer writes the limits in force to `$SITE_CERT_DIR/limits`, and the app reads
them from there for the waiting-time estimate it shows, so nothing else needs changing. How
often each issuer runs is its systemd timer (`simple-host-*-certs.timer`, every 10 minutes);
change it with a drop-in (`systemctl edit`).
