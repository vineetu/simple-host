---
name: connect-domain
description: Connect a user's own custom domain (subdomain e.g. recipes.brand.com via CNAME, or apex e.g. brand.com via A record) to a site already deployed on simple-host. Use when a user wants their site served from their own domain over HTTPS. Drives the bind → DNS → verify → live flow; the agent does the API work and relays the one DNS record the human must add at their registrar.
---

# Connect a Custom Domain

A site deployed on simple-host is already live at
`https://sites.simple-host.app/<handle>/<site>/`. This skill connects the user's **own
domain** — a subdomain (e.g. `recipes.brand.com`) or an apex (e.g. `brand.com`) — so the
site is served from it over HTTPS.

**This is agent-driven.** You do every API call and compute the exact DNS record. Then either
**add that record yourself** if you have DNS access for the domain (a provider MCP/API — see step
3b; ask permission first), or hand the human the single record to paste. Buying a domain (when
they have none) and — absent your own DNS access — pasting the record are the only human steps.

## When to use this

- The user asks to use their own domain / brand for a site.

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

### 5. Confirm it's live
Once `https://recipes.brand.com/` returns 200, it serves the connected site over HTTPS,
on its **own origin**. The site is still public: a custom domain changes the address, not who
can read it. (The Google sign-in a page needs before it can *write* to its backend gates saving,
not reading — it is not a private page.)

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
domain to its own site, so it can't be used to write to a different site.) Writes still need the
visitor signed in with Google: load `https://simple-host.app/auth.js` and, because the site name
cannot be derived from a custom-domain URL, set `window.SH_CONFIG = { site: "<site>" }` before
the tag. Pattern and API: the `website-deploy` skill's `references/backend.md`.

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
  added is normal; give it a few minutes.
