<!-- Derived from simple-host-website/skills/connect-domain/references/registrars.md. Keep in step. -->

# Adding the DNS records at the registrar

The records are always the two `connect_domain` returned: **CNAME `<label>` →
`cname.simple-host.app`** for a subdomain, or **A `@` → the IP returned** for an apex, and the
**TXT ownership record** (`ownership_record`): **TXT `_simple-host.<label>`** (apex:
`_simple-host`) holding the site's value. Nothing else changes. Give the person the section for the service that manages their DNS. That is
where they bought the domain, unless they moved its nameservers elsewhere (for example to
Cloudflare or Vercel); if unsure, they can check which company the domain's nameservers belong
to in their registrar's settings.

Rules everywhere:

- **Never a CNAME at the apex.** Apex = A record (or ALIAS/ANAME to `cname.simple-host.app`
  where offered; Cloudflare's flattened CNAME is fine too). Prefer the A record returned.
- **The TXT record is added the same way,** with type `TXT`, the name from
  `ownership_record.host` (label form, e.g. `_simple-host.rsvp`) and the value exactly as given
  (some panels add the quotes themselves). It stays in place after the domain is live.
- **Low TTL while connecting** (60-300 s, or the provider's minimum), so a typo is fixable in
  minutes.
- **Add, don't replace**, except at the apex, where an existing parking/default A, ALIAS or
  forwarding at `@` must be **edited** (or removed and re-added), not duplicated. Leave MX, TXT
  and everything else alone.
- **Name is the label only**: `rsvp`, not `rsvp.example.com`. Apex is `@` or blank.
- A public resolver can show the old answer for up to the old TTL right after a change. That is
  cache, not a mistake; `domain_status` is the verdict.

---

## Vercel

Applies when the domain's nameservers are `ns1.vercel-dns.com` / `ns2.vercel-dns.com` (bought
at Vercel, or moved there). If the domain is only attached to a Vercel project but its
nameservers are elsewhere, Vercel is not the DNS host.

**Steps:** Vercel dashboard → team → **Domains** → click the domain → DNS Records form (**Enable
Vercel DNS** first if the form is hidden) → fill in → **Add**.

- Subdomain: Name `rsvp` · Type `CNAME` · Value `cname.simple-host.app` · TTL `60`.
- Apex: Name *empty* · Type `A` · Value `<IP returned>` · TTL `60`. (Vercel also offers `ALIAS`
  → `cname.simple-host.app`; use one or the other, not both.)

Vercel updates within seconds.

---

## GoDaddy

**Steps:** godaddy.com → sign in → **Domain Portfolio** (My Products → Domains) → click the
domain → **DNS** tab → **Add New Record**.

- Subdomain: Type `CNAME` · Name `rsvp` · Value `cname.simple-host.app` · TTL: smallest offered
  (600 s / custom if available; the default is 1 hour).
- Apex: GoDaddy has no ALIAS, so it is the A record. A default A at `@` ("Parked",
  "WebsiteBuilder Site") already exists: **edit** it (pencil icon) to the IP returned rather
  than adding a second one. If **Forwarding** is set up for the domain, turn it off or it fights
  the A record.

GoDaddy's own nameservers pick it up in about a minute.

---

## Porkbun

**Steps:** porkbun.com → sign in → **Account → Domain Management** → find the domain →
**Details** (or the **DNS** link on its row) → **DNS Records** → **Add Record**.

- Subdomain: Type `CNAME` · Host `rsvp` · Answer `cname.simple-host.app` · TTL `600`.
- Apex: Type `A` · Host *blank* · Answer `<IP returned>` · TTL `600`. (`ALIAS` →
  `cname.simple-host.app` also works; prefer the A record.)
- New Porkbun domains come with parking records (an `ALIAS` at the root and a `CNAME` at `*`).
  The error "A CNAME or ALIAS record with that host already exists" means one is in the way:
  delete only the record at the **same host** you are adding (the root, for an apex). A named
  subdomain never needs the `*` record touched.

600 s is Porkbun's usual minimum TTL; that is fine.

---

## Cloudflare

Applies when the nameservers are `*.ns.cloudflare.com`. **DNS → Records → Add record**.

- Subdomain: Type `CNAME` · Name `rsvp` · Target `cname.simple-host.app`.
- Apex: Type `A` · Name `@` · IPv4 `<IP returned>` (or a flattened `CNAME @` →
  `cname.simple-host.app`).
- Set **Proxy status to DNS only (grey cloud)**. A proxied (orange) record hides the site from
  Simple Host's check and the certificate never issues.

---

## Namecheap, Squarespace, Route 53 and others

Same record, same rules. The DNS editor is under the domain's settings, usually named *DNS*,
*DNS Records*, *Manage DNS*, *Advanced DNS* or *Zone*. Fields map to Type / Name or Host (label
only; `@` or blank for apex) / Value, Target, Answer or Points to / TTL. Some older panels
(Namecheap among them) want a trailing dot on the target: `cname.simple-host.app.` is always
safe.

---

## Adding records

Give the person the DNS records and the registrar dashboard steps; they add the records themselves. Do not ask for or use registrar credentials in chat.
