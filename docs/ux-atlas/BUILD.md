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
  api.js            fetch wrapper (X-API-Key from localStorage 'apiKey', 401 → signed out); also
                    isSignedOut(e) (check an Error against that signal, instead of restating
                    `e.message === 'signed out'`) and downloadFile(path, filename, button) (fetch
                    bytes with the key header and save them, since a bare <a href> can't carry it;
                    returns true/false so a caller can show its own success toast)
  shell.js          app frame: event switcher, role sidebar, status header, phone tabs, menu
  ui.js             shared parts, no screen logic: ui.dialog (the one scrim/focus-trap/Escape
                    primitive) and ui.confirm built on it (body takes a string, Node or Node[]);
                    ui.live(main) (one status/toast line, replacing every screen's own `setLive`);
                    ui.errText(r, {codes, fallback}) (server-message-first error text, or
                    codes-first when a screen group passes its own code→copy map) plus the shared
                    ui.ERROR_MESSAGES map; ui.el (DOM-builder); ui.writeLockReason(ev, adminView);
                    ui.fetchBinary/ui.showImage (authenticated images); ui.localInputValue(iso,
                    event|zone, {dateOnly}) (an instant's wall-clock value for a date/datetime-local
                    field, in the event's zone -- reading the field back on save needs no inverse);
                    ui.markInk(color) and ui.safeHref(url) (the shared luminance-ink and http(s)-only
                    link checks -- a few screens still carry their own copy until they're switched
                    over to these); plus toast, copy button, stage names, escaping, ui.dateTime for
                    display (errText's server-message-first text always loses to a generic "Something
                    went wrong. Try again." on a 5xx, since the server's own 5xx body is just
                    jargon).
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
| `/e/{slug}/manage` | organiser sections, chosen by `?s=` : overview (default), event-page (+`&tab=` basics (default), about, schedule, sponsors, faq, look, website), announcements, people, teams, team (+`&team=`), entries, entry (+`&team=`), judging, assignments, results, settings. `?s=event-page&tab=announcements` and `#announcements` redirect to `?s=announcements` |
| `/e/{slug}/team` | participant sections by `?s=` : home (default), schedule, announcements, team, entry, website, results |
| `/e/{slug}/judge` | judge sections by `?s=` : home (default), queue, scoring (+`&team=`), rubric |

Inside the app, navigation is `history.pushState` + re-render (no full reload); back/forward work.
Old deep links keep working: unknown `?s=` values fall back to the section's home.

## Switching between old and new

- The server serves `static/hack/app.html` instead of `hack-app.html` when the cookie
  `sh_hack_ui=next` is set. `next` is the default (one constant, flipped 2026-10-30, every role
  verified on production); `?ui=classic` on any of those routes sets the cookie to `classic` and
  keeps the old app reachable, `?ui=next` sets it back to `next`. `?ui=new` is also accepted, as
  an alias of `?ui=next` (Grok retest round 1 BROKEN #1: someone stuck on classic and guessing
  the opposite of `?ui=classic` types `new`, not `next` -- both land on the new app).
- The account page and the shell's Help menu no longer offer "Use the old screens" (owner
  decision 2026-10-30: classic is being retired, not kept as a standing option). The one place
  that still links to it is the "this screen is being rebuilt" fallback card (`app.js`, shown
  when a screen module import fails) -- that is the one case someone genuinely needs the old
  screen today, until every screen module exists and `?ui=classic` itself is deleted.
- Classic itself (`hack-app.html`) carries the other direction back: judge Help (`#judge-new-ui`),
  manage Help (`#manage-new-ui`) and the Account page (`#acct-new-ui`) each have a visible "Use the
  new screens" link to `?ui=next`, so someone who ended up on `?ui=classic` (a bookmark, a shared
  link, the fallback card above) can get back to the new app without knowing the query parameter.

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

## Form layout

Every label-plus-control in the app and on the public pages is one of the patterns below. Their
spacing and alignment live in one block of `kit.css` ("Form layout"), driven by tokens on `:root`:
`--field-gap` (16px, field to field and any form block to the next), `--label-gap` (6px, label to
control to help to error), `--control-h` (40px, every single-line input and select, and a button
beside one), `--check-size`, `--check-gap` and `--label-size`. A spacing or alignment problem is
fixed there, never with a `style=""` attribute or a one-off margin in a screen module.

| Pattern | Markup | Use it for |
|---|---|---|
| Stacked field (default) | `<div class="field"><label for="x">Label</label><input class="input" id="x"><p class="help">…</p><p class="err"></p></div>` | Every form field unless a row below says otherwise. `.lab-row` (label + `.opt` + `.counter`) replaces the label when it needs extras. `<span class="lab">` labels a group or a read-only value. `<fieldset class="field"><legend>` groups radios or several fields. |
| Inline field | `<div class="field inline"><label for="x">Show</label><select class="select" id="x">…</select></div>` | A short label beside one control: a filter above a list, a value per row (tied-rank places). Stacks on phone. Not for ordinary form fields. |
| Check or radio row | `<label class="check"><input type="checkbox"><span>Label</span></label>`, with help: `<span><b>Title</b><span>Help</span></span>` | Every checkbox and radio that isn't a choice card. The box lines up with the first text line and help is indented under the label. Consecutive rows are 12px apart. |
| Toggle setting row | `<div class="setting"><div class="t"><b>Title</b><span>Help</span></div><div class="ctl"><button class="toggle" role="switch">…</button></div></div>` | Settings that save on change (instant toggles) and danger-zone rows. |
| Field row | `<div class="field-row"><div class="field">…</div><div class="field grow">…</div></div>` | Several fields side by side (dates and time zone, filters). Every control is the same height. On phone the fields pair up, and a `.grow` field or a date-and-time field takes the full width. |
| Control row | `<div class="control-row"><input class="input"><button class="btn">Copy</button></div>` | One control with its buttons beside it (name and Save, link and Copy, judge, team and Add). Inputs and buttons share `--control-h`. |
| Form actions | `<div class="form-actions"><button class="btn primary">Save</button><button class="btn">Cancel</button><span class="hint">…</span></div>` | The Save row under any form in a card. It is 16px below the last field with the primary button first, and a short status goes in a `.hint` after the buttons. Long editor cards with a dirty state use `.card-foot` instead (see the Rhythm block), dialogs use `.acts`. |

Rules of thumb:

- Prefer a `for=` label beside its control. A label that wraps its control
  (`<label>Name<input></label>`, the DOM-built editors) still gets the same gap from CSS, so it is
  not wrong, just not the pattern to copy.
- Help belongs inside its `.field`, under the control. A standalone `.help` paragraph is for
  help that covers a whole card.
- The last form block in a card has no bottom margin of its own, and a card heading is 12px above the
  first field. Both are automatic, so do not add margins to get there.
- Empty `.help` and `.err` slots take no room, so screens can render them up front and fill them
  later. An action row whose buttons are all hidden takes no room either.

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
