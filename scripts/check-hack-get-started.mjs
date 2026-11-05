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
      // There is one way to connect: the MCP address, shown once near the
      // top. Three apps (Claude, ChatGPT, Claude Code) get their own
      // accordion up front; six more (Grok, Copilot, Meta Muse, Gemini,
      // Mistral Vibe, Codex/other coding agents) sit collapsed under one
      // "Other AI apps" group -- same connection, not alternatives. Keep
      // this in step with the page (docs/architecture/frontend.md).
      assert.equal(await page.locator('#mcp-url').count(), 1);
      assert.equal((await page.locator('#mcp-url').textContent()).trim().endsWith('/mcp'), true);
      assert.equal(await page.locator('.skills > details').count(), 9);
      assert.equal(await page.locator('#other-ai-apps').getAttribute('open'), null);
      assert.equal(await page.locator('#other-ai-apps a').first().isVisible(), false);
      assert.equal(await page.locator('#other-installs').getAttribute('open'), null);
      assert.equal(await page.locator('#other-installs a').first().isVisible(), false);
      // No plugin, directory or review mention anywhere on the page.
      const bodyText = await page.locator('body').innerText();
      for (const stale of ['plugin', 'Plugin', 'Browse connectors', 'chatgpt.com/plugins', 'is in review']) {
        assert.equal(bodyText.includes(stale), false, `page must not mention ${JSON.stringify(stale)}`);
      }
      const noOverflow = async () => assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `${width}/${colorScheme}: horizontal overflow`);
      await noOverflow();
      // #hero-signed-in (the signed-in /get-started mode) carries its own
      // h1 too, hidden unless a key is set: scope to the public hero's,
      // which this pass (no key in localStorage) always shows.
      assert.match(await page.locator('#hero-public h1').evaluate(el => getComputedStyle(el).fontFamily), /Caveat/);

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

      const primaryIds = ['claude', 'chatgpt', 'claude-code'];
      const otherIds = ['grok', 'copilot', 'muse', 'gemini', 'mistral', 'other-coding'];
      const ids = primaryIds.concat(otherIds);
      assert.equal(await page.locator('.skills details[open], .faq details[open]').count(), 0);
      for (const id of primaryIds) {
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
      // "Other AI apps" opens as a group; each app inside it is its own
      // (separately exclusive) accordion.
      await page.locator('#other-ai-apps > summary').click();
      assert.equal(await page.locator('#other-ai-apps').getAttribute('open'), '');
      for (const id of otherIds) {
        const summary = page.locator('#' + id + ' > summary');
        assert.ok((await summary.boundingBox()).height >= 44);
        await summary.click();
        assert.equal(await page.locator('#other-ai-apps .skills details[open]').count(), 1);
        assert.equal(await page.locator('#' + id + ' .steps').first().isVisible(), true);
        await noOverflow();
        await page.screenshot({ path: `${shots}/${width}-${colorScheme}-${id}.png`, fullPage: true });
        await summary.click();
        assert.equal(await page.locator('#other-ai-apps .skills details[open]').count(), 0);
      }
      await page.locator('#other-ai-apps > summary').click();
      assert.equal(await page.locator('#other-ai-apps').getAttribute('open'), null);
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
        if (otherIds.includes(id)) {
          await page.waitForFunction(() => document.getElementById('other-ai-apps').open);
        }
        await page.waitForFunction(id => document.getElementById(id).open, id);
        await noOverflow();
      }
      await page.goto(url + '#chatgpt', { waitUntil: 'networkidle' });
      assert.equal(await page.locator('#chatgpt .steps').first().isVisible(), true);
      // Self-hosted has exactly one way to connect: the developer-mode
      // custom connector steps, no plugin upload (rule: no plugin mentions
      // at all off the hosted service). The hosted service keeps both,
      // byte-identical to origin/main, so this must tell them apart rather
      // than assume either count universally.
      const chatgptHosted = (await page.locator('#chatgpt').innerText()).includes('Upload plugin archive');
      assert.equal(await page.locator('#chatgpt .steps').count(), chatgptHosted ? 2 : 1);
      const buttons = page.locator('[data-copy-target]');
      // The top mcp-url box, one mcp-<app>/cmd-<app> row per of the 9 apps
      // (Meta Muse has an extra Client ID row), the collapsed skill-install
      // command, and the 3 role prompts (each role's own AI-app path). A
      // self-hosted install's skill-install step is a plain download link
      // instead (its own GitHub has no business naming the hosted service's
      // repo), so it has no #install-command copy row: 14, not 15.
      assert.equal(await buttons.count(), await page.locator('#install-command').count() ? 15 : 14);
      for (const button of await buttons.all()) {
        // An "other AI apps" row needs its own accordion AND the group's
        // outer <details> open; the install-command row lives directly
        // inside #other-installs, with no inner accordion of its own.
        // Either ancestor left closed leaves the row at zero height.
        await button.evaluate(button => {
          const row = button.closest('.skills > details');
          if (row) row.open = true;
          const group = button.closest('#other-ai-apps');
          if (group) group.open = true;
          const installs = button.closest('#other-installs');
          if (installs) installs.open = true;
        });
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
      // The install-command row's own iteration above left #other-installs
      // open (so its box had a height to measure); close it again so the
      // click below is a real open, not a toggle-closed.
      await page.locator('#other-installs').evaluate(el => { el.open = false; });
      await page.locator('#other-installs > summary').click();
      assert.equal(await page.locator('#other-installs a').first().isVisible(), true);
      await noOverflow();
      // Exercise the real legacy clipboard fallback, then the selected-text fallback.
      const mcpAddress = (await page.locator('#mcp-chatgpt').textContent()).trim();
      await page.evaluate(() => Object.defineProperty(navigator.clipboard, 'writeText', { configurable: true, value: async () => { throw new Error('denied'); } }));
      const fallback = page.locator('[data-copy-target="mcp-chatgpt"]');
      await page.locator('#chatgpt').evaluate(row => { row.open = true; });
      await fallback.click();
      assert.equal(await page.evaluate(() => navigator.clipboard.readText()), mcpAddress);
      await page.evaluate(() => { document.execCommand = () => false; });
      await fallback.click();
      assert.equal(await page.evaluate(() => window.getSelection().toString()), mcpAddress);
      assert.match(await page.locator('#copy-status').textContent(), /Text selected/);
      await noOverflow();
      await page.evaluate(() => { window.getSelection().removeAllRanges(); window.scrollTo(0, 0); });
      await page.screenshot({ path: `${shots}/${width}-${colorScheme}.png`, fullPage: true });
      assert.deepEqual(requests, [], 'No third-party requests');
      assert.deepEqual(errors, [], 'No browser or CSP errors');
      console.log(`PASS ${width}px ${colorScheme}: system theme, equal role paths, accordions, deep links, keyboard, all ${await buttons.count()} Copy buttons, both fallbacks, no overflow, no plugin mentions`);
      await context.close();
    }
  }
  // Signed-in view (A5): a valid key makes /v1/me and /v1/me/connections
  // succeed, client-side -- the page is static and cached, so this is
  // mocked rather than needing a real account. Public-only marketing
  // (the public hero, the "Is it free?"/"Can I run my own?" FAQ items)
  // must hide; the role how-to sections must not.
  {
    const context = await browser.newContext({ viewport: { width: 390, height: 844 } });
    const page = await context.newPage();
    await page.route('**/v1/me', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ email: 'ada@example.com', username: 'ada@example.com' }) }));
    await page.route('**/v1/me/connections', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ connections: [{ client_id: 'c1', name: 'Claude', connected_at: new Date().toISOString(), last_used_at: new Date().toISOString() }] }) }));
    await page.addInitScript(() => { try { localStorage.setItem('apiKey', 'test-key'); } catch (e) {} });
    assert.equal((await page.goto(url, { waitUntil: 'networkidle' })).status(), 200);
    await page.waitForFunction(() => document.getElementById('hero-signed-in') && !document.getElementById('hero-signed-in').hidden);
    assert.equal(await page.locator('#hero-public').isVisible(), false);
    assert.equal(await page.locator('#hero-signed-in').isVisible(), true);
    assert.match(await page.locator('#hero-signed-in').innerText(), /Signed in as ada@example\.com/);
    assert.match(await page.locator('#connect-status').textContent(), /Connected: Claude/);
    assert.equal(await page.locator('#hero-signed-in a[href="/events"]').count(), 1);
    assert.equal(await page.locator('#hero-signed-in a[href="/account"]').count(), 1);
    for (const el of await page.locator('[data-public-only]').all()) assert.equal(await el.isVisible(), false, 'public-only content must hide when signed in');
    // Role how-to sections stay: they are useful whether signed in or not.
    assert.equal(await page.locator('#organiser-heading').isVisible(), true);
    await context.close();
    console.log('PASS signed-in view: public-only marketing hidden, role how-to kept, connection status shown');
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
