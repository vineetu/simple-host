#!/usr/bin/env node
// Browser check of the one site-wide theme (INTENT 2026-09-28): with no stored
// choice every page follows the system (OS dark → dark, OS light → light);
// Light or Dark picked in the header's theme menu is kept once and applies to
// every page, before first paint, whatever the OS says; "Match my system"
// clears it. No horizontal scroll at 390 and 1280 px. Screenshots of a few
// pages in both themes go to <shots-dir>.
//   NODE_PATH=/opt/pw/node_modules node scripts/e2e-theme.js <base> <shots-dir>
// <base> serves the UI routes, e.g. the CHROME_SERVE_ADDR test server
// (go test ./internal/handler -run TestServeChromeForScreenshots), which adds
// /jane (a person page), /_404, /_offline and /_takendown.
const { chromium } = require('playwright');
const assert = (c, m) => { if (!c) { console.error('FAIL: ' + m); process.exitCode = 1; } else console.log('ok: ' + m); };
const PAGES = ['/', '/dashboard', '/install.html', '/docs.html', '/architecture.html', '/privacy.html', '/features',
  '/enterprise', '/enterprise/brief', '/enterprise/architecture', '/hackathons', '/setup', '/costs', '/terms',
  '/support', '/admin', '/analytics/my-site', '/jane', '/_404'];
const BARE = ['/_offline', '/_takendown', '/_wizard'];
const FIVE = ['/', '/features', '/enterprise/architecture', '/costs', '/setup'];
(async () => {
  const [base, out] = process.argv.slice(2);
  const b = await chromium.launch();
  // What the page shows at first paint: the theme attribute and the body's
  // background, read by a script that runs as soon as <body> exists.
  const firstPaint = () => {
    window.__first = null;
    new MutationObserver((_, o) => {
      if (document.body && !window.__first) {
        window.__first = { theme: document.documentElement.dataset.theme || '' };
        o.disconnect();
      }
    }).observe(document, { childList: true, subtree: true });
  };
  const state = async (p) => p.evaluate(() => {
    const bg = getComputedStyle(document.body).backgroundColor;
    const m = bg.match(/\d+(\.\d+)?/g) || [255, 255, 255];
    const lum = (0.2126 * m[0] + 0.7152 * m[1] + 0.0722 * m[2]) / 255;
    return { theme: document.documentElement.dataset.theme, first: window.__first && window.__first.theme, dark: lum < 0.5, bg,
      stored: (() => { try { return localStorage.getItem('sh-theme'); } catch (e) { return 'ERR'; } })(),
      sw: document.documentElement.scrollWidth };
  });

  for (const scheme of ['dark', 'light']) {
    for (const w of [390, 1280]) {
      const ctx = await b.newContext({ colorScheme: scheme, viewport: { width: w, height: 900 } });
      await ctx.addInitScript(firstPaint);
      const p = await ctx.newPage();
      const errs = []; p.on('pageerror', e => errs.push(e.message));
      for (const path of PAGES.concat(BARE)) {
        await p.goto(base + path, { waitUntil: 'load' });
        const s = await state(p);
        const want = scheme === 'dark';
        assert(s.theme === scheme && s.first === scheme && s.dark === want,
          `OS ${scheme}, ${w}px, no choice: ${path} is ${scheme} from first paint (theme=${s.theme} first=${s.first} bg=${s.bg})`);
        assert(s.sw <= w, `${w}px ${scheme}: ${path} no horizontal scroll (${s.sw})`);
      }
      assert(errs.length === 0, `OS ${scheme} ${w}px: no script errors ${errs.join('; ')}`);
      await ctx.close();
    }
  }

  // Override with the OS dark: pick Light on one page, see it on five others,
  // then Dark, then Match my system.
  const ctx = await b.newContext({ colorScheme: 'dark', viewport: { width: 1280, height: 900 } });
  await ctx.addInitScript(firstPaint);
  const p = await ctx.newPage();
  const pick = async (which) => {
    await p.click('#theme-toggle');
    assert(await p.locator('#sh-theme-menu').isVisible(), 'theme menu opens');
    await p.click(`#sh-theme-menu [data-sh-theme="${which}"]`);
    assert(await p.locator('#sh-theme-menu').isHidden(), 'theme menu closes after a pick');
    assert(await p.locator(`#sh-theme-menu [data-sh-theme="${which}"]`).getAttribute('aria-checked') === 'true', `${which} is ticked`);
  };
  const across = async (want, stored, label) => {
    for (const path of FIVE) {
      await p.goto(base + path, { waitUntil: 'load' });
      const s = await state(p);
      assert(s.theme === want && s.first === want && s.dark === (want === 'dark') && s.stored === stored,
        `${label}: ${path} is ${want} from first paint (theme=${s.theme} first=${s.first} stored=${s.stored})`);
      const checked = await p.locator('#sh-theme-menu [aria-checked="true"]').getAttribute('data-sh-theme');
      assert(checked === (stored || 'system'), `${label}: ${path} menu shows ${stored || 'system'} (${checked})`);
    }
  };
  await p.goto(base + '/docs.html', { waitUntil: 'load' });
  assert((await state(p)).theme === 'dark', 'OS dark: docs starts dark');
  await pick('light');
  assert((await state(p)).theme === 'light', 'Light applies at once');
  await across('light', 'light', 'picked Light, OS dark');
  await p.goto(base + '/support', { waitUntil: 'load' });
  await pick('dark');
  await across('dark', 'dark', 'picked Dark');
  // The stored choice wins over the OS in either direction.
  await p.emulateMedia({ colorScheme: 'light' });
  await across('dark', 'dark', 'picked Dark, OS now light');
  await p.goto(base + '/terms', { waitUntil: 'load' });
  await pick('light');
  await across('light', 'light', 'picked Light again, OS light');
  await p.emulateMedia({ colorScheme: 'dark' });
  await across('light', 'light', 'picked Light, OS dark again');
  await p.goto(base + '/privacy.html', { waitUntil: 'load' });
  await pick('system');
  await across('dark', null, 'Match my system, OS dark');
  // Following the system is live when nothing is picked.
  await p.emulateMedia({ colorScheme: 'light' });
  await p.waitForTimeout(100);
  assert((await state(p)).theme === 'light', 'Match my system: follows an OS change without a reload');
  await ctx.close();

  // Screenshots, light and dark (picked).
  for (const t of ['light', 'dark']) {
    const c = await b.newContext({ colorScheme: 'light', viewport: { width: 1280, height: 900 } });
    await c.addInitScript((v) => { try { localStorage.setItem('sh-theme', v); } catch (e) {} }, t);
    const q = await c.newPage();
    for (const [path, name] of [['/', 'home'], ['/features', 'features'], ['/enterprise/architecture', 'enterprise-architecture'],
      ['/costs', 'costs'], ['/setup', 'setup'], ['/jane', 'person-page'], ['/_404', 'not-found']]) {
      await q.goto(base + path, { waitUntil: 'networkidle' });
      await q.screenshot({ path: `${out}/${name}-${t}.png` });
    }
    await q.setViewportSize({ width: 390, height: 844 });
    await q.goto(base + '/enterprise', { waitUntil: 'networkidle' });
    await q.click('#theme-toggle');
    await q.screenshot({ path: `${out}/enterprise-390-menu-${t}.png` });
    await c.close();
  }
  for (const s of ['light', 'dark']) {
    const c = await b.newContext({ colorScheme: s, viewport: { width: 390, height: 700 } });
    const q = await c.newPage();
    await q.goto(base + '/_offline', { waitUntil: 'load' });
    await q.screenshot({ path: `${out}/offline-os-${s}.png` });
    await q.goto(base + '/_wizard', { waitUntil: 'load' });
    await q.screenshot({ path: `${out}/first-run-wizard-os-${s}.png` });
    await c.close();
  }
  await b.close();
})().catch(e => { console.error(e); process.exit(1); });
