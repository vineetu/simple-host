# Versions, rollback, delete, analytics

All of these take `X-API-Key`.

## Listing

- **Sites:** `GET /v1/sites` — sites owned by the caller (admins see all).
- **Current user:** `GET /v1/me` — includes `handle`.
- **Versions of a site:** `GET /v1/sites/<sitename>/versions`.

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
and unique visitors per day with a 30-day trend, computed from access logs. No
tracking script, no cookie banner. IPs are hashed with a daily-rotating salt and
never stored raw; "unique visitors" means distinct hashes per UTC day.

- The dashboard shows `N views · M visitors (30d)` and a sparkline per site.
- API (owner only):

  ```
  GET /v1/sites/<sitename>/analytics?days=30
  → {range_days, totals:{views, visitors}, daily:[{day, views, visitors}…]}
  ```

## Custom domains and private pages

Both live in the separate `connect-domain` skill
(https://simple-host.app/v1/skills/connect-domain). In short: `POST
/v1/sites/<sitename>/domain` with `{domain}` returns one DNS record for the human
to add at their registrar; poll `GET /v1/sites/<sitename>/domain` until `active`.
HTTPS is automatic.

**Private pages (view-lock).** Password-locking a page is a property of a
**connected domain's** isolated origin, not of a path on the shared content host
— so privacy requires a custom domain first. Once a domain is active, the owner
sets or clears the view password via the API; visitors get a login page, and a
signed cookie gates the page along with its state and collections. Good for a
private trip page, a draft, or a client share.
