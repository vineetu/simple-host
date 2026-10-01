# Simple Hack for Claude

Run a hackathon on [simple-hack.app](https://simple-hack.app/). The bundled `run-hackathon` skill guides hosted event setup, team sites, judging, results, and an optional private instance. The `.mcp.json` connector points to the hosted Simple Hack MCP server. Sign in through Claude's connection flow and choose **Manage my events** for organiser work; a participant connects to their team separately for site publishing.

The organiser connection exposes ten tools to list events, check a name, create and read a draft, update page settings and stages, read or replace a rubric, and export judging scores or results. The connector sends tool arguments to `simple-hack.app`; event, team, judge and site data is returned only within the account and role's permissions. A team connection can publish only its selected team's site. Self-hosted setup in the skill may call the chosen cloud provider and DNS provider using the organiser's credentials.

The package contains no credentials. Support is [support@simple-host.app](mailto:support@simple-host.app). Public directory submission must wait for the Hack-specific privacy, terms and support pages and reviewer setup listed in `SUBMISSION.md` in the kit.
