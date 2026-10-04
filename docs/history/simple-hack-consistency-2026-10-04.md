# Simple Hack consistency audit — 2026-10-04

Status: final v0.8.4 shipped and verified on both services; all fixtures removed.

The audit uses the latest origin/main in its own worktree. Production uses
throwaway organiser, participant and judge accounts plus one disposable event.
The native full workflow uses the real handlers and a local email sink in a
throwaway Docker Postgres, on a database separate from `make check`.

Confirmed inconsistencies fixed:

- Root email/Google returns reached the landing film rather than the existing
  sign-in shell. Root token/nonce URLs now redirect locally to `/signin` with
  the query intact. Wrong-device links show the existing friendly message.
- The film home lacked the shared header and used a separate footer. It now
  has the same navigation and account/theme controls as other Hack pages.
- Film scene lighting kept the returning home light in dark mode. Scene
  lighting stays in the film; the home follows the shared theme.
- Shared footer Privacy/Terms links left Hack, unlike the film. They now stay
  on the instance. The film logo no longer opens another landing tab; How it
  works replays the film. Non-judging site links stay in the current tab.
- The FAQ said only winners are public by default. Published ranks and overall
  scores are public by default; both can be hidden.
- Current product prose now uses organiser, entry, team site and judge link.
  API field/route/tool identifiers and historical release evidence keep their
  names. The film's words are unchanged.
- Receipt emails now open the participant's team page. Site-publishing scope
  errors direct people to select their team in the connector.
- Standalone presentation, API/llms text, skills/ZIPs and connector instructions
  named simple-hack.app. They now use the configured instance URL; intentional
  hosted-product and upstream documentation links remain external. Get started
  uses instance skill downloads, and the old install page redirects there.
- Go-built take-down/unavailable/sign-in-error pages now get the shared Hack
  theme and navigation. Simple Host's status pages retain their behavior.
- The Host hackathons page advertised the removed organiser skill self-hosting
  and key-in-chat onboarding. It now describes the full platform and links
  the standalone, DigitalOcean and Coolify guides, with an actual screenshot.

Hack skills 0.27.19 and toolkit 0.2.7 carry the updated current copy. Reviewed
0.2.6 archives remain unchanged on the hosted service. Self-hosted text and
ZIPs are rewritten at serving time; binary image/font files are unchanged.
Defaults remain 25 MB of deployed assets and two retained versions, with a
separate 1,000,000-byte website storage pool.

Review of the return-route and connector-text changes: the redirect target is
fixed to the local `/signin`; it neither consumes nor validates a credential.
The existing one-time-token and browser-nonce verifier still controls sign-in,
and missing/wrong-device nonces remain refused. MCP changes only the instruction
text using configured APIHost, leaving OAuth, caller identity, tool inventory,
permissions and REST dispatch unchanged. No permission boundary changed.

Browser evidence is in `/tmp/hack-consistency-before` and
`/tmp/hack-consistency-after`: signed-out pages and film controls, all organiser
tabs, participant/team states, judging/scoring, voting, results, dialogs,
email-link returns, Back and Get started copying/fallbacks at 320, 390 and
1280 px in both themes. Site status/error documents use the same theme.
Simple Host baseline screenshots cover home, dashboard and Get started at all
three widths in both themes; its index/site.css byte regression stays in place.

Published v0.8.2 at `7e2ca4e` passed anonymous download/checksum/ZIP-tar parity,
amd64/arm64 image pulls (native arm64 execution, amd64 ELF/commit inspection),
a fresh 50-migration install and an actual v0.8.1 package/image upgrade from
44 to 50 migrations. Both runs retained domain, credentials, event settings,
membership, entry, team key, site files and the local TLS CA. Published-image
browser checks passed at all six width/theme combinations, with the instance
on `hack-package.test`; the fresh run also passed real local Traefik routing
for the supplied Coolify Compose template. No new cloud deployment or external
email delivery is claimed. A final text scan found a closed-entry message and
download filename plus stale reference wording; the next patch includes them
rather than changing the published tag.

Final release v0.8.3 at `b76d249` repeats the anonymous download/checksum and
multi-architecture image checks, fresh installation and an actual previous
v0.8.2 package/image upgrade, retaining the same data with 50 migrations.
Published-image browser checks pass in all six width/theme combinations;
local Coolify Traefik routing passes. Installer defaults were changed only
after these published-artifact checks. Toolkit 0.2.7 is live at its existing
address, with all four previous 0.2.6 ZIPs byte-identical to their source.

Production deploy on 2026-10-04 held `/tmp/simple-host-deploy.lock`, rebased and
fast-forward pushed main, built under MemoryMax=2G with version/commit ldflags,
backed up and installed the binary, and restarted both services. The running
binary reports `hack-v0.8.3-1-gf0bc750`, commit `f0bc750`, with 50 migrations.
Both health and readiness endpoints, the site/person/legacy addresses and the
neighbours passed. Five binary backups are retained; rollback protection stayed
active through the browser checks and was not needed.

Live verification passed 72 signed-out checks and film controls, 162 role checks,
all nine Get started copies and both clipboard fallbacks in all six width/theme
combinations, and all 25 crawled internal links. The real throwaway team site
published and served over public HTTPS. Phone judging opened the frozen site,
saved all four scores and a comment; the organiser locked/published, with public
results and private team feedback checked. Real event take-down, missing
join/judge links and root sign-in returns passed at all six combinations.
All 18 Simple Host home/dashboard/install screenshots are byte-identical to
the baseline. Its index, site.css and install source files are unchanged;
only the requested Hack-related content on `/hackathons` changed.

The disposable live event and all three accounts were deleted after verification.
The throwaway Postgres and every release/local test server, container, network
and volume were removed. `make check` passed with DB_DSN on the throwaway Docker
Postgres; standalone configuration/upgrade tests and the updated DigitalOcean
Packer template validation also passed. No production database role was changed.

Remaining verification limits: the new release was tested locally on arm64,
with amd64 pull/ELF/commit inspection; no amd64 runtime was available on this
arm64 box. Fresh DigitalOcean/Coolify cloud deployments and external email
provider delivery were not repeated; the guides retain dated evidence and the
native browser workflow used a local email sink. The published package's
current instructions match v0.8.3; later verification evidence is recorded
here and in its release notes without changing published archives.

Assumptions: current product prose uses organiser, entry, team site and judge
link. Wire names, routes, immutable reviewed archives, historical evidence and
the approved story wording are retained. Intentional hosted-product, AI setup
and upstream documentation links remain external.

A final visual review of the actual take-down screenshot found that the event
hero and favicon requested the event icon endpoint after it became blocked.
The follow-up patch omits that hero image and uses the local platform favicon
on take-down pages; it does not change the blocked endpoint or permissions.

The icon patch's first full check exposed an existing archive-link test flake:
`export.go` gives generated JSON entries export-time mtimes, so raw tar bytes
can differ across a second boundary. The test now compares complete entry paths
and payload bytes while retaining the cross-team content and revocation checks;
archive production behavior is unchanged.

Final published v0.8.4 at `3c72f95` passes anonymous checksum/ZIP-tar parity,
image manifest/pull/architecture/commit checks, native fresh installation and an
actual v0.8.3 package/image upgrade. Data and TLS CA persist with 50 migrations.
The published-image browser now checks 42 states, including all six real
Take-down width/theme combinations, with no event-icon request or broken asset;
the fixture is restored before the local Coolify Traefik check. Installer pins
were changed only after those published-artifact checks.

The final production deploy runs `hack-v0.8.4-1-g2eb5226`, commit `2eb5226`,
with 50 migrations. It again held the lock, rebased/fast-forward pushed, built
under MemoryMax=2G with version/commit ldflags, backed up and restarted both
services, and retained rollback protection through every client check. Five
binary backups remain. Both services and all site/person/legacy/neighbour
checks pass. No rollback was needed.

Final live results: 72 signed-out/film checks and 162 organiser/participant/judge
checks pass; all nine Get started Copy buttons and both fallbacks pass at 320,
390 and 1280 px in both themes. All 25 internal links pass. Live team-site
publishing, frozen-site opening, four-criterion phone scoring, comment/save,
lock/publish and public-results/private-feedback checks pass. All six real
Take-down states use the local favicon with no blocked hero image or broken
image, and missing join/judge links and root sign-in returns retain the ink
navigation. Visual review confirms the corrected take-down state.
All 18 Simple Host baseline screenshots remain byte-identical.

`make check` passes with the final pinned source against the throwaway Docker
Postgres, including the corrected archive comparison; the two targeted
regressions pass five repetitions. Installer configuration/upgrade tests and
the v0.8.4 DigitalOcean Packer validation pass. The final live event and all
three accounts were deleted. The final test Postgres (including its anonymous
volume) and every local release-test container/network/volume were removed;
private fixture and anonymous registry-config files were removed as well.
The verification limits and intentional naming/film/wire exceptions above
still apply; no production Postgres role or Simple Host presentation changed.
