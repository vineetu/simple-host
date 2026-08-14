# The per-site backend: state, collections, widgets, templates

Every site can call server-backed features straight from its own page
JavaScript — no server for you to run.

## Before you write a line of it: the trust model

The content host `sites.simple-host.app` is a **shared origin** across all sites.
Reads of state and collections are gated on the request `Origin`, not on an API
key. A browser page on the site sends that header automatically; anyone else can
set it by hand.

Writes (`PUT`/`PATCH` `/state`, `POST` `/collections/{coll}`) require a signed-in
signed-in Google/GitHub session (`credentials: "include"` + `X-SH-CSRF: 1`) or
the site owner's `X-API-Key`. The hosted-page session is the same account as
dashboard sign-in but is not an API key and cannot deploy. A 401 with
`code === "visitor_auth_required"` means: keep the form,
show `error`, fetch `sign_in` (`/v1/auth/oauth/providers`) and navigate to
`{apex}/v1/auth/oauth/{provider}?return_to=…`. Never claim success on a non-2xx.
If POST is 2xx and PATCH is not, do not re-POST — retry PATCH only.

So: **the store is public-read.** Anyone who knows the site name can read it.
Never store passwords, API keys, tokens, or personal data there. Co-tenancy is
accepted by design — this was never a confidential store. Backend-anywhere
pages (GitHub Pages + `allowed_origins`) can still read; signed-in writes from
those hosts are out of v1 (no cross-site cookie).

The practical trap: a `curl`, backend, or agent request with **no** `Origin` gets
a **403**, on reads as well as writes. Send one:

```
curl -H "Origin: https://sites.simple-host.app" \
  https://sites.simple-host.app/v1/u/<handle>/sites/<sitename>/state
```

A `Referer:` of the page works too.

Owners can allow extra origins with
`PUT /v1/sites/<sitename>/allowed-origins` (max 20).

## Shared JSON state (one key-value blob per site)

The canonical route is same-origin and user-scoped. The legacy
`/v1/sites/<sitename>/state` still works.

```
GET   /v1/u/<handle>/sites/<sitename>/state
PUT   /v1/u/<handle>/sites/<sitename>/state    # replace whole blob (optional If-Match: <etag>)
PATCH /v1/u/<handle>/sites/<sitename>/state    # atomic ops — use these
```

`PATCH` ops, so concurrent visitors never clobber each other:

```json
{"ops":[
  {"op":"inc",         "path":"count", "by":1},
  {"op":"append",      "path":"items", "value":{}},
  {"op":"set",         "path":"a.b",   "value":1},
  {"op":"remove",      "path":"a.b"},
  {"op":"removeWhere", "path":"items", "match":{"id":"x"}}
]}
```

Derive the URL from the page path — never hardcode the handle or site name:

```js
const m = location.pathname.match(/^\/([a-z0-9-]+)\/([a-z0-9-]+)/);
const url = location.origin + `/v1/u/${m[1]}/sites/${m[2]}/state`;   // same-origin
const writeH = {'Content-Type':'application/json','X-SH-CSRF':'1'};
const res = await fetch(url, {method:'PATCH', credentials:'include', headers:writeH,
  body: JSON.stringify({ops:[{op:'inc', path:'count', by:1}]})});
if (!res.ok) {
  const err = await res.json().catch(()=>({}));
  if (err.code === 'visitor_auth_required') { /* keep the form, offer sign-in */ }
}
```

Cheap polling: send `If-None-Match: <etag>` on `GET` and get `304` when nothing
changed. Cap is ~1 MB.

For **per-visitor** state (a draft, a preference, a dismissed banner) use
`localStorage` in the page instead — it never belongs in shared state.

## Append-only collections (growing lists)

For sign-ups, RSVPs, submissions — O(1) append, paginated reads:

```
POST /v1/u/<handle>/sites/<sitename>/collections/<name>           # append one JSON item
GET  /v1/u/<handle>/sites/<sitename>/collections/<name>?limit=50  # newest-first
```

Response shape: `{ items: [ { id, data: {…}, created_at } ], next }`.

**If a site collects anything, give the owner somewhere to read it.** A form with
no way to see the results is useless. Add a second page (e.g. `admin.html`) that
GETs the collection and renders every entry, link to it quietly from the main
page, and put `<meta name="robots" content="noindex">` in its head. Do not build
a fake password gate — the data is public by URL. Say so in one small line
instead of pretending otherwise.

## Drop-in widgets (one script tag, no build)

- **Threaded comments:** add `<section id="sh-comments"></section>` and
  `<script src="https://simple-host.app/comments.js" defer></script>`.
- **Pin-on-page feedback** (great for mockups):
  `<script src="https://simple-host.app/feedback.js"></script>`.

Both store under the site's state and auto-derive the right URL from the page
path. They send `X-SH-CSRF` and `credentials` on writes; on
`visitor_auth_required` they show "Sign in to post" and never claim success.
Use a **solid** page background — the widgets read light/dark from it, and
a gradient breaks the detection.

**Always do a UX pass after embedding.** The default look is a deliberately
neutral baseline: it inherits the page font and auto-detects light/dark, but it
does not know the page's brand.

1. Set the accent before the widget tag:
   `<script>window.SH_COMMENTS = { accent: "#b4451f" }</script>`.
   Also available: `title`, `placeholder`, `theme: "light"|"dark"|"auto"`.
2. Fine-tune with the exposed CSS variables:

   ```css
   #sh-comments { --shc-accent:#b4451f; --shc-surface:rgba(0,0,0,.03);
                  --shc-field:#fff; --shc-border:#e0d8cb; --shc-muted:#6f665c;
                  --shc-radius:10px; }
   ```

   Match `--shc-border`/`--shc-surface` to the page's card style and
   `--shc-radius` to its corner rounding. Check contrast against the real
   background.
3. Look at the result. Ship it when the section looks designed with the page, not
   bolted on.

## Backend for a page hosted anywhere else

The state, collections, and both widgets also work on pages that are **not**
hosted here — an existing GitHub Pages blog, Netlify, Cloudflare Pages. The page
keeps its hosting; a Simple Host site acts purely as its backend.

Owner does this once:

```
# 1. Create (or reuse) a site to be the backend — a placeholder index.html is fine:
POST /v1/sites/<backend-name>/files   {"files":{"index.html":"<!doctype html>…"}}

# 2. Allow the external page's origin (scheme://host, no path):
PUT /v1/sites/<backend-name>/allowed-origins
    {"origins":["https://username.github.io"]}
```

Then on the external page:

```html
<section id="sh-comments"></section>
<script>window.SH_COMMENTS = { site: "<backend-name>", base: "https://simple-host.app", accent: "#…" }</script>
<script src="https://simple-host.app/comments.js" defer></script>
```

Feedback pins work the same way with `window.SH_FEEDBACK = { site, base }`.

Note: GitHub user and project pages share one origin
(`https://<username>.github.io`), so one entry covers all of a user's Pages; a
custom domain needs its own entry. Reads stay Origin-gated. Signed-in writes
from a backend-anywhere host 401 in v1 (no visitor cookie on the apex).

## Start from a template

```
GET /v1/templates          # list: id + description
GET /v1/templates/<id>     # returns {"files":{…}} ready to POST to /files
```

Catalog: `ui-prototype` (app-screen mock + tap-to-comment review), `landing`,
`waitlist`, `event-rsvp`, `architecture`, `travel`, `resume`.

**Prefer a template over hand-authoring.** Fetch it, edit the text, deploy its
`files` map.
