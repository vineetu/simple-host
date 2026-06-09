---
name: website-deploy-builder
description: Plan what to build on Website Deploy (ideaflow.page). Helps a user decide whether their idea fits a static-only hosting model, maps the idea to concrete in-browser patterns (per-site shared JSON state, localStorage, client-only logic, public APIs), and produces a focused prompt for an implementation agent. Use when a user is starting a new Website Deploy site or describes a feature idea and needs help mapping it to what static hosting can do.
---

# Website Deploy Builder

Use this skill when a user wants help deciding what to build on Website Deploy, or how to scope an idea they already have. After the user picks an approach, hand off to the `website-deploy` skill for deploy.

## What Website Deploy gives you

Website Deploy is a static-file host at `https://simple-host.ideaflow.page`. It serves each site at the root of its own subdomain (`https://<sitename>.ideaflow.page/`). There is no server-side execution, no per-user accounts, and no general file uploads beyond the archive you ship at deploy time — but the API does give you one piece of server-backed storage:

| Capability | How |
|---|---|
| HTML / CSS / JS / images / fonts served as a site | Any directory of files, packaged and uploaded |
| Per-site JSON state (≤ 1 MB, shared across all visitors of the site) | `GET / PUT https://simple-host.ideaflow.page/v1/sites/<sitename>/state` |
| Per-visitor state | `localStorage`, `sessionStorage`, `IndexedDB` (in the browser) |
| External APIs | `fetch()` from the page to any public CORS-enabled API |
| Routing | Static files only — `/page/` resolves to `/page/index.html`; SPA routing via the framework's hash router or `404.html` fallback |

If your idea needs a server you control, a shared database, persistent per-user accounts, or anything that runs server-side, Website Deploy is not the right host. Say so and stop.

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

Gotchas: site lives at the root of its subdomain, so root-relative paths just work. Don't ship `node_modules/` or `.env`. Each archive is capped at 100 MB.

### 2. Per-site JSON state (shared across visitors)

What it is: a single JSON document (up to 1 MB) scoped to your site. The server stores it in Postgres; your site reads and writes it from the browser. The blob is shared across **everyone** who visits — the last writer wins.

When to choose: anything you'd want a tiny key-value store for — saved drafts, app state, a shared note, content the page just generated, configuration. **Not for secrets or per-user data**: anyone who can load the page can read the blob, and any visitor can overwrite it. If you need per-user data, store it under different keys inside the blob and key on something like `crypto.randomUUID()` saved in `localStorage`.

How to use, from a page hosted at `https://<sitename>.ideaflow.page/`:

```js
const sitename = location.hostname.split('.')[0];
const url = `https://simple-host.ideaflow.page/v1/sites/${sitename}/state`;

// load
const state = await fetch(url).then(r => r.json());

// save
await fetch(url, {
  method: 'PUT',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify(state),
});
```

Gotchas: the server checks the request `Origin` (or `Referer`) — calls only work from a page hosted at the matching `<sitename>.ideaflow.page` subdomain. Don't put API keys or PII in the blob — anyone who can load the page can read it. Body cap is 1 MB; sending more returns 413.

### 3. Per-visitor state with `localStorage`

What it is: small JSON blobs stored in the visitor's browser, scoped to your origin (`<sitename>.ideaflow.page`).

When to choose: anything you'd want a tiny key-value store for in a single-visitor experience — drafts, settings, app state, the user's progress. Per-visitor only; there is no sharing across browsers or devices.

```js
// save
localStorage.setItem('myapp.state', JSON.stringify(state));

// load
const raw = localStorage.getItem('myapp.state');
const state = raw ? JSON.parse(raw) : {};
```

Gotchas: typical browser quota is ~5 MB per origin. Cleared by the user at any time. Anything saved here is visible to anything else running on the same origin — don't put secrets here. For multi-megabyte structured data, use `IndexedDB` instead.

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

Website Deploy serves files. There is no rewrite layer.

- **Multi-page static site**: every page is a real `index.html` under a directory. `/about/` resolves to `/about/index.html`.
- **SPA with framework router**: build for static export (see the `website-deploy` skill's framework section). Use the framework's hash-router mode or generate a `404.html` that bootstraps the app.
- **Pretty URLs for plain HTML**: put each "page" in its own folder with an `index.html` (`about/index.html`, `pricing/index.html`).

## Picking a capability mix

| User says | Capabilities |
|---|---|
| "a landing page / portfolio / CV" | static only |
| "a guestbook" | static + per-site JSON state |
| "a tool that runs entirely in the browser" (calculator, drawing app, game) | static + `localStorage` for settings/saves |
| "a journal / notes app" | static + `IndexedDB` (single-visitor scope) |
| "a dashboard pulling from a public API" | static + external `fetch()` |
| "a multi-page site" | static only — each page is its own folder + `index.html` |
| "a slide deck I want to share a link to" | build with Slidev, Reveal.js, or similar and deploy the output |

If the user wants something Website Deploy can't host — per-user accounts that span devices, server-side execution, shared multiplayer state, a custom domain — say so explicitly and stop. Suggest they pair Website Deploy (for the static front-end) with a separate backend host (Vercel functions, Cloudflare Workers, Supabase, etc.) where their server-side logic lives.

## Generating a prompt for another agent

When the user wants to wire a capability into an existing site, generate a focused prompt to paste into a fresh agent chat. Keep it short.

Example prompt for "save drafts in localStorage":

> Add draft autosave to this site. On every change to the text input, write `{text, updatedAt}` to `localStorage['mysite.draft']`. On page load, restore the input value from that key if present. Show a small "Draft saved" indicator that fades out after 1 second when the save runs. No external dependencies.

Mirror this shape for `IndexedDB`, external API calls, routing, etc.

## Handoff: deploy

Once the user has decided what to build, they need to deploy. Tell them to use the `website-deploy` skill, which handles registration, framework-aware build, packaging, and upload. The site will be live at `https://<sitename>.ideaflow.page/`.
