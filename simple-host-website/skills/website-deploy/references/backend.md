# The per-site backend: state and collections

Every site has a small JSON backend — one shared state document and any number
of append-only collections — that its own page JavaScript can call. There is no
server for you to run.

## Trust model

Reads are public: anyone with the link can read a site's state and collections.
Writes need an identity — a visitor signed in on the page, or an `X-API-Key`.
**Visitor sign-in exists only on a site with its own custom domain.** On the
shared host `sites.simple-host.app` every site is the same origin, so a sign-in
there could never be private to one site; pages on the shared host cannot save,
and the server answers 401 `{"code":"custom_domain_required"}`. Agents write
with an API key on any site, shared host included.

Reads are gated on the request `Origin`, which a browser page sends by itself;
a `curl` or script with no `Origin` gets 403 on reads, so send one:
`curl -H "Origin: https://sites.simple-host.app" https://sites.simple-host.app/v1/u/<handle>/sites/<sitename>/state`.

## Shared JSON state (one document per site)

The canonical route is user-scoped. The legacy `/v1/sites/<sitename>/state`
still works, and is what a page on a custom domain calls (same origin).

```
GET   /v1/u/<handle>/sites/<sitename>/state
PUT   /v1/u/<handle>/sites/<sitename>/state    # replace whole document (optional If-Match: <etag>)
PATCH /v1/u/<handle>/sites/<sitename>/state    # atomic ops — use these
```

`PATCH` ops, so concurrent writers never clobber each other:

```json
{"ops":[
  {"op":"inc",         "path":"count", "by":1},
  {"op":"append",      "path":"items", "value":{}},
  {"op":"set",         "path":"a.b",   "value":1},
  {"op":"remove",      "path":"a.b"},
  {"op":"removeWhere", "path":"items", "match":{"id":"x"}}
]}
```

`GET` returns the document with an `ETag`; send `If-None-Match: <etag>` to get
`304` when nothing changed (cheap polling). The document is capped at ~1 MB.

For **per-visitor** state (a draft, a preference, a dismissed banner) use
`localStorage` in the page instead — it never belongs in shared state.

## Append-only collections (growing lists)

For sign-ups, RSVPs, submissions — O(1) append, paginated reads:

```
POST /v1/u/<handle>/sites/<sitename>/collections/<name>            # append one JSON item (≤ 64 KB)
GET  /v1/u/<handle>/sites/<sitename>/collections/<name>?limit=50   # newest-first
```

Response shape: `{ items: [ { id, data: {…}, created_at } ], next }`. When a
page fills, `next` is the cursor for the older page: `?limit=50&before=<next>`;
`next` is absent on the last page.

If an append succeeds and a follow-up `state` patch (a live count) fails, retry
only the patch — never re-append.

**Pair every form with a viewer page.** A form with nowhere to read the results
is half a feature. Add a second page (e.g. `admin.html`) that GETs the collection
and lists every entry newest-first, link to it quietly from the main page, and
put `<meta name="robots" content="noindex">` in its head. Do not build a fake
password gate: sites and their data are public to anyone with the link, so say
that in one small line instead.

## Saving from a page (custom domain only)

The site needs its own domain first — the `connect-domain` skill. Then the page
loads the hosted helper, offers sign-in next to the form, and signs the visitor
in before every save. Because a custom-domain URL does not carry the site name,
set `window.SH_CONFIG` before the script tag.

```html
<div id="sh-auth"></div>
<form id="f"><input name="text" required><button>Save</button></form>
<p id="status"></p>
<script>window.SH_CONFIG = { site: "<sitename>" };</script>
<script src="https://simple-host.app/auth.js" defer></script>
<script>
window.addEventListener('DOMContentLoaded', function () {
  SH.mount('#sh-auth');                          // Google sign-in + email-code form
  const status = document.getElementById('status');
  document.getElementById('f').onsubmit = async function (e) {
    e.preventDefault();
    await SH.requireSignIn();                    // signs the visitor in if needed
    try {
      await SH.collection('entries').append({ text: e.target.text.value });
      await SH.state.patch([{ op: 'inc', path: 'count', by: 1 }]);
      e.target.reset(); status.textContent = 'Saved';
    } catch (err) {
      status.textContent = 'Not saved: ' + (err.code || err.status);   // keep the form, never claim success
    }
  };
});
</script>
```

Reads need no sign-in: `const { data, etag } = await SH.state.get();` and
`await SH.collection('entries').list({ limit: 50 })`.

The `SH` object:

- `SH.ready` — promise; resolves after the first identity check.
- `SH.me({fresh})` → `{signed_in:true, email, provider, expires_at}` or
  `{signed_in:false, sign_in:"/v1/auth/oauth/providers"}`.
- `SH.mount(target)` — renders a small status box: signed out, a "Sign in with
  Google" button plus an inline email → 6-digit code form; signed in, "Signed in
  as {email} · Sign out". Google (more providers later).
- `SH.requireSignIn()` → resolves the identity if signed in; otherwise starts
  sign-in and the promise never resolves. **Put this one call in front of every
  save.**
- `SH.signIn({provider, returnTo})`, `SH.email.request(email)`,
  `SH.email.verify(email, code)` (15-minute code, 3 attempts; the account is
  created on first verify), `SH.signOut()`.
- `SH.state.get()` → `{data, etag}`; `SH.state.patch(ops)`;
  `SH.state.put(obj, {ifMatch})`.
- `SH.collection(name).append(item)`; `SH.collection(name).list(query)`.

Every write sends `credentials:"include"`, `Content-Type: application/json` and
`X-SH-CSRF: 1` for you. A non-2xx rejects with an `Error` carrying `.status`,
`.code`, `.body`. It never retries and never re-POSTs. The visitor session is
site-scoped and is not an API key: it cannot deploy or delete.

Raw `fetch` without the helper works too: send `credentials:'include'` and
`X-SH-CSRF: 1` yourself, and on a 401 with `code === "visitor_auth_required"`
navigate to `https://simple-host.app/v1/auth/oauth/google?return_to=` +
`encodeURIComponent(location.href)`.

## Saving from an agent (API key)

Any account's API key writes to any site's state and collections, on the shared
host too. Send `X-API-Key: <key>` on `PUT`/`PATCH /v1/sites/<sitename>/state`
and `POST /v1/sites/<sitename>/collections/<name>` (or the
`/v1/u/<handle>/sites/<sitename>/...` twins).

An agent acting for a person who is **not** the site owner gets that person's
key by email code:

1. `POST https://simple-host.app/v1/auth` with `{"email":"person@example.com"}`
   → 202 `{message, email, expires_in_seconds: 900}`. The person receives a
   6-digit code.
2. Ask the person for the code, then `POST https://simple-host.app/v1/auth/verify`
   with `{"email":"person@example.com","code":"123456"}` → 200 with `api_key`
   (the account is created if it did not exist).
3. Send `X-API-Key: <that key>` on the writes.

Codes are bound to where they were requested: one requested through `/v1/auth`
works only at `/v1/auth/verify`, and one emailed by a page sign-in works only on
that site. Keep the key in the agent's config or secret store, never in page
HTML or committed files — it also grants that person's dashboard and site
management. The site owner's agent already has the owner key and needs none of
this.

## Error bodies

| Status | Body | Meaning |
|---|---|---|
| 401 | `{"error":"…","code":"custom_domain_required"}` | A page on the shared host tried to save. Connect a domain; agents use an API key. |
| 401 | `{"error":"sign-in required to write","code":"visitor_auth_required","sign_in":"/v1/auth/oauth/providers","retry":true}` | No signed-in visitor and no key. Sign the visitor in, then retry once. |
| 403 | `{"error":"missing CSRF header","code":"csrf_required"}` | A session write without `X-SH-CSRF: 1`. The helper always sends it. |
| 401 | `{"error":"invalid API key","code":"invalid_api_key"}` | Unknown `X-API-Key`. Do not retry with the same key. |
| 403 | (reads) | No `Origin` header on a non-browser read. Send one. |

On any of these: keep the form, never claim success, and never re-POST a
collection item after a partial write.
