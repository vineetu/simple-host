> Historical — kept for context; the current truth is INTENT.md, FEATURES.md and ARCHITECTURE.md.

# Architecture notes, 2026-10 (moved out of ARCHITECTURE.md)

These dated feature notes used to follow the Checks section of `ARCHITECTURE.md`. They were
moved here verbatim on 2026-11-02 (code cleanup step 1.4) so the entry document describes the
current system rather than a change history. Each note was true on its date; later changes may
have superseded parts of it (for example, the classic Simple Hack app is due to be removed).

Hosted Simple Hack presentation (2026-10-04): `hack_home.go` serves `hack-home.html` at `/` when `hackInkBaseURL` is `https://simple-hack.app`, and `hack-story.html` otherwise (2026-10-13). The canonical page uses `film.go` for local video cuts and posters and static SVG for its after-section figures; `chrome.go` adds `hack-ink.css` only to Hack chrome, after layout styles. Fonts are local WOFF2/OFL assets; rough.js stays inline on the drawn page. `hack_colors.go` maps event slugs or stored palette names to consistent accents; `z8-hack-ink-color.sql` adds the optional choice. Host index/dashboard/site.css remain unchanged.

Simple Hack signed-in web app (2026-10-30): `internal/handler/static/hack/{app.html,kit.css,public.css,api.js,ui.js,shell.js,nav.js,tour.js,tours.js}` plus `screens/*.js` (one file per atlas screen: `organiser-overview.js`, `participant-team.js`, `judge-queue.js`, and so on) are a plain ES-module app with no build step, embedded via `go:embed` and served by `RegisterHackUI` in `hack_ui.go`. It is now the default for every signed-in Hack page (`/signin`, `/account`, `/events`, `/events/new`, `/e/{slug}/team`, `/e/{slug}/judge`, `/e/{slug}/manage`, `/join/{code}`, `/judge/{code}`, `/organiser/{code}`). A cookie, `sh_hack_ui`, remembers `next` or `classic` once a visitor passes `?ui=`, and `?ui=classic` keeps the prior `hack-app.html` screens reachable (`?ui=new` and `?ui=next` are accepted aliases). `/signin` always serves the shared classic page regardless of the cookie, since sign-in keeps the existing site chrome and SSO handoff. The public pages `hack-event.html`, `hack-directory.html` and `hack-voting.html` were restyled on the same `kit.css`/`public.css` without joining the signed-in app or its routing. A UI change or new screen for this app starts in `docs/ux-atlas`, the living spec, and is implemented here afterward.

Full Simple Hack instances (2026-10-04): `hack_instance.go` rewrites only
Hack presentation/skill text to PUBLIC_BASE_URL; it never applies the older
Simple Host shared-origin/control-plane note to the full event platform.
MCP Hack instructions use the server's configured APIHost. Root token/nonce
returns redirect locally to `/signin`, preserving the existing nonce verifier.

Account homes (2026-10-05): `users.home_site_id` is a nullable site FK. `home.go`
serves the selected site's current tree at the person host root through `siteGate`;
missing paths retain legacy site-link redirects. API lookups, origin authorization,
OAuth return addresses and session checks bind to that site and the exact request host.
Unavailable sites render the showcase; deleting a site clears the FK at soft delete,
and hard delete uses ON DELETE SET NULL. Renames preserve it. The owner app stays on
its apex origin. Hosted events have no home-setting surface.

The public JSON feed `/v1/u/{handle}/showcase.json` and showcase HTML share one
projection in `showcase.go`. Metadata is parsed locally from visible deployed
indexes; hidden/protected/offline/taken-down sites never reach this path. The
feed is public, credential-free CORS, cached for 30 seconds and limited per
resolved owner ID and client IP with the public-read settings. Bio is plain text in
users.showcase_bio; site-ID pin/order settings survive rename and restore. The shared
projection sorts pinned first, ascending order, then existing creation order. All
curation writes require the owner key or connector, excluding deploy-only keys.
Whole-space custom apex domains are not implemented: the family issuer only checks
pre-provisioned wildcard certificates and its template has no account-home apex route.

Your home page (2026-10-05): home selection, public showcase feed, bio, pins and
manual order also work on small-box path installs. The public person page opens
the selected site’s usual URL; the owner dashboard keeps its own origin. No
whole-space domain is included. See [docs/your-home-page.md](docs/your-home-page.md).

Website file caps (2026-10-05): `h/storagecaps.go` checks the post-prune footprint before the shared create/update commits write any files. `storage/footprint.go` counts only current and vN folders, including Recently deleted sites in account totals; backend resources remain separate. Account deploy locks cover promotion and pruning across site names. `h/versions.go` retains actual newest rows plus any older live version and resolves allowlists/default overrides through handle aliases.

Storage story (shipped 2026-10-06): site_storage_resources.write_mode preserves
full writes by default; site_storage_kv.writer_id and site_storage_files track
writer identities. site_storage_rows.go builds bound fixed INSERT/SELECT for
visitors in add/own databases. Own schema tables have an indexed visitor_id TEXT.
Host files use SITE_STORAGE_FILES_MAX_BYTES separately from KV/SQLite. Hack keeps
its old pooled budget. No per-visitor tables/views or visitor-provided SQL.

Order history (shipped 2026-10-06): fixed add-only SQLite inserts on own-read
databases run in BEGIN IMMEDIATE. Declared foreign keys use indexed joins to
check the inserted row's actual reference (including defaults) and parent
visitor_id before COMMIT; refusals roll back the insert. Composite and implicit
primary-key references are supported; optional links must be all NULL, with partial composite NULLs refused.
Foreign keys are explicitly enabled. Parent checks use the RETURNING row ID
and allow owner-created catalog rows with NULL/empty identities. The server overrides visitor_id and stamps UTC created_at on visitor
inserts. Orders/history use separate reads; no include option.

Storage access presets (2026-10-10): `h/site_storage_access.go` holds the
matrix (read, add, edit, delete; nobody/owner/own/signed-in/anyone), the seven
presets, rules R1 to R4, the translation of the older fields, the legacy matrix
for resources saved before presets (always used on Simple Hack), and the caller
decision (`storageAllow`, which writes the refusal and returns the own filter).
The matrix lives in `site_storage_resources.acc_*` (NULL = legacy) and per table
in `site_storage_tables` (migration `zd-storage-access-presets.sql`).
`h/site_storage_table.go` serves every page request on SQLite tables (list, get,
add, edit, delete) with server-built statements under an authorizer that allows
only the one expected write on the target table, so triggers and foreign-key
actions that write elsewhere refuse the change; raw `/query` and `/execute` stay
for the owner and legacy full-mode databases. KV and file `PUT` decide add or
edit inside the write lock. `storageOwnerOnSite` gives the site's owner, signed
in through visitor sign-in on one of the site's own hosts (never the person
host), owner rights for storage data only; settings routes still require
`X-API-Key`. `GET /v1/sites/{s}/me` reports `site_owner`. `ownerFrameGuard`
(called from `serveSiteFile`) adds `frame-ancestors 'self'` to pages the owner
loads while signed in on that host.

Storage security (shipped 2026-10-06): storageWriteLock waits up to the
configured 2 s before 503 with Retry-After. Owner operations retain 5 s and
visitor writes 2 s; body reads and pure reads do not lock. Visitor write buckets
are keyed by site and IP/visitor; reads do not consume them. SQLite execution
uses a CPU-sized process semaphore with a configurable 250 ms acquisition wait;
writers take the site lock before the slot. Own-file pagination advances past
stale metadata. New keys/paths normalize to NFC; legacy exact names remain
readable/deletable. File buckets cap committed object counts, and populated
non-own SQLite databases cannot convert to own reads. Existing clientIP proxy
trust remains documented; deployments must restrict direct app access.

Hosted Hack role films (2026-10-10): `/watch` is `static/hack-watch.html`, served through `hack_home.go` and `hack_watch.go` with the shared chrome, nonce CSP, no-store and instance-host text rewrite. It copies the landing ink engine once without modifying `hack-story.html`; a player factory drives three independent FILM constants, SVG scenes and narration files. Motion/hover detection does not decide a theme; the watch page and chrome stay light through CSS. `HackHandler.RegisterDemo` is called only in the hosted startup block, using `HACK_DEMO_EVENT_SLUG` (default demo). Each public lookup/redirect reads the current event and invitation codes from the database. Weekly event recreation is external.

Add photos (2026-10-23): `h/photos_ui.go` serves `/photos` with the shared
chrome, sign-in card, and nonce CSP. `st/photos.js` processes one image at a time
on the device; HEIC/TIFF always use a short-lived worker loading
self-hosted libheif Wasm or UTIF/pako from `st/photo-codecs/v3/`. Worker termination
releases the decoder heap, with a 20-second timeout, 80 MB input cap and 50 MP
header cap before pixel decoding. Native header checks run before browser
decoding, with the same deadline and a worker where supported. UTIF traversal
has cycle/count guards and streaming Deflate output is bounded per strip/tile.
Only the worker CSP permits Wasm compilation.
`h/photos.go` validates multipart bytes and JPEG/WebP container structure,
limits ICC to 16 KB without inspecting its contents, obtains dimensions with DecodeConfig (no pixel validation),
and merges the live files while holding the existing account/site deploy locks. It uses
`commitSiteUpdateRespond` for the normal quota, version, promotion, and retention
path, and builds photo URLs from that path's `siteResponseFor`.
`X-Photo-Reconcile: 1` instead matches submitted photo bytes and allocated names
against distinct live files under those locks. It returns the current version
without a write, or 409 if the batch cannot be confirmed. `/photos` freezes an
uncertain batch and only checks it on subsequent clicks. Auth is the
existing dashboard X-API-Key (deploy keys are account-wide), not a session cookie;
the account apex Origin check
also applies. A process-wide PHOTO_MAX_CONCURRENT slot (default 3) is acquired
before any body read, including token-proxied saves; overload returns 429 and
Retry-After without queueing. Both entry points set a 60-second body-read deadline
(`PHOTO_BODY_TIMEOUT_SECONDS`) on the real response writer; the token proxy’s capture
writer cannot propagate it. Collision indexing is linear in existing paths.
Hack is gated with 404; Enterprise needs a separate port.

Photo links (2026-10-23): `mcp/uploads.go` mints the default fragment link in
its hashed in-memory map; `mcp/photos.go` checks the creating sign-in, serializes
saves, caps successful saves at 10 in 60 minutes (API-key links are capped at the
key’s known expiry), and forwards only to the bound
site's photo handler. OAuth photo mints recheck the live grant's user, client and MCP
resource plus the user's existence, suspension and admin status, independently of
access-token expiry. Archive mints still recheck the access token. `h/connector.go` applies apex Host/Origin and IP limits to
`GET/POST /v1/photo-links`; the page sends X-Photo-Token without account credentials.
The page removes the fragment immediately, keeps the token only in memory, and
resets in place for Add more; reload asks for a new link. The binding is to the
site name within the same account, including a replacement site using that name.
GET reveals only the bound site. Archive mode and Hack keep the one-use upload path.

Passcode screen (2026-10-30): `mcp/passcode_screen.go` owns the app-only tool and
bounded in-memory session map. Each begin validates `/lock` with the caller's credential,
exchanges ephemeral P-256 keys, and derives AES-256-GCM through HKDF-SHA256 with info
`simple-host passcode screen v1`. Show/set/generate consume the caller-and-site-bound session
before any action; AAD binds action, site, and session. `apps/passcode.{html,js}` uses
WebCrypto, clears displayed values on Hide and teardown, and sends status-only context
updates. `MCP_APP_PASSCODE` defaults off and is personal Host only. Existing passcode tools
return status only; a screen or owner dashboard `?site=<site>&open=passcode` link handles entry and display.
The REST API has no new route. The app-only tool's analytics arguments and results are removed.

MCP Apps (2026-10-23, SIM-195): `mcp/apps.go` owns the embedded screen registry,
per-request flag gates, resources, and tool metadata. `forCaller` copies enabled
listings before adding metadata, preserving the cached lists for other callers.
The connector passes the handle from its existing authenticated user lookup;
no new account lookup or credential is exposed. The UI spec defines a client
extension capability, not a server extension entry; the server advertises
resources only when a screen is enabled. Fixed inventories stay off. Hack offers only My sites and dashboard when the
connection has site tools and the corresponding flags are enabled.
`mcp/apps/bridge.js` implements the standard source-checked JSON-RPC handshake,
tool calls, context theme, link/display requests, and ResizeObserver notifications.
After initialization, outbound messages target the response event's parent origin;
opaque sandbox proxies retain the required wildcard target. Source checks remain.
The frame's stylesheet is separate from site-page chrome because host context
owns its theme. `photo-picker.js` saves through `get_site` and `update_site`,
with keep_other_files and batch sizing against Config.MaxBodyBytes. Each batch
is one normal version, subject to existing quota and retention checks. File-name
collisions are resolved against the current listing; concurrent external edits
between that read and update retain the existing update_site last-write behavior.
Two concurrent pickers can reserve the same name and overwrite each other; this
is an accepted limit, not atomic allocation. After an uncertain save, retry reads
get_site: a reserved path with the expected byte size counts as saved. Otherwise
the retry retains that path. Size matching is not a content hash. Unsaved rows
have Remove controls, failed decodes release capacity, and completion counts only saves.
After a partial save, removing all unsaved rows clears the error and enables
Finish without another tool call.
`st/photo-process.js` is the single decode/shrink/canvas implementation for both
surfaces. Native formats have header checks before decoding. The frame uses a
blob bootstrap to import the same decoder and probes worker startup, imports,
and Wasm compilation before accepting HEIC/TIFF. `/photo-codecs/v3/wasm.js` wraps the existing embedded Wasm bytes
as script data, avoiding connect-src fetches. Worker creation and Wasm compilation
remain subject to the host CSP; probe refusal directs HEIC/TIFF selections to
the link already in chat before decode.
Photo previews and Image fallback decoding use data URLs, so img-src need not
allow blob URLs. The blob-worker requirement is separate.
When the picker is enabled, photos_url in both text and structured output is
https://<api host>/photos?site=<site>&account=<handle>, with no fragment and no
allocated grant. The handle comes from the connector caller. The page uses its
normal sign-in card and selects that site only when the browser account matches;
a different account gets a short notice and no site selection. Provider sign-in
returns preserve both query parameters. The hint does not change authorization. Modern requests
explicitly advertising clientCapabilities without the io.modelcontextprotocol/ui
extension retain tokened links. 2026-10-27 (SIM-226): an initialize-era client sends its
io.modelcontextprotocol/ui capability only once, in initialize, so `mcp/clientui.go` remembers
the answer keyed by a SHA-256 of the OAuth grant ID, else the API key (in-memory, 30-day TTL,
capped at 20,000 entries, oldest-first eviction) and the dispatcher consults it on a later
`create_upload_link` call, which carries no capabilities of its own. A remembered client with the
capability gets the picker whenever it is enabled for their account; one without it, or with no
memory yet this process, gets the sign-in link; owner sign-in is required either way. A call
reached through `run_tool` always forces the sign-in link, never the picker, since a host draws a
screen only for the tool it listed, so the dispatcher sets `textOnlyClient` before the tool runs.
Flags-off results retain the original contract.
The expires_at field is retained for compatibility; the token-free sign-in URL
itself does not expire. Archive results stay unchanged. The January 26 Apps
specification attaches UI to tool definitions, with no per-call suppression:
https://raw.githubusercontent.com/modelcontextprotocol/ext-apps/main/specification/2026-01-26/apps.mdx.
The picker hides the whole body on archive tool-input, before tool-result.
Known limit: the host can still forward the archive upload URL to that hidden frame;
hiding the UI does not remove it from the frame's message boundary.


My sites (SIM-195): `mcp/apps/my-sites.{html,js}` extends that registry and bridge.
`MCP_APP_MY_SITES` gates list_sites metadata and its optional structured display
field; `mySitesOutputSchema` is applied to a copied enabled inventory. The plain
text is serialized before display fields are added. `MCP_APP_DASHBOARD` is passed
in the resource HTML only when both per-caller flags permit it. The fourth
resource, `ui://simple-host/dashboard`, shares the My sites HTML and JavaScript
and is listed/readable only when both flags permit it. It drills into
My sites rather than attaching to an unlisted tool. `output.Meta` carries daily
analytics to the frame without changing site_analytics text or schemas; the bridge's
callToolResult preserves it. Storage calls discover inbox/records SQLite tables
and read their latest three rows, merging at most three for display. Policy lookup
uses lowercase table names and own properties. Discovery excludes the literal
sqlite_ prefix with NOT GLOB 'sqlite_*'. Each table orders by created_at DESC, id DESC when both exist,
created_at DESC alone if id is missing, id DESC if only id exists, or rowid DESC
as a last resort after inspecting columns; failed resources/tables are named without
losing other entries. My sites ends initial loading on errors/cancellation only
before its first successful list; later notifications do not clear that list. It labels
filtered or paged input as Showing N sites; its search stays within those results. Visitor text
uses textContent. No new tool or REST route was added.
My sites always renders initial tiles, with no live thumbnails, frame observer,
or preview URL marker. My sites and dashboard declare empty frameDomains and
connectDomains. Flags-off, account gates, unchanged text, hostile text, and
tile-only rendering have regression tests.

Hack My sites (2026-10-24): `apps/hack-sites.js` adapts the shared view using
`who_am_i`, `hack_get_my_teams`, `hack_list_events`, and organiser-only
`hack_get_event_website`. It calls team storage and `site_analytics` directly,
since Hack has no discovery wrapper. Event storage uses `hack_event_storage_*`
with `event`; website state uses `slug`. With My sites enabled, an unselected
personal `list_sites` returns an empty team list without replaying a site request,
so organisers can open the frame before selecting a team. Other site calls keep
the selected-team gate. No backend identity or route changes.
The selected team is checked again before opening its dashboard; REST still
checks scope on every read. Other teams open their returned URL and require
selection in chat for dashboard reads. Custom event dashboards omit analytics
because no organiser analytics tool is available. Filtered input stays scoped.
The standalone Compose package forwards the two screen flags, default off.

MCP Apps site preview (2026-10-23, SIM-195): `mcp/apps_preview.go` adds only
flag-enabled get_site structured data and a copied output schema. It captures
the original text before adding the access decision and REST platform URL;
the common summary stays unchanged. `apps/site-preview.js` reads
only results with an object structuredContent.site_preview through the existing
bridge. The nested iframe is inert, unfocusable, and scaled inline, with an empty
sandbox, credentialless, no-referrer, and no pointer events. Confirmed fullscreen
replaces it with a fresh allow-scripts-only sandbox (no same-origin, forms, popups,
or top navigation); returning recreates the passive thumbnail. Focus moves to
Back to card on entry and Full screen on return. Open is the full interaction path.
The iframe is absent unless the
site is published, open to anyone, and has an HTTPS URL under this instance's
served site bases or at its separate content origin.
The resource derives frameDomains from `SiteHandler.ServedBases()` and the
separate content origin, with no
connectDomains or external assets, and clipboardWrite permission. The shared
bridge exposes its merged host context; display responses and notifications
both update the card. Clipboard refusal selects a read-only URL input. Custom
canonical redirects outside the allowed frame origins remain blocked by CSP.
`apps/frames.js` is included only in the site preview resource and validates
the same configured HTTPS frame origins without userinfo. Wildcard hosts reject
explicit ports; exact content origins match their configured port. It adds `_sh_preview=1`,
which analytics viewHit skips for visitor counting; traffic still counts.
Anyone can add this marker to hide a visit from counts, even with Apps flags off,
an accepted, low-impact limit. Later unmarked child requests can still count as visits.
Live child documents can load their own resources; the outer CSP does not
restrict those requests, and credentialless depends on browser support.
The preview card is off in production because Simple Host sites refuse framing (`X-Frame-Options: SAMEORIGIN`) and Claude does not allow nested frames. Turning it on needs a decision on letting sites be framed by chat hosts or a server-made preview image.
ChatGPT uses the same MCP Apps standard, verified 2026-10-23 against
https://developers.openai.com/apps-sdk/build/chatgpt-ui; no openai/* metadata is added.

Hack notifications use personal subscriber IDs, the selected OAuth team or organiser event
context, and the hosted engine. `db/hack_notifications.go` checks access before creation
and delivery; creation locks membership before the site to serialize with removal.
`zj-hack-notifications.sql` deletes affected subscriptions when membership changes.
Team deliveries ignore the submission deadline; organiser deliveries stop at event end.

Host tool listing (2026-10-28): `mcp/server.go` selects the complete permitted
operation inventory for ChatGPT, OpenAI, and Codex app names, including the bare
pre-outputSchema list. Other apps keep the compact inventory. `instructions.go`
applies per-caller text variants without mutating cached definitions; Apps metadata
still applies afterward. Cached discovery and execution wrappers remain callable.
My sites tries direct operations first and uses the wrapper only for unknown-tool
errors. Tool names, authorization, and output contracts are unchanged.

Visitor connector tools (2026-10-29): `visitor_tools.go` adapts MCP calls to the browser storage handlers with `IssueInternalVisitorKey`, a server-only pass bound to person, site, and host. `GetUserByAPIKey` rejects that pass. `sanitizeReturnTo` resolves the address, `checkVisitorPasscode` shares browser unlock counters, and the storage matrix supplies row filters. `visitor_ai_daily` persists the daily attempt count; `data_history.actor_app` holds the connected app name. No new HTTP routes.
