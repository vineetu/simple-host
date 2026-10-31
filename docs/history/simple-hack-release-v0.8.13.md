# Simple Hack standalone v0.8.13

Status: published, verified, and pinned on 2026-10-31.

Carries everything on main since hack-v0.8.12: the new default Simple Hack
web app (`static/hack`, with `?ui=classic` for the earlier screens), private
team finding with join requests, invites, solo listing and organiser fill
(SIM-225), announcement edit and delete, zip-upload and
personal-data-minimisation fixes in Hack MCP, the organiser Event page split
into sub-pages with a collapsible sidebar and walkthroughs, and applying to a
hackathon without a join link (`hack_preview_application`/
`hack_apply_to_event`, `EVENT_APPLY_PENDING_PER_EVENT`). Annotated tag
hack-v0.8.13 points at 3a097ad5, the commit built and published. The
workflow built both images from the tag and published the
[release](https://github.com/vineetu/simple-host/releases/tag/hack-v0.8.13).

The ZIP and tar.gz match SHA256SUMS. `simple-hack:0.8.13` is published for
linux/arm64 and linux/amd64; the published image's binary reports
`v0.8.13 (commit 3a097ad)`.

No DigitalOcean token was configured on the release host, so this release
was checked in local Docker containers instead of on a throwaway droplet
(the owner's standing instruction for this case: verify with a local
container/compose fresh install plus an upgrade). Against the published
`ghcr.io/vineetu/simple-host:hack-0.8.12` and `hack-0.8.13` images, with a
throwaway Postgres container and `EVENTS=hosted` (the same configuration
`deploy/hack/standalone/start.sh` sets):

- **Upgrade.** On hack-v0.8.12: bootstrapped an organiser account and a
  participant account through `POST /v1/admin/users` (no email delivery
  needed), created an event, opened it for joining, joined the participant
  by its join code, created a team, minted a team key (`POST .../key`) and
  published the team's site through it — hack-v0.8.12 predates the
  `PUT .../site` shortcut, so this uses the same site-files route a
  small-box site uses. Stopped the app, kept the database and sites
  volumes, then ran `simple-host migrate` from the hack-v0.8.13 image: it
  applied the 7 new migrations (`zk-hack-directory-opt-in.sql`,
  `zk-hack-team-removal-notice.sql`, `zl-hack-team-finding.sql`,
  `zn-sso-hop.sql`, `zn-visitor-ai.sql`, `zo-sso-signout-confirm.sql`,
  `zp-hack-applications.sql`) once. Restarted on hack-v0.8.13: the event,
  the team and the published team site (content and `live_version`) were
  all kept, and the running binary reports `v0.8.13 (commit 3a097ad)`.
- Both the small-box and the Hack images' `/healthz` answered 200 on
  startup, and `docker compose config` parses for `compose.yaml`,
  `deploy/platforms/coolify/compose.yaml` and
  `deploy/hack/standalone/compose.yaml` once their required variables are
  set.

Not tested in this pass: the Hack `/mcp` connector, MCP Apps screens and MCP
Events/webhook delivery (unit-tested, not exercised live here), and the
Packer-built 1-Click DigitalOcean snapshot and marketplace image (needs a
real DigitalOcean account; only the installer's own pins were updated, not
re-verified against a fresh Packer build). Installer pins changed only
after the checks above passed.
