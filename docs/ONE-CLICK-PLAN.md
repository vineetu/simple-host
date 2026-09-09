# One-click install from a provider's catalog

Written 2026-09-09. Nothing here is built.

The goal: an organiser picks Simple Host when creating a server, and gets a
working instance without an agent, a script or a terminal.

---

## Where this is possible, and where it is not

Checked against each provider's own catalog on 2026-09-09.

| Provider | Third-party apps in the catalog | What we would submit |
|---|---|---|
| DigitalOcean | Yes, open submission | A prepared image |
| Vultr | Yes, open submission | A prepared image |
| UpCloud | **No.** 41 public templates, and the 16 non-OS ones are all UpCloud's own Kubernetes images | Nothing; see below |
| Hostinger | Their catalog targets their own stack | Nothing |
| Oracle | Marketplace exists but is enterprise-oriented | Not worth it |

**UpCloud has no third-party catalog, but it does have private templates.** The
`templatize` call is real: you build a server once, turn it into a template, and
create from that template afterwards. That is not a public listing, but it makes
repeat events on UpCloud one call instead of an install. Worth having, worth not
confusing with a marketplace listing.

So: **DigitalOcean and Vultr for the public catalog. UpCloud gets a private
template. Hostinger and Oracle keep the scripted install.**

---

## The problem a catalog image has

An image is built before it knows where it will live. It cannot contain the
hostname, so something has to ask after the box boots.

**The answer is a first-boot setup page.** The image comes up with no hostname
configured and serves one screen on its own IP address, which is exactly what
the provider shows the user the moment the server is created. They open it and
answer one question.

- **They have a domain.** The page shows the two records to add, then polls DNS
  itself and continues the moment both resolve. Nobody relays instructions and
  nobody guesses whether it worked.
- **They have no domain.** They paste their simple-host.app account key and the
  page claims two names for them. **The box calls the claim endpoint itself, so
  the public instance already sees its public address from the request.** There
  is nothing to read off a console, copy, or type wrong. This deletes the
  riskiest step in the current design.

Then the page waits for certificates, redirects to the real hostname, and never
appears again.

---

## Securing the setup page

**Decided 2026-09-09.** Both, because they cost little together:

- **If a setup password was supplied at creation, the page demands it.** The
  create form on DigitalOcean and Vultr has a user-data field; cloud-init turns
  whatever the user typed there into that password. This is the safe path for
  anyone who wants it.
- **If the field was left empty, the page is open and locks permanently after
  first use.** The owner accepts that window: it is minutes long, on an address
  nobody has been told, and the page can never be used twice.

Both paths already prove something on their own. The free-hostname path needs a
simple-host.app account key. The own-domain path needs DNS records only the
domain's owner can add.

---

## What has to be built

1. **Setup mode in the binary.** When no hostname is configured, serve the setup
   page instead of the product, and nothing else. The bulk of the work.
2. **Settings that survive a restart.** The chosen hostname goes in the database,
   not a config file: the process cannot rewrite a file its container mounted
   read-only, and it can always write to its own database. Read it back before
   the environment on the next start. Setup must refuse to run twice, or a second
   run could move a live instance and strand every URL anyone had published.
3. **Certificates without a config file.** Caddy can ask the application whether
   a hostname belongs to it and obtain a certificate on the spot. The repository
   already carries that pattern in `deploy/Caddyfile.v3-content-host.example`.
   With it, the hostname never has to appear in a config file, so nothing needs
   rewriting or restarting after setup.
4. **Claim by source address.** `/v1/events` should take the caller's own address
   when the caller is the box itself, rather than a value someone typed. Small
   change, removes a whole class of error.
5. **An image per provider,** built from the current release, plus a submission.

## What is not code

**Review is the long pole.** DigitalOcean and Vultr both review submissions, and
that is typically a few weeks each. The images also have to be rebuilt for every
release that matters, which is an ongoing obligation rather than a one-off.

Build the setup mode first regardless of the catalog work. It is useful
immediately: it removes the DNS step from the scripted install too, so an
organiser following the skill benefits before any marketplace listing exists.

## Open, deliberately not decided yet

- Whether the free-hostname path should be offered on a catalog image at all,
  since it points a stranger's box at names under our domain with only an
  account key behind it. The rate limit, the per-account cap and the
  already-serving checks all apply, but the volume is different once anyone can
  create one of these in a click.
