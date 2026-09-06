---
name: connect-domain
description: Connect a user's own custom domain (subdomain e.g. recipes.brand.com via CNAME, or apex e.g. brand.com via A record) to a site already deployed on simple-host. Use when a user wants their site served from their own domain over HTTPS, or wants sign-in on saves from a page (a domain is what adds sign-in to saves; on the shared host anyone can read and write). Drives the bind → DNS → verify → live flow; the agent does the API work and relays the one DNS record the human must add at their registrar.
---

# Connect a Custom Domain

A site deployed on simple-host is already live at
`https://sites.simple-host.app/<handle>/<site>/`. This skill connects the user's **own
domain** — a subdomain (e.g. `recipes.brand.com`) or an apex (e.g. `brand.com`) — so the
site is served from it over HTTPS.

A domain is what adds **sign-in** to saves. Visitors sign in with Google or an emailed code
only on a site's own domain; on the shared host every site is the same origin, so a sign-in
there could never be private to one site — pages there save freely, and anyone can change that
data. This is the reason most people connect one.

**This is agent-driven.** You do every API call and compute the exact DNS record. Then either
**add that record yourself** if you have DNS access for the domain (a provider MCP/API — see step
3b; ask permission first), or hand the human the single record to paste. Buying a domain (when
they have none) and — absent your own DNS access — pasting the record are the only human steps.

## When to use this

- The user asks to use their own domain / brand for a site.
- The user wants saves from a page (a guestbook, RSVP, poll, counter) to be per-person or
  protected by sign-in. The `website-deploy` skill sends you here for that.

## Service

- Base URL: `https://simple-host.app`
- Auth header: `X-API-Key: <api_key>` (the key from deploying the site)
- One domain per site; a domain can be connected to only one site.

## The flow

### 1. Confirm the site exists and pick the domain
The site must already be deployed. Ask the user for the exact domain they want.
**Subdomains** (`recipes.brand.com`) are the simplest path (CNAME). **Apex domains**
(`brand.com`) are fully supported too — the bind returns an A record instead of a
CNAME. Prefer a subdomain when the user has no strong preference; use apex when
they want the bare domain.

### 2. Bind the domain
```
POST /v1/sites/{site}/domain
X-API-Key: <api_key>
Content-Type: application/json

{ "domain": "recipes.brand.com" }
```
Response (subdomain example — CNAME):
```json
{
  "domain": "recipes.brand.com",
  "status": "pending",
  "dns": { "type": "CNAME", "host": "recipes.brand.com", "value": "cname.simple-host.app" }
}
```
For an apex (`brand.com`), `dns.type` is `A` and `dns.value` is the IP to point at —
relay whatever the response returns; don't invent the target.
`409` (`domain_taken`) means the domain is connected to another site **and that binding was
actually verified**. `400` means the domain is malformed or is one of our own hostnames.

**A binding is provisional until DNS proves it.** Until the domain resolves here and serves, the
bind is just a claim: another site can bind the same domain and take it over, and the claim
**expires after 24 hours** if it is never proven. `GET .../domain` shows `bound_at` and, while
unproven, `expires_at`. So do not bind days ahead of the DNS change — bind, get the record
added, and verify in one sitting; if the human can't add the record today, bind again when they
can (rebinding is cheap and idempotent for the same site).

### 3. Relay the DNS record to the human (their only task)
Give them the record from the `dns` object, in plain terms. Subdomain (CNAME) example:

> Add this record at your domain registrar (where you bought the domain), then tell me when
> it's saved:
>
> - **Type:** CNAME
> - **Name/Host:** `recipes` (the part before your domain — many registrars want just the
>   subdomain label, not the full name)
> - **Value/Target:** `cname.simple-host.app`
>
> Leave your other records (especially MX / email) untouched.

For apex, use the returned A record (`Type: A`, host `@` or the bare domain, value =
the IP from the response). Do not ask them to change nameservers or delete anything.
Only this one record is added.

Ask which registrar (or DNS host) holds the domain's DNS, then give them that section's exact
menu path and fields from `references/registrars.md` ·
https://simple-host.app/v1/skills/connect-domain/references/registrars.md (Vercel DNS,
GoDaddy, Porkbun, and a generic section — including how to check the record landed at the
authoritative nameserver before trusting a public resolver).

### 3b. If you can edit the domain's DNS yourself, do it (with permission)
Instead of handing the record to the human, you MAY add it yourself **if you have a way to manage
that domain's DNS** (for example an API or an MCP server for wherever the domain is hosted). Work
out the current provider and the right tool yourself — those specifics change over time.

The record is the same one from the bind response: a **CNAME → `cname.simple-host.app`** for a
subdomain, or the **A record** for an apex. Rules (non-negotiable):

- **Ask the human's permission first**, naming the exact record you'll add. Never change DNS silently.
- **Add only that one record.** Leave everything else — MX/email, other DNS records — untouched.
- Apex **replaces** the domain's current root target, so only do that if the human wants the whole
  domain moved; otherwise use a subdomain, which is purely additive.
- No tool, or any doubt about what's safe to touch → just give the human the record (step 3).
- **Credentials are single-use.** If the user hands you a registrar API key, use it for the one
  write (and a read-back), then forget it. Never store it in the site, the repo, a config file
  or a message.

Ask which registrar hosts the DNS, then follow that section of `references/registrars.md` ·
https://simple-host.app/v1/skills/connect-domain/references/registrars.md — it has the
copy-paste API call (endpoint, auth header, body) for Vercel DNS, GoDaddy and Porkbun, plus the
per-vendor prerequisites (GoDaddy gates the API by account; Porkbun needs a per-domain "API
Access" toggle the human must flip).

Then tell them what you added and continue to verification.

### 4. Verify — fetch the domain
Fetching is the answer, and it's immediate:
```
curl -sS -o /dev/null -w '%{http_code}\n' https://recipes.brand.com/
```
- **200** → done. It's live. Go to step 5.
- **404** → DNS and the certificate are fine, but nothing is being served at that domain.
  Check the bind actually pointed at a site that has content deployed.
- **Connection/TLS failure, but `http://` returns 301** → DNS and routing are correct and only
  the certificate is missing. **This is not propagation — waiting will not fix it.** See below.
- **DNS doesn't resolve yet** → that genuinely is propagation. Re-check the record matches the
  bind response exactly, then retry over a few minutes.

The status endpoint reports the same verdict — the server re-checks bound domains in the
background (every couple of minutes) by resolving them and fetching them, exactly as above:
```
GET /v1/sites/{site}/domain
X-API-Key: <api_key>
```
Returns `{"domain": "...", "status": "...", "verified_at": ..., "last_error": ...}`.

- **`active`** — the domain resolves to us *and* served a page over HTTPS. `verified_at` is when
  that was last proved. It is re-proved hourly, so a domain that breaks leaves `active` on its own.
- **`pending`** — not serving yet; `last_error` says which half is missing:
  `domain does not resolve yet` (propagation, or the record isn't saved),
  `resolves to <ip>, not to this server` (the record points somewhere else — compare it against
  the bind response), or `resolves to this server but HTTPS is not answering yet (certificate not
  issued)` (the DNS half is done; see 4b).
- **`error`** — it resolves here and HTTPS works, but the site isn't served; `last_error` carries
  the code, e.g. `HTTPS returned 404`.

A domain you just bound reads `pending` until the first background check runs, so don't take an
immediate `pending` as a verdict — fetch, and re-read the status a couple of minutes later.

### 4b. If HTTPS never comes up — the certificate is an operator step
Certificate issuance is **not** part of the API. It depends on how the deployment's edge is
configured: an edge with on-demand TLS (e.g. the Caddy setup in `deploy/`) issues automatically,
while an nginx deployment needs the operator to add a vhost and issue a cert once per domain.
The API cannot tell you which you're on — that's why step 4 tests the domain directly.

If `http://` redirects but `https://` fails, tell the user plainly that the DNS half is done and
the certificate is pending an operator step, rather than blaming propagation. If you're the
operator, the per-domain work is a vhost pointing at that domain's site directory plus a cert for
it (see `deploy/prod/nginx-customdomain.example.conf`). Until that is done the status stays
`pending` with `last_error` naming the certificate — that is the signal to act on.

The redirect from the site's `sites.simple-host.app` URL to the domain needs no extra step: the
server writes a `domain-redirect` marker file in the site directory on bind and removes it on
disconnect, and the content-host nginx config tests for that file.

### 5. Confirm it's live
Once `https://recipes.brand.com/` returns 200, it serves the connected site over HTTPS,
on its **own origin**. Pages on it can now sign visitors in (Google or email code), and saves
to the site's backend need that sign-in. The site is still public: a custom domain changes the
address, not who can read it — sign-in gates saving, not reading; it is not a private page.
From now on the site lives only on the domain: its old `sites.simple-host.app/<handle>/<site>/...`
URL answers 302 to `https://recipes.brand.com/...` (same path and query), and the shared-host API
stops accepting writes for it (401 `use_custom_domain`, even with a key — reads stay public).

### Disconnect
```
DELETE /v1/sites/{site}/domain
X-API-Key: <api_key>
```
Unbinds the domain (the site stays live at its `sites.simple-host.app/<handle>/<site>/` path).
Disconnecting reverses both changes immediately — the shared-host URL stops redirecting and
accepts writes again (the redirect is a 302, so nothing stays cached) — and any link people saved
to the domain simply stops working. Tell the user they can also remove the DNS record at their
registrar afterward.

## Backend on a connected domain

The per-site backend (shared JSON state, collections) works from the connected domain
**same-origin** — a page at `https://recipes.brand.com/` calls `/v1/sites/<site>/state` directly.
(The server ties the domain to its own site, so it can't be used to write to a different site.)
Writes here need the visitor signed in — Google (more providers later) or an emailed code
(unlike the shared host, where writes are open): load `https://simple-host.app/auth.js` and,
because the site name cannot be derived from a custom-domain URL, set
`window.SH_CONFIG = { site: "<site>" }` before the tag, then `await SH.requireSignIn()` before
each save. The same page code works on the shared host for a site without a domain, where that call resolves at once.
Once a domain is connected, the site lives only there: its `sites.simple-host.app` page URL
answers 302 to the same path on the domain, and the shared-host API takes no writes for it at all
(401 `use_custom_domain`, with the `domain`, key or not); `/me` there returns
`code: use_custom_domain` so `SH.mount()` shows "This site saves on <domain>. Sign in there to
save." with a link. Agents keep writing through the apex `https://simple-host.app/v1/...` with a
key, or through the domain's own `/v1/`. Disconnecting reverses both immediately. Pattern and API:
the `website-deploy` skill's `references/backend.md`.

## Gotchas

- **Add the DNS record, don't replace anything.** Never touch MX/email records — whether the
  human adds it or you do it via an API/MCP.
- **If you have DNS access, do it yourself — but ask first (step 3b).** Explicit human consent
  every time; add only the one record. No tool or any doubt → hand the record to the human.
- **Subdomain or apex.** Subdomains (`recipes.brand.com`) use a CNAME — simplest path.
  Apex domains (`brand.com`) work too via the A record returned by the bind. Prefer a
  subdomain when the user has no preference for the bare domain.
- **`status` tracks reality, but it lags.** The server re-checks bound domains every couple of
  minutes, so `active` means "resolved here and served over HTTPS", not "someone hoped so".
  Fetching the domain is still the immediate answer; read `last_error` to see which half is
  missing (step 4).
- **Users never upload certificates.** But issuance isn't automatic on every deployment — it
  depends on the edge (step 4b). DNS pointing at us is required first either way.
- **`http://` 301 but `https://` failing is NOT propagation.** DNS is already correct; the
  certificate is the missing piece. Saying "it's still propagating" here sends the user away to
  wait for something that will never happen on its own.
- **Propagation is not instant.** A domain that doesn't resolve at all right after the record is
  added is normal; give it a few minutes. Check the registrar's own nameserver first
  (`references/registrars.md`); a public resolver can hold the old answer for the old TTL.
- **A bind is provisional until DNS proves it.** An unproven binding can be taken over by
  another site and expires after 24 hours (`GET .../domain` shows `bound_at` and `expires_at`
  while unproven). Bind and add the record in the same sitting; `409 domain_taken` only fires
  against a binding that was actually verified.
