#!/usr/bin/env node
// Browser proof for storage access presets: one page per preset family, each
// published exactly as get_page_recipe returns it, its owner setup run through
// the connector, then driven by real visitors (and by the owner signed in on the
// site) in a real browser.
//
// Runs against the fixture TestServeVisitorRecordsBrowser writes
// (internal/handler/visitor_records_browser_test.go):
//
//   VISITOR_RECORDS_FIXTURE=/tmp/f.json DB_DSN=... go test ./internal/handler/ -run TestServeVisitorRecordsBrowser
//   node scripts/e2e-storage-presets.mjs /tmp/f.json [--browser chromium|webkit] [--keep] [--shots dir]
//
// Needs Playwright (PLAYWRIGHT_DIR) and a Chromium at $CHROMIUM (or Playwright's
// own). Every browser request is forwarded to the fixture with the Host it was
// addressed to, so auth.js from https://simple-host.app is the fixture's too.
import fs from 'node:fs';
import http from 'node:http';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createRequire } from 'node:module';
const require = createRequire(process.env.PLAYWRIGHT_DIR ? process.env.PLAYWRIGHT_DIR + '/' : import.meta.url);
const { chromium, webkit } = require('playwright');

const args = process.argv.slice(2);
const fixtureFile = args.find(a => !a.startsWith('--'));
const opt = (name, def) => { const i = args.indexOf('--' + name); return i >= 0 ? args[i + 1] : def; };
const browserName = opt('browser', 'chromium');
const shots = opt('shots', '');
const keep = args.includes('--keep');
const info = JSON.parse(fs.readFileSync(fixtureFile, 'utf8'));
const stamp = Date.now().toString(36);
// Fresh people per site: the send-code limit counts per address.
// Live: visitors are support+<tag>@simple-host.app, read through the box's mail CLI.
const live = !!info.live;
let ann = '', bob = '';
const people = topic => {
  ann = live ? `support+ann${topic}${stamp}@simple-host.app` : `ann${topic}${stamp}@example.com`;
  bob = live ? `support+bob${topic}${stamp}@simple-host.app` : `bob${topic}${stamp}@example.com`;
};
const failedRequests = new Set();
const report = { browser: browserName, steps: [] };
const consoleLog = [];
const step = (name, detail) => { report.steps.push({ name, ...(detail ? { detail } : {}) }); console.log('ok', name, detail ? JSON.stringify(detail) : ''); };
const originOf = site => `https://${site}.${info.handle}.${info.site_domain}`;
const prefix = browserName === 'webkit' ? 'w' : 'c';
const only = opt('only', '');
const want = name => !only || only.split(',').includes(name);

let rpcID = 0;
async function rpc(method, params) {
  const r = await fetch(info.url + '/mcp', {
    method: 'POST', headers: { 'Content-Type': 'application/json', 'X-API-Key': info.owner_key },
    body: JSON.stringify({ jsonrpc: '2.0', id: ++rpcID, method, params: params || {} }),
  });
  const body = await r.json();
  assert(body.result, `MCP ${method} failed: ${JSON.stringify(body.error || body)}`);
  return body.result;
}
async function tool(name, a) {
  const res = await rpc('tools/call', { name, arguments: a });
  assert(!res.isError, `tool ${name} failed: ${res.content?.[0]?.text}`);
  return res;
}
async function mailCode(email) {
  if (live) {
    for (let i = 0; i < 30; i++) {
      const out = JSON.parse(execFileSync('sudo', ['-n', '/usr/local/bin/agent-mail', 'search', 'support', email], { encoding: 'utf8' }));
      const m = (out.messages || []).filter(x => x.to === email && /(\d{6})/.test(x.subject)).pop();
      if (m) return m.subject.match(/(\d{6})/)[1];
      await new Promise(res => setTimeout(res, 5000));
    }
    throw new Error('no code mailed to ' + email);
  }
  for (let i = 0; i < 40; i++) {
    const r = await (await fetch(info.url + '/_fixture/mail?email=' + encodeURIComponent(email))).json();
    if (r.code) return r.code;
    await new Promise(res => setTimeout(res, 250));
  }
  throw new Error('no code mailed to ' + email);
}
async function clearCode(email) {
  if (live) return '';
  // The sink keeps the last code per address; wait for a new one after each send.
  const r = await (await fetch(info.url + '/_fixture/mail?email=' + encodeURIComponent(email))).json();
  return r.code || '';
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
    await route.fulfill(response);
  } catch (e) {
    consoleLog.push('forward failed ' + req.method() + ' ' + u.href + ': ' + e.message);
    await route.abort().catch(() => {});
  }
}
const errors = [];
async function newPage(browser) {
  const context = await browser.newContext({ ignoreHTTPSErrors: !live, viewport: { width: 1000, height: 900 } });
  if (!live) await context.route('**/*', route => forward(route, context));
  const page = await context.newPage();
  page.on('pageerror', e => errors.push(e.message));
  page.on('console', m => consoleLog.push(m.type() + ': ' + m.text().slice(0, 200)));
  page.on('dialog', d => d.dismiss());
  page.on('requestfailed', r => failedRequests.add(new URL(r.url()).host));
  return page;
}
async function shot(page, name) {
  if (shots) await page.screenshot({ path: `${shots}/presets-${browserName}-${name}.png`, fullPage: true });
}

// Sign in through the mounted box: email, Send code, the mailed code, Verify.
async function signIn(page, email) {
  const before = await clearCode(email);
  const box = page.locator('#sh-auth');
  const field = box.locator('input[type=email]');
  await field.waitFor();
  await page.waitForTimeout(300);
  await field.fill(email);
  await box.getByRole('button', { name: 'Send code', exact: true }).click();
  try {
    await box.getByRole('button', { name: 'Verify', exact: true }).waitFor({ timeout: 15000 });
  } catch (e) {
    throw new Error('no Verify step for ' + email + ' on ' + page.url() + '; the box says: ' + JSON.stringify(await box.textContent()) + '; console: ' + consoleLog.slice(-4).join(' | '));
  }
  let code = '';
  for (let i = 0; i < 40 && (!code || code === before); i++) { code = await mailCode(email); if (code === before) await page.waitForTimeout(250); }
  await box.locator('input[placeholder="6-digit code"]').fill(code);
  await box.getByRole('button', { name: 'Verify', exact: true }).click();
  try {
    await box.getByText('Signed in as', { exact: false }).waitFor({ timeout: 20000 });
  } catch (e) {
    throw new Error('not signed in as ' + email + ' on ' + page.url() + ' with code ' + code + ' (previous ' + before + '); the box says: ' + JSON.stringify(await box.textContent()) + '; console: ' + consoleLog.slice(-4).join(' | '));
  }
}

// Publish a recipe's page verbatim on its own site and run its section 1 owner setup.
async function setUp(topic, site, file = 'index.html', extra = {}) {
  const recipe = (await tool('get_page_recipe', { topic, site })).structuredContent.recipe;
  const setupText = recipe.slice(recipe.indexOf('## 1.'), recipe.indexOf('## 2.'));
  const calls = [...setupText.matchAll(/^(storage_\w+) (\{.*\})$/gm)].map(m => ({ name: m[1], args: JSON.parse(m[2]) }));
  const html = recipe.slice(recipe.indexOf('<!doctype html>'), recipe.indexOf('</html>') + '</html>'.length);
  assert(html.includes('https://simple-host.app/auth.js') && html.includes(`window.SH_CONFIG = { site: "${site}" };`), topic + ': page shape');
  assert(!/innerHTML/.test(html), topic + ': page uses innerHTML');
  const files = { ...extra, [file]: html };
  let res = await rpc('tools/call', { name: 'create_site', arguments: { site, files } });
  if (res.isError) res = await tool('update_site', { site, files });
  for (const c of calls) await tool(c.name, c.args);
  if (live) {
    // A new account's site host waits for its certificate.
    for (let i = 0; i < 60; i++) {
      const ok = await fetch(originOf(site) + '/', { redirect: 'manual' }).then(r => r.status === 200).catch(() => false);
      if (ok) break;
      await new Promise(res => setTimeout(res, 10000));
    }
  }
  step(`${topic}: published the recipe page and ran its setup`, { site, setup: calls.map(c => c.name + (c.args.body?.preset ? ':' + c.args.body.preset : '')) });
  return { recipe, html, calls };
}
async function pageFetch(page, path, init) {
  return page.evaluate(async ([p, i]) => { const r = await fetch(p, i); let b = null; try { b = await r.json(); } catch (e) {} return { status: r.status, body: b }; }, [path, init || {}]);
}

async function run() {
  const browser = browserName === 'webkit' ? await webkit.launch() : await chromium.launch({ executablePath: process.env.CHROMIUM || undefined });
  try {
    // ---------- public: a menu everyone reads, only the owner writes ----------
    if (want('public')) {
      people('public');
      const site = prefix + 'menu' + stamp;
      await setUp('public', site);
      const p = await newPage(browser);
      await p.goto(originOf(site) + '/');
      await p.locator('#menu li', { hasText: 'Masala dosa' }).waitFor();
      const w = await pageFetch(p, `/v1/sites/${site}/storage/kv/menu/keys/today`, { method: 'PUT', headers: { 'Content-Type': 'application/json', 'X-SH-CSRF': '1' }, body: JSON.stringify({ value: { dishes: [] } }) });
      assert.equal(w.status, 403, 'anonymous write to public');
      await shot(p, 'public');
      step('public: anyone reads the menu; a visitor write is refused', { write: w.status });
      await p.context().close();
    }
    // ---------- inbox: people send, only the owner reads ----------
    if (want('inbox')) {
      people('inbox');
      const site = prefix + 'contact' + stamp;
      await setUp('inbox', site);
      const p = await newPage(browser);
      await p.goto(originOf(site) + '/');
      await p.locator('#contact [name=name]').fill('Ann');
      await p.locator('#contact [name=email]').fill(ann);
      await p.locator('#contact [name=message]').fill('Hello from ann ' + stamp);
      await p.locator('#contact button[type=submit]').click();
      await signIn(p, ann);
      await p.locator('#status', { hasText: 'sent' }).waitFor({ timeout: 20000 });
      const r = await pageFetch(p, `/v1/sites/${site}/storage/sqlite/messages/tables/messages/rows`);
      assert.equal(r.status, 403, 'sender read back the inbox');
      const q = (await tool('storage_sql_query', { site, name: 'messages', sql: 'SELECT message, visitor_id FROM messages', params: [] })).structuredContent.response;
      assert(q.rows.some(row => String(row[0]).includes(stamp) && row[1]), 'owner sees the message with its sender');
      await shot(p, 'inbox');
      step('inbox: a signed-in visitor sends; they cannot read it back; the owner reads it with the sender', { readBack: r.status });
      await p.context().close();
    }
    // ---------- wall: guestbook, authors delete their own ----------
    if (want('wall')) {
      people('wall');
      const site = prefix + 'guests' + stamp;
      await setUp('wall', site);
      const a = await newPage(browser), b = await newPage(browser), anon = await newPage(browser);
      await a.goto(originOf(site) + '/');
      await a.locator('#post [name=name]').fill('Ann');
      await a.locator('#post [name=message]').fill('Congratulations ' + stamp);
      await a.locator('#post button[type=submit]').click();
      await signIn(a, ann);
      await a.locator('#status', { hasText: 'Thanks' }).waitFor({ timeout: 20000 });
      await a.locator('.entry', { hasText: 'Congratulations' }).getByRole('button', { name: 'Delete my post' }).waitFor();
      await anon.goto(originOf(site) + '/');
      await anon.locator('.entry', { hasText: 'Congratulations' }).waitFor();
      assert.equal(await anon.getByRole('button', { name: 'Delete my post' }).count(), 0);
      await b.goto(originOf(site) + '/');
      await signIn(b, bob);
      await b.locator('.entry', { hasText: 'Congratulations' }).waitFor();
      assert.equal(await b.getByRole('button', { name: 'Delete my post' }).count(), 0, 'bob sees a delete button on ann\'s post');
      const id = (await pageFetch(b, `/v1/sites/${site}/storage/sqlite/guestbook/tables/entries/rows`)).body.rows[0][0];
      const del = await pageFetch(b, `/v1/sites/${site}/storage/sqlite/guestbook/tables/entries/rows/${id}`, { method: 'DELETE', headers: { 'X-SH-CSRF': '1' } });
      assert.equal(del.status, 404, 'bob deleted ann\'s post');
      const leak = await pageFetch(anon, `/v1/sites/${site}/storage/sqlite/guestbook/tables/entries/rows`);
      assert(!JSON.stringify(leak.body).match(/[0-9a-f]{8}-[0-9a-f]{4}-/), 'author ids reached a stranger');
      await shot(a, 'wall-author');
      await a.locator('.entry', { hasText: 'Congratulations' }).getByRole('button', { name: 'Delete my post' }).click();
      await a.locator('#entries', { hasText: 'Nobody has signed yet' }).waitFor();
      step('wall: anyone reads; signed-in people post; only the author sees and uses Delete; others get 404', { bobDelete: del.status });
      for (const p of [a, b, anon]) await p.context().close();
    }
    // ---------- records + admin: each customer sees their own; the owner manages all on the site ----------
    if (want('records')) {
      people('records');
      const site = prefix + 'shop' + stamp;
      const { html: shopHTML } = await setUp('records', site);
      await setUp('admin', site, 'admin.html', { 'index.html': shopHTML });
      const customers = [{ email: ann, product: 'Cumin' }, { email: bob, product: 'Turmeric' }];
      const pages = [];
      for (const c of customers) {
        const p = await newPage(browser);
        await p.goto(originOf(site) + '/');
        await p.locator('#products div', { hasText: c.product }).getByRole('button', { name: 'Add' }).click();
        await p.locator('#checkout').click();
        await signIn(p, c.email);
        await p.locator('#checkout-status', { hasText: 'placed' }).waitFor({ timeout: 20000 });
        await p.locator('#orders table').waitFor();
        pages.push(p);
      }
      for (let i = 0; i < 2; i++) {
        const text = await pages[i].locator('#orders').textContent();
        assert(text.includes(customers[i].product) && !text.includes(customers[1 - i].product), 'customer sees only their own order');
      }
      // A customer opening admin.html is told it is the owner's page, and sees nobody else's orders.
      await pages[1].goto(originOf(site) + '/admin.html');
      await pages[1].locator('#who', { hasText: "shop's owner" }).waitFor();
      const forged = await pageFetch(pages[1], `/v1/sites/${site}/storage/sqlite/orders/tables/orders/rows/1`, { method: 'PATCH', headers: { 'Content-Type': 'application/json', 'X-SH-CSRF': '1' }, body: JSON.stringify({ status: 'shipped' }) });
      assert.equal(forged.status, 403, 'a customer edited an order status');
      // The owner signs in on the site with their account email: every order, and a status to set.
      const o = await newPage(browser);
      await o.goto(originOf(site) + '/admin.html');
      await signIn(o, info.owner_email);
      await o.locator('#who', { hasText: '(owner)' }).waitFor({ timeout: 20000 });
      await o.locator('#orders table').waitFor();
      const rows = await o.locator('#orders table tr').count();
      assert(rows >= 3, 'owner sees both orders, got ' + (rows - 1));
      await shot(o, 'admin-before');
      const annRow = o.locator('#orders tr', { hasText: 'Cumin' });
      await annRow.locator('select').selectOption('shipped');
      await annRow.locator('span', { hasText: 'saved' }).waitFor();
      await o.locator('#filter').selectOption('shipped');
      await o.waitForFunction(() => document.querySelectorAll('#orders table tr').length === 2, null, { timeout: 10000 });
      await shot(o, 'admin-filtered');
      const q = (await tool('storage_sql_query', { site, name: 'orders', sql: "SELECT count(*) FROM orders WHERE status = 'shipped'", params: [] })).structuredContent.response;
      assert.equal(q.rows[0][0], 1, 'the status the owner set is saved');
      await pages[0].reload(); await pages[0].locator('#orders', { hasText: 'shipped' }).waitFor();
      // Owner rights are storage data only: the page cannot reach settings.
      const settings = await pageFetch(o, `/v1/sites/${site}/storage/resources`);
      const passcode = await pageFetch(o, `/v1/sites/${site}/lock`, { method: 'PUT', headers: { 'Content-Type': 'application/json', 'X-SH-CSRF': '1' }, body: JSON.stringify({ passcode: '123456' }) });
      const remove = await pageFetch(o, `/v1/sites/${site}`, { method: 'DELETE', headers: { 'X-SH-CSRF': '1' } });
      assert(remove.status >= 400, 'owner page deleted the site: ' + remove.status);
      assert(settings.status >= 400 && passcode.status >= 400, 'owner page reached settings: ' + settings.status + ' ' + passcode.status);
      // A sibling site frames the owner's admin page (clickjacking): the page
      // the owner loads while signed in may not be framed by another origin.
      const evil = prefix + 'evil' + stamp;
      await tool('create_site', { site: evil, files: { 'index.html': `<!doctype html><title>Win a prize</title><h1>Click to win</h1><iframe id="f" src="${originOf(site)}/admin.html" width="900" height="500"></iframe>` } });
      await o.goto(originOf(evil) + '/');
      await o.waitForTimeout(3000);
      let framedOwner = false;
      for (const f of o.frames()) {
        if (f === o.mainFrame()) continue;
        const t = await f.evaluate(() => document.body ? document.body.innerText : '').catch(() => '');
        if (/\(owner\)|Garam|Cumin/.test(t)) framedOwner = true;
      }
      assert(!framedOwner, 'the admin page rendered with owner data inside another site');
      await shot(o, 'framed-by-sibling');
      step('records + admin: a sibling site that frames the admin page gets nothing (frame-ancestors)');
      step('records + admin: customers see their own orders; a customer cannot set a status; the owner signed in on the site lists all, filters, and sets a status; settings stay closed', { customerEdit: forged.status, settings: settings.status, passcode: passcode.status, deleteSite: remove.status });
      for (const p of [...pages, o]) await p.context().close();
    }
    // ---------- personal: a wishlist only its owner sees ----------
    if (want('personal')) {
      people('personal');
      const site = prefix + 'wish' + stamp;
      await setUp('personal', site);
      const a = await newPage(browser), b = await newPage(browser);
      await a.goto(originOf(site) + '/');
      await signIn(a, ann);
      await a.locator('#add').waitFor({ state: 'visible' });
      for (const title of ['Kettle', 'Teapot']) {
        await a.locator('#add [name=title]').fill(title);
        await a.locator('#add button[type=submit]').click();
        await a.locator('#items li', { hasText: title }).waitFor();
      }
      await a.locator('#items li', { hasText: 'Kettle' }).locator('input[type=checkbox]').check();
      await a.locator('#items li span.got', { hasText: 'Kettle' }).waitFor();
      await a.locator('#items li', { hasText: 'Teapot' }).getByRole('button', { name: 'Remove' }).click();
      await a.waitForFunction(() => !document.querySelector('#items').textContent.includes('Teapot'));
      await shot(a, 'personal');
      await b.goto(originOf(site) + '/');
      await signIn(b, bob);
      await b.locator('#items', { hasText: 'Nothing yet' }).waitFor();
      const id = (await pageFetch(a, `/v1/sites/${site}/storage/sqlite/wishlist/tables/items/rows`)).body.rows[0][0];
      const edit = await pageFetch(b, `/v1/sites/${site}/storage/sqlite/wishlist/tables/items/rows/${id}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json', 'X-SH-CSRF': '1' }, body: JSON.stringify({ title: 'hacked' }) });
      assert.equal(edit.status, 404, 'bob edited ann\'s wishlist');
      step('personal: each person adds, ticks and removes their own; another person sees none and gets 404', { otherEdit: edit.status });
      for (const p of [a, b]) await p.context().close();
    }
    // ---------- board: everyone signed in edits; only the owner deletes ----------
    if (want('board')) {
      people('board');
      const site = prefix + 'potluck' + stamp;
      await setUp('board', site);
      const a = await newPage(browser), b = await newPage(browser), anon = await newPage(browser);
      await a.goto(originOf(site) + '/');
      await signIn(a, ann);
      await a.locator('#add').waitFor({ state: 'visible' });
      await a.locator('#add [name=dish]').fill('Biryani');
      await a.locator('#add [name=who]').fill('Ann');
      await a.locator('#add button[type=submit]').click();
      await a.locator('#dishes td', { hasText: 'Biryani' }).waitFor();
      assert.equal(await a.getByRole('button', { name: 'Remove' }).count(), 0);
      await b.goto(originOf(site) + '/');
      await signIn(b, bob);
      const who = b.locator('#dishes tr', { hasText: 'Biryani' }).locator('input');
      await who.fill('Bob'); await who.press('Tab');
      await b.waitForTimeout(500);
      await a.reload(); await a.locator('#dishes td', { hasText: 'Biryani' }).waitFor();
      assert.equal(await a.locator('#dishes tr', { hasText: 'Biryani' }).locator('input').inputValue(), 'Bob', 'bob\'s swap saved');
      await anon.goto(originOf(site) + '/');
      await anon.locator('#dishes caption', { hasText: 'Sign in' }).waitFor();
      const o = await newPage(browser);
      await o.goto(originOf(site) + '/');
      await signIn(o, info.owner_email);
      await o.getByRole('button', { name: 'Remove' }).waitFor();
      await shot(o, 'board-owner');
      await o.getByRole('button', { name: 'Remove' }).click();
      await o.locator('#dishes caption', { hasText: 'Nothing on the list' }).waitFor();
      step('board: signed-in people add and swap; strangers see nothing; only the owner on the site removes');
      for (const p of [a, b, anon, o]) await p.context().close();
    }
    // ---------- private: only the owner, on the site ----------
    if (want('private')) {
      people('private');
      const site = prefix + 'notes' + stamp;
      await setUp('private', site, 'notes.html', { 'index.html': '<!doctype html><title>Notes</title><a href="notes.html">Notes</a>' });
      const a = await newPage(browser);
      await a.goto(originOf(site) + '/notes.html');
      await signIn(a, ann);
      await a.locator('#who', { hasText: "site's owner" }).waitFor();
      assert(await a.locator('#editor').isHidden());
      const read = await pageFetch(a, `/v1/sites/${site}/storage/kv/notes/keys/main`);
      assert.equal(read.status, 403, 'a visitor read private notes');
      const o = await newPage(browser);
      await o.goto(originOf(site) + '/notes.html');
      await signIn(o, info.owner_email);
      await o.locator('#editor').waitFor({ state: 'visible' });
      await o.locator('#text').fill('Reorder cardamom ' + stamp);
      await o.locator('#save').click();
      await o.locator('#saved', { hasText: /^saved$/ }).waitFor();
      const kv = (await tool('storage_get_kv', { site, name: 'notes', key: 'main' })).structuredContent.response;
      assert.equal(kv.value, 'Reorder cardamom ' + stamp);
      await shot(o, 'private-owner');
      step('private: a visitor gets 403; the owner signed in on the site saves a note the tools read back', { visitorRead: read.status });
      for (const p of [a, o]) await p.context().close();
    }
    assert.deepEqual(errors, [], 'page errors: ' + errors.join(' | '));
    report.failedRequestHosts = [...failedRequests];
    report.ok = true;
  } finally {
    await browser.close();
  }
}

try {
  await run();
} catch (e) {
  report.ok = false; report.error = e.message; report.console = consoleLog.slice(-15);
  console.error('FAILED', e.message, '\n', consoleLog.slice(-15).join('\n'));
  process.exitCode = 1;
} finally {
  if (opt('report', '')) fs.writeFileSync(opt('report', ''), JSON.stringify(report, null, 2));
  if (!keep && !live) await fetch(info.url + '/_fixture/stop', { method: 'POST' }).catch(() => {});
}
