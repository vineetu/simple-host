// Shared helpers for the golden-snapshot harness (run.mjs) and its diff tool
// (diff.mjs). Node 20+ built-ins only -- no npm dependencies.
//
// normalize() turns a captured HTTP exchange into something that compares
// stable across two runs of the *same* binary (random ids/keys/tokens,
// per-run temp paths and ports) while still catching a real behaviour
// change between two *different* binaries (e.g. a status code or a field
// that should not have moved).

import { createHash } from 'node:crypto';

// A Namer hands out "<category:N>" placeholders for distinct raw values, in
// order of first appearance, and remembers them so the same raw value
// always maps to the same placeholder everywhere it shows up again later in
// the same run (a site id minted in a create response and echoed back in
// ten later GETs must normalize to the same token every time).
export class Namer {
  constructor() {
    this.seen = new Map(); // category -> Map(rawValue -> placeholder)
    this.counts = new Map(); // category -> next number
  }

  placeholderFor(category, rawValue) {
    let table = this.seen.get(category);
    if (!table) {
      table = new Map();
      this.seen.set(category, table);
      this.counts.set(category, 1);
    }
    let placeholder = table.get(rawValue);
    if (placeholder === undefined) {
      const n = this.counts.get(category);
      placeholder = `<${category}:${n}>`;
      table.set(rawValue, placeholder);
      this.counts.set(category, n + 1);
    }
    return placeholder;
  }
}

// Patterns recognised anywhere in a string value, independent of the
// Namer's per-run exact-string substitutions below (ports, tmp dirs):
//   - a UUID                          -> <id:N>
//   - shk_<64 lowercase hex> API key  -> <key:N>
//   - a bare 64-hex-char token        -> <token:N>  (session/export tokens etc)
//   - an ISO-8601 timestamp           -> <ts>
// Order matters: UUID and API-key patterns are checked before the generic
// hex-token pattern so a key isn't double-counted as a token.
const UUID_RE = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi;
const API_KEY_RE = /shk_[0-9a-f]{64}/gi;
const HEX_TOKEN_RE = /\b[0-9a-f]{32,64}\b/gi;
const ISO_TS_RE = /\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})/g;
// hackHandle() (internal/handler/hack_mode.go) mints a hack-mode account's
// handle as "u-" + 10 random hex chars -- unlike a Host-mode handle (derived
// from the account's own email/username, so already stable), this one is
// random every run and then echoed back wherever that account's handle
// shows up (site/team URLs, "who created this").
const HACK_HANDLE_RE = /\bu-[0-9a-f]{10}\b/g;
// signPreviewToken (internal/handler/preview.go) seals (site, version,
// *an expiry a few minutes from now*) with a fresh AEAD nonce every call --
// so even a request for the exact same site+version mints a new token each
// time it's signed, deterministic-per-run only in that the Nth distinct
// token to appear is always the Nth one this fixed request sequence asks
// the server to mint. base64.RawURLEncoding, so an unpadded run of its
// alphabet right after the "/__preview/<version>/" path segment.
const PREVIEW_TOKEN_RE = /(?<=__preview\/\d+\/)[A-Za-z0-9_-]{20,}/g;
// uiCSP (internal/handler/ui.go) stamps a fresh 16-byte base64 nonce onto
// every admin/landing page's inline <script> tags on every response, purely
// for that response's Content-Security-Policy -- never meant to repeat, and
// never meaningful to compare.
const CSP_NONCE_RE = /nonce="[A-Za-z0-9+/]{20,24}=*"/g;

// An empty, shared default so callers that have no known seed timestamps
// (or call normalizeText directly on a header/path string) don't each need
// their own placeholder Set.
const NO_KNOWN_TIMESTAMPS = new Set();

// normalizeText rewrites one string value (a JSON string field, or a whole
// HTML/text body) by:
//  1. substituting every exact-string, per-run dynamic value the harness
//     itself generated (temp dirs, chosen ports, build version/commit) --
//     those are passed in as `exactReplacements` (Map of raw -> placeholder,
//     already resolved by the caller, e.g. {"<tmp dir path>": "<tmpdir>"});
//  2. then pattern-matching ids/keys/tokens/timestamps and mapping each
//     distinct raw value to a stable placeholder via `namer` -- except an
//     ISO timestamp that exactly matches a value the seed itself supplied
//     in a request (an event's starts_at/ends_at, given as an absolute
//     date in run.mjs), which is left as-is: it is not server-generated
//     noise, it is seeded input the server only echoes back, so a real
//     regression in date handling should still show up in a diff instead
//     of being normalized away with everything else.
export function normalizeText(value, namer, exactReplacements, knownTimestamps = NO_KNOWN_TIMESTAMPS) {
  if (typeof value !== 'string') return value;
  let s = value;
  for (const [raw, placeholder] of exactReplacements) {
    if (raw) s = s.split(raw).join(placeholder);
  }
  s = s.replace(ISO_TS_RE, (m) => (knownTimestamps.has(m) ? m : '<ts>'));
  s = s.replace(UUID_RE, (m) => namer.placeholderFor('id', m.toLowerCase()));
  s = s.replace(API_KEY_RE, (m) => namer.placeholderFor('key', m.toLowerCase()));
  s = s.replace(HEX_TOKEN_RE, (m) => namer.placeholderFor('token', m.toLowerCase()));
  s = s.replace(HACK_HANDLE_RE, (m) => namer.placeholderFor('handle', m.toLowerCase()));
  s = s.replace(PREVIEW_TOKEN_RE, (m) => namer.placeholderFor('previewtoken', m));
  s = s.replace(CSP_NONCE_RE, 'nonce="<nonce>"');
  return s;
}

// ALWAYS_VOLATILE_FIELD_NAMES are JSON object keys blanked everywhere, by
// name, regardless of which request they show up in. Unlike the removed
// VOLATILE_KEYS (commit/network/disk_* -- see normalizeValue below), each of
// these is a narrow, single-purpose field name used nowhere in this codebase
// for anything other than a fragment of a freshly minted secret:
//   - "last4": the last 4 characters of a brand-new API/deploy key, shown
//     once so the owner can tell keys apart -- internal/handler/keys.go,
//     hack.go, hack_keys.go all use it this way and no other.
//   - "passcode": a site visitor-lock passcode, only ever present in a
//     response when PUT .../lock just generated or is reporting one --
//     internal/handler/site.go, passcode.go. (hack_entries.go has an
//     unrelated *string* value "passcode" under the key "not_frameable_why";
//     this only matches the JSON *key*, so that is untouched.)
const ALWAYS_VOLATILE_FIELD_NAMES = new Set(['last4', 'passcode']);

// normalizeValue walks a parsed JSON value recursively, normalizing every
// string leaf, and sorts object keys so two structurally-identical objects
// serialize identically regardless of insertion order. keyName is the
// object key this value was found under (undefined for array elements and
// the document root) -- used only for ALWAYS_VOLATILE_FIELD_NAMES above.
//
// There used to be a much broader version of that same idea here (a global
// VOLATILE_KEYS set blanking any field called e.g. "commit" or "network"
// anywhere in any response). That was imprecise: those fields only ever
// appear in GET /v1/admin/usage's response, so run.mjs now blanks them by
// request name instead, the same way it already scopes "message"/"status"
// (see EXTRA_VOLATILE_FIELDS in run.mjs) -- a field whose name could
// plausibly carry an unrelated, meaningful value elsewhere is never hidden
// just because it shares a key name. ALWAYS_VOLATILE_FIELD_NAMES is a much
// smaller, deliberately-checked list of names that have no other use.
export function normalizeValue(value, namer, exactReplacements, knownTimestamps = NO_KNOWN_TIMESTAMPS, keyName) {
  if (ALWAYS_VOLATILE_FIELD_NAMES.has(keyName)) return '<volatile>';
  if (Array.isArray(value)) {
    return value.map((v) => normalizeValue(v, namer, exactReplacements, knownTimestamps));
  }
  if (value && typeof value === 'object') {
    const out = {};
    for (const k of Object.keys(value).sort()) {
      out[k] = normalizeValue(value[k], namer, exactReplacements, knownTimestamps, k);
    }
    return out;
  }
  if (typeof value === 'string') {
    return normalizeText(value, namer, exactReplacements, knownTimestamps);
  }
  return value;
}

// normalizeSetCookie turns a run's raw Set-Cookie header values into a short,
// deterministic, sorted list: each cookie's own value (a session token,
// nearly always run-specific) is replaced outright, its name and attributes
// (Path, HttpOnly, SameSite, an Expires date, ...) are kept and still run
// through normalizeText (so e.g. an Expires timestamp still normalizes),
// and the whole list is sorted so two runs that set the same cookies in a
// different order still compare equal.
export function normalizeSetCookie(cookies, namer, exactReplacements, knownTimestamps = NO_KNOWN_TIMESTAMPS) {
  const normalized = cookies.map((raw) => {
    const parts = raw.split(';').map((p) => p.trim());
    const name = parts[0].split('=')[0];
    const attrs = parts.slice(1).map((p) => normalizeText(p, namer, exactReplacements, knownTimestamps));
    return [`${name}=<value>`, ...attrs].join('; ');
  });
  normalized.sort();
  return normalized;
}

export function sha256hex(s) {
  return createHash('sha256').update(s, 'utf8').digest('hex');
}

// A tiny deterministic stringify: since normalizeValue already sorts object
// keys, JSON.stringify's own key order (insertion order) is already the
// sorted one -- this just exists so callers don't have to remember that.
export function stableStringify(value) {
  return JSON.stringify(value);
}
