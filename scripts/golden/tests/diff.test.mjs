import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, symlinkSync, renameSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { loadRecording, compareRecordings, validateApproval, lineDiff, main, parsePath } from '../diff.mjs';
import { renderJSON, rendersLossy } from '../lib.mjs';
const script = fileURLToPath(new URL('../diff.mjs', import.meta.url));
const entry = (extra = {}) => ({
  product: 'host',
  request: 'GET /v1/skills',
  reason: 'The skill count changes.',
  at: 'body.count',
  old: 4,
  new: 3,
  ...extra,
});
const req = (body = { count: 4 }, extra = {}) => ({
  name: 'GET /v1/skills',
  method: 'GET',
  path: '/v1/skills',
  status: 200,
  headers: {},
  body_kind: 'json',
  body,
  ...extra,
});
const run = (requests, scenario = 'base', product = 'host') => ({ product, scenario, requests });
function recording(dir, runs) {
  mkdirSync(dir, { recursive: true });
  const manifest = { format: 2, runs: [] };
  for (const r of runs) {
    const stem = r.product + (r.scenario === 'base' ? '' : `.${r.scenario}`),
      file = `${stem}.json`,
      text_dir = `text/${stem}`;
    const requests = r.requests.map((original) => {
      const q = structuredClone(original),
        text = q._text;
      delete q._text;
      if (['json', 'text'].includes(q.body_kind)) {
        const content = text ?? renderJSON(q.body);
        const name = `${text_dir}/${q.name.replace(/[^A-Za-z0-9._-]/g, '_')}.txt`;
        mkdirSync(dirname(resolve(dir, name)), { recursive: true });
        writeFileSync(resolve(dir, name), content);
        q.text = {
          file: name,
          sha256: createHash('sha256').update(content).digest('hex'),
          length: Buffer.byteLength(content),
        };
      }
      if (!['json', 'binary'].includes(q.body_kind)) delete q.body;
      return q;
    });
    writeFileSync(resolve(dir, file), JSON.stringify({ format: 2, ...r, requests }));
    manifest.runs.push({
      product: r.product,
      scenario: r.scenario,
      file,
      text_dir,
      requests: requests.length,
    });
  }
  manifest.runs.sort((a, b) => `${a.product}/${a.scenario}`.localeCompare(`${b.product}/${b.scenario}`));
  writeFileSync(resolve(dir, 'manifest.json'), JSON.stringify(manifest));
}
function setup(t, a = [run([req()])], b = [run([req({ count: 3 })])]) {
  const root = mkdtempSync(resolve(tmpdir(), 'golden-diff-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const ref = resolve(root, 'ref'),
    cand = resolve(root, 'cand'),
    approvals = resolve(root, 'approvals');
  recording(ref, a);
  recording(cand, b);
  mkdirSync(approvals);
  const put = (entries, slice = 'S1') =>
    writeFileSync(resolve(approvals, `${slice}.json`), JSON.stringify({ slice, entries }));
  const compare = (entries = []) => {
    const a = loadRecording(ref),
      b = loadRecording(cand);
    assert.deepEqual(a.errors, []);
    assert.deepEqual(b.errors, []);
    return compareRecordings(a, b, entries);
  };
  const cli = (args = []) =>
    spawnSync(process.execPath, [script, ref, cand, '--require', 'host', '--approved-dir', approvals, ...args], {
      encoding: 'utf8',
      maxBuffer: 32 * 1024 * 1024,
      timeout: 5000,
    });
  return { root, ref, cand, approvals, put, compare, cli };
}
function pass(result) {
  assert.deepEqual(result.problems, []);
}
function fail(result, pattern) {
  assert.ok(result.problems.length);
  if (pattern) assert.match(JSON.stringify(result.problems), pattern);
}

test('approved value', (t) => {
  const f = setup(t);
  f.put([entry()]);
  assert.equal(f.cli(['--slice', 'S1']).status, 0);
  pass(f.compare([entry()]));
});
test('wrong old', (t) =>
  fail(setup(t).compare([entry({ old: 8 })]), /old does not equal the reference/));
test('wrong new', (t) =>
  fail(setup(t).compare([entry({ new: 8 })]), /new does not equal the candidate/));
test('unused entry', (t) => fail(setup(t).compare([entry({ request: 'missing' })]), /unused entry/));
test('an extra difference beside an approved one in the same request', (t) => {
  const f = setup(t, [run([req()])], [run([req({ count: 3 }, { status: 302 })])]);
  fail(f.compare([entry()]), /status/);
});
function textFixture(t, json = false, extra = false) {
  const a = req(json ? { sentence: 'old' } : undefined, {
    body_kind: json ? 'json' : 'text',
    ...(json ? {} : { _text: 'same\nold\nend\n' }),
  });
  const b = req(json ? { sentence: 'new' } : undefined, {
    body_kind: json ? 'json' : 'text',
    ...(json ? {} : { _text: `same\nnew\n${extra ? 'extra\n' : ''}end\n` }),
  });
  return setup(t, [run([a])], [run([b])]);
}
const textEntry = {
  product: 'host',
  request: 'GET /v1/skills',
  reason: 'The sentence changes.',
  at: 'text',
  removed: ['old'],
  added: ['new'],
};
const jsonTextEntry = { ...textEntry, removed: ['  "sentence": "old"'], added: ['  "sentence": "new"'] };
test('text lines approved (text body)', (t) => pass(textFixture(t).compare([textEntry])));
test('text lines approved (json body accounts for hash/length and body leaves)', (t) =>
  pass(textFixture(t, true).compare([jsonTextEntry])));
test('one extra changed line refused', (t) =>
  fail(textFixture(t, false, true).compare([textEntry]), /text lines do not match/));
const replaceEntry = {
  product: 'host',
  request: 'GET /v1/skills',
  reason: 'Remove the old qualifier.',
  at: 'body.result.tools[*].description',
  replace: { from: ' (not Simple Hack)', to: '' },
};
function tools(descriptions) {
  return req({ result: { tools: descriptions.map((description) => ({ description })) } });
}
function replaceFixture(
  t,
  values = ['one', 'two'],
  original = ['one (not Simple Hack)', 'two (not Simple Hack)'],
) {
  return setup(t, [run([tools(original)])], [run([tools(values)])]);
}
test('replace matching', (t) => pass(replaceFixture(t).compare([replaceEntry])));
test('replace with one non-matching value refused', (t) =>
  fail(replaceFixture(t, ['one', 'oops']).compare([replaceEntry]), /replace does not match/));
test('replace where nothing changed refused', (t) =>
  fail(replaceFixture(t, ['one', 'two'], ['one', 'two']).compare([replaceEntry]), /nothing changed/));
const changeEntry = (change) => ({
  product: 'host',
  request: 'extra',
  reason: 'The replay gains or loses this request.',
  change,
});
test('request removed', (t) =>
  pass(
    setup(t, [run([req(), req({}, { name: 'extra' })])], [run([req()])]).compare([
      changeEntry('removed'),
    ]),
  ));
test('request added with later requests otherwise identical', (t) =>
  pass(
    setup(t, [run([req()])], [run([req({}, { name: 'extra' }), req()])]).compare([changeEntry('added')]),
  ));
test('reorder refused', (t) =>
  fail(
    setup(
      t,
      [run([req(), req({}, { name: 'extra' })])],
      [run([req({}, { name: 'extra' }), req()])],
    ).compare(),
    /sequence/,
  ));
test('two slice files together', (t) => {
  const f = setup(t, [run([req()])], [run([req({ count: 3 }, { status: 302 })])]);
  f.put([entry()]);
  f.put([entry({ at: 'status', old: 200, new: 302 })], 'S2');
  assert.equal(f.cli(['--slice', 'S1,S2']).status, 0);
});
test('an entry in a named second slice file unused -> fails', (t) => {
  const f = setup(t);
  f.put([entry()]);
  f.put([entry({ request: 'missing' })], 'S2');
  const r = f.cli(['--slice', 'S1,S2']);
  assert.equal(r.status, 1);
  assert.match(r.stdout, /S2 entry 1: unused/);
});
test('--propose output accepted when pasted (after filling reasons)', (t) => {
  const f = setup(t);
  const p = f.cli(['--slice', 'S1', '--propose']);
  assert.equal(p.status, 1);
  const doc = JSON.parse(p.stdout);
  assert.equal(doc.entries.length, 1);
  assert.match(p.stderr, /unapproved/);
  assert.ok(validateApproval(doc, 'S1').length);
  doc.entries.forEach((e) => (e.reason = 'Reviewed count change.'));
  f.put(doc.entries);
  assert.equal(f.cli(['--slice', 'S1']).status, 0);
});
test('__absent__ on either side', (t) => {
  const f = setup(t, [run([req({ a: 1 })])], [run([req({ b: 2 })])]);
  pass(
    f.compare([
      entry({ at: 'body.a', old: 1, new: '__absent__' }),
      entry({ at: 'body.b', old: '__absent__', new: 2 }),
    ]),
  );
});
test('old/new pinned at an object path covering several leaves', (t) =>
  pass(
    setup(t, [run([req({ a: 1, b: 2 })])], [run([req({ a: 3, b: 4 })])]).compare([
      entry({ at: 'body', old: { a: 1, b: 2 }, new: { a: 3, b: 4 } }),
    ]),
  ));
test('two entries covering one difference refused', (t) => {
  fail(setup(t).compare([entry(), entry()]), /multiple entries/);
  fail(
    setup(t).compare([entry(), entry({ at: 'body', old: { count: 4 }, new: { count: 3 } })]),
    /multiple entries/,
  );
});
const scenarioEntry = (change) => ({
  product: 'host',
  scenario: 'rules-on',
  reason: 'The scenario changes with the new setting.',
  change,
});
test('scenario added approved by a whole-scenario entry and refused without it', (t) => {
  const f = setup(t, [run([req()])], [run([req()]), run([req()], 'rules-on')]);
  fail(f.compare(), /scenario/);
  const r = f.compare([scenarioEntry('added')]);
  pass(r);
  assert.match(r.notes.join(), /1 requests/);
});
test('scenario removed', (t) =>
  pass(
    setup(t, [run([req()]), run([req()], 'rules-on')], [run([req()])]).compare([
      scenarioEntry('removed'),
    ]),
  ));
test('an entry written "<scenario>/<name>"', (t) =>
  pass(
    setup(t, [run([req()], 'rules-on')], [run([req({ count: 3 })], 'rules-on')]).compare([
      entry({ request: 'rules-on/GET /v1/skills' }),
    ]),
  ));
test('a request name containing "/" in base', (t) => pass(setup(t).compare([entry()])));
test('no --slice with a difference fails', (t) => {
  const r = setup(t).cli();
  assert.equal(r.status, 1);
  assert.match(r.stdout, /host\/base.*body.count/);
});
test('identical folders pass', (t) => {
  const f = setup(
    t,
    [run([req()]), run([req()], 'base', 'hack')],
    [run([req()]), run([req()], 'base', 'hack')],
  );
  assert.match(f.cli().stdout, /ok: 2 requests in 2 runs, identical/);
  assert.equal(f.cli().status, 0);
});
test('integrity error (text file edited after recording) exits 2', (t) => {
  const f = setup(t);
  const r = JSON.parse(readFileSync(resolve(f.cand, 'host.json')));
  writeFileSync(resolve(f.cand, r.requests[0].text.file), 'tampered');
  const out = f.cli();
  assert.equal(out.status, 2);
  assert.match(out.stderr, /integrity/);
});
test('invalid entry file (two forms in one entry, unknown key, wrong slice name, TODO reason) exits 2', (t) => {
  const f = setup(t);
  for (const e of [
    entry({ change: 'added' }),
    entry({ surprise: true }),
    entry({ reason: 'TODO: review' }),
  ]) {
    f.put([e]);
    assert.equal(f.cli(['--slice', 'S1']).status, 2);
  }
  writeFileSync(resolve(f.approvals, 'S1.json'), JSON.stringify({ slice: 'wrong', entries: [] }));
  assert.equal(f.cli(['--slice', 'S1']).status, 2);
});
test('README and slice-text examples validate', () => {
  const readme = readFileSync(new URL('../approved/README.md', import.meta.url), 'utf8');
  const examples = [...readme.matchAll(/```json\n([\s\S]*?)\n```/g)].map((m) => JSON.parse(m[1]));
  assert.equal(examples.length, 8);
  examples.push(
    entry({
      at: 'body.result.tools[0].inputSchema.properties.rules',
      old: '__absent__',
      new: { type: 'object' },
    }),
  );
  assert.deepEqual(validateApproval({ slice: 'S1', entries: examples }, 'S1'), []);
});
test('bracket quoted keys and array indexes round trip through proposals', (t) => {
  const f = setup(t, [run([req({ 'a.b': [1] })])], [run([req({ 'a.b': [2, 3] })])]);
  const proposed = f.compare().proposals.map((e) => ({ ...e, reason: 'Reviewed leaf change.' }));
  assert.equal(proposed[0].at, 'body["a.b"][0]');
  assert.deepEqual(validateApproval({ slice: 'S1', entries: proposed }, 'S1'), []);
  pass(f.compare(proposed));
});
test('Myers shortest script handles long specifications and insertion/deletion ties', () => {
  const prefix = Array.from({ length: 20000 }, (_, i) => `line ${i}`).join('\n');
  assert.deepEqual(lineDiff(`${prefix}\na\nb\nz\n`, `${prefix}\nb\nc\nz\n`), {
    removed: ['a'],
    added: ['c'],
  });
  assert.deepEqual(lineDiff('a\n', 'a'), { removed: [''], added: [] });
});
test('Myers script lengths agree with independent small LCS oracle', () => {
  let seed = 8;
  const rand = () => {
    seed = (seed * 1664525 + 1013904223) >>> 0;
    return seed;
  };
  for (let k = 0; k < 200; k++) {
    const a = Array.from({ length: (rand() % 10) + 1 }, () => String(rand() % 4)),
      b = Array.from({ length: (rand() % 10) + 1 }, () => String(rand() % 4));
    const dp = Array.from({ length: a.length + 1 }, () => Array(b.length + 1).fill(0));
    for (let i = 1; i <= a.length; i++)
      for (let j = 1; j <= b.length; j++)
        dp[i][j] = a[i - 1] === b[j - 1] ? dp[i - 1][j - 1] + 1 : Math.max(dp[i - 1][j], dp[i][j - 1]);
    const d = lineDiff(a.join('\n'), b.join('\n'));
    assert.equal(d.removed.length + d.added.length, a.length + b.length - 2 * dp[a.length][b.length]);
  }
});
test('whole scenario entry for existing run unused; base scenario invalid', (t) => {
  fail(setup(t).compare([scenarioEntry('added')]), /unused/);
  assert.ok(
    validateApproval({ slice: 'S1', entries: [{ ...scenarioEntry('added'), scenario: 'base' }] }, 'S1')
      .length,
  );
});
test('unknown manifest files and duplicate names are integrity errors', (t) => {
  const f = setup(t);
  writeFileSync(resolve(f.ref, 'orphan.json'), '{}');
  assert.equal(f.cli().status, 2);
  const g = setup(t, [run([req(), req()])]);
  assert.equal(g.cli().status, 2);
});
test('text entry overlapping a structural body approval is refused', (t) =>
  fail(
    textFixture(t, true).compare([jsonTextEntry, entry({ at: 'body.sentence', old: 'old', new: 'new' })]),
    /multiple entries/,
  ));
test('main exported function returns usage error without exiting process', () => {
  const errors = [];
  assert.equal(main([], { out: () => {}, err: (s) => errors.push(s) }), 2);
  assert.match(errors.join(), /usage/);
});
test('literal examples from the slice specification validate', () => {
  const examples = [
    { product: 'host', request: 'GET /hackathons', reason: '…', at: 'status', old: 200, new: 302 },
    { product: 'host', request: 'GET /v1/skills', reason: '…', at: 'body.count', old: 4, new: 3 },
    {
      product: 'host',
      request: 'GET /openapi.json',
      reason: '…',
      at: 'text',
      removed: ['  description: Hosted hackathons …'],
      added: ['  description: …'],
    },
    {
      product: 'host',
      request: 'tools/list personal',
      reason: '…',
      at: 'body.result.tools[*].description',
      replace: { from: ' (not Simple Hack)', to: '' },
    },
    { product: 'hack', request: 'GET /v1/admin/usage', reason: '…', change: 'removed' },
    { product: 'host', scenario: 'rules-on', reason: '…', change: 'added' },
  ];
  assert.deepEqual(validateApproval({ slice: 'D0-S3b', entries: examples }, 'D0-S3b'), []);
});
test('proposal forms for text, added request, and added scenario are usable', (t) => {
  const cases = [
    [
      [run([req({}, { body_kind: 'text', _text: 'old\n' })])],
      [run([req({}, { body_kind: 'text', _text: 'new\n' })])],
    ],
    [[run([req()])], [run([req(), req({}, { name: 'extra' })])]],
    [[run([req()])], [run([req()]), run([req()], 'rules-on')]],
  ];
  for (const [a, b] of cases) {
    const f = setup(t, a, b);
    const out = f.cli(['--slice', 'S1', '--propose']);
    assert.equal(out.status, 1);
    const doc = JSON.parse(out.stdout);
    doc.entries.forEach((e) => (e.reason = 'Each change reviewed.'));
    assert.deepEqual(validateApproval(doc, 'S1'), []);
    f.put(doc.entries);
    assert.equal(f.cli(['--slice', 'S1']).status, 0);
  }
});
test('nested wildcard replacement and changed array length', (t) => {
  const e = { ...replaceEntry, at: 'body.groups[*][*]' };
  pass(
    setup(
      t,
      [run([req({ groups: [['a (not Simple Hack)'], ['b']] })])],
      [run([req({ groups: [['a'], ['b']] })])],
    ).compare([e]),
  );
  fail(
    setup(t, [run([tools(['a (not Simple Hack)'])])], [run([tools(['a', 'b'])])]).compare([
      replaceEntry,
    ]),
    /replace does not match/,
  );
});
test('quoted star is an object key, not an array wildcard', (t) => {
  const f = setup(t, [run([req({ '*': 1 })])], [run([req({ '*': 2 })])]);
  const e = entry({ at: 'body["*"]', old: 1, new: 2 });
  assert.deepEqual(validateApproval({ slice: 'S1', entries: [e] }, 'S1'), []);
  pass(f.compare([e]));
});
test('absent versus a literal sentinel is still a difference', (t) => {
  fail(setup(t, [run([req({})])], [run([req({ key: '__absent__' })])]).compare(), /unapproved/);
});
test('body-kind changes require kind and body approvals', (t) => {
  const f = setup(
    t,
    [run([req({ sentence: 'old' })])],
    [run([req({}, { body_kind: 'text', _text: 'new\n' })])],
  );
  fail(f.compare([{ ...textEntry, ...lineDiff(renderJSON({ sentence: 'old' }), 'new\n') }]), /body_kind/);
  pass(
    f.compare([
      { ...textEntry, ...lineDiff(renderJSON({ sentence: 'old' }), 'new\n') },
      entry({ at: 'body_kind', old: 'json', new: 'text' }),
      entry({ at: 'body', old: { sentence: 'old' }, new: '__absent__' }),
    ]),
  );
});
test('integrity reports all corrupt requests before comparison', (t) => {
  const f = setup(t, [run([req(), req({}, { name: 'second' })])]);
  const requests = JSON.parse(readFileSync(resolve(f.ref, 'host.json'))).requests;
  for (const r of requests) writeFileSync(resolve(f.ref, r.text.file), 'corrupt');
  const out = f.cli();
  assert.equal(out.status, 2);
  assert.match(out.stderr, /GET \/v1\/skills: text integrity/);
  assert.match(out.stderr, /second: text integrity/);
});
test('schema path forms and reasons agree with hand validation for examples', () => {
  const schema = JSON.parse(readFileSync(new URL('../approved/schema.json', import.meta.url)));
  const forms = schema.properties.entries.items.oneOf;
  const e = entry({ reason: '\nReviewed change.' });
  assert.deepEqual(validateApproval({ slice: 'S1', entries: [e] }, 'S1'), []);
  assert.ok(new RegExp(forms[0].properties.reason.pattern).test(e.reason));
  for (const at of ['body.result.tools[*].description', 'body.groups[*][*]'])
    assert.ok(new RegExp(forms[2].properties.at.pattern).test(at));
  assert.equal(new RegExp(forms[2].properties.at.pattern).test('body["[*]"]'), false);
});

test('large empty and wholly changed text stays fast and proposals round trip', (t) => {
  const oldLines = Array.from({ length: 50000 }, (_, i) => `reference ${i}`);
  const newLines = Array.from({ length: 50000 }, (_, i) => `candidate ${i}`);
  for (const before of [[], oldLines]) {
    const a = before.join('\n');
    const b = newLines.join('\n');
    const started = performance.now();
    assert.deepEqual(lineDiff(a, b), { removed: before, added: newLines });
    assert.ok(performance.now() - started < 3000, 'line diff must finish within three seconds');
    const f = setup(
      t,
      [run([req({}, { body_kind: 'text', _text: a })])],
      [run([req({}, { body_kind: 'text', _text: b })])],
    );
    const proposeStarted = performance.now();
    const proposed = f.cli(['--slice', 'S1', '--propose']);
    assert.equal(proposed.error, undefined);
    assert.equal(proposed.status, 1);
    assert.ok(performance.now() - proposeStarted < 4000, 'proposal must finish within four seconds');
    const doc = JSON.parse(proposed.stdout);
    assert.equal(doc.entries.length, 1);
    assert.deepEqual(doc.entries[0].removed, before);
    assert.deepEqual(doc.entries[0].added, newLines);
    doc.entries[0].reason = 'The full replacement was reviewed.';
    f.put(doc.entries);
    const approvedStarted = performance.now();
    const approved = f.cli(['--slice', 'S1']);
    assert.equal(approved.error, undefined);
    assert.equal(approved.status, 0);
    assert.ok(performance.now() - approvedStarted < 4000, 'approval must finish within four seconds');
  }
  assert.deepEqual(lineDiff(newLines.join('\n'), ''), { removed: newLines, added: [] });
});

test('small edit in 50000 lines remains minimal', () => {
  const a = Array.from({ length: 50000 }, (_, i) => `line ${i}`);
  const b = [...a];
  b[25000] = 'edited line';
  const started = performance.now();
  assert.deepEqual(lineDiff(a.join('\n'), b.join('\n')), {
    removed: ['line 25000'],
    added: ['edited line'],
  });
  assert.ok(performance.now() - started < 3000);
});

test('fallback retains common prefix and suffix', () => {
  const a = Array.from({ length: 2200 }, (_, i) => `old ${i}`);
  const b = Array.from({ length: 2200 }, (_, i) => `new ${i}`);
  assert.deepEqual(
    lineDiff(['prefix', ...a, 'suffix'].join('\n'), ['prefix', ...b, 'suffix'].join('\n')),
    { removed: a, added: b },
  );
});

test('unknown recording fields and invalid request types exit 2', (t) => {
  const cases = [
    [
      'manifest.json',
      (d) => {
        d.extra = true;
      },
      /unexpected manifest keys/,
    ],
    [
      'manifest.json',
      (d) => {
        d.runs[0].extra = true;
      },
      /unexpected manifest run keys/,
    ],
    [
      'host.json',
      (d) => {
        d.extra = true;
      },
      /unexpected run keys/,
    ],
    [
      'host.json',
      (d) => {
        d.requests[0].extra = true;
      },
      /unexpected request keys/,
    ],
  ];
  for (const [field, values] of Object.entries({
    method: [null, 1],
    path: [null, []],
    status: [200.5, '200'],
    headers: [null, []],
  })) {
    for (const value of values) {
      cases.push([
        'host.json',
        (d) => {
          d.requests[0][field] = value;
        },
        /invalid request method\/path\/status\/headers/,
      ]);
    }
    cases.push([
      'host.json',
      (d) => {
        delete d.requests[0][field];
      },
      /invalid request method\/path\/status\/headers/,
    ]);
  }
  for (const [file, mutate, pattern] of cases) {
    const f = setup(t);
    const path = resolve(f.cand, file);
    const doc = JSON.parse(readFileSync(path));
    mutate(doc);
    writeFileSync(path, JSON.stringify(doc));
    const out = f.cli();
    assert.equal(out.status, 2);
    assert.match(out.stderr, pattern);
  }
});

test('JSON text approval must list both changed fields', (t) => {
  const f = setup(t, [run([req({ a: 'old a', b: 'old b' })])], [run([req({ a: 'new a', b: 'new b' })])]);
  const partial = { ...textEntry, removed: ['  "a": "old a",'], added: ['  "a": "new a",'] };
  const result = f.compare([partial]);
  fail(result, /text lines do not match/);
  assert.equal(result.problems.filter((p) => p.message === 'unapproved difference').length, 2);
  assert.ok(!result.problems.some((p) => /unused entry/.test(p.message)));
  const full = {
    ...partial,
    removed: ['  "a": "old a",', '  "b": "old b"'],
    added: ['  "a": "new a",', '  "b": "new b"'],
  };
  pass(f.compare([full]));
  fail(
    f.compare([full, entry({ at: 'body.b', old: 'old b', new: 'new b' })]),
    /difference covered by multiple entries/,
  );
});

test('specific approval failures suppress unused but keep unapproved differences', (t) => {
  const cases = [
    [setup(t), entry({ old: 8 }), /old does not equal the reference/],
    [setup(t), entry({ new: 8 }), /new does not equal the candidate/],
    [textFixture(t), { ...textEntry, added: ['wrong'] }, /text lines do not match/],
    [replaceFixture(t, ['one', 'oops']), replaceEntry, /replace does not match/],
  ];
  for (const [f, e, pattern] of cases) {
    f.put([e]);
    const out = f.cli(['--slice', 'S1']);
    assert.equal(out.status, 1);
    assert.match(out.stdout, pattern);
    assert.match(out.stdout, /unapproved difference/);
    assert.doesNotMatch(out.stdout, /unused entry/);
  }
});

test('whole scenario present in both recordings explains how to record and compare', (t) => {
  const runs = [run([req()]), run([req()], 'rules-on')];
  for (const change of ['added', 'removed']) {
    const f = setup(t, runs, runs);
    f.put([scenarioEntry(change)]);
    const out = f.cli(['--slice', 'S1']);
    assert.equal(out.status, 1);
    if (change === 'added') {
      assert.match(out.stdout, /reference also recorded scenario rules-on/);
      assert.match(out.stdout, /run.mjs --scenario all --skip-scenario rules-on/);
    } else {
      assert.match(out.stdout, /candidate also recorded scenario rules-on/);
      assert.match(out.stdout, /candidate no longer records/);
      assert.doesNotMatch(out.stdout, /skip-scenario/);
    }
    assert.match(out.stdout, /compare request by request/);
    assert.doesNotMatch(out.stdout, /unused entry/);
  }
});

test('missing first slice and present second slice work with notice on stderr', (t) => {
  const f = setup(t);
  f.put([entry()], 'B');
  const out = f.cli(['--slice', 'A,B']);
  assert.equal(out.status, 0);
  assert.match(out.stderr, /slice A: no approval file; contributes no entries/);
  assert.doesNotMatch(out.stdout, /no approval file/);
});

test('success lists each approved difference and reason', (t) => {
  const f = setup(t, [run([req()])], [run([req({ count: 3 }, { status: 302 })])]);
  assert.equal(f.cli().status, 1);
  const count = entry();
  const status = entry({ at: 'status', old: 200, new: 302, reason: 'The endpoint redirects.' });
  f.put([count, status]);
  const out = f.cli(['--slice', 'S1']);
  assert.equal(out.status, 0);
  for (const e of [count, status]) {
    assert.ok(
      out.stdout.includes(`approved: host/base request="GET /v1/skills" path=${e.at}: ${e.reason}`),
    );
  }
  assert.match(out.stdout, /ok: 1 requests in 1 runs, 2 approved difference\(s\) from S1/);
  assert.ok(out.stdout.includes(`${resolve(f.approvals, 'S1.json')}: 2 approved difference(s)`));
});

test('multiple slices attribute used entries and count differences per file even on failure', (t) => {
  const f = setup(t, [run([req()])], [run([req({ count: 3 }, { status: 302, method: 'POST' })])]);
  f.put([entry()], 'A');
  f.put([entry({ at: 'status', old: 200, new: 302 })], 'B');
  const out = f.cli(['--slice', 'A,B,C']);
  assert.equal(out.status, 1);
  const lines = out.stdout.split('\n').filter((l) => l.startsWith('approved:'));
  assert.equal(lines.length, 2);
  for (const slice of ['A', 'B']) {
    assert.ok(lines.some((line) => line.includes(`from ${resolve(f.approvals, `${slice}.json`)}, ${slice} entry 1`)));
    assert.ok(out.stdout.includes(`${resolve(f.approvals, `${slice}.json`)}: 1 approved difference(s)`));
  }
  assert.ok(out.stdout.includes(`${resolve(f.approvals, 'C.json')}: 0 approved difference(s)`));
});

test('long unapproved values point to both absolute text files', (t) => {
  for (const longSide of ['old', 'new']) {
    const body = (side) => ({ value: side === longSide ? 'x'.repeat(701) : 'short' });
    const f = setup(t, [run([req(body('old'))])], [run([req(body('new'))])]);
    const out = f.cli();
    assert.equal(out.status, 1);
    assert.match(out.stdout, /…/);
    for (const [label, dir] of [['reference', f.ref], ['candidate', f.cand]])
      assert.ok(out.stdout.includes(`${label}: ${resolve(dir, 'text/host/GET__v1_skills.txt')}`));
    assert.match(out.stdout, /diff the text files/);
  }
});

test('text diagnostics show at most 40 changed lines and locate full files', (t) => {
  const text = (prefix, length) => req(undefined, {
    body_kind: 'text', _text: Array.from({ length }, (_, i) => `${prefix}${i}`).join('\n'),
  });
  for (const length of [20, 21]) {
    const f = setup(t, [run([text('old', length)])], [run([text('new', length)])]);
    const out = f.cli();
    assert.equal(out.status, 1);
    assert.equal(out.stdout.split('\n').filter((l) => /^[+-] /.test(l)).length, 40);
    if (length === 21) {
      assert.match(out.stdout, /2 more changed line\(s\)/);
      assert.doesNotMatch(out.stdout, /\+ new19|\+ new20/);
      for (const dir of [f.ref, f.cand])
        assert.ok(out.stdout.includes(resolve(dir, 'text/host/GET__v1_skills.txt')));
    } else assert.doesNotMatch(out.stdout, /more changed line|diff the text files/);
  }
});

function proposalRoundTrip(f) {
  const out = f.cli(['--slice', 'S1', '--propose']);
  assert.equal(out.status, 1, out.stderr);
  const doc = JSON.parse(out.stdout);
  doc.entries.forEach((e) => { e.reason = 'Reviewed exact change.'; });
  assert.deepEqual(validateApproval(doc, 'S1'), []);
  f.put(doc.entries);
  const approved = f.cli(['--slice', 'S1']);
  assert.equal(approved.status, 0, approved.stdout + approved.stderr);
  return doc.entries;
}

test('JSON text approval leaves rendering-invisible leaves for a separate pin', (t) => {
  const old = { hidden: '{"x":1}', visible: 'old' };
  const next = { hidden: '{ "x": 1 }', visible: 'new' };
  const f = setup(t, [run([req(old)])], [run([req(next)])]);
  const text = { ...textEntry, removed: ['  "visible": "old"'], added: ['  "visible": "new"'] };
  const result = f.compare([text]);
  assert.equal(result.problems.length, 1);
  assert.equal(result.problems[0].path, 'body.hidden');
  pass(f.compare([text, entry({ at: 'body.hidden', old: old.hidden, new: next.hidden })]));
});

test('JSON files with valid byte metadata must match renderJSON(body)', (t) => {
  const f = setup(t,
    [run([req({ permissions: 'read' }, { _text: 'old\n' })])],
    [run([req({ permissions: 'admin' }, { _text: 'new\n' })])]);
  f.put([textEntry]);
  const out = f.cli(['--slice', 'S1']);
  assert.equal(out.status, 2);
  assert.match(out.stderr, /GET \/v1\/skills: text integrity error \(JSON rendering\)/);
});

for (const reverse of [false, true]) {
  test(`sentinel only pins absence; literal string needs parent pin (${reverse})`, (t) => {
    const a = { key: '__absent__', other: 1 }, b = { key: 3, other: 2 };
    const [old, next] = reverse ? [b, a] : [a, b];
    const f = setup(t, [run([req(old)])], [run([req(next)])]);
    fail(f.compare([entry({ at: 'body.key', old: old.key, new: next.key })]), /does not equal/);
    pass(f.compare([entry({ at: 'body', old, new: next })]));
    const out = f.cli(['--propose']);
    assert.equal(out.status, 1);
    assert.match(out.stderr, /cannot propose: the value is the reserved string __absent__; pin an enclosing object by hand/);
    assert.deepEqual(JSON.parse(out.stdout).entries.map((e) => e.at), ['body.other']);
  });
  test(`literal sentinel versus actual absence cannot be proposed (${reverse})`, (t) => {
    const [a, b] = reverse ? [{ key: '__absent__' }, {}] : [{}, { key: '__absent__' }];
    const f = setup(t, [run([req(a)])], [run([req(b)])]);
    fail(f.compare([entry({ at: 'body.key', old: '__absent__', new: '__absent__' })]));
    const out = f.cli(['--propose']);
    assert.equal(out.status, 1);
    assert.match(out.stderr, /cannot propose:/);
    assert.deepEqual(JSON.parse(out.stdout).entries, []);
    pass(f.compare([entry({ at: 'body', old: a, new: b })]));
  });
}

test('base names colliding with scenarios in either folder are integrity errors', (t) => {
  for (const scenarioSide of ['ref', 'cand']) {
    const base = run([req({ v: 1 }, { name: 'rules-on/x' })]);
    const scenario = run([req({ v: 2 }, { name: 'x' })], 'rules-on');
    const f = setup(t, [base, ...(scenarioSide === 'ref' ? [scenario] : [])],
      [base, ...(scenarioSide === 'cand' ? [scenario] : [])]);
    const out = f.cli(['--propose']);
    assert.equal(out.status, 2);
    assert.match(out.stderr, /base request name collides with scenario prefix/);
  }
});

test('scenario request proposals use scenario prefix and round trip', (t) => {
  const f = setup(t, [run([req()]), run([req({ v: 1 }, { name: 'x' })], 'rules-on')],
    [run([req()]), run([req({ v: 2 }, { name: 'x' })], 'rules-on')]);
  assert.equal(proposalRoundTrip(f)[0].request, 'rules-on/x');
});

test('scenario prefixes are scoped to the product', (t) => {
  const a = [run([req({ v: 1 }, { name: 'rules-on/x' })]), run([req()], 'rules-on', 'hack')];
  const b = [run([req({ v: 2 }, { name: 'rules-on/x' })]), run([req()], 'rules-on', 'hack')];
  const f = setup(t, a, b);
  proposalRoundTrip(f);
});

for (const [from, to] of [['text', 'empty'], ['empty', 'text'], ['json', 'empty'], ['json', 'binary']]) {
  test(`${from} to ${to} proposal includes exact text, kind and body pins`, (t) => {
    const q = (kind) => req(kind === 'binary' ? { sha256: 'abc', length: 3 } : { v: 1 },
      { body_kind: kind, ...(kind === 'text' ? { _text: 'hello\n' } : {}) });
    const f = setup(t, [run([q(from)])], [run([q(to)])]);
    const proposed = proposalRoundTrip(f);
    assert.ok(proposed.some((e) => e.at === 'body_kind'));
    assert.ok(proposed.some((e) => e.at === 'text'));
    if (from === 'json' || to === 'binary') assert.ok(proposed.some((e) => e.at === 'body'));
    if (from === 'json') {
      fail(f.compare(proposed.filter((e) => e.at !== 'body')), /unapproved difference/);
      assert.equal(f.compare(proposed.filter((e) => e.at !== 'body')).problems[0].path, 'body');
    }
  });
}

test('invalid UTF-8 fails integrity even with matching bytes metadata', (t) => {
  const f = setup(t,
    [run([req(undefined, { body_kind: 'text', _text: Buffer.from([0x80]) })])],
    [run([req(undefined, { body_kind: 'text', _text: Buffer.from([0x81]) })])]);
  const out = f.cli();
  assert.equal(out.status, 2);
  assert.match(out.stderr, /invalid UTF-8/);
});

test('schema and parsePath share JSON quoted-key grammar', () => {
  const forms = JSON.parse(readFileSync(new URL('../approved/schema.json', import.meta.url)))
    .properties.entries.items.oneOf;
  const patterns = [0, 2].map((i) => new RegExp(forms[i].properties.at.pattern));
  const cases = [
    ['status', true], ['headers["content-type"]', true], ['body["a.b"][0]', true],
    ['body["\\u0000"]', true], ['body["\\\"\\\\\\/\\b\\f\\n\\r\\t"]', true],
    ['body["q"][*].x', true], ['body["[*]"]', true], ['body[*][*]', true],
    ['body["\\q"]', false], ['body["\n"]', false], ['body["\x00"]', false],
    ['body["\\u123"]', false], ['body["\\uGGGG"]', false], ['body["x]', false],
    ['body[-1]', false], ['body[]', false], ['headers[*]', false], ['headers[0]', false],
    ['status.x', false], ['body\n', false], ['body["x"]\n', false], ['body.foo-bar', false],
  ];
  for (const [path, valid] of cases) {
    assert.equal(patterns.some((p) => p.test(path)), valid, path);
    if (valid) assert.doesNotThrow(() => parsePath(path), path);
    else assert.throws(() => parsePath(path), undefined, path);
  }
});

test('text metadata accepts only exact keys and prescribed types', (t) => {
  for (const mutate of [
    (m) => { m.extra = 1; }, (m) => { delete m.length; },
    (m) => { m.sha256 = 3; }, (m) => { m.length = '5'; },
    (m) => { m.file = 1; }, (m) => { m.length = -1; },
  ]) {
    const f = setup(t);
    const file = resolve(f.ref, 'host.json'), d = JSON.parse(readFileSync(file));
    mutate(d.requests[0].text);
    writeFileSync(file, JSON.stringify(d));
    assert.equal(f.cli().status, 2);
  }
});

test('symlinked files, directories, root, and unused artifacts fail integrity', (t) => {
  for (const target of ['text/host/GET__v1_skills.txt', 'text/host', '', 'extra']) {
    const f = setup(t);
    if (target === 'extra') symlinkSync(f.cand, resolve(f.ref, target));
    else {
      const path = resolve(f.ref, target), moved = resolve(f.root, 'outside');
      renameSync(path, moved);
      symlinkSync(moved, path);
    }
    const out = f.cli();
    assert.equal(out.status, 2);
    assert.match(out.stderr, /symlink in recording/);
  }
});

test('unused null pins remain null in diagnostics', (t) => {
  const f = setup(t);
  f.put([entry({ request: 'missing', at: 'body', old: null, new: null })]);
  const out = f.cli(['--slice', 'S1']);
  assert.match(out.stdout, /unused entry\n  old: null\n  new: null/);
});

test('truncated mismatch diagnostics locate approval entry and recorded values', (t) => {
  for (const kind of ['json', 'binary', 'headers']) {
    const old = 'a'.repeat(800), next = 'b'.repeat(800);
    const q = (value) => kind === 'headers' ? req({}, { headers: { long: value } }) :
      req({ value }, { body_kind: kind });
    const f = setup(t, [run([q(old)])], [run([q(next)])]);
    f.put([entry({ at: kind === 'headers' ? 'headers.long' : 'body.value', old: 'wrong'.repeat(200), new: next })]);
    const out = f.cli(['--slice', 'S1']);
    assert.equal(out.status, 1);
    assert.ok(out.stdout.includes(`${resolve(f.approvals, 'S1.json')}: S1 entry 1`));
    for (const dir of [f.ref, f.cand])
      assert.ok(out.stdout.includes(resolve(dir, kind === 'json' ? 'text/host/GET__v1_skills.txt' : 'host.json')));
  }
});

test('default requires both nonempty product base runs in both recordings', (t) => {
  const both = [run([req()]), run([req()], 'base', 'hack')];
  for (const [a, b] of [[[], []], [both, [run([req()])]],
    [[run([req()])], both], [both, [run([req()]), run([], 'base', 'hack')]]]) {
    const f = setup(t, a, b);
    const out = spawnSync(process.execPath, [script, f.ref, f.cand], { encoding: 'utf8' });
    assert.equal(out.status, 2);
    assert.match(out.stderr, /(?:required (?:host|hack)\/base run missing or has zero requests|(?:host|hack)\/base: run has zero requests)/);
  }
  const f = setup(t, [run([req()])], [run([req()])]);
  assert.equal(f.cli().status, 0);
  assert.equal(f.cli(['--require', 'hack']).status, 2);
  assert.equal(f.cli(['--require', '']).status, 2);
  assert.equal(f.cli(['--require', 'other']).status, 2);
});

test('a base name equal to a scenario name has no colliding slash prefix', (t) => {
  const runs = [run([req({}, { name: 'rules-on' })]), run([req()], 'rules-on')];
  assert.equal(setup(t, runs, runs).cli().status, 0);
});

test('empty text to absent text has zero changed lines', (t) => {
  const f = setup(t, [run([req(undefined, { body_kind: 'text', _text: '' })])],
    [run([req(undefined, { body_kind: 'empty' })])]);
  const proposed = proposalRoundTrip(f);
  assert.deepEqual(proposed.map((e) => e.at), ['body_kind']);
});

test('UTF-8 BOM remains significant text content', (t) => {
  const f = setup(t, [run([req(undefined, { body_kind: 'text', _text: '\uFEFFhello' })])],
    [run([req(undefined, { body_kind: 'text', _text: 'hello' })])]);
  const proposed = proposalRoundTrip(f);
  assert.deepEqual(proposed[0].removed, ['\uFEFFhello']);
});

test('literal sentinel array elements do not promote to a parent pin', (t) => {
  const f = setup(t, [run([req({ a: ['__absent__', 1] })])], [run([req({ a: [2, 3] })])]);
  const out = f.cli(['--propose']);
  assert.equal(out.status, 1);
  assert.match(out.stderr, /cannot propose:/);
  assert.deepEqual(JSON.parse(out.stdout).entries.map((e) => e.at), ['body.a[1]']);
});

test('rendersLossy identifies embedded documents and preserves ordinary string forms', () => {
  for (const value of ['{"a":1}', '{ "a":2 }', '1', 'true', 'null', '"quoted"', '[1]',
    ' 2\n', -0, Infinity, { nested: ['false'] }]) assert.equal(rendersLossy(value), true);
  for (const value of ['ordinary', '', 'a\nb', '"""\n| not JSON\n"""', 0, 1, false, null,
    { nested: ['ordinary\nstring', 2] }]) assert.equal(rendersLossy(value), false);
  assert.notEqual(renderJSON('a\nb'), renderJSON('a\n b'));
  assert.notEqual(renderJSON('"quoted'), renderJSON('quoted'));
});

test('mixed visible and invisible embedded-JSON change needs a pin beside exact text lines', (t) => {
  const old = { x: '{"a":1}' }, next = { x: '{ "a":2 }' };
  const f = setup(t, [run([req(old)])], [run([req(next)])]);
  const text = { ...textEntry, removed: ['  |   "a": 1'], added: ['  |   "a": 2'] };
  const pin = entry({ at: 'body.x', old: old.x, new: next.x });
  fail(f.compare([text]), /body.x/);
  pass(f.compare([text, pin]));
  fail(f.compare([text, text, pin]), /multiple entries/);
  f.put([text]);
  assert.equal(f.cli(['--slice', 'S1']).status, 1);
  f.put([text, pin]);
  assert.equal(f.cli(['--slice', 'S1']).status, 0);
  rmSync(resolve(f.approvals, 'S1.json'));
  pass(f.compare([pin]));
  fail(f.compare([{ ...text, added: ['wrong'] }, pin]), /text lines do not match/);
  const proposed = proposalRoundTrip(f);
  assert.deepEqual(proposed.map((e) => e.at), ['body.x']);
});

test('embedded scalar strings also need structural pins for visible changes', (t) => {
  const f = setup(t, [run([req({ x: '1' })])], [run([req({ x: ' 2 ' })])]);
  const text = { ...textEntry, ...lineDiff(renderJSON({ x: '1' }), renderJSON({ x: ' 2 ' })) };
  fail(f.compare([text]), /body.x/);
  pass(f.compare([text, entry({ at: 'body.x', old: '1', new: ' 2 ' })]));
  assert.deepEqual(proposalRoundTrip(f).map((e) => e.at), ['body.x']);
});

for (const reverse of [false, true]) {
  for (const kind of ['root', 'header', 'header-plus-body', 'kind-transition']) {
    test(`reserved sentinel fails closed at ${kind}, reverse=${reverse}`, (t) => {
      let a, b;
      if (kind === 'root') [a, b] = [req('__absent__'), req('x')];
      else if (kind === 'kind-transition')
        [a, b] = [req('__absent__'), req(undefined, { body_kind: 'empty' })];
      else [a, b] = [req({ v: 1 }, { headers: { location: '__absent__' } }),
        req({ v: kind === 'header-plus-body' ? 2 : 1 }, { headers: { location: 'x' } })];
      if (reverse) [a, b] = [b, a];
      const f = setup(t, [run([a])], [run([b])]);
      const plain = f.cli();
      assert.equal(plain.error, undefined);
      assert.equal(plain.status, 1, plain.stderr);
      const out = f.cli(['--slice', 'S1', '--propose']);
      assert.equal(out.error, undefined);
      assert.equal(out.status, 1, out.stderr);
      assert.match(out.stderr, /cannot propose: the value is the reserved string __absent__; pin an enclosing object by hand/);
      const doc = JSON.parse(out.stdout);
      doc.entries.forEach((e) => { e.reason = 'Reviewed exact change.'; });
      assert.deepEqual(validateApproval(doc, 'S1'), []);
      assert.ok(doc.entries.every((e) => e.at !== 'headers' && e.at !== 'body' && e.at !== 'headers.location'));
      if (kind === 'header-plus-body') assert.deepEqual(doc.entries.map((e) => e.at), ['body.v']);
      f.put(doc.entries);
      assert.equal(f.cli(['--slice', 'S1']).status, 1);
    });
  }
}

test('sentinel proposals do not overlap approved siblings across slices', (t) => {
  for (const stacked of [false, true]) {
    const f = setup(t, [run([req({ a: '__absent__', b: 1, c: 1 })])],
      [run([req({ a: 2, b: 2, c: 2 })])]);
    const b = entry({ at: 'body.b', old: 1, new: 2 });
    const c = entry({ at: 'body.c', old: 1, new: 2 });
    f.put(stacked ? [b] : [b, c]);
    if (stacked) f.put([c], 'S2');
    const out = f.cli(['--slice', stacked ? 'S1,S2' : 'S1', '--propose']);
    assert.equal(out.status, 1, out.stderr);
    assert.match(out.stderr, /cannot propose:/);
    assert.deepEqual(JSON.parse(out.stdout).entries, []);
    assert.doesNotMatch(out.stderr, /multiple entries/);
  }
});

for (const invalid of ['null', 'missing', 'number']) {
  test(`invalid request ${invalid} returns integrity status before collisions`, (t) => {
    const runs = [run([req()]), run([req()], 'base', 'hack')];
    const f = setup(t, runs, runs);
    const file = resolve(f.ref, 'host.json'), data = JSON.parse(readFileSync(file));
    if (invalid === 'null') data.requests = [null];
    else if (invalid === 'missing') delete data.requests[0].name;
    else data.requests[0].name = 42;
    writeFileSync(file, JSON.stringify(data));
    const out = f.cli(['--require', 'host,hack']);
    assert.equal(out.status, 2, out.stderr);
    assert.match(out.stderr, /unexpected request keys|invalid request name/);
    assert.doesNotMatch(out.stderr, /TypeError|\.split is not a function/);
  });
}

for (const artifact of ['host.json', 'text/host/GET__v1_skills.txt', 'manifest.json', 'extra']) {
  test(`FIFO ${artifact} is rejected before reading`, (t) => {
    const f = setup(t);
    const file = resolve(f.ref, artifact);
    rmSync(file, { force: true });
    const made = spawnSync('mkfifo', [file], { encoding: 'utf8' });
    if (made.error?.code === 'ENOENT') { t.skip('mkfifo unavailable'); return; }
    assert.equal(made.status, 0, made.stderr);
    const out = f.cli();
    assert.equal(out.error, undefined, 'comparator must not hang reading a FIFO');
    assert.equal(out.status, 2);
    assert.match(out.stderr, /not a regular file in recording/);
  });
}

test('long rendering-invisible JSON strings name both run JSON files', (t) => {
  const old = { x: '{"a":1}' }, next = { x: '{' + ' '.repeat(800) + '"a":1}' };
  assert.equal(renderJSON(old), renderJSON(next));
  const f = setup(t, [run([req(old)])], [run([req(next)])]);
  const out = f.cli();
  assert.equal(out.status, 1);
  assert.match(out.stdout, /…/);
  for (const dir of [f.ref, f.cand]) {
    assert.ok(out.stdout.includes(resolve(dir, 'host.json')));
    assert.ok(out.stdout.includes(resolve(dir, 'text/host/GET__v1_skills.txt')));
  }
});

test('empty named runs fail integrity on either side even when approved whole', (t) => {
  for (const side of ['ref', 'cand', 'both']) {
    const base = run([req()]), empty = run([], 'rules-on');
    const f = setup(t, [base, ...(side !== 'cand' ? [empty] : [])],
      [base, ...(side !== 'ref' ? [empty] : [])]);
    f.put([{ product: 'host', scenario: 'rules-on', change: side === 'ref' ? 'removed' : 'added',
      reason: 'Reviewed scenario change.' }]);
    const out = f.cli(['--slice', 'S1']);
    assert.equal(out.status, 2);
    assert.match(out.stderr, /host\/rules-on: run has zero requests/);
  }
});
