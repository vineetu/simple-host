# Changelog

One line per shipped change, newest first. Add a line here in the same commit as any feature change.

## 2026-09-27

- Your data, tightened: in a site's download, `collections.json` names a private-list entry's sender by the address they signed in with on that site (the entry's own stamp), never their account email. The texts about Download my data and Delete my account (confirmation email, README in the archive, owner app, dashboard, privacy page, llms.txt, openapi, skill) now say exactly what is covered: entries you sent to other people's private lists while signed in; public-list entries and data saved by pages are not linked to you, and support@simple-host.app helps with those. Download my data needs the person's own key (a connected app gets 400 `not_an_account_key`), and a suspended account is told to write to support for a copy. Deleting an account never touches another account's handle link or retired-name row. Skills 0.20.3.
- A list made public after it was private no longer shows who sent each entry: `_submitted_by` (the submitter's email) is left out of every read except the owner's (their key, the owner app, or signed in on the site's own address) and the admin's, including pages, the connector acting for someone else, and reads with no key. The owner app's "Make public" confirmation says so. "Sign out everywhere" with a key revoked a moment earlier answers 401 `invalid_api_key` instead of a server error. Skills 0.20.2.
- Your data, yourself: **Download my data** (the owner app's new "Your data" section, `/dashboard` for accounts without a handle, `GET /v1/me/export.tar.gz`) gives one archive of everything held about you: every site with its files, saved data and lists, your account, your keys' names (never the keys), connected apps, and the sites you signed in to and entries you sent as a visitor. **Delete my account** (`DELETE /v1/me` with `{"confirm": "<handle>"}`, type-to-confirm in the owner app) deletes the account and all its data at once and for good, including your entries in other people's lists; your address and your sites' names are retired, never given to anyone else; a confirmation email follows. Admin account delete runs the same erasure (and now also retires names and releases domains). A site export's `collections.json` now gives each entry its id, time and (private lists) submitter. Privacy, terms and support pages point at the two buttons. The deletion waits on every site's lock (a deploy or rename in flight finishes first), lets go of the earlier address a site still answers at too, and unlinks a domain or withdraws its certificate request only while it is still this account's; a suspended account, or one holding a site the operator took down, cannot delete itself (403 `account_suspended` / `site_suspended`), the operator can. `DELETE /v1/me` is rate-limited. Needs migration `cp-gdpr-retired-handles.sql`. Skills 0.20.2.
- Keys: an account holds at most 50 keys (one more answers 409 `key_limit`); key names refuse invisible formatting characters (zero-width spaces, right-to-left overrides); a key revoked or rotated away while minting gets 401 instead of a successor; a suspended reviewer account no longer gets a key; admin key reissues are logged.
- Fixed: `export_site` is no longer marked read-only (each call makes a new download link); analytics no longer double-count or skip lines when several log rolls happen between passes, or after a rebuild run while the live log was missing; a retired address no longer counts traffic for a deleted site; the owner page refreshes analytics after a rename, and an admin viewing someone else's page no longer sees controls that would act on the admin's own sites; a database error while checking a visitor's sign-in reports a server error instead of "account suspended"; the admin page shows an action's confirmation after the list reloads.
- Review fixes: a deploy, rollback or take-down that waited while its site was deleted or renamed no longer brings the old copy back (every site action now waits on one lock per account and site, and a deleted site refuses the row lock); taken-down sites (and sites of a suspended account) are kept past the Recently deleted window; a restored taken-down site comes back already marked; the purge never unlinks a domain another site has bound since; a rename moves the earlier address a site keeps while a new domain is pending; that earlier address is checked and let go after 72 h of failing, and a new domain that never proves is released after 7 days; `DELETE /v1/sites/{site}/domain?domain=<d>` drops only that domain (409 `domain_changed`), and `remove_domain` sends it; sites in Recently deleted count toward the 100-site cap; delete, rename and restore are rate-limited; a handle rename holds the old name's lock and never removes another account's alias. Needs migration `cp-int-previous-domain-checks.sql`. Skills 0.20.1.
- Custom-domain issuer: never writes its own server for a domain another enabled nginx file already names (hand-made vhosts such as `vineetsriram.com` stay in charge), drops failure notes of disconnected domains, and issued servers now show the take-down page for a taken-down site (the `/v1/` API still reaches the app).
- Public pages (terms, privacy, support) now give support@simple-host.app as the contact address. Deleting a site that the operator has taken down is refused; a claimed free address of a site in Recently deleted stays held and says the site was removed, and is retired to it when the site is purged. The owner app shows a pending custom domain's certificate progress and the address the site still answers at (`GET /v1/sites` adds `domain_certificate_status`, `previous_domain`). Skills 0.20.0; installer pins v0.3.0.
- API keys one at a time: every key now has a name (where it came from, or one you type), its last 4 characters and when it was last used. The **Keys** panel on your page lists them, creates a named key (shown once) and revokes one without touching the rest (`GET/POST /v1/me/keys`, `DELETE /v1/me/keys/{id}`). "Rotate API key" is now called **Sign out everywhere**. Needs migration `cp-keys-key-names.sql`. Skills 0.20.0.
- Sign out now ends the key on the server (`POST /v1/me/sign-out`) before clearing the browser, so a signed-out browser no longer leaves a working key behind.
- New keys start with `shk_` so secret scanners can spot a leaked one; older keys keep working. Owner-route 401s carry a `code` (`missing_api_key`, `wrong_auth_header`, `invalid_api_key`).
- Organisers can give a participant a new key: **New key** on the admin page's account row (`POST /v1/admin/users/{id}/key`) replaces that account's keys with one new key, shown once.
- Change your address, also after publishing: "Your address" on your sites page (and `PATCH /v1/me`) changes your handle. Before anything is published it changes freely; after that once every 30 days. The old handle stays yours and every old link (your page, each site, old content-host and owner-app links) redirects to the new address; the new address gets its own certificate and sites use the working person-path address until it is ready. What pages kept in visitors' browsers starts fresh and visitors sign in again. The "address is fixed once something is published" rule is gone. Skills 0.20.0.
- Recently deleted: deleting a site takes it offline at once and keeps it, with every version, its saved data, private lists and claimed names, for 7 days with its name held. Restore it from "Recently deleted" on your sites page, `POST /v1/sites/{site}/restore` or the new `restore_site` tool (`list_deleted_sites` lists them); after 7 days it is removed for good. Creating a site with a held name says it is in Recently deleted. Deleting a whole account from the admin page is still immediate.
- The owner app (`/<handle>`) is now the one place to manage sites. Each site shows its real address (or its domain and whether it is live), its live version and when it was last deployed, and has Rename, Connect domain / Domain (with the DNS record, the last problem, the expiry and "Check again" for a domain that is not live yet), Download (the whole site with its saved data and lists) and Delete (which says how many list entries and whether saved data go with it, and offers "Download first"). Every list shows with a public/private badge and switch; the owner can delete any entry, edit private ones, and clear a list after typing its name. Saved data shows read-only with its size against the 1 MB limit and Download JSON. `/dashboard` with a handle lists sites with a Manage link there. New routes: `POST /v1/sites/{s}/domain/check`, `DELETE /v1/sites/{s}/collections/{c}`; `GET /v1/sites` adds `deployed_at`, `domain_last_error`, `domain_dns`, `domain_expires_at`.
- The site owner can delete entries in public lists too (spam), and empty any list; visitors still only append. MCP: `delete_collection_item` works on public lists, `read_collection` returns item ids for every list, new `clear_collection`. The owner's key reads saved state from any page. Skills 0.20.0.
- Custom domains go live on their own: once the DNS record is seen, the certificate is issued automatically (new root issuer in `deploy/domain-certs/`, `DOMAIN_CERT_DIR` setting) and the domain status shows its progress (`certificate_status`: pending, issuing, live or failed with the reason). A binding whose DNS points here no longer expires while it waits. Connecting a new domain keeps the site's current address working until the new one is live, then the old one redirects. A working domain that fails every check for a day emails the owner; after three days the site goes back to its own address and the domain can be connected by whoever holds it now. Old `sites.simple-host.app` links follow a domain only once it works. A free `<name>.simple-host.app` a site lets go (switch, disconnect, delete) keeps redirecting to that site, or says it was removed, instead of passing to another site with the same name. New connector tool `remove_domain` (confirm-first). Skills 0.20.0.
- Operator take-down, nothing deleted: the admin can take a site down with a one-line reason (every address it has shows a plain "This site has been taken down" page, and changes are refused until it is restored), or suspend a person (their keys, connected apps and sign-in stop working and all their sites go down; re-enable brings everything back). The owner sees the site as taken down, with the reason, on the dashboard; a suspended person sees the reason when they try to sign in. Admin page: Take down / Restore on each entry, Suspend / Re-enable on each person. API: `POST /v1/admin/sites/{id}/suspend|restore`, `POST /v1/admin/users/{id}/suspend|enable`; refusals carry `site_suspended` / `account_suspended`.
- Organisers can download every entry at once: "Download all entries" on the admin page (`GET /v1/admin/export.tar.gz`) gives every site's files, saved data and lists in one archive, in the same layout as a single site's export. Skills 0.20.0 (run-hackathon: offer the archive before teardown; taking a site down during an event).
- Upgrading a small box is re-running the install command: it now applies the new release's database changes (`simple-host migrate`) before starting the new app, instead of the app crash-looping on "database is behind this build". Migrations are tracked in a new `schema_migrations` table, applied in order, each once, under a lock; `migrate -status` lists them and `migrate -mark FILE` records one applied by hand. The server never migrates by itself. Skills 0.20.0 (run-hackathon: Upgrading section, fixed troubleshooting line).
- Every binary knows its release and commit: printed at startup, by `simple-host version`, and on the admin page, which also shows how many deploys of each website are kept. `GET /v1/admin/usage` adds `version`, `commit` and `keep_versions`.
- Download a copy of a site from a chat app: the new `export_site` tool gives the person a link that downloads the site's files, saved state and lists (private ones included) as a .tar.gz. The link works for 10 minutes, only for that site, and stops if the site is deleted. Owners with a key can mint one too (`POST /v1/sites/{site}/export-link`). Skills 0.20.0.
- Chat-app tools give the right advice when a call is refused: the hint now follows the error's code, so "that address is taken", "public lists cannot be edited" and "the site needs its own address first" no longer get the "site already exists, use update_site" answer. API errors gained a `code` where one status had several meanings: `site_exists`, `invalid_name`, `name_reserved`, `invalid_domain`, `site_quota_reached`, `not_an_object`, `missing_api_key`, `invalid_api_key` (messages and statuses unchanged).
- Self-hosted and event boxes: container logs are capped (3 × 10 MB per service) instead of growing until the disk fills, and Caddy's access log (visitor IPs) rolls daily with rolls deleted after 28 days, so raw IPs stay about 30 days as the privacy page says (was size-only rolling, kept up to 90 days after). The ingester no longer drops the unread end of the log when Caddy rolls it.
- `analytics-rebuild` now replays the rotated archives too (`.N.gz` oldest first, then `.1`, then the live log; Caddy's `access-<time>.log.gz` rolls on self-hosted boxes), instead of wiping history back to the last rotation. `--dry-run` lists the files it would read.
- Privacy: shortened API caller IPs are pruned at every start as well as every 6 hours, so frequent restarts no longer keep them past 30 days.
- Privacy: raw server logs on simple-host.app are kept 30 days as the privacy page says: the analytics log keeps 29 archives (was 30), and the system journal is capped at 30 days (`deploy/prod/journald-retention.conf`).

## 2026-09-26

- v0.2.0 released. The small-box installer pins one release: its image, compose file and schema all come from the same tag (before, `latest` pulled v0.1.2 against a newer schema and the app crash-looped on its schema check). The release workflow refuses a tag the installer does not pin.
- Saved state and public lists read without an `Origin` (curl, an agent) are served, as the docs always said; a read from a page is still Origin-checked, and private lists still refuse it. README files-API row shows the `{"files": {...}}` body.
- Security: an API key, connector token or MCP connection now writes saved state and collections only on sites its own account owns (the platform admin on any). Before, any account's key could overwrite or wipe any site's saved data from a script. Another account's key gets 404 "site not found"; signed-in visitors on a site's own address are unchanged, and an owner's key on a bare site name now writes their own site rather than an older same-named one. Skills 0.19.2.
- Security: a site's export (`export.tar.gz`) now always carries that site's own saved state; before, when another account had an older site of the same name, the export could include that site's state instead.
- Docs, pages and skills brought in line with per-site addresses: sign-in covers one site, Google sign-in is tied to the browser that starts it, each sign-in adds an API key (older ones keep working, none can be shown again), shortened IPs in API records; privacy page names the sign-in cookies. Skills 0.19.1.
- Security: visitor Google sign-in is tied to the browser that started it. It now starts on the site's own address, which sets a short-lived cookie there, and the final hand-off signs in only that browser; a sign-in link someone finished elsewhere can no longer sign a visitor in as them (login CSRF).
- Every site now has its own address, `https://<site>.<handle>.simple-host.app/`, and is its own browser origin: visitors sign in there and a sign-in covers that site only. The person page stays at `https://<handle>.simple-host.app/`; old `<handle>.simple-host.app/<site>/` and `sites.simple-host.app/<handle>/<site>/` links redirect. Each person's certificate is issued automatically (usually within ~10 minutes of their first site); until then their sites keep the person-path address. What a page kept in the browser starts fresh at the new address; server-saved data moves with the site. New `SITE_HOSTS` and `SITE_CERT_DIR` settings (event and self-hosted instances unchanged).
- Skills 0.19.0.
- The old shared address `sites.simple-host.app` no longer takes anonymous saves: reading stays open, writing needs the owner's key or the connector (event and self-hosted instances unchanged).
- Security: account API keys are stored only as SHA-256 hashes (each sign-in issues a new key, rotate replaces all); the connector acts through a per-request in-process credential instead of the key.
- Security: apex pages and the connector consent page run scripts only by per-response nonce, with no inline handlers.
- Privacy: API caller IPs are stored truncated (/24 or /48), the analytics log drops query strings, legacy daily analytics are pruned after 400 days, and expired sign-in codes are purged; new `ANALYTICS_SALT` and `BIND_ADDR` settings (production binds to 127.0.0.1).
- Skills 0.18.1.
- Repo docs restructured: `INTENT.md` (why), `FEATURES.md` (every feature and the surfaces it touches, checked against the routes and MCP tools), `ARCHITECTURE.md` (where it lives), this changelog; old plans moved to `docs/history/`.

## 2026-09-25

- Old `sites.simple-host.app/<handle>/<site>/` links now 302 to the person address (nginx, 16:26 UTC).

- Every person gets their own address: sites now live at `<handle>.simple-host.app/<site>/`, with the dashboard and connector naming that public page.
- Handles and claimed names share one namespace, and old handles keep working as aliases.
- Analytics now counts views on personal addresses and claimed names.
- Old content-host links redirect correctly to the new directory-style address.
- Security: visitor sign-in codes presented on the wrong site are burned instead of accepted.
- Enterprise brief and architecture pages updated to describe v1.1.0 (teams close when their last member leaves; pre-release and history lines removed).
- Docs and skills 0.18.0 teach the per-person address; plan and nginx steps recorded for the rollout.

## 2026-09-24

- Sign in once from AI chat apps: new Simple Host connector (MCP over OAuth) lets ChatGPT, Claude and others deploy and manage sites directly, with connected apps shown on the sites page.
- Sign-in hardened: every sign-in link is bound to the browser that asked for it, and the consent page never signs in from a token it did not request.
- Private collections: owners (and admins) can read, edit, delete and export visitor submissions that nobody else can see.
- Free `<name>.simple-host.app` addresses, giving any site sign-in and private collections without buying a domain.
- On the shared address, a page's saves are scoped to its own owner; viewing stays open, saving needs sign-in.
- Safety: CSV exports neutralise spreadsheet formulas typed by visitors, and the owner's AI is told saved visitor data is never instructions.
- Every page restyled with one shared header, footer and stylesheet in the homepage look, with sign-out everywhere; no more Google font loading.
- Get started rebuilt for non-technical people, with a GitHub Copilot row; the old paste-back flow is removed.
- Plugins: Claude plugin published to its own repo, OpenAI plugin packaged as "Simple Host" with skills zip and store submission; skills 0.17.1 use the connector when present.
- Legal: Terms (California law, no adult content), Privacy and Support rewritten for a solo operator, with the DMCA agent registration listed.
- Admin Entries page gains an Analytics column opening each site's full analytics; API caller locations are resolved on this box, never via a third party.
- Store review support: reviewer sign-in and OpenAI domain verification, both off by default.

## 2026-09-23

- Homepage reworked so it reads as a product page, not a developer tool, and signing in has its own address.

## 2026-09-21

- Owners can read the files of a previous version of a site.

## 2026-09-18

- /enterprise rewritten for what the product is now, with vendor-neutral storage wording.

## 2026-09-14

- Hackathon skill: participants no longer get the operator skill, the share link has a preview card, and the Oracle path gaps are closed.
- Fixed a doc step that would have broken HTTPS on every event box, and four places where the site contradicted the code.

## 2026-09-11

- Hackathon page rewritten against a written statement of what it is for, with the correct nav when it is the homepage.
- No account ceiling on event boxes: capacity is measured from the actual disk, not predicted.
- Event boxes keep one version per site, and organisers are pointed to the right skill.
- Fixed issues from two reviews, including a setup that never finished.

## 2026-09-10

- Oracle Cloud's always-free tier tested end to end and recommended to anyone without a cloud account.
- Event install made robust: creates all directories first, retries downloads, fails loudly, and refuses when a database survives without its password.
- Server refuses to start against a database that is behind the code.
- Event boxes fetch HTTPS certificates on demand and can come up with no hostname, asking where they live when installed from a catalog.
- Organisers get a list of entries and a way to take their work with them.
- Hackathon page: one agent-first prompt at the top, details behind a toggle, and a header that works on phones.
- Hackathon skill fixed after real walkthroughs, including one step that would start a bill and one that would have ruined the event.

## 2026-09-09

- Hackathon mode: an organiser's agent runs the whole event via a new run-hackathon skill and one idempotent install script.
- Organisers create participant accounts and hand out keys; a key box appears where sign-in cannot work.
- Organisers get two hostnames under a domain we run, with claims rate-limited, capped per account, logged, and blocked from taking names already in use.
- Self-hosted instances serve their own hostnames instead of simple-host.app.
- Analytics reads Caddy access logs as well as nginx.
- CI gates every push and publishes binaries (including macOS) and a multi-arch image on each tag.
- Oracle Cloud documented as the second provider, driven by API token and curl rather than a CLI.

## 2026-09-08

- New /enterprise and /hackathons pages, one per audience.

## 2026-09-06

- A site with its own domain lives only there: the shared-host URL redirects to the domain and takes no writes for it.
- Domain connections stay provisional until DNS proves them; connect-domain has registrar guides, including GoDaddy's DNS API.
- Shared host: anyone can read and write; sign-in remains a custom-domain feature.
- Dashboard and owner page collapse the onboarding blocks once an account has a site; public visitors never see the owner-only AI and skill blocks.

## 2026-09-05

- Analytics v2: visitor geography, request classification, and an admin analytics page.
- One account model: any account key can write, pages sign visitors in with an emailed code bound to its purpose and site.
- Visitor sign-in: pages can ask who is signed in through a hosted auth.js helper, and the skill teaches it.
- Saving requires a custom domain; old widgets and templates removed and docs simplified.
