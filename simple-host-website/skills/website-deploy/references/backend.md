# The per-site backend: state, collections, widgets, templates

Every site can call server-backed features straight from its own page
JavaScript — no server for you to run.

## Before you write a line of it: the trust model

**If the page saves, the page signs the visitor in.** Every write to a site's
backend (`PUT`/`PATCH` `/state`, `POST` `/collections/{coll}`, and the
`/v1/u/{handle}/...` twins) requires either a visitor signed in with Google on
that page or the site owner's `X-API-Key`. A hosted page never has an API key,
so any page that writes — a form, a counter, a vote, a guestbook — loads
`https://simple-host.app/auth.js` and calls `await SH.requireSignIn()` before
it saves (see "Sign-in for writes" below). This is a precondition, not an error
branch: enforcement is global, and a page that skips it gets a 401
(`code: "visitor_auth_required"`) and its save fails. Google is the only
provider. `X-API-Key` still works for writes from scripts and agents.

Sign-in identifies the visitor; it does not make the page private. Reads stay
public, and there is no private or password-locked page feature.

The content host `sites.simple-host.app` is a **shared origin** across all sites.
Reads of state and collections are gated on the request `Origin`, not on an API
key or a sign-in. A browser page on the site sends that header automatically;
anyone else can set it by hand.

So: **the store is public-read.** Anyone who knows the site name can read it.
Never store passwords, API keys, tokens, or personal data there. Co-tenancy is
accepted by design — this was never a confidential store. Backend-anywhere
pages (GitHub Pages + `allowed_origins`) can read; they cannot sign in, so they
cannot write in v1 (no cross-site cookie).

The practical trap: a `curl`, backend, or agent request with **no** `Origin` gets
a **403**, on reads as well as writes. Send one:

```
curl -H "Origin: https://sites.simple-host.app" \
  https://sites.simple-host.app/v1/u/<handle>/sites/<sitename>/state
```

A `Referer:` of the page works too.

Owners can allow extra origins with
`PUT /v1/sites/<sitename>/allowed-origins` (max 20).

## Sign-in for writes (auth.js)

```html
<script src="https://simple-host.app/auth.js" defer></script>
```

Exposes `window.SH`. On the content host it derives the site from the page URL.
Elsewhere set `window.SH_CONFIG` before the tag, same keys as `SH_COMMENTS`:
on a custom domain `{ site: "my-site" }`; backend-anywhere
`{ site, handle, base: "https://simple-host.app" }` (`authBase` optional).

- `SH.ready` — promise; resolves after the first identity check.
- `SH.me({fresh})` → `{signed_in:true, expires_at}` (plus `email` and
  `provider:"google"` on a custom domain only: on the shared content host every
  site is the same origin, so the page learns *that* the visitor is signed in,
  not who) or `{signed_in:false, sign_in:"/v1/auth/oauth/providers"}`. Backed by
  `GET /v1/sites/{site}/me` (and `/v1/u/{handle}/sites/{site}/me`), Origin-gated
  like state, sent with `credentials:"include"`.
- `SH.signIn({provider, returnTo})` — navigates to Google; returns to the current
  page afterwards.
- `SH.signOut()` → promise.
- `SH.requireSignIn()` → resolves the identity if signed in; otherwise navigates
  to sign-in and the promise never resolves. **Put this one call in front of
  every save.**
- `SH.state.get()` → `{data, etag}`; `SH.state.patch(ops)`;
  `SH.state.put(obj, {ifMatch})`.
- `SH.collection(name).append(item)`; `SH.collection(name).list(query)`.
- `SH.mount(target)` — renders a small status box: a "Sign in with Google to
  save" button, or "Signed in as {email} · Sign out". Put
  `<div id="sh-auth"></div>` near the form and call `SH.mount('#sh-auth')`.

Every write sends `credentials:"include"`, `Content-Type: application/json` and
`X-SH-CSRF: 1` for you. A non-2xx rejects with an `Error` carrying `.status`,
`.code`, `.body`; on `code === "visitor_auth_required"` it also fires the
`window` event `sh:signin-required`. It never retries and never re-POSTs.

The visitor session is the same account as dashboard sign-in but is site-scoped
and is not an API key: it cannot deploy or delete.

Raw `fetch` without the script is allowed. Then you send `credentials:'include'`
+ `X-SH-CSRF: 1` yourself and handle the 401 body
`{"error":"sign-in required to write","code":"visitor_auth_required","sign_in":"/v1/auth/oauth/providers","retry":true}`
by navigating to `https://simple-host.app/v1/auth/oauth/google?return_to={location.href}`:

```js
const m = location.pathname.match(/^\/([a-z0-9-]+)\/([a-z0-9-]+)/);
const url = location.origin + `/v1/u/${m[1]}/sites/${m[2]}/state`;   // same-origin
const res = await fetch(url, {method:'PATCH', credentials:'include',
  headers:{'Content-Type':'application/json','X-SH-CSRF':'1'},
  body: JSON.stringify({ops:[{op:'inc', path:'count', by:1}]})});
if (!res.ok) {
  const err = await res.json().catch(()=>({}));
  if (err.code === 'visitor_auth_required') {
    location.href = 'https://simple-host.app/v1/auth/oauth/google?return_to=' + encodeURIComponent(location.href);
    return;
  }
  status.textContent = 'Not saved: ' + (err.code || res.status);   // keep the form, never claim success
}
```

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

Use the helper: it derives the URL from the page path (never hardcode the
handle or site name), signs the visitor in, and sends the write headers.

```html
<div id="sh-auth"></div>
<form id="f"><input name="text" required><button>Save</button></form>
<p id="status"></p>
<script src="https://simple-host.app/auth.js" defer></script>
<script>
window.addEventListener('DOMContentLoaded', function () {
  SH.mount('#sh-auth');
  const status = document.getElementById('status');
  document.getElementById('f').onsubmit = async function (e) {
    e.preventDefault();
    await SH.requireSignIn();                      // navigates to Google if needed
    try {
      await SH.state.patch([{ op: 'append', path: 'items', value: { text: e.target.text.value } },
                            { op: 'inc',    path: 'count', by: 1 }]);
      e.target.reset(); status.textContent = 'Saved';
    } catch (err) {
      status.textContent = 'Not saved: ' + (err.code || err.status);   // keep the form, never claim success
    }
  };
});
</script>
```

Reads need no sign-in: `const { data, etag } = await SH.state.get();`.

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

Appending is a write, so the page signs the visitor in first:
`await SH.requireSignIn(); await SH.collection('rsvps').append({ name, email });`.
If the append succeeds and a follow-up `SH.state.patch` (live count) fails,
retry only the patch — never re-append.

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
path. Posting a comment or a pin is a write, so it needs a signed-in visitor;
the widgets handle that themselves — they show "Sign in with Google to post"
until then and never claim success on a failed write.
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

The state, collections, and both widgets also work — **read-only** — on pages
that are **not** hosted here — an existing GitHub Pages blog, Netlify, Cloudflare Pages. The page
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
custom domain needs its own entry. Reads stay Origin-gated. A backend-anywhere
page cannot sign the visitor in (no visitor cookie on the external host), so it
cannot write in v1: state and collection writes, new comments and new pins from
such a page 401. Put any page that saves on Simple Host itself.

## Start from a template

```
GET /v1/templates          # list: id + description
GET /v1/templates/<id>     # returns {"files":{…}} ready to POST to /files
```

Catalog: `ui-prototype` (app-screen mock + tap-to-comment review), `landing`,
`waitlist`, `event-rsvp`, `architecture`, `travel`, `resume`.

**Prefer a template over hand-authoring.** Fetch it, edit the text, deploy its
`files` map.
