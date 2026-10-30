#!/usr/bin/env node
// Browser proof for the owner dashboard fast path (2026-10-30), Chromium and
// WebKit, against a local server. Needs: an account with handle "owner" and
// 174 sites (some public), its key in OWNER_KEY_FILE; a second account with
// handle "bee-01", its key in OTHER_KEY_FILE. PLAYWRIGHT points at playwright's
// index.mjs. Checks: no public flash for the owner, the public view for anyone
// else (even with a stale handle hint), owner blocks still load, search works,
// sign-out forgets the hint.
import fs from 'node:fs';
const { chromium, webkit } = await import(process.env.PLAYWRIGHT || 'playwright');
const BASE = process.env.BASE || 'http://localhost:18490';
const OWNER = fs.readFileSync(process.env.OWNER_KEY_FILE, 'utf8').trim();
const OTHER = fs.readFileSync(process.env.OTHER_KEY_FILE, 'utf8').trim();
let fails = 0;
const ok = (c, m) => { console.log((c ? 'PASS ' : 'FAIL ') + m); if (!c) fails++; };
for (const [bn, bt] of [['chromium', chromium], ['webkit', webkit]]) {
  const b = await bt.launch();
  async function ctxWith(store) {
    const ctx = await b.newContext({ viewport: { width: 1280, height: 900 } });
    await ctx.addInitScript(s => { if (!sessionStorage.getItem('__s')) { sessionStorage.setItem('__s', '1'); for (const k in s) localStorage.setItem(k, s[k]); } }, store);
    return ctx;
  }
  // 1. another account's key with a stale owner hint: ends on the public view, hint corrected
  let ctx = await ctxWith({ apiKey: OTHER, 'sh-owner-handle': 'owner' });
  let p = await ctx.newPage();
  await p.goto(BASE + '/owner'); await p.waitForTimeout(1500);
  ok(await p.locator('#gallery .g-card').first().isVisible(), `${bn}: other account with stale hint sees the public gallery`);
  ok(!(await p.locator('#owner-view').isVisible()), `${bn}: other account never gets the owner view`);
  ok(!(await p.locator('#owner-pending').isVisible()), `${bn}: loading line gone for other account`);
  ok(await p.evaluate(() => localStorage.getItem('sh-owner-handle')) === 'bee-01', `${bn}: hint corrected to the signed-in handle`);
  await ctx.close();
  // 2. signed out visitor: public view, no loading line
  ctx = await ctxWith({ 'sh-owner-handle': 'owner' });
  p = await ctx.newPage();
  await p.goto(BASE + '/owner'); await p.waitForTimeout(800);
  ok(await p.locator('#gallery .g-card').first().isVisible() && !(await p.locator('#owner-pending').isVisible()), `${bn}: no key + leftover hint shows the public view`);
  ok(await p.locator('#dash-link').getAttribute('href') === '/dashboard' || (await p.locator('#dash-link').getAttribute('href')).endsWith('/dashboard'), `${bn}: signed out, header link stays /dashboard`);
  await ctx.close();
  // 2b. remembered handle no longer exists (renamed elsewhere): /dashboard lands on the real page
  ctx = await ctxWith({ apiKey: OWNER, 'sh-owner-handle': 'ghost-handle-gone' });
  p = await ctx.newPage();
  await p.goto(BASE + '/dashboard');
  await p.waitForURL(/\/owner$/, { timeout: 10000 }).catch(() => {});
  ok(new URL(p.url()).pathname === '/owner', `${bn}: dead hint recovers to the owner page (${new URL(p.url()).pathname})`);
  ok(await p.evaluate(() => localStorage.getItem('sh-owner-handle')) === 'owner', `${bn}: dead hint replaced`);
  await ctx.close();
  // 3. owner: owner blocks still load from the shared calls
  ctx = await ctxWith({ apiKey: OWNER });
  p = await ctx.newPage();
  await p.goto(BASE + '/dashboard');
  await p.waitForURL(/\/owner$/); await p.waitForSelector('#inventory .inv-row'); await p.waitForTimeout(1200);
  ok(await p.locator('#inventory .inv-row').count() === 174, `${bn}: owner sees 174 rows`);
  ok(await p.evaluate(() => localStorage.getItem('sh-owner-handle')) === 'owner', `${bn}: hint saved after /v1/me`);
  ok((await p.locator('#home-site option').count()) >= 175, `${bn}: home-page picker lists own sites (${await p.locator('#home-site option').count()})`);
  ok((await p.locator('#dash-link').getAttribute('href')).endsWith('/owner'), `${bn}: header Your sites links to /owner`);
  ok((await p.locator('#data-delete-handle').textContent()) === 'owner', `${bn}: Your data block filled`);
  ok((await p.locator('#signin-email').textContent()) === 'admin', `${bn}: Sign-in block filled`);
  // search still works
  await p.fill('#inv-search', 'site-017');
  await p.waitForTimeout(300);
  ok(await p.locator('#inventory .inv-row:visible').count() === 1, `${bn}: search filters to one row`);
  // 4. sign out clears the hint
  await p.evaluate(() => { localStorage.removeItem('apiKey'); });
  await p.evaluate(() => window.shSignOut());
  await p.waitForTimeout(1200);
  ok(await p.evaluate(() => localStorage.getItem('sh-owner-handle')) === null, `${bn}: sign-out forgets the hint`);
  await ctx.close();
  await b.close();
}
console.log(fails ? `${fails} FAILED` : 'ALL PASS');
process.exit(fails ? 1 : 0);
