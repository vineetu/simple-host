#!/usr/bin/env node
// Browser check that the owner page asks its questions in the page, never with
// the browser's native confirm/alert/prompt (an AI browser agent cannot see or
// press those, so the page hangs). Any native dialog fails the run.
//
// On a THROWAWAY account (the last step deletes it):
//   1. Your address → Change → Save: an in-page dialog; Escape cancels and no
//      PATCH is sent; "Yes, change my address" sends it and the page moves.
//   2. A site's "Take offline": in-page confirm, then the PATCH; put back online.
//   3. Keys → "Sign out everywhere": in-page confirm, then a new key.
//   1b. A taken address (TAKEN) is refused in the page with the reason; no
//      question, no PATCH, still signed in; PATCH /v1/me with it is a 409.
//   4. The dialog fits at 390 px and follows the light and dark themes.
//   5. Your data → Delete my account: typed confirmation, then the in-page
//      "Your account and all its data were deleted" notice; OK signs out.
//
//   NODE_PATH=/opt/pw/node_modules node scripts/e2e-dialogs.js <base> <api-key> <handle> <new-handle> [site]
// <site> is one of the account's sites (default: the first). With KEEP=1 step 5
// is skipped and the account kept.
const { chromium } = require('playwright');
const assert = (c, m) => { if (!c) { console.error('FAIL: ' + m); process.exitCode = 1; } else console.log('ok: ' + m); };

(async () => {
  const [base, key0, handle, newHandle, siteArg] = process.argv.slice(2);
  if (!base || !key0 || !handle || !newHandle) {
    console.error('usage: e2e-dialogs.js <base> <api-key> <handle> <new-handle> [site]');
    process.exit(2);
  }
  const b = await chromium.launch();
  const ctx = await b.newContext({ viewport: { width: 1280, height: 900 } });
  const origin = new URL(base).origin;
  await ctx.addInitScript(([k, o]) => {
    if (location.origin === o && !sessionStorage.getItem('e2e-seeded')) {
      localStorage.setItem('apiKey', k);
      sessionStorage.setItem('e2e-seeded', '1');
    }
  }, [key0, origin]);
  const p = await ctx.newPage();
  const natives = [];
  p.on('dialog', async (d) => { natives.push(d.type() + ': ' + d.message()); await d.dismiss().catch(() => {}); });
  const errs = []; p.on('pageerror', (e) => errs.push(e.message));
  const sent = []; p.on('request', (r) => { if (r.url().includes('/v1/')) sent.push(r.method() + ' ' + new URL(r.url()).pathname); });
  const dlg = p.locator('dialog.sh-dlg');
  const noNative = (what) => assert(natives.length === 0, `${what}: no native dialog${natives.length ? ' (' + natives.join(' | ') + ')' : ''}`);

  // ---- 1. Change the address --------------------------------------------
  await p.goto(base + '/' + handle, { waitUntil: 'load' });
  // Voice input is decided by the page data: loading sends nothing to /v1/transcribe.
  await p.waitForTimeout(1500);
  assert(!sent.some((s) => s === 'POST /v1/transcribe'), 'page load sends no POST /v1/transcribe');
  await p.locator('#addr-change').click({ timeout: 15000 });
  await p.locator('#addr-input').fill(newHandle);
  await p.locator('#addr-save').click();
  await dlg.waitFor({ state: 'visible', timeout: 5000 });
  const a11y = await dlg.evaluate((d) => ({
    role: d.getAttribute('role'), modal: d.getAttribute('aria-modal'),
    label: (document.getElementById(d.getAttribute('aria-labelledby')) || {}).textContent || '',
    focusInside: d.contains(document.activeElement),
  }));
  assert(a11y.role === 'dialog' && a11y.modal === 'true', 'dialog has role=dialog and aria-modal');
  assert(a11y.label.indexOf('Change your address to ' + newHandle) === 0, `dialog is labelled by its question (${a11y.label})`);
  assert(a11y.focusInside, 'focus moves into the dialog');
  assert(await dlg.getByRole('button', { name: 'Yes, change my address' }).isVisible(), '"Yes, change my address" button');
  assert(await dlg.getByRole('button', { name: 'Cancel' }).isVisible(), '"Cancel" button');
  await p.keyboard.press('Escape');
  await dlg.waitFor({ state: 'hidden', timeout: 3000 });
  await p.waitForTimeout(500);
  assert(!sent.includes('PATCH /v1/me'), 'Escape cancels: no PATCH /v1/me sent');
  assert(await p.evaluate((k) => localStorage.getItem('apiKey') === k, key0), 'cancelling keeps this browser signed in');

  // A taken or reserved address (TAKEN, default "admin") is refused at once,
  // in the page, before any question; the page and the sign-in stay.
  const taken = process.env.TAKEN || 'admin';
  await p.locator('#addr-input').fill(taken);
  await p.waitForFunction(() => { const m = document.getElementById('addr-msg'); return m && !m.hidden && m.classList.contains('bad'); }, null, { timeout: 8000 });
  const whyTyping = await p.locator('#addr-msg').textContent();
  assert(/^That address is (taken|reserved): /.test(whyTyping), `typing a taken address says why (${whyTyping})`);
  await p.locator('#addr-save').click();
  await p.waitForTimeout(1200);
  assert(!(await dlg.isVisible()) && !sent.includes('PATCH /v1/me'), 'saving it asks nothing and sends no PATCH');
  assert(await p.locator('#addr-current').isVisible() && await p.evaluate((k) => localStorage.getItem('apiKey') === k, key0), 'the page and the sign-in stay');
  const direct = await p.request.patch(base + '/v1/me', { headers: { 'X-API-Key': key0, 'Content-Type': 'application/json' }, data: { handle: taken } });
  const dj = await direct.json();
  assert(direct.status() === 409 && /^handle_(taken|reserved)$/.test(dj.code) && dj.error.indexOf(taken + '.') > 0, `PATCH /v1/me with it: 409 ${dj.code} "${dj.error}"`);

  await p.locator('#addr-input').fill(newHandle);
  await p.waitForFunction(() => { const m = document.getElementById('addr-msg'); return m && !m.hidden && !m.classList.contains('bad'); }, null, { timeout: 8000 });
  await p.locator('#addr-save').click();
  await dlg.waitFor({ state: 'visible', timeout: 5000 });
  // The page moves to <main site>/<new handle>; a local server's main site
  // may be another origin, so follow the path on <base>.
  const moved = p.waitForRequest((r) => r.isNavigationRequest() && new URL(r.url()).pathname === '/' + newHandle, { timeout: 20000 });
  await dlg.getByRole('button', { name: 'Yes, change my address' }).click();
  const nav = await moved.catch(() => null);
  assert(sent.includes('PATCH /v1/me'), 'PATCH /v1/me sent after "Yes, change my address"');
  assert(!!nav, 'the page moves to the new address /' + newHandle);
  await p.goto(base + '/' + newHandle, { waitUntil: 'load' });
  noNative('change address');

  // ---- 2. Take a site offline --------------------------------------------
  await p.waitForLoadState('load');
  const off = siteArg ? p.locator(`[data-act="toggleOffline"][data-name="${siteArg}"]`) : p.locator('[data-act="toggleOffline"]').first();
  await off.waitFor({ state: 'attached', timeout: 15000 });
  const site = await off.getAttribute('data-name');
  if (!(await off.isVisible())) {
    // Row actions sit behind the row's menu on some layouts.
    const more = p.locator(`[data-name="${site}"][data-act="more"], [data-more="${site}"]`).first();
    if (await more.count()) await more.click();
  }
  await off.click();
  await dlg.waitFor({ state: 'visible', timeout: 5000 });
  assert((await dlg.textContent()).includes('Take "' + site + '" offline?'), 'offline dialog asks with the same words');
  const before = sent.length;
  await dlg.getByRole('button', { name: 'Cancel' }).click();
  await p.waitForTimeout(500);
  assert(!sent.slice(before).some((s) => s.startsWith('PATCH /v1/sites/')), 'Cancel sends nothing');
  await off.click();
  await dlg.getByRole('button', { name: 'Take it offline' }).click();
  await p.waitForResponse((r) => r.request().method() === 'PATCH' && r.url().includes('/v1/sites/'), { timeout: 10000 }).catch(() => {});
  await p.waitForTimeout(800);
  assert(sent.slice(before).some((s) => s.startsWith('PATCH /v1/sites/')), 'Take it offline sends the PATCH');
  const back = p.locator(`[data-act="toggleOffline"][data-name="${site}"]`);
  await p.waitForFunction((n) => { const e = document.querySelector(`[data-act="toggleOffline"][data-name="${n}"]`); return e && /online/i.test(e.textContent); }, site, { timeout: 10000 }).catch(() => {});
  assert(/Put back online/.test(await back.textContent()), 'site shows "Put back online"');
  await back.click(); // no question to put it back
  await p.waitForTimeout(800);
  noNative('take offline');

  // ---- 3. Sign out everywhere --------------------------------------------
  const keyBefore = await p.evaluate(() => localStorage.getItem('apiKey'));
  await p.locator('#keys-signout-all').scrollIntoViewIfNeeded();
  await p.locator('#keys-signout-all').click();
  await dlg.waitFor({ state: 'visible', timeout: 5000 });
  assert((await dlg.textContent()).includes('Sign out everywhere?'), 'sign-out-everywhere dialog asks with the same words');
  await dlg.getByRole('button', { name: 'Sign out everywhere' }).click();
  await p.waitForFunction((k) => localStorage.getItem('apiKey') && localStorage.getItem('apiKey') !== k, keyBefore, { timeout: 10000 }).catch(() => {});
  const key1 = await p.evaluate(() => localStorage.getItem('apiKey'));
  assert(key1 && key1 !== keyBefore, 'a new key replaced the old one');
  noNative('sign out everywhere');

  // ---- 4. Fits at 390 px, both themes ------------------------------------
  for (const theme of ['light', 'dark']) {
    await p.setViewportSize({ width: 390, height: 800 });
    await p.evaluate((t) => window.shTheme && window.shTheme.set(t), theme);
    p.evaluate(() => window.shConfirm({ title: 'Delete site "a-rather-long-site-name-here"?', body: 'It goes offline now. For 7 days you can restore it.', confirmLabel: 'Delete site', danger: true }));
    await dlg.waitFor({ state: 'visible' });
    const box = await dlg.boundingBox();
    const st = await dlg.evaluate((d) => ({ bg: getComputedStyle(d).backgroundColor, sw: document.documentElement.scrollWidth, over: d.scrollWidth > d.clientWidth }));
    const m = st.bg.match(/\d+/g).map(Number);
    const dark = (0.2126 * m[0] + 0.7152 * m[1] + 0.0722 * m[2]) / 255 < 0.5;
    assert(box && box.x >= 0 && box.x + box.width <= 390 && !st.over && st.sw <= 390, `390px ${theme}: dialog fits (${box && Math.round(box.width)}px wide)`);
    assert(dark === (theme === 'dark'), `${theme} theme: dialog background follows (${st.bg})`);
    if (process.env.SHOTS) await p.screenshot({ path: `${process.env.SHOTS}/dialog-390-${theme}.png` });
    await p.keyboard.press('Escape');
    await dlg.waitFor({ state: 'hidden' });
  }
  await p.evaluate(() => window.shTheme && window.shTheme.set('system'));
  await p.setViewportSize({ width: 1280, height: 900 });

  // ---- 5. Delete the account (GDPR) --------------------------------------
  if (!process.env.KEEP) {
    await p.locator('#data-delete-open').scrollIntoViewIfNeeded();
    await p.locator('#data-delete-open').click();
    const word = (await p.locator('#data-delete-word').textContent()).trim();
    await p.locator('#data-delete-input').fill(word);
    await p.locator('#data-delete-go').click();
    await dlg.waitFor({ state: 'visible', timeout: 15000 });
    assert((await dlg.textContent()).includes('Your account and all its data were deleted.'), 'account-deleted notice is in the page');
    assert(sent.includes('DELETE /v1/me'), 'DELETE /v1/me sent');
    await Promise.all([
      p.waitForURL((u) => u.pathname === '/', { timeout: 15000 }),
      dlg.getByRole('button', { name: 'OK' }).click(),
    ]);
    assert(await p.evaluate(() => !localStorage.getItem('apiKey')), 'signed out after OK');
    noNative('delete account');
    const gone = await (await p.request.get(base + '/v1/me', { headers: { 'X-API-Key': key1 } })).status();
    assert(gone === 401, `the account's key no longer works (${gone})`);
  }
  assert(errs.length === 0, 'no script errors ' + errs.join('; '));
  await b.close();
})().catch((e) => { console.error('FAIL: ' + e.stack); process.exit(1); });
