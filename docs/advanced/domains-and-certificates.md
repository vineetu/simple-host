# Domains and certificates

An owner can give a site their own domain: a subdomain with a CNAME record, or a bare domain with
an A record, plus one TXT record that proves ownership. The certificate is issued automatically
(on a small box, by Caddy when the name is first asked for). `www` and the bare domain work
together, one redirecting to the other.

**Checks and release.** A connected domain whose DNS never points here is released after
`DOMAIN_UNPROVEN_HOURS`; one that points here but never goes live over HTTPS after
`DOMAIN_UNPROVEN_MAX_DAYS`. A live domain that starts failing every check gets its owner an email,
and later stops being the site's address; the date in that email holds even if the setting
changes.

**Event hostnames** (`<name>.<event domain>` handed to hackathon organisers) are a feature of
the public instance only.

<!-- settings:group=domains -->
| Setting | Default | Allowed | What it does |
|---|---|---|---|
| `DOMAIN_UNPROVEN_HOURS` | `24` | 1–720 hours | A connected domain whose DNS never points here is released after this long. |
| `DOMAIN_UNPROVEN_MAX_DAYS` | `7` | 1–90 days | A domain that points here but never goes live over HTTPS is released after this long. |
| `DOMAIN_LAPSE_WARN_HOURS` | `24` | 1–720 hours | A live domain failing every check: its owner is emailed after this long. |
| `DOMAIN_LAPSE_HOURS` | `72` | 2–2160 hours | ... and it stops being the site's address after this long. Longer than DOMAIN_LAPSE_WARN_HOURS. |
| `DOMAIN_CHECK_INTERVAL_MINUTES` | `2` | 1–60 minutes | How often connected domains are checked. |
| `DOMAIN_CERTS_PER_ACCOUNT_DAILY` | `5` | 1–1000 certificates | New custom-domain certificates one account may ask for in a day. |
| `EVENT_TTL_DAYS` | `21` | 1–60 days | How long a claimed event hostname lives (only where EVENT_DNS_TOKEN is set). |
| `EVENT_MAX_CLAIMS` | `5` | 1–100 names | Event hostnames one account may hold at once. |
| `ADDRESS_FAMILIES` | `on` | `on` / `off` | on lets an account connect *.<its domain> once, so every site of the account answers at <site>.<its domain> (an address family). Families also need ADDRESS_FAMILY_CERT_DIR and a wildcard certificate the operator sets up. |
| `ADDRESS_FAMILIES_PER_ACCOUNT` | `5` | 0–100 families | Address families one account may connect. 0: none. |
| `ADDRESS_FAMILY_UNPROVEN_HOURS` | `24` | 1–720 hours | How long a new address family may wait for its DNS records before it is dropped. |
| `ADDRESS_FAMILY_LAPSE_WARN_HOURS` | `24` | 1–720 hours | How long a working address family may fail its checks before its owner is emailed. |
| `ADDRESS_FAMILY_LAPSE_HOURS` | `72` | 2–2160 hours | How long a working address family may fail its checks before it is disconnected. Must be longer than ADDRESS_FAMILY_LAPSE_WARN_HOURS. |
| `ADDRESS_FAMILY_CHECK_INTERVAL_MINUTES` | `10` | 1–60 minutes | How often address families are checked. |
| `ADDRESS_FAMILY_ACTIVE_RECHECK_MINUTES` | `60` | 5–1440 minutes | How often a working address family's DNS records are proved again. |
| `ADDRESS_FAMILY_CERTS_PER_ACCOUNT_DAILY` | `12` | 1–1000 certificates | Per-site-name certificates one account's families may ask for in a day (for a later release; wildcard families need none). |
| `ADDRESS_FAMILY_RESERVED_LABELS` | `www` | 0–0 labels | Names (comma-separated) that never name a site under an address family. |
| `ADDRESS_FAMILY_CACHE_SECONDS` | `30` | 1–3600 seconds | How long the server keeps its list of working address families before reading it again. |
| `RATE_LIMIT_TLS_ASK` | `60,100ms` | any (warns past 10× looser) | Certificate checks (/internal/tls-ask), per address. |
| `RATE_LIMIT_DOMAIN_CHECK` | `10,10s` | any (warns past 10× looser) | "Check again" on a domain, per address. |
| `RATE_LIMIT_DOMAIN_CHECK_USER` | `3,30s` | any (warns past 10× looser) | "Check again" on a domain, per account. |
| `RATE_LIMIT_ADDRESS_FAMILY_CHECK` | `10,10s` | any (warns past 10× looser) | "Check again" on an address family, per address. |
| `RATE_LIMIT_ADDRESS_FAMILY_CHECK_USER` | `3,30s` | any (warns past 10× looser) | "Check again" on an address family, per account. |
| `CNAME_TARGET` | `cname.<SITE_DOMAIN>` | text | The hostname people point their own domain at with a CNAME record. |
| `CUSTOM_DOMAIN_IP` | none | text | This server's public IPv4, given as the A record for a bare domain (brand.com). |
| `SITE_CERT_DIR` | none | text | Where per-person certificates are requested from, and found ready from, the certificate issuer. |
| `SITE_BASE_CERT_DIR` | none | text | Like SITE_CERT_DIR, for the per-person certificates under SITE_BASE_DOMAIN. Required, with ready/ and requests/ in it, once SITE_BASE_MOVE is on with its own domain: the server will not start without it. |
| `DOMAIN_CERT_DIR` | none | text | Where custom-domain certificates are requested from the certificate issuer. Empty: issued by hand. |
| `ADDRESS_FAMILY_CERT_DIR` | none | text | Where address-family requests go to the family issuer, and where it says a family is served (ready/). Empty: no address family is ever served. |
| `EVENT_DNS_TOKEN` | none | secret | The DNS token that hands out event hostnames. The public instance only. **Security-sensitive.** |
| `EVENT_DNS_TEAM_ID` | none | text | The DNS account the event token belongs to. |
| `EVENT_DOMAINS` | none | text | Zones (comma-separated) event hostnames are handed out under. |
<!-- /settings -->

## Recipes

**Bare domains on a small box.** Set `CUSTOM_DOMAIN_IP` to the server's public IPv4 so owners are
told the right A record.

**Faster release of abandoned domains.**

```
DOMAIN_UNPROVEN_HOURS=6
DOMAIN_UNPROVEN_MAX_DAYS=2
```
