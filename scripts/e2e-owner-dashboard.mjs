#!/usr/bin/env node
// Browser proof for the owner dashboard fast path (2026-10-30), Chromium and
// WebKit, against a local server. Needs: an account with handle "owner" and
// 174 sites (some public), its key in OWNER_KEY_FILE; a second account with
// handle "bee-01", its key in OTHER_KEY_FILE. PLAYWRIGHT points at playwright's
// index.mjs. Checks: no public flash for the owner, the public view for anyone
// else (even with a stale handle hint), owner blocks still load, search works,
// sign-out forgets the hint. Also checks the three owner tabs, section hashes,
// query-link priority, section visibility, and phone widths in both themes.
// Run: BASE=http://localhost:18490 OWNER_KEY_FILE=<file> OTHER_KEY_FILE=<file>
//   PLAYWRIGHT=/opt/pw/node_modules/playwright/index.mjs node scripts/e2e-owner-dashboard.mjs
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
    await ctx.addInitScript(() => {
      window.ownerTestTools = {};
      Object.defineProperty(document, 'modelContext', { value: { registerTool(tool) { window.ownerTestTools[tool.name] = tool; } }, configurable: true });
    });
    ctx.setDefaultTimeout(10000);
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
  ok((await p.locator('#signin-email-text').textContent()) === 'Your account has no email address.', `${bn}: Sign-in block filled`);
  const settingsOrder = ['owner-address', 'owner-showcase-curation', 'owner-signin', 'owner-password', 'owner-connections', 'owner-keys', 'owner-notifications', 'owner-families', 'owner-data'];
  ok(await p.locator('.owner-tabs [role="tab"]').count() === 3, `${bn}: three owner tabs`);
  ok(await p.locator('#tab-sites').getAttribute('aria-selected') === 'true', `${bn}: Sites is the default`);
  ok(await p.locator('#owner-sites-view h2').textContent() === 'Your sites', `${bn}: Your sites heading`);
  ok(await p.locator('#owner-tab-sites #owner-sites-view').count() === 1 && await p.locator('#owner-tab-sites #owner-deleted').count() === 1, `${bn}: sites and Recently deleted share Sites`);
  ok(await p.locator('#owner-tab-analytics #owner-analytics-view').count() === 1 && await p.locator('#owner-analytics-view h2').count() === 0, `${bn}: Analytics has its own panel without a repeated heading`);
  ok(JSON.stringify(await p.locator('#owner-tab-settings > .section').evaluateAll(nodes => nodes.map(n => n.id))) === JSON.stringify(settingsOrder), `${bn}: Settings sections in the agreed DOM order`);
  ok(await p.locator('#owner-data > :last-child').getAttribute('id') === 'owner-delete-account', `${bn}: Delete account is last`);
  const ownHidden = await p.locator('#owner-view .section').evaluateAll(nodes => nodes.map(n => [n.id, n.hidden]));
  const historyLength = await p.evaluate(() => history.length);
  await p.click('#tab-analytics');
  ok(await p.locator('#owner-tab-analytics').isVisible() && !(await p.locator('#owner-connections').isVisible()) && !(await p.locator('#owner-sites-view').isVisible()), `${bn}: Analytics hides all sites and account sections`);
  ok(new URL(p.url()).hash === '#analytics' && await p.evaluate(() => history.length) === historyLength, `${bn}: tab click replaces hash without adding history`);
  await p.click('#tab-settings');
  await p.waitForSelector('#owner-keys');
  ok(await p.locator('#owner-connections').isVisible() && !(await p.locator('#owner-sites-view').isVisible()), `${bn}: Settings shows account sections only`);
  ok(JSON.stringify(await p.locator('#owner-view .section').evaluateAll(nodes => nodes.map(n => [n.id, n.hidden]))) === JSON.stringify(ownHidden), `${bn}: tabs preserve each section's own hidden rule`);
  ok(await p.locator('#connections-list').textContent().then(t => t.includes('No apps connected yet.')) && await p.locator('#connections-list a').getAttribute('href') === '/install.html', `${bn}: empty connected apps offers setup`);
  const hashes = { keys: 'owner-keys', 'connected-apps': 'owner-connections', notifications: 'owner-notifications', 'recently-deleted': 'owner-deleted', address: 'owner-address', showcase: 'owner-showcase-curation', 'sign-in': 'owner-signin', password: 'owner-password', 'address-families': 'owner-families', 'your-data': 'owner-data', 'delete-account': 'owner-delete-account' };
  for (const [hash, id] of Object.entries(hashes)) {
    const tab = hash === 'recently-deleted' ? 'sites' : 'settings';
    for (const fragment of [hash, id]) {
      await p.evaluate(h => { location.hash = h; }, fragment);
      await p.waitForFunction(t => document.getElementById('tab-' + t).getAttribute('aria-selected') === 'true', tab);
      ok(await p.locator('#owner-tab-' + tab).isVisible(), `${bn}: #${fragment} opens ${tab}`);
    }
  }
  // Every section element ID routes by its actual containing panel, including
  // sections without a named alias and the onboarding cards.
  const sections = await p.locator('[role="tabpanel"] .section, [role="tabpanel"] .skill-install').evaluateAll(nodes => nodes.map(n => [n.id, n.closest('[role="tabpanel"]').id.replace('owner-tab-', '')]));
  for (const [id, tab] of sections) {
    await p.click(tab === 'sites' ? '#tab-settings' : '#tab-sites');
    await p.evaluate(id => { location.hash = id; }, id);
    await p.waitForFunction(t => document.getElementById('tab-' + t).getAttribute('aria-selected') === 'true', tab);
    ok(new URL(p.url()).hash === '#' + id, `${bn}: #${id} opens ${tab} without replacing the section hash`);
    const section = p.locator('#' + id);
    if (await section.isVisible()) {
      ok(await section.evaluate(n => Math.abs(n.getBoundingClientRect().top) < 5 || window.scrollY + window.innerHeight >= document.documentElement.scrollHeight - 5), `${bn}: #${id} scrolls to its section`);
    }
  }
  // A cold deep link must wait for its section's independent loader.
  await p.route('**/v1/me/keys', async route => { await new Promise(r => setTimeout(r, 500)); await route.continue(); });
  await p.goto(BASE + '/owner#keys');
  await p.reload();
  await p.waitForSelector('#owner-keys');
  await p.waitForTimeout(100);
  ok(await p.locator('#owner-keys').evaluate(n => Math.abs(n.getBoundingClientRect().top) < 5), `${bn}: initial #keys scrolls after its delayed load`);
  await p.unroute('**/v1/me/keys');
  await p.click('#tab-analytics');
  await p.evaluate(() => showNewKey('browser-test-value', 'Test display'));
  ok(await p.locator('#key-shown').isVisible() && await p.locator('#tab-settings').getAttribute('aria-selected') === 'true', `${bn}: showing a new key opens Settings`);
  await p.click('#key-shown-done');
  await p.evaluate(() => {
    window.open = () => null;
    window.ownerTestTools.open_site.execute({ site: 'site-017' });
  });
  ok(await p.locator('#tab-sites').getAttribute('aria-selected') === 'true', `${bn}: WebMCP open_site opens Sites first`);
  ok(!(await p.locator('#owner-deleted').isVisible()), `${bn}: Recently deleted stays hidden when empty`);
  await p.route('**/v1/me/deleted-sites', route => route.fulfill({ json: { sites: [{ name: 'deleted-fixture', deleted_at: '2026-11-05T00:00:00Z', purge_at: '2026-11-12T00:00:00Z' }] } }));
  await p.goto(BASE + '/owner#recently-deleted');
  await p.reload();
  await p.waitForSelector('#deleted-list .conn-row');
  ok(await p.locator('#owner-deleted').isVisible(), `${bn}: Recently deleted shows nonempty items on Sites`);
  await p.click('#tab-settings');
  ok(!(await p.locator('#owner-deleted').isVisible()), `${bn}: nonempty Recently deleted is hidden on Settings`);
  ok(!(await p.locator('#owner-password').isVisible()) && !(await p.locator('#owner-families').isVisible()), `${bn}: unavailable Password and Address families remain hidden`);
  await p.unroute('**/v1/me/deleted-sites');
  for (const theme of ['light', 'dark']) {
    await p.evaluate(t => document.documentElement.setAttribute('data-theme', t), theme);
    for (const width of [320, 390, 479]) {
      await p.setViewportSize({ width, height: 900 });
      const boxes = await p.locator('.owner-tab').evaluateAll(nodes => nodes.map(n => { const r = n.getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width }; }));
      ok(boxes.every(b => Math.abs(b.y - boxes[0].y) < 1 && Math.abs(b.width - boxes[0].width) < 1) && boxes[2].x + boxes[2].width <= width, `${bn}: ${theme} ${width}px tabs share one row equally`);
      ok(await p.locator('#build-with-ai, #connect-ai, #skill-install').evaluateAll(nodes => nodes.every(n => n.closest('[role="tabpanel"]').hidden)), `${bn}: ${theme} ${width}px onboarding cards stay inside hidden Sites`);
      ok(await p.locator('.owner-tabs').evaluate(n => !['sticky', 'fixed'].includes(getComputedStyle(n).position)), `${bn}: ${theme} ${width}px tabs are not sticky`);
    }
  }
  await p.setViewportSize({ width: 1280, height: 900 });
  await p.goto(BASE + '/owner?site=site-017&open=passcode#settings');
  await p.waitForSelector('#pc-dlg[open]');
  ok(await p.locator('#tab-sites').getAttribute('aria-selected') === 'true' && new URL(p.url()).hash === '#sites', `${bn}: passcode link overrides Settings hash`);
  await p.locator('#pc-close').click();
  // Background notification failures neither navigate nor stack a dialog on
  // the passcode controls, regardless of which request finishes first.
  await p.route('**/v1/notifications', route => route.fulfill({ status: 500, json: { error: 'Could not load notifications.' } }));
  await p.goto(BASE + '/owner#analytics');
  await p.reload();
  await p.waitForFunction(() => !document.getElementById('notif-load-error').hidden);
  ok(await p.locator('#tab-analytics').getAttribute('aria-selected') === 'true' && new URL(p.url()).hash === '#analytics' && await p.locator('dialog[open]').count() === 0, `${bn}: notifications failure preserves Analytics and opens no dialog`);
  await p.goto(BASE + '/owner?site=site-017&open=passcode#settings');
  await p.waitForSelector('#pc-dlg[open]');
  await p.waitForFunction(() => !document.getElementById('notif-load-error').hidden);
  ok(await p.locator('#tab-sites').getAttribute('aria-selected') === 'true' && new URL(p.url()).hash === '#sites' && await p.locator('dialog[open]').count() === 1, `${bn}: notifications failure preserves passcode link priority with one dialog`);
  await p.locator('#pc-close').click();
  await p.unroute('**/v1/notifications');
  // Hold connected apps pending to prove there is no premature empty state.
  let releaseConnections;
  const connectionsReady = new Promise(resolve => { releaseConnections = resolve; });
  await p.route('**/v1/me/connections', async route => { await connectionsReady; await route.fulfill({ json: { connections: [] } }); });
  await p.goto(BASE + '/owner#settings');
  await p.reload();
  await p.waitForSelector('#owner-connections');
  ok(!(await p.locator('#connections-list').textContent()).includes('No apps connected yet.'), `${bn}: pending connected apps does not claim an empty list`);
  releaseConnections();
  await p.waitForFunction(() => document.getElementById('connections-list').textContent.includes('No apps connected yet.'));
  ok(await p.locator('#connections-list a').getAttribute('href') === '/install.html', `${bn}: successful empty list offers setup`);
  await p.unroute('**/v1/me/connections');
  for (const failure of ['http', 'network']) {
    await p.route('**/v1/me/connections', route => failure === 'http' ? route.fulfill({ status: 500, json: {} }) : route.abort());
    await p.goto(BASE + '/owner#settings');
    await p.reload();
    await p.waitForFunction(() => document.getElementById('connections-list').textContent.includes('Could not load connected apps.'));
    ok(!(await p.locator('#connections-list').textContent()).includes('No apps connected yet.'), `${bn}: ${failure} failure shows an error instead of an empty list`);
    await p.unroute('**/v1/me/connections');
  }
  // No sites leaves Analytics useful and account sections confined to Settings.
  await p.route('**/v1/sites', route => route.fulfill({ json: [] }));
  await p.goto(BASE + '/owner#analytics');
  await p.reload();
  await p.waitForSelector('#an-body .empty-state');
  ok(await p.locator('#an-body').textContent() === 'No sites yet. Visits show here once a site is live.', `${bn}: Analytics empty state`);
  ok(!(await p.locator('#owner-connections').isVisible()), `${bn}: empty Analytics still hides Settings`);
  await p.unroute('**/v1/sites');
  await p.goto(BASE + '/owner#sites');
  await p.reload();
  await p.waitForSelector('#inventory .inv-row');
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
