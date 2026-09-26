> **Status: Superseded (2026-09-26)** by `per-site-subdomains.md` — sites now live at `https://<site>.<handle>.simple-host.app/` (`internal/handler/sitehost.go`, `SITE_HOSTS=canonical`). What this plan built still stands: the person page at `https://<handle>.simple-host.app/` (`internal/handler/personhost.go`, `PERSON_HOSTS=canonical`, done 2026-09-25), the one namespace, and old links redirecting; the 301 soak and the eb2-wait move are tracked in `per-person-subdomains-nginx.md`.

# Plan: per-person subdomains for hosted Simple Host (2026-09-25, not started)

Target: `https://<handle>.simple-host.app/<site>/`; person index at `<handle>.simple-host.app/`; old `sites.simple-host.app/<handle>/<site>/...` redirects forever (path + query kept). Claimed `<name>.simple-host.app` site addresses and custom domains unchanged.

## Verified facts
- Wildcard DNS already resolves (Vercel NS). Cert covers `*.simple-host.app` + apex, **manual DNS-01 renewal, expires 2026-11-07**.
- nginx `/etc/nginx/sites-enabled/simple-host` wildcard block already proxies `<label>.simple-host.app` to Go :8090 (no `access_log shanalytics` yet).
- DB: 37 users, all handles valid DNS labels, 0 collisions with claimed names / legacy hostnames / site names. Only conflict: operator handle `admin` (reserved) with 5 public sites.
- 123 sites; `sites.site_url` stored for 122 rows as `sites.simple-host.app/...` (compute on read instead).
- 12 hosted sites have absolute `https://sites.simple-host.app/...` links (redirect covers); `poojahs19/prepared-shelf` uses root-absolute `/poojahs19/prepared-shelf/...` (prefix shim covers); `vineetu/eb2-wait` uses nginx `/eb2-api/*` (must move); 32 sites use localStorage/IndexedDB (lost on origin change); 2 register service workers.
- Anonymous shared-host writes: effectively none from real users.

## Decisions (planner's recommendations)
1. One shared first-come namespace for handles + claimed site addresses + reserved + legacy hostnames; checked both ways under the same advisory lock key as `ClaimPlatformSubdomain`. Merge reserved lists into `reservedSubdomainLabels`.
2. Routing (new `internal/handler/personhost.go`, order in `cmd/server/main.go:229`): BoundSubdomains → PersonHosts → LegacyHostRedirect → app. Person host: `/` index (reuse renderShowcase), `/<site>/...` via `serveSiteFile`, `/v1/` host-bound (only this owner's sites; never fall back to global name lookup), site with own domain → 302 there, prefix shim `/<handle>/<site>/` → `/<site>/`.
3. Flag `PERSON_HOSTS=off|serve|canonical` (default off; event/self-hosted keep path model).
4. `sites.` content host → Go `ContentHostRedirect`: 302 for ~a week, then 301; `/v1/`, `/internal/` pass through forever. Analytics site_id unchanged; ingester attributes person hosts.
5. Security: person host = own origin → visitor sign-in + every page save requires sign-in + private collections work on person host. Session bound to host, covers that owner's sites. `__Host-` cookies + same-origin checks. Submit simple-host.app to the Public Suffix List in P0 (months to ship; nothing waits on it).
6. Bugs to fix regardless (live today): `ConsumeEstablishToken` (internal/db/visitors.go:204-218) doesn't burn a code presented on the wrong host; `authorizeStateOrigin` legacy per-name origin rule (site.go:608-610); `resolveSiteID` global-name fallback (site.go:523); ingester attribution of label hosts (ingest.go:681-688); auth.js:57 first-label-as-site.
7. Everything emitting/parsing addresses: one helper `siteURL/personURL`; site.go 387/884/1511/1617, showcase.go, chrome.go, connector.go ContentOrigin, mcp tools/instructions/outputs, auth.js, all skills (canonical `simple-host-website/skills/` → sync to plugin, OpenAI zip, standalone repo; bump version; resubmit OpenAI listing text), static pages (index, showcase, features, architecture, enterprise-architecture, privacy, terms, support, llms.txt, generate.go), openapi, docs, check-docs-sync rule forbidding the old form.
8. nginx (operator): add access_log to wildcard block; `sites.` block → plain proxy to Go; move `/eb2-api/*` to vineetu person host in P2.

## Rollout
- P0 prep (~1 day): namespace, admin handle rename + alias, bug fixes, PSL submission.
- P1 `serve` (2–3 days): person hosts live alongside; nginx swap; verify from client.
- P2 `canonical` (~2 days): all URLs/text switch; `sites.` 302s; eb2 move.
- P3 (~0.5 day, after ~7 quiet days): 301s, drop shared-host write paths, INTENT update.
Rollback: flag back; 301 only after soak.

## Owner questions
1. New handle for the `admin` operator account (default `simple-host-team`).
2. Allow a one-time handle change before P2 (e.g. `1952911286` becomes a public address)? Default no.
3. Who does the PSL PR + `_psl` TXT at Vercel, and the manual cert renewal before 2026-11-07 (or automate DNS-01 via the Vercel API token `eventdns` already uses).

## INTENT.md
New decision "2026-09-25. Per-person subdomains" reversing "2026-09-24. No per-person subdomains"; rewrite shared-address, private-collections, free-subdomain and visitor-sign-in decisions accordingly; event/self-hosted keep path model.
