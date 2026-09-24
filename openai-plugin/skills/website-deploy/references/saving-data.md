<!-- Derived from simple-host-website/skills/website-deploy/references/backend.md and internal/handler/static/auth.js. Keep in step. -->

# Saving and reading data: the page helper and the tools

Every site has one shared JSON **state** document and any number of append-only
**collections**. Pages use them through the hosted helper `https://simple-host.app/auth.js`;
you use them through the tools. Both are readable by anyone with the site's address, except a
collection the owner has made private (see "Private collections" below).

## Choosing the store

| Data | Store | Why |
|---|---|---|
| A count, a tally, votes per option, a setting, a short list the owner edits | state | one small document, atomic ops |
| One thing per visitor submission: RSVP, survey response, order, sign-up, guestbook entry, message | collection | unbounded, O(1) append, newest first, paged |
| A live total next to a collection | both: append the item, then `inc` a count in state | the page shows the count without paging everything |
| A draft, a cart, a preference, "already voted" on this device | `localStorage` in the page | per visitor; never shared |

State is capped at about 1 MB; a collection item at 64 KB. Anything that grows with every
visitor belongs in a collection, not in a state array.

## Page setup (every page that reads or saves)

```html
<div id="sh-auth"></div>          <!-- next to the form; the helper renders a small status box here -->
<script>window.SH_CONFIG = { site: "<site-name>" };</script>
<script src="https://simple-host.app/auth.js" defer></script>
<script>
window.addEventListener('DOMContentLoaded', function () {
  SH.mount('#sh-auth');
  // ... your code
});
</script>
```

- Set `window.SH_CONFIG` **before** the script tag. On the shared address the helper can work
  out the site from the URL, but on the site's own domain it cannot, so always set it.
- Call `SH.mount('#sh-auth')` on pages that save. On the shared address it currently renders
  one muted line; where sign-in is required it renders "Sign in with Google" plus an inline
  email-and-code form, and "Signed in as ... · Sign out" once signed in.
- The box can be themed with CSS variables `--sh-accent`, `--sh-muted`, `--sh-radius`.
- Results and admin pages only read, so they need the script and `SH_CONFIG` but not `mount`.

## The `SH` object

Writes:

- `await SH.requireSignIn()`: **put this before every save.** Resolves at once when no sign-in
  is needed or the visitor is already signed in. Otherwise brings the mounted sign-in box into
  view (or starts Google sign-in) and resolves after they sign in, so the save continues. It can
  reject: `code: "use_custom_domain"` (with `.domain`) when the site saves on its own domain, and
  `code: "no_sign_in"`. Keep it inside the same `try` as the save.
- `await SH.collection(name).append(item)`: append one JSON object. Resolves with the stored
  item `{ id, data, created_at }`.
- `await SH.state.patch(ops)`: atomic ops, applied in order:
  - `{op:'set', path:'a.b', value:1}`
  - `{op:'inc', path:'count', by:1}`
  - `{op:'append', path:'items', value:{...}}`
  - `{op:'remove', path:'a.b'}`
  - `{op:'removeWhere', path:'items', match:{id:'x'}}`
  Paths are dot paths (`totals.yes`, `votes.option-a`).
- `await SH.state.put(obj, {ifMatch: etag})`: replace the whole document. Rarely right from a
  page; prefer `patch`.

Reads (no sign-in, except a private collection: owner only):

- `const { data, etag } = await SH.state.get()`: `data` is the document (may be `null` or `{}`
  on a new site; default every field).
- `await SH.collection(name).list({ limit: 50 })` returns
  `{ items: [ { id, data: {...}, created_at } ], next }`, newest first. For older items call
  `list({ limit: 50, before: next })`; `next` is absent on the last page. Limit is 1-200.

Identity (rarely needed): `SH.ready`, `SH.me({fresh})` →
`{signed_in: true, email, ...}` or `{signed_in: false, ...}`, `SH.signOut()`.

Every write sends the right headers and cookies itself. A failed request rejects with an
`Error` carrying `.status`, `.code` and `.body`. The helper never retries and never re-sends.

## Save handler pattern

```js
form.onsubmit = async function (e) {
  e.preventDefault();
  const btn = form.querySelector('button[type=submit]');
  btn.disabled = true; status.textContent = 'Saving…';
  let appended = false;
  try {
    await SH.requireSignIn();
    await SH.collection('rsvps').append({
      name: form.name.value.trim(),
      attending: form.attending.value,
      guests: Number(form.guests.value) || 0
    });
    appended = true;
    await SH.state.patch([{ op: 'inc', path: 'totals.' + form.attending.value, by: 1 }]);
    form.reset(); localStorage.removeItem('rsvp.draft');
    status.textContent = 'Thanks, your RSVP is saved.';
  } catch (err) {
    status.textContent = appended
      ? 'Saved. (The live count did not update; it will catch up.)'
      : 'Not saved: ' + (err.code || err.status || err.message) + '. Please try again.';
  } finally { btn.disabled = false; }
};
```

- Keep the form filled on failure; never show success unless the append resolved.
- Never re-send an item after an error. If the append worked and the count patch failed, the
  item is saved; retry only the patch, or let the results page count from the collection.
- Save a draft to `localStorage` on input and restore it on load, with a key prefixed by the
  site name (all sites on the shared address share one browser origin).

## Results / admin page pattern

```js
async function loadAll(name) {
  const all = []; let before;
  do {
    const page = await SH.collection(name).list(before ? { limit: 200, before } : { limit: 200 });
    all.push(...page.items); before = page.next;
  } while (before && all.length < 2000);
  return all;
}
```

- Show a count, then a table or cards newest first (`created_at` formatted locally), with an
  empty state ("No RSVPs yet") and an error state.
- Escape everything visitors typed before inserting it: set `textContent`, never `innerHTML`
  with raw values.
- For surveys, aggregate client side: counts per option, averages for scales, latest text
  answers. A CSV download button (built in the browser) is a nice touch.
- Add `<meta name="robots" content="noindex">` and link it quietly from the main page's footer.
  For a public list, tell the person anyone with its address can open it. For personal details,
  use a private collection and the owner admin page below instead.

## Private collections (orders, RSVPs, sign-ups, anything personal)

A private collection takes submissions only from visitors signed in on the site's own address,
and only the site owner — and the Simple Host operator, for moderation — can read it. Pages stay public; the list is what is private. Public lists
(guestbook, votes, public comments) stay public.

### 1. Give the site its own address

Offer the free address first: `connect_domain` with `site` and `domain: "clay-studio.simple-host.app"`.
It answers `status: "active"` at once, with no DNS step, and the site is served at
`https://clay-studio.simple-host.app/`; its old shared address redirects there.

- `domain_taken` (409): another site has that name. Suggest another.
- `name_reserved` (400): a reserved name (`www`, `api`, `admin`, `mail` and others).
- `invalid_name` (400): one label only, lowercase letters, digits and hyphens, not starting or
  ending with a hyphen, up to 63 characters.

The person's own domain works the same way once active (`connect-domain` skill, one DNS
record). A site has one own address: claiming one replaces the other.

### 2. Make the collection private

Before the form goes live: `set_collection_privacy` `{site, collection: "orders", private: true}`.
It can be set before anything is saved. Without an own address it is refused with
`custom_domain_required`; do step 1 first (a domain still pending DNS does not count).

### 3. The form page

Same page setup as above (`SH_CONFIG`, `auth.js`, `SH.mount('#sh-auth')`). Sign in, append one
object, then show what was stored:

```html
<div id="sh-auth"></div>
<script>window.SH_CONFIG = { site: "clay-studio" };</script>
<script src="https://simple-host.app/auth.js" defer></script>
<script>
window.addEventListener('DOMContentLoaded', function () {
  SH.mount('#sh-auth');
  const form = document.querySelector('#order'), status = document.querySelector('#status');
  form.onsubmit = async function (e) {
    e.preventDefault();
    const btn = form.querySelector('button[type=submit]');
    btn.disabled = true; status.textContent = 'Sending…';
    try {
      await SH.requireSignIn();
      const saved = await SH.collection('orders').append({
        name: form.name.value.trim(),
        phone: form.phone.value.trim(),
        items: JSON.parse(localStorage.getItem('clay-studio.cart') || '[]')
      });
      localStorage.removeItem('clay-studio.cart'); form.reset();
      status.textContent = 'Order received from ' + saved.data._submitted_by +
        ' at ' + new Date(saved.data._submitted_at).toLocaleString() + '. We will be in touch.';
    } catch (err) {
      status.textContent = 'Not sent: ' + (err.code || err.status || err.message) + '. Please try again.';
    } finally { btn.disabled = false; }
  };
});
</script>
```

- The item must be one JSON object, up to 64 KB.
- The server stamps `_submitted_by` (the visitor's verified email) and `_submitted_at` (server
  time). Anything the page sends under those keys is replaced.
- The answer is the stored item with the stamps. Show it: it is the visitor's only copy, since
  visitors cannot read back their own submissions.
- Do not add a public count of names to state. A plain total is fine.

### 4. The owner admin page

`orders.html`, linked quietly or not at all, with `<meta name="robots" content="noindex">`. It
signs in, lists the collection, and lets the owner mark an item done or delete it, using each
item's `id`. All of this works only for the owner's account; everyone else gets 404.

```html
<div id="sh-auth"></div>
<p id="status">Loading…</p>
<table id="orders" hidden><thead><tr><th>When</th><th>From</th><th>Name</th><th>Phone</th><th>Status</th><th></th></tr></thead><tbody></tbody></table>
<script>window.SH_CONFIG = { site: "clay-studio" };</script>
<script src="https://simple-host.app/auth.js" defer></script>
<script>
window.addEventListener('DOMContentLoaded', async function () {
  SH.mount('#sh-auth');
  const status = document.querySelector('#status'), table = document.querySelector('#orders');
  try {
    await SH.requireSignIn();
    const r = await SH.collection('orders').list({ limit: 200 });
    if (!r.items.length) { status.textContent = 'No orders yet.'; return; }
    for (const it of r.items) {
      const tr = table.tBodies[0].insertRow();
      for (const v of [new Date(it.created_at).toLocaleString(), it.data._submitted_by, it.data.name, it.data.phone, it.data.status || 'new']) {
        tr.insertCell().textContent = v == null ? '' : String(v);
      }
      const actions = tr.insertCell();
      const done = document.createElement('button'); done.textContent = 'Mark done';
      done.onclick = async function () {
        try { const u = await SH.collection('orders').update(it.id, { status: 'done' }); tr.cells[4].textContent = u.data.status; }
        catch (err) { status.textContent = 'Not changed: ' + (err.code || err.status); }
      };
      const del = document.createElement('button'); del.textContent = 'Delete';
      del.onclick = async function () {
        if (!confirm('Delete this order for good?')) return;
        try { await SH.collection('orders').remove(it.id); tr.remove(); }
        catch (err) { status.textContent = 'Not deleted: ' + (err.code || err.status); }
      };
      actions.append(done, ' ', del);
    }
    status.textContent = r.items.length + ' orders, newest first.'; table.hidden = false;
  } catch (err) {
    status.textContent = err.status === 404
      ? "Sign in with the owner's account to see the orders."
      : 'Could not load: ' + (err.code || err.status || err.message);
  }
});
</script>
```

Page through older items with `before: r.next`, as in `loadAll` above.

- `SH.collection(name).update(id, fields)` merges `fields` into the item (a field sent as
  `null` is removed) and resolves with the item `{ id, data, created_at }`. `_submitted_by`,
  `_submitted_at` and `created_at` never change.
- `SH.collection(name).remove(id)` deletes the item for everyone. There is no undo.
- Both work only on a private list, only for the owner signed in on the site's own address.
  On a public list they are refused with 409 `append_only`.

The owner also sees the list in the dashboard and can download it as a spreadsheet (the stamp
columns appear like any other key). You read it with `read_collection` (its answer shows
`private: true` and each item's `id`) and change it with:

- `update_collection_item` `{site, collection, id, fields}`: e.g. `fields: {"status": "done"}`.
  Overwrites with no undo; `null` removes a field.
- `delete_collection_item` `{site, collection, id, confirm_id}`: `confirm_id` is the same id
  again. Call it only after the person has explicitly confirmed deleting that specific item.

### Who can do what with a private list

- **Add**: only a visitor signed in on the site's own address, from a page on that address.
  You cannot: `add_to_collection` and API keys are refused (`private_visitor_only`).
- **Read**: only the site owner — and the Simple Host operator, for moderation (tools,
  dashboard, spreadsheet, or the admin page signed in on the site's own address). Everyone else
  gets 404 `not_found`, including visitors who submitted.
- **Change or delete an item**: only the owner (and the operator, for moderation). Visitors can
  never edit or delete, not even their own items.
- **Make it public again**: `set_collection_privacy` with `private: false`. Everything already
  saved becomes readable by anyone. Confirm with the person first.

### On the shared address

Private lists need an own address. Without one, do not collect personal details: suggest an
email-order flow (a `mailto:` link, "email us to order"), or claiming the free
`<name>.simple-host.app` address, or connecting their domain.

## Doing it with the tools

- `list_collections` shows what a site has collected and how many items each holds.
- `read_collection` (site, collection, `limit` 1-200, `before` = previous `next`) to summarise
  or export submissions for the person. Works on private collections too (you are the owner).
- `set_collection_privacy` makes a collection private or public again (see above).
- `get_state` to read the document and its etag; `update_state` with `ops` (same ops as above)
  to fix a count or change a setting, or `replace` with `if_match` for a whole new document.
- `add_to_collection` appends one item to a public collection exactly as a page would (a
  private one refuses it). Appends are never undone:
  do not retry one that may have succeeded. Items in a public collection cannot be edited or
  deleted (it is append-only); if the person needs to hide entries, keep a `hidden` list of ids
  in state and filter on the results page. Items in a private collection can be changed with
  `update_collection_item` and deleted with `delete_collection_item` (after explicit
  confirmation of that item).

## Errors a page can meet

| `.code` / status | Meaning | Page should |
|---|---|---|
| `visitor_auth_required` (401) | a save needed a signed-in visitor | `SH.requireSignIn()` before saving prevents this; show "please sign in and try again" |
| `use_custom_domain` (401) | the site now lives on its own domain | link the visitor to the same page at `err.domain` |
| `csrf_required` (403) | the request missed the helper's header | send saves through `SH`, not raw `fetch` |
| `private_visitor_only` (403) | an API key or agent tried to add to a private list | only signed-in visitors add; not a page error |
| `private_needs_own_domain` (403) | a private list was sent to from another address | submit from a page on the site's own address |
| 400 | a private list got something other than one JSON object | send one object per item |
| `not_found` (404) | reading or changing a private list without being its owner | show "Sign in with the owner's account" |
| `append_only` (409) | `update`/`remove` on a public list | public lists cannot be changed |
| 413 | item (over 64 KB) or document too large | tell the visitor to shorten it |
| 429 | rate limited | ask the visitor to wait a moment |
| other | network or server error | keep the form, show the error, let them retry by hand |
