# Changelog

One line per shipped change, newest first. Add a line here in the same commit as any feature change.

## 2026-09-26

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
