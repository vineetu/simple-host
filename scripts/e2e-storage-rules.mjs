#!/usr/bin/env node
// Dashboard rules proof, using only an isolated local Postgres/HTTP fixture.
// Start (TEST_PG_URL is a bare URL to a disposable test server):
//   STORAGE_RULES_FIXTURE=/tmp/rules.json TEST_PKGS=./internal/handler TEST_COUNT=1 bash scripts/test-integration.sh -run '^TestServeStorageRulesBrowser$' -timeout 35m
// Then, in another shell:
//   PLAYWRIGHT_DIR=/opt/pw/node_modules CHROMIUM=/usr/local/bin/chromium node scripts/e2e-storage-rules.mjs /tmp/rules.json --shots /tmp/rules-shots
// TestServeStorageRulesBrowser sets STORAGE_RULES=on and KV_STORE=sqlite;
// canonical person/site hosts, captured mail and all data are fixture-local.
// This script creates its own site/resources and stops the fixture on exit
// (use --keep to leave it running). It tests 390px and 1280px in light theme.
import fs from 'node:fs';
import http from 'node:http';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
const require = createRequire(process.env.PLAYWRIGHT_DIR ? process.env.PLAYWRIGHT_DIR + '/' : import.meta.url);
const { chromium } = require('playwright');
const args = process.argv.slice(2);
const info = JSON.parse(fs.readFileSync(args[0], 'utf8'));
const shots = args.includes('--shots') ? args[args.indexOf('--shots') + 1] : '/tmp/storage-rules-shots';
fs.mkdirSync(shots, { recursive: true });
assert(['127.0.0.1', 'localhost'].includes(new URL(info.url).hostname), 'requires the local test fixture');
const site = 'rules-' + Date.now().toString(36);
const base = '/v1/sites/' + site + '/storage';
const rules = { policies: [
  { name: 'readers', command: 'read', using: 'true' },
  { name: 'owners', command: 'all', using: 'auth.role == "owner"', check: 'auth.role == "owner"' },
  { name: 'active', command: 'read', restrictive: true, using: 'true' },
] };
const own = { policies: [{ name: 'table_owner', command: 'all', using: 'auth.role == "owner"', check: 'auth.role == "owner"' }] };
async function api(method, path, body) {
  const res = await fetch(info.url + path, { method, headers: { 'X-API-Key': info.owner_key, 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) });
  assert(res.ok, method + ' ' + path + ': ' + res.status);
  return res.status === 204 ? null : res.json();
}
async function tool(name, arguments_) {
  const reply = await api('POST', '/mcp', { jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name, arguments: arguments_ } });
  assert(!reply.error && !reply.result.isError, 'fixture tool ' + name + ' failed');
}
// The browser renders the actual dashboard and uses its normal API calls.
// Forward every address to the fixture, preserving Host; no request goes live.
async function forward(route) {
  const req = route.request(), url = new URL(req.url());
  const headers = { ...req.headers(), host: url.host, 'x-forwarded-proto': 'https' };
  delete headers['content-length'];
  const result = await new Promise((resolve, reject) => {
    const request = http.request(info.url + url.pathname + url.search, { method: req.method(), headers, agent: false }, res => {
      const chunks = [];
      res.on('data', chunk => chunks.push(chunk));
      res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body: Buffer.concat(chunks) }));
    });
    request.on('error', reject);
    request.end(req.postDataBuffer());
  });
  delete result.headers['content-length'];
  delete result.headers['transfer-encoding'];
  await route.fulfill(result);
}
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM || undefined });
try {
  await tool('create_site', { site, files: { 'index.html': '<!doctype html><title>Rules browser fixture</title>' } });
  for (const width of [390, 1280]) {
    await api('PUT', base + '/resources/converted', { kind: 'sqlite', rules, tables: { kept: { rules: own } } });
    await tool('storage_sql_schema', { site, name: 'converted', sql: 'CREATE TABLE IF NOT EXISTS items (id INTEGER PRIMARY KEY, value TEXT)', params: [] });
    await api('PUT', base + '/resources/kv_rules', { kind: 'kv', rules });
    await api('PUT', base + '/resources/unconverted', { kind: 'sqlite', preset: 'private' });
    await api('PUT', base + '/resources/kv_presets', { kind: 'kv', preset: 'private' });
    await api('PUT', base + '/resources/photos', { kind: 'files', preset: 'public' });
    const context = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: 'light',
      locale: width === 390 ? 'en-GB' : 'en-US', timezoneId: width === 390 ? 'Asia/Kolkata' : 'America/Los_Angeles' });
    await context.route('**/*', forward);
    await context.addInitScript(({ key, handle }) => {
      localStorage.setItem('apiKey', key);
      localStorage.setItem('sh-owner-handle', handle);
    }, { key: info.owner_key, handle: info.handle });
    const page = await context.newPage(), errors = [];
    page.on('pageerror', err => errors.push(err.message));
    await page.goto('https://' + info.site_domain + '/' + info.handle);
    await page.locator('#inventory .inv-row').first().waitFor();
    await page.locator('[data-manage="' + site + '"]').click();
    await page.locator('[data-act="openStorage"][data-name="' + site + '"]').click();
    const panel = page.locator('[data-panel="' + site + '"]');
    const card = name => panel.locator('form.st-card').filter({ has: page.locator('input[name="resource"][value="' + name + '"]') });
    const converted = card('converted');
    await converted.getByLabel('Default rules', { exact: true }).waitFor();
    assert.equal(await converted.locator('select[name="preset"]').count(), 0);
    assert.match(await converted.locator('[data-policies]').first().innerText(), /readers: read · permissive · using true/);
    assert.match(await converted.locator('[data-policies]').first().innerText(), /active: read · restrictive · using true/);
    assert.equal(await converted.getByLabel('Table: kept', { exact: true }).count(), 1);
    assert.equal(await card('kv_rules').locator('select[name="preset"]').count(), 0);
    assert.equal(await page.evaluate(() => window.__SHOWCASE__.storageSwitchSet), false);
    const futureLine = 'Presets end on ' + (width === 390 ? '17 Dec 2099, 22:30' : 'Dec 17, 2099, 9:00 AM') + '. Rules replace them and allow the same things.';
    const pastLine = "Visitors can't use this until you set rules. You still have full access.";
    // The switch gate still hides both lines when no date is configured.
    // The two browser locales/timezones prove the instant is shown locally.
    for (const [switchSet, switchAt, line] of [
      [false, '', ''],
      [true, '2099-12-17T17:00:00Z', futureLine],
      [true, '2000-12-17T17:00:00Z', pastLine],
      [false, '2000-12-17T17:00:00Z', ''],
    ]) {
      await page.evaluate(async ({ site, switchSet, switchAt }) => {
        window.__SHOWCASE__.storageSwitchSet = switchSet;
        window.__SHOWCASE__.storageSwitchAt = switchAt;
        hideRowPanel(site);
        await openStorage(site, false);
      }, { site, switchSet, switchAt });
      for (const name of ['unconverted', 'kv_presets']) {
        const text = await card(name).innerText();
        if (line) assert(text.includes(line), text);
        assert.equal(text.includes('Presets end on '), line === futureLine);
        assert.equal(text.includes(pastLine), line === pastLine);
        assert.equal(await card(name).locator('select[name="preset"]').count(), 1);
      }
      for (const name of ['converted', 'kv_rules', 'photos']) {
        assert(!/Presets end on|Visitors can't use this/.test(await card(name).innerText()));
      }
    }
    await page.screenshot({ fullPage: true, path: `${shots}/rules-${width}.png` });
    // Valid JSON containing a broken expression must reach server validation.
    const broken = structuredClone(rules);
    broken.policies[0].using = 'true && )';
    await converted.getByLabel('Default rules', { exact: true }).fill(JSON.stringify(broken, null, 2));
    const refusal = page.waitForResponse(r => r.request().method() === 'PUT' && r.url().endsWith('/resources/converted'));
    await converted.getByRole('button', { name: 'Save converted', exact: true }).click();
    const response = await refusal;
    assert.equal(response.status(), 400);
    const diagnostic = (await response.json()).rule_error;
    assert(diagnostic.position >= 0);
    const error = converted.locator('.panel-err').first();
    await error.waitFor();
    assert.match(await error.innerText(), new RegExp('position ' + diagnostic.position));
    assert((await error.innerText()).includes(diagnostic.message));
    assert.equal(await error.locator('mark').innerText(), ')');
    assert.equal(await converted.getByLabel('Default rules', { exact: true }).getAttribute('aria-invalid'), 'true');
    assert.deepEqual((await api('GET', base + '/resources')).resources.find(r => r.name === 'converted').rules.policies, rules.policies);
    await page.screenshot({ fullPage: true, path: `${shots}/rule-error-${width}.png` });
    await converted.getByLabel('Default rules', { exact: true }).fill(JSON.stringify(rules, null, 2));
    async function refuse(set, label = 'Default rules') {
      const editor = converted.getByLabel(label, { exact: true });
      await editor.fill(JSON.stringify(set, null, 2));
      const done = page.waitForResponse(r => r.request().method() === 'PUT' && r.url().endsWith('/resources/converted'));
      await converted.getByRole('button', { name: 'Save converted', exact: true }).click();
      const response = await done;
      assert.equal(response.status(), 400);
      const diagnostic = (await response.json()).rule_error;
      const error = editor.locator('..').locator('.panel-err');
      await error.waitFor();
      assert((await error.innerText()).includes(diagnostic.message));
      assert.equal(await editor.getAttribute('aria-invalid'), 'true');
      assert(await editor.evaluate(el => document.activeElement === el), 'focus the failing editor');
      return { diagnostic, error };
    }
    // Semantic categories carry positions but do not name an expression field.
    for (const [policy, clause, marked] of [
      [{ command: 'read', using: 'auth.typo' }, 'binding', 'a'],
      [{ command: 'read', using: '1' }, 'type', '1'],
      [{ command: 'read', using: 'missing == 1' }, 'column', 'm'],
      [{ command: 'edit', using: 'true' }, 'anonymous_change', 't'],
      [{ command: 'add', check: 'auth.typo' }, 'binding', 'a'],
      [{ command: 'read', using: 'a'.repeat(65) }, 'size', 'a'],
      [{ command: 'add', check: 'a'.repeat(65) }, 'size', 'a'],
    ]) {
      const result = await refuse({ policies: [{ name: 'semantic', ...policy }] });
      assert.equal(result.diagnostic.clause, clause);
      assert(result.diagnostic.position >= 0);
      assert((await result.error.innerText()).includes('position ' + result.diagnostic.position));
      assert.equal(await result.error.locator('mark').innerText(), marked);
    }
    // The response has no policy index. Keep all unnamed candidates labelled,
    // including omitted names, instead of pretending the first policy failed.
    for (const name of [undefined, '']) {
      const result = await refuse({ policies: [
        { name, command: 'read', using: 'true' },
        { name, command: 'read', using: 'true && )' },
      ] });
      assert.equal(result.diagnostic.policy || '', '');
      const excerpts = result.error.locator('div');
      assert.equal(await excerpts.count(), 2);
      assert.match(await excerpts.nth(0).innerText(), /Policy 1 · using \(candidate\)/);
      assert.match(await excerpts.nth(1).innerText(), /Policy 2 · using \(candidate\)/);
      assert.equal(await excerpts.nth(1).locator('mark').innerText(), ')');
    }
    await converted.getByLabel('Default rules', { exact: true }).fill(JSON.stringify(rules, null, 2));
    // Case-insensitive lookup covers errors, duplicate detection and removal.
    async function addTable(name) {
      await converted.getByLabel('Table with its own rules', { exact: true }).fill(name);
      await converted.getByRole('button', { name: 'Add table rules', exact: true }).click();
    }
    await addTable('Notes');
    await addTable('notes');
    assert.equal(await converted.locator('textarea').count(), 3);
    const tableError = await refuse({ policies: [{ name: 'broken', command: 'read', using: 'true && )' }] }, 'Table: Notes');
    assert.equal(tableError.diagnostic.table, 'notes');
    assert.equal(await tableError.error.locator('mark').innerText(), ')');
    assert.equal(await converted.getByLabel('Default rules', { exact: true }).getAttribute('aria-invalid'), null);
    await converted.getByRole('button', { name: 'Remove rules for Notes', exact: true }).click();
    await addTable('NOTES');
    assert.equal(await converted.getByLabel('Table: NOTES', { exact: true }).count(), 1);
    await converted.getByLabel('Table: NOTES', { exact: true }).fill(JSON.stringify(own, null, 2));
    async function save(resource) {
      const done = page.waitForResponse(r => r.request().method() === 'GET' && r.url().endsWith('/storage/resources'));
      await card(resource).getByRole('button', { name: 'Save ' + resource, exact: true }).click();
      await done;
      await card(resource).getByRole('button', { name: 'Save ' + resource, exact: true }).waitFor();
    }
    await save('converted');
    let saved = (await api('GET', base + '/resources')).resources.find(r => r.name === 'converted');
    assert.deepEqual(Object.keys(saved.tables).sort(), ['kept', 'notes']);
    assert.deepEqual(saved.tables.notes.rules.policies, own.policies);
    await converted.getByRole('button', { name: 'Remove rules for notes', exact: true }).click();
    await converted.getByRole('button', { name: 'Remove rules for kept', exact: true }).click();
    await save('converted');
    saved = (await api('GET', base + '/resources')).resources.find(r => r.name === 'converted');
    assert.deepEqual(saved.tables, {});
    assert.equal(await converted.getByLabel('Table: notes', { exact: true }).count(), 0);
    // KV uses the same JSON editor; no table controls or preset fields.
    const kvEdited = { policies: [{ name: 'kv_reader', command: 'read', using: 'true' }] };
    await card('kv_rules').getByLabel('Default rules', { exact: true }).fill(JSON.stringify(kvEdited));
    await save('kv_rules');
    assert.deepEqual((await api('GET', base + '/resources')).resources.find(r => r.name === 'kv_rules').rules.policies, kvEdited.policies);
    // Files keep exactly the existing preset menu and save flow.
    const files = card('photos');
    assert(!/Presets end on|Visitors can't use this/.test(await files.innerText()));
    assert.equal(await files.locator('textarea').count(), 0);
    await files.getByLabel('Who can do what?').selectOption('private');
    await save('photos');
    assert.equal((await api('GET', base + '/resources')).resources.find(r => r.name === 'photos').preset, 'private');
    assert.deepEqual(errors, [], 'dashboard JavaScript errors');
    assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), 'light');
    console.log(`PASS storage rules: ${width}px light; policies, server error position, table add/remove, KV, conversion labels, file presets`);
    await context.close();
  }
} finally {
  await browser.close();
  if (!args.includes('--keep')) await fetch(info.url + '/_fixture/stop', { method: 'POST' });
}
