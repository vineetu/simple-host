# Hosts and access

The host dispatcher and package map are in [ARCHITECTURE.md](../../ARCHITECTURE.md#hosts-and-how-a-request-flows).
This guide covers the contracts shared by routing, TLS, account and visitor identity, and request
accounting. Paths below are repository paths; `host/handler/` is abbreviated as `handler/`.
Exact endpoints are in [routes](../generated/routes.md); settings are in [configuration](../configuration.md).

## Host routing and certificates

### Site and person hosts

`PERSON_HOSTS` and `SITE_HOSTS` accept `off`, `serve`, `canonical`; site hosts require person
hosts. Defaults are off (path model). Serve answers hosts but hands out path addresses; canonical
hands out host addresses. `handler/personhost.go:PersonHosts` serves the person's public sites/home
at `<handle>.<base>/`; its `/<site>/` path redirects to an own address, or to a ready site host
only in canonical site-host mode (`sitehost.go:siteHostCanonicalOn`); otherwise it serves the Host
fallback. Aliases preserve old handles/site names and path/query.

`handler/sitehost.go:SiteHosts` serves `<site>.<handle>.<base>` files at `/` and binds `/v1/`
lookups to that site. Normal files redirect with 302 to a verified own address; version previews
are an explicit exception. Readiness gates advertised URLs and redirects. With a configured
certificate hand-off directory, readiness requires its regular `ready/<handle>` marker; without
one, `handler/sitebase.go:siteCertReadyOn` returns true (TLS is the operator's responsibility).
Before readiness, Host hands out the person-path fallback; Hack does not expose that fallback.

| Site-certificate hand-off | Contract |
|---|---|
| App request | `SITE_CERT_DIR/requests/<handle>`; `handler/sitehost.go:StartSiteCertRequests` and request helpers |
| Activation | Root-owned issuer triggered by a path unit and two-minute safety timer; `deploy/site-certs/simple-host-site-certs.{path,timer}` |
| Issuance | `deploy/site-certs/issue.sh` ensures explicit handle/wildcard A records, then DNS-01 through Vercel hooks in `/usr/local/lib/certbot-vercel/` |
| Queue bounds | Defaults: 10,000 per rolling week, 1,000 per rolling day, 30 per run; queue bounds, not CA quotas (`issue.sh`) |
| CA fallback | Let's Encrypt rate limits enter Google Trust Services then ZeroSSL; Google request/order pacing is shared across issuers (`deploy/cert-issuers/fallback.sh`, `google-certbot.py`) |
| Completion | Issuer deploy hook copies certificates for nginx and writes `ready/<handle>`; failure/deadline markers permit retry (`deploy/site-certs/`) |
| TLS lookup | nginx loads the per-handle certificate using a variable (`deploy/prod/nginx-site-base-domain.conf` and its generator) |

The issuer is outside the Go process; the app never invokes certbot. Current timer files specify
two minutes even though the introductory comment in `deploy/site-certs/issue.sh` still says ten.

### Custom domains and claimed platform names

The owner binds a custom domain through the domain API. `handler/domaincheck.go:startDomainChecks`
runs pending checks every two minutes by default (`DOMAIN_CHECK_INTERVAL_MINUTES`); active bindings
are due for re-proof hourly (`domainActiveAge`). A previous working address stays live while a
replacement is pending. Claimed `<name>.<base>` names are immediately verified and served in-process
by `handler/platformsubdomain.go:BoundSubdomains`, with files at `/` and same-origin visitor/API routes.

| Custom-domain hand-off | Contract |
|---|---|
| App | Require matching TXT ownership token at `_simple-host.<domain>` unless the binding is already verified and proof-exempt; failed proof cancels requests. After proof, DNS pointing here without working HTTPS can queue `DOMAIN_CERT_DIR/requests/<domain>` (`handler/domaincheck.go:checkDomain`, `domainProof`, `domaincert.go`) |
| Issuer activation | Path unit plus two-minute timer (`deploy/domain-certs/simple-host-domain-certs.{path,timer}`) |
| Proof and limit | Recheck A/AAAA records; default maximum 50 new certificates per rolling day (`deploy/domain-certs/issue.sh`) |
| Challenge | certbot HTTP-01 through `/var/www/acme`, answered by port-80 proxy configurations |
| Publish | Write managed `sites-enabled/simple-host-domain-<domain>` from `vhost.conf.template`, test/reload nginx, write `ready/<domain>` |
| Failure | Write `failed/<domain>`; the app exposes its reason in domain `last_error` (`handler/domaincert.go:domainCertProgress`, `domains.go`) |
| Removal | Require the `DATA_DIR/domains/<domain>` binding link; when it disappears, remove the issuer's own vhost and certificate. Leave hand-made servers/certificates alone (`issue.sh`) |

Active failures warn after 24h and release after 72h by default; limits are configurable. Once
emailed, the stored release date wins over later limit changes (`handler/domaincheck.go:applyDomainCheck`).
A released claimed name stays in `legacy_hostnames`: `handler/legacyhost.go:LegacyHostRedirect`
302s it to the site's current address. Older unbound per-name hosts keep their legacy 301 path
redirect; hosted Hack uses 404 instead of that historical fallback.

### Address families

An account connects `*.<suffix>`; its site `<site_prefix><label>` answers at `<label>.<suffix>`.
The host names exactly one account/site. TXT ownership proof (or admin proof exemption) plus
wildcard DNS resolving only here are required. Verification defaults to every ten minutes,
with active re-proof hourly (`handler/familyhost.go:StartAddressFamilies`,
`handler/familyapi.go:checkFamilies`, `config/limits.go:DefaultLimits`).

| Family hand-off | Contract |
|---|---|
| Disk link | `DATA_DIR/families/<suffix>` → `by-id/<user_id>`; `host/storage/families.go:BindFamily` |
| Request | `ADDRESS_FAMILY_CERT_DIR/requests/<suffix>` contains token, relative account link, certificate mode/lineage, site prefix and reserved labels (`handler/familyapi.go:familyRequestBody`) |
| Issuer | `deploy/family-certs/issue.sh`, installed as `/usr/local/sbin/simple-host-family-certs`; default state `/var/lib/simple-host-family-certs`; path unit plus ten-minute timer |
| Certificate ownership | Check existing operator wildcard lineage covers `*.<suffix>` and is unexpired. Never issue, renew or delete it; normal certbot renewal owns that work |
| Routing ownership | Refuse another nginx server answering the random `sh-probe-….<suffix>` probe; this does not detect every exact-host overlap. Write managed `sites-enabled/simple-host-family-<suffix>`, test/reload and write `ready/<suffix>` (`issue.sh`) |
| Ready | App requires a ready-marker prefix matching the database; proven but non-live family hosts return 404 (`handler/familyhost.go:familyReadyPrefix`, `FamilyHosts`) |

nginx serves `families/<suffix>/<prefix>$label/current` with suspension/offline/passcode and
lives-elsewhere markers; `/v1/`, `/internal/` and misses reach `FamilyHosts`. It handles site-bound
API, renamed labels (302), missing sites (404) and own-domain redirects (302). Main-address ranking
is own domain, most-specific canonical family, then site host (`handler/familyhost.go`).

`deploy/prod/family-adopt.sh` adopts a hand-made wildcard vhost. It defaults to dry run and supports
`--apply` and `--rollback`. The forward order is backup → ready marker → wait until admin API says
`live` → vhost swap → nginx test/reload; failure before live restores the marker without swapping.
Rollback restores the old vhost and reloads before withdrawing managed files/readiness and waiting
for the API to report not live. See also [address-family design](../designs/address-families.md).

### Flat base and legacy content host

`SITE_BASE_MOVE=flat` splits people's addresses across two domains, one decision per person
(`sites/addressing.go:FlatFor`): without a `SITE_CERT_DIR/ready/<handle>` marker a person's page
is `<handle>.<base>` and each site `<site>--<handle>.<base>` (parsed by `Hosts.SiteHostParts`,
built by `TeamSiteHost`, the same functions Hack's flat team sites use). `BaseFor(handle)` is
the base every address of that person is handed out, served and origin-checked under;
`SiteCertReadyOn` is false for a person's hosts under the other base, so `SiteHosts` 302s them
to the address handed out, and `PersonHostOwner` does not resolve them. `siteshttp/sitebase.go:SiteBaseHosts`
302s the rest (`Hosts.BaseTarget`): two-label and unknown hosts under the base to the same
labels under `SITE_DOMAIN`, a flat person's `SITE_DOMAIN` page to the base; only page GETs are
redirected (the API there is a 404). With `off` a distinct base 301s everything back to `SITE_DOMAIN`. The
[runbook](../operations/site-base-flat.md) owns the wildcard, nginx and rollout.

On legacy `sites.simple-host.app`, nginx uses `/internal/domain-redirect` for own-domain markers
and `/internal/site-redirect` otherwise. Current canonical mode uses 302 with no-store to the live
site address, retaining path/query; `/<handle>` redirects to the person page. `/v1/` remains a
compatibility API for old pages and agents, not a temporary redirect-only endpoint
(`handler/personhost.go:contentHostRedirect`, `deploy/prod/nginx-sites-content-host.conf`).
`contentHostOnlySites` pins `vineetu/eb2-wait` to this host for nginx `/eb2-api/*` integrations;
Go routing does not depend on those sidecars.

## Identities and authority

### Account keys and the connector

Every completed account sign-in issues a fresh key; previous sign-in keys remain valid until
revocation, expiry or rotation. Rotation deletes existing account keys, ends visitor sessions,
issues a replacement and disconnects apps (`handler/user.go:verifySignIn`, `rotateAPIKey`,
`host/app/accounts/signin_operations.go:RotateKey`). Only SHA-256 hashes are stored. The dashboard retains its
key in localStorage and sends `X-API-Key` (`handler/static/index.html`). Admin is a real user row
upserted at boot (`cmd/server/main.go:main`); visitor cookies never confer account middleware power.

OAuth registration, PKCE and consent guard Streamable HTTP `/mcp`. Tool calls and connector bearer
REST replay enter the same gated mux as REST; scope/storage gates still apply
(`handler/connector.go`, `cmd/server/main.go:main`). Replay mints an in-memory `shint_` credential,
revokes it at request end and has a 15-minute TTL backstop (`host/repository/accounts/internalkey.go`). A selected
Hack team uses a separately scoped credential; legacy and chat-facing credential tools are filtered.

`simple-host oauth-client create` registers clients manually (`cmd/server/oauthclient.go`).
Reviewer password sign-in is off unless configured (`handler/reviewer.go`);
`cmd/server/reviewaccount.go` manages its account/hash commands. The OpenAI domain challenge is also
configured-only (`handler/openaichallenge.go:RegisterOpenAIAppsChallenge`). Instance asset hostname
rewriting is `handler/instancehost.go`; `plugins/openai/` contains the ChatGPT package.

### Teams: who may act as an account's owner

One function answers "may this person act as the owner of this account's sites":
`host/app/accounts/teams.go:ActsAsOwner` (the account is their own, or a team they belong
to, read live from `team_members` on every call). Two doors reach it:

- **A key, connection or sign-in naming `owner=<team>`** on a `/v1/sites` route,
  `GET /v1/me/deleted-sites` or `GET /v1/analytics/sites`. `auth.ScopeGate` calls
  `accounts.Service.ResolveOwner` before the mux, which refuses a non-member
  (`404 owner_not_found`), a deploy-only key (`403 deploy_only_key`) or a one-site Hack
  credential, and otherwise marks the request (`accounts.WithActingOwner`). From then on
  `GetUserByAPIKey` answers the team's account for that request's key, so the auth
  middleware, the storage owner check (`resolveWriterKey`), passcode and viewer bypasses and
  every site, version, domain, export and storage handler act on the team's sites with no
  change of their own. `model.User.Member` names the person (version author, image uploader).
  Owner equal to the caller's handle, the admin key, and (on the analytics routes only) a
  handle that is no team leave the request exactly as before. Only the Simple Host program
  (`edition.Identity().PersonalSites`) reads `owner`, from the query only.
- **A person signed in on the site's own address.** Storage's owner-on-site
  (`storage.DefaultProvider.OwnerOnSite`, the default binding provider), the owner frame
  guard, `site_owner` on `/v1/sites/{s}/me`, the push owner role and the named-viewer check
  (`viewerAllowedCtx`) call `ActsAsOwner`, so `auth.role == "owner"` in storage rules is any
  member. Simple Hack's provider keeps its own answer.

Team management (`/v1/teams`, `host/app/sites/teams.go`) checks membership through the same
function. Joining is by invitation only, to an email: inviting looks up no account and answers
alike for any email; an invitation is shown to, and answered by, only the account whose sign-in
email it is, verified (`visitorEmail`; a username that is no email matches nothing). Deleting
the last member's account takes each such team with it, decided under the team's row lock, and
is refused like the account's own deletion when that team is suspended or has a taken-down site. Paths that compare a person with the site's account directly and do not use it:
`notify.go` (an owner's own on-site write sends no notification; teams cannot subscribe to
notifications, being `event_account` rows) and `visitor_tools.go` (a visitor tool read by the
owner's own connection; it never takes `owner`).

### Apex SSO and site sessions

The apex cookie is `__Host-sh_sso`: host-only, HttpOnly, Secure, SameSite=Lax, Path=/; HTTP development
uses `sh_sso` without Secure. SSO routes issue/read it under `/v1/sso/*`, while account middleware
still requires keys or connector tokens. Dashboard sign-out also reads it solely to revoke that
browser family (`handler/sso.go:ssoCookie`, `setSSOCookie`, `endBrowserSSO`).

- `sso_passes` stores a hash referencing `api_keys.key_hash` with ON DELETE CASCADE; SSO-created
  `visitor_sessions.sso_pass_hash` references that pass with the same cascade (`host/db/schema.sql`).
- Pass reads require live pass/idle expiry, live parent key/idle expiry and an unsuspended account
  (`host/app/accounts/sso.go:GetSSOPass`,
  `host/repository/accounts/acct_sso.go:GetSSOPass`, `principals.go:liveSSOParent`).
- Site start stores host, return destination and nonce hash in `sso_hops`, then sends the browser
  to the apex for remembered consent or shared sign-in. Redemption creates a 60-second host- and
  nonce-bound establishment code (`handler/sso.go:startSSO`, `hopSSO`).
- Pass issuance serializes on the parent key, keeps one pass per key and retires the previous
  browser family (`host/app/accounts/sso.go:ReplaceSSOPass`,
  `host/repository/accounts/acct_sso.go:ReplaceSSOPass`). Browser verification then calls `/v1/sso/session`.
- Site sign-out revokes the parent key (cascading sibling sessions) and stores a short-lived
  `sso_signouts` ticket. Even absent/expired/legacy local sessions receive the apex navigation;
  completion revokes the current apex-cookie family and clears the cookie (`handler/sso.go:endSiteSSO`,
  `signedOutSSO`, `host/app/accounts/sso.go:RevokeVisitorSSO`, `CompleteSSOSignout`;
  SQL in `host/repository/accounts/acct_sso.go`).
- `SetSSOHop` wires automatic named-viewer gates. Provider discovery lets `/auth.js` retain its
  legacy behavior on instances without a hop-capable apex (`cmd/server/main.go`, `handler/sso.go`).

Visitor sessions cover one site and host. Pages include `/auth.js` (`window.SH`) and use
`SH.signIn()` / `SH.requireSignIn()` before identity-dependent storage. Hosted Hack serves
the same helper with its own header and wording (`handler/static/auth.js`,
`products/hack/pages/hack_assets.go:hackAuthJS`). `GET /v1/sites/{site}/me` reports the session without extending it
(`handler/visitorsession.go:getVisitorMe`).

### Legacy visitor login

Cached pages and instances without apex SSO retain the provider and email flows:

| Step | Protection and source |
|---|---|
| Provider start | `/v1/visitor/oauth/{provider}` starts on the site origin; an apex start for a site redirects there. Set ten-minute host-only `__Host-sh_vnonce`; store only its SHA-256 with OAuth state (`handler/oauth.go:startOnSite`, `beginOAuth`) |
| Provider callback | Callback reaches apex and creates a one-time token carrying nonce hash; redirect to the site `/v1/visitor/establish?once=...` (`oauth.go:callback`) |
| Establish | Admit navigation first, then consume the code before host/nonce/session validation and set host-only `__Host-sh_vsess` on success. Navigation rejection leaves the code usable; host/nonce/session failures after consumption burn it (`handler/visitorsession.go:establishVisitor`, `host/app/accounts/acct_access.go:ConsumeEstablishToken`, `host/repository/accounts/acct_visitors.go:ConsumeEstablishToken`) |
| Email | `POST /v1/sites/{site}/visitor/auth` and `/verify` bind codes to the site and issue its visitor cookie (`handler/visitoremail.go`) |

Cookie helpers use HTTP development names without the `__Host-` prefix; HTTPS uses only the
strict host cookies for protected reads/writes. The shared content host offers no visitor sign-in.

## View locks and saved-data access

`handler/passcode.go:siteGate` checks suspension/offline before a view lock. A passcode is one
shared code for the whole site, not an identity login. Passcodes and named-viewer access are
mutually exclusive (409 on conflicting configuration). Named-viewer access `specific` admits the
owner and the explicitly named email identities after visitor sign-in on the site's own origin;
a suspended account is refused. Owner/admin account API authority is handled separately
(`handler/viewers.go:viewerAllowed`, `viewersLetIn`, owner access routes in `viewers.go` and `passcode.go`).
There is no per-page view lock. List on my profile only controls the profile page.

Storage resources have their own policy in `host/app/storage/access.go`.

## Notification triggers

Visitor storage writes can emit webhook/MCP Events/Web Push notifications in both editions; owner
writes are excluded (`handler/notify.go:emitNotification`), unless the row/key belongs to a
visitor (`data["row_visitor_id"]`, set in `app/storage/{row_write,kv,files}.go`), in which case the
write still emits so that visitor's own web-push devices, and only those, can be reached
(SIM-360) -- it never reaches the owner's own webhook/MCP-event/push subscriptions either way.
Host additionally emits `site.deployed` from
deploy/active-version changes, `domain.status_changed` from domain checks, `viewer.access_requested`
from the named-viewer gate, and `visitor.signed_up` from visitor sign-in paths. The latter events
are excluded on Hack (`emitSiteNotification`, `notify_routes.go:notifySiteEvents`;
`handler/{site_sites_svc,domaincheck,viewers,visitoremail,visitorsession}.go`).
Delivery/outbox/lease details are in [persistence](persistence.md#outbox-and-delivery-leases). A
web-push subscription's own privacy boundary -- a visitor's device never matches an event about
another visitor's data, and a delivery never fires back to the exact actor who made the write --
is enforced in the fan-out SQL itself (`repository/notifications.go:emitSiteEvent`'s `kind='web-push'`
`CASE`), not only in the owner's per-resource recipients control
(`site_push_rules`, checked again at delivery dispatch in `handler/notify_push.go:pushPayload`).

## Request accounting

Create-request idempotency is an in-memory response cache, distinct from the legacy Postgres
`idempotency_keys` mechanism (`handler/idempotency.go:createIdempotencyStore`). It scopes an
Idempotency-Key by account, resolved site (when supplied), method, concrete escaped path and
canonical query apart from creation access/viewers; SHA-256 hashes include those creation
options and the body, so changed access/viewers reject key reuse. It snapshots exact status,
headers and body for replay, expires after 24 hours and cleans hourly. Uploads are temporarily
spooled while hashing and removed at request end, rather than retained as another memory copy.
The wrapper runs after authentication/IP limiting and before response notices. Storage row creates
and file links reuse it after existing resource authorization with caller identity and resolved
site ID (`handler/site.go:Register`, `site_storage_table.go`, `site_storage_files.go`).

`host/httpx/ratelimit.go:AllowState` refills a token bucket continuously and consumes a token
under the same mutex that snapshots its remaining balance; `AllowHTTP` derives RateLimit headers
from that snapshot. The policy window is empty-to-full refill time, not a fixed reset window.

Analytics exclude query strings from the access log, since queries can carry emails or tokens
(`deploy/prod/nginx-analytics-logformat.conf`); local GeoIP and hashed/truncated analytics identity
rules remain in [ARCHITECTURE.md](../../ARCHITECTURE.md#invariants-and-traps).

### Profile listing and search engines

List on my profile (`sites.visibility`: `public` = on, `unlisted` = off, default off)
only controls the owner's profile page. Every open site can be found by search engines
on every address. Go and disk servers add no indexing restriction for this setting.
Missing robots.txt is 404; a site's own robots.txt and meta tags are served as supplied.
Passcode, named viewers, offline, taken-down, deleted and preview responses retain noindex.
Hosted sitemaps enumerate eligible open sites with a platform repository query.
Hack's `Hooks.Sitemap` filter then limits those rows to team sites without an organiser
take-down, preserving their order; the platform-only path makes no product query.
The filter runs before disk checks and per-brand caching; an error returns 503 and caches nothing.
The two database reads are outside a transaction, so a concurrent take-down can appear
in one cached sitemap, as it already can during the cache lifetime.
Own domains (including claimed names) win, then canonical
families, then the normal platform address; redirecting aliases are omitted. Self-hosted
instances enumerate only marketing and docs pages.

## Private from creation

`app/sites.Create` accepts access and normalized viewers. The handler's refusal hook
runs after the handle is known, before inserting a site. Access and viewers are inserted
in the version transaction. After WriteFiles creates the site directory, the passcode
marker is set and verified before commit; only after commit may UpdateCurrent publish.
A rollback removes the attempted marker best-effort; a committed promotion failure keeps
it. Existing named-viewer request gates, noindex and sitemap filters apply unchanged.
Create-or-update checks existing access before writing a version and never adds viewers
on an update. Default creation never touches a marker.
