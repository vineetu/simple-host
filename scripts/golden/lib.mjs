// Shared helpers for the golden-snapshot harness (run.mjs) and its diff tool
// (diff.mjs). Node 22 built-ins only -- no npm dependencies.
//
// normalize() turns a captured HTTP exchange into something that compares
// stable across two runs of the *same* binary (random ids/keys/tokens,
// per-run temp paths and ports) while still catching a real behaviour
// change between two *different* binaries (e.g. a status code or a field
// that should not have moved).

import { inflateRawSync, crc32 } from 'node:zlib';
import { createHash } from 'node:crypto';
import { mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';

// Selection is independent of the runner so importing it starts no servers.
export function selectScenarios(available, selected = 'base', skipped = '') {
  const names = selected === 'all' ? Object.keys(available) : selected.split(',');
  const skip = skipped ? skipped.split(',') : [];
  for (const list of [names, skip]) {
    if (new Set(list).size !== list.length || list.some(name => !Object.hasOwn(available, name))) {
      throw new Error('unknown or repeated scenario');
    }
  }
  if (skip.includes('base')) throw new Error('base cannot be skipped');
  return names.filter(name => !skip.includes(name));
}

// Only these request/path pairs describe the running program, not content versions.
const BUILD_FIELDS = new Map([
  ...['GET /.well-known/agent-card.json', 'GET /.well-known/mcp/server-card.json',
    'GET /mcp/server-card'].map(name => [name, [['version', '<version>']]]),
  ...['admin-usage', 'GET /v1/admin/usage', 'GET /v1/admin/usage as admin']
    .map(name => [name, [['version', '<version>'], ['commit', '<commit>']]]),
]);
function normalizeBuildFields(name, value) {
  const fields = BUILD_FIELDS.get(name) || [];
  for (const [field, placeholder] of fields) {
    const keys = field.split('.');
    let parent = value;
    for (const key of keys.slice(0, -1)) parent = parent?.[key];
    const key = keys.at(-1);
    if (parent && typeof parent[key] === 'string' && parent[key].length) parent[key] = placeholder;
  }
}

// XML is rendered with one adjacent tag per line; hashes describe this rendering.
export function renderRecordedText(contentType, text, name = '') {
  return isSitemap(name)
    ? text.replaceAll('><', '>\n<') : text;
}

export function validatePostgresURL(prefix) {
  const pg = new URL(prefix);
  if (pg.protocol !== 'postgres:' || pg.pathname || pg.search || pg.hash || !pg.username || !pg.password || !['127.0.0.1','localhost'].includes(pg.hostname) || !pg.port || pg.port === '5432') throw new Error('GOLDEN_PG_URL must be postgres://user:pass@localhost:port with no path, query or fragment; shared port 5432 is forbidden');
  return pg;
}
export function fixedGetRoutes(inventory) {
  return new Set(inventory.routes.filter(r => r.method === 'GET').map(r => r.path.replaceAll('{$}', '')).filter(p => !p.includes('{')).sort());
}
export function validateScenarios(table) {
  if (!table.base) throw new Error('base scenario required');
  for (const [name, config] of Object.entries(table)) {
    if (!/^[a-z][a-z0-9-]*$/.test(name)) throw new Error('invalid scenario name');
    for (const product of ['host', 'hack']) if (config[product] !== undefined && typeof config[product] !== 'function') throw new Error('scenario callback must be a function');
  }
}
export function isSitemap(name) { return /^GET \/sitemap[^ ]*\.xml$/.test(name); }
export function relativeDates(today) {
  const dates = new Map();
  for (let offset = -400; offset <= 1; offset++) {
    const date = new Date(today + 'T00:00:00Z'); date.setUTCDate(date.getUTCDate() + offset);
    dates.set(date.toISOString().slice(0, 10), offset === 0 ? '<today>' : `<today${offset > 0 ? '+' : ''}${offset}>`);
  }
  return dates;
}
// First matching name rule decides what the response is, independent of credentials.
export const RESPONSE_CLASSES = [
  ['dynamic', /^(?:event-public-page|admin-usage|GET \/v1\/admin\/usage(?: as admin)?|GET \/v1\/u\/owner\/showcase\.json)$/],
  ['static', /^(?:admin-settings|(?:tools\/list|initialize|resources\/list)(?: |$)|tools\/call (?:find_tools|get_page_recipe|hack_get_page_recipe)(?: |$))/],
  ['static', /^GET (?:\/v1\/skills\/|\/skills\/)/],
];
export function responseClass(name, fixedGets = new Set()) {
  for (const [kind, pattern] of RESPONSE_CLASSES) if (pattern.test(name)) return kind;
  return name.startsWith('GET ') && fixedGets.has(name.slice(4)) ? 'static' : 'dynamic';
}
// Only these recorded answers contain calendar dates computed from the clock.
export const DATE_REQUESTS = new Set(['GET /v1/admin/growth as admin']);
export function requestReplacements(name, exact) {
  const scoped = new Map(exact);
  if (DATE_REQUESTS.has(name)) scoped.dates = exact.dates;
  return scoped;
}
export function mcpCallName(name, params) {
  const prefix = `tools/call ${params.name}`;
  const target = params.name === 'run_tool' ? params.arguments?.name : undefined;
  const suffix = name.startsWith(prefix) ? name.slice(prefix.length).trim() : name;
  return [prefix, target, target && (suffix === target || suffix.startsWith(target + ' ')) ? suffix.slice(target.length).trim() : suffix].filter(Boolean).join(' ');
}
export const STARTUP_PROGRESS_WORKERS = ['operations.network_usage', 'operations.paper_metrics'];
export const TIMER_WORKERS = new Set([
  'accounts.connector_sweep', 'accounts.visitor_auth',
  'http.connector.caches', 'http.hack.limiters', 'http.oauth.caches', 'http.site.caches', 'http.user.caches',
  'mail.submissions', 'notifications.delivery', 'operations.api_metrics',
  'hack.event_cleanup', 'operations.geoip_watch', 'operations.saved_data_sweep',
  'sites.hack_sweep', 'sites.domain_checks', 'sites.idle_cleanup', 'sites.preview_expiry', 'sites.address_families',
]);
export function normalizeStaticText(value, exact, name = '') {
  let s = value;
  // Secrets registered during seeding are deliberately excluded from static content.
  for (const [raw, replacement] of exact) if (/^<(?:base|hostport|tmpdir|geoip)(?::|>)/.test(replacement)) s = s.split(raw).join(replacement);
  s = s.replace(CSP_NONCE_RE, 'nonce="<nonce>"').replace(/nonce-[A-Za-z0-9+/]{20,24}=*/g, 'nonce-<nonce>');
  if (isSitemap(name)) s = s.replace(/(<lastmod>)[^<]*(<\/lastmod>)/g, '$1<ts>$2');
  return s;
}
export function isTextBody(type, bytes) {
  try { new TextDecoder('utf-8', { fatal: true }).decode(bytes); } catch { return false; }
  return !type || /^(?:text\/|application\/(?:[\w.-]+\+)?(?:json|xml)(?:;|$)|application\/(?:javascript|x-javascript|yaml|x-yaml|x-www-form-urlencoded)(?:;|$)|image\/svg\+xml(?:;|$))/.test(type);
}
export function mapStrings(value, normalize) {
  if (typeof value === 'string') return normalize(value);
  if (Array.isArray(value)) return value.map(v => mapStrings(v, normalize));
  if (value && typeof value === 'object') return Object.fromEntries(Object.keys(value).sort().map(k => [k, mapStrings(value[k], normalize)]));
  return value;
}

// Request-scoped placeholders preserve references without coupling request ordinals.
export function sanitizeName(name) {
  return name.replace(/[^A-Za-z0-9._-]/g, '_');
}

export class Namer {
  constructor() { this.seen = new Map(); this.counts = new Map(); this.request = ''; }
  beginRequest(name) { this.request = sanitizeName(name); }
  placeholderFor(category, rawValue) {
    const key = `${category}:${rawValue}`;
    if (!this.seen.has(key)) {
      const scope = `${category}:${this.request}`;
      const n = (this.counts.get(scope) || 0) + 1;
      this.counts.set(scope, n);
      this.seen.set(key, this.request ? `<${category}:${this.request}#${n}>` : `<${category}>`);
    }
    return this.seen.get(key);
  }
}

// Diff rendering is deliberately not JSON: multiline strings are visible lines.
export function renderJSON(value) {
  function render(v, depth) {
    const indent = '  '.repeat(depth);
    if (typeof v === 'string') {
      let text = v, embedded = false;
      try {
        const parsed = JSON.parse(v);
        text = JSON.stringify(sortJSON(parsed), null, 2);
        embedded = true;
      } catch { /* ordinary string */ }
      if (embedded || text.includes('\n')) return '"""\n' + text.split('\n').map(line => indent + '| ' + line).join('\n') + '\n' + indent + '"""';
    }
    if (!v || typeof v !== 'object') return JSON.stringify(v);
    const array = Array.isArray(v);
    const entries = array ? v.map((item, i) => [i, item]) : Object.keys(v).sort().map(k => [k, v[k]]);
    if (!entries.length) return array ? '[]' : '{}';
    return (array ? '[' : '{') + '\n' + entries.map(([k, item]) => '  '.repeat(depth + 1) + (array ? '' : JSON.stringify(k) + ': ') + render(item, depth + 1)).join(',\n') + '\n' + indent + (array ? ']' : '}');
  }
  return render(value, 0) + '\n';
}
// Whether rendering a JSON value can erase information needed by an exact pin.
export function rendersLossy(value) {
  if (typeof value === 'string') {
    try { JSON.parse(value); return true; } catch { return false; }
  }
  if (typeof value === 'number') return !Number.isFinite(value) || Object.is(value, -0);
  if (value && typeof value === 'object') return Object.values(value).some(rendersLossy);
  // Ordinary multiline strings retain every line and character in the block;
  // quoted single-line strings, finite numbers other than -0, booleans and null
  // are one-to-one. Containers retain keys/indexes (object key order is ignored).
  return false;
}
function sortJSON(v) {
  if (Array.isArray(v)) return v.map(sortJSON);
  if (v && typeof v === 'object') return Object.fromEntries(Object.keys(v).sort().map(k => [k, sortJSON(v[k])]));
  return v;
}

export function createTextWriter(root, textDir) {
  const names = new Set();
  return (name, value, kind = 'text') => {
    const filename = sanitizeName(name) + '.txt';
    if (names.has(filename)) throw new Error(`text filename collision: ${filename}`);
    names.add(filename);
    const file = `${textDir}/${filename}`;
    const text = kind === 'json' ? renderJSON(value) : value;
    mkdirSync(path.dirname(path.join(root, file)), { recursive: true });
    writeFileSync(path.join(root, file), text);
    return { file, sha256: sha256hex(text), length: Buffer.byteLength(text) };
  };
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
//     itself generated (temp dirs, chosen ports, registered codes) --
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
  s = s.replace(/\b\d{4}-\d{2}-\d{2}\b(?!T)/g, m => knownTimestamps.has(m + 'T00:00:00Z') ? m : exactReplacements.dates?.get(m) || m);
  s = s.replace(ISO_TS_RE, (m) => (knownTimestamps.has(m) ? m : '<ts>'));
  s = s.replace(UUID_RE, (m) => namer.placeholderFor('id', m.toLowerCase()));
  s = s.replace(API_KEY_RE, (m) => namer.placeholderFor('key', m.toLowerCase()));
  s = s.replace(HEX_TOKEN_RE, (m) => namer.placeholderFor('token', m.toLowerCase()));
  s = s.replace(HACK_HANDLE_RE, (m) => namer.placeholderFor('handle', m.toLowerCase()));
  s = s.replace(PREVIEW_TOKEN_RE, (m) => namer.placeholderFor('previewtoken', m));
  s = s.replace(CSP_NONCE_RE, 'nonce="<nonce>"');
  s = s.replace(/nonce-[A-Za-z0-9+/]{20,24}=*/g, 'nonce-<nonce>');
  return s;
}

// Only nonempty credential strings normalize; schema objects and other types survive.
const ALWAYS_VOLATILE_FIELD_NAMES = new Set(['last4', 'passcode']);

// Walk dynamic JSON and sort keys without dropping __proto__ or other user keys.
export function normalizeValue(value, namer, exactReplacements, knownTimestamps = NO_KNOWN_TIMESTAMPS, keyName) {
  if (ALWAYS_VOLATILE_FIELD_NAMES.has(keyName) && typeof value === 'string' && value.length) return '<volatile>';
  if (Array.isArray(value)) {
    return value.map((v) => normalizeValue(v, namer, exactReplacements, knownTimestamps));
  }
  if (value && typeof value === 'object') {
    const out = Object.create(null);
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
    // Only passcode cookie names derive this suffix from the random site ID.
    const name = parts[0].split('=')[0].replace(/^(?:__Host-)?sh_pass_([a-f0-9]{16})$/, raw => {
      const suffix = raw.slice(-16);
      return raw.slice(0, -16) + namer.placeholderFor('passcookie', suffix);
    });
    const attrs = parts.slice(1).map(p => /^Expires=/i.test(p) && !Number.isNaN(Date.parse(p.slice(8))) ? 'Expires=<http-date>' : normalizeText(p, namer, exactReplacements, knownTimestamps));
    return [`${name}=${parts[0].slice(parts[0].indexOf('=') + 1) ? '<value>' : ''}`, ...attrs].join('; ');
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

// Build identity, host measurements and worker timing/counters are incomparable.
export function normalizeResponseNoise(name, value, { observationCount = 0 } = {}) {
  if (!value || typeof value !== 'object') return value;
  normalizeBuildFields(name, value);
  // Observation requests are harness work, not part of the replay traffic.
  const selfField = name === 'GET /v1/admin/api-analytics as admin' ? 'self_calls_today'
    : name === 'GET /v1/admin/growth as admin' ? 'self_calls' : undefined;
  if (selfField && typeof value[selfField] === 'number') value[selfField] -= observationCount;
  if (['judging-settings-select-organisers', 'judging-settings-preserve-organisers', 'judging-settings-selected-get'].includes(name)) {
    const names = new Map((value.judges || []).map(judge => [judge.user_id, judge.name]));
    if (Array.isArray(value.judges)) value.judges.sort((a,b) => a.name.localeCompare(b.name, 'en'));
    if (Array.isArray(value.organiser_judges)) value.organiser_judges.sort((a,b) => String(names.get(a) || a).localeCompare(String(names.get(b) || b), 'en'));
  }
  if (name === 'judging-dashboard') {
    for (const [key, field] of [['teams','team_name'], ['judges','judge_name']]) {
      if (Array.isArray(value[key])) value[key].sort((a,b) => String(a[field]).localeCompare(String(b[field]), 'en'));
    }
  }
  if (['GET /v1/admin/usage', 'GET /v1/admin/usage as admin', 'admin-usage'].includes(name)) {
    if (typeof value.message === 'string') value.message = value.message.replace(/\b\d+(?:\.\d+)? (?:(?:[KMGT]B)|bytes?)\b/g, '<measurement>');
    const measurements = v => {
      if (typeof v === 'number') return '<n>';
      if (Array.isArray(v)) return v.map(measurements);
      if (v && typeof v === 'object') return Object.fromEntries(Object.entries(v).map(([k,x]) => [k, ['allowance_gb','alert_pct'].includes(k) ? x : measurements(x)]));
      return v;
    };
    for (const key of ['network','disk_bytes','disk_free_bytes','disk_used_bytes','disk_used_pct']) if (key in value) value[key] = measurements(value[key]);
    for (const worker of value.workers || []) {
      if (worker.enabled && TIMER_WORKERS.has(worker.name)) {
        for (const key of ['last_attempt', 'last_progress']) worker[key] = '<timer>';
        if (['mail.submissions', 'notifications.delivery'].includes(worker.name)) worker.pending = '<timer>';
      }
      for (const key of ['last_attempt', 'last_progress', 'last_error_at']) if (typeof worker[key] === 'string' && worker[key] !== '<timer>') worker[key] = '<ts>';
      if (typeof worker.pending === 'number') worker.pending = '<n>';
    }
  }
  return value;
}

// Compare ZIP contents, not compression or DOS/extended entry timestamps.
export function describeZip(bytes, normalize, { accountExport = false } = {}) {
  const end = bytes.lastIndexOf(Buffer.from([0x50, 0x4b, 0x05, 0x06]));
  if (end < 0) throw new Error('ZIP end directory missing');
  const count = bytes.readUInt16LE(end + 10);
  let offset = bytes.readUInt32LE(end + 16);
  const entries = [], rawEntries = [];
  for (let i = 0; i < count; i++) {
    if (bytes.readUInt32LE(offset) !== 0x02014b50) throw new Error('invalid ZIP directory');
    const method = bytes.readUInt16LE(offset + 10), size = bytes.readUInt32LE(offset + 20);
    const nameLength = bytes.readUInt16LE(offset + 28), extraLength = bytes.readUInt16LE(offset + 30), commentLength = bytes.readUInt16LE(offset + 32);
    const name = bytes.subarray(offset + 46, offset + 46 + nameLength).toString();
    const local = bytes.readUInt32LE(offset + 42);
    if (bytes.readUInt32LE(local) !== 0x04034b50 || bytes.readUInt16LE(local + 8) !== method || !bytes.subarray(local + 30, local + 30 + bytes.readUInt16LE(local + 26)).equals(Buffer.from(name))) throw new Error('ZIP local header mismatch');
    const start = local + 30 + bytes.readUInt16LE(local + 26) + bytes.readUInt16LE(local + 28);
    const compressed = bytes.subarray(start, start + size);
    const contents = method === 0 ? compressed : method === 8 ? inflateRawSync(compressed) : null;
    if (!contents) throw new Error(`unsupported ZIP compression ${method}`);
    if (contents.length !== bytes.readUInt32LE(offset + 24) || crc32(contents) !== bytes.readUInt32LE(offset + 16)) throw new Error(`ZIP integrity error: ${name}`);
    if (!(bytes.readUInt16LE(local + 6) & 8) && (bytes.readUInt32LE(local + 14) !== crc32(contents) || bytes.readUInt32LE(local + 18) !== size || bytes.readUInt32LE(local + 22) !== contents.length)) throw new Error(`ZIP local size/CRC mismatch: ${name}`);
    if (bytes.readUInt16LE(local + 6) & 8) {
      let descriptor = start + size;
      if (bytes.readUInt32LE(descriptor) === 0x08074b50) descriptor += 4;
      if (bytes.readUInt32LE(descriptor) !== crc32(contents) || bytes.readUInt32LE(descriptor + 4) !== size || bytes.readUInt32LE(descriptor + 8) !== contents.length) throw new Error(`ZIP descriptor size/CRC mismatch: ${name}`);
    }
    rawEntries.push({ name, contents, attributes: bytes.readUInt32LE(offset + 38) });
    offset += 46 + nameLength + extraLength + commentLength;
  }
  for (const { name, contents, attributes } of rawEntries.sort((a,b) => a.name.localeCompare(b.name, 'en'))) {
    const textual = isTextBody('', contents);
    const text = textual ? normalize(contents.toString('utf8'), name) : undefined;
    const normalized = textual ? Buffer.from(text) : contents;
    entries.push({ name: accountExport ? name.replace(/^(simple-host-[^/]+)-\d{4}-\d{2}-\d{2}(?=\/|$)/, '$1-<date>') : name, attributes, sha256: sha256hex(normalized), length: normalized.length, ...(text === undefined ? {} : { text }) });
  }
  return { entries: entries.sort((a, b) => a.name.localeCompare(b.name, 'en')) };
}

export function normalizeRecordedText(name, text) {
  if (name === 'event-public-page') {
    // Stage close and result publication set these instants to now. The HTML
    // human-date form does not match the ISO normalizer. Keep markup and labels.
    return text.replace(/(<b>(?:Entries due|Results published)<\/b><\/span><span class="when">)[^<]+(?=<\/span>)/g, '$1<ts>');
  }
  return text;
}

// Codes are local to sign-in mail; never put six digits in the global exact map.
export function normalizeMailOutbox(messages, normalize, { namer = new Namer(), exact = new Map(), knownTimestamps = NO_KNOWN_TIMESTAMPS } = {}) {
  const prepare = message => {
    const payload = { ...message };
    const code = /^Simple (?:Host|Hack) sign-in code: (\d{6})$/.exec(message.subject)?.[1];
    for (const field of ['subject', 'text', 'html']) if (typeof payload[field] === 'string') {
      if (code) payload[field] = payload[field]
        .replace(/https?:\/\/[^\s<>"']+|href=["'][^"']*["']/g, link => link.replaceAll(code, '<code>'))
        .replace(new RegExp(`(?<![A-Za-z0-9_])${code}(?![A-Za-z0-9_])`, 'g'), '<code>');
      if (message.subject === 'New sign-in to your Simple Host account') payload[field] = payload[field].replace(/^When: [^\n]+ UTC$/gm, 'When: <ts>');
    }
    if (payload.attachments) payload.attachments = payload.attachments.map(a => {
      const bytes = Buffer.from(a.content || '', 'base64');
      return { ...Object.fromEntries(Object.entries(a).filter(([k]) => k !== 'content')), sha256: sha256hex(bytes), length: bytes.length };
    });
    return payload;
  };
  // Each projection inherits established identities, but never mutates the live namer.
  // Unseen identities are allocated in semantic-key order; normalized output is
  // sorted again so neither raw random values nor arrival order break ties.
  const key = message => {
    const clone = new Namer();
    clone.seen = new Map(namer.seen); clone.counts = new Map(namer.counts); clone.request = namer.request;
    return JSON.stringify(mapStrings(message, s => normalizeText(s, clone, exact, knownTimestamps)));
  };
  return messages.map(prepare).map(message => ({ message, key: key(message) }))
    .sort((a,b) => a.key.localeCompare(b.key, 'en')).map(({ message }) => {
      // Derived attachment hashes are bytes, not random identifiers.
      const attachments = message.attachments;
      const payload = { ...message }; delete payload.attachments;
      const out = mapStrings(payload, normalize);
      if (attachments) out.attachments = attachments.map(({ sha256, length, ...metadata }) => ({ ...mapStrings(metadata, normalize), sha256, length }));
      if (typeof out.html === 'string') out.html_sha256 = sha256hex(out.html);
      return sortJSON(out);
    }).sort((a,b) => JSON.stringify(a).localeCompare(JSON.stringify(b), 'en'));
}
