# Simple Hack standalone v0.8.14

Status: published and verified on 2026-11-02.

Carries everything on main since hack-v0.8.13: Simple Hack on Kubernetes
(one image, the `deploy/kubernetes/simple-hack` Helm chart, rendered
manifests, `docs/kubernetes.md`, `simple-host migrate -init`,
`MIGRATE_ON_START`, `TRUSTED_PROXY_CIDRS`); a schedule with fixed rows for the
real deadlines and organiser prompts; notifications ("Needs you" on Home,
badges, email updates with an Account opt-out); the entries journey (entry
summary card, rules, submit and unsubmit, participant checklist and countdown,
the judge's one screen with click-to-load video and framed team sites, guided
site publishing, result cards, deadline reminder emails); the teams redesign;
and the fixes from the 2026-11-02 full production test, including gating
judges' team-site preview links. Annotated tag hack-v0.8.14 points at
65c7faa3, the commit built and published. The workflow built both images from
the tag and published the
[release](https://github.com/vineetu/simple-host/releases/tag/hack-v0.8.14),
with the Helm chart `simple-hack-0.1.0.tgz` attached for the first time.

The ZIP and tar.gz match SHA256SUMS. The published `simple-hack:0.8.14`
image's binary reports `v0.8.14 (commit 65c7faa)`.

As for v0.8.13, no DigitalOcean token was configured on the release host, so
the release was checked in local Docker containers: a fresh install of
`hack-0.8.13` with Postgres, accounts created, then an upgrade with the
0.8.14 image's `migrate` (11 migrations applied cleanly), restart on 0.8.14,
accounts intact and `/healthz` OK. A full event flow was not exercised in the
container; production simple-hack.app has run this code since the same day
and passed a full browser test.
