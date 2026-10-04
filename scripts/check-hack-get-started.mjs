// Phone/desktop checks against a served page, including real clipboard writes.
// PLAYWRIGHT_MODULE, CHROMIUM_PATH, HACK_GET_STARTED_URL and SHOTS_DIR may override defaults.
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || '/tmp/tsx/node_modules/playwright/index.mjs');
const url = process.env.HACK_GET_STARTED_URL || 'https://simple-hack.app/get-started';
const origin = new URL(url).origin;
const shots = process.env.SHOTS_DIR || '/tmp/hack-get-started-checks';
await mkdir(shots, { recursive: true });
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || '/home/ubuntu/.cache/ms-playwright/chromium-1234/chrome-linux/chrome', headless: true, args: ['--no-sandbox'] });
try {
  for (const width of [320, 390, 1280]) {
    for (const colorScheme of ['light', 'dark']) {
      const context = await browser.newContext({ viewport: { width, height: 844 }, colorScheme, permissions: ['clipboard-read', 'clipboard-write'] });
      const page = await context.newPage();
      const requests = [], errors = [];
      page.on('request', request => {
        if (/^https?:/.test(request.url()) && new URL(request.url()).origin !== origin) requests.push(request.url());
      });
      page.on('pageerror', error => errors.push(error.message));
      page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });
      assert.equal((await page.goto(url, { waitUntil: 'networkidle' })).status(), 200);
      await page.evaluate(() => document.fonts.ready);
      assert.equal(await page.locator('html').getAttribute('data-theme'), colorScheme);
      assert.match(await page.locator('html').getAttribute('class'), /sh-hack/);
      assert.equal(await page.locator('.sh-header').count(), 1);
      assert.equal(await page.locator('.sh-footer').count(), 1);
      assert.equal(await page.locator('#pick-heading').textContent(), 'Pick your AI');
      assert.equal(await page.locator('.skills > details').count(), 5);
      assert.equal(await page.locator('.prompts > .prompt').count(), 3);
      assert.equal(await page.locator('#other-installs').getAttribute('open'), null);
      assert.equal(await page.locator('#other-installs a').first().isVisible(), false);
      const noOverflow = async () => assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `${width}/${colorScheme}: horizontal overflow`);
      await noOverflow();
      assert.match(await page.locator('h1').evaluate(el => getComputedStyle(el).fontFamily), /Caveat/);
      const ids = ['chatgpt', 'claude', 'grok', 'copilot', 'coding-agents'];
      assert.equal(await page.locator('.skills details[open], .faq details[open]').count(), 0);
      for (const id of ids) {
        const summary = page.locator('#' + id + ' > summary');
        assert.ok((await summary.boundingBox()).height >= 44);
        await summary.click();
        assert.equal(await page.locator('.skills details[open]').count(), 1);
        assert.equal(await page.locator('#' + id + ' .steps').isVisible(), true);
        await noOverflow();
        await page.screenshot({ path: `${shots}/${width}-${colorScheme}-${id}.png`, fullPage: true });
        await summary.click();
        assert.equal(await page.locator('.skills details[open]').count(), 0);
      }
      await page.locator('#chatgpt > summary').focus();
      await page.keyboard.press('Enter');
      await page.locator('#claude > summary').click();
      assert.equal(await page.locator('#chatgpt').getAttribute('open'), null);
      for (const question of await page.locator('.faq summary').all()) {
        assert.ok((await question.boundingBox()).height >= 44);
        await question.click();
        assert.equal(await page.locator('.faq details[open]').count(), 1);
        await noOverflow();
        await question.click();
      }
      for (const id of ids) {
        await page.evaluate(hash => { location.hash = hash; }, id);
        await page.waitForFunction(id => document.getElementById(id).open, id);
        assert.equal(await page.locator('.skills details[open]').count(), 1);
        await noOverflow();
      }
      await page.goto(url + '#chatgpt', { waitUntil: 'networkidle' });
      assert.equal(await page.locator('#chatgpt .steps').isVisible(), true);
      const buttons = page.locator('[data-copy-target]');
      assert.equal(await buttons.count(), 9);
      for (const button of await buttons.all()) {
        await button.evaluate(button => { const row = button.closest('.skills > details'); if (row) row.open = true; });
        const target = await button.getAttribute('data-copy-target');
        const expected = (await page.locator('#' + target).textContent()).trim();
        assert.ok((await button.boundingBox()).height >= 44, 'Copy button must be tappable');
        await button.click();
        for (let attempt = 0; attempt < 100 && await button.textContent() !== 'Copied'; attempt++) {
          await new Promise(resolve => setTimeout(resolve, 20));
        }
        assert.equal(await button.textContent(), 'Copied', target);
        assert.equal(await page.evaluate(() => navigator.clipboard.readText()), expected, target);
      }
      await page.locator('#other-installs > summary').click();
      assert.equal(await page.locator('#other-installs a').first().isVisible(), true);
      await noOverflow();
      // Exercise the real legacy clipboard fallback, then the selected-text fallback.
      await page.evaluate(() => Object.defineProperty(navigator.clipboard, 'writeText', { configurable: true, value: async () => { throw new Error('denied'); } }));
      const fallback = page.locator('[data-copy-target="mcp-chatgpt"]');
      await page.locator('#chatgpt').evaluate(row => { row.open = true; });
      await fallback.click();
      assert.equal(await page.evaluate(() => navigator.clipboard.readText()), 'https://simple-hack.app/mcp');
      await page.evaluate(() => { document.execCommand = () => false; });
      await fallback.click();
      assert.equal(await page.evaluate(() => window.getSelection().toString()), 'https://simple-hack.app/mcp');
      assert.match(await page.locator('#copy-status').textContent(), /Text selected/);
      await noOverflow();
      await page.evaluate(() => { window.getSelection().removeAllRanges(); window.scrollTo(0, 0); });
      await page.screenshot({ path: `${shots}/${width}-${colorScheme}.png`, fullPage: true });
      assert.deepEqual(requests, [], 'No third-party requests');
      assert.deepEqual(errors, [], 'No browser or CSP errors');
      console.log(`PASS ${width}px ${colorScheme}: system theme, accordions, deep links, keyboard, all 9 Copy buttons, both fallbacks, no overflow`);
      await context.close();
    }
  }
  const noJS = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 320, height: 844 } });
  const nativePage = await noJS.newPage();
  await nativePage.goto(url);
  assert.equal(await nativePage.locator('.skills details[open], .faq details[open]').count(), 0);
  await nativePage.locator('#chatgpt > summary').click();
  assert.equal(await nativePage.locator('#chatgpt .steps').isVisible(), true);
  await nativePage.locator('#claude > summary').click();
  assert.equal(await nativePage.locator('#chatgpt').getAttribute('open'), null);
  await nativePage.locator('#claude > summary').click();
  assert.equal(await nativePage.locator('.skills details[open]').count(), 0);
  await noJS.close();
  console.log('PASS native accordions without JavaScript');
} finally { await browser.close(); }
