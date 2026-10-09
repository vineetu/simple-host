> **Status: release 1 shipped 2026-09-29 (wildcard, operator certificates); per-host certificates (release 2) not started; chhotabreak migration pending.** As built: `internal/handler/familyhost.go` (index, resolver, ranking, `FamilyHosts`), `internal/handler/familyapi.go` (API, checks, lapse, issuer hand-off, admin), `internal/db/families.go`, `internal/storage/families.go`, migration `db/migrations/v077-address-families.sql`, `deploy/family-certs/` (issuer, template, units, tests), `deploy/prod/family-adopt.sh`. Decision: INTENT 2026-09-29 "Address families" and the amendment to 2026-09-06.

# Address families

An account connects `*.<suffix>` once. Every site of the account named `<site_prefix><label>`
then also answers at `<label>.<suffix>`. It exists because chhotabreak runs four hand-made
wildcard families under chhotabreak.com (`*.trips`, `*.quotes`, `*.voucher`, `*.guide`), where
sign-in, saved data, private lists and analytics did not work and every change needed the
operator. Her voucher and guide families serve the sites `voucher-<name>` and `guide-<name>`,
which is why a family has an optional site prefix.

## Data model

`address_families` (one row per account and suffix): `user_id`, `suffix` (no `*.`),
`site_prefix` (default empty), `rank` (0–1000, tie-break), `canonical` (default true), `token`
(`sh-<32 hex>`, the TXT value), `status` (`pending`, `active`, `failing`), `last_error`,
`bound_at`, `verified_at`, `checked_at`, `failing_since`, `lapse_notified_at`, `release_at`,
`cert_mode` (`wildcard`; `per_host` reserved for release 2), `cert_name` (the operator's
certbot lineage), `proof_exempt`. Unique on `(user_id, suffix)`, and on `suffix` among verified
rows, so a verified suffix has one owner. `family_cert_requests` holds the per-account daily cap
for release 2. On disk, `sites/families/<suffix>` links to `../by-id/<user_id>`, so a handle
change never breaks it and removing the link stops serving at once.

## Names

- Suffix: ASCII, at least two labels, not a public suffix, not internationalised, and never one
  of the platform's zones (the content host, the site bases, the CNAME target's zone, the public
  host, and every `EVENT_DOMAINS` zone such as simple-hack.app). The same zone check now also
  refuses those names as custom domains.
- Overlap: under an advisory lock on the registrable domain, a suffix is refused (409
  `domain_taken`) when another account has a verified family equal to it, over it or under it,
  or a custom domain equal to it or under it. A custom domain under another account's verified
  family is refused the same way. Within one account, nesting and a custom domain under its own
  family are allowed; the exact domain wins, and the site's family addresses redirect to it.
- Label: one DNS label, no `xn--`, not in `ADDRESS_FAMILY_RESERVED_LABELS` (default `www`), and
  `<prefix><label>` must be a valid site name. The platform's reserved-name lists do not apply:
  it is the customer's zone.
- `<label>.<suffix>` is the family owner's live site named `<prefix><label>`. Rename: the new
  label serves at once and the old one 302s through the rename alias. Delete: 404. Restore:
  serves again. A site created later answers at once.

## Proof, pending period and lapse

- Proof: TXT `_simple-host.<suffix>` = the token (skipped when `proof_exempt`), and a random
  label `sh-check-<hex>.<suffix>` resolving only to this server's public addresses. Once live,
  one real site must also answer over HTTPS through this server.
- Pending families serve nothing and are never exclusive. Several accounts may wait on one
  suffix; the first to prove it wins and the others are dropped and emailed. A pending family
  is dropped after `ADDRESS_FAMILY_UNPROVEN_HOURS` (24).
- Checks run every `ADDRESS_FAMILY_CHECK_INTERVAL_MINUTES` (10); a working family is re-proved
  every `ADDRESS_FAMILY_ACTIVE_RECHECK_MINUTES` (60). A verified family that fails gets
  `failing_since`; its owner is emailed after `ADDRESS_FAMILY_LAPSE_WARN_HOURS` (24) with a
  `release_at` date that then holds, and it is disconnected after `ADDRESS_FAMILY_LAPSE_HOURS`
  (72). Disconnecting removes the row, the link and the issuer's request, and re-points every
  site's redirect. Reconnecting needs the TXT again.

## Certificates

- Release 1, `cert_mode` `wildcard`: the operator issues `*.<suffix>` with certbot (DNS-01 with
  credentials limited to that subtree) and names the lineage (`cert_name`) from the admin API.
  Until then the family's certificate is `waiting_for_operator` and nothing is served. The
  issuer never issues, renews or deletes the certificate; it checks that the lineage covers
  `*.<suffix>` and has not expired, and logs a warning 14 days ahead.
- Release 2, `per_host` (not started): one certificate per existing site name over HTTP-01,
  requested when the family verifies and when a site is created or renamed, never for random
  labels; capped per account per day (`ADDRESS_FAMILY_CERTS_PER_ACCOUNT_DAILY`) and per
  registered domain per week at the issuer, reusing certificates it issued. Until then the API
  answers 400 `cert_mode_unavailable`.

## Serving

- The app writes `ADDRESS_FAMILY_CERT_DIR/requests/<suffix>` for a verified family with a
  lineage (token, link target, mode, lineage, prefix, reserved labels). The root issuer
  (`deploy/family-certs/issue.sh`, installed as `/usr/local/sbin/simple-host-family-certs`, path
  unit plus 10-minute timer, state `/var/lib/simple-host-family-certs`) checks the link and the
  lineage, refuses a family any other nginx server answers (probing exact, wildcard and regex
  names in `nginx -T`), writes `sites-enabled/simple-host-family-<suffix>` from
  `vhost.conf.template`, reloads nginx and writes `ready/<suffix>` (prefix, lineage, expiry), or
  `failed/<suffix>` with one line shown to the owner. The app treats a family as live only while
  the ready marker's prefix matches the database.
- The nginx server matches `~^(?<sh_label>...)\.<suffix>$`, writes the analytics log, serves
  `families/<suffix>/<prefix>$sh_label/current` from disk, checks the take-down, offline,
  passcode and lives-elsewhere markers in that order, and sends `/v1/`, `/internal/` and misses
  to the app. The label has no dots, so hidden folders are unreachable.
- `FamilyHosts` is outermost in the host chain. On a family host `/v1/` resolves only the one
  site the host names; other paths go through the same file gate as the site host; a renamed
  label 302s, a missing site is 404, and a site with a domain of its own 302s there.
- `deploy/prod/family-adopt.sh <suffix> --old <vhost>` moves a hand-made wildcard server to the
  managed file with no downtime: dry run by default. `--apply` backs up the old link, writes the
  ready marker, waits (up to 90 s) for the admin API to report the family `live`, which is what
  requests are routed by, and only then swaps the links back to back (never both enabled), runs
  `nginx -t` (restoring everything, the marker included, on failure) and reloads. If the app
  never reports it live, nothing is swapped and the marker is taken back. `--rollback --apply`
  is the reverse: the old server back and reloaded, then the marker removed, then a wait for
  `live: false`.
- A host under a proven family (verified, or proof-exempt by the admin) that is not live
  (pending, failing, verified without its marker, not yet in the routing index, or let go since
  the server started) is a plain 404, never the platform's own pages. An unproven family changes
  nothing: anyone may connect any name, and must not blank its hosts.

## Main address

Ranking: custom domain or free name > the most specific canonical family (longest site prefix,
then lowest rank, then suffix) > the site host `<site>.<handle>.simple-host.app`. When a family
address is the main one, the site host and old links 302 there, and the site host refuses
sign-in and saves with `use_custom_domain`, as for a custom domain. Every family address of a
site keeps taking saves and sign-ins either way. `canonical: false` keeps the site host as the
main address (the chhotabreak migration starts with it off). Previews stay on the site host.

## Everything else on a family address

- Visitor sign-in: the sign-in return check accepts a family host only for a live family, a
  site without a domain of its own, and a host that resolves to this server now. Sessions are
  bound to the host with `__Host-` cookies. Email-code sign-in needs a same-origin request there,
  because sibling family hosts are the same "site" to a browser.
- Saved data, private lists, "who may save", the origin check, analytics attribution (suffix to
  account and prefix), the report form, take-down, offline and passcode work as on a custom
  domain.
- Idle cleanup exempts sites whose main address is a family (`IDLE_EXEMPT_FAMILY_SITES`, on).
- The account's data download lists `account.address_families`; account erasure removes them.
- API: site responses add `family_address` (the main family address) and `family_addresses`;
  `site_url` is unchanged. The connector's `url` is the active domain, else `family_address`,
  else `site_url`. `connect_domain`, `domain_status` and `remove_domain` take `*.<suffix>`
  without a site.
- Hosted only: Simple Host Enterprise has no custom domains (PARITY: different on purpose).

## Security points

- Dangling wildcards: nothing is served before the TXT proof; release removes the server; a
  record still pointing here serves nothing and can only be claimed with the TXT.
- A stranger's site never serves on someone's family: the host resolves only within the family
  owner's `by-id/<user_id>`.
- Redirect targets come only from the database (main address, renamed name) plus an escaped
  path.
- Host spoofing: the app trusts only hosts in the verified family index, and sessions are bound
  to the host.

## Settings

`ADDRESS_FAMILIES`, `ADDRESS_FAMILIES_PER_ACCOUNT` (5), `ADDRESS_FAMILY_UNPROVEN_HOURS` (24),
`ADDRESS_FAMILY_LAPSE_WARN_HOURS` (24), `ADDRESS_FAMILY_LAPSE_HOURS` (72),
`ADDRESS_FAMILY_CHECK_INTERVAL_MINUTES` (10), `ADDRESS_FAMILY_ACTIVE_RECHECK_MINUTES` (60),
`ADDRESS_FAMILY_CERTS_PER_ACCOUNT_DAILY` (12, release 2), `ADDRESS_FAMILY_RESERVED_LABELS`
(`www`), `ADDRESS_FAMILY_CACHE_SECONDS` (30), `IDLE_EXEMPT_FAMILY_SITES` (on),
`RATE_LIMIT_ADDRESS_FAMILY_CHECK` / `_USER`, `ADDRESS_FAMILY_CERT_DIR`. Full table:
`docs/configuration.md`.

## Migrating chhotabreak's four families

Her DNS is at GoDaddy and her wildcard certificates renew through GoDaddy hooks limited to each
subtree; they stay as they are. Per family, in the order trips, guide, voucher, quotes:

1. Take a client-side baseline (status, body hash and certificate subject of every site's
   family address).
2. An admin connects the family for her account: prefix `voucher-` / `guide-` (none for trips and
   quotes), `cert_mode` `wildcard`, `cert_name` the existing lineage, `canonical: false`; add the
   `_simple-host.<family>` TXT record with her OK, or set `proof_exempt`. Wait for it to verify
   (the issuer reports "already served here", as expected). Rollback: delete the family.
3. `deploy/prod/family-adopt.sh <family> --old <vhost>`, then `--apply` (it waits for the app
   to report the family live before it swaps). Rollback: `--rollback --apply`.
4. Verify from a client: the baseline matches, a missing label is 404, an offline test site
   shows the offline page, `/v1/sites/<label>/me` answers, the certificate is the wildcard, an
   analytics line is written, and the neighbours (`console.quotes`, `trip.chhotabreak.com`,
   simple-host.app, simple-hack.app, vineetsriram.com, sf-fog.today) answer 200.
5. Soak 24 hours, then remove the old file from sites-available (keep the backup 30 days).

After all four, and once her console's API base is checked (a main family address makes site
hosts refuse saves), turn `canonical` on one family at a time, voucher and guide first. Rollback
is turning it off.
