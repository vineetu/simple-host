---
name: website-deploy-builder
description: Decide what to build on Simple Host before building it. Use when the person has an idea for a website or web tool but has not settled what it should do, asks whether Simple Host can handle something (accounts, payments, a database, private data, server code), or describes a feature and needs it mapped to what a static site with a light backend can do. Checks the fit, picks the pattern (static page, shared state, collections, localStorage, public APIs, own domain), says plainly what is public, then hands off to website-deploy.
---

<!-- Derived from simple-host-website/skills/website-deploy-builder/SKILL.md. Keep in step. -->

# Plan a website on Simple Host

Use this to turn an idea into a concrete plan, then build it with the `website-deploy` skill.
The person's own explicit instructions take priority over this guidance. Keep planning short:
if the idea is clear, go straight to building.

## What a Simple Host site can be

- **Static files**: HTML, CSS, JS, images, fonts, served at
  `sites.simple-host.app/<handle>/<site>/`, or on the person's own domain. Any number of pages.
- **Shared state**: one small JSON document per site (about 1 MB) with atomic ops. Counters,
  vote tallies, settings, a short list.
- **Collections**: append-only lists, one item per submission, newest first. RSVPs, sign-ups,
  survey responses, orders, guestbook entries.
- **Per-visitor storage** in the browser (`localStorage`, IndexedDB): drafts, carts, settings,
  game saves, a private journal on one device.
- **Public APIs** called from the page with `fetch()` (weather, maps, open data), when the API
  allows browser requests and needs no secret key.

Everything a site shows and saves can be read by anyone with the link. There are no private or
password-protected pages. Visitors may be asked to sign in before saving (Google or an emailed
code); that ties a save to a person, it does not make anything private.

## Not a fit

Say so plainly, then offer the part that does fit:

- Server code, scheduled jobs, sending email or texts, webhooks.
- A private database, per-user accounts with private data, anything confidential (medical,
  financial, IDs).
- Taking card payments on the page. (A shop can take orders and the owner confirms and bills
  separately, or link out to a payment page the owner already has.)
- Calling APIs that need a secret key. A key in a page is public.

For those, suggest keeping Simple Host as the front end and a separate service for the part
that needs a server.

## Map the idea

| The person wants | Build |
|---|---|
| Landing page, portfolio, CV, menu, event info | static pages |
| RSVP, waitlist, sign-up, contact form | collection + live count in state + results page |
| Survey or quiz with answers collected | collection `responses` + `results.html` aggregating them |
| Poll, votes, likes, counter | state with `inc` (remember "already voted" in `localStorage`) |
| Guestbook, wall of messages | collection, listed newest first on the page |
| Small shop | product list in the page, cart in `localStorage`, orders collection, orders page |
| Calculator, game, drawing tool, planner | static + `localStorage` |
| Dashboard from public data | static + `fetch()` to a public API |
| Report from a spreadsheet or export | the data as a `.json` or `.csv` file in the site, rendered in the page |
| Their own address (brand.com, rsvp.brand.com) | build the site, then the `connect-domain` skill |

## Say these up front

- Anything that collects data gets a page that shows what was collected. Plan it in; the person
  rarely asks for it.
- What visitors save is public. If the list itself should not be seen by strangers (guest list,
  survey answers, orders), say that it can be read by anyone who finds the site's address; a
  site on its own domain is the step toward keeping it closer, and even then it is not a
  private database. Do not promise privacy. Suggest collecting only what is needed.
- An admin page is a convenience view anyone with its address can open, not a locked page.

## Hand off

Confirm the plan in two or three sentences (pages, what is saved where, the results page),
then build it with `website-deploy`. For an existing site, read its files first and change only
what the plan needs.
