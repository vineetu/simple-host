---
name: connect-domain
description: Give a Simple Host site its own address, either a free <name>.simple-host.app (one call, no DNS) or the person's own domain or subdomain (for example rsvp.example.com via a CNAME record, or example.com via an A record) to a site they already have on Simple Host, using the connect_domain and domain_status tools. Use when the person wants their site on their own address, asks how to point a domain they bought (Vercel, GoDaddy, Porkbun, Namecheap, Cloudflare, Squarespace and others) at their site, wants visitors to sign in before saving, or needs a private collection (orders, RSVPs, sign-ups), which needs the site on its own address. Gets the one DNS record, relays it with registrar-specific steps, and checks until the domain is live.
---

<!-- Derived from simple-host-website/skills/connect-domain/SKILL.md (+ references/registrars.md). Keep in step. -->

# Connect a custom domain

A Simple Host site is already live at its `sites.simple-host.app` address. This gives it its
own address, served over HTTPS: a free `<name>.simple-host.app`, or the person's own domain. You do the Simple Host side with the
tools; the person adds one DNS record where their domain is managed. The person's own explicit
instructions take priority over this guidance.

You are already signed in as the person. Never ask for a Simple Host email, code or password.
If a tool says the connection is no longer signed in, ask them to reconnect Simple Host in the
app's settings.

## The free address: `<name>.simple-host.app`

One call, no DNS step. Offer it first when the person has no domain of their own, or just needs
the site on its own address (for example, to keep orders or RSVPs private).

- `connect_domain` with `site` and `domain: "clay-studio.simple-host.app"`. It answers
  `status: "active"` at once. Give the person `https://clay-studio.simple-host.app/`.
- First come, first served. `domain_taken`: another site has it; suggest another name.
  `name_reserved`: reserved names (`www`, `api`, `admin`, `mail` and others) are refused.
  `invalid_name`: one label, lowercase letters, digits and hyphens.
- It behaves exactly like a custom domain: everything below applies, and the old shared address
  redirects there.
- A site has one own address. Claiming the free address replaces a custom domain, and
  connecting a custom domain replaces the free address.

## What an own address changes

- The site is served from the domain, on its own origin, over HTTPS (certificates are handled
  by Simple Host; the person never uploads one).
- Visitors sign in (Google or an emailed code) before saving from its pages. Pages written with
  `SH.requireSignIn()` before each save (see `website-deploy`) work unchanged.
- Once active, the site lives only at the domain: its old `sites.simple-host.app` address
  redirects there, and pages save only on the domain. The tools keep working as before.
- Pages stay public, and so does saved data by default. What it adds: a collection can now be
  made private (`set_collection_privacy`), so only the site owner — and the Simple Host
  operator, for moderation — can read it. See `website-deploy` for the form and owner admin
  pages.

## The flow (the person's own domain)

1. **Pick the site and the domain.** `list_sites` for the exact site name. Ask which domain they
   want. A **subdomain** (`rsvp.example.com`) is simplest and leaves the rest of their domain
   alone; use the **apex** (`example.com`) only if they want the bare domain, which moves
   whatever the root currently points at. One own address per site (this replaces a free
   `<name>.simple-host.app` if the site has one).
2. **Bind it:** `connect_domain` with `site` and `domain` (no `https://`). It returns the one DNS
   record: a CNAME to `cname.simple-host.app` for a subdomain, or an A record with an IP for an
   apex. Relay whatever it returns; never invent a target. A "domain taken" error means another
   site holds a verified binding for that domain.
   The binding is provisional until DNS proves it: it expires after 24 hours and another site
   can take it over. Bind and add the record in the same sitting; binding again later is fine.
3. **Relay the record.** Ask where the domain's DNS is managed (usually where they bought it),
   then give the record in plain terms:

   > Add this record where you manage your domain's DNS, then tell me when it is saved:
   > - **Type:** CNAME
   > - **Name / Host:** `rsvp` (just the part before your domain)
   > - **Value / Target:** `cname.simple-host.app`
   > - **TTL:** the lowest offered (60-600 seconds)
   >
   > Leave your other records, especially email (MX), as they are.

   For an apex: Type `A`, Name `@` (or blank), Value = the IP returned. If an A, ALIAS or
   forwarding record already exists at the root, it must be edited to the new IP, not
   duplicated. Never ask them to change nameservers or delete unrelated records.
   For step-by-step screens at **Vercel, GoDaddy, Porkbun, Cloudflare, Namecheap** and others,
   load `references/registrars.md` and give them that section.
4. **Check:** `domain_status`. A freshly bound domain reads `pending` until the first
   background check (every couple of minutes), so check again after the person says the record
   is saved, and a few minutes later if needed. `last_error` says what is missing:
   - `domain does not resolve yet`: the record is not saved yet or is still propagating; have
     them double-check it against the record from step 2, then wait a few minutes.
   - `resolves to <ip>, not to this server`: the record points elsewhere (often an old parking
     record); compare it with step 2 and fix it.
   - `... HTTPS is not answering yet (certificate not issued)`: DNS is done. This is not
     propagation and waiting on DNS will not change it; the certificate is on Simple Host's
     side. Tell the person their part is finished.
   - `error` with e.g. `HTTPS returned 404`: the domain reaches Simple Host but the site serves
     nothing; check the site has content.
5. **Done** when the status is `active`. Give the person `https://<their domain>/` and remind
   them the old address now redirects there.

## Registrar credentials

Relaying the record is the default. Do not ask for registrar logins or access tokens. If the
person offers to let you add the record yourself and you have a way to call their DNS
provider, `references/registrars.md` has the calls; ask permission naming the exact record,
add only that record, read it back, and never store or repeat the credentials.

Disconnecting a domain is not something these tools do; say so if asked.
