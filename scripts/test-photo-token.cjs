// Run against TestServePhotoLinkForBrowser's private fixture file.
// PLAYWRIGHT_DIR=/path/to/playwright node scripts/test-photo-token.cjs fixture.json
const {chromium} = require(process.env.PLAYWRIGHT_DIR || 'playwright');
const fs = require('node:fs');
const assert = require('node:assert/strict');
(async () => {
  const fixture = JSON.parse(fs.readFileSync(process.argv[2]));
  const token = new URLSearchParams(new URL(fixture.url).hash.slice(1)).get('t');
  const browser = await chromium.launch({headless: true, executablePath: process.env.CHROMIUM});
  try {
    const page = await browser.newPage();
    const requests = [];
    page.on('request', request => requests.push(request));
    await page.goto(fixture.url);
    await page.locator('#editor').waitFor({state: 'visible'});
    assert.ok(new URL(page.url()).hash === '', 'Fragment was not removed');
    assert.ok(new URL(page.url()).search === '?site=blog', 'Site query was lost');
    assert.ok(!JSON.stringify(await page.evaluate(() => history.state)).includes(token), 'Token entered history state');
    assert.ok(await page.evaluate(() => localStorage.length + sessionStorage.length) === 0, 'Token entered browser storage');
    const png = await page.evaluate(() => { const c = document.createElement('canvas'); c.width = c.height = 2; return c.toDataURL('image/png').split(',')[1]; });
    for (let i = 0; i < 2; i++) {
      await page.locator('#files').setInputFiles({name: 'test.png', mimeType: 'image/png', buffer: Buffer.from(png, 'base64')});
      for (let tries = 0; !await page.locator('#save').isEnabled(); tries++) {
        if (tries === 150) throw new Error('Save did not become enabled');
        await new Promise(resolve => setTimeout(resolve, 100));
      }
      await page.locator('#save').click();
      await page.locator('#done').waitFor({state: 'visible'});
      assert.ok(!await page.locator('#again').getAttribute('href').then(href => href.includes(token)), 'Add more link contains token');
      const documents = requests.filter(r => r.resourceType() === 'document').length;
      await page.locator('#again').click();
      await page.locator('#editor').waitFor({state: 'visible'});
      assert.ok(requests.filter(r => r.resourceType() === 'document').length === documents, 'Add more navigated');
      assert.ok(await page.locator('#photos img').count() === 0, 'Old previews remain');
    }
    const posts = requests.filter(r => r.method() === 'POST' && new URL(r.url()).pathname === '/v1/photo-links');
    assert.ok(posts.length === 2 && posts.every(r => r.headers()['x-photo-token'] === token), 'Same-page saves lost the token');
    assert.ok(requests.every(r => !r.url().includes(token) && !(r.headers().referer || '').includes(token)), 'Token entered a request URL or Referer');
    const apiCalls = requests.filter(r => new URL(r.url()).pathname.startsWith('/v1/')).length;
    await page.reload();
    await page.getByText('This photo link was used in this tab. Ask your chat for a new link if needed.', {exact: true}).waitFor();
    assert.ok(await page.locator('#editor').isHidden(), 'Reload exposed the editor');
    assert.ok(requests.filter(r => new URL(r.url()).pathname.startsWith('/v1/')).length === apiCalls, 'Reload attempted authentication');
    console.log('PASS Chromium: fragment removal, memory-only token, two saves, Add more without navigation, reload message, no token in network URLs');
  } finally {
    await browser.close();
    await fetch(fixture.base + '/test-done', {method: 'POST'});
  }
})().catch(error => { console.error(error.message.replace(/#t=[^\s"<>]+/g, '#t=[redacted]')); process.exitCode = 1; });
