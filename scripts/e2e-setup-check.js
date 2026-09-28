#!/usr/bin/env node
// Browser end-to-end check of the setup helper's "Check my choices"
// (static/setup/setup.js → POST /v1/setup/check) against a server whose model
// backend is the fake sidecar in scripts/e2e-setup-check-sidecar.py.
//
// Proves: ?product= preselects; the check request carries only changed
// numbers/durations/switches/rates (no hostnames, emails or ids); findings the
// server drops (an out-of-range suggestion) never show; Apply sets the value
// and the files follow it, Ignore keeps the visitor's; the same choices are
// not checked twice; no backend (404) and Skip both show the files with
// "Check skipped."; nothing changed means no request at all. Screenshots of
// the check step at 390 and 1280 px go to <shots-dir>.
//
// Needs Playwright (NODE_PATH pointing at a node_modules that has it) and a
// Chromium. Start the fake sidecar and a server with
//   LLM_API_KEY=fake LLM_BASE_URL=http://127.0.0.1:<sidecar port>/v1 PUBLIC_BASE_URL=<base>
// then: NODE_PATH=/opt/pw/node_modules node scripts/e2e-setup-check.js <base> <shots-dir>
const { chromium } = require('playwright');
const base = process.argv[2], shots = process.argv[3];
const assert = (c, m) => { if (!c) { console.error('FAIL: ' + m); process.exit(1); } else console.log('ok: ' + m); };

async function fillIf(page, name, value) {
  const f = page.locator('#f-' + name);
  if (await f.count()) { await f.fill(value); await f.dispatchEvent('input'); return true; }
  return false;
}

async function enterpriseToCheck(page, width) {
  await page.setViewportSize({ width, height: 900 });
  await page.goto(base + '/setup?product=enterprise');
  assert(await page.locator('input[name=product][value=ent]').isChecked(), 'product=enterprise preselects Enterprise');
  await page.locator('label.choice:has(input[value=advanced])').click();
  await page.getByRole('button', { name: 'Next' }).click();
  await page.fill('#f-host', 'sites.example.com');
  await page.fill('#f-admins', 'platform@example.com');
  await page.fill('#f-clientId', 'client-123');
  await page.fill('#f-issuer', 'https://acme.okta.com');
  await page.fill('#f-issuerName', 'internal-ca');
  await page.fill('#f-bucketName', 'sh-sites');
  await page.fill('#f-dbHost', 'db.example.com');
  await page.getByRole('button', { name: 'Next: every setting' }).click();
  const want = { MAX_ARCHIVE_BYTES: '524288000', UPLOAD_CONCURRENCY: '8', SESSION_TTL: '24h' };
  for (let i = 0; i < 20; i++) {
    for (const [k, v] of Object.entries(want)) await fillIf(page, k, v);
    const show = page.getByRole('button', { name: 'Show my files' });
    if (await show.count()) { await show.click(); break; }
    await page.locator('.nav .btn.solid').click();
  }
}

(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROMIUM || '/usr/local/bin/chromium' });
  // 1. Enterprise, with findings, at both widths.
  for (const width of [1280, 390]) {
    const page = await browser.newPage();
    let sent = null;
    page.on('request', r => { if (r.url().endsWith('/v1/setup/check')) sent = r.postData(); });
    await enterpriseToCheck(page, width);
    await page.waitForSelector('.finding');
    assert(sent && !/example\.com|okta|client-123|internal-ca|sh-sites/.test(sent), 'check request carries no hostnames, emails or ids: ' + sent);
    assert(JSON.parse(sent).product === 'enterprise', 'check request names the product');
    const n = await page.locator('.finding').count();
    assert(n === 3, 'three findings shown (the out-of-range suggestion was dropped): ' + n);
    await page.screenshot({ path: `${shots}/setup-check-${width}.png`, fullPage: true });
    if (width === 1280) {
      await page.locator('#finding-0').getByRole('button', { name: 'Apply' }).click();
      assert(await page.locator('#finding-0 .decided').innerText() === 'Applied', 'Apply marks the finding applied');
      assert(await page.locator('#finding-1').getByRole('button', { name: 'Apply' }).count() === 0, 'a finding without a suggestion has no Apply');
      await page.locator('#finding-2').getByRole('button', { name: 'Ignore' }).click();
      await page.screenshot({ path: `${shots}/setup-check-decided-${width}.png`, fullPage: true });
      await page.getByRole('button', { name: 'Show my files' }).click();
      const cfg = await page.locator('pre').first().innerText();
      assert(!/UPLOAD_CONCURRENCY/.test(cfg), 'applied suggestion (back to the default 2) left UPLOAD_CONCURRENCY out of config.env');
      assert(/SESSION_TTL=24h/.test(cfg), 'ignored suggestion kept SESSION_TTL=24h');
      assert(/Checked: 1 suggestion applied\./.test(await page.locator('.check-note').innerText()), 'output notes the check');
      await page.screenshot({ path: `${shots}/setup-output-after-check-${width}.png`, fullPage: true });
      // Back and forward with the same choices: no second check.
      let again = 0;
      page.on('request', r => { if (r.url().endsWith('/v1/setup/check')) again++; });
      await page.getByRole('button', { name: 'Back' }).click();
      await page.getByRole('button', { name: 'Show my files' }).click();
      await page.waitForSelector('pre');
      assert(again === 0, 'unchanged choices are not checked twice');
    }
    await page.close();
  }
  // 2. No model backend (404): the files appear with "Check skipped."
  const page = await browser.newPage();
  await page.setViewportSize({ width: 390, height: 900 });
  await page.route('**/v1/setup/check', r => r.fulfill({ status: 404, body: '404 page not found' }));
  await page.goto(base + '/setup?product=small-box');
  assert(await page.locator('input[name=product][value=small]').isChecked(), 'product=small-box preselects Small box');
  await page.locator('label.choice:has(input[value=advanced])').click();
  await page.getByRole('button', { name: 'Next' }).click();
  await page.fill('#f-domain', 'hack.example.com');
  await page.getByRole('button', { name: 'Next: every setting' }).click();
  for (let i = 0; i < 20; i++) {
    await fillIf(page, 'MAX_ARCHIVE_MB', '1000');
    const show = page.getByRole('button', { name: 'Show my files' });
    if (await show.count()) { await show.click(); break; }
    await page.locator('.nav .btn.solid').click();
  }
  await page.waitForSelector('pre');
  assert((await page.locator('.check-note').innerText()) === 'Check skipped.', 'no backend: files shown, "Check skipped."');
  // 3. Basic mode with nothing changed: no check at all.
  let calls = 0;
  const p2 = await browser.newPage();
  p2.on('request', r => { if (r.url().endsWith('/v1/setup/check')) calls++; });
  await p2.goto(base + '/setup');
  await p2.getByRole('button', { name: 'Next' }).click();
  await p2.fill('#f-domain', 'hack.example.com');
  await p2.getByRole('button', { name: 'Show my files' }).click();
  await p2.waitForSelector('pre');
  assert(calls === 0 && await p2.locator('.check-note').count() === 0, 'nothing changed: no check request, no note');
  // 4. Skip while checking (a slow backend).
  const p3 = await browser.newPage();
  await p3.route('**/v1/setup/check', () => {});
  await p3.goto(base + '/setup?product=small-box');
  await p3.locator('label.choice:has(input[value=advanced])').click();
  await p3.getByRole('button', { name: 'Next' }).click();
  await p3.fill('#f-domain', 'hack.example.com');
  await p3.getByRole('button', { name: 'Next: every setting' }).click();
  for (let i = 0; i < 20; i++) {
    await fillIf(p3, 'KEEP_VERSIONS', '5');
    const show = p3.getByRole('button', { name: 'Show my files' });
    if (await show.count()) { await show.click(); break; }
    await p3.locator('.nav .btn.solid').click();
  }
  await p3.waitForSelector('.checking');
  await p3.setViewportSize({ width: 390, height: 700 });
  await p3.screenshot({ path: `${shots}/setup-checking-390.png` });
  await p3.getByRole('button', { name: 'Skip the check' }).click();
  await p3.waitForSelector('pre');
  assert((await p3.locator('.check-note').innerText()) === 'Check skipped.', 'Skip the check shows the files');
  // 5. Enterprise basics on UpCloud: the region is asked (never preset), the
  // Postgres fields name the public- host and port 11569, and the ingress
  // pod range lands in config.env.
  const p4 = await browser.newPage();
  await p4.setViewportSize({ width: 390, height: 900 });
  await p4.goto(base + '/setup?product=enterprise');
  await p4.getByRole('button', { name: 'Next' }).click();
  await p4.fill('#f-host', 'sites.example.com');
  await p4.fill('#f-admins', 'platform@example.com');
  await p4.fill('#f-clientId', 'client-123');
  await p4.fill('#f-issuer', 'https://acme.okta.com');
  await p4.selectOption('#f-idp', 'entra');
  assert(await p4.inputValue('#f-issuer') === 'https://acme.okta.com', 'another provider keeps a typed issuer (no template over it)');
  await p4.selectOption('#f-idp', 'okta');
  await p4.fill('#f-issuerName', 'internal-ca');
  assert(await p4.inputValue('#f-dbPort') === '5432', 'Postgres port starts at 5432');
  await p4.selectOption('#f-bucket', 'upcloud');
  assert(await p4.inputValue('#f-region') === '', 'UpCloud: no preset region');
  assert(await p4.inputValue('#f-dbPort') === '11569', 'UpCloud: the Postgres port is filled in as 11569');
  assert(/public-/.test(await p4.locator('#f-dbHost-h').innerText()) && /11569/.test(await p4.locator('#f-dbPort-h').innerText()), 'UpCloud: Postgres hints');
  assert(/192\.168\.0\.0\/16/.test(await p4.locator('#f-proxies-h').innerText()), 'UpCloud: pod range hint');
  await p4.fill('#f-endpoint', 'https://abc12.upcloudobjects.com');
  await p4.fill('#f-bucketName', 'sh-sites');
  await p4.fill('#f-dbHost', 'public-sh-abc.db.upclouddatabases.com');
  await p4.fill('#f-proxies', 'not a range');
  await p4.getByRole('button', { name: 'Show my files' }).click();
  assert(await p4.locator('#f-region.bad').count() === 0 && await p4.locator('#f-proxies.bad').count() === 1, 'an empty region is not an error; a bad range is refused');
  await p4.fill('#f-region', 'europe-2');
  await p4.fill('#f-proxies', '192.168.0.0/16');
  await p4.screenshot({ path: `${shots}/setup-ent-upcloud-390.png`, fullPage: true });
  await p4.getByRole('button', { name: 'Show my files' }).click();
  // The changed port is a number the check looks at: past its findings.
  await p4.waitForSelector('pre, .finding, .check-note');
  if (!(await p4.locator('pre').count())) await p4.getByRole('button', { name: 'Show my files' }).click();
  await p4.waitForSelector('pre');
  const entCfg = await p4.locator('pre').first().innerText();
  assert(/^TRUSTED_PROXY_CIDRS=192\.168\.0\.0\/16$/m.test(entCfg) && /^BACKUP_STORAGE_REGION=europe-2$/m.test(entCfg) && /^DB_PORT=11569$/m.test(entCfg), 'config.env carries the pod range, region and port');
  assert(/^# Complete as it is: anything not listed keeps its default/m.test(entCfg), 'config.env says it is complete');
  for (const line of ['PORT=8080', 'HTTPS_REDIRECT_PORT=8081', 'OIDC_SCOPES=openid email profile', 'SESSION_TTL=8h', 'SESSION_IDLE=30m', 'DB_SSLMODE=verify-full', 'BACKUP_STORAGE_PREFIX=backups/', 'BACKUP_SSE=AES256'])
    assert(new RegExp('^' + line.replace(/[.*+?^${}()|[\]\\/]/g, '\\$&') + '$', 'm').test(entCfg), 'config.env has the example\'s ' + line);
  const entAgent = await p4.locator('#agent pre').last().innerText();
  assert(/HUMAN STEP D/.test(entAgent) && /make smoke BASE=https:\/\/sites\.example\.com/.test(entAgent) && /CURL_CA_BUNDLE/.test(entAgent) && /internal-ca/.test(entAgent), 'the handoff ends with HUMAN STEP D, make smoke and the internal-CA note');
  await browser.close();
  console.log('ALL OK');
})().catch(e => { console.error(e); process.exit(1); });
