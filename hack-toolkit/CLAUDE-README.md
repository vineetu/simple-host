# Simple Hack for Claude

Run a hackathon on [simple-hack.app](https://simple-hack.app/). Five bundled skills cover organising (`run-hackathon`), participating (`join-hackathon`), judging (`judge-hackathon`), building a site (`website-deploy-builder`), and publishing it (`website-deploy`). The `.mcp.json` connector points to the hosted Simple Hack MCP server. Sign in through Claude's connection flow; one personal connection works across event roles. A participant selects a current team for site publishing.

The connector covers organiser, participant and judge actions alongside team-site publishing. Its calls go to `simple-hack.app`, where the person's current event role and team membership are checked on each request. The run-hackathon skill keeps optional private-instance guidance in a separate reference.

The package contains no credentials. Support is [support@simple-host.app](mailto:support@simple-host.app). This updated package is locally prepared; its expanded connector and skill workflows must be deployed and checked before release. The public marketplace source is [vineetu/simple-hack-plugin](https://github.com/vineetu/simple-hack-plugin); no new marketplace or directory release is claimed.

For Claude Code, add the marketplace with `/plugin marketplace add vineetu/simple-hack-plugin`, then install with `/plugin install simple-hack@simple-hack-marketplace` when this version is published. The local ZIP can be inspected independently; the public marketplace still serves the previous release until the owner publishes an update.
