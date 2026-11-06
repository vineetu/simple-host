# Hosts and access

The host dispatcher and package map are in [ARCHITECTURE.md](../../ARCHITECTURE.md#hosts-and-how-a-request-flows).
This guide covers the contracts shared by routing, TLS, account and visitor identity, and request
accounting. Paths below are repository paths; `internal/handler/` is abbreviated as `handler/`.
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
| Disk link | `DATA_DIR/families/<suffix>` → `by-id/<user_id>`; `internal/storage/families.go:BindFamily` |
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

### Paused base and legacy content host

Hosted `SITE_BASE_MOVE=off` retains the alternate `SITE_BASE_DOMAIN` only to recognize old links.
`handler/sitebase.go:SiteBaseHosts` returns **301** to matching `SITE_DOMAIN` labels, escaped path
and query, including `/v1/`. Keep old TLS proxy blocks, DNS and certificates so shared HTTPS links
reach the redirect; alternate-base issuer units remain disabled. The generated proxy's
`HTTP_MODE=app` sends HTTP directly to matching .app hosts. The [paused-base runbook](../operations/site-base-paused.md)
owns renewal exclusions, backup locations, verification and resume procedures.

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
`internal/app/accounts/signin_operations.go:RotateKey`). Only SHA-256 hashes are stored. The dashboard retains its
key in localStorage and sends `X-API-Key` (`handler/static/index.html`). Admin is a real user row
upserted at boot (`cmd/server/main.go:main`); visitor cookies never confer account middleware power.

OAuth registration, PKCE and consent guard Streamable HTTP `/mcp`. Tool calls and connector bearer
REST replay enter the same gated mux as REST; scope/storage gates still apply
(`handler/connector.go`, `cmd/server/main.go:main`). Replay mints an in-memory `shint_` credential,
revokes it at request end and has a 15-minute TTL backstop (`internal/repository/accounts/internalkey.go`). A selected
Hack team uses a separately scoped credential; legacy and chat-facing credential tools are filtered.

`simple-host oauth-client create` registers clients manually (`cmd/server/oauthclient.go`).
Reviewer password sign-in is off unless configured (`handler/reviewer.go`);
`cmd/server/reviewaccount.go` manages its account/hash commands. The OpenAI domain challenge is also
configured-only (`handler/openaichallenge.go:RegisterOpenAIAppsChallenge`). Instance asset hostname
rewriting is `handler/instancehost.go`; `openai-plugin/` contains the ChatGPT package.

### Apex SSO and site sessions

The apex cookie is `__Host-sh_sso`: host-only, HttpOnly, Secure, SameSite=Lax, Path=/; HTTP development
uses `sh_sso` without Secure. SSO routes issue/read it under `/v1/sso/*`, while account middleware
still requires keys or connector tokens. Dashboard sign-out also reads it solely to revoke that
browser family (`handler/sso.go:ssoCookie`, `setSSOCookie`, `endBrowserSSO`).

- `sso_passes` stores a hash referencing `api_keys.key_hash` with ON DELETE CASCADE; SSO-created
  `visitor_sessions.sso_pass_hash` references that pass with the same cascade (`db/schema.sql`).
- Pass reads require live pass/idle expiry, live parent key/idle expiry and an unsuspended account
  (`internal/app/accounts/sso.go:GetSSOPass`,
  `internal/repository/accounts/acct_sso.go:GetSSOPass`, `principals.go:liveSSOParent`).
- Site start stores host, return destination and nonce hash in `sso_hops`, then sends the browser
  to the apex for remembered consent or shared sign-in. Redemption creates a 60-second host- and
  nonce-bound establishment code (`handler/sso.go:startSSO`, `hopSSO`).
- Pass issuance serializes on the parent key, keeps one pass per key and retires the previous
  browser family (`internal/app/accounts/sso.go:ReplaceSSOPass`,
  `internal/repository/accounts/acct_sso.go:ReplaceSSOPass`). Browser verification then calls `/v1/sso/session`.
- Site sign-out revokes the parent key (cascading sibling sessions) and stores a short-lived
  `sso_signouts` ticket. Even absent/expired/legacy local sessions receive the apex navigation;
  completion revokes the current apex-cookie family and clears the cookie (`handler/sso.go:endSiteSSO`,
  `signedOutSSO`, `internal/app/accounts/sso.go:RevokeVisitorSSO`, `CompleteSSOSignout`;
  SQL in `internal/repository/accounts/acct_sso.go`).
- `SetSSOHop` wires automatic named-viewer gates. Provider discovery lets `/auth.js` retain its
  legacy behavior on instances without a hop-capable apex (`cmd/server/main.go`, `handler/sso.go`).

Visitor sessions cover one site and host. Pages include `/auth.js` (`window.SH`) and use
`SH.signIn()` / `SH.requireSignIn()` before identity-dependent storage. Hosted Hack keeps
`SH.storage` but strips legacy `SH.state`, `SH.collection`, `SH.data` (`handler/static/auth.js`,
`handler/basetext.go:hackAuthJS`). `GET /v1/sites/{site}/me` reports the session without extending it
(`handler/visitorsession.go:getVisitorMe`).

### Legacy visitor login

Cached pages and instances without apex SSO retain the provider and email flows:

| Step | Protection and source |
|---|---|
| Provider start | `/v1/visitor/oauth/{provider}` starts on the site origin; an apex start for a site redirects there. Set ten-minute host-only `__Host-sh_vnonce`; store only its SHA-256 with OAuth state (`handler/oauth.go:startOnSite`, `beginOAuth`) |
| Provider callback | Callback reaches apex and creates a one-time token carrying nonce hash; redirect to the site `/v1/visitor/establish?once=...` (`oauth.go:callback`) |
| Establish | Admit navigation first, then consume the code before host/nonce/session validation and set host-only `__Host-sh_vsess` on success. Navigation rejection leaves the code usable; host/nonce/session failures after consumption burn it (`handler/visitorsession.go:establishVisitor`, `internal/app/accounts/acct_access.go:ConsumeEstablishToken`, `internal/repository/accounts/acct_visitors.go:ConsumeEstablishToken`) |
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
There is no per-page view lock. Unlisted controls listing/robots rather than granting access.

Host grandfathered APIs run through `internal/app/legacy`; storage resources have their own
policy in `internal/app/storage/access.go`. Hosted Hack returns 410 for state/collection/declared-data,
saver/history routes on site and handle aliases, and serves only the narrower storage/visitor
allowlist on custom event hosts (`handler/hack_legacy_storage.go`, `hack_mode.go:hackEventAPIAllowed`).

| Legacy operation | Access contract |
|---|---|
| Public state/collection read | Originless reads can succeed; browser reads apply Origin/Referer gates (`handler/kinds.go:readSitePrelude`, `app/legacy/collections.go:ReadCollection`) |
| Write | Owner/admin key or permitted site-scoped visitor plus `X-SH-CSRF: 1`; anonymous and shared-host behavior depends on WRITE_AUTH_MODE, canonical mode and per-site policy (`app/legacy/kinds.go:VisitorWrite`) |
| Private submission | Only signed-in visitors on an own site origin submit, not account keys or shared-host callers; server stamps identity (`app/legacy/collections.go:appendPrivate`) |
| Private list read | Shared content host returns 404 even with owner/admin keys. Elsewhere owner/admin keys read all; owner browser session on own origin can read. Originless non-owner reads and other visitors, including submitters reading their own submissions, get 404 (`collectionReadView`, `handler/privatecollections.go:ownerBrowserRead`) |
| Personal (`mine`) | Each person reads/writes their own record; owners may obtain limited counts, not other people's records (`app/legacy/personal.go:GetPersonal`, `personalWriter`) |
| Shared board | Allowed visitors edit one item at a time; supplied If-Match version is checked, stale edits return 409 `version_conflict`; whole-board clear/replacement is owner-only (`handler/board.go`, `handler/entry_operations.go`, `internal/app/legacy` write operations) |

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
canonical query; SHA-256 body hashes reject reuse for a different body. It snapshots exact status,
headers and body for replay, expires after 24 hours and cleans hourly. Uploads are temporarily
spooled while hashing and removed at request end, rather than retained as another memory copy.
The wrapper runs after authentication/IP limiting and before response notices. Storage row creates
and file links reuse it after existing resource authorization with caller identity and resolved
site ID (`handler/site.go:Register`, `site_storage_table.go`, `site_storage_files.go`).

`internal/httpx/ratelimit.go:AllowState` refills a token bucket continuously and consumes a token
under the same mutex that snapshots its remaining balance; `AllowHTTP` derives RateLimit headers
from that snapshot. The policy window is empty-to-full refill time, not a fixed reset window.

Analytics exclude query strings from the access log, since queries can carry emails or tokens
(`deploy/prod/nginx-analytics-logformat.conf`); local GeoIP and hashed/truncated analytics identity
rules remain in [ARCHITECTURE.md](../../ARCHITECTURE.md#invariants-and-traps).
