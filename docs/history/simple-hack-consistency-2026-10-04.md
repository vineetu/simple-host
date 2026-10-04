# Simple Hack consistency audit — 2026-10-04

Status: implementation and published v0.8.2 verified; final copy patch and production checks pending.

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
