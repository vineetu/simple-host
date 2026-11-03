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

// normalizeText rewrites one string value (a JSON string field, or a whole
// HTML/text body) by:
//  1. substituting every exact-string, per-run dynamic value the harness
//     itself generated (temp dirs, chosen ports, build version/commit) --
//     those are passed in as `exactReplacements` (Map of raw -> placeholder,
//     already resolved by the caller, e.g. {"<tmp dir path>": "<tmpdir>"});
//  2. then pattern-matching ids/keys/tokens/timestamps and mapping each
//     distinct raw value to a stable placeholder via `namer`.
export function normalizeText(value, namer, exactReplacements) {
  if (typeof value !== 'string') return value;
  let s = value;
  for (const [raw, placeholder] of exactReplacements) {
    if (raw) s = s.split(raw).join(placeholder);
  }
  s = s.replace(ISO_TS_RE, '<ts>');
  s = s.replace(UUID_RE, (m) => namer.placeholderFor('id', m.toLowerCase()));
  s = s.replace(API_KEY_RE, (m) => namer.placeholderFor('key', m.toLowerCase()));
  s = s.replace(HEX_TOKEN_RE, (m) => namer.placeholderFor('token', m.toLowerCase()));
  return s;
}

// VOLATILE_KEYS are JSON object keys whose value is never comparable across
// two runs of even the *same* binary, because it reflects the shared box
// (disk headroom, bandwidth counters) rather than anything the harness
// seeded -- or the build (the git commit baked into the binary, which is
// expected to differ between the baseline and current-commit binaries on
// purpose). They are replaced outright, keyed by name rather than pattern,
// because their values (numbers, nested objects) don't look like an id/key/
// token/timestamp.
const VOLATILE_KEYS = new Set([
  'commit',
  'disk_bytes',
  'disk_free_bytes',
  'disk_used_bytes',
  'disk_used_pct',
  'network',
]);

// normalizeValue walks a parsed JSON value recursively, normalizing every
// string leaf, and sorts object keys so two structurally-identical objects
// serialize identically regardless of insertion order. keyName is the
// object key this value was found under (undefined for array elements and
// the document root), used only for the VOLATILE_KEYS override above.
export function normalizeValue(value, namer, exactReplacements, keyName) {
  if (VOLATILE_KEYS.has(keyName)) return '<volatile>';
  if (Array.isArray(value)) {
    return value.map((v) => normalizeValue(v, namer, exactReplacements));
  }
  if (value && typeof value === 'object') {
    const out = {};
    for (const k of Object.keys(value).sort()) {
      out[k] = normalizeValue(value[k], namer, exactReplacements, k);
    }
    return out;
  }
  if (typeof value === 'string') {
    return normalizeText(value, namer, exactReplacements);
  }
  return value;
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
