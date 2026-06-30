# Spec: Private Sites (one model — always zero-knowledge)

Status: Implementation-ready spec / Draft
Author: hosting ops
Date: 2026-06-25
Scope: `github.com/vineetu/simple-host`
Supersedes (narrows): `docs/designs/client-side-encryption.md` — this spec is the
concrete, buildable subset: **zero-knowledge private sites only.** It drops the
operator-key at-rest option (old "Option B") and the server-side password gate
(an earlier draft's "Tier 1") as out of scope / not built.
Composes with: `docs/designs/multi-user-editing.md` (Phase 2 sharing reuses its
collaborator model).

---

## 0. TL;DR

A **private site** is a site whose content is **client-side-encrypted before
upload**. There is exactly **one** model — it is **always zero-knowledge**:

- The owner (in the CLI / Website Deploy plugin / browser) encrypts the site
  with a random **data key (DK)** before anything is sent.
- The server stores only an **opaque ciphertext bundle**, two **wrapped copies of
  the DK** (one under the passphrase, one under an owner-held recovery code), the
  KDF/AEAD parameters, and a small static **unlock bootstrap page**.
- A visitor opens `{site}.ideaflow.page`, the unlock page derives the key in-browser
  from the passphrase (Argon2id), fetches the ciphertext, decrypts it
  (XChaCha20-Poly1305 / AES-256-GCM), and renders it via a Service-Worker virtual FS.

**The operator never holds the key and cannot read the content at rest** (subject
to the honest trust caveat in §2).

### The headline simplification

Because the server only ever holds **opaque ciphertext + a static unlock shell**,
**the ciphertext leaks nothing without the passphrase.** Confidentiality comes
from the *encryption*, not from any server-side access control. Therefore:

> **The server does NOT need to authenticate or gate viewers at all.**

There is no login, no session cookie, no server-side password hash, no
"operator-can-read" tier. An anonymous visitor hitting the URL gets ciphertext +
an unlock prompt — which is exactly the intended public surface.

This collapses what an earlier draft called the "load-bearing routing problem."
That draft wanted to route private-site traffic *through the Go binary* so the
server could enforce auth. **With zero-knowledge there is nothing to enforce at
serve time**, so the encrypted bundle, the unlock page, and the Service Worker are
just **ordinary static files**. The **existing serving path serves them unchanged**
(verified against the code in §1/§4): a private site is deployed exactly like any
other static site — it simply *contains* ciphertext + an unlock shell instead of
plaintext HTML. Nothing changes on the hot path.

The one residual leak is **metadata**: the proxy still sees the ciphertext's size,
the unlock page, and the bundle's existence/URL — never its content (§8).

---

## 1. Grounding in the current code (file:line)

Read before trusting any summary:

- **No visitor-facing static serving lives in the Go binary, and that's fine.**
  `cmd/server/main.go:62-65` mounts only `RegisterHealthRoutes`, the user handler,
  the site handler (`NewSiteHandler(...).Register`), and `RegisterUIRoutes`. The
  site handler registers only `/v1/sites/...` REST routes and
  `/v1/sites/{sitename}/state` (`internal/handler/site.go:61-74`) — there is **no**
  `GET /sites/{site}/...` route. The only `http.FileServer` in the whole binary is
  `internal/handler/ui.go:69,79` (`http.FileServerFS(sub)` mounted at `GET /`) — the
  **embedded admin UI**, not site content.
- **Visitor bytes are served by the external proxy reading `current/` off disk.**
  The ops runbook (`/root/workspace/CLAUDE.md`) is authoritative: `simple-host`
  "serves the registered static/website shares" via **cortex-share** registration,
  and the shared **ideaflow/nginx proxy** fronting `*.ideaflow.page` reads
  `<DATA_DIR>/<site>/current/` straight from disk. (The README architecture diagram
  showing the binary serving site bytes via `http.FileServer` is aspirational; no
  such route exists.) **This is the fact that makes the whole feature cheap:** an
  encrypted bundle + unlock page placed in `current/` is served by the existing path
  with zero new code.
- **Deploy is content-blind and needs no change.** After a successful upload,
  `createSite`/`updateSite` simply append the site name to `<DATA_DIR>/.deploy-queue`
  (`internal/handler/site.go:277-283`); `deploy-watcher` registers the cortex-share.
  A private site rides this **same** queue — it is a normal `file`/`website` share
  whose files happen to be ciphertext + an unlock shell.
- **Upload path extracts + validates plaintext — and we must skip that for
  ciphertext.** `internal/handler/site.go:570-589` `readAndValidateFiles` reads the
  body (`readLimitedBody`, 100 MiB cap, `site.go:591`), calls `tarball.Extract`
  (`internal/tarball/extract.go:16`) to decompress into a `map[string][]byte`, then
  `tarball.ValidateExtensions` (`internal/tarball/validate.go:35`) which blocks
  `.sh/.py/.go/...` (`validate.go:19-33`). Both operate on plaintext and cannot run
  on an opaque ciphertext blob (§5).
- **Disk writes are plaintext, mode 0644.** `internal/storage/disk.go:29-74`
  `WriteFiles` does `os.WriteFile(dstPath, content, 0o644)` (`disk.go:61`) into
  `.v<n>.tmp`, renames to `v<n>`; `UpdateCurrent` (`disk.go:76-90`) `copyDir`s into
  `current/` (`disk.go:104-151`, content-blind byte copy). For an encrypted site the
  "files" written are simply ciphertext + the unlock shell.
- **Schema** (`README.md:64-72`, `internal/db/models.go:13-24`): `sites(id, user_id,
  name, active_version, site_url, state jsonb, created_at, updated_at)`. No crypto or
  visibility columns. No migration framework — raw `CREATE TABLE`/`ALTER` SQL.
- **Auth** is `X-API-Key` only (`internal/auth/middleware.go:32-68`) and gates the
  *management* API (who may upload). It has **nothing** to do with viewing a site —
  and this spec adds **no** new auth surface (no session cookies).
- **Embedded static assets** live under `internal/handler/static/`
  (`ui.go:21-22`, `//go:embed all:static`). The unlock shell *could* be embedded
  there, but because it just needs to be a static file in the bundle, it can equally
  be emitted by the client encryptor — see §3 and §6.

---

## 2. Trust model — read this honestly

| Adversary | Zero-knowledge private site |
|---|---|
| Anonymous internet visitor | Sees only ciphertext + the unlock prompt |
| Disk / backup thief (offline) | Cannot read (ciphertext only on disk) |
| Postgres dump alone | Reads wrapped-DK blobs + salts — useless without passphrase/recovery code |
| Curious / compromised operator (root, env) | **Cannot read at rest** — the key only ever exists in the visitor's browser |
| Subpoena on operator | Operator has only ciphertext to hand over |

**Honest caveat (state this in the UI and docs — do not overpromise):** in a
browser-delivered app **the operator serves the unlock JavaScript**. A malicious or
compromised operator could serve **backdoored unlock code** that captures the
passphrase as the visitor types it. This is the unavoidable "trust on first use" /
"server can backdoor delivered web crypto" weakness of *all* web-delivered E2EE.

So a private site protects against **passive operator access, disk/backup theft, DB
dumps, and casual snooping** — the strongest guarantee a *web host* can offer — but
it is **NOT equivalent to a native zero-knowledge app** (Bitwarden / Standard Notes
class), where the decryptor ships out-of-band and the server cannot alter it.
Partial mitigations (not full fixes): Subresource Integrity on the unlock bundle, a
published/pinned hash users can audit, or a browser-extension / CLI decryptor shipped
separately. We disclose this; we do not claim "even we can't read it" without the
asterisk.

Out of scope: hiding **metadata** — that a site exists, its owner, version count,
total ciphertext size, and (depending on bundling, §8) file paths/count. With
content fully encrypted, **metadata is now the only thing that leaks** — §8.

---

## 3. Data model — manifest-in-bundle, not a DB table (recommended)

The protection metadata a private site needs is small and entirely
**operator-opaque** (the server can't use any of it): the AEAD algorithm, the KDF
algorithm + params, two salts, and two wrapped-DK blobs. The earlier draft put this
in a `site_protection` Postgres table with a `tier` column and an Argon2id
*password-gate* hash. **With the gate gone, the tier column and the password hash
are gone**, and the remaining fields no longer need a DB row at all.

**Recommendation: ship the metadata as a small JSON `manifest.json` inside the site
bundle on disk** (alongside the ciphertext), not as a Postgres table. Rationale now
that routing has dissolved:

- The metadata is **client-supplied, operator-opaque, and version-scoped** — it
  travels with the ciphertext it describes. Keeping it next to the blob means a
  version rollback automatically restores the matching salts/wraps; a separate DB
  row would have to be versioned in lockstep.
- The server **never reads it** — only the visitor's browser does. There is no query
  the Go binary needs to run against it, so a DB table buys nothing.
- It rides the **existing static-serving path** for free: the unlock page just
  `fetch('manifest.json')`. No new endpoint, no new `queries.go` funcs, no SELECT
  column changes.
- It keeps the change **additive and DB-free**: no `ALTER TABLE`, no new model field.

```jsonc
// current/manifest.json  — operator-opaque, read only by the unlock page
{
  "format": 1,
  "aead_alg": "XChaCha20-Poly1305",     // or "AES-256-GCM"
  "kdf_alg":  "argon2id",
  "kdf_params": { "m": 19456, "t": 2, "p": 1 },   // KiB, iters, lanes
  "salt_p": "<base64 16B>",             // KDF salt for the passphrase
  "wrapped_dk_p": "<base64>",           // wrap(KDF(passphrase, salt_p), DK), nonce+tag inline
  "salt_r": "<base64 16B>",             // KDF salt for the recovery code
  "wrapped_dk_r": "<base64>",           // wrap(KDF(recovery_code, salt_r), DK), nonce+tag inline
  "blob": "blob.bin"                    // the opaque ciphertext bundle
}
```

**Do we even need a `sites.visibility` column?** Mostly no. Since serving is
identical for public and private sites, `visibility` would be a pure **label**
(semantics/UX: "this site's content is client-encrypted; the URL alone reveals
nothing"), not a serving-time switch. The simplest thing that works:

- **Detect, don't flag.** "Private" ≡ "a site whose bundle contains a
  `manifest.json` + ciphertext blob" — the upload path already knows this (the
  client sets a header, §4). No column required for correctness.
- **Optional convenience label.** If the admin UI wants a badge without sniffing the
  bundle, add a single additive, defaulted column
  `ALTER TABLE sites ADD COLUMN visibility TEXT NOT NULL DEFAULT 'public' CHECK (visibility IN ('public','private'));`
  and surface it in `siteResponse`. This is **cosmetic only** — it changes no
  serving or auth behavior. **Recommendation: skip it for Phase 1**; add the label
  later if the UI needs it. (If added, remember `CreateSite`/`GetSite`
  `queries.go:70,95` select explicit column lists.)

---

## 4. API surface changes (minimal)

Reuse the existing handler/registration style (`site.go:61-74`).

- **One new request header on create/update** (consistent with the
  raw-tarball-body convention — the body is the archive, config rides in headers):
  - `X-Site-Encrypted: 1` — tells the upload handler "this body is an opaque
    encrypted bundle; do not extract or validate it" (§5). Absent / `0` = today's
    normal plaintext path, unchanged.
- **No new visitor-facing routes.** The unlock page, `manifest.json`, and ciphertext
  are static files in `current/`, fetched over the existing proxy path. There is no
  `GET /sites/{site}/...` gate, no `__login`, no `__unlock`, no session endpoint —
  all of that machinery is **removed** vs. the earlier draft.
- **No recovery-code transit.** The recovery code is generated client-side, shown to
  the owner once, and never sent (§7). Only `wrapped_dk_r` (useless without the code)
  is uploaded, inside the bundle's `manifest.json`.

That's the entire API delta: **one boolean header.**

---

## 5. Upload path: store the opaque bundle (cite what's bypassed)

For an encrypted upload (`X-Site-Encrypted: 1`) the server becomes a dumb
encrypted-blob store. Branch *before* extraction in `readAndValidateFiles`
(`site.go:570-589`) / `createSite` / `updateSite`:

- **Skip `tarball.Extract`** (`extract.go:16`) — you cannot gunzip/untar an opaque
  ciphertext blob, and must not try.
- **Skip `tarball.ValidateExtensions`** (`validate.go:35`) — meaningless on
  ciphertext (and pointless: nothing is rendered server-side). The denylist
  (`validate.go:19-33`) was only ever a "did you upload your source tree" guardrail
  (`validate.go:9-18`), not a safety control.
- **Skip the per-file `WriteFiles` map write** (`disk.go:29`). Add
  `DiskStorage.WriteBlob(ctx, site, version, io.Reader)` that streams the ciphertext
  to `<DATA_DIR>/<site>/v<n>/blob.bin` and writes the client-supplied `manifest.json`
  + the embedded `index.html` unlock shell — tightening mode to `0o600` for the blob.
- Concretely: read the raw body (still wrapped in `http.MaxBytesReader`,
  `site.go:591`) and hand it to `WriteBlob`; then `UpdateCurrent` (`disk.go:76`)
  promotes `v<n>` to `current/` exactly as for any site. After that, the **existing**
  `.deploy-queue` append (`site.go:277-283`) registers the share — no private-site
  branch in the deploy logic.

**Security tradeoff (state it):** the server loses all upload introspection — no
extension guard, no "you uploaded your `.git`" guard, no previews/indexing. That is
inherent to zero-knowledge and acceptable: the bytes are inert ciphertext on the
server and only execute in the owner's own browser after the owner decrypts them.
**Versioning, rollback, delete, and quota all still work** — they operate on opaque
blobs.

**Where the encryptor lives — implications for the Website Deploy plugin/MCP.** The
plugin (`simple-host-website/`, embedded MCP server) currently shells the plaintext
tarball to `POST/PUT /v1/sites/{name}`. For a private site the encryptor must be
added to that client path: either (a) a JS/WASM encrypt step in the MCP server
(Node, libsodium-wrappers), or (b) a browser-only "encrypt & upload" page so the
passphrase never touches the agent/CLI process. **Recommendation: browser-only
encryptor for the strongest story** (the passphrase never transits an agent
process); offer the MCP/CLI path only with an explicit "your passphrase passes
through this tool" disclosure. Either way the *server* upload handler is unchanged in
shape — it receives an opaque blob.

---

## 6. In-browser unlock + render

1. Visitor opens `{site}.ideaflow.page` → the proxy serves `current/index.html`, the
   **unlock shell** (the only plaintext the server holds for the site). This is an
   ordinary static page — no server logic runs.
2. The shell prompts for the passphrase and runs **Argon2id in-browser** (libsodium.js
   `crypto_pwhash`, or a WASM argon2 if using WebCrypto for the AEAD) over `salt_p`
   from `manifest.json` → `K_p`; unwraps `DK` from `wrapped_dk_p`.
3. It `fetch`es the ciphertext `blob.bin`, decrypts the bundle with `DK`
   (XChaCha20-Poly1305 via libsodium.js, or AES-256-GCM via WebCrypto), and expands
   it into an in-memory `Map<path, bytes>`.
4. **Render via a Service-Worker virtual FS.** A Service Worker registered by the
   shell intercepts `fetch` for the site's sub-resources and serves them from that map
   ([sw-crypto] demonstrates exactly this decrypt-on-the-fly SW pattern). This makes
   multi-file sites, relative asset URLs, and most SPA routing "just work" because the
   page sees normal same-origin URLs.
   - **Limits to name plainly:** (a) the whole site decrypts up front before first
     paint — fine for small/medium sites, heavy for large ones; (b) deep-link / hard-
     refresh into an SPA route requires the SW to be already installed (first visit
     must land on `/`); (c) absolute external / cross-origin URLs bypass the SW
     (expected); (d) Service Workers require HTTPS (always true here) and a stable
     scope under the site origin.
   - **Simpler fallback for single-file sites:** if the site is one self-contained
     HTML file, skip the SW and inject decrypted HTML into the document directly.
     Offer this "single-page" mode; use the SW VFS for everything else.

The unlock shell + bundled `libsodium.js`/WebCrypto + decrypt JS can either be
**embedded in the binary** (`internal/handler/static/`, rebuild required per
`CLAUDE.md`) and copied into each private bundle at deploy time, or **emitted by the
client encryptor** so it travels with the ciphertext. The latter keeps the server
out of it entirely and is the simplest fit with "private = a bundle of ciphertext +
shell"; the former centralizes the crypto code for easier auditing/SRI (§2).
**Recommendation: emit from the client encryptor, publish a pinned hash for audit.**

---

## 7. Recovery model — owner-held recovery code (zero-knowledge preserved)

Envelope / key-wrapping with **two independent wraps of the same DK**:

```
random DK (256-bit)  ── encrypts ──▶  the site's files (one bundle)
   │
   ├─ wrap(  KDF(passphrase,    salt_p), DK )  ─▶ wrapped_dk_p   (in manifest.json)
   └─ wrap(  KDF(recovery_code, salt_r), DK )  ─▶ wrapped_dk_r   (in manifest.json)
```

- At **setup**, the client generates a high-entropy **recovery code**, derives `K_r`,
  produces `wrapped_dk_r`, and **shows the code to the owner exactly once**. It is
  **never sent to the server, never stored plaintext anywhere, never visible to the
  operator** — only `wrapped_dk_r` (useless without the code) is uploaded.
- **Forgotten passphrase** → owner enters the recovery code on the unlock page →
  derive `K_r` → unwrap `DK` from `wrapped_dk_r` → decrypt. Recovery is just a second
  unlock path.
- **Change passphrase** = client re-derives `K_p'` from the new passphrase + a fresh
  `salt_p`, recomputes `wrapped_dk_p'`, uploads only the new wrap+salt. **No file
  re-encryption** — `DK` is unchanged. Same for rotating the recovery code
  (`wrapped_dk_r`).
- The operator stores only `{ ciphertext, wrapped_dk_p, wrapped_dk_r, salt_p, salt_r,
  params }` → **still cannot decrypt anything.** A PG dump + disk image reveals no
  usable key.
- **Recovery code entropy & format:** ≥128-bit, from `crypto.getRandomValues`,
  rendered as **Crockford base32** in grouped chunks (e.g.
  `XXXX-XXXX-XXXX-XXXX-XXXX-XXXX`, 26 base32 chars ≈ 130 bits). Base32 is
  case-insensitive and avoids ambiguous characters — friendly to print / write down.
  (Mirrors age's scrypt-passphrase recovery ergonomics; see [age].)

This is strictly stronger than "lost passphrase = unrecoverable" while preserving
zero knowledge: recovery is **owner-held, not operator-held**. This spec **mandates**
the recovery-code wrap as the standard recovery path.

---

## 8. Crypto parameters (OWASP 2025 + WebCrypto/libsodium)

**Password KDF (passphrase → `K_p`, recovery code → `K_r`):**
- **Argon2id**, OWASP baseline **m = 19456 KiB (19 MiB), t = 2, p = 1**, 16-byte
  random salt, 256-bit output. (OWASP's equivalent alternative is m=46 MiB/t=1/p=1;
  pick the 19 MiB/t=2 baseline — friendlier for in-browser WASM on low-end devices.)
  ([OWASP Password Storage].)
- In-browser: libsodium.js `crypto_pwhash` (Argon2id) — set memlimit/opslimit to
  match. If forced to pure WebCrypto (which lacks native Argon2), fall back to
  **PBKDF2-HMAC-SHA-256, ≥ 600,000 iterations** ([OWASP], [MDN deriveKey]) — but
  prefer Argon2id via WASM.

**Symmetric content encryption (DK encrypts files; K_p/K_r wrap DK):**
- **XChaCha20-Poly1305** (libsodium `crypto_aead_xchacha20poly1305` /
  `crypto_secretbox`; Go `golang.org/x/crypto/chacha20poly1305.NewX`) **or**
  **AES-256-GCM** (WebCrypto `{name:"AES-GCM", length:256}`,
  [MDN SubtleCrypto.encrypt]; Go `crypto/aes` + `cipher.NewGCM`).
- **Nonce handling:** XChaCha20's 192-bit nonce makes random nonces collision-safe at
  any practical scale — **prefer XChaCha20-Poly1305 for the file bundle.** AES-256-GCM
  (96-bit nonce) is fine for the small one-shot DK wraps (one nonce each); generate
  fresh per encryption with a CSPRNG and **never reuse a (key, nonce) pair**.
- For wraps, store nonce+tag inline in `wrapped_dk_p` / `wrapped_dk_r`.

**age alternative (if the encryptor is Go, e.g. CLI/plugin):** `filippo.io/age` gives
streaming AEAD, scrypt-passphrase + X25519-recipient wrapping, and a tiny audited
format for free — store the age ciphertext as the opaque blob. Needs a JS age
decryptor (`age-encryption` npm) in the browser. **Pick one format end-to-end**
(libsodium *or* age, not both). ([age].)

---

## 9. Metadata leakage — the only thing that leaks

With content fully encrypted, metadata is now the **sole** residual leak. Be upfront:
- **Existence/ownership/timing:** `sites`, `versions`, `users` rows reveal the site
  exists, who owns it, version count, and when it was uploaded; request logs reveal
  when it was unlocked.
- **Size:** total ciphertext ≈ plaintext + small AEAD overhead.
- **File paths & count:** controlled by how the client bundles:
  - **one opaque bundle** (recommended) → hides paths, filenames, and file count; the
    in-browser SW VFS (§6) expands it client-side.
  - per-file ciphertext → leaks directory structure and filenames. Don't do this.
- **The unlock shell + bundle URL** are visible (they have to be — they're served).
- **Recommendation: one opaque XChaCha20-Poly1305 bundle.** Per-file size padding to
  hide exact sizes is **out of scope** initially (overhead not worth it); note it as a
  future option.

---

## 10. Phased rollout

- **Phase 1 — zero-knowledge private site, end-to-end (single owner).**
  - Client encryptor (browser-first) that: generates DK, compresses *then* encrypts
    the site into one bundle, derives `K_p`/`K_r`, writes `manifest.json`, generates
    and one-time-shows the recovery code, and emits the unlock shell.
  - Server upload branch on `X-Site-Encrypted` → `DiskStorage.WriteBlob` (skip
    `Extract`/`ValidateExtensions`, §5).
  - The unlock shell + Service-Worker VFS render (§6).
  - **No** new serving route, **no** auth/session work, **no** DB migration required
    (manifest-in-bundle, §3). The deploy/share path is unchanged.

  This delivers the full "operator cannot read it" private site. It is the whole
  product; everything heavy in the earlier draft (in-binary gate, session cookies,
  deploy-script branching) is gone.

- **Phase 2 — sharing with other users (future, brief).** Composes with
  `multi-user-editing.md`'s `site_collaborators(site_id, user_id, role)` model.
  Sharing = **wrap DK once per recipient public key** (X25519 sealed boxes / age
  recipients): give each user a keypair (public key stored, secret key wrapped by
  their own master key), and add a `site_dk_wraps(site_id, recipient_user_id,
  wrapped_dk)` row per grant (this *is* worth a DB table — it's per-user, queried by
  recipient). Revocation = rotate DK + re-wrap for remaining recipients + re-upload.
  Keep this out of Phase 1.

---

## 11. Open questions

1. **Encryptor home:** browser-only (strongest — passphrase never touches an agent)
   vs. MCP/CLI (smoother agent UX — passphrase transits the tool). Recommend
   browser-only default; allow CLI with explicit disclosure.
2. **SW VFS vs. inline:** confirm the Service-Worker VFS covers the real sites people
   host (mostly small SPAs/static bundles). For very large sites, decrypt-up-front
   latency may warrant streaming/range decryption — defer.
3. **Bootstrap integrity:** publish a pinned hash / SRI for the unlock shell so users
   can audit it and partly close the TOFU gap (§2)? Recommend yes — a doc + a published
   `index.sha256` users can check — even though it is not a full fix.
4. **Manifest-in-bundle vs. a DB row:** this spec recommends the manifest file (§3).
   The only reason to revisit is if we later want the server to *enumerate* private
   sites' crypto params without reading disk — which nothing currently needs.
5. **Cosmetic `visibility` label:** add the additive `sites.visibility` column for a
   UI badge, or detect "private" by sniffing the bundle? Recommend defer (§3).

---

## 12. Final recommendation

Ship **Phase 1**: a private site is **always zero-knowledge**, encrypted client-side
before upload into **one opaque XChaCha20-Poly1305 bundle**, with **Argon2id
(m=19 MiB, t=2, p=1)** in-browser, a **Service-Worker VFS** for rendering, and a
**mandatory owner-held base32 recovery code** via a double-wrapped DK.

The key realization that makes this small: **zero-knowledge means there is nothing to
gate at serve time**, so the encrypted bundle + unlock shell are plain static files
served by the **existing proxy path with no new code** — no in-binary route, no
session cookies, no password hash, no DB migration. The server change is **one upload
branch** (`X-Site-Encrypted` → `WriteBlob`, skipping `tarball.Extract` /
`ValidateExtensions`) plus a client-side encryptor and unlock shell. Be honest in the
UI: this is the strongest a web host can offer (operator cannot read at rest) but is
not a native zero-knowledge app, because we still serve the unlock JS.

---

## References

- OWASP Password Storage Cheat Sheet (Argon2id m=19MiB/t=2/p=1; PBKDF2-SHA256 ≥600k) — https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html
- OWASP Cryptographic Storage Cheat Sheet — https://cheatsheetseries.owasp.org/cheatsheets/Cryptographic_Storage_Cheat_Sheet.html
- OWASP Top 10:2025 A04 Cryptographic Failures — https://owasp.org/Top10/2025/A04_2025-Cryptographic_Failures/
- MDN Web Crypto API — https://developer.mozilla.org/en-US/docs/Web/API/Web_Crypto_API
- MDN SubtleCrypto.encrypt() (AES-GCM) — https://developer.mozilla.org/en-US/docs/Web/API/SubtleCrypto/encrypt
- MDN SubtleCrypto.deriveKey() (PBKDF2 → AES-GCM) — https://developer.mozilla.org/en-US/docs/Web/API/SubtleCrypto/deriveKey
- libsodium — Sealed boxes (`crypto_box_seal`/`_open`) — https://doc.libsodium.org/public-key_cryptography/sealed_boxes
- libsodium.js — https://github.com/jedisct1/libsodium.js
- libsodium — password hashing (`crypto_pwhash`, Argon2id) — https://doc.libsodium.org/password_hashing/default_phf
- sw-crypto — Service-Worker on-the-fly WebCrypto decryption — https://github.com/wiktor-k/sw-crypto
- filippo.io/age — Go package docs — https://pkg.go.dev/filippo.io/age
- FiloSottile/age — https://github.com/FiloSottile/age
- age-encryption (JS/WASM browser decrypt) — https://www.npmjs.com/package/age-encryption
- golang.org/x/crypto/chacha20poly1305 — https://pkg.go.dev/golang.org/x/crypto/chacha20poly1305
