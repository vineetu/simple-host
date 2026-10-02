#!/usr/bin/env node
// Contract smoke test for the hosted auth.js storage helpers. The whole
// network is mocked in Chromium: no production site or account is touched.
// Run with NODE_PATH pointing at a Playwright installation.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('playwright');

const script = fs.readFileSync(path.join(__dirname, '..', 'internal/handler/static/auth.js'), 'utf8');
const host = 'demo.owner.simple-host.app';
const seen = [];
const json = (value, status = 200) => ({ status, contentType: 'application/json', body: JSON.stringify(value) });

async function answer(route) {
  const req = route.request(), url = new URL(req.url()), p = url.pathname;
  if (p === '/' && url.hostname.endsWith('.owner.simple-host.app')) {
    return route.fulfill({ contentType: 'text/html', body: '<script>window.SH_CONFIG={site:"demo"}</script><script src="https://simple-host.app/auth.js"></script>' });
  }
  if (p === '/demo/' && url.hostname === 'owner.simple-host.app') {
    return route.fulfill({ contentType: 'text/html', body: '<script src="https://simple-host.app/auth.js"></script>' });
  }
  if (url.hostname === 'simple-host.app' && p === '/auth.js') return route.fulfill({ contentType: 'text/javascript', body: script });
  if (p === '/v1/auth/oauth/providers') return route.fulfill(json({ providers: [] }));
  if (p === '/v1/sites/demo/me') return route.fulfill(json({ signed_in: false, sign_in_available: true }));
  seen.push({ host: url.hostname, path: p + url.search, method: req.method(), headers: req.headers(), body: req.postDataBuffer() });
  if (p === '/v1/sites/demo/storage/kv/settings/keys') return route.fulfill(json({ items: [{ key: 'theme', value: 'dark' }], next_after: '' }));
  if (p.endsWith('/storage/kv/settings/keys/blocked')) return route.fulfill(json({ error: 'sign in first', code: 'sign_in_required' }, 401));
  if (p.endsWith('/storage/kv/settings/keys/locked')) return route.fulfill(json({ error: 'unlock site', code: 'site_locked' }, 403));
  if (p.endsWith('/storage/kv/settings/keys/theme')) {
    if (req.method() === 'GET') return route.fulfill(json({ key: 'theme', value: 'dark' }));
    if (req.method() === 'PUT') return route.fulfill(json({ key: 'theme', value: 'blue' }));
    if (req.method() === 'DELETE') return route.fulfill(json({ deleted: true }));
  }
  if (p.endsWith('/storage/sqlite/tasks/query')) return route.fulfill(json({ columns: ['id'], rows: [[7]] }));
  if (p.endsWith('/storage/sqlite/tasks/execute')) return route.fulfill(json({ changes: 1, last_insert_id: 7 }));
  if (p === '/v1/sites/demo/storage/files/gallery/objects') return route.fulfill(json({ items: [{ path: 'cover.webp', bytes: 3, content_type: 'image/webp' }], next_after: '' }));
  if (p.endsWith('/storage/files/gallery/objects/2026/cover.webp')) {
    if (req.method() === 'GET') return route.fulfill({ status: 200, contentType: 'application/octet-stream', body: Buffer.from([1, 2, 3]) });
    if (req.method() === 'PUT') return route.fulfill(json({ path: '2026/cover.webp', bytes: 3 }));
    if (req.method() === 'DELETE') return route.fulfill(json({ deleted: true }));
  }
  if (p.endsWith('/data/legacy/kind')) return route.fulfill(json({ kind: 'content', label: 'Page info' }));
  if (p.endsWith('/data/legacy')) return route.fulfill(json({ kind: 'content', data: { still: 'works' } }));
  return route.fulfill(json({ error: 'unexpected request', code: 'unexpected' }, 500));
}

(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROMIUM || '/usr/local/bin/chromium', headless: true, args: ['--no-sandbox'] });
  const context = await browser.newContext();
  await context.addCookies([{ name: '__Host-sh_vsess', value: 'fixture-session', url: 'https://' + host + '/', secure: true, sameSite: 'Lax' }]);
  await context.route('**/*', answer);
  const page = await context.newPage();
  await page.goto('https://' + host + '/', { waitUntil: 'networkidle' });
  const result = await page.evaluate(async () => {
    await SH.ready;
    const kv = SH.storage.kv('settings'), sql = SH.storage.sqlite('tasks'), files = SH.storage.files('gallery');
    let signinEvents = 0;
    window.addEventListener('sh:signin-required', () => { signinEvents++; });
    const out = {
      keys: await kv.keys('theme/'),
      nextKeys: await kv.keys('theme/', { after: 'theme/a', limit: 25 }),
      key: await kv.get('theme'),
      set: await kv.set('theme', 'blue'),
      removed: await kv.delete('theme'),
      rows: await sql.query('SELECT id FROM tasks WHERE id = ?', [7]),
      write: await sql.execute('INSERT INTO tasks (id) VALUES (?)', [7]),
      files: await files.list('2026/'),
      nextFiles: await files.list('2026/', { after: '2026/a.webp', limit: 25 }),
      upload: await files.put('2026/cover.webp', new Blob([new Uint8Array([1, 2, 3])], { type: 'image/webp' })),
      raw: await files.get('2026/cover.webp'),
      directURL: await files.url('2026/cover.webp'),
      fileDeleted: await files.delete('2026/cover.webp'),
      legacy: await SH.data('legacy', 'content').get()
    };
    out.raw = { bytes: Array.from(new Uint8Array(await out.raw.blob.arrayBuffer())), type: out.raw.contentType };
    try { await kv.get('blocked'); } catch (e) { out.blocked = { status: e.status, code: e.code }; }
    try { await kv.get('locked'); } catch (e) { out.locked = { status: e.status, code: e.code }; }
    try { files.url('../escape'); } catch (e) { out.invalidPath = e.name; }
    out.signinEvents = signinEvents;
    return out;
  });
  assert.deepEqual(result.keys.items, [{ key: 'theme', value: 'dark' }]);
  assert.deepEqual(result.nextKeys.items, result.keys.items);
  assert.deepEqual(result.key, { key: 'theme', value: 'dark' });
  assert.deepEqual(result.rows, { columns: ['id'], rows: [[7]] });
  assert.equal(result.write.changes, 1);
  assert.deepEqual(result.raw.bytes, [1, 2, 3]);
  assert.deepEqual(result.files.items, [{ path: 'cover.webp', bytes: 3, content_type: 'image/webp' }]);
  assert.deepEqual(result.nextFiles.items, result.files.items);
  assert.equal(result.raw.type, 'application/octet-stream');
  assert.equal(result.directURL, 'https://' + host + '/v1/sites/demo/storage/files/gallery/objects/2026/cover.webp');
  assert.deepEqual(result.legacy, { still: 'works' });
  assert.deepEqual(result.blocked, { status: 401, code: 'sign_in_required' });
  assert.deepEqual(result.locked, { status: 403, code: 'site_locked' });
  assert.equal(result.signinEvents, 1);
  assert.equal(result.invalidPath, 'TypeError');
  for (const req of seen) {
    assert.equal(req.host, host, 'storage and legacy calls stay on site origin');
    assert.equal(req.headers['x-api-key'], undefined, 'a hosted page never holds an API key');
    assert.match(req.headers.cookie || '', /__Host-sh_vsess=fixture-session/, 'site-scoped cookie reaches site APIs');
    if (['PUT', 'POST', 'DELETE'].includes(req.method)) assert.equal(req.headers['x-sh-csrf'], '1');
  }
  assert.equal(seen.find(r => r.path.endsWith('/storage/kv/settings/keys/theme') && r.method === 'PUT').body.toString(), '{"value":"blue"}');
  assert.equal(seen.find(r => r.path.endsWith('/storage/sqlite/tasks/query')).body.toString(), '{"sql":"SELECT id FROM tasks WHERE id = ?","params":[7]}');
  assert.equal(seen.find(r => r.path.endsWith('/storage/sqlite/tasks/execute')).body.toString(), '{"sql":"INSERT INTO tasks (id) VALUES (?)","params":[7]}');
  assert.ok(seen.some(r => r.path.endsWith('/storage/kv/settings/keys?prefix=theme%2F')));
  assert.ok(seen.some(r => r.path.endsWith('/storage/kv/settings/keys?prefix=theme%2F&after=theme%2Fa&limit=25')));
  assert.ok(seen.some(r => r.path.endsWith('/storage/files/gallery/objects?prefix=2026%2F')));
  assert.ok(seen.some(r => r.path.endsWith('/storage/files/gallery/objects?prefix=2026%2F&after=2026%2Fa.webp&limit=25')));
  assert.equal(seen.find(r => r.path.endsWith('/storage/files/gallery/objects/2026/cover.webp') && r.method === 'PUT').body.toString('hex'), '010203');
  assert.match(seen.find(r => r.path.endsWith('/storage/files/gallery/objects/2026/cover.webp') && r.method === 'PUT').headers['content-type'], /^image\/webp/);
  const other = await context.newPage();
  await other.goto('https://other.owner.simple-host.app/', { waitUntil: 'networkidle' });
  await other.evaluate(() => SH.storage.kv('settings').get('theme'));
  const last = seen.at(-1);
  assert.equal(last.host, 'other.owner.simple-host.app');
  assert.equal(last.headers.cookie, undefined, 'visitor cookie does not cross to a sibling site');
  const anonymousContext = await browser.newContext();
  await anonymousContext.route('**/*', answer);
  const anonymous = await anonymousContext.newPage();
  await anonymous.goto('https://' + host + '/', { waitUntil: 'networkidle' });
  await anonymous.evaluate(() => SH.storage.kv('settings').set('theme', 'blue'));
  const anonymousWrite = seen.at(-1);
  assert.equal(anonymousWrite.method, 'PUT');
  assert.equal(anonymousWrite.headers.cookie, undefined, 'an anyone-policy write can be sent with no session');
  assert.equal(anonymousWrite.headers['x-sh-csrf'], '1');
  await anonymousContext.close();
  const fallbackContext = await browser.newContext();
  await fallbackContext.route('**/*', answer);
  const fallback = await fallbackContext.newPage();
  await fallback.goto('https://owner.simple-host.app/demo/', { waitUntil: 'networkidle' });
  const fallbackResult = await fallback.evaluate(async () => {
    const [key, url] = await Promise.all([
      SH.storage.kv('settings').get('theme'),
      SH.storage.files('gallery').url('2026/cover.webp')
    ]);
    return { key, url };
  });
  assert.deepEqual(fallbackResult.key, { key: 'theme', value: 'dark' });
  assert.equal(fallbackResult.url, 'https://owner.simple-host.app/v1/sites/demo/storage/files/gallery/objects/2026/cover.webp');
  await fallbackContext.close();
  await browser.close();
  console.log('storage helper browser contract passed: KV, SQLite, binary files, legacy data, errors, and cookie scope');
})().catch(e => { console.error(e); process.exit(1); });
