# Adding the DNS record at the registrar

Companion to the `connect-domain` skill, step 3 / 3b. The record is always the one from the
bind response: **CNAME `<label>` → `cname.simple-host.app`** for a subdomain, or **A `@` →
the IP the bind returned** for an apex. Nothing else changes. Ask the user which service hosts
the domain's DNS (it is the registrar unless they moved nameservers — `dig NS <zone> +short`
tells you), then follow that section.

Rules that apply everywhere:

- **Never CNAME an apex.** A CNAME at `@` breaks MX/TXT/everything at the root and most
  registrars refuse it. Apex = A record (or ALIAS/ANAME where the provider offers one, pointed
  at `cname.simple-host.app`).
- **TTL 60–300 s while connecting.** Low TTL means a typo is fixable in minutes, not an hour.
  Raise it later if the user cares; the server doesn't.
- **Add, don't replace** — except at the apex, where a parking/default A or ALIAS at `@`
  already exists and must be **edited** (or removed and re-added), not duplicated. Leave MX,
  TXT, everything else alone.
- **Credentials:** if the user hands you an API key, use it for the one call (plus a read-back
  to confirm) and forget it. Never write it into the site, the repo, a config file, or a
  message. Ask permission naming the exact record before the write (skill step 3b).
- **Check it landed at the authoritative server, not a public resolver.** `8.8.8.8` / `1.1.1.1`
  may hold an older answer (often a parking wildcard) for the old TTL — a "wrong" answer there
  right after the change means cache, not a mistake. Query the registrar's nameserver directly.

---

## Vercel DNS

Applies when the domain's nameservers are `ns1.vercel-dns.com` / `ns2.vercel-dns.com`
(domains bought at Vercel, or moved there). If the domain is in a Vercel *project* but its
nameservers are elsewhere, Vercel is not the DNS host — go to the actual host.

**UI:** Vercel dashboard → team → **Domains** (sidebar) → click the domain → DNS Records form
(**Name**, **Type**, **Value**, **TTL**; **Enable Vercel DNS** first if the form is hidden) → **Add**.

- Subdomain: Name `recipes` · Type `CNAME` · Value `cname.simple-host.app` · TTL `60`.
- Apex: Name *empty* · Type `A` · Value `<ip from bind>` · TTL `60`. Vercel also supports
  `ALIAS` at the apex (Value `cname.simple-host.app`) — either works; the A record is the one
  the bind response describes, so prefer it. Don't leave both.
- Name is the **label only** (`recipes`, not `recipes.brand.com`). Apex = empty string in the
  API, blank in the UI. Default and minimum TTL is 60.

**API** (verified 2026-09-06 against
https://vercel.com/docs/rest-api/reference/endpoints/dns/create-a-dns-record ; the docs list
`/v2`, `/v4` is what worked in practice the same day — both accept this body):

```bash
# token: Vercel → Account Settings → Tokens. teamId only if the domain belongs to a team.
curl -sS -X POST "https://api.vercel.com/v4/domains/$ZONE/records?teamId=$TEAM_ID" \
  -H "Authorization: Bearer $VERCEL_TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"recipes","type":"CNAME","value":"cname.simple-host.app","ttl":60}'
# apex:  -d '{"name":"","type":"A","value":"<ip from bind>","ttl":60}'
# → {"uid":"rec_..."}   keep uid: undo is
#   curl -X DELETE "https://api.vercel.com/v2/domains/$ZONE/records/$UID?teamId=$TEAM_ID" -H "Authorization: Bearer $VERCEL_TOKEN"
# list:  GET https://api.vercel.com/v4/domains/$ZONE/records?teamId=$TEAM_ID
```

`403` = token is personal but the domain is on a team (add `teamId`), or the token lacks the
scope. `409` = a record with that name/type already exists — list, then delete or edit it.

**Check it landed:** `dig +short recipes.brand.com @ns1.vercel-dns.com`
(apex: `dig +short A brand.com @ns1.vercel-dns.com`). Vercel answers within seconds.

---

## GoDaddy

**UI:** godaddy.com → sign in → **Domain Portfolio** (My Products → Domains) → click the domain →
**DNS** tab → **Add New Record**.

- Subdomain: Type `CNAME` · Name `recipes` · Value `cname.simple-host.app` · TTL: pick the
  smallest offered (default is 1 hour; choose 600 s / "custom" if the menu allows it).
- Apex: GoDaddy has **no ALIAS/ANAME** — the A record is the only option. A default A record
  at `@` (parking, "WebsiteBuilder Site", or "Forwarding") is already there: **edit** it (pencil
  icon) to the IP from the bind rather than adding a second one. If a *forwarding* is set up
  on the domain, turn it off under **Forwarding** or it fights the A record.
- Name is the label only; `@` means the apex. Save; GoDaddy's own nameservers pick it up in
  under a minute.

**API** — availability depends on the account, so **try once, and on `403` fall back to the UI**.
GoDaddy's current policy (https://www.godaddy.com/help/how-do-i-access-domain-related-apis-42424,
read 2026-09-06): the Domains API needs at least one active domain in the account and is capped at
20,000 calls/month; availability checks need 50+ domains or ≥US$20/month spend. Older accounts
were cut off in 2024 (the "10 domains or Discount Domain Club" rule) and some still get
`403 "Authenticated user is not allowed access"` — that is eligibility, not a bad key, and no
retry fixes it. Auth is now a **Personal Access Token** from developer.godaddy.com
(`Authorization: Bearer <PAT>`, scope `domains.dns:update`); legacy keys use
`Authorization: sso-key <KEY>:<SECRET>` and still work on v1.

Endpoints verified 2026-09-06 against https://developer.godaddy.com/openapi/domains-v1.json
(the landing page https://developer.godaddy.com/doc/endpoint/domains now fronts a v3 API whose
DNS calls live at `POST /v3/zones/{zone}/dns-records` — same body fields; v1 below is the
long-standing one). Base URL `https://api.godaddy.com`. Records are arrays; `name` is the label
(`@` for apex); v1 types are `A AAAA CAA CNAME MX NS SOA SRV TXT` (no ALIAS). GoDaddy has
historically rejected `ttl` under 600 — the spec doesn't state a minimum, so use 600.

```bash
# add (does not touch existing records) — subdomain CNAME
curl -sS -X PATCH "https://api.godaddy.com/v1/domains/$ZONE/records" \
  -H "Authorization: Bearer $GODADDY_PAT" -H 'Content-Type: application/json' \
  -d '[{"type":"CNAME","name":"recipes","data":"cname.simple-host.app","ttl":600}]'

# apex: REPLACE the existing A records at @ (PATCH would add a second A next to the parking one)
curl -sS -X PUT "https://api.godaddy.com/v1/domains/$ZONE/records/A/@" \
  -H "Authorization: Bearer $GODADDY_PAT" -H 'Content-Type: application/json' \
  -d '[{"data":"<ip from bind>","ttl":600}]'

# read back / undo
curl -sS "https://api.godaddy.com/v1/domains/$ZONE/records/CNAME/recipes" -H "Authorization: Bearer $GODADDY_PAT"
curl -sS -X DELETE "https://api.godaddy.com/v1/domains/$ZONE/records/CNAME/recipes" -H "Authorization: Bearer $GODADDY_PAT"
```

Success is an empty `200`/`204` body. `PUT .../records/{type}/{name}` replaces **all** records
of that type at that name — only ever use it for the apex A, never for `TXT`/`MX`.

**Check it landed:** GoDaddy nameservers vary by account (`ns23.domaincontrol.com`,
`ns68.domaincontrol.com`, ...) — find them first:
```bash
dig NS brand.com +short            # e.g. ns23.domaincontrol.com.
dig +short recipes.brand.com @ns23.domaincontrol.com
```

---

## Porkbun

**UI:** porkbun.com → sign in → **Account → Domain Management** → find the domain → **Details**
(or the **DNS** link on the row) → **DNS Records** (edit icon) → **Add Record**.

- Subdomain: Type `CNAME` · Host `recipes` · Answer `cname.simple-host.app` · TTL `600`.
- Apex: Type `A` · Host *blank* · Answer `<ip from bind>` · TTL `600`. Porkbun also offers
  `ALIAS` at the apex (Answer `cname.simple-host.app`); either is fine — prefer the A record
  the bind described.
- **New Porkbun domains ship with default parking records** (typically an `ALIAS` at the root
  and a `CNAME` at `*`) — the error *"A CNAME or ALIAS record with that host already exists"*
  means one is in the way. Delete only the record at the **same host** you are adding
  (root for apex; `*` never needs touching for a named subdomain). Nothing else.
- Host is the label only (blank = root). Porkbun's minimum TTL is 600 by default (account
  setting) — 600 is fine; the 60–300 advice above is "as low as the provider allows".

**API** (verified 2026-09-06 against https://porkbun.com/llms-full.txt, the text version of
https://porkbun.com/api/json/v3/documentation). Base URL **`https://api.porkbun.com/api/json/v3`**
(the old `porkbun.com` host is retired). Two prerequisites, both in the UI, both per user:

1. Keys: **Account → API Access → Create API Key** → `pk1_...` + `sk1_...` (secret shown once).
2. **Per-domain opt-in:** Domain Management → the domain's **Details** → **API Access** toggle
   ON. Without it every call for that domain fails (the error's `next_action` says "enable API
   access"). Ask the user to flip it; you cannot do it via API.

Credentials go in the JSON body (`apikey`, `secretapikey`) or as headers
`X-API-Key` / `X-Secret-API-Key`. Writes are `POST`. `name` is the label (blank/omitted =
root); `content` is the value; `ttl` minimum is the account minimum (typically 600). Pass
`"dryRun": true` first to rehearse without writing — Porkbun validates ownership/permissions and
returns `wouldSucceed`.

```bash
# ping — proves the key pair works before touching anything
curl -sS -X POST https://api.porkbun.com/api/json/v3/ping \
  -H 'Content-Type: application/json' -d "{\"apikey\":\"$PB_KEY\",\"secretapikey\":\"$PB_SECRET\"}"

# subdomain CNAME
curl -sS -X POST "https://api.porkbun.com/api/json/v3/dns/create/$ZONE" \
  -H 'Content-Type: application/json' \
  -d "{\"apikey\":\"$PB_KEY\",\"secretapikey\":\"$PB_SECRET\",\"name\":\"recipes\",\"type\":\"CNAME\",\"content\":\"cname.simple-host.app\",\"ttl\":600}"
# apex A:  ...,"name":"","type":"A","content":"<ip from bind>","ttl":600}
# → {"status":"SUCCESS","id":"123456789"}   keep id: undo is
#   POST https://api.porkbun.com/api/json/v3/dns/delete/$ZONE/$ID  (same credentials body)

# read back
curl -sS -X POST "https://api.porkbun.com/api/json/v3/dns/retrieve/$ZONE" \
  -H 'Content-Type: application/json' -d "{\"apikey\":\"$PB_KEY\",\"secretapikey\":\"$PB_SECRET\"}"
```

For the apex, if a parking `ALIAS` at root blocks the `A`, `dns/retrieve` gives its `id`;
`dns/delete/$ZONE/$ID` it, then create. `status: "ERROR"` with a `code` is the failure form;
HTTP 429 carries `Retry-After`.

**Check it landed:** Porkbun nameservers vary (`curitiba.ns.porkbun.com`,
`fortaleza.ns.porkbun.com`, `maceio.ns.porkbun.com`, `salvador.ns.porkbun.com` are common) —
find them first:
```bash
dig NS brand.com +short            # e.g. curitiba.ns.porkbun.com.
dig +short recipes.brand.com @curitiba.ns.porkbun.com
```

---

## Any other registrar / DNS host

Same record, same rules. Work out where DNS actually lives (`dig NS <zone> +short` — Cloudflare,
Route 53, Namecheap, Google/Squarespace, etc. all show up here), then:

- **UI:** the DNS editor is under the domain's settings, usually named *DNS*, *DNS Records*,
  *Manage DNS*, *Advanced DNS* or *Zone*. Fields map to Type / Name-Host (label only, `@` or
  blank for apex) / Value-Target-Answer-Points to / TTL. Some hosts (Namecheap, older panels)
  want the target with a trailing dot: `cname.simple-host.app.` is always safe.
- **Cloudflare specifically:** set the record to **DNS only** (grey cloud), not proxied — a
  proxied record hides the origin from our verifier and the cert check. Cloudflare flattens
  CNAME at the apex, so there a `CNAME @ → cname.simple-host.app` is acceptable.
- **API:** only if the user offers credentials and you can find the vendor's current docs;
  otherwise hand over the record. Same rule: one write, a read-back, forget the key.
- **Check it landed:** `dig NS <zone> +short`, then `dig +short <host> @<that nameserver>`.
  Only after the authoritative server answers correctly is a wrong answer from `8.8.8.8` a
  cache issue worth waiting out (the old TTL, worst case an hour or a day).
