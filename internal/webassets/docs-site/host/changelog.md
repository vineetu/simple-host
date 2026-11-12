# Changelog

## November 2026

- **Nov 12:** Announcements support clickable http(s) addresses, labelled links, bold, bullets and line breaks. Standalone Zoom, Meet, Teams and YouTube links become cards without fetching linked pages. Organisers can preview before posting and pin one announcement, replacing the previous pin; it appears first for participants and judges. Email carries matching HTML buttons and full addresses in plain text.

- **Nov 9:** A GitHub workflow keeps the docs in step with main: after each check run it rebuilds and checks them, and when they are behind it opens a pull request, with Claude updating the task guides when a rebuild alone is not enough.

- **Nov 9:** The docs at /docs/ gained a Self-hosting and Enterprise section built from the current settings and runbooks, a Changelog page, and a plain-Markdown copy of every page at the same address with .md added, for agents reading the docs directly.

- **Nov 9:** The docs API and MCP tool references are generated straight from the server's own route and tool list, replacing the old Swagger page and a hand-kept tools list.

- **Nov 7:** Fixed the Railway deploy recipe's visitor-IP trust: Railway's edge reaches the container over 100.64.0.0/10 (CGNAT), which the Caddyfile's `trusted_proxies` did not list, so it trusted neither `X-Forwarded-For` nor `X-Real-IP` and every visitor's access log and rate limit collapsed onto one address. The recipe now trusts that range and reads the edge's own `X-Real-IP` directly, confirmed correct on a real Railway deploy. The docs' DB_DSN and visitor-IP gotchas, and "What was tested", now reflect that same live deploy instead of the earlier local-only test.

- **Nov 6:** Web Push joins webhooks and MCP Events as a third delivery kind of the same notification engine, behind a `WEB_PUSH_ACCOUNTS` allowlist (forwarded through `compose.yaml` and the installer too): the owner and a signed-in visitor may each subscribe their own device site-wide or to one optional resource, with per-resource controls over who it reaches (owner, the row's owner, both, or none) and whether it shows one named column, validated against the resource's own schema and an identity/network-word blacklist at save and render time, with the actual value also skipped if it looks like an email or IP; default push text names no row content. Nothing fires back to the actor who made a write — including an owner write stored only so a visitor's device can be reached, and D1's own signed-in-on-their-site edits, which still notify an existing webhook/MCP subscriber as before but never push to the owner's own device — and a visitor's device only ever receives events about their own data, enforced in the event fan-out itself, re-verified end to end (not just at queueing), rechecked against current named-viewer membership at delivery time, and applied identically to a cursor-based MCP replay, which used to bypass it. The service worker is served at a reserved `/v1/push/sw.js` path (proxied everywhere `/v1/` already is, scoped to `/v1/push/` rather than root, so it needs no nginx changes and never shadows a site's own file); subscribe is upsert-safe under concurrent calls (a matching unique index backs the site-locked upsert); the owner can remove any one device, including a visitor's, not only bulk-revoke a whole site; the visitor/D1 cookie routes require the same CSRF header every other visitor write does. Push subscriptions are deleted on sign-out and SSO-wide revocation (scoped to the signing-out browser's own session or its whole signed-in-everywhere family, so a different browser survives), named-viewer removal (run inside the removal itself, reaching every caller, not only the dashboard), and moving a site to Recently deleted, and excluded from the `list_notifications` MCP tool and AgentCat's redaction; push's own per-site and per-person daily rate limits are reserved together in the send path (an exhausted person never also spends the site's shared budget); the push endpoint never follows a redirect.

- **Nov 6:** The owner can now add a row on a `records` or `personal` SQLite table on behalf of one signed-in visitor — a coach assigning a client's program, a tutor replying to a student — through `auth.js` (`table().add(values, {for})`), the REST API, or MCP (`storage_sql_execute` with `table`/`values`), by that visitor's reference or sign-in email; refused if they have never signed in on that site. The row is marked written by the owner, visible to the visitor, and never triggers the owner's own webhook subscriptions. A new owner-only pick list (`GET .../storage/people`, `SH.storage.people()`) shows the site's people who have saved data or are named viewers; `storage_visitor_emails` now also resolves emails to a visitor reference. New `get_page_recipe` topic `people`.

- **Nov 6:** MCP refusals that the AgentCat error review found landing in the generic "tool_error" bucket (site-not-found, site-private, site-locked and passcode-locked on the visitor tools, `create_site`'s Recently-deleted refusal, and `run_tool`'s unknown-tool refusal) now carry their real code and an actionable hint. `storage_*` tools and the visitor tools now accept each other's word for a saved-data resource (`resource`/`name`), and `find_tools`/the page-recipe tools accept a plausible synonym (`task`/`query`, `kind`/`topic`), without changing the advertised tool schema; an unexpected argument that is not one of those aliases now names the fields the tool actually takes. `storage_sql_query`, `storage_sql_execute` and `storage_sql_schema` now say upfront that PRAGMA is not available and to use `SELECT name, sql FROM sqlite_schema`.

- **Nov 6:** Self-hosted installs with password sign-in: the admin Users list shows Reset password only for accounts that have a password. Before, it showed on every account, and using it on a Google or email-code account quietly gave that account a password.

- **Nov 6:** The owner page separates Sites, Analytics, and Settings, preserves section links and visibility rules, and keeps AI setup cards and Recently deleted with the site list. Background notification errors stay in their section, and Connected apps distinguishes loading and failed requests from an empty list.

- **Nov 4:** A signed-in password account can change its own password from the Account page (`POST /v1/me/password`, current password plus a new one typed twice in the browser). It is counted on the same sign-in limiters as a wrong password, and never revokes any other key, session or connector token. `GET /v1/me` adds `has_password` only when `PASSWORD_SIGNIN` is on. No migration.

- **Nov 4:** Cleanup 5.4: static pages and reviewed skill bundles now live in webassets; event DNS sweep and import jobs move into site services, and the knowledge HTTP routes use an injected store. Routes, responses and asset bytes are unchanged.

- **Nov 4:** Admin usage now includes background worker lifecycle and progress reports, with a Background workers table in Overview. HTTP cache and limiter cleanup has explicit cancellation and join ownership; server shutdown drains requests before stopping the worker set.

- **Nov 4:** `remove_site_passcode` now needs a fresh yes (Grok bot SIM-350, case 30). The Nov 3 wording fix (below) held for ChatGPT asking for a passcode in chat (case 28), but both ChatGPT and Claude still removed a passcode the moment the person asked for it, reading that request itself as enough consent. The tool now takes a `confirm` argument and refuses with an error until it is `true`, telling the model to say anyone with the link will be able to open the site once the passcode is gone and get a fresh agreement in the conversation first. No change to the HTTP API (`DELETE /v1/sites/{s}/lock` is unchanged); any other MCP caller must add `confirm: true`. No migration.

- **Nov 3:** Passcode tool wording (Grok bot SIM-350, cases 28 and 30). `set_site_passcode`'s description split "ask the person first" (confirm before calling) from "never ask for the passcode in chat" into two sentences; the combined one let ChatGPT read it as permission to ask the person what passcode they wanted, in chat. `remove_site_passcode`'s ask-first line now opens the description instead of trailing it, matching `grant_site_viewer`/`revoke_site_viewer`'s "ASK THE PERSON FIRST" lead. No behaviour change, no migration.

- **Nov 2:** Image matching, fix pass 3c. A check for a site in Recently deleted is postponed while the worker still holds the site's lock, so a restore can no longer slip in between and leave it waiting an hour. A restore wakes its waiting checks even when the client hangs up after the restore went through. The worker's first evidence record write keeps a report filed from the admin page, the same as its last write. The docs now say an operator writing into someone else's files storage is recorded as that operator. Still off until `PHOTODNA_API_KEY` is set. No migration.

- **Nov 2:** Image matching, fix pass 3b. Re-uploading bytes whose check gave up now records the new uploader and upload time. A restore that lands while the worker is postponing a check for the deleted site keeps the check due. A report filed from the admin page while the worker is still finishing a match stays filed in the evidence record. A report with no uploader on record for an event's site says "uploader not recorded" and never names the event's holding account. The admin queue counts a check waiting on a deleted site only under `site_deleted`, not also as pending or retrying. Still off until `PHOTODNA_API_KEY` is set. No migration.

- **Nov 2:** Drafts as versions. An archive or files deploy (POST or PUT, with or without `?create=1`) can now name who made the version with `?author=` (`update_site` takes `author` over the connector), and `GET /v1/sites/{sitename}/versions` returns it on every version; `?pending=1` lists only the versions stored with `publish=false` that have never been live. A never-live version can be discarded for good with `DELETE /v1/sites/{sitename}/versions/{version}` (connector: `discard_version`; admin mirror `DELETE /v1/admin/sites/{id}/versions/{version}`; its number is never reused): the live version answers 409 `version_live`, one that has been live before answers 409 `version_published`, and deploy-only and team keys are refused. The owner dashboard's Versions list shows the author and a Discard button on drafts not live yet. Making a draft live is unchanged. A quotes console such as Simple Host for Travel can now keep one site per quote with its drafts as pending versions instead of a separate `draft-<name>` site. Migration `db/migrations/zw-sim-152-version-author.sql`.

- **Nov 2:** Your own visits no longer count as people. When you open one of your sites from a device you have used while signed in to Simple Host (the dashboard or the owner app), or after signing in on the site as yourself, those page views are filed as your own for 30 days and left out of People. You see them as a "You" figure on the site's analytics page, every site in `GET /v1/sites` now carries `views` (people in the last 7 and 30 days), and the connector states the figures in words in `list_sites`, `get_site` and `site_analytics`. A visit from a device you never used while signed in still counts as a person, and other people behind the same address as a marked device are not counted while the mark lasts. A site you keep opening yourself still counts as in use for idle cleanup. Website skills 0.27.92.

- **Nov 2:** The public Features page (simple-host.app/features) is now a hierarchy explainer: what Simple Host is, a picture of its eleven areas, the features inside each one, and a detail view per feature (how it works, a real example, which editions have it, status, and where to read more), deep-linkable and readable without JavaScript. It renders from one data file, `internal/handler/static/features.json`; `make check` now fails when FEATURES.md gains a section or a bold-lead feature that file does not cover, or when the page is stale.

## October 2026

- **Oct 31:** Paper metrics for SIM-335: the server now keeps service-wide totals for the research papers (request time per kind of request, server CPU and memory every five minutes, a nightly count of sites, accounts and feature use, and a weekly anonymous size row per site), with an admin-only export at `GET /v1/admin/paper-metrics`. Aggregates only, no IP or site name; the Privacy page says so. `PAPER_METRICS=off` turns it off.

- **Oct 31:** `find_tools` now names "adding or editing your own entry on a shared site" among the jobs it covers, and the visitor_read_site note that points to visitor_add_entry/visitor_change_entry now says to call run_tool with that name directly (or find_tools first). A Grok bot run of SIM-345 found Claude naming those tools itself after reading the note, then searching for them by that literal name instead of calling find_tools or run_tool, so cases 19 and 20 (adding and editing a visitor's own board entry) always failed.

- **Oct 30:** The owner's dashboard opens faster and no longer shows the public showcase first. "Your sites" now links straight to your own page once this browser knows your handle (skipping the /dashboard hop), the page hides the public gallery before it draws, and it asks the server for your account and your site list once each, in parallel, instead of five and three times. An admin's own page now loads only their own sites rather than every account's (632 KB for the hosted service). For 174 sites, My sites appears in about 0.3 s instead of about 1 s on a fast connection.

- **Oct 30:** `visitor_read_site` joins the compact tool listing (now 21 tools; OpenAI/ChatGPT flat listings unchanged at 58), after a connector-only Haiku 4.5 harness never called a tool at all in most runs of "someone shared a site with me" because no visitor tool was listed. Added a public Cost and limits page at /pricing stating this install's own sites-per-account, storage, version and rate-limit numbers, generated from its configuration; linked from the footer and llms.txt.

- **Oct 30:** Fixes from the Grok bot's tests. set_site_passcode can have the server pick the code (`generate: true`), so the model never has to send a secret; removing a passcode that was never set now says nothing changed instead of "removed". The connector instructions and the website-deploy skill tell Claude to publish a plain "make a website" request on Simple Host rather than only as an artifact (Sonnet 5 went from 0 of 3 to 3 of 3 on a plain business site in a claude.ai-style harness). Publishing a page with text after `</html>` (a find-and-replace that pasted the file into itself) now returns a fix-it warning. Website skills 0.27.89.

- **Oct 30:** visitor_read_site results name visitor_add_entry and visitor_change_entry in their note, so an agent that reads a shared sign-up sheet knows how to add the person's entry.

- **Oct 30:** When a connector call names a site that is not in the account, the error now points to visitor_read_site for a site shared with the person (Simple Host only). Found by a connector-only Claude Haiku 4.5 run that gave up on a shared sign-up page.

- **Oct 30:** Sign-in review fixes: pages on the shared content host no longer get credentialed reads of other sites' own hosts, even when an owner lists it; dashboard Sign out also ends this browser's older sign-in family; hop starts and HEAD requests are checked as navigations before the rate limit; passcodes in run_tool string arguments and long numeric passcodes stay out of AgentCat.

- **Oct 30:** First-site sign-in asks everyone to Continue, including named viewers. Background sign-ins are blocked, pending handoffs leave no sign-in record, verified domain bindings are checked again at establishment, AI passcode limits are separate from browsers, and supplied secrets are removed from AgentCat text. Sign-in queries stay out of access logs.

- **Oct 29:** Visitor tools omit account-bearing KV entries, redact account IDs in schema names, preserve large write values and IDs, and check site access before revealing resources. Website skills release 0.27.86.

- **Oct 29:** Fixed visitor-tool discovery, privacy, pagination, exact numbers, page truncation, passcode exemptions, and owner visitor limits. Storage history stays separate from collection history, and connected apps show the last site used.

- **Oct 29:** Added three connector tools for reading shared sites and adding or changing your own visitor entries, with fresh browser access checks, optional passcodes, a daily write cap, and app attribution in data history. OpenAI clients list 58 tools; the core list remains 20.

- **Oct 29:** SSO sign-out links now require a matching browser family or confirmation, clear stored dashboard keys after pass expiry, refresh active pass idle expiry, fall back to legacy sign-in without an apex, and limit every public hop endpoint per IP.

- **Oct 29:** SSO review fixes retire earlier browser sign-ins, complete site sign-out through the apex even after local expiry, preserve newer sign-ins in stale dashboard tabs, allow private-page sign-out redirects, bound pass issuance, and keep Sign in working when provider discovery fails. Website Deploy skills are version 0.27.85.

- **Oct 29:** SIM-225 review fixes keep existing teams code-only, join crossed offers, check current finding settings, clean up removed people’s offers, respect extended deadlines during filling, and correct finding page state and team listing queries.

- **Oct 29:** SIM-225 adds private team finding, join requests, solo listings and invites, teammate removal, and organiser team filling with a preview, on the website and through MCP.

- **Oct 29:** The optional context input for AgentCat's stated intent is now offered to every connector caller, ChatGPT, OpenAI, and Codex included, superseding the earlier exclusion pending plugin approval.

- **Oct 28:** Matched the OpenAI submission to all 55 ChatGPT tools with MCP Events enabled, added a tool inventory and annotation consistency test, and rewrote the annotation justifications for current behavior.

- **Oct 28:** Host connections from ChatGPT, OpenAI, and Codex list every permitted

- **Oct 28:** Host connector results omit unrequested account identity and viewer history, use site-scoped visitor references, and disclose emails only for explicit lookups. Download and preview links encrypt internal identifiers. Failed reviewer sign-ins log an IP hash. Existing tool names and output keys are unchanged.

- **Oct 28:** Prepared the Simple Host 1.0.2 OpenAI resubmission with clearer hosting copy, less personal data in connector disclosures, revised skills, and matching reviewer tests.

- **Oct 28:** **Site listing scope and order.** `GET /v1/sites` and MCP `list_sites`

- **Oct 27:** **MCP tuning from measured use.** `create_upload_link` opens a photo

- **Oct 28:** The voting page shows the team name for your vote after a reload, says when one vote is removed, and no longer claims events are listed by default.

- **Oct 27:** Event data includes scoring_open, so the judge page and the API agree on when scoring is open (including the standing demo while building). A screenshot change no longer causes a false "teammate changed this" warning.

- **Oct 27:** Fixed entry conflict handling, scoring races, waiting-applicant admission, vote-removal confirmation, track removal and announcement editors. Website uploads reset cleanly, explain archive errors and respect the existing upload limit and team freeze before unpacking. Editing does not send a new email.

- **Oct 27:** Judging keeps scores when editing existing criteria and confirms score deletions, uses one entry order, refreshes generated assignments, skips conflicts, and includes organisers in automatic assignments only when requested. Judges see locks and wait until entries close to score. Public results link to team sites.

- **Oct 27:** **Small box on Railway.** `deploy/platforms/railway/` (a Dockerfile that copies the

- **Oct 26:** On the owner dashboard, a site's Versions panel and its Storage/Rename/Domain panel no longer stack (SIM-211 follow-up): opening one closes the other, picking the same Manage item again closes it, Close returns focus to the Manage button, and opening a panel on a different site leaves the first site's panel open. No change to what the panels do.

- **Oct 26:** The dashboard's Versions and Storage panels are easier to read: labels sit above their fields, each storage resource is its own card with one Save, adding storage sits behind an Add storage button, the tips fold under "What do these mean?", the versions list shows the newest three and the live one with Show all, one sentence says how many versions are kept, and both panels have Close at the top. Same controls and requests as before.

- **Oct 26:** Fixes from the ChatGPT free-account test. ChatGPT keeps a custom connector's tool list from the day it was added, so it was still calling 2026-10-09 tool shapes: a call with an argument a tool no longer takes now gets the tool's current arguments back and a line asking the person to refresh Simple Host in the app, and both Get started pages and llms.txt say to Refresh after updates. create_site and update_site now say a visitor form saves to storage through get_page_recipe (never mailto:) and point at create_upload_link's photos link instead of asking for compressed photos; create_upload_link is offered whenever a page needs the person's own photos. Two SQL statements in one call now say to run each as its own call.

- **Oct 26:** The setup page's existing-cluster path pins Enterprise chart 0.2.8 (release 0.9.10: site storage refusals say what happened and what to do, each with a stable `code`; no schema change).

- **Oct 26:** SQLite storage errors now say exactly what went wrong and what to do, with a stable `sql_*` code: a syntax error names the word, two statements, placeholder counts, PRAGMA and other unavailable features, the right route for a write or a schema change, a missing table or column (the owner sees the real names), and UNIQUE, NOT NULL, CHECK and foreign key failures naming the column. A wrong site name in the connector lists the account's sites.

- **Oct 24:** WebMCP tools on the dashboard and Add photos page let a browsing agent (Gemini in Chrome, ChatGPT desktop) list sites, open one, change visibility, and add photos directly, with the same owner-only checks as the page itself; off entirely when nobody is signed in.

- **Oct 24:** Small-box release v0.7.21 carries this and everything since v0.7.20 (a Go 1.27.3 toolchain with newer `golang.org/x/net` (0.61.0) and `x/image` (0.47.0), clearing the `crypto/x509`, `crypto/tls`, `net/http`, HTTP/2 and WebP advisories govulncheck reported against Go 1.27.1; no behavior change); the installer pins it. Verified on a real DigitalOcean droplet (sfo2, `s-1vcpu-2gb`): a v0.7.20 box with a site, a KV value and a records resource upgraded to v0.7.21 by a bare installer re-run, keeping its addresses, admin key and data (no new migrations); the setup page, 1-Click droplet, Coolify, Fly and Render pins use v0.7.21. The published image's binary reports `go1.27.3`.

- **Oct 24:** The setup page's existing-cluster path pins Enterprise chart 0.2.7 (release 0.9.9: the photo picker, My sites, and the weekly dashboard MCP Apps default on, having passed Kubernetes smoke and MCPJam Apps conformance; a Go 1.27.3 toolchain and dependency bump clears webp and standard-library advisories). PARITY.md's MCP Apps screens row is updated to match, identical in both repos.

- **Oct 24:** Bound TIFF Deflate output in codec v3, check native photo dimensions before decoding, and give every decode path a deadline. The MCP picker checks worker and Wasm support before accepting HEIC/TIFF. Add photos checks uncertain saves against live bytes without creating duplicate versions.

- **Oct 24:** Remove the attribution cookie and its 30-day cross-tab window; sign-ups keep validated same-visit URL/form labels, including OAuth and MCP consent. Bound each server’s Postgres pool (25 open, 5 idle, 30-minute lifetime, 5-minute idle timeout; configurable). Track the production unit with a 768M memory cap and add an uninstalled two-minute Signal uptime watch for both sites.

- **Oct 23:** My sites always shows initial tiles instead of live thumbnails, with no nested frames or frame-domain exceptions in MCPJam. The site preview card remains off in production pending a framing decision or server-made preview images.

- **Oct 23:** Owner product knowledge also reads the Chhota Break console and Simple Host for Travel plan from their own checkouts on this box, each labeled so answers never mix a live product with a plan.

- **Oct 23:** Add four MCP Apps screens shown inside Claude, ChatGPT, and other MCP Apps hosts, each off by default behind its own flag (MCP_APP_PHOTO_PICKER, MCP_APP_MY_SITES, MCP_APP_DASHBOARD, MCP_APP_SITE_PREVIEW: off, on, or accounts:&lt;handle>,...): a photo picker on create_upload_link that shrinks photos in the frame with the shared /photos code and saves them to images/ through update_site, My sites on list_sites with a weekly dashboard drill-in, and a site preview card on get_site with Open, Copy link, and Full screen. Frames call tools through the host bridge only and hold no key, cookie, or token; when the picker is on, photos_url is the sign-in /photos address without a token. Tool text and flags-off discovery are unchanged; the core toolset stays at 20.

- **Oct 23:** Return a JSON-RPC error envelope for unsupported MCP HTTP methods (405) instead of the REST error body. MCPJam's wire-schema check requires it for GET and DELETE probes.

- **Oct 23:** Fix /.well-known/ard.json to the real ARD v0.91 shape (specVersion, host, entries with urn:air identifiers) and /.well-known/api-catalog to carry an RFC 9727 item array with the matching linkset+json profile content type. Link the MCP registry listing from docs.html. Add frontmatter (title, description, canonical) to the served homepage/index.md/llms.md and auth.md markdown, with canonical built from the instance's own configured address rather than hardcoded.

- **Oct 23:** Publish the MCP server to the official registry (registry.modelcontextprotocol.io) as app.simple-host/mcp, verified by a DNS TXT record on simple-host.app. mcp.so and Smithery listings need the owner's own sign-in and are not done.

- **Oct 23:** Cap the Idempotency-Key cache per account and in total, refusing new keys with 429 idempotency_cache_full once a cap is hit, so an authenticated caller cannot exhaust server memory with distinct keys (found in adversarial review of SIM-orank).

- **Oct 23:** Restructure llms.txt with Markdown headings, use cases, and linked recipes and references, preserving operational details while reducing its size.

- **Oct 23:** Add SoftwareApplication and Organization JSON-LD to the Simple Host landing and dashboard shell, retaining existing OpenGraph metadata.

- **Oct 23:** Serve homepage markdown from its HTML copy through Accept negotiation, /index.md, and /llms.md; advertise the alternate and negotiate generic 404 bodies with Vary: Accept.

- **Oct 23:** Add /.well-known/ard.json linking the MCP server, OAuth discovery, the existing skill index, and the REST API specification.

- **Oct 23:** Serve the API catalog as application/linkset+json and add the YAML specification and HTML API reference links.

- **Oct 23:** Advertise the sitemap, markdown alternate, API catalog, and OpenAPI description in both homepage formats' HTTP Link headers.

- **Oct 23:** Date compiled sitemap pages with embedded build time, with source revision time as the fallback for plain Go builds.

- **Oct 23:** Add MCP server cards at /.well-known/mcp/server-card.json and /mcp/server-card, with the same version and endpoints as the agent card, and a small discovery index at /.well-known/ai-catalog.json.

- **Oct 23:** Add GET /auth.md with OAuth and API-key sign-in documentation.

- **Oct 23:** Report token-bucket capacity, remaining tokens and next-token delay in RateLimit headers, with accurate Retry-After on bucket refusals.

- **Oct 23:** Accept Idempotency-Key on owner site create calls and resource/link creates, replaying the original response for 24 hours and rejecting a changed body with 409.

- **Oct 23:** Raise OpenAPI JSON response-schema coverage from 60.4% to 87.4% with a reusable ErrorResponse and storage success schemas.

- **Oct 23:** Return JSON with error codes and docs hints for unknown API and MCP routes and unsupported methods. Add missing codes to sign-in, throttling and oversized-upload errors while keeping visitor 404 pages as HTML.

- **Oct 23:** mcp.simple-host.app now serves the MCP connector (was a dead address since the DNS-AID HTTPS record).

- **Oct 23:** Give refused photo saves neutral error codes for 401, 403, 404 and 413, so photo-link holders cannot distinguish account-wide storage quotas from per-site caps.

- **Oct 23:** Keep OAuth photo links valid for 60 minutes by rechecking the connection grant, even after the original access token expires. Disconnect, sign-out everywhere and account suspension still refuse the link. API-key expiry caps and archive links are unchanged.

- **Oct 23:** Finish the Add photos round-2 fixes: time out stalled upload bodies, cap API-key photo-link lifetime at the key’s known expiry, remove fragment tokens from browser history, and keep Add more photos in the same page. Document ICC inspection limits and site-name binding.

- **Oct 23:** Address the Add photos security review: bound concurrent saves before reading bodies, index collision checks, reject folder conflicts, cap ICC metadata, time out and bound worker decoders, preserve existing photo-named hosts, and send nosniff on small-box static files. Photo links share the same limits and return retry advice. Document the retained libde265 risk pending a patched prebuilt decoder.

- **Oct 23:** Publish a public DNS-AID capability document at `/.well-known/agent-card.json` with this server’s address, OAuth discovery links and current MCP tools.

- **Oct 23:** The connector lists 20 tools instead of 55: the building tools plus `find_tools`, which finds the rest by the job in plain words, and `run_tool`, which calls any of them by name with the same checks. Every tool still answers when called directly. Its instructions are now the cross-tool rules only, under 2,000 characters, and a signed-out tool error carries the reconnect steps. What an app loads before the person types drops from about 20,300 tokens to about 8,400. The skills, recipes, llms.txt and the MCPJam evals (new advanced-jobs suite) say how to reach the other tools.

- **Oct 23:** Privacy policy names AgentCat under "Who we share it with" and turns its MCP tool usage events on for hosted Simple Host.

- **Oct 23:** Add optional minimal MCP tool usage events to AgentCat, off unless AGENTCAT_PROJECT_ID is set. The minimal payload is superseded by the 2026-10-29 context decision.

- **Oct 23:** The API reference page has a short "Connect a domain" guide (four calls, the two DNS records, what to poll), and llms.txt points to it.

- **Oct 23:** The MCP server answers `initialize` and `ping` sent under the 2026-07-28 revision with method-not-found on HTTP 404, as that revision requires (older clients are unchanged), and repeats each tool's title in its annotations for the connector directories. New `mcpjam` CI workflow runs MCPJam protocol and OAuth conformance, host compatibility and the Claude directory policy against a local instance on every push and pull request, and an eval suite of the core jobs weekly.

- **Oct 23:** Find sites by description, date or page through REST and list_sites using compact summaries captured at deploy time, with a one-time backfill for existing sites.

- **Oct 23:** Limit notification sealing to a bounded startup sweep, bind header tokens separately, keep unsubscribe working without the key, recover rotation after a key change, and replace installer configuration atomically.

- **Oct 23:** Encrypt stored notification signing secrets and custom header tokens with the passcode key, and preserve that key on small-box installs.

- **Oct 22:** Notification review fixes cap API-key subscription lifetimes, isolate OAuth subscription identities, redact MCP destination URLs, limit owner concurrency and URL delivery rates, exclude paused subscriptions from emission, and restrict callback addresses and custom headers.

- **Oct 22:** Notification review fixes restrict deploy keys, preserve leases and retry budgets, queue emission after storage release, and correct callback verification, payloads, retention, and dashboard controls.

- **Oct 22:** Simple Host phase 1 adds signed visitor-storage webhooks, MCP Events, and dashboard notification controls.
