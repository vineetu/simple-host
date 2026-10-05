# Your home page

Your public person address opens your showcase, or an owned site you choose.
In the owner dashboard, use **Your address → Home page** and **Your showcase**
for a plain-text bio, pins and order. Connector tools are `set_home_page`,
`set_bio`, `set_showcase_site`; owner REST uses `GET/PUT /v1/me/home`,
`GET/PUT /v1/me/bio`, `GET/PUT /v1/sites/{name}/showcase`.
`{"site":null}` restores the showcase. Rename follows the site; deletion clears
it, and offline, suspended or taken-down homes fall back to the showcase.
Pinned sites come first, then smaller order numbers, then newest creation time.
The bio limit is `SHOWCASE_BIO_MAX_LENGTH` (280 characters; range 1–2000).

A custom home can fetch `GET /v1/u/{handle}/showcase.json` from the API origin
without credentials. Its `handle`, `bio` and `sites` include each visible site's
`name`, `url`, `title`, `description`, `created_at`, `updated_at`, `pinned` and
`order`. The feed uses the public showcase filter, CORS without credentials and
a 30-second cache. Unlisted, offline, passcode-protected and taken-down sites
stay hidden.

On hosted Simple Host, the selected site serves at the person-host root and
keeps its normal URL; sign-in and data on that origin cover the selected site
only. On a small-box install, including Droplet 1-Click and Coolify, the default
path model remains: `https://sites.<domain>/<handle>` opens the selected site's
normal URL. The owner dashboard remains `https://<domain>/<handle>`.
No wildcard DNS or extra certificate is required for path-model home selection.
Person-host installations keep the hosted behavior when configured. A domain
for a whole personal space has not shipped; existing per-site domains remain.
Simple Hack accounts own no personal sites, so this feature does not apply.
