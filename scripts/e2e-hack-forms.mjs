#!/usr/bin/env node
// Browser walk of Simple Hack's web forms as organiser, participants and
// judges (SIM-215). It records what fails instead of stopping at the first
// failure, because the point is a bug list, not a green check.
//
//   HACK_INK_BROWSER_FILE=/tmp/f.json DB_DSN=... go test ./internal/handler/ -run '^TestServeHackInkBrowser$'
//   node scripts/e2e-hack-forms.mjs /tmp/f.json [--out dir] [--only tracks,signin]
//
// The fixture serves the hosted app on 127.0.0.1 and captures sign-in mail at
// /_fixture/mail. Requests to *.simple-hack.test are forwarded to it with the
// Host they were addressed to, so event pages resolve as they do in production.
import fs from 'node:fs';
import http from 'node:http';
import { createRequire } from 'node:module';
const require = createRequire(process.env.PLAYWRIGHT_DIR ? process.env.PLAYWRIGHT_DIR + '/' : '/tmp/tsx/node_modules/');
const { chromium } = require('playwright');

const args = process.argv.slice(2);
const opt = (name, def) => { const i = args.indexOf('--' + name); return i >= 0 ? args[i + 1] : def; };
const info = JSON.parse(fs.readFileSync(args.find(a => !a.startsWith('--')), 'utf8'));
const out = opt('out', 'e2e-hack-forms-out');
const only = (opt('only', '') || '').split(',').filter(Boolean);
const width = Number(opt('width', '1280'));
fs.mkdirSync(out, { recursive: true });
const APEX = info.url;
const DOMAIN = 'simple-hack.test';
const stamp = Date.now().toString(36);
const mail = who => `${who}-${stamp}@example.com`;

// ---- results ---------------------------------------------------------------
const results = [];
let current = null;
function begin(area, name) { current = { area, name, ok: true, notes: [], console: [], failed: [] }; results.push(current); }
function fail(note) { current.ok = false; current.notes.push(note); }
function note(n) { current.notes.push(n); }
async function attempt(page, area, name, fn) {
  if (only.length && !only.includes(area)) return;
  begin(area, name);
  const mark = { console: consoleLog.length, failed: failedReq.length };
  try { await fn(); } catch (e) { fail('threw: ' + String(e.message || e).split('\n')[0].slice(0, 400)); }
  current.console = consoleLog.slice(mark.console).filter(l => l.startsWith('error'));
  current.failed = failedReq.slice(mark.failed);
  if (current.failed.length) current.ok = false;
  if (!current.ok && page) {
    const f = `${out}/${area}-${results.length}.png`;
    await page.screenshot({ path: f, fullPage: true }).catch(() => {});
    current.screenshot = f;
  }
  console.log(current.ok ? 'pass' : 'FAIL', area, '|', name, current.ok ? '' : '| ' + current.notes.join(' ; ') + (current.failed.length ? ' | ' + current.failed.join(' ; ') : ''));
}

// ---- browser plumbing ------------------------------------------------------
const consoleLog = [], failedReq = [];
const agent = new http.Agent({ keepAlive: false, maxSockets: 32 });
async function forward(route, context) {
  const req = route.request(), u = new URL(req.url());
  const headers = { ...req.headers(), host: u.host, 'x-forwarded-proto': 'https' };
  delete headers['content-length'];
  if (!headers.cookie) {
    const jar = await context.cookies(u.href);
    if (jar.length) headers.cookie = jar.map(c => c.name + '=' + c.value).join('; ');
  }
  try {
    const res = await new Promise((resolve, reject) => {
      const r = http.request(APEX + u.pathname + u.search, { method: req.method(), headers, agent, timeout: 20000 }, s => {
        const chunks = []; s.on('data', c => chunks.push(c)); s.on('end', () => resolve({ status: s.statusCode, headers: s.headers, body: Buffer.concat(chunks) }));
      });
      r.on('timeout', () => r.destroy(new Error('timeout'))); r.on('error', reject); r.end(req.postDataBuffer());
    });
    delete res.headers['content-length'];
    await route.fulfill(res);
  } catch (e) { consoleLog.push('error: forward ' + u.href + ' ' + e.message); await route.abort().catch(() => {}); }
}
async function newPerson(browser, label) {
  const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width, height: 900 } });
  await context.route(`**://*.${DOMAIN}/**`, r => forward(r, context));
  const page = await context.newPage();
  page.on('pageerror', e => consoleLog.push('error: [' + label + '] pageerror ' + e.message.slice(0, 300)));
  page.on('console', m => consoleLog.push(m.type() + ': [' + label + '] ' + m.text().slice(0, 300)));
  // Routes production registers outside the shared test app (checked live 2026-10-27): not bugs.
  const fixtureGaps = [/\/v1\/auth\/oauth\/providers$/, /\/website\/notifications$/];
  page.on('response', r => { const path = new URL(r.url()).pathname; if (r.status() >= 400 && !r.url().includes('/_fixture/') && !fixtureGaps.some(g => g.test(path))) failedReq.push(`[${label}] ${r.status()} ${r.request().method()} ${new URL(r.url()).pathname}`); });
  page.on('dialog', d => d.accept());
  return { label, email: mail(label), context, page };
}
async function code(email, before) {
  for (let i = 0; i < 80; i++) {
    const r = await (await fetch(APEX + '/_fixture/mail?email=' + encodeURIComponent(email))).json();
    if (r.code && r.code !== before) return r.code;
    await new Promise(res => setTimeout(res, 250));
  }
  throw new Error('no sign-in code mailed to ' + email);
}
async function lastCode(email) { return (await (await fetch(APEX + '/_fixture/mail?email=' + encodeURIComponent(email))).json()).code || ''; }
// The shared sign-in card: email, Continue, then the six-digit code (submits itself).
async function signIn(p, url = APEX + '/signin') {
  const page = p.page;
  if (url) await page.goto(url);
  const emailField = page.locator('input[type=email]').first();
  await emailField.waitFor({ timeout: 15000 });
  const before = await lastCode(p.email);
  await emailField.fill(p.email);
  await page.getByRole('button', { name: 'Continue', exact: true }).first().click();
  const codeField = page.locator('input[autocomplete="one-time-code"]').first();
  await codeField.waitFor({ timeout: 15000 });
  await codeField.fill(await code(p.email, before));
  await page.waitForFunction(() => !location.pathname.startsWith('/signin') || document.querySelector('#view-events:not([hidden])'), null, { timeout: 20000 }).catch(() => {});
  await page.waitForTimeout(800);
}
async function visibleError(page) {
  const t = await page.locator('[role=alert]:visible, .err:visible, .live.err:visible').allInnerTexts().catch(() => []);
  return t.map(s => s.trim()).filter(Boolean).join(' / ');
}
// First visits open a walkthrough dialog over the page; a person reads it and dismisses it.
async function dismissTours(page) {
  for (let i = 0; i < 4; i++) {
    const d = page.locator('button:visible', { hasText: /^\s*Dismiss\s*$/ }).first();
    try { await d.waitFor({ state: 'visible', timeout: i ? 600 : 2000 }); } catch { break; }
    await d.click(); await page.waitForTimeout(300);
  }
}
async function tab(page, name) { await dismissTours(page); await page.locator('#tab-' + name).click(); await page.waitForTimeout(400); await dismissTours(page); }

// ---- journeys --------------------------------------------------------------
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM || '/home/ubuntu/.cache/ms-playwright/chromium-1234/chrome-linux/chrome' });
const org = await newPerson(browser, 'org');
const eventSlug = 'e2e-' + stamp;
let manageURL = APEX + '/e/' + eventSlug + '/manage';

await attempt(org.page, 'signin', 'organiser signs in with an emailed code', async () => {
  await signIn(org);
  if (new URL(org.page.url()).pathname.startsWith('/signin')) fail('still on /signin after entering the code: ' + await visibleError(org.page));
});

await attempt(org.page, 'create', 'create an event from the form', async () => {
  const page = org.page;
  await page.goto(APEX + '/events/new');
  await page.locator('#new-title').fill('Generation Girl test ' + stamp);
  await page.locator('#new-slug').fill(eventSlug);
  await page.locator('#new-organiser').fill('Tsabita Test');
  await page.locator('#new-org').fill('Generation Girl');
  await page.locator('#new-email').fill(org.email);
  await page.locator('#new-purpose').fill('A hackathon for girls building solutions for the SDGs.');
  const d = new Date(Date.now() + 7 * 864e5).toISOString().slice(0, 10), e2 = new Date(Date.now() + 9 * 864e5).toISOString().slice(0, 10);
  await page.locator('#new-start').fill(d);
  await page.locator('#new-end').fill(e2);
  await page.locator('#new-n').fill('100');
  await page.locator('#new-submit').click();
  await page.waitForURL(u => u.pathname.includes('/e/' + eventSlug), { timeout: 20000 }).catch(() => {});
  if (!page.url().includes('/e/' + eventSlug)) fail('did not land on the event after Create: ' + page.url() + ' ' + await visibleError(page));
  manageURL = APEX + '/e/' + eventSlug + '/manage';
});

const trackNames = [
  'Track 1: Quality Education (SDG 4)',
  'Track 2: Good Health and Wellbeing (SDG 3)',
  'Track 3: Gender Equality (SDG 5)',
  'Éducation – équité ✨',
  'A'.repeat(20) + ' track with a sixty character name ' + 'B'.repeat(5),
];
for (const name of trackNames) {
  await attempt(org.page, 'tracks', `add and save track "${name}"`, async () => {
    const page = org.page;
    if (!page.url().includes('/manage')) await page.goto(manageURL);
    await tab(page, 'teams');
    await page.locator('#track-new-name').fill(name);
    await page.locator('#track-add').click();
    const failsBefore = failedReq.length;
    await page.locator('#tracks-save').click();
    await page.waitForTimeout(1500);
    const err = await visibleError(page);
    if (failedReq.length > failsBefore || /slug|lowercase|invalid|error/i.test(err)) fail('save rejected: ' + (err || 'no message on screen'));
    await page.reload(); await tab(page, 'teams');
    const values = await page.locator('#tracks-editor input, #tracks-editor textarea').evaluateAll(els => els.map(e => e.value));
    if (!values.some(v => v === name)) fail('after reload the track is not in the list (saved names: ' + JSON.stringify(values.filter(Boolean).slice(0, 8)) + ')');
    // Leave the list clean for the next name: remove anything that failed.
    if (!current.ok) { await page.reload(); await tab(page, 'teams'); }
  });
}

fs.writeFileSync(`${out}/report.json`, JSON.stringify({ when: new Date().toISOString(), width, results }, null, 2));
const md = ['# Simple Hack web forms e2e', '', `Width ${width} px. ${results.filter(r => r.ok).length} passed, ${results.filter(r => !r.ok).length} failed.`, '',
  ...results.map(r => `- ${r.ok ? 'pass' : '**FAIL**'} · ${r.area} · ${r.name}${r.ok ? '' : ' · ' + [...r.notes, ...r.failed].join('; ')}`)];
fs.writeFileSync(`${out}/report.md`, md.join('\n') + '\n');
await browser.close();
if (opt('stop', '') === '1') await fetch(APEX + '/_fixture/stop', { method: 'POST' }).catch(() => {});
console.log(`\n${results.filter(r => !r.ok).length} failed of ${results.length}; report in ${out}/report.md`);
