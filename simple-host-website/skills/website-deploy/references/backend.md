# The per-site backend: state and collections

Every site has a small JSON backend — one shared state document and any number
of append-only collections — that its own page JavaScript can call. There is no
server for you to run.

## Trust model

Reads are public: anyone with the link can read a site's state and its public
collections. The one exception is a **private collection** (see "Private
collections" below): visitors add to it, only the site owner reads it.
**On the shared host `sites.simple-host.app` anyone can write too.** A page
there saves without sign-in or key, and that data can be changed by anyone.
**On a site with its own custom domain writes need an identity** — a visitor
signed in on the page (Google or an emailed code), or an `X-API-Key`. Visitor
sign-in exists only there: on the shared host every site is the same origin, so
a sign-in could never be private to one site. Agents write with an API key on
any site, shared host included.

Once a site has a custom domain bound, it lives only there. Its
`sites.simple-host.app/<handle>/<site>/...` page URL answers 302 to
`https://<domain>/...` (same path and query), and its shared-host API takes no
writes at all: state and collection writes there answer 401
`use_custom_domain` (with the `domain`) whether or not an `X-API-Key` is sent.
Reads there stay public. Agents write through the apex
`https://simple-host.app/v1/...` with a key (what this skill already does) or
through the domain's own `/v1/` (key or session). Sites without a domain are
unchanged. A free `<name>.simple-host.app` address counts as the site's own
domain here, exactly like a custom one.

The plain-`fetch` shape that works on both hosts (the `SH` helper below sends
the same headers for you):

```js
const m = location.pathname.match(/^\/([^/]+)\/([^/]+)\//);          // shared host: /<handle>/<site>/
const API = m ? `/v1/u/${m[1]}/sites/${m[2]}` : '/v1/sites/<sitename>';   // custom domain: same-origin
await fetch(API + '/state', { method: 'PATCH', credentials: 'include',
  headers: { 'Content-Type': 'application/json', 'X-SH-CSRF': '1' },
  body: JSON.stringify({ ops: [{ op: 'inc', path: 'count', by: 1 }] }) });
await fetch(API + '/collections/entries', { method: 'POST', credentials: 'include',
  headers: { 'Content-Type': 'application/json', 'X-SH-CSRF': '1' },
  body: JSON.stringify({ text: 'hello' }) });
```

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
password gate: a public collection is readable by anyone with the link, so say
that in one small line instead. If the entries hold personal details, use a
private collection and an owner page instead (below).

## Saving from a page with the hosted helper

The page loads the hosted helper, offers sign-in next to the form, and signs the
visitor in before every save. The same code works on both hosts: on the shared
host `SH.mount` renders one muted line ("Saves on this site are public. Connect
a domain to add sign-in.") and `SH.requireSignIn()` resolves at once, so the
save just proceeds; on a custom domain (the `connect-domain` skill) it signs the
visitor in first. On the shared host for a site that has a domain bound,
`SH.mount` renders "This site saves on <domain>. Sign in there to save." with a
link to the same page on the domain, and `SH.requireSignIn()` rejects with
`code: "use_custom_domain"` and `.domain`. Because a custom-domain URL does not carry the site name, set
`window.SH_CONFIG` before the script tag.

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

Reads of state and public collections need no sign-in:
`const { data, etag } = await SH.state.get();` and
`await SH.collection('entries').list({ limit: 50 })`.

The `SH` object:

- `SH.ready` — promise; resolves after the first identity check.
- `SH.me({fresh})` → `{signed_in:true, email, provider, expires_at}` or
  `{signed_in:false, sign_in:"/v1/auth/oauth/providers"}`.
- `SH.mount(target)` — renders a small status box: signed out, a "Sign in with
  Google" button plus an inline email → 6-digit code form; signed in, "Signed in
  as {email} · Sign out". Google (more providers later).
- `SH.requireSignIn()` → resolves the identity if signed in; otherwise starts
  sign-in and the promise never resolves. On the shared host it resolves
  immediately (with the `/me` body) so the save proceeds without sign-in.
  **Put this one call in front of every save.**
- `SH.signIn({provider, returnTo})`, `SH.email.request(email)`,
  `SH.email.verify(email, code)` (15-minute code, 3 attempts; the account is
  created on first verify), `SH.signOut()`.
- `SH.state.get()` → `{data, etag}`; `SH.state.patch(ops)`;
  `SH.state.put(obj, {ifMatch})`.
- `SH.collection(name).append(item)` → the stored item `{id, data, created_at}`;
  `SH.collection(name).list(query)` → `{items, next}` (plus `private: true` on a
  private list, which only the owner can read).
- Private lists, owner only: `SH.collection(name).update(id, fields)` → the
  updated item; `SH.collection(name).remove(id)`. `id` is `items[].id` from
  `list()`.

Every write sends `credentials:"include"`, `Content-Type: application/json` and
`X-SH-CSRF: 1` for you. A non-2xx rejects with an `Error` carrying `.status`,
`.code`, `.body`. It never retries and never re-POSTs. The visitor session is
site-scoped and is not an API key: it cannot deploy or delete.

Raw `fetch` without the helper works too: send `credentials:'include'` and
`X-SH-CSRF: 1` yourself, and on a 401 with `code === "visitor_auth_required"`
navigate to `https://simple-host.app/v1/auth/oauth/google?return_to=` +
`encodeURIComponent(location.href)`.

## Private collections (orders, RSVPs, anything personal)

Use a private collection when a form collects orders, RSVPs, survey answers,
sign-ups, or anything with names, emails, phone numbers or addresses. Visitors
signed in on the site's own address add to it. Only the site owner — and the
Simple Host operator, for moderation — can read it. Everyone else gets 404.
Pages stay public; only the list is private.

Public lists (a guestbook, votes, public comments) stay public. Say so plainly
when you build one.

### 1. Give the site its own address

Offer the free `<name>.simple-host.app` first. It is one call, active at once,
with no DNS step. With the connector: `connect_domain` with
`clay-studio.simple-host.app`. Without it:

```
POST /v1/sites/<sitename>/domain
X-API-Key: <api_key>
{"domain": "clay-studio.simple-host.app"}
```

It answers 200 with `"status": "active"`. Names are first come, first served:
409 `domain_taken` means another site has it, 400 `name_reserved` means the
name is kept for the platform, 400 `invalid_name` means it is not one DNS label
(letters, digits, hyphens). Pick another name and retry. The person's own domain
is the alternative (the `connect-domain` skill, one DNS record). Either way the
site now lives at that address and its old shared URL 302s there.

### 2. Make the collection private

Do this before the form goes live. It works before any item exists.
With the connector: `set_collection_privacy`. Without it:

```
PUT /v1/sites/<sitename>/collections/orders/privacy
X-API-Key: <api_key>
{"private": true}
```

It answers 200 with `"private": true`, the `domain` and a one-line `message`.
It answers 409 `custom_domain_required` when the site has no active own address
yet (a custom domain still waiting on DNS does not count); do step 1 first.

`{"private": false}` makes the list public again, and everything already saved
in it becomes readable by anyone. Confirm with the owner before sending it.

### 3. The form page

The visitor signs in, then adds one JSON object. The server stamps
`_submitted_by` (the visitor's verified email) and `_submitted_at` (server time)
on every item, replacing anything the page sent under those keys. The 201 answer
is the stored item; show it back to the visitor as their confirmation. Visitors
cannot read their own submissions later, so this answer is the receipt.

```html
<div id="sh-auth"></div>
<form id="order">
  <input name="name" required placeholder="Name">
  <input name="phone" required placeholder="Phone">
  <input name="item" required placeholder="What would you like?">
  <button>Place order</button>
</form>
<p id="status"></p>
<script>window.SH_CONFIG = { site: "<sitename>" };</script>
<script src="https://simple-host.app/auth.js" defer></script>
<script>
window.addEventListener('DOMContentLoaded', function () {
  SH.mount('#sh-auth');
  const status = document.getElementById('status');
  document.getElementById('order').onsubmit = async function (e) {
    e.preventDefault();
    const f = e.target;
    await SH.requireSignIn();                        // signs the visitor in if needed
    try {
      const saved = await SH.collection('orders').append({ name: f.name.value, phone: f.phone.value, item: f.item.value });
      f.reset();
      status.textContent = 'Order received: ' + saved.data.item + ' for ' + saved.data.name + ' (' + saved.data._submitted_by + ')';
    } catch (err) {
      status.textContent = 'Not sent: ' + (err.code || err.status);   // keep the form, never claim success
    }
  };
});
</script>
```

### 4. The owner page

Add a page on the site, e.g. `orders.html`, that signs in, lists the
collection, and lets the owner mark an order done or delete it. It works only
when the owner's own account is signed in on that domain; anyone else gets 404
`not_found`. Link it quietly or not at all, and mark it `noindex`.

```html
<meta name="robots" content="noindex">
<div id="sh-auth"></div>
<p id="status">Loading…</p>
<table id="orders" hidden>
  <thead><tr><th>When</th><th>From</th><th>Name</th><th>Phone</th><th>Item</th><th>Status</th><th></th></tr></thead>
  <tbody></tbody>
</table>
<script>window.SH_CONFIG = { site: "<sitename>" };</script>
<script src="https://simple-host.app/auth.js" defer></script>
<script>
window.addEventListener('DOMContentLoaded', async function () {
  SH.mount('#sh-auth');
  const status = document.getElementById('status');
  const orders = SH.collection('orders');
  function button(label, onclick) {
    const b = document.createElement('button'); b.textContent = label; b.onclick = onclick; return b;
  }
  await SH.requireSignIn();
  try {
    const { items } = await orders.list({ limit: 200 });
    const body = document.querySelector('#orders tbody');
    for (const { id, data } of items) {
      const tr = body.insertRow();
      for (const v of [data._submitted_at, data._submitted_by, data.name, data.phone, data.item, data.status]) tr.insertCell().textContent = v || '';
      const actions = tr.insertCell();
      actions.append(
        button('Mark done', async function () {
          try { const saved = await orders.update(id, { status: 'done' }); tr.cells[5].textContent = saved.data.status; }
          catch (err) { status.textContent = 'Not updated: ' + (err.code || err.status); }
        }),
        button('Delete', async function () {
          if (!confirm('Delete this order for good?')) return;
          try { await orders.remove(id); tr.remove(); }
          catch (err) { status.textContent = 'Not deleted: ' + (err.code || err.status); }
        })
      );
    }
    document.getElementById('orders').hidden = false;
    status.textContent = items.length + ' orders, newest first';
  } catch (err) {
    status.textContent = err.status === 404 ? "Sign in with the owner's account to see orders." : 'Could not load: ' + (err.code || err.status);
  }
});
</script>
```

Use `textContent`, never `innerHTML`, for submitted values.

### Editing and deleting items (private lists, owner only)

```
PATCH  /v1/sites/<sitename>/collections/<name>/items/<id>    # merge fields into the item
DELETE /v1/sites/<sitename>/collections/<name>/items/<id>    # delete the item for everyone
```

The `/v1/u/<handle>/sites/<sitename>/...` twins work too. `<id>` is
`items[].id` from a list read.

- **PATCH** takes one JSON object of fields to merge. A field sent as `null` is
  removed. `_submitted_by` and `_submitted_at` can never be set, changed or
  removed (ignored if sent), and `created_at` never changes. Answers 200 with the
  item `{id, data, created_at}`. 400 if the body is not an object, 413 if the
  item is over 64 KB after the merge.
- **DELETE** answers 204. The item is gone for good; confirm with the owner first.
- **Who:** the site owner — with `X-API-Key`, the connector
  (`update_collection_item`, `delete_collection_item`), or the owner's own
  sign-in on the site's own address from a page there (send `X-SH-CSRF: 1`; the
  helper does) — and the Simple Host operator, for moderation. Everyone else,
  including the visitor who submitted the item, gets 404 `not_found`.
- **Public lists stay append-only:** these calls on a public list answer 409
  `append_only`.

### Reading as the owner, outside the page

Only the site owner — and the Simple Host operator, for moderation — can read a
private list.


- The dashboard shows the list, with a spreadsheet download.
- `read_collection` with the connector. Without it,
  `GET /v1/sites/<sitename>/collections/orders` with `X-API-Key`; the answer
  adds `"private": true`.
- `GET /v1/sites/<sitename>/collections` lists every collection; each entry has
  `"private": true|false`. A private list shows even while empty, with
  `last_at: null`.
- `GET /v1/sites/<sitename>/collections/orders/export.csv` with `X-API-Key`
  downloads a CSV; `_submitted_by` and `_submitted_at` are columns like any
  other key.

Reads through the shared host `sites.simple-host.app` answer 404 for a private
list, even with a key; use the apex `https://simple-host.app/v1/...`.

### Errors when adding to a private list

| Status | Code | Meaning |
|---|---|---|
| 401 | `visitor_auth_required` | Not signed in. `SH.requireSignIn()` handles it. |
| 403 | `csrf_required` | Missing `X-SH-CSRF: 1`. The helper always sends it. |
| 403 | `private_visitor_only` | Sent with an API key, or by an agent (`add_to_collection`). Agents cannot add to a private list; only signed-in visitors can. |
| 403 | `private_needs_own_domain` | Sent from anywhere other than the site's own address. |
| 401 | `use_custom_domain` (+ `domain`) | Sent through the shared host. Link the visitor to the same page on `domain`. |
| 400 | — | The item is not one JSON object. |
| 413 | — | The item is over 64 KB. |

### On the shared address

On `sites.simple-host.app/<handle>/<sitename>/` private lists are not offered,
and anything saved is public. Do not collect personal details there. Suggest
an email-order flow instead (a `mailto:` link, "email us to order"), or claim
the free `<name>.simple-host.app` address (one call) and use a private list.

## Saving from an agent (API key)

Any account's API key writes to any site's state and public collections, on
the shared host too. A private collection takes no writes from a key or an
agent (403 `private_visitor_only`). Send `X-API-Key: <key>` on `PUT`/`PATCH /v1/sites/<sitename>/state`
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
| 401 | `{"error":"sign-in required to write","code":"visitor_auth_required","sign_in":"/v1/auth/oauth/providers","retry":true}` | Custom domain: no signed-in visitor and no key. Sign the visitor in, then retry once. Never returned on the shared host. |
| 403 | `{"error":"missing CSRF header","code":"csrf_required"}` | A session write without `X-SH-CSRF: 1`. The helper always sends it. |
| 401 | `{"error":"invalid API key","code":"invalid_api_key"}` | Unknown `X-API-Key`. Do not retry with the same key. |
| 401 | `{"error":"this site saves on its own domain","code":"use_custom_domain","domain":"recipes.brand.com"}` | Shared host, site has a custom domain: the shared-host API takes no writes for it, key or not (the shared-host page URL itself 302s to the domain). Pages: link the visitor to the same page on `domain`. Agents: write through the apex `https://simple-host.app/v1/...` or the domain's `/v1/`. Do not retry here. |
| 403 | (reads) | No `Origin` header on a non-browser read. Send one. |

On any of these: keep the form, never claim success, and never re-POST a
collection item after a partial write.
