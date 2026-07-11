---
name: connect-domain
description: Connect a user's own custom domain (subdomain e.g. recipes.brand.com via CNAME, or apex e.g. brand.com via A record) to a site already deployed on simple-host. Use when a user wants their site served from their own domain with automatic HTTPS, or wants a private/password-protected site (privacy is offered only on a connected domain). Drives the bind → DNS → verify → live flow; the agent does the API work and relays the one DNS record the human must add at their registrar.
---

# Connect a Custom Domain

A site deployed on simple-host is already live at
`https://sites.simple-host.app/<handle>/<site>/`. This skill connects the user's **own
domain** — a subdomain (e.g. `recipes.brand.com`) or an apex (e.g. `brand.com`) — so the
site is served from it with automatic HTTPS.

**This is agent-driven.** You do every API call and compute the exact DNS record. Then either
**add that record yourself** if you have DNS access for the domain (a provider MCP/API — see step
3b; ask permission first), or hand the human the single record to paste. Buying a domain (when
they have none) and — absent your own DNS access — pasting the record are the only human steps.

## When to use this

- The user asks to use their own domain / brand for a site.
- The user wants a **private** or **password-locked** page: privacy is a property of a
  *connected domain* (its own isolated origin), not of a path on the shared host. If they want
  privacy, connect a domain first.

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
`409` means the domain is already connected to another site. `400` means the domain is
malformed or is one of our own hostnames.

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

### 3b. Better: add the record YOURSELF if you have DNS access (ask permission first)
If you (the agent) already have a way to edit this domain's DNS — a **DNS-provider MCP
server** or an **API token** for the host that manages it — prefer to make the change
yourself instead of asking the human to paste it. Do NOT do it silently: get explicit
permission first, then act.

1. **Find who runs the DNS.** Check the domain's nameservers (`dig +short NS <domain>` or an
   equivalent tool): `*.vercel-dns.com` → Vercel; `*.ns.cloudflare.com` → Cloudflare;
   `*.registrar-servers.com` → Namecheap; `*.porkbun.com` / `curitiba…` → Porkbun; etc.
2. **Check you actually have a tool for that provider** — a connected MCP server (e.g. a Vercel
   / Cloudflare MCP) or an API credential in scope. If you don't, fall back to step 3 (relay to
   the human).
3. **Ask the human for permission, naming exactly what you'll do.** For example:
   > I can add this record for you — I have access to your **Vercel** DNS. I'll add **one**
   > record (`CNAME recipes → cname.simple-host.app`) and change nothing else. OK to proceed?
   Wait for a clear yes. This consent gate is required every time — never modify someone's DNS
   without it.
4. **Make the change — ADD-ONLY, least authority:**
   - Create exactly the one record from the bind response (the CNAME, or the apex A record).
   - **Never delete or edit existing records.** MX, TXT (SPF/DKIM/DMARC), NS, and any existing
     A/CNAME must be left untouched — email and the current site must keep working.
   - Apex is the one careful case: pointing the bare domain at us **replaces** the domain's
     current apex target (e.g. its Vercel A/ALIAS). Only do that if the human confirmed they want
     the whole apex moved; otherwise steer them to a subdomain (`recipes.brand.com`), which is
     purely additive.
   - If the provider supports it, read back the record you created to confirm it's exactly right.
5. **Tell the human what you did** ("Added `CNAME recipes → cname.simple-host.app` in Vercel;
   left everything else untouched"), then continue to verification (step 4 below) as normal.

If anything is ambiguous — you're unsure which record set is safe to touch, the domain has an
existing apex site, or you lack a scoped tool — **don't guess; hand the single record to the human
(step 3) instead.** Add-only + explicit consent is the rule; automation is a convenience on top of
it, never a reason to skip the guardrails.

### 4. Verify — poll until active
```
GET /v1/sites/{site}/domain
X-API-Key: <api_key>
```
Returns `{"domain": "...", "status": "...", "verified_at": ..., "last_error": ...}`.

- `pending` — DNS not yet visible / cert not issued. Wait and poll again (DNS can take a few
  minutes to a couple of hours to propagate).
- `active` — the domain resolves to us and a real HTTPS certificate has been issued.
- `error` — see `last_error`; usually the DNS record isn't in place yet or points elsewhere.
  Re-check step 3 with the user.

Poll every ~30s for a few minutes; if it's still pending after that, DNS is likely still
propagating — tell the user it can take longer and they can come back.

### 5. Confirm it's live
Once `active`, open `https://recipes.brand.com/` — it serves the connected site over HTTPS,
on its **own origin**. This is where private/password-locked pages are available (the site has a
real isolated origin, unlike a shared path).

### Disconnect
```
DELETE /v1/sites/{site}/domain
X-API-Key: <api_key>
```
Unbinds the domain (the site stays live at its `sites.simple-host.app/<handle>/<site>/` path).
Tell the user they can also remove the DNS record at their registrar afterward.

## Backend on a connected domain

The per-site backend (shared JSON state, collections) works from the connected domain
**same-origin** — a page at `https://recipes.brand.com/` can call
`/v1/sites/<site>/state` directly with no extra origin configuration. (The server ties the
domain to its own site, so it can't be used to write to a different site.)

## Gotchas

- **Add the DNS record, don't replace anything.** Never touch MX/email records — whether the
  human adds it or you do it via an API/MCP.
- **If you have DNS access, do it yourself — but ask first (step 3b).** Explicit human consent
  every time; add-only; least authority. No tool or any doubt → hand the record to the human.
- **Subdomain or apex.** Subdomains (`recipes.brand.com`) use a CNAME — simplest path.
  Apex domains (`brand.com`) work too via the A record returned by the bind. Prefer a
  subdomain when the user has no preference for the bare domain.
- **HTTPS is automatic.** Don't tell users to upload certificates — the cert is issued for them
  once DNS points at us. It cannot be issued until the DNS record is in place (that's the gate).
- **Propagation is not instant.** `pending` right after the user adds the record is normal.
