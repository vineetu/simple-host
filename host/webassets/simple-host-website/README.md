# Simple Host skills

Publish and manage websites on simple-host.app from your AI app or coding agent.

## Install

- Coding agents (Claude Code, Codex, Cursor and others): `npx skills add vineetu/simple-host`
- Chat apps (ChatGPT, Claude, Grok): add the connector `https://simple-host.app/mcp`, sign in,
  then choose **Allow**
- Claude Code plugin: the `simple-host` plugin (skills plus the connector)

The steps for each app are on https://simple-host.app/install. Restart your agent after
installing.

## Skills

- `website-deploy`: the tools and when to use each: publish, saved data and rules, visitor
  sign-in, who may open a site, short name or own domain, versions, teams, REST fallback.
- `website-deploy-builder`: plan what to build before building it.
- `connect-domain`: a short name or the person's own domain, with the DNS steps.

## What Simple Host is for

Static pages (HTML, CSS, JS, images, a framework's built output) with saved data the page reads
and writes: forms, bookings, guestbooks, client portals, galleries. Each site lives at its own
address (always give the `url` a tool returns). It does not run server code.

Try it: tell your AI "Put a one-page site for my bakery online", then "Add a contact form".

`mcp-server/` and `setup.sh` are the old local stdio server; the connector replaces them.
