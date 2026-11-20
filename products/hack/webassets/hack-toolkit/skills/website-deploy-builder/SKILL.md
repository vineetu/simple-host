---
name: website-deploy-builder
description: Plan a static Simple Hack team site or custom event website, including layout, data model, storage rules and per-person privacy, then hand implementation to website-deploy.
---

# Plan a Simple Hack website

Needs the Simple Hack connector, `https://simple-hack.app/mcp` (setup steps are in `website-deploy`). Never ask for or handle a sign-in code, key, password or passcode in chat, and never plan to put a credential in site HTML, localStorage or a shared document.

Use this for a team's site or an organiser's custom event website on simple-hack.app. Ask what the site should help visitors do, who updates it, what it shows publicly, whether it collects personal data, and when the deadline is. Make a focused plan: pages, content, mobile layout, assets, data and a testable first release. Hand implementation to `website-deploy`.

A team site belongs to one team in one event; publishing depends on membership, stage and deadline. A custom event website reads the public event feed, while joining, judging and management stay on the Simple Hack apex. Sites are static (HTML, CSS, JS, assets); no server-side code. The first publication makes the site open to anyone with its URL.

For saved data plan KV for key-based documents, SQLite for records and queries, and file resources for images and downloads. Each resource has row rules: who can read, add, edit and delete which rows (see website-deploy's storage reference). For per-person privacy, read with `visitor_id == auth.id || auth.role == "owner"`; requiring sign-in alone shares rows among everyone signed in. "Owner" is the team on a team site (until its deadline) and the organisers on an event website (until the event ends). Plan phone-photo resizing.

If the site collects entries, plan how the owner reviews them. Never imply an unlinked page or a client-side password protects data. Rule changes, deletion, rollback, take-down and letting people who are not signed in write all need explicit confirmation.
