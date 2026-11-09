# API reference

Read the whole contract as [OpenAPI JSON](/openapi.json) or [OpenAPI YAML](/openapi.yaml); the areas below link to the same operations as readable pages.

| Area | Description | Operations |
| --- | --- | --- |
| [Discovery](/docs/api/discovery/) | Public product, connector, and API discovery. | 9 |
| [Auth](/docs/api/auth/) | Account sign-in by emailed code or Google, for Simple Host account owners and the agents working for them. It ends in an API key. It is never for a site's visitors: these routes answer only on the app's own address (403 account_auth_unavailable elsewhere), and an API key never belongs in a page. A page signs its visitors in with the Visitor sign-in routes through auth.js. | 9 |
| [Account](/docs/api/account/) | Account profile, credentials and connected apps. | 26 |
| [Sites](/docs/api/sites/) | List, inspect and manage websites. | 22 |
| [Deploy](/docs/api/deploy/) | Publish inline JSON files or a tar.gz/zip archive. | 12 |
| [Versions](/docs/api/versions/) | Inspect retained deployments, preview and roll back. | 6 |
| [Analytics](/docs/api/analytics/) | Owner-scoped website traffic. | 4 |
| [Custom domains](/docs/api/custom-domains/) | Claim a free address or connect a custom domain. | 4 |
| [Address families](/docs/api/address-families/) | Connect a wildcard address family for your sites. | 6 |
| [Storage resources](/docs/api/storage-resources/) | Owner-declared KV, SQLite and durable file resources. | 23 |
| [Notifications](/docs/api/notifications/) | Signed HTTPS notifications for visitor storage writes on Simple Host. | 18 |
| [Visitor sign-in](/docs/api/visitor-sign-in/) | Site-scoped browser sessions for website visitors (customers, guests, members), on the site's own address, through https://simple-host.app/auth.js (SH.mount, SH.requireSignIn, SH.me, SH.signOut). The only sign-in a page ever uses. Never returns an API key. | 20 |
| [Skills](/docs/api/skills/) | Discover and fetch this server’s agent skills and references. | 4 |
| [Showcase](/docs/api/showcase/) | Public feed of a person’s showcased sites. | 1 |
| [Health](/docs/api/health/) | Service health and readiness. | 2 |
