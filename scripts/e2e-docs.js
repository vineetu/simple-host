#!/usr/bin/env node
// Run against disposable Host and Hack servers, with Chromium installed:
// NODE_PATH=/opt/pw/node_modules node scripts/e2e-docs.js HOST_URL HACK_URL SHOTS_DIR
const { chromium } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs');
(async () => {
  const [host, hack, shots] = process.argv.slice(2);
  fs.mkdirSync(shots, {recursive:true});
  const browser = await chromium.launch();
  try {
    for (const [brand, base] of [['host', host], ['hack', hack]]) {
      for (const scheme of ['light', 'dark']) for (const width of [390, 1280]) {
        const context = await browser.newContext({colorScheme:scheme, viewport:{width,height:900}});
        // Keep a local verification run off public services.
        const origin = new URL(base).origin;
        await context.route('**/*', route => new URL(route.request().url()).origin === origin ? route.continue() : route.abort());
        const page = await context.newPage();
        const errors = [];
        page.on('pageerror', e => errors.push(e.message));
        page.on('console', m => { if (m.type() === 'error') errors.push(m.text()); });
        await context.addInitScript(() => {
          new MutationObserver((_, observer) => {
            if (document.body && !window.firstDocsTheme) {
              window.firstDocsTheme = document.documentElement.dataset.theme;
              observer.disconnect();
            }
          }).observe(document, {childList:true, subtree:true});
        });
        const guide = brand === 'host' ? 'addresses' : 'notifications';
        for (const [path, name] of [['/docs/','home'], [`/docs/guides/${guide}/`,'guide'], ['/docs/api/','api']]) {
          const response = await page.goto(base + path, {waitUntil:'networkidle'});
          assert.equal(response.status(), 200);
          assert.equal(await page.locator('script:not([src])').count(), 0);
          assert.equal(await page.locator('.sh-header').count(), 1);
          assert.equal(await page.locator('.sh-footer').count(), 1);
          const state = await page.evaluate(() => ({theme:document.documentElement.dataset.theme, first:window.firstDocsTheme, width:document.documentElement.scrollWidth}));
          assert.equal(state.theme, scheme, `${brand} ${path} theme`);
          assert.equal(state.first, scheme, `${brand} ${path} first paint`);
          assert.ok(state.width <= width, `${brand} ${path} ${width}px overflows to ${state.width}`);
          if (name === 'api') await page.locator('#swagger-ui .opblock').first().waitFor();
          await page.screenshot({path:`${shots}/${brand}-${name}-${width}-${scheme}.png`,fullPage:true});
        }
        await page.goto(base + '/docs/', {waitUntil:'networkidle'});
        await page.locator('site-search input').waitFor({state:'attached'});
        await page.keyboard.press('/');
        await page.locator('dialog[open] input').fill('storage');
        await page.locator('dialog[open] .pagefind-ui__result-link').first().waitFor();
        assert.match(await page.locator('dialog[open]').innerText(), /Storage|storage/);
        await page.keyboard.press('Escape');
        // Search indexes only instructions shared by hosted and self-hosted installs.
        const searchCounts = await page.evaluate(async () => {
          const pf = await import('/docs/pagefind/pagefind.js');
          const counts = {};
          for (const query of ['"free short name"', '"npx skills add"', '"Download this install"']) {
            counts[query] = (await pf.search(query)).results.length;
          }
          return counts;
        });
        assert.ok(Object.values(searchCounts).every(n => n === 0), JSON.stringify(searchCounts));
        await page.keyboard.press('Control+k');
        assert.equal(await page.locator('dialog[open]').count(), 1);
        await page.keyboard.press('Escape');
        if (width === 390) {
          await page.locator('button[popovertarget=starlight__sidebar]').click();
          assert.equal(await page.locator('#starlight__sidebar').evaluate(e => e.matches(':popover-open')), true);
          await page.locator('#starlight__sidebar a[href="/docs/guides/storage/"]').click();
          await page.waitForURL('**/docs/guides/storage/');
        } else {
          await page.getByRole('link', {name:'Docs home',exact:true}).click();
          await page.waitForURL(base + '/docs/');
        }
        // The site header's choice is the only theme preference and survives navigation.
        await page.locator('#theme-toggle').click();
        const opposite = scheme === 'light' ? 'dark' : 'light';
        await page.locator(`[data-sh-theme="${opposite}"]`).click();
        await page.goto(base + '/docs/guides/storage/');
        assert.equal(await page.locator('html').getAttribute('data-theme'), opposite);
        await page.getByRole('tab', {name:'API',exact:true}).first().click();
        await page.goto(base + '/docs/guides/publish/');
        assert.equal((await page.locator('starlight-tabs [role=tab][aria-selected=true]').first().innerText()).trim(), 'API');
        assert.equal(await page.locator('starlight-tab-item').count(), 0, 'MDX tab markup compiled');
        await page.locator('#theme-toggle').click();
        await page.locator(`[data-sh-theme="${scheme}"]`).click();
        await page.goto(base + '/docs.html#notifications');
        await page.waitForURL('**/docs/guides/notifications/');
        assert.equal(await page.locator('h1').innerText(), 'Notifications');
        await page.screenshot({path:`${shots}/${brand}-legacy-notifications-${width}-${scheme}.png`});
        await page.goto(base + '/docs/');
        await page.goto(base + '/docs.html#connect-a-domain');
        if (brand === 'host') {
          await page.waitForURL('**/docs/guides/addresses/#connect-your-own-domain');
          assert.equal(await page.locator('#connect-your-own-domain').count(), 1);
          const heading = await page.locator('#connect-your-own-domain').boundingBox();
          assert.ok(heading.y >= 108 && heading.y < 700, `anchor visible below chrome: ${heading.y}`);
        } else {
          await page.waitForURL(base + '/docs/');
          assert.equal(await page.locator('h1').innerText(), 'Docs');
        }
        await page.screenshot({path:`${shots}/${brand}-legacy-domain-${width}-${scheme}.png`});
        await page.goto(base + '/docs.html#unrecognised-old-anchor');
        assert.equal(new URL(page.url()).pathname, '/docs/');
        assert.deepEqual(errors, [], `${brand} ${width}px ${scheme}: browser errors`);
        console.log(`ok: ${brand} ${width}px ${scheme}, chrome, theme, search, menu, Swagger, tabs, anchors`);
        await context.close();
      }
    }
  } finally { await browser.close(); }
})().catch(error => {console.error(error); process.exitCode=1;});
