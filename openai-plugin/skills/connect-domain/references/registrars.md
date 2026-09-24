<!-- Derived from simple-host-website/skills/connect-domain/references/registrars.md. Keep in step. -->

# Adding the DNS record at the registrar

The record is always the one `connect_domain` returned: **CNAME `<label>` →
`cname.simple-host.app`** for a subdomain, or **A `@` → the IP returned** for an apex. Nothing
else changes. Give the person the section for the service that manages their DNS. That is
where they bought the domain, unless they moved its nameservers elsewhere (for example to
Cloudflare or Vercel); if unsure, they can check which company the domain's nameservers belong
to in their registrar's settings.

Rules everywhere:

- **Never a CNAME at the apex.** Apex = A record (or ALIAS/ANAME to `cname.simple-host.app`
  where offered; Cloudflare's flattened CNAME is fine too). Prefer the A record returned.
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

## Only if the person offers: adding the record yourself

Do this only when the person offers credentials unprompted or asks you to do it, and you can
make HTTPS requests. Ask permission naming the exact record first, make the one write and a
read-back, and never store, log or repeat the credentials. If anything fails, fall back to the
steps above. Endpoints verified 2026-09-06.

**Vercel** (access token from Account Settings → Tokens; add `?teamId=<id>` if the domain is
on a team):

```
POST https://api.vercel.com/v4/domains/<zone>/records
Authorization: Bearer <token>
Content-Type: application/json

{"name":"rsvp","type":"CNAME","value":"cname.simple-host.app","ttl":60}
apex: {"name":"","type":"A","value":"<IP returned>","ttl":60}
```

Returns `{"uid":"rec_..."}`; undo is `DELETE https://api.vercel.com/v2/domains/<zone>/records/<uid>`.
`403`: missing `teamId` or scope. `409`: a record with that name and type exists; list with
`GET .../records` and edit it instead.

**GoDaddy** (personal access token from developer.godaddy.com with DNS update scope, sent as
`Authorization: Bearer <token>`; base `https://api.godaddy.com`; minimum TTL 600):

```
PATCH /v1/domains/<zone>/records            (adds; leaves existing records alone)
[{"type":"CNAME","name":"rsvp","data":"cname.simple-host.app","ttl":600}]

PUT /v1/domains/<zone>/records/A/@          (apex only: replaces the parking A)
[{"data":"<IP returned>","ttl":600}]
```

Success is an empty 200/204. Read back with `GET /v1/domains/<zone>/records/CNAME/rsvp`. Only
ever use `PUT .../records/{type}/{name}` for the apex A; it replaces every record of that type
at that name. A `403` means the token lacks DNS scope or the domain is in another account.

**Porkbun** (the person creates a key pair under Account → API Access, **and** turns on the
**API Access** toggle on the domain's Details page, which only they can do; base
`https://api.porkbun.com/api/json/v3`; credentials go in the JSON body):

```
POST /ping                     {"apikey":"pk1_...","secretapikey":"sk1_..."}
POST /dns/create/<zone>        {"apikey":"...","secretapikey":"...","name":"rsvp","type":"CNAME","content":"cname.simple-host.app","ttl":600}
apex:                          {..., "name":"","type":"A","content":"<IP returned>","ttl":600}
POST /dns/retrieve/<zone>      {"apikey":"...","secretapikey":"..."}      (read back)
```

Returns `{"status":"SUCCESS","id":"..."}`; undo is `POST /dns/delete/<zone>/<id>` with the same
credentials. A parking `ALIAS` at the root blocks an apex `A`: find its id with `dns/retrieve`,
delete it, then create. `status: "ERROR"` carries the reason; 429 carries `Retry-After`.
