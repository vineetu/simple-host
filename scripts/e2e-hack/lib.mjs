// Shared helpers for the Simple Hack production browser tests (SIM-215).
// People sign in with throwaway account keys (hack-test-accounts.zip on
// SIM-215): the key goes into localStorage before the app loads, so no email
// is involved and any number of people can be signed in at once, one browser
// context each.
//
// Each area script records every step as pass or fail and keeps going, then
// writes <out>/<area>.json. Failures carry the on-screen error, console
// errors, failed requests and a screenshot.
import fs from 'node:fs';
import { createRequire } from 'node:module';
const require = createRequire(process.env.PLAYWRIGHT_DIR ? process.env.PLAYWRIGHT_DIR + '/' : '/tmp/tsx/node_modules/');
export const { chromium, webkit } = require('playwright');

export const SITE = process.env.HACK_SITE || 'https://simple-hack.app';
export const ACCOUNTS_DIR = process.env.HACK_ACCOUNTS || '/tmp/claude-1001/-home-ubuntu-workspace-4/2268cadb-dfb1-4db4-9617-f1516b000a56/scratchpad/hack-test-accounts';
export const CHROMIUM = '/home/ubuntu/.cache/ms-playwright/chromium-1234/chrome-linux/chrome';
export const stamp = Date.now().toString(36).slice(-6);

// accounts(1) -> { organiser:[..], 'co-organiser':[..], participant:[..], judge:[..] }
export function accounts(set) {
  const file = `${ACCOUNTS_DIR}/set-${String(set).padStart(2, '0')}.json`;
  const by = {};
  for (const a of JSON.parse(fs.readFileSync(file, 'utf8')).accounts) (by[a.role] ||= []).push(a);
  return by;
}

export class Run {
  constructor(area, out) {
    this.area = area; this.out = out; this.results = []; this.console = []; this.failed = []; this.browsers = [];
    fs.mkdirSync(out, { recursive: true });
  }
  async browser(kind = 'chromium') {
    const b = kind === 'webkit' ? await webkit.launch() : await chromium.launch({ executablePath: CHROMIUM });
    this.browsers.push(b); return b;
  }
  // A signed-in person: own context, key set before any page script runs.
  async person(browser, account, { width = 1280, label } = {}) {
    const context = await browser.newContext({ viewport: { width, height: 900 } });
    if (account) await context.addInitScript(k => { try { localStorage.setItem('apiKey', k); } catch (e) {} }, account.api_key);
    const page = await context.newPage();
    const who = label || (account ? account.username : 'visitor');
    page.on('pageerror', e => this.console.push(`error: [${who}] pageerror ${e.message.slice(0, 300)}`));
    page.on('console', m => { if (m.type() === 'error') this.console.push(`error: [${who}] ${m.text().slice(0, 300)}`); });
    page.on('response', r => { if (r.status() >= 400) this.failed.push(`[${who}] ${r.status()} ${r.request().method()} ${new URL(r.url()).pathname}`); });
    page.on('dialog', d => d.accept());
    return { account, label: who, context, page, key: account && account.api_key };
  }
  // step(sim, name, page, fn): sim is the Linear sub-issue this step belongs to.
  async step(sim, name, page, fn) {
    const r = { sim, name, ok: true, notes: [] };
    this.results.push(r);
    const mark = { c: this.console.length, f: this.failed.length };
    const ctx = { fail: n => { r.ok = false; r.notes.push(n); }, note: n => r.notes.push(n) };
    try { await fn(ctx); } catch (e) { r.ok = false; r.notes.push('threw: ' + String(e.message || e).split('\n')[0].slice(0, 400)); }
    r.console = this.console.slice(mark.c);
    r.failedRequests = this.failed.slice(mark.f);
    if (!r.ok && page) {
      r.screenshot = `${this.out}/${this.area}-${this.results.length}.png`;
      await page.screenshot({ path: r.screenshot, fullPage: true }).catch(() => {});
    }
    console.log(`${r.ok ? 'pass' : 'FAIL'} ${sim} | ${name}${r.ok ? '' : ' | ' + r.notes.join(' ; ')}${r.failedRequests.length ? ' | ' + r.failedRequests.join(' ; ') : ''}`);
    return r.ok;
  }
  async done() {
    for (const b of this.browsers) await b.close().catch(() => {});
    const file = `${this.out}/${this.area}.json`;
    fs.writeFileSync(file, JSON.stringify({ area: this.area, when: new Date().toISOString(), results: this.results }, null, 2));
    const bad = this.results.filter(r => !r.ok).length;
    console.log(`\n${this.area}: ${this.results.length - bad} passed, ${bad} failed -> ${file}`);
  }
}

// First visits open a walkthrough dialog; a person reads it and dismisses it.
export async function dismissTours(page) {
  for (let i = 0; i < 4; i++) {
    const d = page.locator('button:visible', { hasText: /^\s*Dismiss\s*$/ }).first();
    try { await d.waitFor({ state: 'visible', timeout: i ? 600 : 1500 }); } catch { break; }
    await d.click(); await page.waitForTimeout(300);
  }
}
export async function tab(page, name) {
  await dismissTours(page);
  await page.locator('#tab-' + name).click();
  await page.waitForTimeout(500);
  await dismissTours(page);
}
export async function visibleError(page) {
  const t = await page.locator('[role=alert]:visible, .err:visible').allInnerTexts().catch(() => []);
  return t.map(s => s.trim()).filter(Boolean).join(' / ');
}
export async function goto(page, path) {
  await page.goto(path.startsWith('http') ? path : SITE + path, { waitUntil: 'domcontentloaded' });
  await page.waitForLoadState('networkidle', { timeout: 15000 }).catch(() => {});
  await dismissTours(page);
}

// REST, for setup shortcuts and cleanup only (journeys under test use the page).
export async function api(key, method, path, body) {
  const r = await fetch(SITE + path, { method, headers: { 'X-API-Key': key, 'Content-Type': 'application/json' }, body: body == null ? undefined : JSON.stringify(body) });
  let data = null; try { data = await r.json(); } catch (e) {}
  return { status: r.status, data };
}

// Create an event through the real form; returns its slug.
export async function createEventViaForm(page, { slug, title, email = 'e2e@example.com' }) {
  await goto(page, '/events/new');
  await page.locator('#new-title').fill(title);
  await page.locator('#new-slug').fill(slug);
  await page.locator('#new-organiser').fill('E2E Organiser');
  await page.locator('#new-org').fill('Simple Hack e2e');
  await page.locator('#new-email').fill(email);
  await page.locator('#new-purpose').fill('Automated end-to-end test event. Safe to delete.');
  const day = n => new Date(Date.now() + n * 864e5).toISOString().slice(0, 10);
  await page.locator('#new-start').fill(day(7));
  await page.locator('#new-end').fill(day(9));
  await page.locator('#new-n').fill('50');
  await page.locator('#new-submit').click();
  await page.waitForURL(u => u.pathname.includes('/e/' + slug), { timeout: 20000 });
  await dismissTours(page);
  return slug;
}

// Cleanup: delete an e2e- event through the API with the organiser's key.
export async function deleteEvent(key, slug) {
  if (!slug || !slug.startsWith('e2e-')) throw new Error('refusing to delete a non-e2e event: ' + slug);
  let r = await api(key, 'DELETE', `/v1/hack/events/${slug}`);
  if (r.status === 409 || r.status === 400) r = await api(key, 'DELETE', `/v1/hack/events/${slug}`, { confirm: slug });
  return r.status;
}
