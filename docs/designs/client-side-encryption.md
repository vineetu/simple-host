# Design: Encrypting user files in simple-host ("only they can read them")

Status: Draft / proposal
Author: hosting ops
Date: 2026-06-25
Scope: `github.com/vineetu/simple-host` (binary at `cmd/server`, storage in
`internal/storage/disk.go`, upload/serve in `internal/handler/site.go`)

---

## 1. Summary

The literal ask: *"when users are saving their files, is there any way to
encrypt them so that only they have access — and no one, not even me (the
operator), can read them?"*

The honest one-paragraph answer: **yes, but only for sites we stop serving as
normal public websites.** simple-host's entire job today is to take an uploaded
tarball, extract it to plaintext on disk, and let the reverse proxy serve those
bytes to *anonymous* visitors over plain HTTPS (`{site}.ideaflow.page`). If an
anonymous browser can render the page, then by definition *something the server
controls* had to hand it readable bytes — so for a normal public site the
operator can always read the content. "Operator cannot read it" and "operator
serves it to the public unauthenticated" are mutually exclusive. You can have one
or the other per site, not both.

So this doc proposes **two distinct site types**, chosen at create time:

- **Option B — at-rest envelope encryption (operator-managed keys).** Default
  for normal public sites. Protects a stolen disk or backup. Does **not** protect
  against the operator (we hold the keys to serve the page). Pure server-side Go
  change in `disk.go`; the public URL keeps working unchanged.
- **Option A — zero-knowledge private sites (client-managed keys).** The true
  answer to the user's question. The site is **not** publicly rendered. The
  server stores an opaque ciphertext blob it cannot decrypt; a tiny plaintext
  "unlock" page run in the visitor's browser decrypts it with a key derived from
  a passphrase (or carried in the URL `#fragment`) that never reaches the server.
  Only the owner and people they share the key with can read it.

Recommendation: ship **B first** (easy, broad win, no UX change), then add **A**
as an opt-in `encryption=private` site type for people who genuinely need
"not even the operator."

---

## 2. Threat model & the hard truth (read this first)

Be precise about *who* you are defending against. These are different threats and
they need different mechanisms.

| Adversary | Defended by encryption-at-rest (B)? | Defended by zero-knowledge (A)? |
|---|---|---|
| Thief who steals the disk / a backup tarball, offline | **Yes** (ciphertext + key are separated) | Yes |
| Someone who dumps Postgres | Yes (file bytes aren't in PG; wrapped keys are, but useless without the master key / passphrase) | Yes |
| A curious or compromised operator (root on the box, or with env access) | **No.** The master key is in the process env / KMS the operator controls. Root can read plaintext on disk *or* read the key and decrypt. | **Yes** (the key only ever exists in the visitor's browser) |
| A subpoena / coercion served on the operator | No (operator can be forced to hand over plaintext) | Yes (operator has nothing to hand over but ciphertext) |
| Network eavesdropper | TLS already (out of scope of file encryption) | TLS |
| Anonymous public visitor of the site | n/a — they're *supposed* to read a public site | They get only ciphertext + an unlock prompt |

**The public-serving tension, stated plainly.** Today the flow is: tarball →
`tarball.Extract` → `DiskStorage.WriteFiles` writes mode-`0644` plaintext to
`<DATA_DIR>/<site>/v<n>/` → `UpdateCurrent` copies it into `…/current/` → the
reverse proxy (cortex-share / the outer `simple-host` proxy) serves
`…/current/` straight to the public. For a browser at `https://foo.ideaflow.page`
to show HTML/CSS/JS, *someone in that chain must produce plaintext for an
unauthenticated client*. Therefore:

- If the **server/proxy** can produce that plaintext (Option B), the operator can
  too. Full stop. There is no cryptographic trick that lets a server decrypt for
  every anonymous visitor but not for its own operator.
- The only way to make the operator unable to read it (Option A) is to **stop
  serving rendered plaintext** and instead serve ciphertext plus a client-side
  decryptor, gated on a secret the client holds. That necessarily changes the
  product for those sites: they are private/unlock-gated, not open public pages,
  and the server can no longer extract, validate, index, or thumbnail them.

Do not let anyone (including marketing copy) claim "even we can't read your sites"
for the public-serving case. It is only true for Option A sites.

---

## 3. Goals / Non-goals

**Goals**
- Give users a real "operator cannot read this" mode (Option A) for private content.
- Give *all* sites a cheap disk-theft protection (Option B) with no UX change.
- Keep the single-binary + Postgres + disk architecture. No new microservices.
- Be explicit in the UI/API about which guarantee each site type provides.

**Non-goals**
- Hiding *metadata* (existence of a site, owner, version count, total/byte sizes,
  and — for B — file paths). See §10; full metadata privacy is out of scope.
- Protecting against a malicious operator who modifies the *unlock page itself*
  for Option A (a server that serves the bootstrap HTML can serve a backdoored
  one). This is the unavoidable "trust on first use" weakness of all
  server-delivered web crypto; see §10 and §12.
- DRM. Anyone who can legitimately decrypt can copy the plaintext.
- Per-file ACLs beyond "owner + explicitly shared recipients."

---

## 4. Current state (grounded in the code)

- **Plaintext on disk, world-readable mode.** `internal/storage/disk.go`
  `WriteFiles` does `os.WriteFile(dstPath, content, 0o644)` into
  `<DATA_DIR>/<site>/.v<n>.tmp` then renames to `v<n>`; `UpdateCurrent` does
  `copyDir(versionDir, currentDir)` into `…/current/`. No encryption anywhere.
- **Server inspects every upload.** `internal/handler/site.go`
  `readAndValidateFiles` calls `tarball.Extract` (`internal/tarball/extract.go`)
  to fully decompress into a `map[string][]byte`, then `tarball.ValidateExtensions`
  (`internal/tarball/validate.go`) blocks `.sh/.py/.go/...`. This only works
  because the server sees plaintext — **it cannot run on ciphertext** (relevant
  to Option A, §6).
- **Public serving is NOT in this Go binary.** Grep confirms the binary has no
  `/sites/{site}/...` `http.FileServer` route. It writes `…/current/` and the
  **external reverse proxy serves those files directly from disk.** Consequence
  for Option B: *decrypt-on-read must happen wherever bytes are served.* If we
  encrypt files at rest, the external proxy can no longer serve them as-is — so
  Option B requires either (b1) keeping the proxy serving plaintext and only
  encrypting *cold/non-current* versions, or (b2) moving public serving **into**
  the Go binary so it can decrypt in a `ServeHTTP` hook. This is the single
  biggest implementation decision for B; see §7 and §11.
- **Postgres schema** (`README.md`, `internal/db/models.go`): `users`, `sites`,
  `versions`, `auth_tokens`. `sites` has `state jsonb`. No crypto columns,
  no per-user keypair, no public keys.
- **Auth** is an `X-API-Key` header (`internal/auth`); the admin key short-circuits
  to a synthetic admin. No notion yet of "owner-only viewing" for the rendered
  site — all rendered sites are public.

---

## 5. Option A — Zero-knowledge private sites (the real answer)

**Guarantee delivered: operator genuinely cannot read the content.** The decryption
key exists only in the user's browser / device, never in our process, env, disk,
or DB.

### 5.1 Shape

The site is **private**: visiting `https://foo.ideaflow.page` does *not* render the
user's site. Instead it serves one small, static, plaintext **bootstrap/unlock
page** (the only plaintext the server holds for this site) plus opaque encrypted
blobs. The unlock page, running in the browser:

1. Obtains the key — either by prompting for a passphrase and running a KDF, or by
   reading it from the URL fragment `#key=…` (fragments are never sent to the
   server — see [MDN: Location.hash / fragment]).
2. Fetches the ciphertext blob(s) via `fetch()`.
3. Decrypts in-browser with **WebCrypto** ([MDN Web Crypto API],
   [SubtleCrypto.deriveKey]) — AES-256-GCM, key from PBKDF2/Argon2id — or with
   **libsodium.js** (XChaCha20-Poly1305 + Argon2id) for a nicer API and sealed-box
   sharing.
4. Renders the decrypted HTML/assets into the page (e.g. injects into the DOM,
   or for a multi-file site, registers a Service Worker that decrypts each asset
   on the fly and serves it to the page from an in-memory map).

The server is **never** in possession of a key and **never** produces plaintext of
the user's content.

### 5.2 Encrypt-on-upload (client side, before anything is sent)

Crucially, encryption happens **in the client tool before the tarball is sent.**
The simple-host server only ever receives ciphertext.

Per-site key model (mirrors Standard Notes / Bitwarden, see [Standard Notes
encryption], [Bitwarden security white paper]):

```
passphrase ──Argon2id(salt)──▶ master key (stays on client)
                                   │ wraps
                                   ▼
                            random content key  ◀── used to encrypt the actual files
```

- Generate a random 256-bit **content key** per site (or per version).
- Encrypt each file with the content key: `XChaCha20-Poly1305` (libsodium
  `crypto_secretbox` / `crypto_aead_xchacha20poly1305`) or `AES-256-GCM`
  (WebCrypto). Fresh random nonce per file (XChaCha's 192-bit nonce makes random
  nonces safe; for AES-GCM's 96-bit nonce use a random nonce + never reuse, or a
  counter — see §9).
- Derive a **master key** from the user's passphrase with **Argon2id** (params in
  §9). Use the master key to **wrap** (encrypt) the content key.
- Upload to the server: the wrapped content key, the Argon2 salt, per-file nonces,
  and the ciphertext. The passphrase and master key never leave the browser/CLI.

What the server stores: a single opaque encrypted archive (or a directory of
ciphertext files) + a small JSON manifest of wrapped-key/salt/nonces. **It cannot
run `tarball.Extract` or `ValidateExtensions` on this** — that's fine and expected
(see §6/§11).

### 5.3 Key handling: passphrase vs URL fragment

- **Passphrase mode**: user types a passphrase on the unlock page; Argon2id in the
  browser derives the master key; unwrap content key; decrypt. The operator never
  sees the passphrase. Best for "only me."
- **Fragment mode (`#key=…`)**: the content key (base64) lives in the URL fragment.
  The owner shares the full link `https://foo.ideaflow.page/#key=<b64>`; anyone with
  the link can read. The fragment is **not** transmitted to the server by browsers
  ([MDN fragment]), so the operator still never sees the key in logs/requests —
  but anyone the link is forwarded to can read, and it may end up in browser
  history. Best for "share a private page by link."

### 5.4 Sharing with specific people (public-key / sealed boxes)

To share with named recipients without a shared passphrase, give each user an
X25519 keypair (libsodium `crypto_box_keypair`), generated in the browser; the
**public** key is uploaded to the server, the **secret** key is wrapped by the
owner's master key (or kept locally). To share a site, the owner wraps the site's
content key once per recipient using **sealed boxes**
(`crypto_box_seal(content_key, recipientPubKey)` — anonymous, ephemeral-key,
recipient-only decryptable; see [libsodium sealed boxes]). Store one wrapped-key
row per recipient. Each recipient unwraps with their secret key
(`crypto_box_seal_open`). Revocation = rotate the content key, re-wrap for the
remaining recipients, re-encrypt (or re-upload) the site.

### 5.5 Go-side / libage alternative

If we ever want the *client tool* to be the simple-host CLI/plugin (Go) rather than
the browser, `filippo.io/age` is an excellent fit: `age.Encrypt` to a set of
`*age.X25519Recipient` (per share recipient) and/or `*age.ScryptRecipient` (a
passphrase), `age.Decrypt` with the matching identities (see [age pkg.go.dev],
[FiloSottile/age]). age gives us streaming authenticated encryption,
multiple-recipient wrapping, and a tiny, audited format for free — we'd just be
storing the age ciphertext as the opaque blob. The browser unlock page would still
need a JS age implementation (`age-encryption` npm) or libsodium to decrypt; age
and libsodium both target X25519 so they interoperate at the primitive level but
**not** at the file-format level, so pick one format end to end.

---

## 6. How the upload path must change for Option A

This is the part people underestimate. For a `private` site the server stops being
a content-aware host and becomes a dumb encrypted-blob store:

- **No extraction.** `readAndValidateFiles` in `site.go` must be bypassed for
  `private` sites. The server cannot gunzip/untar an opaque blob, and shouldn't try.
- **No extension validation.** `tarball.ValidateExtensions` is meaningless on
  ciphertext (and pointless anyway — nothing is rendered server-side). Skip it.
- **No `WriteFiles(map[string][]byte)`.** Instead store the raw uploaded
  ciphertext blob to `<DATA_DIR>/<site>/v<n>/blob.age` (or `.bin`) plus the
  client-supplied `manifest.json` (wrapped keys, salt, nonces, format version).
  A new `DiskStorage.WriteBlob(site, version, io.Reader)` method is cleaner than
  forcing it through the file-map path.
- **No deploy-script / public share** the way normal sites get it. The `private`
  site's public hostname must instead route to the **unlock bootstrap** (served by
  the binary), not to `…/current/`.
- **Versioning, rollback, delete, quota** all still work — they operate on opaque
  blobs and don't need to read content.

Trade-offs the user must accept for Option A: no server-side malware scanning, no
"did you accidentally upload your source tree" guard, no previews, no
search/indexing, larger blob (compress *before* encrypt on the client, never
after — ciphertext doesn't compress), and the unavoidable TOFU trust in the
bootstrap page (§10/§12).

---

## 7. Option B — At-rest envelope encryption for normal public sites

**Guarantee delivered: protects a stolen disk/backup. Does NOT protect against the
operator.** Say this in the UI.

### 7.1 Crypto

Envelope encryption (same pattern as cloud KMS):

- One **master key** (KEK), 256-bit, from env (`FILE_ENCRYPTION_KEY`, base64) or a
  KMS. Held by the process — i.e., by the operator. This is the whole reason B
  isn't zero-knowledge.
- Per **site** (or per version) a random **data key** (DEK), 256-bit. Encrypt files
  with the DEK using **AES-256-GCM** (`crypto/aes` + `cipher.NewGCM`) or
  XChaCha20-Poly1305 (`golang.org/x/crypto/chacha20poly1305`). Wrap the DEK with
  the KEK (AES-GCM) and store the wrapped DEK in Postgres.
- Fresh 12-byte random nonce per file for AES-GCM; store nonce as a prefix on the
  ciphertext file. Authenticate the relative path as GCM additional data (AAD) so
  files can't be swapped between paths.

### 7.2 The serving problem (decide this first)

Because public serving currently happens in the **external proxy reading
`…/current/` directly** (§4), encrypting `current/` at rest breaks public serving
unless we also move/teach decryption at the serve point. Two viable variants:

- **B1 — encrypt cold storage only.** Keep `…/current/` as plaintext (the proxy
  keeps working untouched), but encrypt the immutable historical versions
  `v1…v(n-1)` and any backups. Disk theft still exposes the *current* version of
  every site (it's plaintext), so this is weak — partial protection only.
- **B2 — bring serving into the binary (recommended for real B).** Add a
  `GET /sites/{site}/...` handler in the Go binary that reads the encrypted file,
  decrypts in memory (unwrap DEK with KEK, AES-GCM open), and streams plaintext to
  the visitor; point the proxy at the binary instead of the raw directory. Now
  **all** on-disk bytes (current + historical) are ciphertext, so a stolen disk
  is useless. Costs: a decrypt per request (cache hot files in memory), and the
  binary now owns static serving (a meaningful but contained change to
  `cmd/server/main.go` routing).

B2 is the only variant that delivers the headline "stolen disk = useless." Prefer
it if we do B at all.

Even simpler alternative to all of B: **full-disk encryption (LUKS/dm-crypt) on the
`DATA_DIR` volume**, or Postgres TDE for the DB. That protects a *powered-off*
stolen disk with zero code change, but gives nothing once the box is running
(the OS/proxy/operator all see plaintext) — same operator-trust caveat as app-level
B, but cheaper. Worth doing regardless of B as defense-in-depth.

---

## 8. Comparison: who can read what

| | Plain (today) | B: at-rest (B2) | A: zero-knowledge |
|---|---|---|---|
| Anonymous public visitor | Reads site | Reads site (normal) | Sees ciphertext + unlock prompt only |
| Owner | Reads | Reads | Reads (has passphrase/key) |
| Shared users | n/a (public) | n/a (public) | Read (sealed-box wrapped key) |
| **Operator (root/env)** | **Reads** | **Reads** (holds KEK) | **Cannot read** |
| Disk/backup thief (offline) | Reads | Cannot (ciphertext only) | Cannot |
| Postgres dump alone | n/a | Cannot (KEK not in PG) | Cannot |
| Server can validate/scan/index | Yes | Yes (decrypts on serve) | **No** |
| Public URL renders normally | Yes | Yes | No (private by design) |

Only column A answers "not even me." B is a real and worthwhile control against
the *theft* threat, but it is honest only as "encrypted at rest," never as
"we can't read it."

---

## 9. Concrete crypto parameters

Grounded in current OWASP guidance ([OWASP Cryptographic Storage Cheat Sheet],
[OWASP Password Storage Cheat Sheet]) and the referenced products.

**Symmetric content encryption**
- AES-256-GCM (WebCrypto `{name:"AES-GCM", length:256}`; Go `crypto/aes` +
  `cipher.NewGCM`), **or** XChaCha20-Poly1305 (libsodium /
  `x/crypto/chacha20poly1305.NewX`). Prefer XChaCha20 where random nonces are
  used at scale (192-bit nonce ⇒ random-nonce collision risk negligible).
- Nonce: AES-GCM 96-bit — **never reuse a (key, nonce) pair**; use `crypto/rand`
  per file and store the nonce alongside ciphertext, or rotate the DEK well before
  2^32 files. XChaCha20 192-bit — random nonce per file is safe.
- Use the file's relative path as AAD (GCM additional data) to bind ciphertext to
  its location.

**Password-based key derivation (Option A passphrase, master key)**
- **Argon2id**, OWASP minimum: `m = 19 MiB (19456 KiB), t = 2, p = 1`, or the
  alternate `m = 47104 KiB (46 MiB), t = 1, p = 1`. 16-byte random salt per site/user.
- If Argon2 isn't available (older WebCrypto only ships PBKDF2 natively):
  **PBKDF2-HMAC-SHA-256, ≥ 600,000 iterations** (OWASP 2025), 16-byte salt.
  (Bitwarden historically used PBKDF2-SHA-256; Standard Notes and Proton use
  Argon2id — prefer Argon2id via libsodium/argon2 WASM in the browser.)
- 256-bit derived key.

**Key wrapping**
- Wrap DEK/content key with the KEK/master key via AES-256-GCM or
  `crypto_secretbox` / sealed boxes (sharing). Store wrapped key + its nonce.

**Envelope KEK (Option B)**
- 256-bit, from `FILE_ENCRYPTION_KEY` (base64 env) or KMS. Rotatable (§11).

**Postgres columns (new)**
```sql
-- shared
ALTER TABLE sites ADD COLUMN encryption TEXT NOT NULL DEFAULT 'none';
  -- 'none' | 'at_rest' (B) | 'private' (A)

-- Option B: per-site wrapped data key
ALTER TABLE sites ADD COLUMN wrapped_dek BYTEA;     -- DEK encrypted under KEK
ALTER TABLE sites ADD COLUMN dek_nonce   BYTEA;
ALTER TABLE sites ADD COLUMN kek_id      TEXT;      -- which KEK version wrapped it

-- Option A: client-supplied, operator-opaque
ALTER TABLE sites ADD COLUMN kdf_salt    BYTEA;     -- Argon2id salt
ALTER TABLE sites ADD COLUMN kdf_params  JSONB;     -- {alg:'argon2id',m,t,p}
ALTER TABLE sites ADD COLUMN wrapped_content_key BYTEA;  -- owner's wrap

-- Option A sharing: one row per recipient
CREATE TABLE site_shares (
  site_id    UUID REFERENCES sites(id) ON DELETE CASCADE,
  recipient_user_id UUID REFERENCES users(id) ON DELETE CASCADE,
  sealed_key BYTEA NOT NULL,   -- crypto_box_seal(content_key, recipient_pubkey)
  created_at TIMESTAMPTZ DEFAULT now(),
  PRIMARY KEY (site_id, recipient_user_id)
);
ALTER TABLE users ADD COLUMN public_key BYTEA;          -- X25519 pub, for sharing
ALTER TABLE users ADD COLUMN wrapped_secret_key BYTEA;  -- X25519 secret, wrapped by user's master key
```
Note Postgres still stores wrapped keys for A — but they're only unwrappable with
the user's passphrase-derived master key, which the operator never has, so a PG
dump reveals nothing usable.

---

## 10. Metadata leakage (what encryption does NOT hide)

Encrypting file *content* leaves a lot visible. Be upfront:

- **Existence & ownership**: the `sites`, `versions`, `users` rows show that a
  site exists, who owns it, when, and how many versions.
- **Sizes**: per-file and total ciphertext size ≈ plaintext size (+ a few bytes
  per file). Padding can blunt this but we don't propose it initially.
- **File paths / count (Option B)**: B2 stores one ciphertext file per original
  file, so directory structure and filenames are visible on disk. To hide these,
  store a single encrypted bundle (like A) — but then the in-binary serve handler
  must decrypt the whole bundle per request. Trade-off: leak paths vs. per-request
  cost. (Option A's single opaque blob hides paths and count.)
- **Access patterns / timing**: request logs reveal when a private site is
  unlocked and roughly how big it is.
- **The bootstrap/unlock page itself (Option A)**: it is plaintext and
  server-served. A malicious operator could serve a *modified* unlock page that
  exfiltrates the passphrase/key (the classic "server can backdoor delivered web
  crypto" / TOFU problem). Mitigations: Subresource Integrity, signed releases the
  user verifies, a browser-extension or native client that ships the decryptor
  out-of-band, or publishing the unlock page hash so users can audit. This is a
  real residual risk of *any* web-delivered E2EE and should be disclosed.

---

## 11. Code integration points (real files & functions)

**Option B (server-side envelope), B2 variant:**
- `internal/config`: add `FILE_ENCRYPTION_KEY` (base64 KEK) + `kek_id`. Required
  only when any site has `encryption='at_rest'`.
- New `internal/crypto` package: `WrapDEK/UnwrapDEK`, `EncryptFile/DecryptFile`
  (AES-256-GCM via `crypto/aes`+`cipher.NewGCM`), nonce handling, AAD = rel path.
- `internal/storage/disk.go` `WriteFiles`: the single write point. Today:
  `os.WriteFile(dstPath, content, 0o644)`. Change to: fetch/generate the site DEK,
  `content = crypto.EncryptFile(dek, relPath, content)`, write ciphertext (and
  tighten mode to `0o600`). `UpdateCurrent`/`copyDir` copy ciphertext unchanged
  (they're content-blind already).
- **New serve handler** in `internal/handler` + a `mux.Handle("GET /sites/{site}/",
  …)` in `cmd/server/main.go`: read the on-disk ciphertext, unwrap DEK with KEK,
  `crypto.DecryptFile`, stream plaintext with correct `Content-Type`. Point the
  external proxy at the binary instead of the raw directory. This is the change
  that lets us encrypt `current/` without breaking public sites.
- `internal/db`: store/load `wrapped_dek`, `dek_nonce`, `kek_id` on the `sites`
  row (new columns, §9). `CreateSite`/`GetSite` extended.

**Option A (zero-knowledge):**
- `internal/handler/site.go` `createSite`/`updateSite`/`readAndValidateFiles`:
  branch on the site's `encryption` type. For `private`, **do not** call
  `tarball.Extract` or `tarball.ValidateExtensions`; read the raw body (still
  `MaxBytesReader`) and hand it to a new `DiskStorage.WriteBlob`.
- `internal/storage/disk.go`: add `WriteBlob(ctx, site, version, io.Reader)` that
  streams the opaque ciphertext to `v<n>/blob.bin` + writes `manifest.json`. No
  per-file map, no extension logic.
- New unlock route: `mux.Handle("GET /sites/{site}/", unlockHandler)` for
  `private` sites serves the embedded, static **bootstrap page** (add it under
  `internal/handler/static/`, embedded like the existing admin UI) plus an
  endpoint that streams the ciphertext blob + manifest. The bootstrap page bundles
  WebCrypto/libsodium.js + the decrypt logic.
- `internal/db`: `kdf_salt`, `kdf_params`, `wrapped_content_key` on `sites`;
  `site_shares` table; `users.public_key`, `users.wrapped_secret_key` (§9). New
  query funcs alongside the existing `internal/db/queries.go` style.
- `cmd/server/main.go`: wire the new serve/unlock routes onto the single
  `http.ServeMux` (one `mux.Handle` line each, per the repo's routing convention).

**Both:** the create API gains an `encryption` field (header or query param), and
the admin UI (`internal/handler/static/index.html`, embedded — rebuild required)
gains a site-type selector and a clear label of the guarantee.

---

## 12. Key management & rotation

- **Option B KEK**: store in env/KMS, never in the repo or DB. Rotate by
  introducing `kek_id=2`, unwrapping each site DEK with the old KEK and re-wrapping
  with the new one (a background pass over `sites`), then retiring the old KEK.
  DEKs and file ciphertext don't change — only the small wrapped-DEK blob does.
  Per-site DEKs limit blast radius if one DEK leaks.
- **Option A master key**: derived from the user's passphrase; we never store it.
  **Passphrase change / rotation** = client re-derives the new master key, re-wraps
  the (unchanged) content key, uploads the new `wrapped_content_key` + salt. The
  underlying file ciphertext is untouched. **Lost passphrase = unrecoverable** — by
  design; the operator cannot reset it. Offer optional escrow (e.g. a printable
  recovery key that wraps the content key) only with the user's explicit choice.
- **Content-key rotation / revoke a share** (Option A): generate a new content key,
  re-encrypt (re-upload) the site, re-wrap for the remaining recipients via sealed
  boxes; drop the revoked `site_shares` row.
- **Nonce discipline**: per §9, random per file; never reuse with a fixed key.

---

## 13. Recommended phasing

1. **Phase 0 (now, zero code):** enable LUKS/dm-crypt on the `DATA_DIR` volume +
   tighten file mode to `0600`. Cheap defense-in-depth for powered-off theft.
   Document plainly that it does *not* protect against the running operator.
2. **Phase 1 — Option B (B2):** envelope-encrypt at rest, move public serving into
   the binary so `current/` can be ciphertext. Default for new sites; opt-in
   migrate for existing. Headline: "encrypted at rest." Do **not** claim more.
3. **Phase 2 — Option A passphrase mode:** `encryption=private` site type, opaque
   blob storage, embedded unlock bootstrap, WebCrypto/libsodium decrypt, passphrase
   KDF (Argon2id). This is the actual answer to the user's question. Ship single-user
   first.
4. **Phase 3 — Option A sharing:** X25519 keypairs per user, sealed-box wrapped keys,
   `site_shares`, and `#key=` link mode. Optional recovery-key escrow.

---

## 14. Alternatives considered

- **Full-disk encryption only (LUKS) / Postgres TDE**: simplest, but only protects
  powered-off media; operator and running process see plaintext. Adopt as Phase 0,
  not as the answer.
- **Server-side app encryption with a server-held key (plain B without B2)**: same
  operator-trust as B but leaves `current/` plaintext — rejected as the headline
  control; only useful for cold versions (B1).
- **HTTP Basic-Auth / login-gated "private" sites without encryption**: hides the
  site from the public but the operator still reads disk plaintext — does **not**
  meet "not even me." Useful as a separate, weaker "unlisted" feature, not as A.
- **age (`filippo.io/age`) as the blob format**: strongly recommended *if* the
  client/encryptor is Go (CLI/plugin). Clean multi-recipient + scrypt-passphrase
  wrapping; small audited format ([age pkg.go.dev], [FiloSottile/age]). Needs a JS
  decryptor (`age-encryption` npm) in the browser, or use libsodium end-to-end
  instead. Pick one format across encrypt and decrypt.

---

## 15. Open questions

1. Do we want public serving to move *into* the Go binary (B2), or keep the
   external proxy and accept B1's weaker guarantee? (Biggest architectural fork.)
2. For Option A multi-file sites: decrypt-everything-then-render in the page, or a
   Service Worker that decrypts assets on demand? (UX vs. complexity.)
3. Where does the Option A encryptor live — browser-only, or the simple-host CLI /
   Website Deploy plugin (which could use Go `age`)? Affects format choice.
4. Do we offer passphrase-recovery escrow (convenience) at the cost of a
   server-held wrap, or stay strictly unrecoverable (purest zero-knowledge)?
5. Padding to hide file sizes — worth the overhead, or out of scope (§10)?
6. How do we let users *verify* the unlock bootstrap page (SRI / published hash /
   native client) to close the TOFU gap (§10)?

---

## References

- OWASP Cryptographic Storage Cheat Sheet — https://cheatsheetseries.owasp.org/cheatsheets/Cryptographic_Storage_Cheat_Sheet.html
- OWASP Password Storage Cheat Sheet (Argon2id m=19MiB/t=2/p=1; PBKDF2-SHA256 ≥600k) — https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html
- OWASP Top 10:2025 A04 Cryptographic Failures — https://owasp.org/Top10/2025/A04_2025-Cryptographic_Failures/
- MDN Web Crypto API — https://developer.mozilla.org/en-US/docs/Web/API/Web_Crypto_API
- MDN SubtleCrypto.deriveKey() (PBKDF2 → AES-GCM example) — https://developer.mozilla.org/en-US/docs/Web/API/SubtleCrypto/deriveKey
- MDN derive-key live example — https://mdn.github.io/dom-examples/web-crypto/derive-key/index.html
- libsodium — Sealed boxes (`crypto_box_seal`/`_open`) — https://doc.libsodium.org/public-key_cryptography/sealed_boxes
- libsodium.js (`crypto_box_seal`) — https://github.com/jedisct1/libsodium.js
- Standard Notes encryption (XChaCha20-Poly1305 + Argon2id, items-keys model) — https://standardnotes.com/help/security/encryption
- Bitwarden security white paper (master-password KDF, vault key) — https://bitwarden.com/help/bitwarden-security-white-paper/
- Bitwarden — encryption used — https://bitwarden.com/help/what-encryption-is-used/
- Proton Pass security (AES-256-GCM, SRP) — https://proton.me/ (Pass security model)
- filippo.io/age — Go package docs — https://pkg.go.dev/filippo.io/age
- FiloSottile/age — repo (X25519/scrypt recipients, multi-recipient) — https://github.com/FiloSottile/age
- age-encryption (JS/WASM, browser decrypt) — https://www.npmjs.com/package/age-encryption
