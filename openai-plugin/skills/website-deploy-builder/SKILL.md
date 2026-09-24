---
name: website-deploy-builder
description: Decide what to build on Simple Host before building it. Use when the person has an idea for a website or web tool but has not settled what it should do, asks whether Simple Host can handle something (accounts, payments, a database, private data, server code), or describes a feature and needs it mapped to what a static site with a light backend can do. Checks the fit, picks the pattern (static page, shared state, collections, private collections, localStorage, public APIs, own address), says plainly what is public and what is owner-only, then hands off to website-deploy.
---

<!-- Derived from simple-host-website/skills/website-deploy-builder/SKILL.md. Keep in step. -->

# Plan a website on Simple Host

Use this to turn an idea into a concrete plan, then build it with the `website-deploy` skill.
The person's own explicit instructions take priority over this guidance. Keep planning short:
if the idea is clear, go straight to building.

## What a Simple Host site can be

- **Static files**: HTML, CSS, JS, images, fonts, served at
  `sites.simple-host.app/<handle>/<site>/`, or on the site's own address: a free
  `<name>.simple-host.app` or the person's own domain. Any number of pages.
- **Shared state**: one small JSON document per site (about 1 MB) with atomic ops. Counters,
  vote tallies, settings, a short list.
- **Collections**: append-only lists, one item per submission, newest first. RSVPs, sign-ups,
  survey responses, orders, guestbook entries. On a site with its own address, a collection
  can be made **private**: signed-in visitors add to it, and only the site owner — and the
  Simple Host operator, for moderation — can read it. The owner can mark items done or delete
  them; public lists are append-only.
- **Per-visitor storage** in the browser (`localStorage`, IndexedDB): drafts, carts, settings,
  game saves, a private journal on one device.
- **Public APIs** called from the page with `fetch()` (weather, maps, open data), when the API
  allows browser requests and needs no secret key.

Pages are public: anyone with the link can open them, and there are no password-protected
pages. Saved data is public too, except a private collection. Visitors may be asked to sign in
before saving (Google or an emailed code); that ties a save to a person.

## Not a fit

Say so plainly, then offer the part that does fit:

- Server code, scheduled jobs, sending email or texts, webhooks.
- Per-user accounts where visitors see their own private data, anything confidential
  (medical, financial, IDs). (Owner-only lists of submissions do fit: private collections.)
- Taking card payments on the page. (A shop can take orders and the owner confirms and bills
  separately, or link out to a payment page the owner already has.)
- Calling APIs that need a secret key. A key in a page is public.

For those, suggest keeping Simple Host as the front end and a separate service for the part
that needs a server.

## Map the idea

| The person wants | Build |
|---|---|
| Landing page, portfolio, CV, menu, event info | static pages |
| RSVP, waitlist, sign-up, contact form | own address + private collection + owner admin page; a plain count in state if wanted |
| Survey or quiz with answers collected | collection `responses` + `results.html` aggregating them (private and owner-only if answers are personal) |
| Poll, votes, likes, counter | state with `inc` (remember "already voted" in `localStorage`) |
| Guestbook, wall of messages | public collection, listed newest first on the page |
| Small shop | product list in the page, cart in `localStorage`, own address + private `orders` collection + owner `orders.html` |
| Calculator, game, drawing tool, planner | static + `localStorage` |
| Dashboard from public data | static + `fetch()` to a public API |
| Report from a spreadsheet or export | the data as a `.json` or `.csv` file in the site, rendered in the page |
| Its own address | free `<name>.simple-host.app` (one `connect_domain` call), or their domain via the `connect-domain` skill |

## Say these up front

- Anything that collects data gets a page that shows what was collected. Plan it in; the person
  rarely asks for it.
- **Anything personal** (orders, RSVPs, survey answers, sign-ups; names, emails, phone numbers,
  addresses): plan the site's own address first, offering the free `<name>.simple-host.app`
  before their own domain. Then a private collection, set before the form goes live, and an
  owner admin page that shows the list only to the owner signed in.
- **On the shared address**, do not collect personal details. Suggest an email-order flow (a
  `mailto:` link) or claiming the free address.
- Public lists stay public: guestbook, votes, public comments. Say so plainly.
- Pages are always public. Only a private collection is owner-only. Suggest collecting only what
  is needed.

## Hand off

Confirm the plan in two or three sentences (pages, the address, what is saved where and whether
it is private, the results or admin page),
then build it with `website-deploy`. For an existing site, read its files first and change only
what the plan needs.
