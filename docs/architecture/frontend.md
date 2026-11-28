# Frontend and generated assets

This is the implementation map for browser pages and embedded MCP Apps.
For server ownership see [the architecture entry](../../ARCHITECTURE.md);
for adding a feature see [target.md](target.md#9-where-to-add-a-feature).
The complete method/path/edition index is [routes.md](../generated/routes.md).
The tables below group screens rather than repeat that inventory.

## Asset ownership and serving

The browser sources live in `host/webassets/static/` (the platform) and
`products/hack/webassets/static/` (Simple Hack: every `hack-*` file and the
`hack/` folder): handwritten HTML, CSS and JavaScript. `host/handler/ui.go`
and `products/hack/webassets` embed them into the Go binary. Changing a file requires rebuilding and restarting the server.
A product hands the platform two tables (`host/extension.Assets`): `Replace`, the platform
names it supplies its own file for, and `Own`, its own file names. The platform's pages can read
an own name, but its file server never answers one: the product mounts
`handler.AssetServices.ProductFiles()` at each address it wants served
(`products/hack/asset_files.go:RegisterOwnFiles`), so the chrome, text rewriting and headers are
the platform's and the address is the product's.
There is no browser build step for ordinary Host pages or the Hack website's
ES modules. MCP Apps have a separate, checked-in generated bundle.

`host/handler/assets.go:assetPipeline` owns an edition's presentation,
limits and served-text caches. `ui.go:RegisterUIRoutes` installs the static
file server and explicit page routes. `chrome.go:chromePage` composes page
partials, theme/chrome and instance text; CSP middleware stamps script nonces.
Files needing this pipeline must not bypass it through raw file serving.

Hack pages live in `products/hack/pages`, which receives the asset pipeline
through `hack.go:hackPages` and `HackHandler.ShareAssets`. The embedded tree
belongs to `host/webassets`; presentation and instance rewriting remain in
`handler`. Hack reads bundles through `webassets` and does not import `handler`
to read assets.

## Product docs build

`docs/site/src/content/docs` holds task guides in MDX. This is the one Astro/Starlight
exception to the handwritten frontend, with Node 22 used only to build. Run
`bash scripts/build-docs-site.sh` and commit `host/webassets/docs-site` with the
source and package lock. Go builds and runtime need no Node. The README in docs/site
specifies brand blocks, page budgets, feature links, and authoring conventions.

The build selects Host or Hack before MDX parsing and Pagefind indexing. Component
overrides emit the three shared chrome markers, replace Starlight's header with a
docs search bar, and remove Starlight's theme provider/select. `docs.css` maps its
colours to site.css tokens. Build scripts become external, content-addressed files.
`handler/docs.go` assembles each page once with the same passes as chromePage and
externalizes the shared chrome's scripts as well; HTML still passes through stampNonce
under adminUICSP. Only `/docs/` adds `wasm-unsafe-eval`, required by Pagefind;
JavaScript eval remains blocked. No docs page has inline scripts. Non-HTML assets
are served as bytes.

The dedicated `webassets.Docs` embed uses `all:` so `_astro` is included. `/docs/`
selects the edition's tree; unknown files return 404. `/docs` redirects to `/docs/`.
The old document route redirects permanently and the docs home's external anchor
map forwards old fragments. Swagger and its theme CSS live at `/docs/api/`.
The sitemap reads the embedded brand page list. CI runs npm ci and checks regenerated
output; `scripts/docs-site.py check` checks hashes and guide feature IDs without Node.

## Browser route to screen map

Source filenames in this table are relative to `host/webassets/static/`,
except the `hack-*` files and the `hack/` folder, which are relative to
`products/hack/webassets/static/`.
Go owners name the registration or rendering function.

| Surface | Source / screen selection | Go owner |
|---|---|---|
| Host `/` | `host-story.html`; auth returns redirect to `/dashboard`; Markdown negotiation uses the landing copy | `handler/ui.go:RegisterUIRoutes` |
| Host `/dashboard` | `index.html`, including its owner app and sign-in UI | `handler/ui.go:RegisterUIRoutes` |
| Host person showcase | `showcase.html`; host/path routing supplies the handle | `handler/showcase.go:renderShowcase`, `ownerAppOrStatic`; `handler/personhost.go` |
| Host `/analytics/{sitename}` | `analytics.html` | `handler/ui.go:RegisterUIRoutes` |
| `/admin`, `/features`, `/pricing` | `admin.html`, generated `features.html`, settings-filled `pricing.html` | `handler/ui.go:RegisterUIRoutes` |
| `/enterprise` and its brief/architecture pages | Corresponding `enterprise*.html` | `handler/ui.go:RegisterUIRoutes` |
| `/setup`, `/costs` | `setup-helper.html` with `setup/setup.js`; `costs.html` with `costs/calc.js` | `handler/ui.go:RegisterUIRoutes` |
| First boot without SITE_DOMAIN | `setup.html`, a separate setup-only server | `handler/setup.go:SetupHandler.Register` |
| Hack `/` | `hack-home.html` on every install (the film and the hosted-only links sit in `<!--sh:hosted-->` blocks) | `pages/hack_home.go:RegisterHackHome` |
| Hack `/directory`, `/get-started` | `hack-directory.html`, `hack-get-started.html` | `RegisterHackHome`, `hack_get_started.go:RegisterHackGetStarted` |
| Hack public event | `hack-event.html`, rendered with event data | `public/hack_eventpage.go:assembleHackEventPage`; `public/hack_public.go:RegisterHackPublic` |
| Hack `/signin`, `/events`, `/events/new`, `/account` | The web app (`web/hack`, embedded from `products/hack/web/dist`): its `index.html` | `pages/hack_ui.go:RegisterHackUI` |
| Hack `/e/{slug}/manage`, `/team`, `/judge` | Same page; `?s=` selects role screens | `RegisterHackUI`; `web/hack/src/routes/resolve.ts` |
| Hack `/join/{code}`, `/judge/{code}`, `/organiser/{code}`, `/team/{code}`, `/e/{slug}/apply`, `/e/{slug}/vote` | Same page; standalone public screens (`/voting-settings` redirects to the app's Results section) | `RegisterHackUI`; `resolve.ts` |

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
`host/handler/theme_test.go` guards the conventions in CLAUDE.md.
The Hack web app uses the shared kit with Hack's own tokens, light only.

| Presentation asset | Serving relationship |
|---|---|
| `static/film/` | `handler/film.go:serveFilm` serves embedded video/poster files at `/film/{file}`, with Range support and ETags; registered by `ui.go:RegisterUIRoutes` |
| `static/film/` (Hack) | `products/hack/pages/hack_film.go:RegisterHackFilm` serves Hack's film files from its own embed at one exact `/film/<file>` route each, with the same type, caching, ETag and Range answers |
| `host-ink.css` | `chrome.go:addHostInk` supplies Host's local-font presentation |
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

The signed-in and link pages are one React app in `web/hack` on the shared
kit (`web/kit`); `web/hack/README.md` is how a screen is written, registered,
put in the catalogue and checked. The server answers every app route with
the app's `index.html` and serves its hashed files at `/hack/app/`
(`pages/hack_ui.go`). Pages the server writes (home, `/directory`, the public
event page, marketing) are reached with a full page load.

## Browser and MCP App platforms

`products/hack/mcp/apps/entry/{judge,organiser}.js` import selected screens of
the old website, kept for these frames in `products/hack/mcp/apps/legacy`
(the website itself now runs on `web/hack`).
`hack-judge-view.js` and `hack-organiser-view.js` construct their contexts and
map screen API calls to tools through the MCP bridge. These are embedded
frames, not deployments of the full browser app. Resource assembly lives in
`products/hack/mcp/hack_apps.go:readHackApp`; the tool server's own screens
are in `host/mcp/apps.go`.

`apps/bundlegen/bundle.go:framePlugin` resolves the legacy screens' three
`./nav.js`, `./tour.js` and `./platform.js` imports to `apps/frame/`
(`{judge,organiser}-nav.js`, a no-op `tour.js`, and `platform.js`, whose
`onSubmit` binds a button click because the sandbox lacks allow-forms). Other
imports are bundled normally; `go generate ./products/hack/mcp/...` rebuilds
the bundles.

The organiser frame supplies `alive` and `refresh` using its own render
generation (`hack-organiser-view.js:ctxFor`). The judge frame has a narrower
context, including `noFrames: true`; its current `ctxFor` does not supply
`alive`/`refresh`.

## Reproducible generated files

Run these commands from the repository root; commit derived files with their
sources. The Go server build embeds the results and needs neither Node nor
esbuild. Generation uses esbuild's Go API pinned to **v0.28.2** in `go.mod`
and `apps/bundlegen/bundle.go:esbuildVersion`; it is not a floating CLI.

| Authored source | Generated, checked-in output | Regenerate | Freshness guard |
|---|---|---|---|
| Hack screens, `hack/kit.css`, MCP entries/adapters | `products/hack/mcp/apps/hack-{judge,organiser}-bundle.js`, `hack-{judge,organiser}-kit.css` | `go run ./products/hack/mcp/apps/gentool` | `products/hack/mcp`: `TestHackJudgeBundleFresh`, `TestHackOrganiserBundleFresh` |
| `static/openapi.yaml`, product `openapi.part.yaml` | Platform JSON, product JSON/YAML and internal contract | `make inventory` | `scripts/check-docs-sync.sh`, parsed YAML/JSON equality |
| `static/features.json`, `scripts/features-page.tmpl.html` | `static/features.html` | `python3 scripts/build-features-page.py` | `scripts/check-features-page.py` |
| `host/webassets/analytics-parse.js` | Marked script blocks in `index.html`, `showcase.html`, `analytics.html` | `bash scripts/inline-analytics-parser.sh` | `--check` plus Node parser fixtures in docs-sync |
| `host/webassets/simple-host-website/skills`, plugin manifest | `plugins/simple-host/skills` and synced manifest version | `bash scripts/sync-claude-plugin.sh` | `scripts/check-claude-plugin.sh` |
| HTTP registrations, MCP registry, FEATURES placement | `docs/generated/{routes.md,mcp-tools.md,inventory.json}` | `make inventory` | `go run ./scripts/inventory check` |

Do not hand-edit the generated bundle/CSS, JSON contract, public feature
page, copied parser blocks, plugin copies or inventories. `gentool/main.go`
writes all four MCP assets; `GenerateCSS` removes `@font-face` from the
shared kit for the embedded frame. The `go:generate` directives are in
`products/hack/mcp/hack_apps*.go`; direct `go run` above regenerates both once.

Generate both references (PyYAML required) with `make inventory`.
The platform's authored operations use `public-host`, `internal` and `legacy`.
The product part includes shared operations and adds `public-hack` operations.
The generic `webassets.PublicOpenAPISpec` filter selects Host's public audience;
Hack serves its already composed public document. Public references exclude
internal and legacy operations and unreferenced components, followed transitively.
Both `/openapi.json` and `/openapi.yaml` retain their existing JSON responses
(valid YAML 1.2); `/docs/api/` uses the same public references. Instance
rewriting/filtering happens on served bytes, not by editing generated files
per instance.

## UX atlas: design and pictures

[The atlas README](../ux-atlas/README.md) defines the design workflow:
change `features.json`, place features on screens in `spec.json`, add a line
to `changelog.json`, then build the screen in `web/hack` with its catalogue
states. `python3 docs/ux-atlas/build.py` regenerates `catalogue.json` (every
screen and state, grouped by role) from the catalogue and builds `dist/` with
each state's 390 and 1280 picture; `--check` is offline and read-only;
`--publish` additionally publishes the atlas.

The atlas specifies intended UX and supplies examples. It is not loaded by
the running app, and its sample state is not server policy. Runtime truth is
the registered routes, real screen modules and server responses/rules.
Use the [Hack domain guide](hack-domain.md) for transitions and permissions.

The reviewed Hack skill source is `products/hack/webassets/hack-toolkit/skills`.
The GitHub install address on `/get-started` points at its parent folder; there is no
second copy.

The shared destination map lives in `products/hack/rules/hackevents/navigation.json`. Run `python3 scripts/build-hack-navigation.py` for the web app's `web/hack/src/navigation.gen.ts` and the MCP Apps' `legacy/navigation-data.js`; it also holds each help record's labels (`navigation-help.json`) to the app files that show them, and `make hack-web-check` runs it with `--check`.

## Reference composition

`host/webassets/static/openapi.yaml` is the authored platform contract,
including its internal and legacy audiences. `products/hack/webassets/openapi.part.yaml`
owns product operations, shared-operation inclusions and structural overrides.
An inclusion is an operation ID, or `METHOD /path` for an operation without an ID.
Overrides use JSON Pointers relative to an operation or `components`; missing
operations and targets fail generation. The part's `tags` supplies product tags
and changes shared tag descriptions. The public JSON reference is also valid
YAML; both HTTP addresses retain the same JSON bytes.

`make inventory` runs `scripts/compose-openapi.py` and the features-page generator.
The composed public files live in the product's `static/` tree. The unserved
`products/hack/webassets/openapi.internal.json` retains internal operations for
coverage. The generator's `--check` mode rejects stale output, and the coverage
check resolves references transitively in both contracts and public files.

The platform feature catalogue is authored in `static/features.json`.
`products/hack/webassets/features.part.json` carries explicit JSON Pointer
sets, insertions and removals, plus the product's rendering metadata.
The generated product JSON and the separately authored hackathon map in
`products/hack/webassets/static/features.html` replace the platform files
through the usual product asset precedence.
