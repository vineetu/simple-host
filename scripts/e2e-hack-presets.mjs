#!/usr/bin/env node
// Browser proof for storage presets on Simple Hack: the Hack connector's own
// recipes (get_page_recipe with a team key), published on a team site and on
// an organiser's custom event website, then driven by real visitors, a team
// member and an organiser signed in through the sites' own sign-in boxes.
//
//   HACK_PRESETS_FIXTURE=/tmp/f.json DB_DSN=... go test ./internal/handler/ -run TestServeHackPresetsBrowser
//   node scripts/e2e-hack-presets.mjs /tmp/f.json [--browser chromium|webkit] [--shots dir]
//
// Every browser request is forwarded to the fixture with the Host it was
// addressed to, so auth.js from https://simple-hack.app is the fixture's too.
import fs from 'node:fs';
import http from 'node:http';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
const require = createRequire(process.env.PLAYWRIGHT_DIR ? process.env.PLAYWRIGHT_DIR + '/' : import.meta.url);
const { chromium, webkit } = require('playwright');

const args = process.argv.slice(2);
const fixtureFile = args.find(a => !a.startsWith('--'));
const opt = (name, def) => { const i = args.indexOf('--' + name); return i >= 0 ? args[i + 1] : def; };
const browserName = opt('browser', 'chromium');
const shots = opt('shots', '');
const info = JSON.parse(fs.readFileSync(fixtureFile, 'utf8'));
const live = false;
const stamp = Date.now().toString(36);
const ann = `ann${stamp}@example.com`, bob = `bob${stamp}@example.com`, carol = `carol${stamp}@example.com`;
const failedRequests = new Set();
const report = { browser: browserName, steps: [] };
const consoleLog = [];
const step = (name, detail) => { report.steps.push({ name, ...(detail ? { detail } : {}) }); console.log('ok', name, detail ? JSON.stringify(detail) : ''); };
const teamOrigin = `https://${info.team}.${info.event}.${info.site_domain}`;
const eventOrigin = `https://${info.event}.${info.site_domain}`;

let rpcID = 0;
async function tool(name, a) {
  const r = await fetch(info.url + '/mcp', {
    method: 'POST', headers: { 'Content-Type': 'application/json', 'X-API-Key': info.team_key },
    body: JSON.stringify({ jsonrpc: '2.0', id: ++rpcID, method: 'tools/call', params: { name, arguments: a } }),
  });
  const body = await r.json();
  assert(body.result && !body.result.isError, `tool ${name} failed: ${JSON.stringify(body.error || body.result?.content?.[0]?.text)}`);
  return body.result;
}
async function rest(method, path, body, key) {
  const r = await fetch(info.url + path, { method, headers: { 'Content-Type': 'application/json', 'X-API-Key': key }, body: body == null ? undefined : JSON.stringify(body) });
  let b = null; try { b = await r.json(); } catch (e) {}
  return { status: r.status, body: b };
}
async function mailCode(email) {
  for (let i = 0; i < 40; i++) {
    const r = await (await fetch(info.url + '/_fixture/mail?email=' + encodeURIComponent(email))).json();
    if (r.code) return r.code;
    await new Promise(res => setTimeout(res, 250));
  }
  throw new Error('no code mailed to ' + email);
}
async function clearCode(email) {
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
  const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1000, height: 900 } });
  await context.route('**/*', route => forward(route, context));
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


async function recipe(topic, site) {
  const text = (await tool('get_page_recipe', { topic, site })).structuredContent.recipe;
  assert(text.startsWith('On Simple Hack'), topic + ': no Hack note');
  const setupText = text.slice(text.indexOf('## 1.'), text.indexOf('## 2.'));
  const calls = [...setupText.matchAll(/^(storage_\w+) (\{.*\})$/gm)].map(m => ({ name: m[1], args: JSON.parse(m[2]) }));
  const html = text.slice(text.indexOf('<!doctype html>'), text.indexOf('</html>') + '</html>'.length);
  assert(html.includes('https://simple-hack.app/auth.js') && html.includes(`window.SH_CONFIG = { site: "${site}" };`), topic + ': page shape');
  assert(!/innerHTML/.test(html), topic + ': page uses innerHTML');
  return { calls, html };
}
async function pageFetch(page, path, init) {
  return page.evaluate(async ([p, i]) => { const r = await fetch(p, i); let b = null; try { b = await r.json(); } catch (e) {} return { status: r.status, body: b }; }, [path, init || {}]);
}

async function run() {
  const browser = browserName === 'webkit' ? await webkit.launch() : await chromium.launch({ executablePath: process.env.CHROMIUM || undefined });
  try {
    // ---------- team site setup: the team key and the Hack connector's recipes ----------
    const wall = await recipe('wall', info.team);
    const personal = await recipe('personal', info.team);
    let pub = await fetch(info.url + '/mcp', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-API-Key': info.team_key },
      body: JSON.stringify({ jsonrpc: '2.0', id: ++rpcID, method: 'tools/call', params: { name: 'create_site', arguments: { site: info.team, files: { 'index.html': wall.html, 'wishlist.html': personal.html } } } }) }).then(r => r.json());
    if (pub.result?.isError) await tool('update_site', { site: info.team, files: { 'index.html': wall.html, 'wishlist.html': personal.html } });
    for (const c of [...wall.calls, ...personal.calls]) await tool(c.name, c.args);
    const list = (await tool('storage_list_resources', { site: info.team })).structuredContent.response.resources;
    assert.deepEqual(list.map(r => r.name + ':' + r.preset).sort(), ['guestbook:wall', 'wishlist:personal']);
    step('team: the team key ran the Hack recipes\' setup and published both pages', { resources: list.map(r => r.name + ':' + r.preset) });

    // ---------- wall on the team site ----------
    {
      const a = await newPage(browser), b = await newPage(browser), anon = await newPage(browser);
      await a.goto(teamOrigin + '/');
      await a.locator('#post [name=name]').fill('Ann');
      await a.locator('#post [name=message]').fill('Great demo ' + stamp);
      await a.locator('#post button[type=submit]').click();
      await signIn(a, ann);
      await a.locator('#status', { hasText: 'Thanks' }).waitFor({ timeout: 20000 });
      await a.locator('.entry', { hasText: 'Great demo' }).getByRole('button', { name: 'Delete my post' }).waitFor();
      await anon.goto(teamOrigin + '/');
      await anon.locator('.entry', { hasText: 'Great demo' }).waitFor();
      assert.equal(await anon.getByRole('button', { name: 'Delete my post' }).count(), 0);
      await b.goto(teamOrigin + '/');
      await signIn(b, bob);
      await b.locator('.entry', { hasText: 'Great demo' }).waitFor();
      assert.equal(await b.getByRole('button', { name: 'Delete my post' }).count(), 0, 'bob sees a delete button on ann\'s post');
      const id = (await pageFetch(b, `/v1/sites/${info.team}/storage/sqlite/guestbook/tables/entries/rows`)).body.rows[0][0];
      const del = await pageFetch(b, `/v1/sites/${info.team}/storage/sqlite/guestbook/tables/entries/rows/${id}`, { method: 'DELETE', headers: { 'X-SH-CSRF': '1' } });
      assert.equal(del.status, 404, 'bob deleted ann\'s post');
      const leak = await pageFetch(anon, `/v1/sites/${info.team}/storage/sqlite/guestbook/tables/entries/rows`);
      assert(!JSON.stringify(leak.body).match(/[0-9a-f]{8}-[0-9a-f]{4}-/), 'author ids reached a stranger');
      if (shots) await a.screenshot({ path: `${shots}/hack-${browserName}-team-wall.png`, fullPage: true });
      await a.locator('.entry', { hasText: 'Great demo' }).getByRole('button', { name: 'Delete my post' }).click();
      await a.locator('#entries', { hasText: 'Nobody has signed yet' }).waitFor();
      step('team wall: anyone reads; signed-in visitors post; only the author deletes; another visitor gets 404', { bobDelete: del.status });
      for (const p of [a, b, anon]) await p.context().close();
    }

    // ---------- event website inbox: organisers are the owner ----------
    {
      const inbox = await recipe('inbox', info.event);
      const base = `/v1/hack/events/${info.event}/website`;
      let r = await rest('PUT', base + '/files?create=1', { files: { 'index.html': inbox.html } }, info.org_key);
      assert(r.status === 201 || r.status === 200, 'event publish ' + r.status + ' ' + JSON.stringify(r.body));
      r = await rest('PATCH', base, { mode: 'custom' }, info.org_key);
      assert.equal(r.status, 200, 'custom mode ' + JSON.stringify(r.body));
      for (const c of inbox.calls) {
        const a = c.args;
        if (c.name === 'storage_set_resource') r = await rest('PUT', `${base}/storage/resources/${a.name}`, a.body, info.org_key);
        else if (c.name === 'storage_sql_schema') r = await rest('POST', `${base}/storage/sqlite/${a.name}/schema`, { sql: a.sql }, info.org_key);
        else throw new Error('unexpected setup call ' + c.name);
        assert(r.status < 300, c.name + ' ' + r.status + ' ' + JSON.stringify(r.body));
      }
      step('event: the organiser routes ran the inbox setup and published the event website', { setup: inbox.calls.map(c => c.name) });
      const v = await newPage(browser);
      await v.goto(eventOrigin + '/');
      await v.locator('#contact [name=name]').fill('Carol');
      await v.locator('#contact [name=email]').fill(carol);
      await v.locator('#contact [name=message]').fill('Is there food? ' + stamp);
      await v.locator('#contact button[type=submit]').click();
      await signIn(v, carol);
      await v.locator('#status', { hasText: 'sent' }).waitFor({ timeout: 20000 });
      const back = await pageFetch(v, `/v1/sites/${info.event}/storage/sqlite/messages/tables/messages/rows`);
      assert.equal(back.status, 403, 'sender read back the inbox');
      if (shots) await v.screenshot({ path: `${shots}/hack-${browserName}-event-inbox-sent.png`, fullPage: true });
      const m = await newPage(browser);
      await m.goto(eventOrigin + '/');
      await signIn(m, info.member_email);
      const memberRead = await pageFetch(m, `/v1/sites/${info.event}/storage/sqlite/messages/tables/messages/rows`);
      assert.equal(memberRead.status, 403, 'a participant read the organisers\' inbox');
      const o = await newPage(browser);
      await o.goto(eventOrigin + '/');
      await signIn(o, info.org_email);
      const me = await pageFetch(o, `/v1/sites/${info.event}/me`);
      assert.equal(me.body.site_owner, true, 'organiser me ' + JSON.stringify(me.body));
      const all = await pageFetch(o, `/v1/sites/${info.event}/storage/sqlite/messages/tables/messages/rows`);
      assert.equal(all.status, 200);
      assert(JSON.stringify(all.body).includes('Is there food? ' + stamp), 'organiser on the site sees the message');
      const q = await rest('POST', `/v1/hack/events/${info.event}/website/storage/sqlite/messages/query`, { sql: 'SELECT message, visitor_id FROM messages' }, info.org_key);
      assert(q.body.rows.some(row => String(row[0]).includes(stamp) && row[1]), 'organiser route sees the sender');
      step('event inbox: a visitor sends and cannot read it back; a participant signed in there gets 403; an organiser signed in there reads it (site_owner true); the organiser route sees the sender', { senderRead: back.status, participantRead: memberRead.status });
      for (const p of [v, m, o]) await p.context().close();
    }

    // ---------- personal data on the team site; a member is the owner until the deadline ----------
    {
      const a = await newPage(browser), b = await newPage(browser), m = await newPage(browser);
      await a.goto(teamOrigin + '/wishlist.html');
      await signIn(a, ann);
      await a.locator('#add [name=title]').fill('Kite ' + stamp);
      await a.locator('#add button[type=submit]').click();
      await a.locator('#items li', { hasText: 'Kite ' + stamp }).waitFor();
      await b.goto(teamOrigin + '/wishlist.html');
      await signIn(b, bob);
      await b.locator('#items li', { hasText: 'Nothing yet.' }).waitFor();
      assert.equal(await b.locator('#items li', { hasText: 'Kite' }).count(), 0, 'bob sees ann\'s wishlist');
      await m.goto(teamOrigin + '/wishlist.html');
      await signIn(m, info.member_email);
      await m.locator('#items li', { hasText: 'Kite ' + stamp }).waitFor();
      const me = await pageFetch(m, `/v1/sites/${info.team}/me`);
      assert.equal(me.body.site_owner, true, 'member me ' + JSON.stringify(me.body));
      const settings = await pageFetch(m, `/v1/sites/${info.team}/storage/resources`);
      assert.equal(settings.status, 403, 'member on the site reached settings');
      if (shots) await m.screenshot({ path: `${shots}/hack-${browserName}-team-member-owner.png`, fullPage: true });
      step('team personal: each visitor sees only their own; a team member signed in on the team site sees every entry (site_owner true) but not the settings', { settings: settings.status });
      const fx = await fetch(info.url + '/_fixture/deadline-passed', { method: 'POST' });
      assert.equal(fx.status, 204);
      await m.reload();
      await m.locator('#items li', { hasText: 'Nothing yet.' }).waitFor();
      const me2 = await pageFetch(m, `/v1/sites/${info.team}/me`);
      assert.notEqual(me2.body.site_owner, true, 'member still owner after the deadline');
      const frozen = await tool('storage_list_resources', { site: info.team }).then(() => 'ok', e => e.message);
      await a.reload();
      await a.locator('#add [name=title]').fill('Late kite ' + stamp);
      await a.locator('#add button[type=submit]').click();
      await a.locator('#items li', { hasText: 'Late kite ' + stamp }).waitFor();
      const keyWrite = await rest('PUT', `/v1/sites/${info.team}/storage/resources/wishlist`, { kind: 'sqlite', preset: 'private' }, info.team_key);
      assert.equal(keyWrite.status, 409, 'team key changed storage after the deadline');
      if (shots) await m.screenshot({ path: `${shots}/hack-${browserName}-team-member-after-deadline.png`, fullPage: true });
      step('team deadline: the member on the site is an ordinary visitor again; the team key cannot change storage (409); visitors still add their own', { teamKey: keyWrite.status, keyRead: frozen });
      for (const p of [a, b, m]) await p.context().close();
    }
  } finally {
    await browser.close();
  }
}

run().then(() => {
  report.errors = errors; report.failedHosts = [...failedRequests];
  console.log(JSON.stringify(report, null, 1));
  if (!args.includes('--keep')) fetch(info.url + '/_fixture/stop', { method: 'POST' }).catch(() => {});
}).catch(e => {
  console.error('FAIL', e.message);
  console.error(consoleLog.slice(-15).join('\n'));
  fetch(info.url + '/_fixture/stop', { method: 'POST' }).catch(() => {}).finally(() => process.exit(1));
});
