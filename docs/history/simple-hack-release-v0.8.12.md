# Simple Hack standalone v0.8.12

Status: published, verified, and pinned on 2026-10-24.

Security patch release: builds with Go 1.27.3 and newer `golang.org/x/net`
(0.61.0) and `x/image` (0.47.0), clearing the `crypto/x509`, `crypto/tls`,
`net/http`, HTTP/2 and WebP advisories govulncheck reported against Go 1.27.1.
No behavior change. Annotated tag hack-v0.8.12 points at 341ecc20, the commit
built and published. The workflow built both images from the tag and
published the [release](https://github.com/vineetu/simple-host/releases/tag/hack-v0.8.12).

The ZIP and tar.gz match SHA256SUMS. `simple-hack:0.8.12` is published for
linux/arm64 and linux/amd64; the published image's binary reports `go1.27.3`
(checked by extracting `/usr/local/bin/simple-host` with `docker create` +
`docker cp` and running `go version -m` on it).

Checked on a throwaway DigitalOcean droplet (sfo2, `s-1vcpu-2gb`, a
`<ip>.nip.io` name, a Let's Encrypt certificate), deleted afterwards with its
SSH key: a published hack-v0.8.11 install with an event, an admin-created
participant, a team and a published team site created directly through the
API, upgraded with `upgrade.sh --image ghcr.io/vineetu/simple-hack:0.8.12`.
No new migrations (none shipped in this release); the event, its participant
count, the team and the published team site's content were all kept, along
with the admin key. `/healthz`, the home page and
`/.well-known/mcp/server-card.json` all answered 200, and
`docker compose config` parsed.

Not tested end to end on this release (unchanged from hack-v0.8.11, where it
was last exercised): the Hack `/mcp` connector, the MCP Apps screens and MCP
Events/webhook delivery, which have unit tests but were not exercised live on
this droplet. Installer pins changed only after this check passed.
