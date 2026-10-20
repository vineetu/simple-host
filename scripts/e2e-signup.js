#!/usr/bin/env node
// Browser check of choosing the address at sign-up, as a headless AI agent
// would drive it (no native dialog may appear; any one fails the run):
//   1. /dashboard → email → code → "Pick your address" in the sign-in card,
//      prefilled with a suggestion, checked while typing.
//   2. A taken address (TAKEN) shows why and Create my account stays disabled.
//   3. The chosen address (HANDLE) is free → Create my account → signed in at /<HANDLE>.
// Prints the new account's API key as `KEY=<key>` for the next step
// (scripts/e2e-dialogs.js changes the address, then deletes the account).
//
//   CODE_CMD='<prints the 6-digit code for $EMAIL>' TAKEN=<taken> \
//   NODE_PATH=/opt/pw/node_modules node scripts/e2e-signup.js <base> <email> <handle>
const { chromium } = require('playwright');
const { execSync } = require('child_process');
const assert = (c, m) => { if (!c) { console.error('FAIL: ' + m); process.exitCode = 1; } else console.log('ok: ' + m); };

(async () => {
  const [base, email, handle] = process.argv.slice(2);
  const taken = process.env.TAKEN || 'admin';
  const b = await chromium.launch();
  const ctx = await b.newContext({ viewport: { width: 390, height: 844 } });
  const p = await ctx.newPage();
  const natives = [];
  p.on('dialog', async (d) => { natives.push(d.type() + ': ' + d.message()); await d.dismiss().catch(() => {}); });
  const errs = []; p.on('pageerror', (e) => errs.push(e.message));
  const dlg = p.locator('dialog.sh-dlg');

  await p.goto(base + '/dashboard', { waitUntil: 'load' });
  await p.locator('[data-sh-signin="email"]').fill(email);
  const sentAt = Date.now();
  await p.locator('[data-sh-signin="continue"]').click();
  await p.locator('[data-sh-signin="code"]').waitFor({ state: 'visible', timeout: 15000 });
  let code = '';
  for (let i = 0; i < 20 && !/^\d{6}$/.test(code); i++) {
    await p.waitForTimeout(3000);
    try { code = execSync(process.env.CODE_CMD, { env: Object.assign({}, process.env, { EMAIL: email, SINCE: String(sentAt) }) }).toString().trim(); } catch (e) { code = ''; }
  }
  assert(/^\d{6}$/.test(code), 'got the emailed code');
  // Filling six digits submits. A second click would send the code twice.
  await p.locator('[data-sh-signin="code"]').fill(code);

  await p.getByRole('heading', { name: 'Pick your address' }).waitFor({ timeout: 15000 });
  const input = p.locator('[data-sh-signin="handle"]');
  const suggested = await input.inputValue();
  assert(/^[a-z0-9-]+$/.test(suggested), `prefilled with a suggestion (${suggested})`);
  await p.waitForFunction(() => { const m = document.querySelector('.sh-si-ok'); return m && m.textContent === 'Available'; }, null, { timeout: 8000 });
  assert((await p.locator('.sh-si-ok').textContent()) === 'Available', 'the suggestion is checked inline');

  await input.fill(taken);
  await p.waitForFunction(() => { const m = document.querySelector('.sh-si-badtxt'); return m && /^That address is (taken|reserved)/.test(m.textContent); }, null, { timeout: 8000 });
  const why = await p.locator('.sh-si-badtxt').textContent();
  assert(/^That address is (taken|reserved)/.test(why), `a taken address says why (${why})`);
  assert(await p.getByRole('button', { name: 'Create my account' }).isDisabled(), 'Create my account is disabled for it');

  await input.fill(handle);
  await p.waitForFunction(() => { const m = document.querySelector('.sh-si-ok'); return m && m.textContent === 'Available'; }, null, { timeout: 8000 });
  await Promise.all([
    p.waitForURL((u) => u.pathname === '/' + handle, { timeout: 20000 }),
    p.getByRole('button', { name: 'Create my account' }).click(),
  ]);
  assert(new URL(p.url()).pathname === '/' + handle, 'signed in at /' + handle);
  const key = await p.evaluate(() => localStorage.getItem('apiKey'));
  const me = await (await p.request.get(base + '/v1/me', { headers: { 'X-API-Key': key } })).json();
  assert(me.handle === handle, `the account's address is the chosen one (${me.handle})`);
  assert(await dlg.count() === 0, 'the address is chosen in the card, not a dialog');
  assert(natives.length === 0, 'no native dialog ' + natives.join(' | '));
  assert(errs.length === 0, 'no script errors ' + errs.join('; '));
  if (key) console.log('KEY=' + key);
  await b.close();
})().catch((e) => { console.error('FAIL: ' + e.stack); process.exit(1); });
