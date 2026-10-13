# Capabilities in full

Moved here from SKILL.md unchanged.

## Contents

- Connecting and scope
- Two sign-ins, one rule
- What Website Deploy gives you
- How to use this skill (long form)
- Capability tree
- Picking a capability mix

## Connecting and scope

On Simple Host, the older state, collection and declared-data APIs are deprecated. Use them only to maintain an existing site that depends on their behavior. New sites should use owner-defined KV, SQLite and file resources. Choose a preset per resource (and per SQLite table): `records` for each signed-in visitor’s own records, `inbox` for forms. `signed-in` alone still means shared access. For orders, bookings, reservations, RSVPs with a status, support requests and assignments, use the [Each person's records](handoff-and-examples.md#each-persons-records) pattern in `references/handoff-and-examples.md` with domain-specific tables, status and linked change rows; quote requests, contact forms, waitlists and sign-ups are `inbox`, sent without sign-in. Simple Hack websites expose only KV, SQLite and files; event signup stays on the trusted Simple Hack apex.

[Open ChatGPT Plugins](https://chatgpt.com/plugins). Click **Add**, then choose **Add custom MCP server**. Name it **Simple Host**, paste `https://simple-host.app/mcp`, and save. In the sign-in window, sign in with Google or an email code, then choose **Allow**. In a chat, pick Simple Host from the **+** menu, or just ask.

Open Claude, then **Settings**, then **Connectors**, then **Browse connectors**. Search for **Simple Host**, choose **Connect**, sign in, then choose **Allow**. It works on web, desktop, and phone.

For a Simple Hack team or custom event website, use the separate reviewed Simple Hack `website-deploy-builder` skill. Its website storage is KV, SQLite and files only, through the signed-in Simple Hack connector.

Use the signed-in Simple Host connector when available. If it is disconnected, ask the person to reconnect it through the app's trusted browser window. Never request, receive, read or transmit sign-in codes, API keys, passwords or site passcodes in chat. Without the connector, use REST only if this environment already has a locally configured owner credential; keep it out of chat, logs, pages and committed files. New account setup and credential or passcode changes belong in the trusted Simple Host browser/dashboard. Do not run a remote installer to obtain credentials.

Use this skill when a user wants help deciding what to build on Website Deploy, or how to scope an idea they already have. After the user picks an approach, hand off to the `website-deploy` skill for deploy.

For a **new Simple Host** site's backend, plan with three flexible resources:
JSON KV for named values, SQLite for related records and queries, and files for
durable binary objects. The agent chooses the schema, keys and paths for the
actual app. Each resource (and each SQLite table) has an access preset that sets
who may read, add, edit and delete: `public`, `inbox`, `wall`, `records`,
`personal`, `board` or `private` (the default), plus optional inheritance of the
site's passcode. Anonymous adds need a preset that allows them (`inbox`), and
nobody anonymous ever edits or deletes. `board` (signed-in everything) does not
make rows private per person; `records` and `personal` do.
Read `website-deploy/references/storage.md` before implementation. For an
**existing site** using state, collections or declared kinds, preserve its
built-in privacy, atomic operations, undo and notification behavior unless the
owner chooses and verifies a migration. Simple Hack team and custom event websites use only these resources; follow their separate skill for site scope and event rules.

## Two sign-ins, one rule

Account sign-in (an API key) is how the site's owner, or their agent, gets into
Simple Host and publishes or deletes sites. It is never for a site's visitors,
and a page never calls it. Visitor sign-in is for the people who use a site:
it runs on the site's own address through `auth.js` (`SH.mount('#sh-auth')`,
`await SH.requireSignIn()`) and identifies a visitor to that one site only, with
a cookie, never a key. When you plan a page that needs "sign in", it is always
visitor sign-in — plan the owner's view (who reads what visitors saved) as a
connector or dashboard job, never a page in the site.

## What Website Deploy gives you

Website Deploy is a static-file host at `https://simple-host.app`. Each site lives at its own address, `https://<sitename>.<handle>.simple-host.app/` (`handle` is the owner's URL-safe handle from GET `/v1/me`; `https://<handle>.simple-host.app/` lists the person's public sites). Always hand the person the `site_url`/`url` the deploy returned — for a brand-new account it is briefly `https://<handle>.simple-host.app/<sitename>/` until the site's certificate is issued. The dashboard/API stay on `https://simple-host.app` (a separate origin). Old `<handle>.simple-host.app/<site>/` and `sites.simple-host.app/<handle>/<site>/` links redirect to the site's address. There is no server-side execution — but the API gives each site a real, server-backed backend:

| Capability | How |
|---|---|
| HTML / CSS / JS / images / fonts served as a site | Deploy files inline as JSON (`/files`) or upload a `.tar.gz`/`.zip`. With the connector: `create_site` / `update_site` (`deploy_site` on older connections) |
| **New Simple Host backend** | Declare KV, SQLite or files resources with `storage_set_resource` or `PUT /v1/sites/<site>/storage/resources/<name>`; pick an access preset per resource and per SQLite table; use the matching `storage_*` connector tools or same-origin REST. KV/SQLite share 10,000,000 bytes; files get 10 MB; plan client-side phone-photo compression for upload pages. See `website-deploy/references/storage.md` |
| Collections (signups, RSVPs, submissions — anything visitors add to) | A SQLite resource (`storage_set_resource`) with preset `wall` (public list, authors remove their own), `board` (everyone signed in edits) or `inbox` (only the owner reads); pages call `table().add()`, `.list()`, `.edit()`, `.delete()` as the preset allows. An existing site may keep the older `/collections` API — existing sites only; see `references/backend.md` |
| Private collections (orders, RSVPs, anything personal) | A SQLite resource with preset `records` (each visitor sees only their own; the owner sets a status), `personal` (each visitor also edits and deletes their own) or `inbox` (only the owner reads) — see [Each person's records](handoff-and-examples.md#each-persons-records) in `references/handoff-and-examples.md`. An existing site may keep private Submissions — existing sites only; see `references/backend.md` |
| Who may save here (an existing site's site-wide allow/block list) | Anyone who signs in (default), or only listed emails and whole `@domains`, plus a block list — existing sites only, no connector tool; see `references/backend.md` |
| Per-site JSON state (≤ 1 MB, shared across all visitors — existing sites only) | `GET / PUT /v1/sites/<sitename>/state` (same-origin from the page; agents can also use `/v1/u/<handle>/sites/<sitename>/state` on the apex). Reads public; a page write needs the visitor signed in first (`auth.js`) |
| Atomic state updates (concurrent-safe counters, lists, votes — existing sites only) | `PATCH .../state` with `{ops:[inc/append/set/remove/removeWhere]}`; `If-None-Match` ETag for cheap polling. A write — same rule as above |
| A nicer address (optional) | Free `<name>.simple-host.app`: one call (`connect_domain`), active at once, no DNS. Or a custom domain via the `connect-domain` skill (two DNS records: the address and a TXT ownership record). The site moves there and its old address redirects |
| Agent writing for the site owner (no browser) | The connector's `storage_*` tools (`storage_sql_execute`, `storage_put_kv`, `storage_put_file`, …) if present; otherwise an already configured local owner credential sent as `X-API-Key` on the matching `/v1/sites/<site>/storage/...` route — works only on sites that account owns (another account's key gets 404). Anyone else saves on the page as a signed-in visitor. Existing sites: `references/backend.md` covers agent writes to state and collections |
| Per-visitor state | A storage resource with `read:"own"` when it must follow the visitor to another device (see Each person's records in `references/handoff-and-examples.md`); `localStorage`, `sessionStorage` or `IndexedDB` (in the browser) when it only needs to live on this one device. An existing site may keep **Personal** (`mine`) — existing sites only |
| External APIs | `fetch()` from the page to any public CORS-enabled API |
| A whole site only certain people can open | Named viewers (`grant_site_viewer` with the emails the person gave, after asking): only the owner and those people, each signed in on the site with their own email, can open it or its saved data. Nobody can pass it on |
| Keeping a whole site from people without a passcode | One shared passcode on the whole site (`set_site_passcode`, after asking, with the code the person chose). Not a login: anyone given it can pass it on, and saved data is not private per person. Never together with named viewers. No per-page lock |
| Routing | Static files only — path-relative directories with `index.html`; SPA routing via the framework's hash router or `404.html` fallback |
| **Existing declared-data API (existing sites only)** | `declare_data` or `PUT /v1/sites/<sitename>/data/<name>/kind` preserves Page info, Submissions, Personal and Shared boards on a site that already depends on their per-person privacy, item versions, history or notifications. The connector no longer offers these tools; see `website-deploy/references/backend.md`. |

If the idea needs server-side application code, custom user accounts, platform-enforced per-row roles or long-running jobs, explain that those parts need another service. A site can use its own SQLite resource for SQL tables and queries; do not describe shared SQL as unsupported.

**State the chosen resource policy before designing the page.** A new resource starts owner-only; `anyone` can allow anonymous reading or writing, and `signed-in` uses the visitor's site-scoped Google or emailed-code sign-in — never account sign-in, and never an API key in the page. Agents acting for the owner use the connector or owner API key. An existing site's state and declared-data writes keep their prior sign-in requirements.

**Preserve per-person privacy.** An existing site's Submissions and Personal kinds have visitor-specific visibility, edits and withdrawal that a database-wide `signed-in` policy does not provide — keep those APIs there (existing sites only). For a new design involving personal details, do not choose a shared KV namespace or SQL table with broad read access; use preset `records` (or `personal` when people edit their own) for each visitor's own reads (see Each person's records in `references/handoff-and-examples.md`), or `inbox` when only the owner should read it. Visitor edits require a separate design (a linked change table, as in Each person's records). SQL joins and search within a resource are supported; platform-enforced per-row roles and instant push updates are not.

**Always pair a form with a viewer.** Any site that collects data (a signup, RSVP, guestbook, contact form, order) must also ship a way for the owner to read it back. For a new site the owner's view is `storage_sql_query` (or, without the connector, the owner-keyed `POST /v1/sites/<site>/storage/sqlite/<name>/query` REST route), or an admin page in the site (`get_page_recipe` topic `admin`): the owner signs in there with the same visitor sign-in box, using their account email, on the site's own address, and the page then reads and changes saved data with owner rights (`SH.me()` answers `site_owner: true`). It never holds a key, and it reaches saved data only, never settings. Tell the person the trade-off: while they are signed in on their own site, a bug or a malicious script on its pages could act on that site's data; see [Each person's records](handoff-and-examples.md#each-persons-records) in `references/handoff-and-examples.md` for the one worked pattern, including how the owner changes a row's status with `storage_sql_execute`.

## How to use this skill

1. Ask the user what they're trying to build, in plain language. Don't push capabilities at them — let them describe the idea.
2. Decide whether it can run as a static site. If parts of it can't, name those parts and either propose a static-friendly substitute or recommend a different host for that piece.
3. If visitors will save anything, choose KV, SQLite or files for a new Simple Host site, and state each resource's preset and passcode setting. Plan sign-in when the policy or a retained legacy API needs it. For personal details choose `inbox`, `records` or `personal`. For per-person records choose `records`; people request changes by adding linked history rows; original records stay unchanged by visitors; deprecated Submissions and Personal remain for existing Simple Host sites only.
4. For the part that can run statically, give them: (a) a one-paragraph explanation of how to structure it, (b) any relevant snippet (storage, routing, external API call), (c) the gotchas.
5. If they're starting from scratch, finish with a "ready to deploy" handoff: tell them to use the `website-deploy` skill, which handles registration (only without the connector), framework-aware build, packaging, and upload.
6. If they want to wire a capability into a site they've already deployed, generate a focused prompt they can paste into a fresh agent chat (in their site's repo). Include the pattern, the storage shape, and any gotcha — nothing else. If the change deletes data, makes private data public or changes who can see or save, the prompt says to confirm that step with the person first.

## Capability tree

### 1. Static hosting (the baseline)

What it is: any folder of HTML/CSS/JS/assets served as-is. Build any framework's normal production output (`dist/`, `build/`, `out/`, `public/`, `.output/public/`) and upload.

When to choose: every Website Deploy site starts here. Deploy first, then layer storage and external calls.

Gotchas: use relative links (`style.css`, not `/style.css`, and `about.html`, not `/about`) so previews and a new site's first minutes work too; root-relative links work only at the live address. For framework builds, set the base/public path so output uses relative URLs (e.g. Vite `base: './'`, Next `basePath` / relative assets, etc.). Don't ship `node_modules/` or `.env`. A site may be up to 300 MB on simple-host.app.

Photos: resize to what the page shows (about 1600 px on the long side, 800 px for cards and thumbnails) and save as WebP or JPEG at quality 75–80, under ~300 KB each; never camera originals or PNG photos (PNG or SVG is for logos, icons and flat graphics); every deploy keeps a full copy as a version, so small files matter.

### 2. Existing shared JSON state (existing sites only)

What it is: a single JSON document (up to 1 MB), shared across everyone who visits, that an existing site already reads and writes with `SH.state.get()` / `SH.state.patch(ops)`. Writing still needs the visitor signed in first (`auth.js`, `await SH.requireSignIn()`); the session is site-scoped and is not an API key.

When to choose: maintaining an existing site that already uses it. For anything new — notes, counters, tallies, configuration, per-person records — use a KV or SQLite resource instead (`references/storage.md`); do not design a new site around shared state. The full `SH.state` / `SH.data` / `SH.collection` API, the page pattern and the error bodies are in the `website-deploy` skill's `references/backend.md` (existing sites only).

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
| "only mom@example.com and dad@example.com should see it" | static + named viewers (`grant_site_viewer` with those emails, after asking) |
| "only my family / class / team should see it" | ask: named viewers by email (each signs in) or one shared passcode (`set_site_passcode`); not unlisted |
| "a guestbook" | static + a SQLite table with preset `wall` (`get_page_recipe` topic `wall`). An existing guestbook using public Submissions can keep them. |
| "a waitlist / quote request / contact / signup form" | SQLite with preset `inbox` (`get_page_recipe` topic `inbox`): anyone sends without signing in, only the owner reads. An event RSVP people check later is `records`; `board` for a sheet people edit together, `wall` for a public who's-coming list. |
| "take orders / bookings / reservations / appointments" | SQLite with preset `records` (topic `booking` for bookings and reservations: the customer signs in when they book and sees the status; “Each person's records” pattern below for orders; `admin` recipe for an owner page); retain existing private Submissions when visitor-specific privacy is needed; optionally an owner-only SQLite resource for separate owner-managed workflow data. |
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
it, while a storage resource may separately inherit or bypass that gate. Named
viewers close the whole site and all of its saved data to everyone but the
owner and the people named. A
signed-in resource is available to every signed-in visitor; do not describe it
as a private page or per-person data store. Existing private Submissions and
Personal records keep their narrower visibility rules.
