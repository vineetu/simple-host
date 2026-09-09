# Hackathon instances — build plan

Written 2026-09-09. Nothing here is built unless marked otherwise.

The goal: an organiser with no server skills runs a hackathon on an instance they
own. Participants build with the AI agent they already have. Every entry is a
live link. We never hold anyone's credentials.

---

## Decisions already made

- **2026-09-09. Hackathons run on their own instance, but may use our domain.**
  Two different things. They cannot share our *server*, because the admin view is
  instance-wide and operator-only and there is no event concept in the schema. They
  can absolutely use our *domain*: `stanford-cs-2026.<our-domain>` and
  `sites.stanford-cs-2026.<our-domain>`, two A records pointing at their own box.
  Bringing their own domain stays optional and is what unlocks Resend and Google.
- **2026-09-09. Event subdomains use simple-host.app and agent-deploy.dev, both.**
  Let's Encrypt allows 50 certificates per registered domain per week and
  subdomains share that budget, but only 2 certificates are in use on
  simple-host.app and 1 on agent-deploy.dev, renewing roughly every 60 days. So
  each domain has effectively its full allowance free, giving about 100 a week
  across the two, or roughly 50 events. A single broken box cannot exhaust that:
  the duplicate-certificate limit caps it at 5 a week for the same names, and the
  Compose file keeps certificates on a persistent volume so restarts reuse them.
  **The skill must handle exhaustion explicitly**: detect the error, offer the
  other domain, and offer bringing their own. A box that silently serves browser
  warnings is the failure to avoid.
- **2026-09-09. The organiser's own agent provisions the box.** The setup skill
  runs on their machine with their cloud token. We never store a cloud credential.
- **2026-09-09. Thin skill, fat script.** The agent gets a token, creates a box,
  uploads one idempotent script, runs it, reports back. Every hard step lives in
  the script, which we test. Agent reliability falls as its decision count rises.
- **2026-09-09. Caddy, not nginx, on event boxes.** Certificates are obtained and
  renewed automatically, so there is no certbot and no renewal job to fail
  mid-event. Production simple-host.app stays on nginx.
- **2026-09-09. Docker Compose, three services, four volumes.** Measured overhead
  of the Docker runtime is about 65 MB plus roughly 10 MB per container, under a
  tenth of a 1 GB box. Compose is chosen for reproducibility across providers,
  not for elegance. Nothing ever compiles on the event box.
- **2026-09-09. Pre-issued keys are the primary sign-in for events, not a
  fallback.** Resend's free tier is 3,000 a month capped at 100 a day, which a
  100-person event exhausts on the first morning. Keys need no mail provider, no
  DNS records for SPF or DKIM, and no waiting for a code. The organiser has a
  roster anyway.
- **2026-09-09. No shared Google OAuth across instances.** Google requires exact
  redirect URIs with no wildcards and caps a client at 100 URIs. The only
  workaround is a central callback service, which would put us in the auth path
  of every event forever and contradicts "your data on your box".
- **2026-09-09. A free subdomain is fine; shared auth is not.** DNS is a
  setup-time dependency: two A records created once, and the event survives us
  disappearing. Shared auth is a runtime dependency. Take the first, refuse the
  second.
- **2026-09-09. Handles are mutable only while the account has zero sites.**
  With no sites there is no `handles/<handle>` symlink, so a rename is one column
  update. A separate display name is freely editable and never appears in a URL.
- **2026-09-09. Resend and Google require the organiser's own domain.** They hold
  the zone, so they add SPF, DKIM and the A records themselves. Organisers without
  a domain get our subdomain and run on pre-issued keys alone.
- **2026-09-09. Caddy on event boxes only; simple-host.app stays on nginx.**
  Only one program can own ports 80 and 443. This box runs 27 nginx vhosts, of
  which 11 are Simple Host and 16 belong to other projects. Switching Simple Host
  to Caddy here would force Caddy to become the front door and move all 16 others
  behind it, which is a migration of unrelated live services for no gain today.
  Revisit when custom domains become frequent enough to be annoying, or when
  Simple Host moves to a box of its own.
- **2026-09-09. No migrations needed for event boxes.** They are created on
  Friday and destroyed on Monday. Upgrades matter for the enterprise case only.

---

## 1. The blocker: served surfaces hardcode simple-host.app

Nothing downstream matters until this is fixed. A participant on an event box
would be told to publish to our public instance.

| File | Occurrences of `simple-host.app` |
|---|---|
| `internal/handler/static/install.html` | 12 |
| `internal/handler/static/llms.txt` | 3 |
| `internal/handler/static/auth.js` | 2 |
| `simple-host-website/skills/website-deploy/SKILL.md` | several |

**Work:** render these from `PUBLIC_BASE_URL` at serve time rather than shipping
the literal. The skill needs an instance setting so an agent can be pointed at
one box. Add a check to `check-docs-sync.sh` so a new hardcoded host fails the
gate.

## 2. Release pipeline

There are no tags, no releases, no CI and no workflows directory in the repo today.

- **On push and pull request:** run `make check`. Nothing runs it automatically today.
- **On tag:** cross-compile `linux/amd64` and `linux/arm64`, attach both to a
  GitHub Release, build a multi-arch image and push to ghcr.io.
- Runs on GitHub-hosted runners, free for public repos. **Never a self-hosted
  runner**: this repo is public, and a fork's pull request would then execute
  code on a box serving thirteen live services.
- Revise the Dockerfile to **copy the prebuilt binary** instead of compiling
  in-image. No cgo means both architectures cross-compile on one runner with no
  emulation.

**Already written, uncommitted:** `Dockerfile`, `compose.yaml`,
`deploy/compose/Caddyfile`, `.env.example`. Built and ran locally; image is 32 MB.

## 3. Event accounts

Today there is exactly one admin route, `GET /v1/admin/users`. No create, no delete.

- `POST /v1/admin/users` — batch create from a pasted roster, return handle and
  key for each, CSV out.
- `DELETE /v1/admin/users/{id}` — **must remove the user's directory as well as
  the row**. The foreign keys cascade in the database only; files on disk would
  be orphaned.
- Admin page: add, delete, view keys, export. Keys are stored in plain text, so
  re-showing one is free and solves "I lost my key" during an event.
- Add a **display name** column, freely editable, never in a URL.
- Handle rename while site count is zero. The count check and the update must be
  atomic, or a first deploy in the gap breaks a brand-new URL. Clean up a stale
  `handles/<old>` symlink if the account previously had a site.
- **Key-only sign-in screen.** When no provider is configured, do not render a
  sign-in form that cannot work. Ask for the key and keep it in `localStorage`,
  which the dashboard already does under `apiKey`. The server must expose which
  providers are enabled so the page can decide.

## 4. Optional sign-in, offered by the skill

**Decided 2026-09-09: Resend and Google are only for organisers who bring their
own domain.** That single rule is the simplification. Because they hold the zone,
they add every record themselves, and we are never in the middle of their mail or
their identity.

One question at setup decides everything after it:

| Own domain? | Address | Sign-in |
|---|---|---|
| No | free subdomain from us | pre-issued keys only |
| Yes | two A records they add themselves | keys, plus optional Resend and Google |

Reuse `simple-host-website/skills/connect-domain/references/registrars.md`, which
already walks people through adding records at Vercel, GoDaddy, Porkbun and
Cloudflare. Resend's SPF and DKIM are just more TXT records in the same place.

### Resend, own-domain only

1. Create an account at resend.com.
2. Add the domain and enter the SPF and DKIM TXT records it prints, at their registrar.
3. Create an API key.
4. Paste it into the skill, which writes `RESEND_API_KEY` and `MAIL_FROM` and restarts.

Free tier: 3,000 a month, 100 a day, one verified domain, and sending pauses at
the cap rather than charging. The organiser owns that limit and can raise it on a
paid plan if they want to. Keys still cover the opening rush with no email at all,
and Google removes the cap entirely, so this is a preference rather than a wall.

### Google, own-domain only — the last step of setup

Google has no daily send limit, so this is the option that removes the email cap
altogether. It runs **last**, after the domain is live, because the redirect URI
is derived from `PUBLIC_BASE_URL` and will be wrong if the address is not final.

The skill prints this checklist and then waits. Everything on it happens in a
browser; the human never touches a file.

1. Open `console.cloud.google.com` and create a project. Name it after the event.
2. Go to **APIs & Services → OAuth consent screen**. Choose **External**.
3. Fill in the app name, a support email and a developer contact email. Save.
4. Scopes: add `userinfo.email` and `userinfo.profile`. Nothing else is needed.
5. Go to **APIs & Services → Credentials → Create credentials → OAuth client ID**.
6. Application type: **Web application**.
7. Under **Authorized redirect URIs**, add this exact line, which the skill prints
   already filled in for their host:
   `https://<their-host>/v1/auth/oauth/google/callback`
8. Click create. Copy the **Client ID** and the **Client secret**.
9. Paste both back to the agent.

The skill writes `GOOGLE_OAUTH_CLIENT_ID` and `GOOGLE_OAUTH_CLIENT_SECRET` and
restarts the stack. Note the server refuses to start if exactly one of the pair is
set, so the skill must write both or neither.

An unverified External app shows a consent warning and is capped at 100 users,
which is fine for one event. Verification is only needed beyond that.

## 4b. Analytics on an event box

**Found 2026-09-09: analytics would report zero on every event box as built.**

The ingester parses a tab-separated line in a fixed field order: timestamp, host,
status, method, request URI, remote address, user agent. That format is produced
by an nginx `log_format` directive. Caddy writes JSON, and stock Caddy has no
arbitrary template formatter, so it cannot emit that TSV without a third-party
encoder plugin and therefore a custom image.

**Do this instead:** teach the ingester to read Caddy's JSON log as a second input
format. Every field it needs is already in there as `ts`, `request.host`,
`status`, `request.method`, `request.uri`, `request.remote_ip` and
`request.headers.User-Agent`. That avoids a custom Caddy image entirely.

This matters because per-site view counts split into people and bots are exactly
the kind of thing a participant enjoys during an event, and the marketing pages
already claim it.

**Country data is separate and should be skipped on event boxes.** It needs the
`ip_country_ranges` table loaded, which is a 717,152-row import. Not worth the
setup for a weekend.

## 5. DNS service

- Serves the **no-domain tier only**. Organisers on that tier never enable Resend
  or Google, so we never touch anyone's mail or identity records.
- Register the domain. `simple-agent.app` does not currently resolve.
- Claim endpoint, authenticated by the organiser's simple-host.app account:
  creates two A records at their server IP, one for the dashboard host and one
  for the content host. No wildcard is needed because content is path-based.
- **Teardown must remove the records with the server.** A record left pointing at
  a released cloud IP is a subdomain takeover: whoever gets that IP next serves
  content on our domain and can obtain a certificate for the name.
- An expiry sweep for events where teardown never ran. The codebase already has
  a periodic sweep that deletes expired sites and their files; reuse the pattern.
- Submit the domain to the Public Suffix List once strangers hold subdomains, so
  cookies and browser reputation are isolated between events.

## 6. The provisioning skill

### Providers

**UpCloud first** (token verified, `upctl` installed, credit available). One bearer
token, one CLI, done.

**Oracle Cloud second, and it is the valuable one.** Their always-free tier means
an event box costs nothing at all, forever, rather than five dollars a month. We
already publish `linux/arm64`, which is exactly what their free Ampere A1 shape
runs, so the artifact side needs nothing.

Two things make Oracle harder than UpCloud, and the skill has to handle both:

- **Authentication is not a bearer token.** Oracle signs every request with an RSA
  key pair, and a call needs the tenancy OCID, the user OCID, the key
  fingerprint, the region and a compartment OCID. That is five values and a
  generated key, against UpCloud's one token. Drive it through the `oci` CLI
  rather than reimplementing request signing.
- **Free ARM capacity is frequently exhausted.** "Out of host capacity" is a
  chronic, well-known failure on the free Ampere shape in popular regions. An
  organiser hitting it on the morning of their event must be told plainly what
  happened and offered another region or the paid micro shape, not left staring
  at an error.



- Idempotent install script: install Docker, write the env file, pull the image,
  `compose up`, wait for health, print the result.
- Skill: ask which provider, take a token, generate an SSH keypair locally, create
  the box, wait for boot, upload and run the script, report the URL.
- **Show the organiser the admin key the script generates.** Nothing does this
  today, and without it they are not the admin of their own instance.
- Teardown command: delete the server and the DNS records, after confirming.
- State the monthly cost before creating anything.

## 7. Marketing page and get started

- **Get started is a copyable prompt** that works in any agent that can fetch a
  URL: ChatGPT, Claude Code, Codex, Copilot, Grok, or a terminal agent. Shape:

  ```
  Read https://<instance>/llms.txt and follow it exactly.
  My Simple Host API key is: <KEY>
  Ask me what I want to build, then build it and publish it.
  ```

  `llms.txt` already exists and is already written as a paste-in prompt, so this
  works today for simple-host.app. It must be templated per instance (see §1).
- Rewrite the hackathon and enterprise pages once §1 to §6 are true. Until then
  they can only describe simple-host.app honestly.

## 7b. For enterprise, later

The event work is deliberately one server. Enterprise is where that stops being
enough, and the owner has named the shape he wants: **a Helm chart, so a team
that already runs Kubernetes installs it in one command.**

What already exists that makes this cheap when we get to it:

- A multi-arch image on ghcr.io, published on every tag. A chart just references it.
- Only two settings are genuinely required, `DB_DSN` and `ADMIN_API_KEY`, so the
  values file is short.
- The content path is a plain directory, so it maps to a PersistentVolumeClaim.

What has to be solved first, and none of it is chart work:

- **Migrations and a version the binary reports.** A chart implies upgrades. An
  event box is destroyed after a weekend and never upgrades; an enterprise
  instance has years of data. See section 8.
- **Where TLS terminates.** On a cluster it is usually an Ingress, not Caddy, so
  the chart must not assume the compose topology.
- **Whether the app or an init container applies the schema.** Today the schema
  is applied out of band by Postgres' entrypoint, which only works on first boot
  of a fresh database.

## 8. Deliberately not doing yet

- **Migrations and version reporting.** Needed before anyone upgrades an existing
  box. Irrelevant to a weekend event.
- **Judging view.** Organisers keep a list of links.
- **Per-site AI token allowance** so entries can include a chatbot.
- **Export and takeout.** Participants lose everything when the box is deleted.
- **Backups on an event box.** A volume failure mid-event loses the event.
