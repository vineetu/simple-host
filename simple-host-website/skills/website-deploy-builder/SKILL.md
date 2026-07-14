---
name: website-deploy-builder
description: Plan what to build on Website Deploy (simple-host.app). Helps a user decide whether their idea fits the static + light-backend model, maps it to concrete patterns (shared JSON state with atomic ops, append-only collections, private/password-locked pages on a custom domain, drop-in comments/feedback widgets, localStorage, public APIs), suggests a starter template, and produces a focused prompt for an implementation agent. Use when a user is starting a new site or describes a feature idea and needs help mapping it to what the platform can do.
---

# Website Deploy Builder

Use this skill when a user wants help deciding what to build on Website Deploy, or how to scope an idea they already have. After the user picks an approach, hand off to the `website-deploy` skill for deploy.

## What Website Deploy gives you

Website Deploy is a static-file host at `https://simple-host.app`. Each site is served at a path under one content host: `https://sites.simple-host.app/<handle>/<sitename>/` (`handle` is the owner's URL-safe handle from GET `/v1/me`). The dashboard/API stay on `https://simple-host.app` (a separate origin). Older `https://<sitename>.simple-host.app/` links still resolve (legacy). There is no server-side execution — but the API gives each site a real, server-backed backend, plus drop-in widgets and templates:

| Capability | How |
|---|---|
| HTML / CSS / JS / images / fonts served as a site | Deploy files inline as JSON (`/files`) or upload a `.tar.gz`/`.zip` |
| Per-site JSON state (≤ 1 MB, shared across all visitors) | `GET / PUT /v1/u/<handle>/sites/<sitename>/state` (legacy `/v1/sites/<sitename>/state` still works) |
| Atomic state updates (concurrent-safe counters, lists, votes) | `PATCH .../state` with `{ops:[inc/append/set/remove/removeWhere]}`; `If-None-Match` ETag for cheap polling |
| Append-only collections (signups / RSVPs / submissions) | `POST/GET /v1/u/<handle>/sites/<sitename>/collections/<name>` |
| Private (password-locked) pages | View-lock on a **connected custom domain** (isolated origin) — connect domain first |
| Custom domain | `connect-domain` skill: bind domain → one CNAME → poll until active |
| Drop-in widgets | Comments (`comments.js` + `<section id="sh-comments">`); pin-on-page feedback (`feedback.js`) |
| Starter templates | `GET /v1/templates`, `GET /v1/templates/<id>` → `{files}` ready to deploy |
| Per-visitor state | `localStorage`, `sessionStorage`, `IndexedDB` (in the browser) |
| External APIs | `fetch()` from the page to any public CORS-enabled API |
| Routing | Static files only — path-relative directories with `index.html`; SPA routing via the framework's hash router or `404.html` fallback |

If your idea needs a server you control, a shared SQL database, persistent per-user accounts, or anything that runs server-side, Website Deploy is not the right host. Say so and stop.

**Always pair a form with a viewer.** Any site that COLLECTS data (a signup, RSVP, guestbook, contact form, order) MUST also ship a second page — e.g. `admin.html` — that reads the same collection back (`GET .../collections/<name>?limit=200` → `{items:[{id,data,created_at},…]}`) and lists every entry for the owner, newest first, plus the live total from state. Link it quietly from the main page (a small "Organizer view →" in the footer). A form with nowhere to read the results is only half the feature — and the person you're building for will not think to ask for the viewer, so add it by default. Mark the viewer `<meta name="robots" content="noindex">`; it's reachable by URL (the store is public), so don't fake a password.

## How to use this skill

1. Ask the user what they're trying to build, in plain language. Don't push capabilities at them — let them describe the idea.
2. Decide whether it can run as a static site. If parts of it can't, name those parts and either propose a static-friendly substitute or recommend a different host for that piece.
3. For the part that can run statically, give them: (a) a one-paragraph explanation of how to structure it, (b) any relevant snippet (storage, routing, external API call), (c) the gotchas.
4. If they're starting from scratch, finish with a "ready to deploy" handoff: tell them to use the `website-deploy` skill, which handles registration, framework-aware build, packaging, and upload.
5. If they want to wire a capability into a site they've already deployed, generate a focused prompt they can paste into a fresh agent chat (in their site's repo). Include the pattern, the storage shape, and any gotcha — nothing else.

## Capability tree

### 1. Static hosting (the baseline)

What it is: any folder of HTML/CSS/JS/assets served as-is. Build any framework's normal production output (`dist/`, `build/`, `out/`, `public/`, `.output/public/`) and upload.

When to choose: every Website Deploy site starts here. Deploy first, then layer storage and external calls.

Gotchas: the site lives under `/<handle>/<sitename>/` on the content host, so **relative links are required**. Root-absolute paths like `/css/app.css` resolve to the wrong place and break — use `css/app.css`, `./img/x.png`, `../shared/y`. For framework builds, set the base/public path so output uses relative URLs (e.g. Vite `base: './'`, Next `basePath` / relative assets, etc.). Don't ship `node_modules/` or `.env`. Each archive is capped at 100 MB.

### 2. Per-site JSON state (shared across visitors)

What it is: a single JSON document (up to 1 MB) scoped to your site. The server stores it in Postgres; your site reads and writes it from the browser. The blob is shared across **everyone** who visits — the last writer wins.

When to choose: anything you'd want a tiny key-value store for — saved drafts, app state, a shared note, content the page just generated, configuration. **Not for secrets or per-user data**: anyone who can load the page can read the blob, and any visitor can overwrite it. If you need per-user data, store it under different keys inside the blob and key on something like `crypto.randomUUID()` saved in `localStorage`.

How to use, from a page hosted at `https://sites.simple-host.app/<handle>/<sitename>/`:

```js
// same-origin user-scoped route (canonical on the content host)
const m = location.pathname.match(/^\/([a-z0-9-]+)\/([a-z0-9-]+)/);
const url = `/v1/u/${m[1]}/sites/${m[2]}/state`;
// legacy /v1/sites/<sitename>/state still works; widgets auto-derive the right URL

// load
const state = await fetch(url).then(r => r.json());

// save
await fetch(url, {
  method: 'PUT',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify(state),
});
```

Gotchas: the content host `sites.simple-host.app` is a **shared origin** across sites (co-tenancy is accepted — don't store secrets; state was never confidential). Owners can allow extra origins via `PUT /v1/sites/<sitename>/allowed-origins` for "backend anywhere." Don't put API keys or PII in the blob — anyone who can load the page can read it. Body cap is 1 MB; sending more returns 413.

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

Gotchas: typical browser quota is ~5 MB per origin. Cleared by the user at any time. Anything saved here is visible to anything else running on the same origin — don't put secrets here. On the shared content host that means co-tenant pages can also see the same origin's storage. For multi-megabyte structured data, use `IndexedDB` instead.

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

When to choose: pulling in public data (weather, GitHub, Wikipedia, public LLM APIs the user provides their own key for, etc.).

```js
const r = await fetch('https://api.example.com/v1/things');
const data = await r.json();
```

Gotchas:
- **CORS** — the upstream API must include `Access-Control-Allow-Origin`. If it doesn't, the browser blocks the response and there's nothing Website Deploy can do; you need a server-side proxy that you control elsewhere.
- **Secrets** — anything in your client-side code is visible to anyone who opens DevTools. Don't bake in API keys. If the API requires a key, have the user paste it into a small input field and save it to `localStorage` with a "paste a fresh key" hint when it's missing.
- **Rate limits** — public APIs throttle by IP. If your site is on a shared machine, that quota is shared too.

### 6. Routing patterns

Website Deploy serves files. There is no rewrite layer. Because sites live under `/<handle>/<sitename>/`, keep links **relative** so navigation stays inside the site path.

- **Multi-page static site**: every page is a real `index.html` under a directory. `about/` resolves to `about/index.html` under the site path.
- **SPA with framework router**: build for static export (see the `website-deploy` skill's framework section) **with a relative base**. Use the framework's hash-router mode or generate a `404.html` that bootstraps the app.
- **Pretty URLs for plain HTML**: put each "page" in its own folder with an `index.html` (`about/index.html`, `pricing/index.html`).

### 7. Custom domains

A user can serve a site from their own domain (e.g. `recipes.brand.com`). This is a distinct flow — use the `connect-domain` skill (`simple-host-website/skills/connect-domain`). Summary: `POST /v1/sites/<sitename>/domain` with `{domain}` → user adds one CNAME → poll `GET /v1/sites/<sitename>/domain` until `active`. Privacy / password-lock is offered on the connected domain's isolated origin, not on a path on the shared host.

## Picking a capability mix

| User says | Capabilities |
|---|---|
| "a landing page / portfolio / CV" | static only (or a `/v1/templates` starter) |
| "a guestbook" | static + per-site JSON state (atomic `append`) |
| "a waitlist / event RSVP / signup form" | static + append-only collection (+ a live count in state) |
| "a private page to share (trip, draft, client proof)" | static + custom domain + view-lock (password) |
| "comments / discussion under a post" | static + `comments.js` widget |
| "feedback on a mockup" | static + `feedback.js` pin widget |
| "a tool that runs entirely in the browser" (calculator, drawing app, game) | static + `localStorage` for settings/saves |
| "a journal / notes app" | static + `IndexedDB` (single-visitor scope) |
| "a dashboard pulling from a public API" | static + external `fetch()` |
| "a multi-page site" | static only — each page is its own folder + `index.html` (relative links) |
| "my own domain / brand.com" | static + `connect-domain` skill |
| "a slide deck I want to share a link to" | build with Slidev, Reveal.js, or similar and deploy the output |

If the user wants something Website Deploy can't host — per-user accounts that span devices, server-side execution, or a shared SQL database — say so explicitly and stop. Suggest they pair Website Deploy (for the static front-end) with a separate backend host (Vercel functions, Cloudflare Workers, Supabase, etc.) where their server-side logic lives. Custom domains and private (password-locked) pages *are* supported via `connect-domain` + view-lock.

## Generating a prompt for another agent

When the user wants to wire a capability into an existing site, generate a focused prompt to paste into a fresh agent chat. Keep it short.

Example prompt for "save drafts in localStorage":

> Add draft autosave to this site. On every change to the text input, write `{text, updatedAt}` to `localStorage['mysite.draft']`. On page load, restore the input value from that key if present. Show a small "Draft saved" indicator that fades out after 1 second when the save runs. No external dependencies. Use relative asset links only (sites are path-hosted).

Mirror this shape for `IndexedDB`, external API calls, routing, etc.

## Handoff: deploy

Once the user has decided what to build, they need to deploy. Tell them to use the `website-deploy` skill, which handles registration, framework-aware build (with a relative base path), packaging, and upload. The site will be live at `https://sites.simple-host.app/<handle>/<sitename>/`. For a custom domain or private page, follow with the `connect-domain` skill.
