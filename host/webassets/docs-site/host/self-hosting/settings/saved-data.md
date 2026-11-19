# Saved data

Pages save to storage resources (KV, SQLite, files) — see
[storage-and-backups.md](/docs/self-hosting/settings/storage-and-backups.md). This group holds the settings for
notifications about saved data and for the spam check that rules use (`auth.checked`).

<!-- settings:group=data -->
| Setting | Default | Allowed | What it does |
|---|---|---|---|
| `NOTIFY_MAX_PER_SITE` | `10` | 1–1000 subscriptions | Notification subscriptions allowed per site. |
| `NOTIFY_PER_MINUTE` | `60` | 1–100000 events | Visitor events accepted per site per minute. |
| `NOTIFY_PER_DAY` | `5000` | 1–10000000 events | Visitor events accepted per site per day. |
| `NOTIFY_HISTORY_DAYS` | `7` | 1–365 days | Days of notification events and delivery history retained. |
| `NOTIFY_OWNER_CONCURRENCY` | `5` | 1–200 deliveries | Concurrent notification deliveries allowed per owner across all workers. |
| `NOTIFY_PER_URL_PER_MINUTE` | `120` | 1–100000 attempts | Notification attempts per exact URL per minute across all sites and subscriptions. |
| `NOTIFY_WORKERS` | `20` | 1–200 workers | Concurrent notification delivery workers. |
| `NOTIFY_ATTEMPTS` | `5` | 1–20 attempts | Maximum delivery attempts per event and subscription. |
| `NOTIFY_FIELD_MAX_BYTES` | `2048` | 1–65536 bytes | Maximum bytes in a notification string field before truncation. |
| `NOTIFY_BODY_MAX_BYTES` | `16384` | 4096–262144 bytes | Maximum event body bytes before fields are omitted. |
| `NOTIFY_SUSPEND_ATTEMPTS` | `100` | 1–100000 attempts | Attempts needed before the rolling failure percentage can suspend a subscription. |
| `NOTIFY_SUSPEND_FAILURE_PERCENT` | `95` | 1–100 percent | Failure percentage above which a subscription is suspended. |
| `NOTIFY_TIMEOUT_MS` | `5000` | 1–30000 milliseconds | Total timeout for a notification attempt, in milliseconds. |
| `NOTIFY_DEFAULT_TTL_HOURS` | `168` | 1–8760 hours | Default lifetime of an MCP event subscription, in hours. |
| `NOTIFY_SUSPEND_WINDOW_HOURS` | `1` | 1–168 hours | Rolling window for the suspension failure percentage, in hours. |
| `NOTIFY_SUSPEND_DAYS` | `3` | 1–365 days | Days of continuous failures before a subscription is suspended. |
| `NOTIFY_PUSH_PER_SITE_PER_DAY` | `2000` | 1–10000000 pushes | SIM-360: web-push sends allowed per site per day, on top of the general NOTIFY_PER_DAY event cap. |
| `NOTIFY_PUSH_PER_PERSON_PER_DAY` | `200` | 1–100000 pushes | SIM-360: web-push sends allowed per recipient (owner or one visitor) per day across their own devices. |
| `RATE_LIMIT_SPAM_CHECKED_IP` | `10,30s` | stricter freely; loosest `40,7.5s` | Posts that only the Turnstile spam check let in (auth.checked in storage rules), per address across every site. **Security-sensitive.** |
| `RATE_LIMIT_SPAM_CHECKED_SITE` | `60,10s` | stricter freely; loosest `240,2.5s` | Posts that only the spam check let in, per site from everyone together. **Security-sensitive.** |
| `SPAM_CHECKED_POST_MAX_BYTES` | `4000` | 100–65536 bytes | Largest post someone not signed in may add through the spam check (the JSON of its values). |
| `SPAM_CHECKED_POST_MAX_LINKS` | `2` | 0–50 links | Links (http://, https://, www.) one such post may contain. |
| `TURNSTILE_SITE_KEY` | none | text | The Cloudflare Turnstile site key for the spam check (auth.checked in storage rules: anyone posts without signing in once the check passes). With no site key and secret, the spam check is not offered. The widget's hostnames must include this server's own address (PUBLIC_BASE_URL), where the check runs in a small frame. |
| `TURNSTILE_SECRET` | none | secret | The Turnstile secret key that goes with TURNSTILE_SITE_KEY. Prefer TURNSTILE_SECRET_FILE. **Security-sensitive.** |
| `TURNSTILE_SECRET_FILE` | none | text | A file holding the Turnstile secret key (read once at start, used when TURNSTILE_SECRET is empty), for example a systemd credential: LoadCredential=turnstile-secret:/path and TURNSTILE_SECRET_FILE=%d/turnstile-secret. **Security-sensitive.** |
<!-- /settings -->

## Recipes

**Turn on the spam check (anyone posts after a spam check).** Rules that let anyone add
after the check (`auth.checked` in an add check, as the `comments` recipe writes) need a
Cloudflare Turnstile widget, which is free. Without both keys the server does not offer
`auth.checked` in rules, and `get_page_recipe` sends guestbooks and comments to `wall`,
where people sign in to post.

1. In the Cloudflare dashboard, open Turnstile, choose Add widget, name it, and add this
   server's own address as the hostname: the host of `PUBLIC_BASE_URL` (on a small box,
   the dashboard address, not the sites address). Widget mode Managed is fine.
2. Copy the site key and the secret key into the server's environment. On a small box
   installed with `install.sh`, add `TURNSTILE_SITE_KEY=...` and `TURNSTILE_SECRET=...` to
   `/opt/simple-host/.env` (re-running `install.sh` keeps both), then restart:

```
cd /opt/simple-host && sudo docker compose up -d
```

3. The boot log says `spam check: Turnstile on`. Rules may now use `auth.checked`, and
   `get_page_recipe` offers the comments topic.
   Under systemd, prefer `TURNSTILE_SECRET_FILE` with a credential over the plain secret.

## Spam-check credentials

Without the Turnstile site key and secret, rules cannot use `auth.checked`. The check's frame is `/v1/turnstile/frame` on
`PUBLIC_BASE_URL`, whatever address the site uses. An unreadable
`TURNSTILE_SECRET_FILE` turns the check off. A systemd credential can supply it:
`LoadCredential=turnstile-secret:/etc/simple-host-secrets/turnstile-secret` and
`Environment=TURNSTILE_SECRET_FILE=%d/turnstile-secret`.

## Notification delivery behavior

The subscription cap counts agents and the dashboard together. Visitor events
over the per-minute cap are not sent. Per-URL delivery limiting uses an in-memory
token bucket with a one-attempt burst across all sites and subscriptions.
Attempts are spread over about 15 minutes; 410 and 413 responses are never retried.
Truncated string fields are listed under `truncated`. Oversized bodies omit
fields, then cut visitor-controlled paths and keys to 256 bytes if needed.
`NOTIFY_DEFAULT_TTL_HOURS` is also the maximum granted without an OAuth grant.
