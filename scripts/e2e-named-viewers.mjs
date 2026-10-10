#!/usr/bin/env node
// Browser proof for named viewers (who can open a site), on the fixture
// TestServeVisitorRecordsBrowser writes (internal/handler/visitor_records_browser_test.go):
//
//   node scripts/e2e-named-viewers.mjs <fixture.json> publish
//       publishes the site "family" (a page with a canary) as the fixture's owner.
//   node scripts/e2e-named-viewers.mjs <fixture.json> check [--browser chromium|webkit] [--shots dir] [--keep]
//       checks GET /v1/sites/family/access names exactly mom@example.com and
//       dad@example.com, then in a real browser: a signed-out visitor gets the
//       sign-in page; mom signs in with an emailed code and sees the site; an
//       unlisted person signs in and gets "This site is private", whose Switch
//       account goes back to the sign-in page. --keep leaves the fixture running.
//
// Turning it on is left to the caller (a connector-only agent in the job that
// ships this; `--grant` does it through the connector for a quick run).
// Needs Playwright (PLAYWRIGHT_DIR) and, for Chromium, $CHROMIUM. Requests are
// forwarded to the fixture with the Host they were addressed to, as in
// e2e-visitor-records.mjs, so no DNS is needed and WebKit works the same; a
// forwarded redirect reaches the browser as an immediate refresh.
import fs from 'node:fs';
import http from 'node:http';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
const require = createRequire(process.env.PLAYWRIGHT_DIR ? process.env.PLAYWRIGHT_DIR + '/' : import.meta.url);
const { chromium, webkit } = require('playwright');

const args = process.argv.slice(2);
const [fixtureFile, phase = 'check'] = args.filter(a => !a.startsWith('--'));
const opt = (name, def) => { const i = args.indexOf('--' + name); return i >= 0 ? args[i + 1] : def; };
const browserName = opt('browser', 'chromium');
const shots = opt('shots', '');
const info = JSON.parse(fs.readFileSync(fixtureFile, 'utf8'));
const site = 'family';
const origin = `https://${site}.${info.handle}.${info.site_domain}`;
const CANARY = 'FAMILY-PHOTOS-7d1e';
const stamp = Date.now().toString(36);
const outsider = `cousin-${stamp}@example.com`;
const ok = (name, detail) => console.log('ok', browserName, name, detail ? JSON.stringify(detail) : '');

async function api(method, path, body) {
  const r = await fetch(info.url + path, {
    method, headers: { 'X-API-Key': info.owner_key, 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : undefined,
  });
  return { status: r.status, body: await r.json().catch(() => null) };
}
async function rpc(name, args) {
  const r = await fetch(info.url + '/mcp', {
    method: 'POST', headers: { 'Content-Type': 'application/json', 'X-API-Key': info.owner_key },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name, arguments: args } }),
  });
  return (await r.json()).result;
}
async function code(email) {
  for (let i = 0; i < 50; i++) {
    const r = await fetch(info.url + '/_fixture/mail?email=' + encodeURIComponent(email));
    const c = (await r.json()).code;
    if (c) return c;
    await new Promise(res => setTimeout(res, 200));
  }
  throw new Error('no code mailed to ' + email);
}

const localAgent = new http.Agent({ keepAlive: false, maxSockets: 32 });
async function forward(route, context) {
  const req = route.request(), u = new URL(req.url());
  const headers = { ...req.headers(), host: u.host, 'x-forwarded-proto': 'https' };
  delete headers['content-length'];
  if (!headers.cookie) {
    const jar = await context.cookies(u.href);
    if (jar.length) headers.cookie = jar.map(c => c.name + '=' + c.value).join('; ');
  }
  const body = req.postDataBuffer();
  try {
    const response = await new Promise((resolve, reject) => {
      const request = http.request(info.url + u.pathname + u.search, { method: req.method(), headers, agent: localAgent, timeout: 20000 }, res => {
        const chunks = []; res.on('data', c => chunks.push(c)); res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body: Buffer.concat(chunks) }));
      });
      request.on('timeout', () => request.destroy(new Error('timeout')));
      request.on('error', reject); request.end(body);
    });
    delete response.headers['content-length'];
    // Playwright does not follow a redirect it was handed through
    // route.fulfill for a form navigation, so a forwarded 3xx becomes an
    // immediate refresh to the same Location, cookies kept (harness only; the
    // app's answer is unchanged).
    if (response.status >= 300 && response.status < 400 && response.headers.location && req.isNavigationRequest()) {
      const loc = new URL(response.headers.location, u).href;
      response.headers['content-type'] = 'text/html';
      delete response.headers.location;
      response.body = Buffer.from(`<!doctype html><meta http-equiv="refresh" content="0;url=${loc}">`);
      response.status = 200;
    }
    if (process.env.E2E_DEBUG) console.log('fwd', req.method(), u.pathname, response.status, response.headers.location || '');
    await route.fulfill(response);
  } catch (e) {
    if (process.env.E2E_DEBUG) console.log('fwd failed', u.pathname, e.message);
    await route.abort().catch(() => {});
  }
}
async function newPage(browser) {
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  await context.route('**/*', route => forward(route, context));
  return context.newPage();
}
async function shot(page, name) {
  if (shots) await page.screenshot({ path: `${shots}/viewers-${browserName}-${name}.png`, fullPage: true });
}
async function signIn(page, email) {
  await page.fill('input[name=email]', email);
  await Promise.all([page.waitForLoadState('load'), page.click('button[type=submit]')]);
  assert.match(await page.content(), /We sent a sign-in code/);
  await page.fill('input[name=code]', await code(email));
  await page.click('form:has(input[name=code]) button[type=submit]');
  await page.waitForURL(u => !String(u).includes('/v1/'), { timeout: 15000 });
  await page.waitForLoadState('load');
}

if (phase === 'publish') {
  const r = await api('PUT', `/v1/sites/${site}/files?create=1`, { files: { 'index.html': `<!doctype html><title>Our family</title><h1>Our family</h1><p>${CANARY}</p><img src="photo.svg" alt="">`, 'photo.svg': `<svg xmlns="http://www.w3.org/2000/svg" width="80" height="40"><text y="20">${CANARY}</text></svg>` } });
  assert.ok(r.status === 200 || r.status === 201, 'publish ' + r.status);
  ok('published', { origin });
  if (args.includes('--grant')) {
    const g = await rpc('grant_site_viewer', { site, emails: ['mom@example.com', 'dad@example.com'] });
    assert.ok(!g.isError, JSON.stringify(g));
    ok('granted through the connector');
  }
  process.exit(0);
}

if (phase === 'dashboard') {
  // The owner app's "Who can open" dialog: read, add, remove, open to anyone.
  const browser = browserName === 'webkit' ? await webkit.launch() : await chromium.launch({ executablePath: process.env.CHROMIUM || undefined });
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  await ctx.addInitScript(k => { try { localStorage.setItem('apiKey', k); } catch (e) {} }, info.owner_key);
  const page = await ctx.newPage();
  // The owner page's CSP forbids eval, so waits poll from here.
  const until = async (fn, what) => {
    for (let i = 0; i < 100; i++) { if (await fn()) return; await new Promise(r => setTimeout(r, 100)); }
    throw new Error('timed out: ' + what);
  };
  const rows = () => page.locator('#wv-list li').count();
  const countText = () => page.locator('#wv-count').innerText();
  const natives = [];
  page.on('dialog', d => { natives.push(d.message()); d.dismiss().catch(() => {}); });
  await page.goto(info.url + '/' + info.handle, { waitUntil: 'load' });
  const btn = page.locator(`[data-act=openViewers][data-name=${site}]`);
  await btn.waitFor({ timeout: 15000 });
  await btn.click();
  await page.locator('#wv-list li').first().waitFor({ timeout: 10000 });
  assert.ok(await page.locator('#wv-specific').isChecked(), 'specific checked');
  assert.equal(await page.locator('#wv-list li').count(), 2);
  await shot(page, 'dashboard-1-open');
  await page.fill('#wv-new', 'aunt@example.com, Uncle@Example.com');
  await page.click('#wv-add-btn');
  await until(async () => (await rows()) === 4, '4 viewers');
  assert.match(await page.locator('#wv-list').innerText(), /uncle@example\.com/);
  await page.click('#wv-list li:has-text("aunt@example.com") button');
  await until(async () => (await rows()) === 3, '3 viewers');
  await page.click('#wv-list li:has-text("uncle@example.com") button');
  await until(async () => (await rows()) === 2, '2 viewers');
  await shot(page, 'dashboard-2-after-edits');
  await page.check('#wv-anyone');
  await until(async () => /kept for when you turn this on/.test(await countText()), 'anyone');
  let a = await api('GET', `/v1/sites/${site}/access`);
  assert.equal(a.body.access, 'anyone');
  await page.check('#wv-specific');
  await until(async () => /named viewers can open it/.test(await countText()), 'specific');
  a = await api('GET', `/v1/sites/${site}/access`);
  assert.equal(a.body.access, 'specific');
  await page.click('#wv-close');
  await page.locator('.vis-badge:has-text("named viewers")').first().waitFor({ timeout: 10000 });
  assert.equal(natives.length, 0, 'native dialogs: ' + natives.join(' | '));
  ok('dashboard dialog reads, adds, removes, and switches who can open the site');
  await browser.close();
  if (!args.includes('--keep')) await fetch(info.url + '/_fixture/stop', { method: 'POST' }).catch(() => {});
  process.exit(0);
}

// The connector agent's work, as the owner sees it.
const access = await api('GET', `/v1/sites/${site}/access`);
assert.equal(access.status, 200);
assert.equal(access.body.access, 'specific', 'access is specific: ' + JSON.stringify(access.body));
assert.deepEqual(access.body.viewers.map(v => v.email).sort(), ['dad@example.com', 'mom@example.com']);
ok('connector turned it on', { access: access.body.access, viewers: access.body.viewers.map(v => v.email) });

const browser = browserName === 'webkit' ? await webkit.launch() : await chromium.launch({ executablePath: process.env.CHROMIUM || undefined });
try {
  // Signed out: the sign-in page, nothing of the site.
  let page = await newPage(browser);
  let res = await page.goto(origin + '/');
  assert.equal(res.status(), 401);
  let html = await page.content();
  assert.match(html, /This site is private/);
  assert.match(html, /Email me a code/);
  assert.ok(!html.includes(CANARY), 'signed-out page leaked the site');
  res = await page.goto(origin + '/photo.svg');
  assert.ok(!(await page.content()).includes(CANARY), 'signed-out photo leaked');
  await page.goto(origin + '/');
  await shot(page, '1-signed-out');
  ok('signed-out visitor gets the sign-in page');

  // mom signs in with an emailed code and sees the site.
  await signIn(page, 'mom@example.com');
  html = await page.content();
  assert.ok(html.includes(CANARY), 'mom does not see the site: ' + html.slice(0, 300));
  await shot(page, '2-mom-signed-in');
  res = await page.goto(origin + '/photo.svg');
  assert.equal(res.status(), 200);
  ok('listed person signs in and sees the site');
  await page.context().close();

  // Someone not on the list: the private page, then Switch account.
  page = await newPage(browser);
  await page.goto(origin + '/');
  await signIn(page, outsider);
  html = await page.content();
  assert.match(html, /This site is private/);
  assert.ok(html.includes(outsider), 'private page names the account');
  assert.ok(!html.includes(CANARY), 'unlisted person saw the site');
  await shot(page, '3-unlisted-private');
  await page.click('button[value=switch]');
  await page.waitForURL(u => !String(u).includes('/v1/'), { timeout: 15000 });
  await page.waitForLoadState('load');
  html = await page.content();
  assert.match(html, /Email me a code/);
  ok('unlisted person gets the private page; Switch account signs out');
  await page.context().close();
} finally {
  await browser.close();
}
if (!args.includes('--keep')) await fetch(info.url + '/_fixture/stop', { method: 'POST' }).catch(() => {});
