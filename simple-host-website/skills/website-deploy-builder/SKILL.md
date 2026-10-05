---
name: website-deploy-builder
description: Plan a static website and its saved data before implementation. Use when someone asks what to build with Simple Host KV, SQLite or files, how to preserve an existing site's declared-data behavior. Hand implementation to website-deploy.
---

On Simple Host, the older state, collection and declared-data APIs are deprecated. Use them only to maintain an existing site that depends on their behavior. New sites should use owner-defined KV, SQLite and file resources. These resources have whole-resource access policies, so do not treat `signed-in` as per-person row privacy. Simple Hack websites expose only KV, SQLite and files; event signup stays on the trusted Simple Hack apex.


# Website Deploy Builder

For a Simple Hack team or custom event website, use the separate reviewed Simple Hack `website-deploy-builder` skill. Its website storage is KV, SQLite and files only, through the signed-in Simple Hack connector.

Use the signed-in Simple Host connector when available. If it is disconnected, ask the person to reconnect it through the app's trusted browser window. Never request, receive, read or transmit sign-in codes, API keys, passwords or site passcodes in chat. Without the connector, use REST only if this environment already has a locally configured owner credential; keep it out of chat, logs, pages and committed files. New account setup and credential or passcode changes belong in the trusted Simple Host browser/dashboard. Do not run a remote installer to obtain credentials.

Use this skill when a user wants help deciding what to build on Website Deploy, or how to scope an idea they already have. After the user picks an approach, hand off to the `website-deploy` skill for deploy.

For a **new Simple Host** site's backend, plan with three flexible resources:
JSON KV for named values, SQLite for related records and queries, and files for
durable binary objects. The agent chooses the schema, keys and paths for the
actual app. Each resource has independent whole-resource `read` and `write`
policies (`anyone`, `signed-in`, `owner`), initially owner-only, plus optional
inheritance of the site's passcode. Anonymous writes require an explicit
`anyone` policy. A signed-in policy does not make rows private per person.
Read `website-deploy/references/storage.md` before implementation. For an
**existing site** using state, collections or declared kinds, preserve its
built-in privacy, atomic operations, undo and notification behavior unless the
owner chooses and verifies a migration. Simple Hack team and custom event websites use only these resources; follow their separate skill for site scope and event rules.

## What Website Deploy gives you

Website Deploy is a static-file host at `https://simple-host.app`. Each site lives at its own address, `https://<sitename>.<handle>.simple-host.app/` (`handle` is the owner's URL-safe handle from GET `/v1/me`; `https://<handle>.simple-host.app/` lists the person's public sites). Always hand the person the `site_url`/`url` the deploy returned — for a brand-new account it is briefly `https://<handle>.simple-host.app/<sitename>/` until the site's certificate is issued. The dashboard/API stay on `https://simple-host.app` (a separate origin). Old `<handle>.simple-host.app/<site>/` and `sites.simple-host.app/<handle>/<site>/` links redirect to the site's address. There is no server-side execution — but the API gives each site a real, server-backed backend:

| Capability | How |
|---|---|
| HTML / CSS / JS / images / fonts served as a site | Deploy files inline as JSON (`/files`) or upload a `.tar.gz`/`.zip`. With the connector: `create_site` / `update_site` (`deploy_site` on older connections) |
| **New Simple Host backend** | Declare KV, SQLite or files resources with `storage_set_resource` or `PUT /v1/sites/<site>/storage/resources/<name>`; set independent whole-resource read/write policies; use the matching `storage_*` connector tools or same-origin REST. The three kinds share 1,000,000 bytes per website; plan client-side phone-photo compression for upload pages. See `website-deploy/references/storage.md` |
| **Existing declared-data API** | `declare_data` or `PUT /v1/sites/<sitename>/data/<name>/kind` preserves Page info, Submissions, Personal and Shared boards. Keep it when an existing site depends on per-person privacy, item versions, history or notifications; see `website-deploy/references/backend.md`. |
| Who may save here | Anyone who signs in (default), or only listed emails and whole `@domains`, plus a block list: `set_who_can_save`, `block_person` |
| Per-site JSON state (≤ 1 MB, shared across all visitors; older sites) | `GET / PUT /v1/sites/<sitename>/state` (same-origin from the page; agents can also use `/v1/u/<handle>/sites/<sitename>/state` on the apex). Reads public; a page write needs the visitor signed in first (`auth.js`) |
| Atomic state updates (concurrent-safe counters, lists, votes) | `PATCH .../state` with `{ops:[inc/append/set/remove/removeWhere]}`; `If-None-Match` ETag for cheap polling. A write — same rule as above |
| Collections (signups / RSVPs / submissions) | `POST/GET /v1/sites/<sitename>/collections/<name>`. GET public; POST is a write. The owner removes entries (one, or the whole list); in declared Submissions each visitor also changes and withdraws their own |
| Private collections (orders, RSVPs, anything personal) | `set_collection_privacy` (or `PUT .../collections/<name>/privacy` `{"private":true}`). Signed-in visitors add; only the site owner — and the Simple Host operator, for moderation — can read it. The owner can edit or delete items (`update` / `remove`). In a public list the owner can delete (spam) but not edit; in declared Submissions each visitor also changes and withdraws their own |
| A nicer address (optional) | Free `<name>.simple-host.app`: one call (`connect_domain`), active at once, no DNS. Or a custom domain via the `connect-domain` skill (two DNS records: the address and a TXT ownership record). The site moves there and its old address redirects |
| Agent writing for the site owner (no browser) | The connector (`update_state`, `add_to_collection`) if present; otherwise an already configured local owner credential sent as `X-API-Key` — works only on sites that account owns (another account's key gets 404). Anyone else saves on the page as a signed-in visitor. See "Saving from an agent" in the `website-deploy` skill's `references/backend.md` |
| Per-visitor state | `localStorage`, `sessionStorage`, `IndexedDB` (in the browser), or **Personal** (`mine`) when it must follow the visitor to another device |
| External APIs | `fetch()` from the page to any public CORS-enabled API |
| Keeping a whole site from people without a passcode | One shared passcode on the whole site, configured by the person in the trusted Simple Host dashboard. Not a login: anyone given it can pass it on, and saved data is not private per person. No per-page lock |
| Routing | Static files only — path-relative directories with `index.html`; SPA routing via the framework's hash router or `404.html` fallback |

If the idea needs server-side application code, custom user accounts, platform-enforced per-row roles or long-running jobs, explain that those parts need another service. A site can use its own SQLite resource for SQL tables and queries; do not describe shared SQL as unsupported.

**State the chosen resource policy before designing the page.** A new resource starts owner-only; `anyone` can allow anonymous reading or writing, and `signed-in` uses the visitor's site-scoped Google or emailed-code sign-in. Agents acting for the owner use the connector or owner API key. Existing state and declared-data writes keep their prior sign-in requirements.

**Preserve per-person privacy.** Existing Submissions and Personal kinds have visitor-specific visibility, edits and withdrawal that a database-wide `signed-in` policy does not provide. Keep those APIs for an existing site that uses them. For a new design involving personal details, do not choose a shared KV namespace or SQL table with broad read access; design the privacy boundary explicitly. Existing sites may retain private Submissions or Personal when their built-in semantics are required; new sites needing per-person reads or edits need a service with row-level access. SQL joins and search within a resource are supported; platform-enforced per-row roles and instant push updates are not.

**When retaining the declared-data API, use private Submissions for personal details.** Orders, RSVPs, survey answers, sign-ups, or anything with names, emails, phone numbers or addresses: only signed-in visitors can submit, only the owner reads them all, and each visitor sees, changes and withdraws their own. Plan it in this order:

1. Declare it (`declare_data` with `kind: "entries"`) before the form goes live.
2. The form page calls `await SH.requireSignIn()` before `SH.data('orders', 'entries').add({...})`, and shows the saved item from the answer as the visitor's receipt (and `.mine()` for what they sent before).
3. An owner page on the site (e.g. `orders.html`) that signs in, lists them (`SH.data('orders').list()`), and has "Mark done" (`.update(id, {status:'done'})`) and "Delete" (`.remove(id)`) buttons. It works only for the owner's account. The owner also has their sites page (every entry with who sent it, a daily email, a spreadsheet download); the agent reads it with `read_collection`.

Public Submissions (a guestbook, public comments) are `"visibility": "public"`; say so plainly. Pages are public to anyone with the link unless the owner puts a passcode on the whole site (then to anyone who also has the passcode); only private Submissions are closed, readable in full by the site owner and the Simple Host operator (for moderation).

**Always pair a form with a viewer.** Any site that COLLECTS data (a signup, RSVP, guestbook, contact form, order) MUST also ship a second page — e.g. `admin.html` — that reads the same collection back (`GET .../collections/<name>?limit=200` → `{items:[{id,data,created_at},…]}`) and lists every entry for the owner, newest first, plus the live total from state. Link it quietly from the main page (a small "Organizer view →" in the footer). A form with nowhere to read the results is only half the feature — and the person you're building for will not think to ask for the viewer, so add it by default. Mark the viewer `<meta name="robots" content="noindex">`. A public collection is readable by anyone with the link, so don't fake a password; if the entries are personal, make the collection private and the viewer becomes the owner page above.

## How to use this skill

1. Ask the user what they're trying to build, in plain language. Don't push capabilities at them — let them describe the idea.
2. Decide whether it can run as a static site. If parts of it can't, name those parts and either propose a static-friendly substitute or recommend a different host for that piece.
3. If visitors will save anything, choose KV, SQLite or files for a new Simple Host site, and state each resource's read/write/passcode policy. Plan sign-in when the policy or a retained legacy API needs it. For personal details, choose owner-only resource reads. If visitors need per-person reads or edits on a new site, use a service with row-level access; deprecated Submissions and Personal remain for existing Simple Host sites only.
4. For the part that can run statically, give them: (a) a one-paragraph explanation of how to structure it, (b) any relevant snippet (storage, routing, external API call), (c) the gotchas.
5. If they're starting from scratch, finish with a "ready to deploy" handoff: tell them to use the `website-deploy` skill, which handles registration (only without the connector), framework-aware build, packaging, and upload.
6. If they want to wire a capability into a site they've already deployed, generate a focused prompt they can paste into a fresh agent chat (in their site's repo). Include the pattern, the storage shape, and any gotcha — nothing else. If the change deletes data, makes private data public or changes who can see or save, the prompt says to confirm that step with the person first.

## Capability tree

### 1. Static hosting (the baseline)

What it is: any folder of HTML/CSS/JS/assets served as-is. Build any framework's normal production output (`dist/`, `build/`, `out/`, `public/`, `.output/public/`) and upload.

When to choose: every Website Deploy site starts here. Deploy first, then layer storage and external calls.

Gotchas: use relative links (`style.css`, not `/style.css`, and `about.html`, not `/about`) so previews and a new site's first minutes work too; root-relative links work only at the live address. For framework builds, set the base/public path so output uses relative URLs (e.g. Vite `base: './'`, Next `basePath` / relative assets, etc.). Don't ship `node_modules/` or `.env`. A site may be up to 300 MB on simple-host.app.

Photos: resize to what the page shows (about 1600 px on the long side, 800 px for cards and thumbnails) and save as WebP or JPEG at quality 75–80, under ~300 KB each; never camera originals or PNG photos (PNG or SVG is for logos, icons and flat graphics); every deploy keeps a full copy as a version, so small files matter.

### 2. Existing shared JSON state (compatibility)

What it is: a single JSON document (up to 1 MB) scoped to your site. The server stores it in Postgres; your site reads and writes it from the browser. The document is shared across **everyone** who visits — use `PATCH` ops so concurrent writers don't clobber each other.

When to choose: maintain an existing site that already uses shared state. For new notes, counters, tallies or configuration, use KV or SQLite resources. Reading is public. **Writing from a page needs sign-in**: the page loads `https://simple-host.app/auth.js`, sets `window.SH_CONFIG = { site: "<sitename>" }` (needed on a custom domain, harmless everywhere), and calls `await SH.requireSignIn()` before each write — the visitor signs in with Google or an emailed code. The session is site-scoped and is not an API key. Sign-in gates writing only — it does not make the page private. If you need per-visitor data, store it under different keys inside the document, keyed on something like `crypto.randomUUID()` saved in `localStorage`.

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

Full `SH` API (`SH.data(name, kind)`, and on older sites `SH.state` and `SH.collection(name)`; `SH.me`, `SH.signOut`) and the error bodies are in the `website-deploy` skill's `references/backend.md`.

Gotchas: state is public to anyone with the link; never keep personal details in it (use a private collection). Body cap is 1 MB; sending more returns 413.

### 3. Per-visitor state with `localStorage`

What it is: small JSON blobs stored in the visitor's browser, scoped to the page's origin (each site's own `<sitename>.<handle>.simple-host.app`, or its custom domain).

When to choose: anything you'd want a tiny key-value store for in a single-visitor experience — drafts, settings, app state, the user's progress. Per-visitor only; there is no sharing across browsers or devices.

```js
// save
localStorage.setItem('myapp.state', JSON.stringify(state));

// load
const raw = localStorage.getItem('myapp.state');
const state = raw ? JSON.parse(raw) : {};
```

Gotchas: typical browser quota is ~5 MB per origin. Cleared by the user at any time. Prefix your keys with the site name: on the `<handle>.simple-host.app/<sitename>/` fallback address a person's sites share one origin. For multi-megabyte structured data, use `IndexedDB` instead.

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
- **Keys** — anything in your client-side code is visible to anyone who opens DevTools. Don't bake in API keys. If the API requires a secret key, use a separate server-side proxy or choose a public API; do not put a secret in the page or browser storage.
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

Optional — every site already has its own `https://<sitename>.<handle>.simple-host.app/`, where sign-in and private collections work. The quickest nicer address is a free `<name>.simple-host.app`: `connect_domain` (or `POST /v1/sites/<sitename>/domain`) with `{"domain":"clay-studio.simple-host.app"}` answers `active` at once, no DNS. First come, first served. A user can instead serve a site from their own domain (e.g. `recipes.brand.com`) — use the `connect-domain` skill (`simple-host-website/skills/connect-domain`). Summary: `POST /v1/sites/<sitename>/domain` with `{domain}` → user adds two DNS records (the address and a TXT ownership record) → poll `GET /v1/sites/<sitename>/domain` until `active` (with the connector: `connect_domain`, then `domain_status`). Either one changes the address; sign-in and private collections carry over. Pages stay public. Once connected, the site lives only at that address: its `<sitename>.<handle>.simple-host.app` URL 302s there and takes no writes for it (agents keep writing through the apex `https://simple-host.app/v1/...`).

## Picking a capability mix

| User says | Capabilities |
|---|---|
| "a landing page / portfolio / CV" | static only |
| "only my family / class / team should see it" | static + a site passcode configured in the trusted dashboard |
| "a guestbook" | static + a KV namespace or SQLite table; choose read/write policy and fields for this guestbook. An existing guestbook using public Submissions can keep them. |
| "a waitlist / event RSVP / signup form" | static + private Submissions if each visitor must see, edit or withdraw only their own entry; a whole-resource SQL/KV policy alone cannot do that. |
| "take orders / bookings / a survey" | private Submissions when visitor-specific privacy is needed; optionally an owner-only SQLite resource for separate owner-managed workflow data. |
| "a poll / a vote" | SQLite for the tally and app-chosen vote schema only if its whole-resource policy and duplicate-vote rules fit; retain `one_per_person` Submissions when that built-in guarantee is needed. |
| "a menu / opening hours / prices I update" | static + owner-write, anyone-read KV resource; retain Page info on a site already using its history. |
| "a habit tracker / saved progress / my reading list, on any device" | Personal (`kind: mine`) when each account needs a private record; a signed-in KV/SQLite resource would expose all visitors' records. |
| "a shared shopping list / kanban / potluck sign-up" | static + SQLite table with app-chosen columns, or KV keys; choose independent read/write policies. Existing Shared boards keep their item versions and undo. |
| "a tool that runs entirely in the browser" (calculator, drawing app, game) | static + `localStorage` for settings/saves |
| "a journal / notes app" | static + `IndexedDB` (single-visitor scope) |
| "a dashboard pulling from a public API" | static + external `fetch()` |
| "a report from an exported dataset (analytics, social, etc.)" | static export + optional client-side filtering |
| "a multi-page site" | static only — each page is its own folder + `index.html` (relative links) |
| "my own domain / brand.com" | static + `connect-domain` skill |
| "a shorter address, but no domain" | static + free `<name>.simple-host.app` (one `connect_domain` call) |
| "every site of mine under my domain" (`<site>.trips.brand.com`) | an address family: `connect-domain` skill §Many sites under one name (`*.trips.brand.com`, for the whole account; ask first) |
| "a slide deck I want to share a link to" | build with Slidev, Reveal.js, or similar and deploy the output |

If the user needs server-side application code, platform-enforced per-row roles or
custom account systems, explain that those pieces need another service. Simple
Host does provide a per-site SQLite resource; its `read`/`write` policy covers
the whole database, not each row. A site passcode is shared with anyone given
it, while a storage resource may separately inherit or bypass that gate. A
signed-in resource is available to every signed-in visitor; do not describe it
as a private page or per-person data store. Existing private Submissions and
Personal records keep their narrower visibility rules.

## Generating a prompt for another agent

When the user wants to wire a capability into an existing site, generate a focused prompt to paste into a fresh agent chat. Keep it short.

Example prompt for "save drafts in localStorage":

> Add draft autosave to this site. On every change to the text input, write `{text, updatedAt}` to `localStorage['mysite.draft']`. On page load, restore the input value from that key if present. Show a small "Draft saved" indicator that fades out after 1 second when the save runs. No external dependencies. Use relative asset links only (a site can also be served under a path).

Example prompt for **maintaining an existing** declared-data guestbook (entries belong to signed-in visitors; this site also has a custom domain, so `SH_CONFIG` is required):

> Add a guestbook to this site (deployed on simple-host, custom domain `guests.example.com`, sitename `guestbook`). First declare the data: `declare_data` with name `entries`, kind `entries`, visibility `public`. Load `https://simple-host.app/auth.js` with `window.SH_CONFIG = { site: "guestbook" }` set before the tag, mount `SH.mount('#sh-auth')` next to the form, and call `await SH.requireSignIn()` before `SH.data('entries', 'entries').add({name, message})`. On a non-2xx keep the form and show "Not saved". Add `admin.html` (noindex) that lists the collection newest-first. Relative asset links only.

Mirror this shape for `IndexedDB`, external API calls, routing, etc.

## Handoff: deploy

Once the user has decided what to build, they need to deploy. Tell them to use the `website-deploy` skill, which handles registration (only when the Simple Host connector is not available), framework-aware build (with a relative base path), packaging, and upload. Before a new site goes online for the first time, it asks the person once (name, address, public to anyone with the link). The site will be live at `https://<sitename>.<handle>.simple-host.app/` (give them the `site_url` the deploy returned). If they want a nicer address, offer the free `<name>.simple-host.app` or the `connect-domain` skill for their own domain; it is optional.

## Your home page

For "make this my home page", use the signed-in connector's `set_home_page`
with `{"site":"portfolio"}`, or a preconfigured owner credential with
`PUT /v1/me/home` and that body. `{"site":null}` restores the showcase.
`who_am_i` reports `home_site`. The person address serves the chosen site's files,
sign-in and storage; its normal site address keeps working. Use `https://simple-host.app/auth.js`, with
`window.SH_CONFIG={site:'portfolio'}` if the page sets an explicit site.
If the home is offline or taken down the showcase appears; rename follows the
site and deleting it clears the choice. The dashboard's Your address panel also
sets it. Available on Simple Host with person addresses, outside hosted events.
"Show my projects on my home page" can mean keeping the default public showcase
or building a site and making it home; unlisted sites stay off the showcase.

For "build me a home page that shows my projects", fetch the public live feed,
render with `textContent`, then choose that site as home. For example:

```html
<ul id="projects"></ul>
<script>
fetch('https://simple-host.app/v1/u/YOUR_HANDLE/showcase.json', {credentials:'omit'})
  .then(r => r.json()).then(feed => feed.sites.forEach(site => {
    const li = document.createElement('li'), a = document.createElement('a');
    a.textContent = site.title || site.name; a.href = site.url;
    li.append(a); document.querySelector('#projects').append(li);
  }));
</script>
```

Substitute the signed-in person's handle. The feed includes only the public
showcase's entries (never unlisted, offline, passcode or taken-down sites), plus
`bio`, `updated_at`, `pinned` and `order`. It allows cross-origin reads without
credentials, caches for 30 seconds and uses the public read rate limits.

To curate the default showcase, use connector `set_bio` with `{"bio":"I make things"}`
(empty clears), and `set_showcase_site` with `{"site":"project","pinned":true,"order":10}`.
Owner REST clients use `GET/PUT /v1/me/bio` and `GET/PUT /v1/sites/{name}/showcase`.
Both refuse deploy-only keys. Pin/order may be changed separately; order is an
integer from 0 to 1000000. Pinned projects come first, then smaller order numbers,
then creation order for ties. Unlisted sites stay hidden even when pinned.
The plain-text bio's limit is returned as `max_length`; it defaults to 280 characters
and is configured by `SHOWCASE_BIO_MAX_LENGTH`. Render it with `textContent`.
The dashboard's Your showcase has the same controls. Hosted events are excluded.
