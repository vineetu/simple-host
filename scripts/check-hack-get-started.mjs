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

// SIM-218: neither the website nor an AI app is the lead way in -- every
// role's path works fully both ways (INTENT 2026-10-27). These are the real
// phrases this page must use for each role's two paths, taken from the web
// app's own screens (nav.js, participant-team.js, participant-website.js,
// participant-entry.js, public-join.js, public-judge.js, judge-queue.js,
// judge-scoring.js, organiser-create-event.js, organiser-overview.js,
// organiser-teams.js) so a check here fails if the copy drifts from what
// those screens actually say, not just from each other.
const ROLES = [
  {
    id: 'organiser', heading: 'Organiser', promptId: 'prompt-organiser',
    website: ['Create event', 'Judging', 'Teams', 'Tracks', 'Overview', 'Share your event', 'join link and judge link'],
    ai: ['Night Build'],
  },
  {
    id: 'participant', heading: 'Participant', promptId: 'prompt-participant',
    website: ['I agree to the code of conduct', 'Join event', 'Send application', 'Start a new team', 'Ask to join a team', 'Join with a link or code', 'Submit entry', 'Save draft', 'Upload'],
    ai: ['Night Owls'],
  },
  {
    id: 'judge', heading: 'Judge', promptId: 'prompt-judge',
    website: ['I agree to the code of conduct', 'Join as judge', 'Rubric', 'My teams', 'Score this team', 'Save & next', 'Save & finish'],
    ai: ['Show me my next entry'],
  },
];

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
      // Eight AI apps have their own accordion today (ChatGPT, Claude, Grok,
      // Copilot, Meta Muse, Gemini, Mistral Vibe, coding agents); keep this
      // in step with the page, unlike the old fixed-five check that quietly
      // trailed it (docs/architecture/frontend.md).
      assert.equal(await page.locator('.skills > details').count(), 8);
      assert.equal(await page.locator('#other-installs').getAttribute('open'), null);
      assert.equal(await page.locator('#other-installs a').first().isVisible(), false);
      const noOverflow = async () => assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `${width}/${colorScheme}: horizontal overflow`);
      await noOverflow();
      assert.match(await page.locator('h1').evaluate(el => getComputedStyle(el).fontFamily), /Caveat/);

      // SIM-218: organiser, participant and judge each get a website path
      // and an AI-app path, described as fully as one another, as two equal
      // cards side by side on desktop and stacked on a phone -- never one
      // first and bigger. This is checked on real text and real geometry,
      // not just that both cards exist.
      for (const role of ROLES) {
        assert.equal(await page.locator('#' + role.id + '-heading').textContent(), role.heading);
        const paths = page.locator('#' + role.id + ' .path');
        assert.equal(await paths.count(), 2, `${role.id}: expected one website path and one AI-app path`);
        const websiteText = await paths.nth(0).innerText();
        for (const phrase of role.website) assert.ok(websiteText.includes(phrase), `${role.id} website path missing ${JSON.stringify(phrase)} -- got: ${websiteText}`);
        const aiText = await paths.nth(1).innerText();
        for (const phrase of role.ai) assert.ok(aiText.includes(phrase), `${role.id} AI-app path missing ${JSON.stringify(phrase)} -- got: ${aiText}`);
        assert.equal(await paths.nth(1).locator('#' + role.promptId).count(), 1, `${role.id}: its own prompt must live in its AI-app path`);
        assert.equal(await paths.nth(1).locator('a[href="#pick-ai"]').count(), 1, `${role.id}: AI-app path must point at Pick your AI`);
        const box0 = await paths.nth(0).boundingBox();
        const box1 = await paths.nth(1).boundingBox();
        if (width > 600) {
          assert.ok(Math.abs(box0.y - box1.y) < 4, `${role.id}/${width}: the two paths must sit on the same row on desktop, got y=${box0.y} vs y=${box1.y}`);
          assert.ok(box1.x > box0.x, `${role.id}/${width}: the AI-app path must sit to the right of the website path`);
          const ratio = box0.width / box1.width;
          assert.ok(ratio > 0.8 && ratio < 1.25, `${role.id}/${width}: the two paths must be equal width, got ${box0.width} vs ${box1.width}`);
        } else {
          assert.ok(box1.y >= box0.y + box0.height - 2, `${role.id}/${width}: the two paths must stack on a phone, got y=${box0.y} h=${box0.height} vs y=${box1.y}`);
          assert.ok(Math.abs(box0.x - box1.x) < 2, `${role.id}/${width}: stacked paths must share the same left edge`);
        }
      }
      await noOverflow();

      const ids = ['chatgpt', 'claude', 'grok', 'copilot', 'muse', 'gemini', 'mistral', 'coding-agents'];
      assert.equal(await page.locator('.skills details[open], .faq details[open]').count(), 0);
      for (const id of ids) {
        const summary = page.locator('#' + id + ' > summary');
        assert.ok((await summary.boundingBox()).height >= 44);
        await summary.click();
        assert.equal(await page.locator('.skills details[open]').count(), 1);
        assert.equal(await page.locator('#' + id + ' .steps').first().isVisible(), true);
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
      assert.equal(await page.locator('#chatgpt .steps').first().isVisible(), true);
      // ChatGPT offers connector setup and plugin upload in the same accordion.
      assert.equal(await page.locator('#chatgpt .steps').count(), 2);
      for (const steps of await page.locator('#chatgpt .steps').all()) assert.equal(await steps.isVisible(), true);
      const buttons = page.locator('[data-copy-target]');
      // One mcp-<app> row per of the 8 apps, Meta Muse's extra Client ID row,
      // the coding-agents install command, and the 3 role prompts (now in
      // each role's AI-app path, not a separate "Try one of these" section).
      assert.equal(await buttons.count(), 13);
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
      console.log(`PASS ${width}px ${colorScheme}: system theme, equal role paths, accordions, deep links, keyboard, all 13 Copy buttons, both fallbacks, no overflow`);
      await context.close();
    }
  }
  const noJS = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 320, height: 844 } });
  const nativePage = await noJS.newPage();
  await nativePage.goto(url);
  assert.equal(await nativePage.locator('.skills details[open], .faq details[open]').count(), 0);
  await nativePage.locator('#chatgpt > summary').click();
  assert.equal(await nativePage.locator('#chatgpt .steps').first().isVisible(), true);
  await nativePage.locator('#claude > summary').click();
  assert.equal(await nativePage.locator('#chatgpt').getAttribute('open'), null);
  await nativePage.locator('#claude > summary').click();
  assert.equal(await nativePage.locator('.skills details[open]').count(), 0);
  await noJS.close();
  console.log('PASS native accordions without JavaScript');
} finally { await browser.close(); }
