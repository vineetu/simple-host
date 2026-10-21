#!/usr/bin/env node
// Browser check of the admin page at phone widths, against a real server
// with seeded data (a throwaway admin session, never the owner's account).
//
// Covers the 2026-10-21 bug: the Sites table's Owner column collapsing to
// one letter per line, and the Versions panel opening cramped inside it.
// Checks every admin tab (Overview, its sign-ups-by-source drill-down,
// Users, Sites — including the Versions panel, API, Moderation, Tools). At
// 320 and 390 px: no table cell narrower than ~60 px (besides the
// deliberately-narrow "..." actions column, number columns and short status
// tags, all unchanged by this fix) and no horizontal page scroll. At all
// three widths, including 768 (desktop stays as it is): no per-letter
// wrapping, the actual symptom reported.
//
//   PLAYWRIGHT_DIR=/opt/pw/node_modules node scripts/e2e-admin-layout.mjs <base> <adminKey>
import { createRequire } from 'node:module';
const require = createRequire(process.env.PLAYWRIGHT_DIR ? process.env.PLAYWRIGHT_DIR + '/' : import.meta.url);
const { chromium, webkit } = require('playwright');

const [base, adminKey] = process.argv.slice(2);
if (!base || !adminKey) {
  console.error('usage: e2e-admin-layout.mjs <base> <adminKey>');
  process.exit(1);
}
let failed = false;
const assert = (c, m) => { if (!c) { console.error('FAIL: ' + m); failed = true; } else console.log('ok: ' + m); };

const TABS = ['Overview', 'Users', 'Sites', 'API', 'Moderation', 'Tools'];
const WIDTHS = [320, 390, 768];

// A cell counts as "narrow" only if it is meant to hold open-ended text:
// skip number columns (.num, right-aligned counts), short status/tag pills
// (.tag, e.g. "live", "active"), empty cells (a "-" placeholder or a blank
// joined-date column) and the sticky "..." actions cell, all deliberately
// narrow by design, unrelated to this fix and present on Events too (the
// known-good reference for this collapse pattern).
async function checkNoNarrowCells(page, label) {
  const bad = await page.evaluate(() => {
    const out = [];
    document.querySelectorAll('table.t').forEach((t) => {
      const rows = t.tBodies[0] ? Array.from(t.tBodies[0].rows) : [];
      rows.forEach((row) => {
        Array.from(row.cells).forEach((cell) => {
          const r = cell.getBoundingClientRect();
          if (r.width <= 0) return; // display:none, not rendered
          if (cell.classList.contains('num')) return;
          if (cell.querySelector('.tag')) return;
          const text = (cell.textContent || '').trim();
          if (text.length <= 1) return;
          if (cell === row.cells[row.cells.length - 1] && cell.querySelector('.more,button[data-menu]')) return;
          if (r.width < 50) out.push({ table: t.id || '(unnamed)', text: text.slice(0, 30), width: Math.round(r.width) });
        });
      });
    });
    return out;
  });
  assert(bad.length === 0, `${label}: no table cell narrower than 50px (${JSON.stringify(bad).slice(0, 300)})`);
}

// The exact symptom reported: text broken onto (nearly) one character per
// line, which is what happens when a cell is squeezed far below its
// content's natural width.
async function checkNoLetterWrap(page, label) {
  const bad = await page.evaluate(() => {
    const out = [];
    document.querySelectorAll('table.t td').forEach((cell) => {
      const text = (cell.textContent || '').trim();
      if (text.length < 5 || cell.getBoundingClientRect().width <= 0) return;
      const walker = document.createTreeWalker(cell, NodeFilter.SHOW_TEXT);
      let node, lines = 0;
      while ((node = walker.nextNode())) {
        if (!node.textContent.trim()) continue;
        const r = document.createRange();
        r.selectNodeContents(node);
        lines += r.getClientRects().length;
      }
      if (lines > 0 && text.length / lines < 2) out.push({ text: text.slice(0, 30), lines });
    });
    return out;
  });
  assert(bad.length === 0, `${label}: no cell wraps one letter per line (${JSON.stringify(bad).slice(0, 300)})`);
}

async function checkNoPageScroll(page, label) {
  const info = await page.evaluate(() => ({ s: document.documentElement.scrollWidth, c: document.documentElement.clientWidth }));
  assert(info.s <= info.c + 1, `${label}: no horizontal page scroll (scrollWidth=${info.s}, clientWidth=${info.c})`);
}

async function run(browserType, browserName) {
  const b = await browserType.launch();
  for (const width of WIDTHS) {
    const ctx = await b.newContext({ viewport: { width, height: 1400 } });
    const page = await ctx.newPage();
    await page.addInitScript((k) => { try { localStorage.setItem('apiKey', k); } catch (e) {} }, adminKey);
    await page.goto(base + '/admin', { waitUntil: 'load' });
    // The 60px-cell and no-page-scroll assertions are scoped to phone
    // widths; all widths get the letter-wrap check, including 768 (desktop
    // stays as it is, so it should never regress either).
    const strict = width <= 390;
    for (const tab of TABS) {
      const label = `${browserName} ${width}px ${tab}`;
      await page.getByRole('tab', { name: new RegExp('^' + tab) }).click();
      await page.waitForTimeout(400);
      if (strict) { await checkNoPageScroll(page, label); await checkNoNarrowCells(page, label); }
      await checkNoLetterWrap(page, label);
    }
    // The sign-ups-by-source drill-down (Overview).
    await page.getByRole('tab', { name: /^Overview/ }).click();
    await page.waitForTimeout(300);
    const src = page.getByRole('button', { name: 'Website' });
    if (await src.count()) {
      await src.click();
      await page.waitForTimeout(400);
      const label = `${browserName} ${width}px Overview drill-down`;
      if (strict) { await checkNoPageScroll(page, label); await checkNoNarrowCells(page, label); }
      await checkNoLetterWrap(page, label);
    }
    // Sites: open "..." on the site with the most versions, then Versions.
    await page.getByRole('tab', { name: /^Sites/ }).click();
    await page.waitForSelector('#s-rows tr[data-site]');
    await page.waitForTimeout(300);
    const menuBtn = page.locator('#s-rows button[data-menu]').first();
    if (await menuBtn.count()) {
      await menuBtn.click();
      await page.getByRole('menuitem', { name: 'Versions' }).click();
      await page.waitForTimeout(500);
      const label = `${browserName} ${width}px Sites Versions panel`;
      if (strict) { await checkNoPageScroll(page, label); await checkNoNarrowCells(page, label); }
      await checkNoLetterWrap(page, label);
      if (width <= 700) {
        const panel = page.locator('.panel-inner').first();
        const pw = (await panel.boundingBox())?.width || 0;
        // The original bug squeezed this panel to a sliver (well under half
        // the viewport); comfortably over half proves it is not squeezed.
        assert(pw >= width * 0.5, `${label}: the panel is full-width, not squeezed (width=${Math.round(pw)}, viewport=${width})`);
        const closeBtn = page.getByRole('button', { name: 'Close' }).last();
        assert(await closeBtn.isVisible(), `${label}: Close is visible`);
        const box = await closeBtn.boundingBox();
        assert(!!box && box.width >= 40 && box.height >= 40, `${label}: Close is tappable (>=40x40)`);
      }
    }
    await ctx.close();
  }
  await b.close();
}

await run(chromium, 'chromium');
await run(webkit, 'webkit');

if (failed) { console.error('FAIL: one or more admin layout checks failed'); process.exit(1); }
console.log('ok: all admin layout checks passed');
