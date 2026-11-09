# API reference

Read the whole contract as [OpenAPI JSON](/openapi.json) or [OpenAPI YAML](/openapi.yaml); the areas below link to the same operations as readable pages.

| Area | Description | Operations |
| --- | --- | --- |
| [Discovery](/docs/api/discovery.md) | Public product, connector, and API discovery. | 9 |
| [Auth](/docs/api/auth.md) | Account sign-in by emailed code or Google, for Simple Host account owners and the agents working for them. It ends in an API key. It is never for a site's visitors: these routes answer only on the app's own address (403 account_auth_unavailable elsewhere), and an API key never belongs in a page. A page signs its visitors in with the Visitor sign-in routes through auth.js. | 9 |
| [Account](/docs/api/account.md) | Account profile, credentials and connected apps. | 20 |
| [Sites](/docs/api/sites.md) | List, inspect and manage websites. | 11 |
| [Deploy](/docs/api/deploy.md) | Publish inline JSON files or a tar.gz/zip archive. | 6 |
| [Versions](/docs/api/versions.md) | Inspect retained deployments, preview and roll back. | 6 |
| [Storage resources](/docs/api/storage-resources.md) | Owner-declared KV, SQLite and durable file resources. | 23 |
| [Notifications](/docs/api/notifications.md) | Signed HTTPS notifications for visitor storage writes on Simple Host and Simple Hack. | 15 |
| [Visitor sign-in](/docs/api/visitor-sign-in.md) | Site-scoped browser sessions for website visitors (customers, guests, members), on the site's own address, through https://simple-host.app/auth.js (SH.mount, SH.requireSignIn, SH.me, SH.signOut). The only sign-in a page ever uses. Never returns an API key. | 17 |
| [Skills](/docs/api/skills.md) | Discover and fetch this server’s agent skills and references. | 4 |
| [Health](/docs/api/health.md) | Service health and readiness. | 2 |
| [Hosted events](/docs/api/hosted-events.md) | Hosted events, teams, entries, judging, results and event websites. | 149 |
| [Events](/docs/api/events.md) | Hostname claims for self-hosted events. | 3 |
