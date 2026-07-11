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

Then tell them what you added and continue to verification.

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
  every time; add only the one record. No tool or any doubt → hand the record to the human.
- **Subdomain or apex.** Subdomains (`recipes.brand.com`) use a CNAME — simplest path.
  Apex domains (`brand.com`) work too via the A record returned by the bind. Prefer a
  subdomain when the user has no preference for the bare domain.
- **HTTPS is automatic.** Don't tell users to upload certificates — the cert is issued for them
  once DNS points at us. It cannot be issued until the DNS record is in place (that's the gate).
- **Propagation is not instant.** `pending` right after the user adds the record is normal.
