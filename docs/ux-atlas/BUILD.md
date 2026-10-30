# Building the new Simple Hack app from the atlas

The signed-in Simple Hack app is being rebuilt from the UX atlas (`docs/ux-atlas/`). This file is
the contract every change follows. Read it whole before touching `internal/handler/static/hack/`.

## Goals

1. Each atlas screen is built exactly as its prototype and `spec.json` entry describe.
2. It stays easy to change: one file per screen, one shared kit, the same names as the atlas.
3. No behaviour is lost. Every capability in `features.json` that has a website home works.
4. The JSON API, MCP tools and server rules do not change. This is a new face on the same API.

## Layout

```
internal/handler/static/hack/
  app.html          entry page; the server serves it on every signed-in route when the new UI is on
  kit.css           THE kit. Same file the atlas prototype uses (build.py copies it into the atlas)
  app.js            boot: sign-in check, load me + events, router, mount shell, render screen
  api.js            fetch wrapper (X-API-Key from localStorage 'apiKey', 401 → signed out), helpers
  shell.js          app frame: event switcher, role sidebar, status header, phone tabs, menu
  ui.js             shared parts: confirm dialog, toast, copy button, field helpers, time formatting,
                    stage names, escaping. No screen logic.
  nav.js            sidebar items per role (from spec.json roles[].nav; stage rules)
  screens/<atlas-screen-id>.js   one module per atlas screen, e.g. screens/judge-scoring.js
```

- Plain ES modules (`<script type="module">`), no build step, no framework, no dependencies.
  The repo rule "HTML, CSS and JS are hand-written and embedded" still holds.
- The CSP allows scripts from `'self'` and inline scripts only with the nonce. Do not use inline
  event handlers (`onclick=`) or inline `<script>` blocks other than the nonce-carrying one the
  server already supports. Attach listeners in JS.
- Every module is small and does one thing. A screen module exports
  `export async function render(ctx)` returning nothing; it fills `ctx.main` (the `<main>` element).
  `ctx` = `{ main, api, ui, me, event, role, params, go(path), refresh(), alive() }`.
  `alive()` returns false once this render's route has been superseded (a newer navigation
  started before an `await` resolved). Any screen that does async work and then performs a
  global effect — `history.pushState`/`replaceState`, `document.title`, a toast, a dialog — must
  check `ctx.alive()` right after each `await` and before that effect, so a slow, abandoned
  request can't act on a screen the person has already navigated away from.
  It may also export `title(ctx)` for the document title.
- Markup uses only kit.css classes, the same markup patterns as the prototype file of the same name
  (`docs/ux-atlas/prototype/<screen-id>.html` and its `~state` variants). If the prototype and the
  real data disagree, the real data wins and the screen's spec entry decides the copy.
- Escape everything that came from the API (`ui.esc`). Never build HTML from unescaped data.

## Routes (unchanged URLs)

| URL | Screen(s) |
|---|---|
| `/events` | `landing-your-events` |
| `/events/new` | `organiser-create-event` |
| `/account` | `account` |
| `/signin` | the existing shared sign-in card (`<!--sh:signin-->` marker, `SHSignIn`) — reuse, do not rebuild |
| `/join/{code}` | `public-join` |
| `/judge/{code}` | `public-judge` |
| `/organiser/{code}` | `public-organiser-invite` |
| `/e/{slug}/manage` | organiser sections, chosen by `?s=` : overview (default), event-page, people, teams, team (+`&team=`), entries, entry (+`&team=`), judging, assignments, results, settings |
| `/e/{slug}/team` | participant sections by `?s=` : home (default), schedule, announcements, team, entry, website, results |
| `/e/{slug}/judge` | judge sections by `?s=` : home (default), queue, scoring (+`&team=`), rubric |

Inside the app, navigation is `history.pushState` + re-render (no full reload); back/forward work.
Old deep links keep working: unknown `?s=` values fall back to the section's home.

## Switching between old and new

- The server serves `static/hack/app.html` instead of `hack-app.html` when the cookie
  `sh_hack_ui=next` is set. `?ui=next` on any of those routes sets it (Path=/, SameSite=Lax,
  Secure, 1 year) and `?ui=classic` clears it. Default stays classic until the owner flips it.
- When every role is verified on production, the default flips to next (one constant), and
  `?ui=classic` keeps the old app reachable until it is deleted.
- The account page and the shell's Help menu offer "Use the old screens" while both exist.

## Sign-in and session

Reuse exactly what `hack-app.html` does today (read it, do not reinvent): `localStorage.apiKey`,
the `SHArrival` handoff, `saveNext`/`safeNext` for returning after sign-in, the join-link
re-auth rule, the email-code nonce. Move that logic into `app.js`/`api.js` unchanged in behaviour.
Never print or log the key.

## Data

- Every call goes through `api.js` to the same endpoints `hack-app.html` uses. `features.json`
  lists the endpoint for each capability. Where the old app shows a confirm or computes a count
  before a destructive call (score deletion, vote removal, approval-off, regenerate link), the new
  screen does the same, with the copy from `spec.json`.
- One screen's data loads in that screen's module. The shell gets `me` and the event list once.
- Saving follows the spec: one Save per section for forms, instant save for toggles, ticks and
  judge scores (with a visible "Saved" note).

## Look

Light only (owner rule, INTENT 2026-10-30 decision). The app does not use the site chrome
(`<!--sh:head-->`, `<!--sh:header-->`): it has its own kit and shell. It carries
`<!--sh:signin-->` for the sign-in card only. Add `static/hack/app.html` to the theme test's
light-only exception, like the hack landings.

## Done means

1. The screen matches its prototype at 1280×800 and 390 px (screenshot side by side).
2. Every feature id listed on that screen in `spec.json` works against the real API.
3. The role's browser test passes on a local server and then on production with test accounts.
4. `go vet ./... && go test ./internal/handler/ ./internal/mcp/` pass.
5. The atlas shows the built screenshot next to the prototype (`build.py` "Built" view).

## Changing things later

Atlas first (features.json, spec.json, prototype, changelog), then the one screen module, then
its test, then deploy. Removing a feature: remove it from features.json and the screen; build.py
reports leftovers.
