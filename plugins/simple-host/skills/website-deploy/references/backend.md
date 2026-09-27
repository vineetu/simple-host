# The per-site backend: kinds, state and collections

Every site has a small JSON backend that its own page JavaScript can call. There
is no server for you to run. Every piece of saved data has a name and one kind
(below); every site also has one shared state document.

## Kinds: what is this data?

A name nobody declared is **Shared**: anyone can read it and anyone who signs in
can add to it (the lists and page data every site has always had). Page info and
Submissions are declared once, before the page saves to them. Anything with
personal details (RSVPs, orders, sign-ups) is private Submissions; anything only
the owner changes is Page info; when unsure, choose the stricter kind. An install
can require declaring every name (`SAVED_DATA_DEFAULT_KIND=declare_first`); there
an undeclared name takes no saves, the owner's included: 409 `declare_first`,
whose message names the call to make.

```
PUT /v1/sites/<sitename>/data/<name>/kind          (owner: X-API-Key; connector: declare_data)
{"kind": "entries"}                                 Submissions, private to the owner, daily email
{"kind": "entries", "visibility": "public"}         Submissions anyone can read (no email by default)
{"kind": "entries", "one_per_person": true}         one entry per signed-in visitor (votes, one RSVP each)
{"kind": "entries", "notify": "each"}               email: "daily", "each" (batched, soon after they arrive) or "off"
{"kind": "content"}                                 Page info
{"kind": "mine"}                                    Personal: one private record per signed-in visitor
{"kind": "board"}                                   Shared board: a list signed-in visitors edit together
```

A field left out keeps what the name had. `GET /v1/sites/<sitename>/data` (connector:
`list_data`) lists every name with its kind and settings, and who may save.

**Page info** (`content`): one JSON object that only the owner writes and everyone
reads — a menu, opening hours, prices, dashboard numbers. The owner saves it with
`PUT /v1/sites/<sitename>/data/<name>` (connector: `update_data`) or, signed in on the
site, `SH.data(name).set(obj)`; visitors get 403 `owner_only`. Pages read it with
`SH.data(name).get()` (null until saved), or `GET /v1/sites/<sitename>/data/<name>`
(`{name, kind, data, saved_at}`). A Page info document is at most 1 MB, and a site
declares at most 20 Page info names. Every save keeps the one before it (History in the
owner app; `data_history` / `restore_data` with the name as `collection`).

**Submissions** (`entries`): things visitors send — RSVPs, orders, sign-ups, votes,
comments, feedback.

- A signed-in visitor (allowed to save, below) adds one: `SH.data(name).add(obj)`,
  or `POST /v1/sites/<sitename>/data/<name>`. Each new entry is at most 16 KB, and one name holds at most 10,000 live entries (409 `list_full`).
- **Private to the owner by default**: the owner reads them all, with who sent
  each (`by`); everyone else gets 404 on the list. With `"visibility": "public"`
  anyone reads them, without who sent them.
- **Each visitor sees, changes and withdraws only their own**:
  `SH.data(name).mine()` (`GET .../data/<name>?mine=1`), `.update(id, fields)`
  (`PATCH .../data/<name>/items/<id>`: fields are merged in, null removes one,
  `_submitted_by`/`_submitted_at` never change), `.remove(id)` (`DELETE`; answers
  `{"withdrawn": id, "undo_minutes": 10}`), and `.undo(id)` (`POST .../items/<id>/undo`)
  brings back what they withdrew for 10 minutes. Anything that is not their own
  live entry is 404. The owner restores anything for 30 days.
- **One per person** (`"one_per_person": true`): a second entry from the same
  visitor is 409 `one_per_person` with the `id` of the one they have — change it
  with `.update(id, ...)` instead.
- `.list()` and `.count()` (`?count=1`) follow the list's read rule: the owner
  always, everyone when public.
- **Email to the owner** (`notify`): `daily` (a digest, the default for private
  Submissions), `each` (batched: at most one email per name every 10 minutes,
  counting what arrived), or `off` (the default for public ones). Each email has a
  one-click "stop these" link. The owner changes it in the owner app (Settings).

**Who may save here** (a site setting; every save from the site's pages — page
data, lists and Submissions): anyone who signs in (the default), or only the
listed emails and whole domains, plus a block list in either mode (403
`not_allowed_to_save`). The owner always may; a blocked visitor can still withdraw
their own entries.

A person here is a signed-in account, identified by the address its sign-in
verified (Google's, or the one an emailed code reached). Accounts are cheap to
make, so a block and one per person count a mailbox, not a human: a block also
covers that address with any `+tag` (`ann+2@` is `ann@`), and blocking by entry
blocks the address without its tag. An `@company.com` entry matches addresses at
exactly that domain (not its subdomains), for anyone who has verified such an
address, including a Google account created with it after they left.

```
PUT /v1/sites/<sitename>/savers      {"mode": "listed", "allow": ["@company.com", "ann@example.com"], "block": []}
PUT /v1/sites/<sitename>/savers      {"mode": "anyone", "block": ["spam@example.org"]}
POST /v1/sites/<sitename>/savers/block   {"email": "..."}  or  {"collection": "rsvps", "id": 12}
```

Connector: `set_who_can_save` (replaces both lists; read them with `list_data`
first) and `block_person`. The owner app has "Who may save here" and a "Block"
button next to any entry with a sender.

**The helper**, with the kind checked on first use (a mismatch rejects with
`code: "wrong_kind"`; an undeclared (Shared) name with `code: "declare_first"`, so
a page that asked for Submissions never saves to a public list by mistake;
`SH.data(name)` with no kind uses a Shared name as it is):

```js
const rsvps = SH.data('rsvps', 'entries');
await SH.requireSignIn();
const mine = await rsvps.add({ name: 'Ann', guests: 2 });   // the stored entry, with its id
const { items } = await rsvps.mine();                      // this visitor's own
await rsvps.update(mine.id, { guests: 3 });
await rsvps.remove(mine.id);   // withdraw; await rsvps.undo(mine.id) brings it back
const menu = await SH.data('menu', 'content').get();       // Page info, for everyone
```

`SH.data` writes carry an `Idempotency-Key` and are retried once, with the same
key, after a network error, so they are saved once. Never re-send by hand.

### Personal (`mine`): each visitor's own record

One private JSON object per signed-in visitor per name — a habit tracker, saved
progress in a course or game, preferences, a reading list — kept on the server
against their sign-in, so it follows them to their phone and laptop. Declare the
name while it is still empty (a name that already holds data answers 409
`has_entries`).

- **Only that visitor** writes it, signed in on the site's own address. Other
  visitors each have their own. Simple Host's owner tools never show a person's Personal record; the site's own pages run in the visitor's browser and can read that visitor's record, so only use Personal on sites you trust. Simple Host's side of that: the owner's
  key, the connector (`read_collection`, `data_history`, `list_deleted`), the
  owner app and the site's download all answer 403 `personal_data` or leave it
  out. The owner sees how many people have a record and their total size
  (`list_data`, `GET .../data/<name>?count=1`), from 3 people up (with one or
  two: `few: true`, no numbers), and can clear the name for everyone
  (`clear_collection`, after the owner confirms); Restore in the owner app then
  brings back what that clear took, never a record its person deleted.
- **Never write a page that sends a Personal record, or anything read from it, anywhere else: not to another data name, not to another site or service.** A page reads the record
  only to show it to that visitor or to change it.
- A personal record is at most 64 KB (413 `item_too_large`). One name holds
  records for at most 1,000 people (409 `people_full` for someone new; people who
  have a record keep saving).
- It is in the visitor's own "Download my data", and is erased with their account.
- A Personal name that holds records never becomes another kind (409
  `has_records`): pick a new name instead.

```js
const me = SH.data('habits', 'personal');
await SH.requireSignIn();
const rec = (await me.get()) || { streak: 0, days: [] };  // null until the first save
await me.set({ streak: 1, days: ['2026-09-27'] });        // PUT: the whole record
await me.set('theme', 'dark');                           // one field
await me.inc('streak');                                  // PATCH with the /state ops
await me.patch([{ op: 'append', path: 'days', value: '2026-09-28' }]);
await me.clear();                                        // DELETE their record
const { history } = await me.history();                  // their own changes (30-day undo)
await me.restore(history[0].id);                         // back to before that change
```

REST: `GET`/`PUT`/`PATCH`/`DELETE /v1/sites/<sitename>/data/<name>` (visitor
cookie, `X-SH-CSRF: 1` on writes), `GET .../data/<name>/history`,
`POST .../data/<name>/history/<id>/restore`. A `GET` answers
`{name, kind, data, version}` with an `ETag`.

### Shared board (`board`): a list everyone edits

A list a group keeps together — a shared shopping list, a kanban, a potluck
sign-up, a team's to-dos. Anyone who can open the site reads it (who added each
item stays the owner's to see); signed-in visitors allowed to save add items
and change or delete **any** item, one at a time. Only the owner empties it
(`clear_collection`); nobody else can touch more than one item per call.

- Items are JSON objects. A board item is at most 16 KB, and a board holds at most 2,000 live items (409 `list_full`).
- Every item has a `version`. Pass it when changing an item; if someone changed
  it first you get 409 `version_conflict` with `item` as it is now — show it,
  and let the visitor apply their change again.
- Whoever deleted an item can bring it back for a few minutes (`undo`); the
  owner restores anything from History and Recently deleted, never past the
  board's cap. The owner's Restore all on a board names a window
  (`{"all": true, "within_minutes": 60}`), so after someone deletes the lot it
  brings back what went since then, not what people deleted on purpose before.
- Adds, changes and deletes are rate-limited per address and per signed-in
  person (429 `rate_limited`). `list()` and `watch()` read every page of the
  board.
- There is no live feed: `watch` polls, and an unchanged board answers 304, so
  polling every few seconds is cheap.

```js
const todo = SH.data('todo', 'board');
const { items } = await todo.list();                 // anyone
await SH.requireSignIn();
const it = await todo.add({ text: 'milk', done: false });
try {
  await todo.update(it.id, { done: true }, { version: it.version });
} catch (e) {
  if (e.code === 'version_conflict') showLatest(e.body.item);  // someone was first
}
await todo.remove(it.id);                            // await todo.undo(it.id) brings it back
const stop = todo.watch(items => render(items), { every: 5000 });
```

REST: `GET`/`POST /v1/sites/<sitename>/data/<name>`,
`PATCH`/`DELETE .../data/<name>/items/<id>` (`If-Match: "<version>"` on PATCH),
`POST .../items/<id>/undo`; reads send `If-None-Match` for a 304.

**What does not fit** (say so rather than approximating it): roles, per-field
rules, joins, search, live co-editing of one object, or instant updates.
Per-visitor things that stay on one device (a draft) belong in `localStorage`;
ones that should follow the visitor to another device are Personal.

| Status | Code | Meaning |
|---|---|---|
| 409 | `declare_first` | The name has no kind yet and must be declared here (or the page asked for a kind). Declare it (`declare_data`), then save. |
| 409 | `confirm_public` | Declaring (or `{"private": false}`) would make this private name's entries public (`count` says how many). Ask the owner; resend with `"confirm_public": true` only if they agree. |
| 409 | `visitor_sign_in_off` | This install reads no visitor sign-in on public saves (`WRITE_AUTH_MODE=off`), so public Submissions could never take an entry. Keep them private, or leave the name Shared. |
| 401 | `visitor_auth_required` | Submissions come only from a signed-in visitor. Call `SH.requireSignIn()` first. |
| 409 | `wrong_kind` | Page info takes no entries (the owner PUTs the document); Submissions are not PUT. |
| 403 | `owner_only` | Only the owner changes Page info. |
| 409 | `one_per_person` | This visitor already has an entry (`id` in the body): update it instead. A person is the signed-in account, and the same address with any `+tag`; an entry from another account of the same address has no `id`. |
| 409 | `list_full` | The name holds as many entries as it may. The owner deletes or clears. |
| 403 | `not_allowed_to_save` | The owner has not allowed this account to save here (or blocked it). Tell the visitor; do not retry. |
| 409 | `undo_expired` | Only a visitor's own withdrawal, within the window, comes back; the owner restores older ones. |
| 413 | `item_too_large` | Over the entry, Page info, personal record or board item size above. |
| 403 | `personal_data` | A Personal record is written only by its own visitor, signed in on the site; the owner's tools get counts only. |
| 409 | `people_full` | This Personal name already keeps records for as many people as it may; nobody new can start one. Tell the visitor. |
| 409 | `has_records` | A Personal name that holds records cannot become another kind. Use a new name. |
| 409 | `has_entries` | The name holds data, so it cannot become Personal (or several entries cannot become Page info). Use a new name. |
| 409 | `version_conflict` | A board item changed since the version sent; `item` in the body is how it is now. |

## Trust model

Reads are public: anyone with the link can read a site's Page info, its state
and its public lists. The one exception is private Submissions (a **private
collection**, see "Private collections" below): visitors add to it, only the site
owner reads it all, and each visitor reads their own.
**Writes need an identity.** Each site lives at its own address,
`https://<site>.<handle>.simple-host.app/` (use the `site_url` the API returned; a
brand-new account's sites briefly use `https://<handle>.simple-host.app/<site>/`
until its certificate is issued). That address is the site's own browser origin:
visitors sign in with Google or an emailed code there, a sign-in covers that site
only, and every save from a page needs a signed-in visitor. Agents save with the
owner's API key (or the connector) on the owner's own sites. Old `<handle>.simple-host.app/<site>/` and
`sites.simple-host.app/<handle>/<site>/` links redirect to the site's address.

A site can also take a nicer address — a free `<name>.simple-host.app` or a
custom domain (the `connect-domain` skill); both behave the same here. Once one
is bound, the site lives only there. Its `<site>.<handle>.simple-host.app/...`
page URL answers 302 to `https://<domain>/...` (same path and query), and state
and collection writes there answer 401 `use_custom_domain` (with the `domain`)
whether or not an `X-API-Key` is sent. Reads there stay public. Agents write
through the apex `https://simple-host.app/v1/...` with a key (what this skill
already does) or through the domain's own `/v1/` (key or session).

The plain-`fetch` shape, same-origin on the site's own address (the `SH` helper
below sends the same headers for you):

```js
const API = '/v1/sites/<sitename>';   // same-origin on <sitename>.<handle>.simple-host.app or the site's domain
await fetch(API + '/state', { method: 'PATCH', credentials: 'include',
  headers: { 'Content-Type': 'application/json', 'X-SH-CSRF': '1' },
  body: JSON.stringify({ ops: [{ op: 'inc', path: 'count', by: 1 }] }) });
await fetch(API + '/collections/entries', { method: 'POST', credentials: 'include',
  headers: { 'Content-Type': 'application/json', 'X-SH-CSRF': '1' },
  body: JSON.stringify({ text: 'hello' }) });
```

Reads are gated on the request `Origin`, which a browser page sends by itself;
a `curl` or script with no `Origin` gets 403 on reads, so send one:
`curl -H "Origin: https://<sitename>.<handle>.simple-host.app" https://<sitename>.<handle>.simple-host.app/v1/sites/<sitename>/state`.

## Shared JSON state (one document per site)

A page calls `/v1/sites/<sitename>/state` on its own address (same origin).
Agents call the apex `https://simple-host.app/v1/...` with a key, where the
user-scoped `/v1/u/<handle>/sites/<sitename>/...` twins work too.

```
GET   /v1/sites/<sitename>/state
PUT   /v1/sites/<sitename>/state    # replace whole document (optional If-Match: <etag>)
PATCH /v1/sites/<sitename>/state    # atomic ops — use these
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
Numbers keep their exact digits. Reads are limited to 30 a second per visitor
address (429 `rate_limited`): poll every few seconds, not in a tight loop.

Every change is kept for 30 days: the owner sees who changed what and when, and
can put an earlier version back (owner app **History**; connector
`data_history` and `restore_data`; `GET /v1/sites/<sitename>/state/history`,
`GET .../state/history/<id>` for the value before that change,
`POST .../state/history/<id>/restore`, all with the owner's key). A wiped or
overwritten document is recoverable, but still prefer `PATCH` ops to a whole
`PUT`. `DELETE /v1/sites/<sitename>/history` with `{"confirm": "<sitename>"}`
(connector `delete_forever` with `history: true`) clears every earlier version
for good, when the person asks for that; the data as it is now stays.

A site's live saved data (this document plus its list items) is capped at 50 MB.
Only a write that grows it is refused (507 `site_full`); history and Recently
deleted do not count, and deleting items or clearing a list makes room at once.

For **per-visitor** state (a draft, a preference, a dismissed banner) use
`localStorage` in the page instead — it never belongs in shared state.

## Collections (growing lists)

For sign-ups, RSVPs, submissions — O(1) append, paginated reads:

```
POST /v1/sites/<sitename>/collections/<name>            # append one JSON item (≤ 64 KB)
GET  /v1/sites/<sitename>/collections/<name>?limit=50   # newest-first
```

Response shape: `{ items: [ { id, data: {…}, created_at } ], next }`. When a
page fills, `next` is the cursor for the older page: `?limit=50&before=<next>`;
`next` is absent on the last page.

If an append succeeds and a follow-up `state` patch (a live count) fails, retry
only the patch — never re-append. A write that may have landed can be retried
safely with the same `Idempotency-Key` header (any unique string per write,
e.g. `crypto.randomUUID()`, never a fixed name like `'vote-a'`): a POST or PATCH
retried with the same key and body by the same signed-in person (or the owner's
key) is saved once (`Idempotent-Replayed: true`; an append answers with its item,
a PATCH with the document as it is now). The same key with another body is 409
`idempotency_key_reused`. Writes made without signing in ignore the header.
Items added without the owner's key are limited to 30 a minute per address
(429 `rate_limited`).

Every item sent by a signed-in visitor records who sent it. The owner sees it
(`by` on each item in the owner's reads, a `sent_by` column in the CSV); public
reads and the POST answer never show it.

Visitors only append. The site owner can remove entries from any list, public
included (spam in a guestbook, test entries before launch): one item with
`DELETE /v1/sites/<sitename>/collections/<name>/items/<id>`, or the whole list
with `DELETE /v1/sites/<sitename>/collections/<name>` and the body
`{"confirm": "<name>"}` (answers `{"deleted": <n>, "restorable_days": 30}`; 400
`confirm_required` without the matching name). Connector: `delete_collection_item`,
`clear_collection`. The owner app on the person's page does both too. Confirm
with the person before either. Deleted entries stay in the list's **Recently
deleted** for 30 days: `GET .../collections/<name>/deleted`,
`POST .../collections/<name>/items/<id>/restore` for one, or
`POST .../collections/<name>/deleted/restore` with `{"all": true}` to undo a clear
(add `"within_minutes": n` for only what went in the last n minutes; a Shared
board needs it)
(connector `list_deleted`, `restore_item`; owner app Recently deleted). Every
edit, delete and clear is in `GET .../collections/<name>/history` and can be
undone with `POST .../history/<id>/restore` (`data_history`, `restore_data`).
Recently deleted pages with `?before=<next>` (`next` is an opaque cursor). To
remove something for good sooner (a visitor asked to be erased, a flood of spam):
`DELETE .../collections/<name>/deleted/<id>` for one item already in Recently
deleted, or `DELETE .../collections/<name>/deleted` with `{"confirm": "<name>"}`
for all of it (connector `delete_forever`; owner app **Delete forever**). It
cannot be undone: confirm exactly what with the person first.

**Pair every form with a viewer page.** A form with nowhere to read the results
is half a feature. Add a second page (e.g. `admin.html`) that GETs the collection
and lists every entry newest-first, link to it quietly from the main page, and
put `<meta name="robots" content="noindex">` in its head. Do not build a fake
password gate: a public collection is readable by anyone with the link, so say
that in one small line instead. If the entries hold personal details, use a
private collection and an owner page instead (below).

## Saving from a page with the hosted helper

The page loads the hosted helper, offers sign-in next to the form, and signs the
visitor in before every save. On `<sitename>.<handle>.simple-host.app` the helper
finds the site from the host name (on the fallback `<handle>.simple-host.app/<sitename>/`,
from the page path). For a site that has a domain bound, visited on its
previous address, `SH.mount` renders "This site saves on <domain>. Sign in there
to save." with a link to the same page on the domain, and `SH.requireSignIn()`
rejects with `code: "use_custom_domain"` and `.domain`. Because a custom-domain
URL does not carry the site name, set `window.SH_CONFIG` before the script tag
(it works on every address, so the examples always set it).

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
  sign-in and the promise never resolves.
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
navigate to `/v1/visitor/oauth/google?return_to=` +
`encodeURIComponent(location.href)` on the site's own address (same origin: the
sign-in is tied to the browser that starts it).

## Private collections (orders, RSVPs, anything personal)

Use a private collection when a form collects orders, RSVPs, survey answers,
sign-ups, or anything with names, emails, phone numbers or addresses. Visitors
signed in on the site's own address add to it: its
`<site>.<handle>.simple-host.app` address, or its domain if it has one. Only the site owner — and the
Simple Host operator, for moderation — can read it. Everyone else gets 404.
Pages stay public; only the list is private.

Public lists (a guestbook, votes, public comments) stay public. Say so plainly
when you build one.

### 1. Declare it as private Submissions

Do this before the form goes live. It works before any item exists. Private is
the default for Submissions, so with the connector: `declare_data` with
`kind: "entries"`. Without it:

```
PUT /v1/sites/<sitename>/data/orders/kind
X-API-Key: <api_key>
{"kind": "entries"}
```

It answers 200 with `"visibility": "owner"`, `"notify": "daily"` and a one-line
`message`. A list can also be made private with
`set_collection_privacy` (`PUT /v1/sites/<sitename>/collections/orders/privacy`
with `{"private": true}`).
Only signed-in visitors can submit, and only you read them all; each visitor sees,
changes and withdraws their own.

`{"visibility": "public"}` (or `{"private": false}`) makes the list public again,
and everything already saved in it becomes readable by anyone. Confirm with the
owner before sending it: both refuse it while the list holds entries
(409 `confirm_public`, with the `count`) until you add `"confirm_public": true`.
A private list that holds entries never becomes Page info (409 `has_entries`):
use another name.

### 2. The form page

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

### 3. The owner page

Add a page on the site, e.g. `orders.html`, that signs in, lists the
collection, and lets the owner mark an order done or delete it. It works only
when the owner's own account is signed in on the site's own address; anyone else gets 404
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

### Editing and deleting items (owner only)

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
- **DELETE** answers 204. The item leaves the list at once and stays in its
  Recently deleted for 30 days, where the owner can restore it; confirm with the
  owner first. An edit can be undone from the list's history the same way.
- **Who:** the site owner — with `X-API-Key`, the connector
  (`update_collection_item`, `delete_collection_item`), or the owner's own
  sign-in on the site's own address from a page there (send `X-SH-CSRF: 1`; the
  helper does) — and the Simple Host operator, for moderation. Everyone else,
  including the visitor who submitted the item, gets 404 `not_found`.
- **Public lists:** DELETE works there too (spam); PATCH answers 409
  `append_only`, because a public entry stays what its visitor wrote.

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

Key reads of a private list go through the apex `https://simple-host.app/v1/...`;
the old `sites.simple-host.app` address answers 404 for it, even with a key.

### Errors when adding to a private list

| Status | Code | Meaning |
|---|---|---|
| 401 | `visitor_auth_required` | Not signed in. `SH.requireSignIn()` handles it. |
| 403 | `csrf_required` | Missing `X-SH-CSRF: 1`. The helper always sends it. |
| 403 | `private_visitor_only` | Sent with an API key, or by an agent (`add_to_collection`). Agents cannot add to a private list; only signed-in visitors can. |
| 403 | `private_needs_own_domain` | Sent from anywhere other than the site's own address (`<site>.<handle>.simple-host.app` or, while a new account uses it, the `<handle>.simple-host.app/<site>/` fallback; or its domain if it has one). |
| 401 | `use_custom_domain` (+ `domain`) | The site has a domain and this was sent through its previous address. Link the visitor to the same page on `domain`. |
| 400 | — | The item is not one JSON object. |
| 413 | `item_too_large` | The item is over 64 KB. |

## Saving from an agent (API key)

A key writes only the sites its own account owns: the owner's API key (or the
connector signed in as the owner) writes the site's state and public
collections. Another account's key gets 404 `site not found`, exactly as if the
site did not exist, and changes nothing. A private collection takes no writes from a key or an
agent (403 `private_visitor_only`). Send `X-API-Key: <key>` on `PUT`/`PATCH /v1/sites/<sitename>/state`
and `POST /v1/sites/<sitename>/collections/<name>` (or the
`/v1/u/<handle>/sites/<sitename>/...` twins).

Someone who does not own the site saves the way any visitor does: on the site's
own page, signed in. No key writes someone else's site.

An agent working for the owner without the connector gets the owner's key by
email code:

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
management. An agent that already holds the owner's key needs none of this.

## Error bodies

| Status | Body | Meaning |
|---|---|---|
| 401 | `{"error":"sign-in required to write","code":"visitor_auth_required","sign_in":"/v1/auth/oauth/providers","retry":true}` | No signed-in visitor and no key. Sign the visitor in, then retry once. |
| 403 | `{"error":"missing CSRF header","code":"csrf_required"}` | A session write without `X-SH-CSRF: 1`. The helper always sends it. |
| 401 | `{"error":"invalid API key","code":"invalid_api_key"}` | Unknown `X-API-Key`. Do not retry with the same key. |
| 404 | `{"error":"site not found"}` | On a write with a key: the key's account does not own this site (or it does not exist). Use the owner's key; do not retry. |
| 401 | `{"error":"this site saves on its own domain","code":"use_custom_domain","domain":"recipes.brand.com"}` | The site has a domain and this was sent through its previous address: that address takes no writes for it, key or not (its page URL itself 302s to the domain). Pages: link the visitor to the same page on `domain`. Agents: write through the apex `https://simple-host.app/v1/...` or the domain's `/v1/`. Do not retry here. |
| 403 | (reads) | No `Origin` header on a non-browser read. Send one. |
| 413 | `{"error":"item too large","code":"item_too_large"}` | Over 64 KB (an item) or 1 MB (the document). |
| 429 | `{"error":"rate limit exceeded, slow down","code":"rate_limited"}` | Too many requests from this address. Wait (`Retry-After`) and poll less often. |
| 507 | `{"error":"…","code":"site_full"}` | The write would grow the site's live saved data (page data plus list items) past 50 MB. The owner deletes items or clears a list; writes that do not grow it still go through. |
| 409 | `{"code":"idempotency_in_progress"}` | The first request with this `Idempotency-Key` is still being saved. Retry in a moment with the same key. |
| 409 | `{"code":"idempotency_key_reused"}` | This `Idempotency-Key` was used for a different body. Use a new key for a new write. |

On any of these: keep the form, never claim success, and never re-POST an
entry by hand after a partial write. The kinds' own codes (`declare_first`, `confirm_public`,
`wrong_kind`, `owner_only`, `one_per_person`, `list_full`, `not_allowed_to_save`,
`undo_expired`) are in the table under "Kinds" above.
