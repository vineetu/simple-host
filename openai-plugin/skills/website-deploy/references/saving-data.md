<!-- Derived from simple-host-website/skills/website-deploy/references/backend.md and internal/handler/static/auth.js. Keep in step. -->

# Saving and reading data: the page helper and the tools

Every site has one shared JSON **state** document and any number of append-only
**collections**. Pages use them through the hosted helper `https://simple-host.app/auth.js`;
you use them through the tools. Both are readable by anyone with the site's address.

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
- `await SH.collection(name).append(item)`: append one JSON object.
- `await SH.state.patch(ops)`: atomic ops, applied in order:
  - `{op:'set', path:'a.b', value:1}`
  - `{op:'inc', path:'count', by:1}`
  - `{op:'append', path:'items', value:{...}}`
  - `{op:'remove', path:'a.b'}`
  - `{op:'removeWhere', path:'items', match:{id:'x'}}`
  Paths are dot paths (`totals.yes`, `votes.option-a`).
- `await SH.state.put(obj, {ifMatch: etag})`: replace the whole document. Rarely right from a
  page; prefer `patch`.

Reads (no sign-in):

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
- Add `<meta name="robots" content="noindex">`, link it quietly from the main page's footer,
  and tell the person anyone with its address can open it.

## Doing it with the tools

- `list_collections` shows what a site has collected and how many items each holds.
- `read_collection` (site, collection, `limit` 1-200, `before` = previous `next`) to summarise
  or export submissions for the person.
- `get_state` to read the document and its etag; `update_state` with `ops` (same ops as above)
  to fix a count or change a setting, or `replace` with `if_match` for a whole new document.
- `add_to_collection` appends one item exactly as a page would. Appends are never undone:
  do not retry one that may have succeeded. Individual collection items cannot be edited or
  deleted with these tools; if the person needs to hide entries, keep a `hidden` list of ids in
  state and filter on the results page.

## Errors a page can meet

| `.code` / status | Meaning | Page should |
|---|---|---|
| `visitor_auth_required` (401) | a save needed a signed-in visitor | `SH.requireSignIn()` before saving prevents this; show "please sign in and try again" |
| `use_custom_domain` | the site now lives on its own domain | link the visitor to the same page at `err.domain` |
| 413 | item or document too large | tell the visitor to shorten it |
| 429 | rate limited | ask the visitor to wait a moment |
| other | network or server error | keep the form, show the error, let them retry by hand |
