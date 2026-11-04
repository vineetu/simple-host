# Frontend and generated assets

This is the implementation map for browser pages and embedded MCP Apps.
For server ownership see [the architecture entry](../../ARCHITECTURE.md);
for adding a feature see [target.md](target.md#9-where-to-add-a-feature).
The complete method/path/edition index is [routes.md](../generated/routes.md).
The tables below group screens rather than repeat that inventory.

## Asset ownership and serving

The browser sources live in `internal/handler/static/`: handwritten HTML,
CSS and JavaScript. `internal/handler/ui.go` embeds `all:static` into the Go
binary. Changing a file requires rebuilding and restarting the server.
There is no browser build step for ordinary Host pages or the Hack website's
ES modules. MCP Apps have a separate, checked-in generated bundle.

`internal/handler/assets.go:assetPipeline` owns an edition's presentation,
limits and served-text caches. `ui.go:RegisterUIRoutes` installs the static
file server and explicit page routes. `chrome.go:chromePage` composes page
partials, theme/chrome and instance text; CSP middleware stamps script nonces.
Files needing this pipeline must not bypass it through raw file serving.

Hack pages live in `internal/hackhttp`, which receives the asset pipeline
through `hack.go:hackPages` and `HackHandler.ShareAssets`. The embedded tree
still belongs to `handler`; the proposed `internal/webassets` extraction in
`target.md` is not the current layout. Hack does not import `handler` to read
assets.

## Browser route to screen map

All source filenames in this table are relative to `internal/handler/static/`.
Go owners name the registration or rendering function.

| Surface | Source / screen selection | Go owner |
|---|---|---|
| Host `/` | `host-story.html`; auth returns redirect to `/dashboard`; Markdown negotiation uses the landing copy | `handler/ui.go:RegisterUIRoutes` |
| Host `/dashboard` | `index.html`, including its owner app and sign-in UI | `handler/ui.go:RegisterUIRoutes` |
| Host person showcase | `showcase.html`; host/path routing supplies the handle | `handler/showcase.go:renderShowcase`, `ownerAppOrStatic`; `handler/personhost.go` |
| Host `/analytics/{sitename}` | `analytics.html` | `handler/ui.go:RegisterUIRoutes` |
| `/admin`, `/features`, `/pricing` | `admin.html`, generated `features.html`, settings-filled `pricing.html` | `handler/ui.go:RegisterUIRoutes` |
| `/enterprise` and its brief/architecture pages, `/hackathons` | Corresponding `enterprise*.html`, `hackathons.html` | `handler/ui.go:RegisterUIRoutes` |
| `/setup`, `/costs` | `setup-helper.html` with `setup/setup.js`; `costs.html` with `costs/calc.js` | `handler/ui.go:RegisterUIRoutes` |
| First boot without SITE_DOMAIN | `setup.html`, a separate setup-only server | `handler/setup.go:SetupHandler.Register` |
| Hack `/` | Canonical instance: `hack-home.html`; other instances: `hack-story.html` | `hackhttp/hack_home.go:RegisterHackHome` |
| Hack `/directory`, `/get-started` | `hack-directory.html`, `hack-get-started.html` | `RegisterHackHome`, `hack_get_started.go:RegisterHackGetStarted` |
| Hack public event | `hack-event.html`, rendered with event data | `hackhttp/hack_eventpage.go:assembleHackEventPage`; `hack_public.go:RegisterHackPublic` |
| Hack `/signin`, `/events`, `/events/new`, `/account` | `hack/app.html` → `hack/app.js` → sign-in, `landing-your-events`, `organiser-create-event`, `account` | `hackhttp/hack_ui.go:RegisterHackUI` |
| Hack `/e/{slug}/manage`, `/team`, `/judge` | Same shell; `?s=` selects role screens | `RegisterHackUI`; `hack/nav.js:resolveRoute` |
| Hack `/join/{code}`, `/judge/{code}`, `/organiser/{code}`, `/team/{code}`, `/e/{slug}/apply` | Same shell; standalone `public-*` modules | `RegisterHackUI`; `hack/nav.js:resolveRoute` |
| Hack `/e/{slug}/vote`, `/voting-settings` | `hack-voting.html`, separate from the module app | `RegisterHackUI` |

Public shells do not establish API authority. For example, `/admin` paints
its data only after the gated admin API answers (`ui.go:RegisterUIRoutes`).
Hosted site files are uploaded content, not this embedded frontend; their
routing and access gates are described in [persistence.md](persistence.md).

The public setup helper builds Enterprise `values.yaml` and blank
`secrets.env` plus commands to create/reuse a persistent Secret. Both Helm
and Kubernetes YAML use the pinned chart; YAML is `helm template` then
`kubectl apply`. This is configuration generation for an existing cluster,
not cloud provisioning (`static/setup/setup.js:build`, download
assembly near `values.yaml`). See [advanced settings](../advanced/README.md).

Shared page chrome uses `site.css` tokens and the shared theme switch;
`internal/handler/theme_test.go` guards the conventions in CLAUDE.md.
The Hack module app uses its own light kit.

| Presentation asset | Serving relationship |
|---|---|
| `static/film/` | `handler/film.go:serveFilm` serves embedded video/poster files at `/film/{file}`, with Range support and ETags; registered by `ui.go:RegisterUIRoutes` |
| `host-ink.css` / `hack-ink.css` | `chrome.go:addHostInk` supplies Host's local-font presentation; `addHackInk` supplies Hack's separate ink styling, never loaded by Host |
| `host-status.css` | `chrome.go:hostedStatusPage` embeds the stylesheet and its data font in bare Host status pages; `passcode.go:hostPasscodeGateCSP` permits its style hash and `font-src data:` |
| First-boot setup styles/fonts | `setup.go:SetupHandler.Register` explicitly serves `/host-ink.css` and `/fonts/{file...}` alongside the setup page |

On nonce-protected account/consent pages, inline `on*=` attributes are
blocked: bind events with `addEventListener` (`ui.go:adminUICSP`,
`connector.go:consentHeaders`). A script nonce does not authorize event
handler attributes.

Hack onboarding uses exclusive native `<details name>` groups, a toggle
fallback for older browsers, hash-linked AI row opening, and Copy buttons
with Clipboard API → `execCommand` → selected-text fallbacks. Downloads
start collapsed under Other ways to install
(`static/hack-get-started.html:openLinkedAI` and its inline listeners).
The dedicated browser check is `node scripts/check-hack-get-started.mjs`:
HACK_GET_STARTED_URL selects the served page; it checks 320/390/1280px,
both themes, accordions/deep links, copy fallbacks, local-only requests and
browser/CSP errors, plus native accordions without JavaScript. Its fixed
five-AI-row assertion currently trails the page's eight rows; review those
expectations before using it to verify the current page.

## Hack screen conventions

`hack/app.html` loads `hack/app.js` as a module. `hack/nav.js:resolveRoute`
selects a screen ID, role, event slug and section. The app dynamically imports
`screens/<id>.js`, then calls optional `title(ctx)` and `render(ctx)`.
`hack/shell.js:mount` supplies the sidebar, event switcher and main container.

| Browser route | Main modules |
|---|---|
| `/events` | `landing-your-events.js` |
| `/events/new` | `organiser-create-event.js` |
| `/e/{slug}/manage?s=overview\|people\|teams\|entries\|judging\|results\|settings\|event-page\|announcements` | Matching `organiser-*.js` |
| Manage detail sections `team`, `entry`, `assignments` | `organiser-team-detail.js`, `organiser-entry-detail.js`, `organiser-judging-assignments.js` |
| `/e/{slug}/team?s=home\|schedule\|announcements\|team\|entry\|website\|results` | Matching `participant-*.js` |
| `/e/{slug}/judge?s=home\|queue\|rubric\|scoring` | Matching `judge-*.js` |
| Invite/application links | `public-join.js`, `public-judge.js`, `public-organiser-invite.js`, `public-team-link.js`, `public-apply.js` |

The actual event role returned by the server overrides a stale role in the
URL. Unknown sections fall back to the role's first section. Keep these
rules in `nav.js:resolveRoute` and `app.js:route`, not in copied screen routers.

A browser `ctx` contains `main`, `api`, `ui`, `me`, `events`, `event`, `role`,
`params`, `go`, `refresh` and `alive`. `app.js:route` increments a generation
on navigation; `ctx.alive()` compares against that generation. After an
asynchronous read, check lifetime before painting or modifying shared caches.
This suppresses stale results; it does not cancel the underlying fetch.
`api.js:api` has no route-wide AbortSignal.

The initial `/v1/me` and `/v1/hack/events` promise is cached for the browser
session. `ctx.refresh()` clears it and routes again; `api.js:onEventsChanged`
also invalidates it after the listed membership-changing operations.
The event itself is fetched again on event-backed navigation
(`app.js:route`, `api.js:MEMBERSHIP_MUTATIONS`).

Screens can own narrower caches. Event page tabs preserve drafts locally,
but a fresh entry to the screen reloads content and website data. Its
`cacheStale(ctx, ev)` checks both generation and slug before writing a cache
following an await (`screens/organiser-event-page.js:cacheStale`). Do not
turn that cache into a second authority for current server state.

Use `ctx.go`/`nav.js:href` for app navigation and `ctx.refresh` after mutations
that need fresh shell data. `app.js:go` performs a full navigation to sign-in;
entering the judge route from a narrower CSP reloads the document so the
server can allow video/team-site frames (`hack_ui.go:hackAppCSP`).

Shared helpers are ordinary source modules:

- `hack/api.js`: JSON/byte requests, sign-out, membership invalidation and
  authenticated downloads. A 401 calls the shared signed-out handler.
- `hack/ui.js`: escaping, link validation, date/stage formatting, errors,
  dialogs, toasts and the unsaved-edit guard.
- `hack/schedule.js`, `needs.js`, `leave.js`: shared schedule, next-action
  and leave flows; `_orgA-util.js`, `_orgB-util.js`, `_judge-util.js` and
  other screen utilities hold role-specific helpers.
- `screens/judge-scoring.js`: owns an AbortController for its keyboard
  listener, aborting the previous binding and checking its root is connected.
  That controller is not a global screen/fetch lifecycle manager.

`ui.js:confirmDiscardForLeave` is also called on browser Back/Forward;
`app.js:route` ends any tour before mounting a new screen. Preserve these
behaviours when introducing another navigation path.

## Sign-in is part of the module app

The classic `static/hack-app.html` is gone. Hack `/signin` is implemented
by `hack/app.js:routeSignIn`, using `hack/app.html` and `window.SHSignIn`.
`hack_ui.go:RegisterHackUI` requests the sign-in script only for `/signin`;
`handler/chrome.go` supplies the shared sign-in widget through its pipeline.
The other app routes do not need that widget loaded.

The app validates same-origin return paths, retains invite destinations,
and consumes email/OAuth browser nonces before completing sign-in.
`hack_home.go:RegisterHackHome` forwards old landing-page email returns to
`/signin`. Host sign-in continues to use `index.html`; deleting the classic
Hack shell did not remove the shared sign-in component.

## Browser and MCP App platforms

`internal/mcp/apps/entry/{judge,organiser}.js` import selected real screens.
`hack-judge-view.js` and `hack-organiser-view.js` construct their contexts and
map screen API calls to tools through the MCP bridge. These are embedded
frames, not deployments of the full browser app. Resource assembly lives in
`internal/mcp/hack_apps.go:readHackApp` and `apps.go`.

`apps/bundlegen/bundle.go:framePlugin` substitutes exactly three modules:
`hack/nav.js`, `hack/tour.js` and `hack/platform.js`. Other screen imports
are bundled normally. There are no generation-time text patches to screens.

| Concern | Browser source | MCP frame source |
|---|---|---|
| Navigation / hrefs | `hack/nav.js`, app history/location | `apps/frame/{judge,organiser}-nav.js`, frame router |
| Scoring URL update | `platform.js:replaceUrl`, history | Best-effort history update in isolated frame |
| Judge invite code / accepted state | Read `/judge/{code}`, navigate to judge page | `window.__hackJudgeCode`, `window.__hackJudgeJoined` |
| Form action binding | `platform.js:onSubmit`, native submit listener | `apps/frame/platform.js:onSubmit`, button click; sandbox lacks allow-forms |
| Open Sign-up section | Real URL hash | Frame's transient `__hackOrgSignupOpen` flag |
| Guided tours | `hack/tour.js` | No-op `apps/frame/tour.js` |

The organiser frame supplies `alive` and `refresh` using its own render
generation (`hack-organiser-view.js:ctxFor`). The judge frame has a narrower
context, including `noFrames: true`; its current `ctxFor` does not supply
`alive`/`refresh`. Its bundled queue/scoring/rubric/invite screens use that
context, while browser-only `judge-home.js` uses `ctx.alive()`. Do not assume
that every browser screen can be added to either frame unchanged.

## Reproducible generated files

Run these commands from the repository root; commit derived files with their
sources. The Go server build embeds the results and needs neither Node nor
esbuild. Generation uses esbuild's Go API pinned to **v0.28.2** in `go.mod`
and `apps/bundlegen/bundle.go:esbuildVersion`; it is not a floating CLI.

| Authored source | Generated, checked-in output | Regenerate | Freshness guard |
|---|---|---|---|
| Hack screens, `hack/kit.css`, MCP entries/adapters | `internal/mcp/apps/hack-{judge,organiser}-bundle.js`, `hack-{judge,organiser}-kit.css` | `go run ./internal/mcp/apps/gentool` | `internal/mcp`: `TestHackJudgeBundleFresh`, `TestHackOrganiserBundleFresh` |
| `static/openapi.yaml` | `static/openapi.json` | Command below | `scripts/check-docs-sync.sh`, parsed YAML/JSON equality |
| `static/features.json`, `scripts/features-page.tmpl.html` | `static/features.html` | `python3 scripts/build-features-page.py` | `scripts/check-features-page.py` |
| `web/analytics-parse.js` | Marked script blocks in `index.html`, `showcase.html`, `analytics.html` | `bash scripts/inline-analytics-parser.sh` | `--check` plus Node parser fixtures in docs-sync |
| `simple-host-website/skills`, plugin manifest | `plugins/simple-host/skills` and synced manifest version | `bash scripts/sync-claude-plugin.sh` | `scripts/check-claude-plugin.sh` |
| HTTP registrations, MCP registry, FEATURES placement | `docs/generated/{routes.md,mcp-tools.md,inventory.json}` | `make inventory` | `go run ./scripts/inventory check` |

Do not hand-edit the generated bundle/CSS, JSON contract, public feature
page, copied parser blocks, plugin copies or inventories. `gentool/main.go`
writes all four MCP assets; `GenerateCSS` removes `@font-face` from the
shared kit for the embedded frame. The `go:generate` directives are in
`internal/mcp/hack_apps*.go`; direct `go run` above regenerates both once.

Exact OpenAPI conversion (PyYAML required):

```sh
python3 -c "import yaml,json;json.dump(yaml.safe_load(open('internal/handler/static/openapi.yaml')),open('internal/handler/static/openapi.json','w'),indent=2)"
```

Source OpenAPI operations use four `x-audience` values: `public-host`,
`public-hack`, `internal` and `legacy`. `handler/hack_assets.go:publicOpenAPISpec`
selects the product's public operations and removes unused tags/components;
internal and legacy operations remain in the source for coverage.
`handler/instancehost.go:serveRewrittenAsset` applies this to both
`/openapi.json` and `/openapi.yaml`: both serve filtered JSON (valid YAML 1.2).
`static/docs.html` loads that filtered `/openapi.json` reference. Instance
rewriting/filtering happens on served bytes, not by editing generated files
per instance.

## UX atlas: design, prototype and runtime

[The atlas README](../ux-atlas/README.md) defines the design workflow:
change `features.json`, place features on screens in `spec.json`, update
prototype views and `changelog.json`, then implement the real screens.
`hack/kit.css` is the stylesheet source copied into the prototype.

`python3 docs/ux-atlas/build.py --check` is offline and read-only. It checks
feature anchors, screen coverage, prototype existence and committed
`prototype/nav.js`/`prototype/files.json` freshness. A normal
`python3 docs/ux-atlas/build.py` regenerates those files, copies the kit and
builds `dist/`; `--publish` additionally publishes the atlas.

The atlas specifies intended UX and supplies examples. It is not loaded by
the running app, and its sample state is not server policy. Runtime truth is
the registered routes, real screen modules and server responses/rules.
Use the [Hack domain guide](hack-domain.md) for transitions and permissions.
