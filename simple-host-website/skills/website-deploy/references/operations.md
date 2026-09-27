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

## API keys: list, name, revoke, sign out everywhere

Each sign-in and each agent holds its own key. Keys issued now start with
`shk_` (older bare-hex keys keep working).

- `GET /v1/me/keys` lists them: `id`, `name` (`dashboard sign-in`,
  `agent sign-in`, `event account`, or a typed name; absent on older keys),
  `last4`, `created_at`, `last_used_at`, and `current` for the key you sent.
- `POST /v1/me/keys` with `{"name":"GitHub Actions"}` mints a named key for a
  CI secret or another machine and returns `api_key` once.
- `DELETE /v1/me/keys/<id>` revokes one key; the others keep working. This is
  the fix for one leaked key: find it by `last4`, revoke it.
- `POST /v1/me/sign-out` ends the key you send (what the dashboard's Sign out
  does).
- `POST /v1/me/api-key/rotate` is **Sign out everywhere**: it returns a new
  `api_key`, every older key stops working immediately and connected apps
  (ChatGPT, Claude, Grok) are signed out, so update the agent or CLI before its
  next request.

The person sees and manages the same list in the **Keys** panel of their page
(`https://<handle>.simple-host.app/` while signed in). A 401 carries a `code`:
`missing_api_key`, `wrong_auth_header` (sent `Authorization` instead of
`X-API-Key`) or `invalid_api_key` (revoked, signed out, or never valid: sign in
again for a new key).

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
  -H "X-API-Key: <api_key>" -H "X-Skill-Version: 0.20.5"
curl -fsS "https://simple-host.app/v1/sites/<sitename>/versions/<n>/files/index.html" \
  -H "X-API-Key: <api_key>" -H "X-Skill-Version: 0.20.5"
```

The first call returns version metadata and files sorted by relative path with byte
sizes. The second streams the file with sandbox CSP. Pruned versions return 404.

## Delete and restore

```
DELETE /v1/sites/<sitename>
```

Takes the site offline at once, with every version, its state and collections.
It stays in Recently deleted for 7 days, then it is removed for good. Confirm
with the user in plain language before calling it, and say what goes offline.
Until it is removed its name stays taken: creating a site with that name answers
409 with `"recently_deleted": true` (ask the person whether to restore it). Offer
a copy first (Download a copy, below). A site the operator has taken down cannot
be deleted (403 `site_suspended`).

- `GET /v1/me/deleted-sites` lists them: `{"sites":[{"name","deleted_at","purge_at"}],"retention_days":7}`.
- `POST /v1/sites/<sitename>/restore` brings one back under the same name and
  address, with every version, its saved data and any connected address.

Connector: `delete_site`, `list_deleted_sites`, `restore_site`.

## Change the handle (the person's address)

`PATCH /v1/me` with `{"handle":"new-name"}` changes the `<handle>` in every
address. Before anything is published it changes freely; after that, once every
30 days (429 with `next_change_after` otherwise). The old handle stays reserved
for the person and every old address redirects to the new one. What pages kept
in visitors' browsers (localStorage) starts empty at the new address, and
visitors sign in again, so tell the person before changing it. They can also do
it themselves under "Your address" on their Simple Host page. Afterwards re-read
`site_url` from `GET /v1/sites`; never compose addresses.

## Change the sign-in email; sign-in alerts

```
POST /v1/me/email         {"email": "new@example.com"}   (X-API-Key) → 202, code sent there
POST /v1/me/email/verify  {"code": "123456"}             (X-API-Key) → 200 {"email"}
```

The person reads the 6-digit code from the NEW inbox (15 minutes, 3 tries).
Verifying moves the account there: codes go to the new address from then on;
keys, the handle, sites and connected apps stay, and a linked Google sign-in
keeps working. The old address gets a notice. Refusals: 400
`not_an_account_key` (a connected app cannot do this; point the person to
"Sign-in" on their page), `same_email`, `invalid_email`, `no_pending_change`;
401 `invalid_code`; 409 `email_taken` (another account signs in with it).

After each sign-in or app connection the person gets a short email (time,
browser or app, a link to "Sign out everywhere"; at most one per browser a day).
`GET /v1/me` shows `signin_alerts`; `PATCH /v1/me {"signin_alerts": false}`
turns them off (own key only). Only change it when the person asks.
## Download a copy

```
GET /v1/sites/<sitename>/export.tar.gz          (X-API-Key)
POST /v1/sites/<sitename>/export-link           (X-API-Key) → {"url", "expires_at"}
```

The archive holds the live files (`<site>/files/…`), the saved state
(`<site>/state.json`) and every collection's items (`<site>/collections.json`,
private lists included). The link form (connector: `export_site`) opens the same
archive without a key for 10 minutes: give it to the person to click, never post
it publicly, and make a new one if it has expired.

Each entry in `collections.json` is `{id, created_at, submitted_by, data}`
(`submitted_by` only on private lists).

## The person's whole account: download or delete

```
GET /v1/me/export.tar.gz                        (X-API-Key)
DELETE /v1/me  {"confirm": "<handle>"}          (X-API-Key; the email if no handle)
```

The first is one archive of everything held about the person: every live site
(as above, under `sites/<name>/`), `account.json`, `keys.json` (names only, never
keys), `connected_apps.json` and `visitor.json` (entries they sent to other
people's private lists while signed in; public-list entries and shared page data
are not linked to anyone, so support@simple-host.app helps with those). The connector's `export_site` covers one site; the whole-account archive needs the
person's own key (a connected app gets 400 `not_an_account_key`), so point them to
"Download my data" on their Simple Host page.

`DELETE /v1/me` deletes the account and all its data at once and for good,
including their entries on other people's private lists; nothing goes to Recently
deleted. Only when the person explicitly asks to delete their account: say what
goes, offer the download first, and send their handle as `confirm` only after
they confirm. It needs their own key (a connected app gets 400
`not_an_account_key`); they can always do it themselves under "Your data" on
their page. A suspended account (403 `account_suspended`) or one with a site
the operator took down (403 `site_suspended`) cannot delete itself: point the
person to support@simple-host.app.

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
site already lives at its own `https://<sitename>.<handle>.simple-host.app/`. In short:
`POST /v1/sites/<sitename>/domain` with `{domain}`.

- A free `<name>.simple-host.app` (`{"domain":"clay-studio.simple-host.app"}`)
  answers `active` at once. No DNS step.
- The person's own domain returns two DNS records for the human to add at their
  registrar: the address record (`dns`) and a TXT ownership record (`dns_txt`,
  kept in place); poll `GET /v1/sites/<sitename>/domain` until `active`.

The site moves there and its old address redirects. Sign-in and private
collections work either way: visitors sign in with Google or an emailed code on
the site's own address, and every save from a page needs a signed-in visitor
(see `backend.md`). Agents write with the site owner's API key.

## Private collections

`PUT /v1/sites/<sitename>/collections/<name>/privacy` with `{"private": true}`
(connector: `set_collection_privacy`) makes one collection owner-only: visitors
signed in on the site's own address add to it; only the site owner — and the
Simple Host operator, for moderation — can read it. Any site can make a list
private.
`{"private": false}` makes it public again, including everything already in it
(the submitters' emails, `_submitted_by`, stay visible only to the owner),
so confirm with the user first. The owner reads it with
`GET /v1/sites/<sitename>/collections/<name>`, lists all with
`GET /v1/sites/<sitename>/collections` (each entry has `private`), and downloads
`GET /v1/sites/<sitename>/collections/<name>/export.csv`. The owner edits or
deletes one item with `PATCH` / `DELETE /v1/sites/<sitename>/collections/<name>/items/<id>`
(in a public list the owner can delete an item but not edit it: 409 `append_only`),
and empties a whole list with `DELETE /v1/sites/<sitename>/collections/<name>` and
`{"confirm": "<name>"}`. Full flow: `backend.md`.

**Pages are always public.** There is no password-locked page. Every deployed
page is public to anyone with its address, on a custom domain or not. If a user
asks for a private page, say so plainly rather than suggesting a workaround.
Sign-in gates saving, not reading pages; only a private collection is
owner-only.
