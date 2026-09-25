---
name: website-deploy-builder
description: Plan what to build on Website Deploy (simple-host.app). Helps a user decide whether their idea fits the static + light-backend model, maps it to concrete patterns (shared JSON state with atomic ops, append-only collections, localStorage, public APIs), and produces a focused prompt for an implementation agent. Knows the planning rules that matter - every site lives at https://<handle>.simple-host.app/<site>/, where visitors sign in (Google or an emailed code) before saving from a page; anything personal (orders, RSVPs, sign-ups) goes in a private collection that only the owner can read; a free <name>.simple-host.app or a custom domain is an optional nicer address. Use when a user is starting a new site or describes a feature idea and needs help mapping it to what the platform can do.
---

# Website Deploy Builder

**First rule: use the Simple Host tools when you have them.** If the Simple Host
connector's tools are available in this session (`who_am_i`, `list_sites`,
`create_site` / `update_site` (`deploy_site` on older connections), `get_state`,
`connect_domain`, …), use them for everything and never ask the person for an
email, a code or an API key — the connector is already signed in as them. Sign-in
itself is unchanged: when the person connects Simple Host in their AI app, a
Simple Host sign-in window opens, they sign in with Google or the emailed code,
then choose Allow, and every chat after that is signed in. If a tool reports the
connection is not signed in, ask them to reconnect Simple Host in their app's
settings. Only when those tools are not available (e.g. a coding agent without
the connector) use the email-code and API-key flow the `website-deploy` skill describes.

Use this skill when a user wants help deciding what to build on Website Deploy, or how to scope an idea they already have. After the user picks an approach, hand off to the `website-deploy` skill for deploy.

## What Website Deploy gives you

Website Deploy is a static-file host at `https://simple-host.app`. Every account gets its own address, `https://<handle>.simple-host.app/`, and each site lives at `https://<handle>.simple-host.app/<sitename>/` (`handle` is the owner's URL-safe handle from GET `/v1/me`). The dashboard/API stay on `https://simple-host.app` (a separate origin). Old `sites.simple-host.app/<handle>/<site>/` links keep working. There is no server-side execution — but the API gives each site a real, server-backed backend:

| Capability | How |
|---|---|
| HTML / CSS / JS / images / fonts served as a site | Deploy files inline as JSON (`/files`) or upload a `.tar.gz`/`.zip`. With the connector: `create_site` / `update_site` (`deploy_site` on older connections) |
| Per-site JSON state (≤ 1 MB, shared across all visitors) | `GET / PUT /v1/sites/<sitename>/state` (same-origin from the page; agents can also use `/v1/u/<handle>/sites/<sitename>/state` on the apex). Reads public; a page write needs the visitor signed in first (`auth.js`) |
| Atomic state updates (concurrent-safe counters, lists, votes) | `PATCH .../state` with `{ops:[inc/append/set/remove/removeWhere]}`; `If-None-Match` ETag for cheap polling. A write — same rule as above |
| Append-only collections (signups / RSVPs / submissions) | `POST/GET /v1/sites/<sitename>/collections/<name>`. GET public; POST is a write |
| Private collections (orders, RSVPs, anything personal) | `set_collection_privacy` (or `PUT .../collections/<name>/privacy` `{"private":true}`). Signed-in visitors add; only the site owner — and the Simple Host operator, for moderation — can read it. The owner can edit or delete items (`update` / `remove`); public lists stay append-only |
| A nicer address (optional) | Free `<name>.simple-host.app`: one call (`connect_domain`), active at once, no DNS. Or a custom domain via the `connect-domain` skill (one DNS record). The site moves there and its old address redirects |
| Agent writing for a person (no browser) | The connector (`update_state`, `add_to_collection`) if present; otherwise that person's own API key, obtained by email code, as `X-API-Key` — works on any site. See "Saving from an agent" in the `website-deploy` skill's `references/backend.md` |
| Per-visitor state | `localStorage`, `sessionStorage`, `IndexedDB` (in the browser) |
| External APIs | `fetch()` from the page to any public CORS-enabled API |
| Routing | Static files only — path-relative directories with `index.html`; SPA routing via the framework's hash router or `404.html` fallback |

If your idea needs a server you control, a shared SQL database, persistent per-user accounts, or anything that runs server-side, Website Deploy is not the right host. Say so and stop.

**Anyone can read; saving from a page needs sign-in.** Visitors sign in with Google or an emailed code on the site's own address; every save from a page needs a signed-in visitor. Agents save with the API key (or the connector). Say this up front, before the page is written, so the form gets its sign-in box.

**Anything personal goes in a private collection.** Orders, RSVPs, survey answers, sign-ups, or anything with names, emails, phone numbers or addresses. A collection can be made private: only signed-in visitors can submit, and only the owner can read it. Plan it in this order:

1. Make the collection private (`set_collection_privacy`) before the form goes live.
2. The form page calls `await SH.requireSignIn()` before `SH.collection('orders').append({...})`, and shows the saved item from the answer as the visitor's receipt.
3. An owner page on the site (e.g. `orders.html`) that signs in, lists the collection, and has "Mark done" (`SH.collection('orders').update(id, {status:'done'})`) and "Delete" (`.remove(id)`) buttons. It works only for the owner's account. The owner also has the dashboard and a spreadsheet download; the agent reads it with `read_collection`.

Public lists (a guestbook, votes, public comments) stay public; say so plainly. Pages are always public; only a private collection is closed, readable by the site owner and the Simple Host operator (for moderation).

**Always pair a form with a viewer.** Any site that COLLECTS data (a signup, RSVP, guestbook, contact form, order) MUST also ship a second page — e.g. `admin.html` — that reads the same collection back (`GET .../collections/<name>?limit=200` → `{items:[{id,data,created_at},…]}`) and lists every entry for the owner, newest first, plus the live total from state. Link it quietly from the main page (a small "Organizer view →" in the footer). A form with nowhere to read the results is only half the feature — and the person you're building for will not think to ask for the viewer, so add it by default. Mark the viewer `<meta name="robots" content="noindex">`. A public collection is readable by anyone with the link, so don't fake a password; if the entries are personal, make the collection private and the viewer becomes the owner page above.

## How to use this skill

1. Ask the user what they're trying to build, in plain language. Don't push capabilities at them — let them describe the idea.
2. Decide whether it can run as a static site. If parts of it can't, name those parts and either propose a static-friendly substitute or recommend a different host for that piece.
3. If visitors will save anything, say now that they sign in first. If the saves hold personal details, plan a private collection and an owner page.
4. For the part that can run statically, give them: (a) a one-paragraph explanation of how to structure it, (b) any relevant snippet (storage, routing, external API call), (c) the gotchas.
5. If they're starting from scratch, finish with a "ready to deploy" handoff: tell them to use the `website-deploy` skill, which handles registration (only without the connector), framework-aware build, packaging, and upload.
6. If they want to wire a capability into a site they've already deployed, generate a focused prompt they can paste into a fresh agent chat (in their site's repo). Include the pattern, the storage shape, and any gotcha — nothing else.

## Capability tree

### 1. Static hosting (the baseline)

What it is: any folder of HTML/CSS/JS/assets served as-is. Build any framework's normal production output (`dist/`, `build/`, `out/`, `public/`, `.output/public/`) and upload.

When to choose: every Website Deploy site starts here. Deploy first, then layer storage and external calls.

Gotchas: the site lives under `/<sitename>/` on the person address, so **relative links are required**. Root-absolute paths like `/css/app.css` resolve to the wrong place and break — use `css/app.css`, `./img/x.png`, `../shared/y`. For framework builds, set the base/public path so output uses relative URLs (e.g. Vite `base: './'`, Next `basePath` / relative assets, etc.). Don't ship `node_modules/` or `.env`. Each archive is capped at 100 MB.

### 2. Per-site JSON state (shared across visitors)

What it is: a single JSON document (up to 1 MB) scoped to your site. The server stores it in Postgres; your site reads and writes it from the browser. The document is shared across **everyone** who visits — use `PATCH` ops so concurrent writers don't clobber each other.

When to choose: anything you'd want a tiny key-value store for — a shared note, a counter, a vote tally, content the page generated, configuration. Reading is public. **Writing from a page needs sign-in**: the page loads `https://simple-host.app/auth.js`, sets `window.SH_CONFIG = { site: "<sitename>" }` (needed on a custom domain, harmless everywhere), and calls `await SH.requireSignIn()` before each write — the visitor signs in with Google or an emailed code. The session is site-scoped and is not an API key. Sign-in gates writing only — it does not make the page private. If you need per-visitor data, store it under different keys inside the document, keyed on something like `crypto.randomUUID()` saved in `localStorage`.

How to use, from a page on the site:

```html
<div id="sh-auth"></div>
<form id="f"><textarea name="draft"></textarea><button>Save</button></form>
<p id="status"></p>
<script>window.SH_CONFIG = { site: "<sitename>" };</script>
<script src="https://simple-host.app/auth.js" defer></script>
<script>
window.addEventListener('DOMContentLoaded', async function () {
  SH.mount('#sh-auth');                              // Google sign-in + email-code form
  const status = document.getElementById('status');
  const form = document.getElementById('f');

  // load — public, no sign-in
  const { data } = await SH.state.get();
  form.draft.value = data.draft || '';

  // save — sign in first, then write; keep the form on failure
  form.onsubmit = async function (e) {
    e.preventDefault();
    await SH.requireSignIn();                        // signs the visitor in if needed
    try {
      await SH.state.patch([{ op: 'set', path: 'draft', value: form.draft.value }]);
      status.textContent = 'Saved';
    } catch (err) {
      status.textContent = 'Not saved: ' + (err.code || err.status);   // never claim success
    }
  };
});
</script>
```

Full `SH` API (`SH.state`, `SH.collection(name).append/list`, `SH.me`, `SH.signOut`) and the error bodies are in the `website-deploy` skill's `references/backend.md`.

Gotchas: state is public to anyone with the link; never keep personal details in it (use a private collection). Body cap is 1 MB; sending more returns 413.

### 3. Per-visitor state with `localStorage`

What it is: small JSON blobs stored in the visitor's browser, scoped to the page's origin (`<handle>.simple-host.app` — shared across that person's sites; a custom domain gets its own origin).

When to choose: anything you'd want a tiny key-value store for in a single-visitor experience — drafts, settings, app state, the user's progress. Per-visitor only; there is no sharing across browsers or devices.

```js
// save
localStorage.setItem('myapp.state', JSON.stringify(state));

// load
const raw = localStorage.getItem('myapp.state');
const state = raw ? JSON.parse(raw) : {};
```

Gotchas: typical browser quota is ~5 MB per origin. Cleared by the user at any time. On `<handle>.simple-host.app`, the person's other sites share the same origin and can see the same storage — prefix your keys. For multi-megabyte structured data, use `IndexedDB` instead.

### 4. Larger per-visitor state with `IndexedDB`

When `localStorage`'s ~5 MB cap is too small or you have a lot of small records, use `IndexedDB` directly. It is built into every browser, so no library or CDN import is needed.

```js
function openDB() {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open('myapp', 1);
    req.onupgradeneeded = () => req.result.createObjectStore('items', { keyPath: 'id' });
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}
function run(db, mode, fn) {
  return new Promise((resolve, reject) => {
    const tx = db.transaction('items', mode);
    const req = fn(tx.objectStore('items'));
    tx.oncomplete = () => resolve(req.result);
    tx.onerror = () => reject(tx.error);
  });
}
const db = await openDB();
await run(db, 'readwrite', s => s.put({ id: 'a', text: 'hello' }));
const item = await run(db, 'readonly', s => s.get('a'));
```

Gotchas: same per-origin / per-visitor scoping as `localStorage`. Cleared if the user clears site data.

### 5. Calling external APIs from the browser

What it is: `fetch()` from your page directly to any public HTTPS API that returns CORS-friendly responses.

When to choose: pulling in public data (weather, Wikipedia, public LLM APIs the user provides their own key for, etc.).

```js
const r = await fetch('https://api.example.com/v1/things');
const data = await r.json();
```

Gotchas:
- **CORS** — the upstream API must include `Access-Control-Allow-Origin`. If it doesn't, the browser blocks the response and there's nothing Website Deploy can do; you need a server-side proxy that you control elsewhere.
- **Keys** — anything in your client-side code is visible to anyone who opens DevTools. Don't bake in API keys. If the API requires a key, have the user paste it into a small input field and save it to `localStorage` with a "paste a fresh key" hint when it's missing.
- **Rate limits** — public APIs throttle by IP. If your site is on a shared machine, that quota is shared too.

### 6. Static reports from generated exports

What it is: a static HTML/JS dashboard or report built from data that was exported before deploy. The export becomes ordinary site data (`.json`, `.csv`, or pre-rendered HTML) and Website Deploy only serves the finished files.

When to choose: public reports, class projects, research notes, and read-only dashboards where the private work already happened in another tool. For example, a user can turn a sanitized analytics or social export (a follower list, a search result, a CSV of metrics) into a static report, then deploy the report output here.

Gotchas:
- Every deployed file is public. Remove keys, cookies, and anything the user did not explicitly approve for publication.
- Prefer small, pre-filtered exports. Large raw datasets can exceed archive limits and make the page slow.
- Do not fetch private APIs from the browser unless the user supplies a key at runtime. If the report needs server-side refresh, Website Deploy is only the static front-end, not the refresh worker.

### 7. Routing patterns

Website Deploy serves files. There is no rewrite layer. Because sites live under `/<sitename>/`, keep links **relative** so navigation stays inside the site path.

- **Multi-page static site**: every page is a real `index.html` under a directory. `about/` resolves to `about/index.html` under the site path.
- **SPA with framework router**: build for static export (see the `website-deploy` skill's framework section) **with a relative base**. Use the framework's hash-router mode or generate a `404.html` that bootstraps the app.
- **Pretty URLs for plain HTML**: put each "page" in its own folder with an `index.html` (`about/index.html`, `pricing/index.html`).

### 8. A nicer address: free name or custom domain

Optional — every site already has `https://<handle>.simple-host.app/<sitename>/`, where sign-in and private collections work. The quickest nicer address is a free `<name>.simple-host.app`: `connect_domain` (or `POST /v1/sites/<sitename>/domain`) with `{"domain":"clay-studio.simple-host.app"}` answers `active` at once, no DNS. First come, first served. A user can instead serve a site from their own domain (e.g. `recipes.brand.com`) — use the `connect-domain` skill (`simple-host-website/skills/connect-domain`). Summary: `POST /v1/sites/<sitename>/domain` with `{domain}` → user adds one DNS record → poll `GET /v1/sites/<sitename>/domain` until `active` (with the connector: `connect_domain`, then `domain_status`). Either one changes the address; sign-in and private collections carry over. Pages stay public. Once connected, the site lives only at that address: its `<handle>.simple-host.app/<sitename>/` URL 302s there and takes no writes for it (agents keep writing through the apex `https://simple-host.app/v1/...`).

## Picking a capability mix

| User says | Capabilities |
|---|---|
| "a landing page / portfolio / CV" | static only |
| "a guestbook" | static + per-site JSON state (atomic `append`) + `auth.js` sign-in |
| "a waitlist / event RSVP / signup form" | static + append-only collection (+ a live count in state); names or emails in it → private collection + owner page |
| "take orders / bookings / a survey" | private collection + form with `auth.js` sign-in + owner page (`orders.html`) |
| "a poll / a vote / a counter" | static + `PATCH` `inc` on state + `auth.js` sign-in |
| "a tool that runs entirely in the browser" (calculator, drawing app, game) | static + `localStorage` for settings/saves |
| "a journal / notes app" | static + `IndexedDB` (single-visitor scope) |
| "a dashboard pulling from a public API" | static + external `fetch()` |
| "a report from an exported dataset (analytics, social, etc.)" | static export + optional client-side filtering |
| "a multi-page site" | static only — each page is its own folder + `index.html` (relative links) |
| "my own domain / brand.com" | static + `connect-domain` skill |
| "a shorter address, but no domain" | static + free `<name>.simple-host.app` (one `connect_domain` call) |
| "a slide deck I want to share a link to" | build with Slidev, Reveal.js, or similar and deploy the output |

If the user wants something Website Deploy can't host — per-user accounts that span devices, server-side execution, or a shared SQL database — say so explicitly and stop. Suggest they pair Website Deploy (for the static front-end) with a separate backend host (Vercel functions, Cloudflare Workers, Supabase, etc.) where their server-side logic lives. Nicer addresses *are* supported (free `<name>.simple-host.app`, or a custom domain via `connect-domain`). Private or password-locked pages are not — every deployed page is public. Sign-in (Google or email code) gates *writing* to the backend; the only thing gated for reading is a private collection, which only the site owner — and the Simple Host operator, for moderation — can read. Never present "sign in to save" as a private page.

## Generating a prompt for another agent

When the user wants to wire a capability into an existing site, generate a focused prompt to paste into a fresh agent chat. Keep it short.

Example prompt for "save drafts in localStorage":

> Add draft autosave to this site. On every change to the text input, write `{text, updatedAt}` to `localStorage['mysite.draft']`. On page load, restore the input value from that key if present. Show a small "Draft saved" indicator that fades out after 1 second when the save runs. No external dependencies. Use relative asset links only (sites are path-hosted).

Example prompt for "let visitors sign the guestbook" (entries belong to signed-in visitors; this site also has a custom domain, so `SH_CONFIG` is required):

> Add a guestbook to this site (deployed on simple-host, custom domain `guests.example.com`, sitename `guestbook`). Load `https://simple-host.app/auth.js` with `window.SH_CONFIG = { site: "guestbook" }` set before the tag, mount `SH.mount('#sh-auth')` next to the form, and call `await SH.requireSignIn()` before `SH.collection('entries').append({name, message})`. On a non-2xx keep the form and show "Not saved". Add `admin.html` (noindex) that lists the collection newest-first. Relative asset links only.

Mirror this shape for `IndexedDB`, external API calls, routing, etc.

## Handoff: deploy

Once the user has decided what to build, they need to deploy. Tell them to use the `website-deploy` skill, which handles registration (only when the Simple Host connector is not available), framework-aware build (with a relative base path), packaging, and upload. The site will be live at `https://<handle>.simple-host.app/<sitename>/`. If they want a nicer address, offer the free `<name>.simple-host.app` or the `connect-domain` skill for their own domain; it is optional.
