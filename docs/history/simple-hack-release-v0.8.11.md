# Simple Hack standalone v0.8.11

Status: published, verified, and pinned on 2026-10-24.

Server and agent cards, discovery, and MCP Apps/Events for Hack (part of the
2026-10-24 wave: Hack discovery headers and resource URNs, auth.md describing
configured sign-in, opt-in MCP Apps My sites and dashboard for selected teams
and organiser event websites, and MCP Events with signed webhooks for team and
organiser sites). Annotated tag hack-v0.8.11 points at 4869c247, the commit
built and published. The workflow built both images from the tag and published
the [release](https://github.com/vineetu/simple-host/releases/tag/hack-v0.8.11).

The ZIP and tar.gz match SHA256SUMS. `simple-hack:0.8.11` is published for
linux/arm64 and linux/amd64; both report v0.8.11 at commit 4869c24.

Checks on two throwaway DigitalOcean droplets in sfo2 (`s-1vcpu-2gb`,
`<ip>.nip.io` names, Let's Encrypt certificates), deleted afterwards with their
SSH key:

- Fresh install: the published 0.8.11 package, with a placeholder Resend key
  (email-code sign-in not exercised) and no company sign-in. 61 migrations
  recorded. `/healthz`, the home page, `/.well-known/mcp/server-card.json` and
  `/auth.md` all answered 200, and `docker compose config` parsed. The admin
  API key bootstraps test accounts (`POST /v1/admin/users`), which this check
  used instead of real email delivery.
- Upgrade: the published 0.8.10 package on a real Let's Encrypt certificate,
  with an event, an admin-created participant account, a team, a team key and
  a published team site created directly through the API before upgrading.
  `upgrade.sh --image ghcr.io/vineetu/simple-hack:0.8.11` applied the three new
  migrations (`zh-site-notifications.sql`, `zi-site-search-summary.sql`,
  `zj-hack-notifications.sql`) and kept the event, its stage, the team, the
  team key and the published team site's content, along with the admin key.

Not tested end to end: the Hack `/mcp` connector, which refuses a plain
X-API-Key (`sign_in_required`) and needs the full OAuth/email or company
sign-in flow; the MCP Apps screens and MCP Events/webhook delivery, which have
unit tests but were not exercised live on these two droplets. Installer pins
changed only after these checks passed.
