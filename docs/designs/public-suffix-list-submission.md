# Public Suffix List: submitting simple-host.app (prepared 2026-09-25, NOT submitted)

Why: every account now has its own address, `<handle>.simple-host.app`. Browsers treat all of
them as one "site" until `simple-host.app` is on the Public Suffix List, so cookies set with
`Domain=simple-host.app` and SameSite=Lax requests cross between people's addresses. The app
already defends against that (host-only `__Host-` visitor cookies; cookie-authenticated calls
must be same-origin; sign-in forms must be same-origin), so nothing waits on this; the listing
makes the browser enforce it too, and gives each address its own cookie jar, storage
partitioning key and Safe Browsing reputation.

Owner does all of the below; nothing has been sent.

## 1. Fork and edit

Fork https://github.com/publicsuffix/list, branch `simple-host-app`, and add to
`public_suffix_list.dat` in the PRIVATE section, sorted alphabetically by the organisation's
comment line (find the right place among the `// S...` entries):

```
// Simple Host : https://simple-host.app
// Submitted by Vineet Sriram <support@simple-host.app>
simple-host.app
```

Only `simple-host.app` itself — not `*.simple-host.app` (the wildcard would make
`x.y.simple-host.app` suffixes too, which nobody uses) and not the apex separately.

## 2. The `_psl` TXT record (Vercel DNS, zone simple-host.app)

Open the PR first (step 3) to learn its number, then add:

| Name | Type | Value |
|---|---|---|
| `_psl` | TXT | `https://github.com/publicsuffix/list/pull/<PR number>` |

Check: `dig +short TXT _psl.simple-host.app` shows the PR URL. Keep the record as long as the
entry is listed (the PSL maintainers re-check it).

## 3. PR text

Title: `Add simple-host.app`

Body (fill the checklist the PR template shows; the answers):

```
Organization: Simple Host (https://simple-host.app), an independent static-site host run by
Vineet Sriram. Contact: support@simple-host.app

Reason for PSL inclusion:
Every account on simple-host.app gets its own address, https://<handle>.simple-host.app/, and
people publish websites there written by themselves (usually with an AI agent). Those
addresses belong to different, mutually untrusting people. Without a PSL entry, browsers treat
alice.simple-host.app and bob.simple-host.app as the same site: a page on one can set cookies
for simple-host.app that the other receives, and SameSite=Lax does not separate them. We
already scope our own cookies to the exact host (__Host- prefix) and require same-origin for
cookie-authenticated requests; we are asking for the browser-level boundary so the pages
people publish (which we do not write) get it too, and so storage and cookie partitioning
treat each person's address as its own site.

Number of users / subdomains: every account (tens today, growing); one subdomain per account,
plus names a site claims for itself (<name>.simple-host.app).

DNS verification: _psl.simple-host.app TXT "https://github.com/publicsuffix/list/pull/<this PR>"

The registration term of simple-host.app is more than 2 years out: <check at the registrar and
state the expiry date here; the PSL requires at least 2 years remaining>.

I understand the PSL is not a way to get cookie or rate-limit exemptions, that inclusion can
take months, and that removal is slow; we want this entry permanently.
```

## 4. Before submitting, check

- Domain expiry at the registrar is 2+ years away (renew first if not; the PSL requires it).
- `sort` order and the section (PRIVATE, between `// ===BEGIN PRIVATE DOMAINS===` and
  `// ===END PRIVATE DOMAINS===`).
- `make test` in the forked repo passes (it runs the linter on the file).
- After listing, nothing to change in the app. Note: once listed, `simple-host.app` and
  `www.simple-host.app` are separate sites from every `<handle>.simple-host.app` too; the
  dashboard keeps its key in the apex's own storage and uses no cookies, so it is unaffected.
