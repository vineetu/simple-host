import assert from 'node:assert/strict';
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || '/tmp/tsx/node_modules/playwright/index.mjs');
const browser = await chromium.launch({ headless: true, args: ['--no-sandbox'] });
try {
  for (const width of [390, 1280]) {
    for (const [brand, base] of [['host', process.argv[2]], ['hack', process.argv[3]]]) {
      const page = await browser.newPage({ viewport: { width, height: 844 } });
      const errors = [];
      page.on('pageerror', error => errors.push(error.message));
      assert.equal((await page.goto(base + '/setup')).status(), 200);
      const choices = page.locator('input[name="product"]');
      assert.equal(await choices.count(), brand === 'hack' ? 3 : 2);
      assert.equal(await page.locator('input[value="hack"]').count(), brand === 'hack' ? 1 : 0);
      if (brand === 'hack') {
        assert.equal(await page.locator('input[value="hack"]').isChecked(), true);
        assert.equal(await page.locator('input[name="mode"]').count(), 0);
        await page.getByRole('button', { name: 'Next', exact: true }).click();
        await page.getByRole('heading', { name: 'Simple Hack on one server' }).waitFor();
        await page.locator('#f-SITE_DOMAIN').fill('hack.example.test');
        await page.getByRole('button', { name: 'Show my steps' }).click();
        await page.getByRole('heading', { name: 'Install Simple Hack' }).waitFor();
        assert.match(await page.locator('#files').innerText(), /hack\.example\.test/);
      } else {
        assert.equal((await page.request.get(base + '/setup/simple-hack.json')).status(), 404);
        const script = await (await page.request.get(base + '/setup/setup.js')).text();
        assert.doesNotMatch(script, /Simple Hack|renderHack|simple-hack\.json/);
      }
      assert.deepEqual(errors, []);
      await page.close();
    }
  }
  console.log('product assets browser: setup choices and Hack install output pass at 390/1280px');
} finally { await browser.close(); }
