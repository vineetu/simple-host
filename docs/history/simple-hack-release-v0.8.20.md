# Simple Hack standalone v0.8.20

Status: published, verified, and pinned on 2026-11-11.

Carries everything on main since hack-v0.8.17: the redesigned Simple Hack web
app (Home runs the event, with a checklist per role and stage), organisers
opting in to judge, prizes per rank, event page Looks, self-updating custom
event websites, the organiser Ask assistant (off on self-hosted installs until
an endpoint is set), no active-event limit by default on self-hosted installs
(SIM-435), and these install fixes found by a clean install of hack-v0.8.18:

- `.env.example` pinned `simple-hack:0.8.0`, so following the guide installed
  v0.8.0. It now leaves the image unset, and an empty value uses the release
  default.
- The images had no time zone data, so any event time zone other than UTC was
  refused. The binary now embeds it.
- `SUPPORT_EMAIL`, `PRIVACY_URL` and `ASSISTANT_*` pass from the config file
  through Compose and survive upgrades.
- Re-running the installer with the original config (empty secret lines) was
  refused; empty secrets now keep the generated values.
- The guide gained a download step, password accounts and `/admin` in place of
  email sign-in, an optional settings table and an upgrade download step.

hack-v0.8.18 (1b02774c) and hack-v0.8.19 (8a532641) were published on the way;
use v0.8.20. Annotated tag `hack-v0.8.20` points at 6851b9f4. The ZIP, tar.gz
and `simple-hack-0.1.0.tgz` match SHA256SUMS; `simple-hack:0.8.20` is published
for linux/amd64 and linux/arm64 and reports `v0.8.20 (commit 6851b9f)`.

Checked in local Docker Compose on the release host (no DigitalOcean token),
with `--tls internal`, loopback `HTTP_BIND`/`HTTPS_BIND` and a CONNECT proxy in
place of public DNS:

- Fresh install from the downloaded archive following the guide: 93
  migrations through `0014-hack-overall-prizes.sql`, `/healthz` and `/readyz`
  200, `/internal/tls-ask` 404, about 18 seconds with images cached.
- A whole event in Chromium with password accounts and no email: event in
  America/Los_Angeles, two tracks, sign-up, two participants on one team, a
  6-of-6 entry, a team site published from a zip, submit, a judge from the
  judge link, Entries closed, Judging, a 16/20 score, Results, Publish results,
  and the public page showing the winner.
- No page links to simple-hack.app or simple-host.app; connect steps show only
  the instance's own `/mcp`; Ask is hidden and its API answers `assistant_off`;
  `SUPPORT_EMAIL` and `PRIVACY_URL` set through the config file and a
  `--config` re-run show on the privacy page and landing footer.
- Upgrade: a published v0.8.17 install with an account and an event, upgraded
  with `upgrade.sh --image ghcr.io/vineetu/simple-hack:0.8.20`, applied 10
  migrations; the account signs in and the event page serves.
- Running: app about 35-100 MB and Postgres about 45-60 MB of memory; images
  about 0.7 GB, data a few MB.

Open (SIM-434): the event page footer ignores `PRIVACY_URL`; the built-in
privacy page and the admin sign-in text read oddly on a Hack-only install.
