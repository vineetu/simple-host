#!/usr/bin/env node
// Format-2 recordings with the recorder's frozen JSON rendering.
import { readFileSync, readdirSync, existsSync, lstatSync, realpathSync } from 'node:fs';
import { resolve, dirname, relative, isAbsolute } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';
import { isDeepStrictEqual as equal } from 'node:util';

import { renderJSON, rendersLossy } from './lib.mjs';

const ABSENT = '__absent__';
const MISSING = Symbol('absent'),
  WILDCARD = Symbol('array wildcard');
// Render the internal absence marker for output.
const display = (v) => (v === MISSING ? ABSENT : v);
// Compare a recorded value with an exact approval value.
const pinned = (actual, expected) =>
  expected === ABSENT ? actual === MISSING : actual !== MISSING && equal(actual, expected);
// Check for an own property on a possibly missing value.
const own = (o, k) => o != null && Object.hasOwn(o, k);
// Recognize JSON objects, excluding null and arrays.
const object = (v) => v !== null && typeof v === 'object' && !Array.isArray(v);
const here = dirname(fileURLToPath(import.meta.url));
const idPattern = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;
// Append an object key using dot or quoted bracket syntax.
const keyPath = (p, k) => (/^[A-Za-z_$][\w$]*$/.test(k) ? `${p}.${k}` : `${p}[${JSON.stringify(k)}]`);

// Load: validate recording files before comparing any values.

// Require exactly the expected object keys.
function exactKeys(value, keys) {
  return object(value) && equal(Object.keys(value).sort(), [...keys].sort());
}
// List every file in a recording, relative to its root.
function filesUnder(dir, prefix = '') {
  const directory = lstatSync(dir);
  if (directory.isSymbolicLink()) throw Error(`symlink in recording: ${dir}`);
  if (!directory.isDirectory()) throw Error(`not a real directory in recording: ${dir}`);
  return readdirSync(dir, { withFileTypes: true }).flatMap((d) => {
    const file = resolve(dir, d.name);
    const stat = lstatSync(file);
    if (stat.isSymbolicLink()) throw Error(`symlink in recording: ${file}`);
    if (!stat.isDirectory() && !stat.isFile()) throw Error(`not a regular file in recording: ${file}`);
    return stat.isDirectory() ? filesUnder(file, `${prefix}${d.name}/`) : [`${prefix}${d.name}`];
  });
}
// Load runs and verify their schema and rendered text integrity.
export function loadRecording(dir) {
  const errors = [],
    runs = new Map(),
    expected = new Set(['manifest.json']);
  const check = (ok, message) => {
    if (!ok) errors.push(`${dir}: ${message}`);
  };
  const read = (file) => JSON.parse(readFileSync(resolve(dir, file), 'utf8'));
  try {
    const actual = filesUnder(dir);
    const root = realpathSync(dir);
    for (const file of actual) {
      const rel = relative(root, realpathSync(resolve(dir, file)));
      if (rel.startsWith('..') || isAbsolute(rel)) throw Error(`file outside recording: ${file}`);
    }
    const manifest = read('manifest.json');
    if (!exactKeys(manifest, ['format', 'runs'])) throw Error('unexpected manifest keys');
    if (manifest.format !== 2 || !Array.isArray(manifest.runs))
      throw Error('manifest must have format 2 and runs array');
    for (const r of manifest.runs) {
      try {
        if (!exactKeys(r, ['product', 'scenario', 'file', 'text_dir', 'requests']))
          throw Error('unexpected manifest run keys');
        if (
          !['host', 'hack'].includes(r.product) ||
          typeof r.scenario !== 'string' ||
          !idPattern.test(r.scenario)
        )
          throw Error('invalid product/scenario');
        const stem = r.product + (r.scenario === 'base' ? '' : `.${r.scenario}`);
        if (r.file !== `${stem}.json` || r.text_dir !== `text/${stem}`)
          throw Error('manifest file/text_dir does not match product/scenario');
        const key = `${r.product}/${r.scenario}`;
        if (runs.has(key)) throw Error(`duplicate run ${key}`);
        expected.add(r.file);
        const data = read(r.file);
        if (!exactKeys(data, ['format', 'product', 'scenario', 'requests']))
          throw Error(`${r.file}: unexpected run keys`);
        if (
          data.format !== 2 ||
          data.product !== r.product ||
          data.scenario !== r.scenario ||
          !Array.isArray(data.requests) ||
          data.requests.length !== r.requests
        )
          throw Error(`${r.file}: metadata/request count mismatch`);
        if (!data.requests.length) check(false, `${key}: run has zero requests`);
        const names = new Set(),
          texts = new Map();
        for (const req of data.requests) {
          try {
            if (
              !object(req) ||
              Object.keys(req).some(
                (k) =>
                  !['name', 'method', 'path', 'status', 'headers', 'body_kind', 'body', 'text'].includes(
                    k,
                  ),
              )
            )
              throw Error(`${r.file}: unexpected request keys`);
            if (
              typeof req.method !== 'string' ||
              typeof req.path !== 'string' ||
              !Number.isInteger(req.status) ||
              !object(req.headers)
            )
              throw Error(`${r.file}: invalid request method/path/status/headers`);
            if (typeof req.name !== 'string' || !req.name || names.has(req.name))
              throw Error(`${r.file}: duplicate or invalid request name ${req.name}`);
            names.add(req.name);
            if (!['json', 'text', 'binary', 'empty'].includes(req.body_kind))
              throw Error(`${req.name}: invalid body_kind`);
            if (['json', 'text'].includes(req.body_kind)) {
              const t = req.text;
              if (
                !exactKeys(t, ['file', 'sha256', 'length']) ||
                typeof t.sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(t.sha256) ||
                !Number.isInteger(t.length) || t.length < 0 ||
                typeof t.file !== 'string' ||
                !t.file.startsWith(`${r.text_dir}/`) ||
                relative(resolve(dir), resolve(dir, t.file)).startsWith('..') ||
                isAbsolute(t.file)
              )
                throw Error(`${req.name}: invalid text metadata (file, sha256, length)`);
              if (expected.has(t.file)) throw Error(`${req.name}: duplicate text.file`);
              expected.add(t.file);
              const bytes = readFileSync(resolve(dir, t.file));
              if (
                createHash('sha256').update(bytes).digest('hex') !== t.sha256 ||
                bytes.length !== t.length
              )
                throw Error(`${req.name}: text integrity error (sha256/length): ${t.file}`);
              let text;
              try {
                // Preserve a BOM as content; decoding must not erase byte differences.
                text = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(bytes);
              } catch {
                throw Error(`${req.name}: text integrity error (invalid UTF-8): ${t.file}`);
              }
              if (req.body_kind === 'json' && text !== renderJSON(req.body))
                throw Error(`${req.name}: text integrity error (JSON rendering): ${t.file}`);
              texts.set(req.name, text);
            } else if (own(req, 'text')) throw Error(`${req.name}: unexpected text metadata`);
            if (['json', 'binary'].includes(req.body_kind) !== own(req, 'body'))
              throw Error(`${req.name}: body presence does not match body_kind`);
          } catch (err) {
            errors.push(`${dir}: ${err.message}`);
          }
        }
        runs.set(key, { ...r, ...data, texts, dir });
      } catch (err) {
        errors.push(`${dir}: ${err.message}`);
      }
    }
    for (const f of actual) check(expected.has(f), `file not in manifest: ${f}`);
    for (const f of expected) check(actual.includes(f), `missing file: ${f}`);
  } catch (err) {
    errors.push(`${dir}: ${err.message}`);
  }
  return { runs, errors };
}

// Diff: collect line and structural differences.

// Find a minimal line edit script up to the cap, then replace the trimmed middle.
export function lineDiff(aText, bText) {
  const lines = (s) => (s === '' ? [] : s.split('\n'));
  let a = lines(aText),
    b = lines(bText),
    start = 0;
  while (start < a.length && start < b.length && a[start] === b[start]) start++;
  a = a.slice(start);
  b = b.slice(start);
  while (a.length && b.length && a.at(-1) === b.at(-1)) {
    a.pop();
    b.pop();
  }
  const n = a.length,
    m = b.length;
  if (!n || !m) return { removed: a, added: b };
  const trace = [];
  const maxDistance = 4000;
  // Each depth owns only its diagonals; unreachable slots are never read.
  const frontier = (depth, diagonal) => trace[depth][diagonal + depth];
  for (let d = 0; d <= Math.min(n + m, maxDistance); d++) {
    const current = new Int32Array(2 * d + 1);
    trace.push(current);
    for (let k = -d; k <= d; k += 2) {
      let x;
      if (d === 0) x = 0;
      else if (k === -d || (k !== d && frontier(d - 1, k - 1) < frontier(d - 1, k + 1))) {
        x = frontier(d - 1, k + 1);
      } else x = frontier(d - 1, k - 1) + 1;
      let y = x - k;
      while (x < n && y < m && a[x] === b[y]) {
        x++;
        y++;
      }
      current[k + d] = x;
      if (x >= n && y >= m) {
        const removed = [],
          added = [];
        for (let depth = d; depth > 0; depth--) {
          const diagonal = x - y;
          const insert =
            diagonal === -depth ||
            (diagonal !== depth &&
              frontier(depth - 1, diagonal - 1) < frontier(depth - 1, diagonal + 1));
          const pk = insert ? diagonal + 1 : diagonal - 1;
          const px = frontier(depth - 1, pk),
            py = px - pk;
          while (x > px && y > py) {
            x--;
            y--;
          }
          if (x === px) added.push(b[--y]);
          else removed.push(a[--x]);
        }
        return { removed: removed.reverse(), added: added.reverse() };
      }
    }
  }
  return { removed: a, added: b };
}
// Collect changed leaves, preserving absence separately from JSON values.
function leaves(a, b, tokens, out) {
  if (equal(a, b)) return;
  if (object(a) && object(b)) {
    for (const key of [...new Set([...Object.keys(a), ...Object.keys(b)])].sort())
      leaves(own(a, key) ? a[key] : MISSING, own(b, key) ? b[key] : MISSING, [...tokens, key], out);
  } else if (Array.isArray(a) && Array.isArray(b)) {
    for (let i = 0; i < Math.max(a.length, b.length); i++)
      leaves(i < a.length ? a[i] : MISSING, i < b.length ? b[i] : MISSING, [...tokens, i], out);
  } else out.push({ tokens, path: formatPath(tokens), old: a, new: b });
}
// Render tokens as an unambiguous approval path.
function formatPath(tokens) {
  return tokens
    .slice(1)
    .reduce((p, t) => (typeof t === 'number' ? `${p}[${t}]` : keyPath(p, t)), tokens[0]);
}
// Check whether a changed path is within an approved parent.
const below = (tokens, parent) =>
  parent.length <= tokens.length && parent.every((t, i) => t === tokens[i]);
// Match: validate approvals and assign each difference exactly one owner.

const pathForms = JSON.parse(readFileSync(resolve(here, 'approved/schema.json'), 'utf8'))
  .properties.entries.items.oneOf;
const pathPatterns = [0, 2].map((i) => new RegExp(pathForms[i].properties.at.pattern));
// Parse a supported approval path into keys, indexes, and array wildcards.
export function parsePath(path) {
  if (typeof path !== 'string' || !pathPatterns.some((p) => p.test(path)))
    throw Error(`invalid path: ${path}`);
  const root = /^(status|method|path|body_kind|headers|body)(?=\.|\[|$)/.exec(path);
  if (!root) throw Error(`invalid path: ${path}`);
  const tokens = [root[0]];
  let rest = path.slice(root[0].length);
  while (rest) {
    const m = /^(?:\.([A-Za-z_$][\w$]*)|\[(\d+|\*|"(?:[^"\\]|\\.)*")\])/.exec(rest);
    if (!m) throw Error(`invalid path: ${path}`);
    tokens.push(m[1] ?? (m[2] === '*' ? WILDCARD : m[2][0] === '"' ? JSON.parse(m[2]) : Number(m[2])));
    rest = rest.slice(m[0].length);
  }
  if (
    tokens[0] === 'headers'
      ? tokens.length !== 2 || typeof tokens[1] !== 'string' || tokens[1] === WILDCARD
      : !['body'].includes(tokens[0]) && tokens.length !== 1
  )
    throw Error(`invalid path: ${path}`);
  return tokens;
}
// Read a path while preserving missing keys.
function get(value, tokens) {
  for (const t of tokens) {
    if (!own(value, t)) return MISSING;
    value = value[t];
  }
  return value;
}
// Validate one slice document and each exact approval form.
export function validateApproval(doc, slice) {
  const errors = [];
  if (
    !exactKeys(doc, ['slice', 'entries']) ||
    doc.slice !== slice ||
    typeof doc.slice !== 'string' ||
    !idPattern.test(doc.slice) ||
    !Array.isArray(doc.entries)
  )
    return ['expected exactly {slice (matching filename), entries: []}'];
  doc.entries.forEach((e, i) => {
    const fail = (message) => errors.push(`entry ${i + 1}: ${message}`);
    if (!object(e)) {
      fail('must be an object');
      return;
    }
    if (!['host', 'hack'].includes(e.product)) fail('product must be host or hack');
    if (typeof e.reason !== 'string' || !e.reason.trim() || e.reason.trimStart().startsWith('TODO'))
      fail('reason must be reviewed, non-empty, and not start with TODO');
    const scenario = own(e, 'scenario');
    const subject = scenario ? 'scenario' : 'request';
    if (typeof e[subject] !== 'string' || !e[subject].trim()) fail(`${subject} must be non-empty`);
    let keys = ['product', subject, 'reason'];
    if (own(e, 'change')) {
      keys.push('change');
      if (!['added', 'removed'].includes(e.change)) fail('invalid change');
      if (scenario && (e.scenario === 'base' || !idPattern.test(e.scenario)))
        fail('invalid scenario (base cannot be approved whole)');
    } else {
      if (scenario) fail('scenario requires change');
      keys.push('at');
      if (e.at === 'text') {
        keys.push('removed', 'added');
        if (
          ![e.removed, e.added].every((a) => Array.isArray(a) && a.every((v) => typeof v === 'string'))
        )
          fail('removed and added must be string arrays');
      } else {
        try {
          const tokens = parsePath(e.at);
          if (own(e, 'replace')) {
            keys.push('replace');
            if (
              !tokens.includes(WILDCARD) ||
              !exactKeys(e.replace, ['from', 'to']) ||
              typeof e.replace.from !== 'string' ||
              !e.replace.from ||
              typeof e.replace.to !== 'string'
            )
              fail('replace needs [*] and non-empty string from and string to');
          } else {
            keys.push('old', 'new');
            if (tokens.includes(WILDCARD)) fail('old/new cannot use wildcards');
          }
        } catch (err) {
          fail(err.message);
        }
      }
    }
    if (!exactKeys(e, keys)) fail(`expected exactly keys: ${keys.join(', ')}`);
  });
  return errors;
}

// Select string pairs for a replacement, checking all traversed array lengths.
function replacement(a, b, tokens, prefix = [], selected = []) {
  if (!tokens.length) {
    if (typeof a !== 'string' || typeof b !== 'string') return null;
    selected.push({ tokens: prefix, old: a, new: b });
    return selected;
  }
  const [t, ...tail] = tokens;
  if (t === WILDCARD) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return null;
    for (let i = 0; i < a.length; i++)
      if (!replacement(a[i], b[i], tail, [...prefix, i], selected)) return null;
  } else {
    if (!own(a, t) || !own(b, t)) return null;
    if (!replacement(a[t], b[t], tail, [...prefix, t], selected)) return null;
  }
  return selected;
}

// Collect differences, match approvals, and propose entries for uncovered changes.
export function compareRecordings(ref, cand, entries = []) {
  const differences = [],
    contexts = [],
    notes = [],
    problems = [];
  let requests = 0;
  for (const key of new Set([...ref.runs.keys(), ...cand.runs.keys()])) {
    const a = ref.runs.get(key),
      b = cand.runs.get(key),
      run = a ?? b;
    const common = { product: run.product, scenario: run.scenario };
    requests += (b ?? a).requests.length;
    if (!a || !b) {
      differences.push({
        ...common,
        request: '(scenario)',
        path: 'scenario',
        change: a ? 'removed' : 'added',
        old: a ? a.file : ABSENT,
        new: b ? b.file : ABSENT,
        wholeScenario: true,
      });
      notes.push(
        `${key}: review ${resolve(run.dir, run.file)} and ` +
          `${resolve(run.dir, run.text_dir)} (${run.requests.length} requests)`,
      );
      continue;
    }
    const am = new Map(a.requests.map((r) => [r.name, r])),
      bm = new Map(b.requests.map((r) => [r.name, r]));
    const seqA = [...am.keys()].filter((n) => bm.has(n)),
      seqB = [...bm.keys()].filter((n) => am.has(n));
    if (!equal(seqA, seqB))
      differences.push({ ...common, request: '(run)', path: 'sequence', old: seqA, new: seqB });
    for (const name of new Set([...am.keys(), ...bm.keys()])) {
      const ra = am.get(name),
        rb = bm.get(name);
      const subject = {
        ...common,
        request: name,
        runFiles: { reference: resolve(a.dir, a.file), candidate: resolve(b.dir, b.file) },
        textFiles: {
          reference: ra?.text && resolve(a.dir, ra.text.file),
          candidate: rb?.text && resolve(b.dir, rb.text.file),
        },
      };
      if (!ra || !rb) {
        differences.push({
          ...subject,
          path: 'request',
          change: ra ? 'removed' : 'added',
          old: ra ?? ABSENT,
          new: rb ?? ABSENT,
        });
        continue;
      }
      const ds = [];
      for (const field of ['method', 'path', 'status', 'body_kind'])
        leaves(get(ra, [field]), get(rb, [field]), [field], ds);
      for (const h of new Set([...Object.keys(ra.headers ?? {}), ...Object.keys(rb.headers ?? {})])) {
        if (!equal(get(ra, ['headers', h]), get(rb, ['headers', h])))
          ds.push({
            tokens: ['headers', h],
            path: formatPath(['headers', h]),
            old: get(ra, ['headers', h]),
            new: get(rb, ['headers', h]),
          });
      }
      if (own(ra, 'body') || own(rb, 'body')) {
        const old = get(ra, ['body']), next = get(rb, ['body']);
        if (ra.body_kind !== rb.body_kind && !equal(old, next))
          ds.push({ tokens: ['body'], path: 'body', old, new: next });
        else leaves(old, next, ['body'], ds);
      }
      const ta = a.texts.get(name),
        tb = b.texts.get(name);
      const textDelta = ta !== undefined || tb !== undefined ? lineDiff(ta ?? '', tb ?? '') : null;
      // JSON structure is authoritative; a rendering-only difference cannot approve anything.
      if (ra.body_kind !== 'json' || rb.body_kind !== 'json') {
        if (textDelta && (textDelta.removed.length || textDelta.added.length))
          ds.push({ path: 'text', old: ta ?? ABSENT, new: tb ?? ABSENT, textDelta });
      }
      const ctx = { ...subject, a: ra, b: rb, textDelta, differences: ds };
      contexts.push(ctx);
      differences.push(...ds.map((d) => Object.assign(d, subject)));
    }
  }
  const covered = new Map(differences.map((d) => [d, []]));
  const textVerifications = new Map();
  const scenarios = new Set(
    [...ref.runs.values(), ...cand.runs.values()]
      .filter((r) => r.scenario !== 'base')
      .map((r) => `${r.product}/${r.scenario}`),
  );
  const identities = entries.map((e, i) => e._source ?? `entry ${i + 1}`);
  entries.forEach((e, index) => {
    const label = identities[index];
    let scenario = 'base',
      name = e.request;
    if (own(e, 'scenario')) scenario = e.scenario;
    else {
      const slash = name.indexOf('/'), prefix = name.slice(0, slash);
      if (slash >= 0 && scenarios.has(`${e.product}/${prefix}`)) {
        scenario = prefix;
        name = name.slice(slash + 1);
      }
    }
    const matchesSubject = (d) =>
      d.product === e.product &&
      d.scenario === scenario &&
      (own(e, 'scenario')
        ? d.wholeScenario && scenario !== 'base'
        : d.request === name && !d.wholeScenario);
    let matched = [];
    const ctx = contexts.find(
      (c) => c.product === e.product && c.scenario === scenario && c.request === name,
    );
    let reported = false, verifiedLossyText = false;
    const issue = (message, path, old, next) => {
      reported = true;
      problems.push({
        product: e.product,
        scenario,
        request: name ?? '(scenario)',
        path,
        old,
        new: next,
        textFiles: ctx?.textFiles,
        runFiles: ctx?.runFiles,
        message: `${e._file ? e._file + ': ' : ''}${label}: ${message}`,
      });
    };
    if (own(e, 'change'))
      matched = differences.filter((d) => matchesSubject(d) && d.change === e.change);
    else if (ctx) {
      if (e.at === 'text') {
        if (
          ctx.textDelta &&
          equal(ctx.textDelta, { removed: e.removed, added: e.added }) &&
          (e.removed.length || e.added.length)
        ) {
          verifiedLossyText = ctx.a.body_kind === 'json' && ctx.b.body_kind === 'json' &&
            ctx.differences.some((d) => d.tokens?.[0] === 'body' &&
              (rendersLossy(d.old) || rendersLossy(d.new)));
          matched = ctx.differences.filter((d) => d.path === 'text' ||
            (ctx.a.body_kind === 'json' && ctx.b.body_kind === 'json' &&
              d.tokens?.[0] === 'body' && (d.old === MISSING || d.new === MISSING ||
                (!rendersLossy(d.old) && !rendersLossy(d.new) &&
                  renderJSON(d.old) !== renderJSON(d.new)))));
          // Lossy leaves belong to pins; the text entry only verifies their lines.
          if (!matched.length && verifiedLossyText) {
            if (textVerifications.has(ctx))
              issue(`text lines covered by multiple entries: ${identities[textVerifications.get(ctx)]}, ${label}`, 'text', ctx.textDelta, ctx.textDelta);
            else textVerifications.set(ctx, index);
          }
        } else
          issue('text lines do not match', 'text', ctx.textDelta, {
            removed: e.removed,
            added: e.added,
          });
      } else if (e.replace) {
        const selected = replacement(ctx.a, ctx.b, parsePath(e.at));
        if (
          selected &&
          selected.some((s) => s.old !== s.new) &&
          selected.every((s) => s.new === s.old.split(e.replace.from).join(e.replace.to))
        )
          matched = ctx.differences.filter((d) => selected.some((s) => equal(s.tokens, d.tokens)));
        else
          issue(
            'replace does not match every value, or nothing changed',
            e.at,
            selected?.map((s) => s.old) ?? ctx.a.body,
            selected?.map((s) => s.new) ?? ctx.b.body,
          );
      } else {
        const tokens = parsePath(e.at),
          old = get(ctx.a, tokens),
          next = get(ctx.b, tokens);
        if (!pinned(old, e.old))
          issue(
            "entry's old does not equal the reference",
            e.at,
            { expected: e.old, actual: old },
            next,
          );
        if (!pinned(next, e.new))
          issue("entry's new does not equal the candidate", e.at, old, {
            expected: e.new,
            actual: next,
          });
        if (pinned(old, e.old) && pinned(next, e.new) && !equal(old, next))
          matched = ctx.differences.filter((d) => d.tokens && below(d.tokens, tokens));
      }
    }
    if (!matched.length && !reported && !verifiedLossyText) {
      const bothRecorded = own(e, 'scenario') &&
        ref.runs.has(`${e.product}/${scenario}`) && cand.runs.has(`${e.product}/${scenario}`);
      issue(
        bothRecorded
          ? e.change === 'removed'
            ? `the candidate also recorded scenario ${scenario}; a whole-scenario "removed" entry is only for a scenario the candidate no longer records; compare request by request or drop the scenario from the table`
            : `the reference also recorded scenario ${scenario}; a whole-scenario entry is only for a scenario the reference program cannot run. Record the reference without it (run.mjs --scenario all --skip-scenario ${scenario}) or, once the reference program knows the scenario, compare request by request`
          : 'unused entry',
        e.at ?? e.change, own(e, 'old') ? e.old : ABSENT, own(e, 'new') ? e.new : ABSENT,
      );
    }
    for (const d of matched) covered.get(d).push(index);
  });
  const proposalKeys = new Set();
  const proposals = [],
    proposalErrors = [],
    approved = [];
  for (const d of differences) {
    const owners = covered.get(d);
    if (owners.length === 1) {
      const entry = entries[owners[0]];
      approved.push({ ...d, reason: entry.reason, source: entry._source, file: entry._file });
      continue;
    }
    problems.push({
      ...d,
      message: owners.length
        ? `difference covered by multiple entries: ${owners.map((i) => identities[i]).join(', ')}`
        : 'unapproved difference',
    });
    if (owners.length || d.path === 'sequence' || (d.wholeScenario && d.scenario === 'base')) continue;
    try {
      const e = {
        product: d.product,
        ...(d.wholeScenario
          ? { scenario: d.scenario }
          : { request: d.scenario === 'base' ? d.request : `${d.scenario}/${d.request}` }),
        reason: 'TODO: one sentence',
      };
      if (d.change) Object.assign(e, { change: d.change });
      else if (d.path === 'text') Object.assign(e, { at: 'text', ...d.textDelta });
      else {
        if (d.old === ABSENT || d.new === ABSENT)
          throw Error('the value is the reserved string __absent__; pin an enclosing object by hand');
        parsePath(d.path);
        Object.assign(e, { at: d.path, old: display(d.old), new: display(d.new) });
      }
      const key = JSON.stringify([e.product, e.request, e.scenario, e.at, e.change]);
      if (!proposalKeys.has(key)) {
        proposals.push(e);
        proposalKeys.add(key);
      }
    } catch (err) {
      proposalErrors.push({ ...d, message: `cannot propose: ${err.message}` });
    }
  }
  return {
    problems,
    approved,
    proposals,
    proposalErrors,
    notes,
    requests,
    runs: new Set([...ref.runs.keys(), ...cand.runs.keys()]).size,
  };
}
// Report: format diagnostics and approval reasons.

// Limit values in diagnostics to a readable length.
const rendered = (value) => JSON.stringify(value, (_key, v) => display(v));
const brief = (value) => {
  const s = rendered(value);
  return s?.length > 700 ? `${s.slice(0, 700)}…` : s;
};
// Render one difference or approval problem with its old and new values.
function describe(p) {
  const subject = `${p.product}/${p.scenario} request=${JSON.stringify(p.request)}`;
  const lead = `${subject} path=${p.path}: ${p.message ?? p.reason}`;
  const bodyText = p.path === 'text' || p.path.startsWith('body') && p.path !== 'body_kind';
  const fullText = () =>
    `\n  To see the full values, diff the ${bodyText ? 'text files or run JSON' : 'run JSON files'}:\n` +
    `  reference: ${(bodyText ? p.textFiles?.reference : null) ?? p.runFiles?.reference ?? '(unavailable)'}\n` +
    `  candidate: ${(bodyText ? p.textFiles?.candidate : null) ?? p.runFiles?.candidate ?? '(unavailable)'}` +
    (bodyText ? `\n  run JSON reference: ${p.runFiles?.reference ?? '(unavailable)'}\n  run JSON candidate: ${p.runFiles?.candidate ?? '(unavailable)'}` : '');
  if (p.textDelta) {
    const lines = p.textDelta.removed
      .map((l) => `- ${l}`)
      .concat(p.textDelta.added.map((l) => `+ ${l}`));
    return `${lead}\n${lines.slice(0, 40).join('\n')}` +
      (lines.length > 40 ? `\n  … ${lines.length - 40} more changed line(s)` : '') +
      (lines.length > 40 || [p.old, p.new].some((v) => rendered(v)?.length > 700) ? fullText() : '');
  }
  return `${lead}\n  old: ${brief(p.old)}\n  new: ${brief(p.new)}` +
    ([p.old, p.new].some((v) => rendered(v)?.length > 700) ? fullText() : '');
}
// Main: parse arguments, load inputs, and report the comparison.

// Run the CLI without exiting the calling process.
export function main(
  args = process.argv.slice(2),
  io = { out: (s) => console.log(s), err: (s) => console.error(s) },
) {
  let refDir,
    candDir,
    propose = false,
    required = ['host', 'hack'],
    slices = [],
    approvedDir = resolve(here, 'approved');
  try {
    if (args.length < 2 || args[0].startsWith('--') || args[1].startsWith('--'))
      throw Error('two recording folders required');
    [refDir, candDir] = args;
    for (let i = 2; i < args.length; i++) {
      if (args[i] === '--propose') propose = true;
      else if (args[i] === '--require' && args[i + 1]) required = args[++i].split(',');
      else if (args[i] === '--slice' && args[i + 1]) slices.push(...args[++i].split(','));
      else if (args[i] === '--approved-dir' && args[i + 1]) approvedDir = resolve(args[++i]);
      else throw Error(`unknown or incomplete argument: ${args[i]}`);
    }
    if (!required.length || required.some((p) => !['host', 'hack'].includes(p))) throw Error('invalid --require products');
    if (slices.some((s) => !idPattern.test(s))) throw Error('invalid slice ID');
    slices = [...new Set(slices)];
  } catch (err) {
    io.err(
      `${err.message}\nusage: node scripts/golden/diff.mjs REF_DIR CAND_DIR ` +
        '[--slice ID[,ID...]] [--propose] [--approved-dir DIR] [--require host,hack]',
    );
    return 2;
  }
  const ref = loadRecording(refDir),
    cand = loadRecording(candDir),
    errors = [...ref.errors, ...cand.errors],
    entries = [];
  if (errors.length) {
    errors.forEach((e) => io.err(`error: ${e}`));
    return 2;
  }
  for (const rec of [ref, cand]) {
    for (const product of required) {
      if (!rec.runs.get(`${product}/base`)?.requests.length)
        errors.push(`${rec === ref ? refDir : candDir}: required ${product}/base run missing or has zero requests`);
    }
    for (const run of rec.runs.values()) {
      if (run.scenario !== 'base') continue;
      for (const request of run.requests) {
        const prefix = request.name.split('/')[0];
        if (request.name.includes('/') && prefix !== 'base' && [ref, cand].some((r) => r.runs.has(`${run.product}/${prefix}`)))
          errors.push(`${run.dir}: base request name collides with scenario prefix: ${request.name}`);
      }
    }
  }
  for (const slice of slices) {
    const file = resolve(approvedDir, `${slice}.json`);
    if (!existsSync(file)) {
      io.err(`slice ${slice}: no approval file; contributes no entries`);
      continue;
    }
    try {
      const doc = JSON.parse(readFileSync(file, 'utf8')),
        invalid = validateApproval(doc, slice);
      errors.push(...invalid.map((e) => `${file}: ${e}`));
      if (!invalid.length)
        entries.push(...doc.entries.map((e, i) => ({ ...e, _source: `${slice} entry ${i + 1}`, _file: file })));
    } catch (err) {
      errors.push(`${file}: ${err.message}`);
    }
  }
  if (errors.length) {
    errors.forEach((e) => io.err(`error: ${e}`));
    return 2;
  }
  const result = compareRecordings(ref, cand, entries);
  const report = propose ? io.err : io.out;
  result.notes.forEach(report);
  result.problems.forEach((p) => report(describe(p)));
  result.approved.forEach((p) =>
    report(
      `approved: ${p.product}/${p.scenario} request=${JSON.stringify(p.request)} path=${p.path}: ${p.reason}` +
        (slices.length > 1 ? ` (from ${p.file}, ${p.source})` : ''),
    ),
  );
  if (result.approved.length) {
    for (const slice of slices) {
      const file = resolve(approvedDir, `${slice}.json`);
      report(`${file}: ${result.approved.filter((p) => p.file === file).length} approved difference(s)`);
    }
  }
  if (result.problems.length) report(`FAIL: ${result.problems.length} problem(s)`);
  else {
    const outcome = result.approved.length
      ? `${result.approved.length} approved difference(s) from ${slices.join(',')}`
      : 'identical';
    report(`ok: ${result.requests} requests in ${result.runs} runs, ${outcome}`);
  }
  if (propose) {
    result.proposalErrors.forEach((p) => io.err(describe(p)));
    const lines = result.proposals.map((e) => `    ${JSON.stringify(e)}`).join(',\n');
    io.out(
      `{\n  "slice": ${JSON.stringify(slices[0] ?? 'TODO')},\n` +
        `  "entries": [${lines ? `\n${lines}\n  ` : ''}]\n}`,
    );
  }
  return result.problems.length ? 1 : 0;
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url))
  process.exitCode = main();
