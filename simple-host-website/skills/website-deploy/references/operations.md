# Versions, rollback, delete, analytics

All of these take `X-API-Key`.

## Listing

- **Sites:** `GET /v1/sites` — sites owned by the caller (admins see all).
- **Current user:** `GET /v1/me` — includes `handle`.
- **Versions of a site:** `GET /v1/sites/<sitename>/versions`.

## Rename

`PATCH /v1/sites/<sitename>` with `{"name":"new-name"}` renames the site and
moves its files. A connected custom domain stays attached. The old public URL
is not redirected and returns 404; use `site_url` from the response.

## API key rotation

`POST /v1/me/api-key/rotate` returns a new `api_key`. The old key stops working
immediately, so update the agent or CLI before its next request.

## Rollback

Uploads are append-only. Rolling back re-points the active version at one that
already exists; it does not delete anything.

```
PUT /v1/sites/<sitename>/active-version
X-API-Key: <api_key>
{"version_number": <n>}
```

There is **no** `.../activate` and no `.../version/<n>` endpoint. This is the one.

## Delete

```
DELETE /v1/sites/<sitename>
```

Removes the site, every version, and its state and collections. Not reversible —
confirm with the user in plain language before calling it, and say what will be
lost.

## Analytics

Every deployed site gets server-side visitor analytics automatically — page views
and unique visitors, hourly and daily, computed from access logs. No tracking
script, no cookie banner. IPs are hashed with a server-side salt and never
stored raw. `visitors` is unique visitors over the window asked for — the range
total is real uniques, not the sum of the daily numbers, so someone who visited
on five days counts once in `totals` and five times across `daily`.

Every count is split four ways by who was asking:

| Class | What it is |
|---|---|
| `person` | No automation signature — **this is the audience number** |
| `bot` | Crawlers, AI scrapers, SEO tools, security scanners, HTTP libraries |
| `infra` | Uptime probes and health checks pointed at the site |
| `unknown` | Days recorded before classification existed (before `classified_from`); cannot be broken down — never fold it into the others |

Report `person` when a user asks how many people visited. Never quote a combined
total: `infra` is typically an order of magnitude larger than real traffic (a
probe hitting `/` every 30s is 2,880 requests a day), so a total answers a
question nobody asked.

- The dashboard shows People / Bots / Infra side by side per site, with a
  24-hour bar chart of people and bots.
- API (owner only):

  ```
  GET /v1/sites/<sitename>/analytics?days=30
  → {
      range_days,
      classified_from,                                        // e.g. "2026-08-09"
      totals:   {person:{views,visitors}, bot:{…}, infra:{…}, unknown:{…}},
      last_24h: {person:{views,visitors}, bot:{…}, infra:{…}, unknown:{…}},
      daily:    [{day,  person:{…}, bot:{…}, infra:{…}, unknown:{…}}…],  // dense
      hourly:   [{hour, person:{…}, bot:{…}, infra:{…}, unknown:{…}}…]   // 24 buckets
    }
  ```

## Custom domains

These live in the separate `connect-domain` skill
(https://simple-host.app/v1/skills/connect-domain). In short: `POST
/v1/sites/<sitename>/domain` with `{domain}` returns one DNS record for the human
to add at their registrar; poll `GET /v1/sites/<sitename>/domain` until `active`.
HTTPS is automatic.

**There is no private or password-locked mode.** Every deployed site is public to
anyone with its address, on a custom domain or not. If a user asks for privacy,
say so plainly rather than suggesting a workaround. The sign-in (Google or email code) that a
page needs before it can *write* to its backend (see `backend.md`) gates saving,
not reading — do not present it as a private page.
