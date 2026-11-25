---
name: connect-domain
description: Give a Simple Host site a shorter or custom address, either a free short name (<name>.simple-host.app, one call, no DNS) or the person's own domain or subdomain (for example rsvp.example.com or example.com), using set_site_address. Use when the person wants their site on a nicer address, or asks how to point a domain they bought (Vercel, GoDaddy, Porkbun, Namecheap, Cloudflare, Squarespace and others) at their site. Optional, since every site already has its own address.
---

# Own domain or short name

Every Simple Host site already has an address (the `url` tools return). This gives it a shorter or
custom one over HTTPS. You do the Simple Host side with `set_site_address`; the person adds DNS
records where their domain is managed. You are already signed in as the person: never ask for a
Simple Host email, code or password, or for registrar credentials. If the tools are missing,
ask them to connect Simple Host (https://simple-host.app/install).

**Ask first.** Name the site and the exact address and wait for a yes before connecting or
removing one.

## Short name: `<name>.simple-host.app`

`set_site_address` with `site` and `short_name: "bakery"`. It is live at once, no DNS. Lowercase
letters, digits and single hyphens; names are first come, first served and a few are reserved: on
a refusal read `fix` and suggest another. Give the person the address the tool returns.
`short_name: ""` removes it.

## Own domain

1. Ask which domain. A **subdomain** (`rsvp.example.com`) is simplest and leaves the rest of
   their domain alone. Use the bare domain (`example.com`) only if they want it; it moves
   whatever the root points at.
2. `set_site_address` with `site` and `domain` (no `https://`). The answer lists the DNS records
   to add and which are still missing: an address record (a CNAME for a subdomain, an A record
   for a bare domain) and a TXT record at `_simple-host.<domain>` that proves the domain is
   theirs and stays in place. Relay exactly what it returns; never invent a target or value.
3. Ask where the domain's DNS is managed (usually where they bought it) and give both records in
   plain terms: type, name (just the part before their domain), value, lowest TTL. Leave other
   records, especially email (MX), alone. Never ask them to change nameservers. Registrar
   screens: `references/registrars.md`.
4. Check with `get_site` after they say the records are saved, and again a few minutes later. The
   domain reads `waiting` while records are missing (each record shows whether it was found),
   `certificate` once DNS is right and HTTPS is being issued (usually minutes; their part is
   done), and `live`. A record found but wrong usually means an old parking record or an `AAAA`
   record pointing elsewhere: compare with what you gave them.
5. When live, give `https://<their domain>/`.

`domain: ""` removes it (ask first; links to it stop working, the site keeps its own address).
A domain another account has already proven is refused: say so.

## One domain for all sites

`update_account` with `domain: "trips.example.com"` makes every site of the account (a team's
with `owner`) answer at `https://<site>.trips.example.com/`, new sites too. It needs a wildcard
record (`*.trips`) plus the TXT record, which the answer lists; at Cloudflare keep the wildcard
**DNS only**. Ask first: it applies to every site.

## Sign-in on a domain

Visitor sign-in belongs to the address the visitor is on. Pages that sign people in or save data
set `window.SH_CONFIG = { site: "<site>" }` before the helper script (see `website-deploy`).
