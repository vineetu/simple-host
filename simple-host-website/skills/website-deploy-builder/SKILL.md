---
name: website-deploy-builder
description: Plan what to build on Website Deploy (simple-host.app). Helps a user decide whether their idea fits the static + light-backend model, maps it to concrete patterns (shared JSON state with atomic ops, append-only collections, localStorage, public APIs), and produces a focused prompt for an implementation agent. Knows the one planning rule that matters - saves on the shared host are open to everyone; if saves should be per-person or protected, the site needs its own custom domain. Use when a user is starting a new site or describes a feature idea and needs help mapping it to what the platform can do.
---

# Website Deploy Builder

Use this skill when a user wants help deciding what to build on Website Deploy, or how to scope an idea they already have. After the user picks an approach, hand off to the `website-deploy` skill for deploy.

## What Website Deploy gives you

Website Deploy is a static-file host at `https://simple-host.app`. Each site is served at a path under one content host: `https://sites.simple-host.app/<handle>/<sitename>/` (`handle` is the owner's URL-safe handle from GET `/v1/me`). The dashboard/API stay on `https://simple-host.app` (a separate origin). Older `https://<sitename>.simple-host.app/` links still resolve (legacy). There is no server-side execution — but the API gives each site a real, server-backed backend:

| Capability | How |
|---|---|
| HTML / CSS / JS / images / fonts served as a site | Deploy files inline as JSON (`/files`) or upload a `.tar.gz`/`.zip` |
| Per-site JSON state (≤ 1 MB, shared across all visitors) | `GET / PUT /v1/u/<handle>/sites/<sitename>/state` (legacy `/v1/sites/<sitename>/state` still works). Reads public; on the shared host a page writes freely; on the site's own custom domain the visitor signs in first (`auth.js`) |
| Atomic state updates (concurrent-safe counters, lists, votes) | `PATCH .../state` with `{ops:[inc/append/set/remove/removeWhere]}`; `If-None-Match` ETag for cheap polling. A write — same rule as above |
| Append-only collections (signups / RSVPs / submissions) | `POST/GET /v1/u/<handle>/sites/<sitename>/collections/<name>`. GET public; POST is a write |
| Custom domain | `connect-domain` skill: bind domain → one DNS record → poll until active. **This is what adds visitor sign-in to saves from a page** |
| Agent writing for a person (no browser) | That person's own API key, obtained by email code, as `X-API-Key` — works on any site, shared host included. See "Saving from an agent" in the `website-deploy` skill's `references/backend.md` |
| Per-visitor state | `localStorage`, `sessionStorage`, `IndexedDB` (in the browser) |
| External APIs | `fetch()` from the page to any public CORS-enabled API |
| Routing | Static files only — path-relative directories with `index.html`; SPA routing via the framework's hash router or `404.html` fallback |

If your idea needs a server you control, a shared SQL database, persistent per-user accounts, or anything that runs server-side, Website Deploy is not the right host. Say so and stop.

**On the shared host anyone can read and write; if saves should be per-person or protected, plan for `connect-domain`.** On the shared host every site is the same origin, so there is no visitor sign-in there — a page writes to the backend freely, and anyone can change that data. That is fine for a party RSVP or a team lunch poll. A guestbook or vote where each entry should belong to a signed-in person, or data a stranger should not be able to rewrite, is "static + backend + connect-domain": on the site's own domain visitors sign in with Google or an emailed code before saving. Say which one applies up front, before the page is written. Agents saving with an API key are not affected.

**Always pair a form with a viewer.** Any site that COLLECTS data (a signup, RSVP, guestbook, contact form, order) MUST also ship a second page — e.g. `admin.html` — that reads the same collection back (`GET .../collections/<name>?limit=200` → `{items:[{id,data,created_at},…]}`) and lists every entry for the owner, newest first, plus the live total from state. Link it quietly from the main page (a small "Organizer view →" in the footer). A form with nowhere to read the results is only half the feature — and the person you're building for will not think to ask for the viewer, so add it by default. Mark the viewer `<meta name="robots" content="noindex">`; sites and their data are public to anyone with the link, so don't fake a password.

## How to use this skill

1. Ask the user what they're trying to build, in plain language. Don't push capabilities at them — let them describe the idea.
2. Decide whether it can run as a static site. If parts of it can't, name those parts and either propose a static-friendly substitute or recommend a different host for that piece.
3. If visitors will save anything, say now that shared-host saves are open to everyone; if the saves should be per-person or protected, include `connect-domain` in the plan.
4. For the part that can run statically, give them: (a) a one-paragraph explanation of how to structure it, (b) any relevant snippet (storage, routing, external API call), (c) the gotchas.
5. If they're starting from scratch, finish with a "ready to deploy" handoff: tell them to use the `website-deploy` skill, which handles registration, framework-aware build, packaging, and upload.
6. If they want to wire a capability into a site they've already deployed, generate a focused prompt they can paste into a fresh agent chat (in their site's repo). Include the pattern, the storage shape, and any gotcha — nothing else.

## Capability tree

### 1. Static hosting (the baseline)

What it is: any folder of HTML/CSS/JS/assets served as-is. Build any framework's normal production output (`dist/`, `build/`, `out/`, `public/`, `.output/public/`) and upload.

When to choose: every Website Deploy site starts here. Deploy first, then layer storage and external calls.

Gotchas: the site lives under `/<handle>/<sitename>/` on the content host, so **relative links are required**. Root-absolute paths like `/css/app.css` resolve to the wrong place and break — use `css/app.css`, `./img/x.png`, `../shared/y`. For framework builds, set the base/public path so output uses relative URLs (e.g. Vite `base: './'`, Next `basePath` / relative assets, etc.). Don't ship `node_modules/` or `.env`. Each archive is capped at 100 MB.

### 2. Per-site JSON state (shared across visitors)

What it is: a single JSON document (up to 1 MB) scoped to your site. The server stores it in Postgres; your site reads and writes it from the browser. The document is shared across **everyone** who visits — use `PATCH` ops so concurrent writers don't clobber each other.

When to choose: anything you'd want a tiny key-value store for — a shared note, a counter, a vote tally, content the page generated, configuration. Reading is public. **On the shared host writing from a page is open too** — no sign-in, and anyone can change the data. **On the site's own custom domain writes need sign-in**: the page loads `https://simple-host.app/auth.js`, sets `window.SH_CONFIG = { site: "<sitename>" }`, and calls `await SH.requireSignIn()` before each write — the visitor signs in with Google or an emailed code. The same code works on both hosts (`requireSignIn` resolves at once on the shared host). The session is site-scoped and is not an API key. Sign-in gates writing only — it does not make the page private. If you need per-visitor data, store it under different keys inside the document, keyed on something like `crypto.randomUUID()` saved in `localStorage`.

How to use, from a page on either host:

```html
<div id="sh-auth"></div>
<form id="f"><textarea name="draft"></textarea><button>Save</button></form>
<p id="status"></p>
<script>window.SH_CONFIG = { site: "<sitename>" };</script>
<script src="https://simple-host.app/auth.js" defer></script>
<script>
window.addEventListener('DOMContentLoaded', async function () {
  SH.mount('#sh-auth');                              // custom domain: Google sign-in + email-code form; shared host: one muted line
  const status = document.getElementById('status');
  const form = document.getElementById('f');

  // load — public, no sign-in
  const { data } = await SH.state.get();
  form.draft.value = data.draft || '';

  // save — sign in first, then write; keep the form on failure
  form.onsubmit = async function (e) {
    e.preventDefault();
    await SH.requireSignIn();                        // custom domain: signs the visitor in; shared host: resolves at once
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

Gotchas: sites and their data are public to anyone with the link. Body cap is 1 MB; sending more returns 413. On the shared host anyone can change the data — that is by design; connect a domain if it matters.

### 3. Per-visitor state with `localStorage`

What it is: small JSON blobs stored in the visitor's browser, scoped to the page's origin (`sites.simple-host.app` for path-hosted sites — shared across sites on that host; a custom domain gets its own origin).

When to choose: anything you'd want a tiny key-value store for in a single-visitor experience — drafts, settings, app state, the user's progress. Per-visitor only; there is no sharing across browsers or devices.

```js
// save
localStorage.setItem('myapp.state', JSON.stringify(state));

// load
const raw = localStorage.getItem('myapp.state');
const state = raw ? JSON.parse(raw) : {};
```

Gotchas: typical browser quota is ~5 MB per origin. Cleared by the user at any time. On the shared content host, pages of other sites share the same origin and can see the same storage — prefix your keys. For multi-megabyte structured data, use `IndexedDB` instead.

### 4. Larger per-visitor state with `IndexedDB`

When `localStorage`'s ~5 MB cap is too small or you have a lot of small records, use `IndexedDB`. Easiest with a tiny wrapper like [`idb`](https://github.com/jakearchibald/idb) loaded from a CDN.

```js
import { openDB } from 'https://esm.sh/idb@8';
const db = await openDB('myapp', 1, {
  upgrade(db) { db.createObjectStore('items', { keyPath: 'id' }); }
});
await db.put('items', { id: 'a', text: 'hello' });
const item = await db.get('items', 'a');
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

Website Deploy serves files. There is no rewrite layer. Because sites live under `/<handle>/<sitename>/`, keep links **relative** so navigation stays inside the site path.

- **Multi-page static site**: every page is a real `index.html` under a directory. `about/` resolves to `about/index.html` under the site path.
- **SPA with framework router**: build for static export (see the `website-deploy` skill's framework section) **with a relative base**. Use the framework's hash-router mode or generate a `404.html` that bootstraps the app.
- **Pretty URLs for plain HTML**: put each "page" in its own folder with an `index.html` (`about/index.html`, `pricing/index.html`).

### 8. Custom domains

A user can serve a site from their own domain (e.g. `recipes.brand.com`). This is a distinct flow — use the `connect-domain` skill (`simple-host-website/skills/connect-domain`). Summary: `POST /v1/sites/<sitename>/domain` with `{domain}` → user adds one DNS record → poll `GET /v1/sites/<sitename>/domain` until `active`. A custom domain changes the address and adds visitor sign-in to saves; it does not change the privacy — the site is still public. Once connected, the site lives only on the domain: its `sites.simple-host.app` URL 302s there and the shared-host API takes no writes for it (agents keep writing through the apex `https://simple-host.app/v1/...`).

## Picking a capability mix

| User says | Capabilities |
|---|---|
| "a landing page / portfolio / CV" | static only |
| "a guestbook" | static + per-site JSON state (atomic `append`); add `auth.js` sign-in + `connect-domain` if each entry should belong to a signed-in person |
| "a waitlist / event RSVP / signup form" | static + append-only collection (+ a live count in state); add `auth.js` sign-in + `connect-domain` if submissions should be per-person |
| "a poll / a vote / a counter" | static + `PATCH` `inc` on state; add `auth.js` sign-in + `connect-domain` if votes should be per-person |
| "a tool that runs entirely in the browser" (calculator, drawing app, game) | static + `localStorage` for settings/saves |
| "a journal / notes app" | static + `IndexedDB` (single-visitor scope) |
| "a dashboard pulling from a public API" | static + external `fetch()` |
| "a report from an exported dataset (analytics, social, etc.)" | static export + optional client-side filtering |
| "a multi-page site" | static only — each page is its own folder + `index.html` (relative links) |
| "my own domain / brand.com" | static + `connect-domain` skill |
| "a slide deck I want to share a link to" | build with Slidev, Reveal.js, or similar and deploy the output |

If the user wants something Website Deploy can't host — per-user accounts that span devices, server-side execution, or a shared SQL database — say so explicitly and stop. Suggest they pair Website Deploy (for the static front-end) with a separate backend host (Vercel functions, Cloudflare Workers, Supabase, etc.) where their server-side logic lives. Custom domains *are* supported via `connect-domain`. Private or password-locked pages are not — every deployed site is public. Sign-in (Google or email code) gates *writing* to the backend, nothing gates *reading*; never present "sign in to save" as a private page.

## Generating a prompt for another agent

When the user wants to wire a capability into an existing site, generate a focused prompt to paste into a fresh agent chat. Keep it short.

Example prompt for "save drafts in localStorage":

> Add draft autosave to this site. On every change to the text input, write `{text, updatedAt}` to `localStorage['mysite.draft']`. On page load, restore the input value from that key if present. Show a small "Draft saved" indicator that fades out after 1 second when the save runs. No external dependencies. Use relative asset links only (sites are path-hosted).

Example prompt for "let visitors sign the guestbook" (site on its own domain, so entries belong to signed-in visitors; on the shared host the same code saves without sign-in):

> Add a guestbook to this site (deployed on simple-host, custom domain `guests.example.com`, sitename `guestbook`). Load `https://simple-host.app/auth.js` with `window.SH_CONFIG = { site: "guestbook" }` set before the tag, mount `SH.mount('#sh-auth')` next to the form, and call `await SH.requireSignIn()` before `SH.collection('entries').append({name, message})`. On a non-2xx keep the form and show "Not saved". Add `admin.html` (noindex) that lists the collection newest-first. Relative asset links only.

Mirror this shape for `IndexedDB`, external API calls, routing, etc.

## Handoff: deploy

Once the user has decided what to build, they need to deploy. Tell them to use the `website-deploy` skill, which handles registration, framework-aware build (with a relative base path), packaging, and upload. The site will be live at `https://sites.simple-host.app/<handle>/<sitename>/`. If saves should be per-person or protected, or the user wants their own address, follow with the `connect-domain` skill.
