Listing images for the website and the demo recording, not for the plugin package: the
portal accepts screenshots only when the MCP server returns UI (`screenshots_not_allowed`),
and Simple Hack returns none to ChatGPT. Each is 1280 by 800, light theme.

- event-page.png: the public page of Harbour Build Day (`/e/harbour-build-day`), signed out.
- organiser-overview.png: the organiser's Home for that event (`/e/harbour-build-day/manage?s=overview`).
- team-home.png: a participant's Overview for team Tide Tables (`/e/harbour-build-day/team?s=home`).
- judge-home.png: a judge's Overview for Winter Build Night, in Judging (`/e/mcpjam-judging/judge?s=home`).

Made on 2026-11-21 from a local fixture (`PRODUCT=hack scripts/mcpjam/fixture.sh`, then a
Harbour Build Day event added through the connector tools, the two fixture events retitled
Riverside Code Jam and Winter Build Night, the organiser named Ada Example), with Playwright
and Chromium at 1280 by 800. The join link in organiser-overview.png belongs to that throwaway
local server. All names and data are fictional.
