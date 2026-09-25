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

### Reading an old version

Preview a retained version before restoring it (owner API key required):

```bash
curl -fsS "https://simple-host.app/v1/sites/<sitename>/versions/<n>/files" \
  -H "X-API-Key: <api_key>" -H "X-Skill-Version: 0.17.0"
curl -fsS "https://simple-host.app/v1/sites/<sitename>/versions/<n>/files/index.html" \
  -H "X-API-Key: <api_key>" -H "X-Skill-Version: 0.17.0"
```

The first call returns version metadata and files sorted by relative path with byte
sizes. The second streams the file with sandbox CSP. Pruned versions return 404.

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

## A nicer address: a free name or a custom domain

These live in the separate `connect-domain` skill
(https://simple-host.app/v1/skills/connect-domain). They are optional: every
site already lives at `https://<handle>.simple-host.app/<sitename>/`. In short:
`POST /v1/sites/<sitename>/domain` with `{domain}`.

- A free `<name>.simple-host.app` (`{"domain":"clay-studio.simple-host.app"}`)
  answers `active` at once. No DNS step.
- The person's own domain returns one DNS record for the human to add at their
  registrar; poll `GET /v1/sites/<sitename>/domain` until `active`.

The site moves there and its old address redirects. Sign-in and private
collections work either way: visitors sign in with Google or an emailed code on
the site's own address, and every save from a page needs a signed-in visitor
(see `backend.md`). Agents write with an API key anywhere.

## Private collections

`PUT /v1/sites/<sitename>/collections/<name>/privacy` with `{"private": true}`
(connector: `set_collection_privacy`) makes one collection owner-only: visitors
signed in on the site's own address add to it; only the site owner — and the
Simple Host operator, for moderation — can read it. Any site can make a list
private.
`{"private": false}` makes it public again, including everything already in it,
so confirm with the user first. The owner reads it with
`GET /v1/sites/<sitename>/collections/<name>`, lists all with
`GET /v1/sites/<sitename>/collections` (each entry has `private`), and downloads
`GET /v1/sites/<sitename>/collections/<name>/export.csv`. The owner edits or
deletes one item with `PATCH` / `DELETE /v1/sites/<sitename>/collections/<name>/items/<id>`
(public lists stay append-only: 409 `append_only`). Full flow: `backend.md`.

**Pages are always public.** There is no password-locked page. Every deployed
page is public to anyone with its address, on a custom domain or not. If a user
asks for a private page, say so plainly rather than suggesting a workaround.
Sign-in gates saving, not reading pages; only a private collection is
owner-only.
