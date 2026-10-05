# Simple Host ink theme — 2026-10-05

Status: implemented, verified and deployed. The final app-error follow-up uses the same deployment sequence.

The owner chose the existing 12-scene Simple Host film for `/`, with its teal/blue inks and navy night paper. Preserve all IDs, behavior and test hooks. The intro remains letter-by-letter with the same timing and colours. This inventory covers 125 page/state entries. Filename aliases use the same template and shared stylesheet.

| # | Address / surface | Template or source | State |
|---|---|---|---|
| 1 | `/` | `host-story.html` | intro |
| 2 | `/` | `host-story.html` | 12 scenes |
| 3 | `/` | `host-story.html` | home after skip/end |
| 4 | `/` | `host-story.html` | return visit |
| 5 | `/` | `host-story.html` | replay |
| 6 | `/` | `host-story.html` | scene hash and Back |
| 7 | `/` | `host-story.html` | reduced motion |
| 8 | `/` | `host-story.html` | signed-in dashboard link |
| 9 | `/install.html` | `install.html` | collapsed AI choices |
| 10 | `/install.html` | `install.html` | ChatGPT |
| 11 | `/install.html` | `install.html` | Claude |
| 12 | `/install.html` | `install.html` | Grok |
| 13 | `/install.html` | `install.html` | Copilot |
| 14 | `/install.html` | `install.html` | coding agents |
| 15 | `/install.html` | `install.html` | FAQ |
| 16 | `/features` | `features.html` | feature list |
| 17 | `/features` | `features.html` | Ask panel |
| 18 | `/enterprise` | `enterprise.html` | company landing |
| 19 | `/enterprise` | `enterprise.html` | Ask panel |
| 20 | `/enterprise/brief` | `enterprise-brief.html` | brief |
| 21 | `/enterprise/brief` | `enterprise-brief.html` | print |
| 22 | `/enterprise/brief` | `enterprise-brief.html` | Ask panel |
| 23 | `/enterprise/architecture` | `enterprise-architecture.html` | architecture |
| 24 | `/enterprise/architecture` | `enterprise-architecture.html` | Ask panel |
| 25 | `/architecture.html` | `architecture.html` | architecture |
| 26 | `/architecture.html` | `architecture.html` | Ask panel |
| 27 | `/diagrams/coolify.svg` | `diagrams/coolify.svg` | diagram |
| 28 | `/diagrams/enterprise-aws.svg` | `diagrams/enterprise-aws.svg` | diagram |
| 29 | `/diagrams/fly.svg` | `diagrams/fly.svg` | diagram |
| 30 | `/diagrams/render.svg` | `diagrams/render.svg` | diagram |
| 31 | `/diagrams/upcloud.svg` | `diagrams/upcloud.svg` | diagram |
| 32 | `/setup` | `setup-helper.html + setup/setup.js` | choose product |
| 33 | `/setup` | `setup-helper.html + setup/setup.js` | small-box basics |
| 34 | `/setup` | `setup-helper.html + setup/setup.js` | Enterprise basics |
| 35 | `/setup` | `setup-helper.html + setup/setup.js` | advanced settings |
| 36 | `/setup` | `setup-helper.html + setup/setup.js` | files |
| 37 | `/setup` | `setup-helper.html + setup/setup.js` | assistant and troubleshooting |
| 38 | `/costs` | `costs.html + costs/*.js` | calculator |
| 39 | `/costs` | `costs.html + costs/*.js` | provider detail |
| 40 | `/costs` | `costs.html + costs/*.js` | smaller clouds |
| 41 | `/hackathons` | `hackathons.html` | self-host hackathon guide |
| 42 | `/dashboard` | `index.html` | signed out |
| 43 | `/dashboard` | `index.html` | email-code sign-in |
| 44 | `/dashboard` | `index.html` | key sign-in |
| 45 | `/dashboard` | `index.html` | choose handle |
| 46 | `/dashboard` | `index.html` | Google/token return |
| 47 | `/dashboard` | `index.html` | new-site upload |
| 48 | `/dashboard` | `index.html` | AI builder |
| 49 | `/dashboard` | `index.html` | site cards |
| 50 | `/dashboard` | `index.html` | Admin tab |
| 51 | `/dashboard` | `index.html` | account sign-in settings |
| 52 | `/dashboard` | `index.html` | account data |
| 53 | `/<handle>` | `showcase.html` | owner sites inventory |
| 54 | `/<handle>` | `showcase.html` | versions and preview |
| 55 | `/<handle>` | `showcase.html` | passcode |
| 56 | `/<handle>` | `showcase.html` | domain |
| 57 | `/docs.html` and `/v1/sites/{sitename}/storage/*` | local Swagger and JSON APIs (no owner panel in main) | storage resources |
| 58 | `/docs.html` and `/v1/sites/{sitename}/storage/*` | local Swagger and JSON APIs (no owner panel in main) | KV |
| 59 | `/docs.html` and `/v1/sites/{sitename}/storage/*` | local Swagger and JSON APIs (no owner panel in main) | SQLite |
| 60 | `/docs.html` and `/v1/sites/{sitename}/storage/*` | local Swagger and JSON APIs (no owner panel in main) | files |
| 61 | `/<handle>` | `showcase.html` | legacy data and history |
| 62 | `/<handle>` | `showcase.html` | legacy lists and settings |
| 63 | `/<handle>` | `showcase.html` | savers |
| 64 | `/<handle>` | `showcase.html` | analytics |
| 65 | `/<handle>` | `showcase.html` | connected apps |
| 66 | `/<handle>` | `showcase.html` | keys |
| 67 | `/<handle>` | `showcase.html` | address and home setting |
| 68 | `/<handle>` | `showcase.html` | showcase bio pin order |
| 69 | `/<handle>` | `showcase.html` | recently deleted |
| 70 | `/<handle>` | `showcase.html` | address families |
| 71 | `/<handle>` | `showcase.html` | download/delete account |
| 72 | `/<handle>` | `showcase.html` | change email identities alerts |
| 73 | `/oauth/authorize` | `connect.html` | sign-in |
| 74 | `/oauth/authorize` | `connect.html` | code and handle |
| 75 | `/oauth/authorize` | `connect.html` | consent |
| 76 | `/oauth/authorize` | `connect.html` | reviewer sign-in |
| 77 | `/oauth/authorize` | `connect.html` | connection errors |
| 78 | `/analytics/<site>` | `analytics.html` | signed out |
| 79 | `/analytics/<site>` | `analytics.html` | traffic |
| 80 | `/analytics/<site>` | `analytics.html` | countries |
| 81 | `/analytics/<site>` | `analytics.html` | top pages/referrers |
| 82 | `/admin` | `admin.html + admin-growth.*` | signed out |
| 83 | `/admin` | `admin.html + admin-growth.*` | Overview |
| 84 | `/admin` | `admin.html + admin-growth.*` | Users |
| 85 | `/admin` | `admin.html + admin-growth.*` | Sites and menus |
| 86 | `/admin` | `admin.html + admin-growth.*` | API and growth |
| 87 | `/admin` | `admin.html + admin-growth.*` | Moderation |
| 88 | `/admin` | `admin.html + admin-growth.*` | Tools |
| 89 | `<handle>.simple-host.app/` | `showcase.html` | public showcase |
| 90 | `<handle>.simple-host.app/` | `showcase.html` | empty showcase |
| 91 | `/internal/notfound and missing paths` | `notfound.html` | 404 |
| 92 | `/internal/notfound and missing paths` | `notfound.html` | unavailable or removed person/site |
| 93 | `/internal/offline` | `offline.go` | offline 503 |
| 94 | `protected site and /internal/passcode/*` | `passcode.go` | passcode form |
| 95 | `protected site and /internal/passcode/*` | `passcode.go` | wrong passcode |
| 96 | `protected site and /internal/passcode/*` | `passcode.go` | rate-limited |
| 97 | `/internal/suspended` | `suspend.go` | taken-down 410 |
| 98 | `/report` | `report.html` | form |
| 99 | `/report` | `report.html` | validation |
| 100 | `/report` | `report.html` | sent |
| 101 | `/terms` | `terms.html` | terms |
| 102 | `/privacy.html` | `privacy.html` | privacy |
| 103 | `/support` | `support.html` | help |
| 104 | `/docs.html` | `docs.html + local Swagger` | API docs |
| 105 | `/docs.html` | `docs.html + local Swagger` | install skills |
| 106 | `/docs.html` | `docs.html + local Swagger` | endpoint/schema expansions |
| 107 | `/setup.html (first boot)` | `setup.html` | password |
| 108 | `/setup.html (first boot)` | `setup.html` | choose address |
| 109 | `/setup.html (first boot)` | `setup.html` | DNS check |
| 110 | `/setup.html (first boot)` | `setup.html` | free name |
| 111 | `/setup.html (first boot)` | `setup.html` | finished |
| 112 | `/v1/idle/keep and /v1/idle/restore` | `notfound.html via writeMessagePage` | confirmation |
| 113 | `/v1/idle/keep and /v1/idle/restore` | `notfound.html via writeMessagePage` | success |
| 114 | `/v1/idle/keep and /v1/idle/restore` | `notfound.html via writeMessagePage` | invalid/expired |
| 115 | `/v1/data-notify/stop` | `notfound.html via writeMessagePage` | confirmation |
| 116 | `/v1/data-notify/stop` | `notfound.html via writeMessagePage` | success |
| 117 | `/v1/data-notify/stop` | `notfound.html via writeMessagePage` | invalid/expired |
| 118 | `/v1/me/email/undo` | `notfound.html via writeMessagePage` | opening fragment link |
| 119 | `/v1/me/email/undo` | `notfound.html via writeMessagePage` | confirmation |
| 120 | `/v1/me/email/undo` | `notfound.html via writeMessagePage` | success |
| 121 | `/v1/me/email/undo` | `notfound.html via writeMessagePage` | invalid/conflict |
| 122 | `failed OAuth return` | `oauth.go` | sign-in failed |
| 123 | `failed OAuth return` | `oauth.go` | sign-in unavailable by region |
| 124 | `service error` | `showcase.go` | temporarily unavailable 503 |
| 125 | `service error` | `showcase.go` | last-resort HTML |

Simple Host has no separate `/signin`, `/verify` or `/account` page: sign-in and verify are dashboard states; account controls are in the dashboard and owner app. API/llms/skill routes are JSON or text, so receive documentation updates where needed, never CSS. Email messages themselves remain unchanged; their HTML landing pages use the message template. Health/API errors are plain text or JSON.

The five standalone diagrams are served SVG assets, not user sites. Every app page with chrome gets a Host-only ink stylesheet after its own styles. Bare status/gate pages remain self-contained; the Host passcode CSP adds only the exact theme style hash and data-font source. The existing script hash and form/access restrictions remain. The first-boot setup page shares the visual theme. Published user HTML is never passed through app chrome. The Enterprise marketing pages in this repo are included; the separate Enterprise application repo is excluded. Simple Hack retains its red/teal stylesheet.

Small-box installs share these embedded pages and get the new look with their next release; existing pinned installers do not update from this deploy. Historical screenshots/plans keep their status as history.

Current `origin/main` exposes KV, SQLite and file storage through APIs and local API docs, without a dedicated owner resource panel. Inventory entries 57–60 cover those API/docs states; the existing owner saved-data/list/history panels are styled. No new storage management behavior was added.

Verification results and deployment commits will be recorded below.

Review before deployment: the root handoff only redirects existing token/new/job queries to the existing dashboard; it does not redeem tokens or change nonce/session rules. Host gate styling permits only its exact additional CSS hash and an embedded font, retaining the existing theme script hash, self-only form action, frame refusal and access checks. Hosted SVGs use the existing script nonce. Published files bypass all app/theme assembly. Hack continues through its original theme/status branches and original SVGs; its derived API tag description is kept unchanged.

Verification before the Host deploy:

- WebKit's actual intro failure was a completed `steps(1)` animation retaining opacity 0 on the delayed “l”. Each letter now releases its completed animation. Padded letter boxes retain the original spacing. The intro and “simple half build live all” probes pass in WebKit at 390 px on both live films; fonts return 200.
- Chromium and WebKit cover the public page matrix at 320, 390 and 1280 px in light/dark (198 cases per engine), plus all twelve film scenes and tap/swipe/keys/wheel/skip/return/replay/hash/Back. No page overflow or third-party resource requests. Expected HTTP statuses for missing, offline, taken-down and expired-link pages are retained.
- Owner/admin panels use a throwaway local account and database. Expanded saved-data controls, versions, passcode dialog, domain/rename forms, analytics, account/home/keys/connections and all six admin tabs are covered. WebKit's native select option text overflow is contained in docs and saved-data settings.
- Bare gates (including wrong-passcode and rate-limited), email message outcomes, OAuth consent/reviewer/error states and first-boot screens use rendered fixtures: 300 browser/theme/viewport cases, no layout, script, CSP or external-resource failures. These fixtures test presentation rather than sending real email, finishing a self-hosted install or signing into Google.
- Five Host SVG diagrams pass both engines at every viewport/theme. Simple Hack receives the original diagrams.
- `make check` passes, including DB integration and fresh-install/migration tests against a disposable Docker Postgres. `check-docs-sync` and `check-features` pass. Enterprise's documentation-only parity commit passes `make test`; its app was neither restyled nor deployed.
- A dedicated live account publishes two versions and writes/reads KV, SQLite and files. Both engines exercise versions, passcode setup/removal, home selection/reset and analytics on that account. It is deleted after deployment verification.

The main checkout's unrelated staged work was left untouched. The missing storage-resource owner panel is recorded above rather than adding a new product feature during a visual pass. A physical iPhone was unavailable; the phone checks use Playwright WebKit and Chromium at iPhone dimensions.

Live verification found a remaining plain-text app 404 on unknown URLs. The follow-up routes those misses to the shared 404 and themes the last-resort message fallback, retaining statuses and Hack behavior.

Production rollouts: `7f41403` deployed the Hack handwriting fix first; `bfe150d` then deployed the Host film/theme. The preview was replaced in full (version 4, all eight files including fonts). Enterprise `bc07bc8` updates only the identical parity document. Each binary rollout uses the deploy flock, a fast-forward push, capped build, five retained backups, both service restarts and the complete client verify list. The error-page follow-up follows the same process.
