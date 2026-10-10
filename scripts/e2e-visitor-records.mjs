#!/usr/bin/env node
// Browser proof for "each person's records": visitor sign-in at checkout, add-only
// orders with own reads, and the owner's view through the connector.
//
// Runs against the fixture TestServeVisitorRecordsBrowser writes (internal/handler/
// visitor_records_browser_test.go). Two modes:
//
//   recipe (default): publish the page get_page_recipe returns, run its owner setup
//     through the connector, then drive two customers in a real browser.
//   site: check a site an agent built with only the connector (--site <name>):
//     its files use visitor sign-in and storage (never /v1/auth or the deprecated
//     saved data), then sign two customers in and check that each sees only their
//     own rows while the owner sees all.
//
//   node scripts/e2e-visitor-records.mjs <fixture.json> [--mode recipe|site] [--site name]
//        [--browser chromium|webkit] [--keep]   (--keep: do not stop the fixture)
//
// Against the live service: a fixture with {"url":"https://simple-host.app","owner_key":<a
// throwaway account's key>,"handle":...,"site_domain":"simple-host.app","live":true}. The
// browser then goes to the real addresses, and visitor codes are read from the support
// mailbox (customers are support+<tag>@simple-host.app) through the agent-mail CLI.
//
// Needs Playwright (PLAYWRIGHT_DIR, or a node_modules next to it) and a Chromium at
// $CHROMIUM (or Playwright's own). The browser sees the site at its real https
// address; every request is forwarded to the fixture server with the Host it was
// addressed to (so auth.js from https://simple-host.app is served by the fixture
// too, and the recipe page runs verbatim). Works the same in WebKit.
import fs from 'node:fs';
import http from 'node:http';
import { execFileSync } from 'node:child_process';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
const require = createRequire(process.env.PLAYWRIGHT_DIR ? process.env.PLAYWRIGHT_DIR + '/' : import.meta.url);
const { chromium, webkit } = require('playwright');

const args = process.argv.slice(2);
const fixtureFile = args.find(a => !a.startsWith('--'));
const opt = (name, def) => { const i = args.indexOf('--' + name); return i >= 0 ? args[i + 1] : def; };
const mode = opt('mode', 'recipe');
const browserName = opt('browser', 'chromium');
const keep = args.includes('--keep');
const info = JSON.parse(fs.readFileSync(fixtureFile, 'utf8'));
const site = opt('site', 'spice-shop');
let origin = `https://${site}.${info.handle}.${info.site_domain}`;
const stamp = Date.now().toString(36);
const live = !!info.live;
const ann = live ? `support+ann-${stamp}@simple-host.app` : `ann-${stamp}@example.com`;
const bob = live ? `support+bob-${stamp}@simple-host.app` : `bob-${stamp}@example.com`;
const report = { mode, browser: browserName, site, origin, steps: [] };
const consoleLog = [];
const step = (name, detail) => { report.steps.push({ name, ...(detail ? { detail } : {}) }); console.log('ok', name, detail ? JSON.stringify(detail) : ''); };

// ---- the connector, as an agent uses it (X-API-Key stands in for the OAuth token) ----
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
async function tool(name, arguments_) {
  const res = await rpc('tools/call', { name, arguments: arguments_ });
  assert(!res.isError, `tool ${name} failed: ${res.content?.[0]?.text}`);
  return res;
}
async function mailCode(email) {
  if (live) {
    // The support mailbox, through the box's mail CLI; the code is in the subject.
    for (let i = 0; i < 24; i++) {
      const out = JSON.parse(execFileSync('sudo', ['-n', '/usr/local/bin/agent-mail', 'search', 'support', email], { encoding: 'utf8' }));
      const m = (out.messages || []).find(x => x.to === email && /code: (\d{6})/.test(x.subject));
      if (m) return m.subject.match(/code: (\d{6})/)[1];
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

async function launch() {
  if (browserName === 'webkit') return webkit.launch();
  return chromium.launch({ executablePath: process.env.CHROMIUM || undefined });
}
// Forward one browser request to the fixture server, keeping the Host it was for.
const localAgent = new http.Agent({ keepAlive: false, maxSockets: 32 });
async function forward(route, context) {
  const req = route.request(), u = new URL(req.url());
  const headers = { ...req.headers(), host: u.host, 'x-forwarded-proto': 'https' };
  delete headers['content-length'];
  if (!headers.cookie) {
    // WebKit hands the handler the request before its cookies are attached.
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
async function newContext(browser) {
  if (live) return browser.newContext();
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  await context.route('**/*', route => forward(route, context));
  return context;
}
async function publish(files) {
  let res = await rpc('tools/call', { name: 'create_site', arguments: { site, files } });
  if (res.isError) {
    assert(/already|exists/i.test(res.content[0].text), 'create_site: ' + res.content[0].text);
    res = await tool('update_site', { site, files });
  }
  // Live, a brand-new account's site may briefly live at the person-path address
  // until its certificate is issued; the browser goes where the tool says.
  if (live && res.structuredContent && res.structuredContent.url) { origin = res.structuredContent.url.replace(/\/$/, ''); report.origin = origin; }
}

// Sign a customer in through the mounted box: email, Send code, the mailed code, Verify.
async function signInThroughBox(page, email) {
  const box = page.locator('#sh-auth');
  const emailField = box.locator('input[type=email]');
  await emailField.fill(email);
  // The box may still be settling (a save that needed sign-in just brought it up).
  await page.waitForTimeout(500);
  if ((await emailField.inputValue()) !== email) await emailField.fill(email);
  await box.getByRole('button', { name: 'Send code', exact: true }).click();
  try {
    await box.getByRole('button', { name: 'Verify', exact: true }).waitFor({ timeout: 15000 });
  } catch (e) {
    throw new Error('no Verify step after Send code; the box says: ' + JSON.stringify(await box.textContent()) + '; console: ' + consoleLog.slice(-5).join(' | '));
  }
  const code = await mailCode(email);
  await box.locator('input[placeholder="6-digit code"]').fill(code);
  await box.getByRole('button', { name: 'Verify', exact: true }).click();
  await box.getByText('Signed in as', { exact: false }).waitFor();
}

// What must hold in every browser context once a customer is signed in.
async function commonChecks(page, context, email) {
  const storage = await page.evaluate(() => JSON.stringify({ l: { ...localStorage }, s: { ...sessionStorage } }));
  assert(!/shk_|api_key/i.test(storage), 'an API key reached browser storage');
  const cookies = await context.cookies();
  assert(cookies.some(c => /sh_vsess$/.test(c.name) && c.httpOnly), 'no HttpOnly visitor cookie');
  const denied = await page.evaluate(async () => {
    const r = await fetch('/v1/auth', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ email: 'customer@example.com' }) });
    return { status: r.status, body: await r.json() };
  });
  assert.equal(denied.status, 403, 'account sign-in answered on the site host');
  assert.equal(denied.body.code, 'account_auth_unavailable');
  assert(!denied.body.api_key);
  const me = await page.evaluate(s => fetch('/v1/sites/' + s + '/me', { credentials: 'include' }).then(r => r.json()), site);
  assert.equal(me.signed_in, true); assert.equal(me.email, email);
  step('security checks for ' + email, { cookie: 'HttpOnly visitor cookie', accountAuth: denied.status + ' ' + denied.body.code });
}

async function recipeMode(browser) {
  const init = await rpc('initialize', { protocolVersion: '2025-11-25', capabilities: {}, clientInfo: { name: 'e2e-visitor-records', version: '1' } });
  assert(init.instructions.includes('TWO SIGN-INS, ONE RULE') && init.instructions.includes('get_page_recipe'), 'instructions lack the two sign-ins and the recipe');
  for (const gone of ['declare_data', 'read_collection', 'SH.data', 'Submissions']) assert(!init.instructions.includes(gone), 'instructions still mention ' + gone);
  const listing = await rpc('tools/list', {});
  const names = listing.tools.map(t => t.name);
  assert(names.includes('get_page_recipe') && names.includes('storage_set_resource'), 'tool list');
  for (const gone of ['declare_data', 'read_collection', 'get_state', 'add_to_collection']) assert(!names.includes(gone), gone + ' still listed');
  step('connector instructions and tool list', { tools: names.length });

  const retired = await rpc('tools/call', { name: 'read_collection', arguments: { site, collection: 'orders' } });
  assert(retired.isError && /no longer offered/.test(retired.content[0].text), 'retired tool answer');
  step('retired tool answers plainly');

  const recipe = (await tool('get_page_recipe', { topic: 'records', site })).structuredContent.recipe;
  assert(recipe.includes('SH.mount') && recipe.includes('"preset": "records"') && /Never call \/v1\/auth/.test(recipe), 'recipe content');
  const pub = await fetch(info.url + '/recipes/records.md');
  assert.equal(pub.status, 200); assert((await pub.text()).includes('SH.mount'));
  const setup = [...recipe.matchAll(/^(storage_set_resource|storage_sql_schema) (\{.*\})$/gm)].map(m => ({ name: m[1], args: JSON.parse(m[2]) }));
  assert(setup.length >= 3, 'at least three setup calls in the recipe, got ' + setup.length);
  // The page exactly as the recipe gives it; nothing rewritten.
  const html = recipe.slice(recipe.indexOf('<!doctype html>'), recipe.indexOf('</html>') + '</html>'.length);
  assert(html.includes('https://simple-host.app/auth.js') && html.includes(`window.SH_CONFIG = { site: "${site}" };`), 'recipe page shape');
  await publish({ 'index.html': html });
  for (const c of setup) {
    const res = await rpc('tools/call', { name: c.name, arguments: c.args });
    assert(!res.isError || /already exists/i.test(res.content[0].text), c.name + ': ' + res.content[0].text);
  }
  step('published the recipe page verbatim and ran its owner setup', { setup: setup.map(c => c.name) });

  const contexts = [await newContext(browser), await newContext(browser)];
  const pages = await Promise.all(contexts.map(c => c.newPage()));
  const errors = [];
  pages.forEach(p => { p.on('pageerror', e => errors.push(e.message)); p.on('console', m => consoleLog.push(m.type() + ': ' + m.text().slice(0, 200))); });
  const customers = [{ email: ann, product: 'Cumin' }, { email: bob, product: 'Turmeric' }];
  for (let i = 0; i < 2; i++) {
    const p = pages[i], c = customers[i];
    await p.goto(origin + '/');
    await p.locator('#products div', { hasText: c.product }).getByRole('button', { name: 'Add' }).click();
    await p.locator('#cart', { hasText: c.product }).waitFor();
    await p.locator('#note').fill('for ' + c.email);
    await p.locator('#checkout').click();
    // requireSignIn scrolls to the box and waits; the order is placed once signed in.
    await signInThroughBox(p, c.email);
    try {
      await p.locator('#checkout-status', { hasText: 'placed' }).waitFor({ timeout: 20000 });
    } catch (e) {
      throw new Error('order not placed after sign-in for ' + c.email + '; status says ' + JSON.stringify(await p.locator('#checkout-status').textContent()) + '; box: ' + JSON.stringify(await p.locator('#sh-auth').textContent()) + '; console: ' + consoleLog.slice(-6).join(' | '));
    }
    await p.locator('#orders table').waitFor();
    await p.waitForFunction(() => document.querySelectorAll('#orders table tr').length >= 2, null, { timeout: 10000 }).catch(() => {});
    const rows = await p.locator('#orders table tr').count();
    assert.equal(rows, 2, 'one order row for ' + c.email + ', got ' + (rows - 1) + '; orders text: ' + JSON.stringify(await p.locator('#orders').textContent()));
    assert.equal(await p.locator('#cart p', { hasText: 'empty' }).count(), 1, 'cart cleared');
    await commonChecks(p, contexts[i], c.email);
    step('customer placed an order', { email: c.email, product: c.product });
  }
  for (let i = 0; i < 2; i++) {
    await pages[i].reload();
    await pages[i].locator('#orders table').waitFor();
    const text = await pages[i].locator('#orders').textContent();
    assert(text.includes(customers[i].product), 'own order missing after reload');
    assert(!text.includes(customers[1 - i].product), 'saw the other customer\'s order');
    assert.equal(await pages[i].locator('#orders table tr').count(), 2);
  }
  step('each customer sees only their own order (after reload, session kept)');
  await pages[0].getByRole('button', { name: 'Ask to cancel' }).click();
  await pages[0].locator('#orders', { hasText: 'cancel_request' }).waitFor();
  await pages[1].reload(); await pages[1].locator('#orders table').waitFor();
  assert(!(await pages[1].locator('#orders').textContent()).includes('cancel_request'));
  step('a linked change row stays with its customer');

  // The owner's view: every order of every customer (this run's two, picked out by their notes
  // so an earlier run against the same fixture does not confuse the count).
  const everything = (await tool('storage_sql_query', { site, name: 'orders', sql: 'SELECT id, visitor_id, items, total_cents, status, created_at, note FROM orders ORDER BY id', params: [] })).structuredContent.response;
  const all = { rows: everything.rows.filter(r => r[6] === 'for ' + ann || r[6] === 'for ' + bob) };
  assert.equal(all.rows.length, 2, 'owner sees both orders (' + everything.rows.length + ' in total)');
  const visitorIDs = new Set(all.rows.map(r => r[1]));
  assert.equal(visitorIDs.size, 2); assert(all.rows.every(r => r[5]), 'created_at stamped');
  const ids = all.rows.map(r => r[0]);
  const changes = (await tool('storage_sql_query', { site, name: 'orders', sql: 'SELECT order_id, kind FROM order_changes WHERE order_id IN (?, ?)', params: ids })).structuredContent.response;
  assert.equal(changes.rows.length, 1);
  const annID = all.rows.find(r => r[6] === 'for ' + ann)[0];
  await tool('storage_sql_execute', { site, name: 'orders', sql: 'UPDATE orders SET status = ? WHERE id = ?', params: ['shipped', annID] });
  await pages[0].reload(); await pages[0].locator('#orders', { hasText: 'shipped' }).waitFor();
  await pages[1].reload(); await pages[1].locator('#orders table').waitFor();
  assert(!(await pages[1].locator('#orders').textContent()).includes('shipped'));
  step('owner sees all orders and set a status the customer sees', { orders: all.rows.length, allRowsInResource: everything.rows.length, shipped: annID });

  // Visitors cannot send SQL or change rows on an add-only, own-read database.
  // The trigger keeps a forged status out, whatever the page sends.
  const forged = await pages[0].evaluate(async s => {
    const r = await fetch('/v1/sites/' + s + '/storage/sqlite/orders/tables/orders/rows', { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json', 'X-SH-CSRF': '1' }, body: JSON.stringify({ items: '[]', total_cents: 1, status: 'shipped' }) });
    return { status: r.status, code: (await r.json()).code };
  }, site);
  // The trigger's RAISE(ABORT) surfaces as the generic 409 row_conflict; 400 covers an invalid row.
  assert([400, 409].includes(forged.status), 'forged status accepted: ' + JSON.stringify(forged));
  step('a forged status on insert is refused', forged);
  const forbidden = await pages[0].evaluate(async s => {
    const q = await fetch('/v1/sites/' + s + '/storage/sqlite/orders/query', { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json', 'X-SH-CSRF': '1' }, body: JSON.stringify({ sql: 'SELECT * FROM orders', params: [] }) });
    return { status: q.status, code: (await q.json()).code };
  }, site);
  assert.equal(forbidden.status, 403); assert.equal(forbidden.code, 'fixed_routes_required');
  step('visitor raw SQL refused', forbidden);
  assert.deepEqual(errors, [], 'page errors: ' + errors.join('; '));
}

async function siteMode(browser) {
  const files = (await tool('get_site', { site })).structuredContent;
  const paths = (files.files || []).map(f => f.path || f);
  const texts = {};
  for (const p of paths) {
    if (!/\.(html?|js|mjs|css)$/i.test(p)) continue;
    const res = (await tool('read_site_file', { site, path: p })).structuredContent;
    texts[p] = res.content || res.text || '';
  }
  // Keep what the agent built, as evidence.
  const dump = process.env.SITE_DUMP_DIR;
  if (dump) {
    fs.mkdirSync(dump, { recursive: true });
    for (const [p, t] of Object.entries(texts)) fs.writeFileSync(dump + '/' + p.replace(/\//g, '__'), t);
  }
  const bad = [];
  for (const [p, t] of Object.entries(texts)) {
    for (const re of [/\/v1\/auth(\/verify)?["'`\s?]/, /\/collections\//, /SH\.data\(/, /SH\.collection\(/, /SH\.state\b/, /\/v1\/sites\/[^"'`]*\/(state|data)\b/, /X-API-Key/i, /shk_[a-z0-9]/i]) {
      if (re.test(t)) bad.push(p + ': ' + re.source);
    }
  }
  assert.deepEqual(bad, [], 'site files use a wrong path: ' + bad.join('; '));
  const usesSH = Object.values(texts).some(t => /auth\.js/.test(t)) && Object.values(texts).some(t => /SH\.(mount|requireSignIn)/.test(t));
  assert(usesSH, 'site does not load auth.js with SH.mount/SH.requireSignIn');
  step('site files use visitor sign-in and storage only', { files: paths.length });

  const resources = (await tool('storage_list_resources', { site })).structuredContent.response.resources || [];
  const own = resources.filter(r => r.kind === 'sqlite' && r.read === 'own');
  assert(own.length >= 1, 'no own-read SQLite resource: ' + JSON.stringify(resources));
  assert(own.every(r => r.write_mode === 'add'), 'own-read resource is not add-only');
  const tables = {};
  for (const r of own) {
    const q = (await tool('storage_sql_query', { site, name: r.name, sql: "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'", params: [] })).structuredContent.response;
    tables[r.name] = q.rows.map(x => x[0]);
  }
  step('owner setup', { resources: own.map(r => r.name), tables });

  const contexts = [await newContext(browser), await newContext(browser)];
  const pages = await Promise.all(contexts.map(c => c.newPage()));
  const customers = [ann, bob];
  const before = {};
  for (const r of own) {
    for (const t of tables[r.name]) {
      const q = (await tool('storage_sql_query', { site, name: r.name, sql: `SELECT count(*) FROM "${t}"`, params: [] })).structuredContent.response;
      before[r.name + '.' + t] = q.rows[0][0];
    }
  }
  for (let i = 0; i < 2; i++) {
    const p = pages[i];
    await p.goto(origin + '/');
    await p.waitForFunction(() => window.SH && window.SH.ready);
    // Best effort at the page's own flow: add to the cart, then go to checkout.
    for (const re of [/add to cart|add/i]) {
      const b = p.getByRole('button', { name: re }).first();
      if (await b.count()) { await b.click().catch(() => {}); break; }
    }
    for (const re of [/checkout|place order|order now|buy|sign in/i]) {
      const b = p.getByRole('button', { name: re }).first();
      if (await b.count()) { await b.click().catch(() => {}); }
      const l = p.getByRole('link', { name: re }).first();
      if (!(await b.count()) && (await l.count())) { await l.click().catch(() => {}); await p.waitForLoadState().catch(() => {}); }
    }
    await p.waitForFunction(() => window.SH && window.SH.ready).catch(() => {});
    if (await p.locator('#sh-auth input[type=email], [id*=sh-auth] input[type=email]').count()) {
      await signInThroughBox(p, customers[i]);
    } else {
      // The page offers sign-in its own way; use the helper the page loaded.
      await p.evaluate(async email => { await SH.email.request(email); }, customers[i]);
      const code = await mailCode(customers[i]);
      await p.evaluate(async ([email, code]) => { await SH.email.verify(email, code); }, [customers[i], code]);
    }
    await commonChecks(p, contexts[i], customers[i]);
    // Try the page's order button once more now that the customer is signed in.
    for (const re of [/place order|checkout|order now|buy|submit/i]) {
      const b = p.getByRole('button', { name: re }).first();
      if (await b.count()) { await b.click().catch(() => {}); break; }
    }
    await p.waitForTimeout(1500);
  }
  // Isolation: what each customer reads back must be theirs alone, and the owner sees all.
  const seen = [[], []];
  for (let i = 0; i < 2; i++) {
    for (const r of own) for (const t of tables[r.name]) {
      const rows = await pages[i].evaluate(async ([r, t]) => {
        try { const res = await SH.storage.sqlite(r).table(t).list({ limit: 100 }); return res.rows.map(x => x.join('|')); } catch (e) { return ['ERR ' + e.code]; }
      }, [r.name, t]);
      seen[i].push(...rows.map(x => r.name + '.' + t + ':' + x));
    }
  }
  const overlap = seen[0].filter(x => seen[1].includes(x) && !x.startsWith('ERR'));
  assert.deepEqual(overlap, [], 'a customer read another customer\'s row: ' + overlap.join('; '));
  const after = {};
  for (const r of own) for (const t of tables[r.name]) {
    const q = (await tool('storage_sql_query', { site, name: r.name, sql: `SELECT count(*) FROM "${t}"`, params: [] })).structuredContent.response;
    after[r.name + '.' + t] = q.rows[0][0];
  }
  const added = Object.keys(after).map(k => after[k] - before[k]).reduce((a, b) => a + b, 0);
  step('isolation: no row is visible to both customers; the owner counts', { ownRowsAnn: seen[0].length, ownRowsBob: seen[1].length, rowsAddedByUI: added });
  report.uiPlacedOrders = added >= 2;
  if (added < 2) console.log('note: the page\'s own buttons did not place two orders through the heuristic clicks; check the site by hand (data isolation and sign-in still verified).');
}

const browser = await launch();
try {
  if (mode === 'site') await siteMode(browser); else await recipeMode(browser);
  report.result = 'PASS';
  console.log('PASS', JSON.stringify(report));
} catch (e) {
  report.result = 'FAIL: ' + e.message;
  console.error('FAIL', e.message);
  console.error(JSON.stringify(report));
  process.exitCode = 1;
} finally {
  await browser.close();
  if (!keep && !live) await fetch(info.url + '/_fixture/stop', { method: 'POST' }).catch(() => {});
}
