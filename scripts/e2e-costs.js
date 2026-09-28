#!/usr/bin/env node
// Browser check of the cost calculator (/costs): at 390 and 1280 px no
// horizontal scroll and no script errors; AWS, Azure and Google Cloud lead
// and AWS is selected; the 2,000-people example shows; the cluster, traffic
// and private-link switches change the totals and land in the address bar;
// a provider in the address is selected; the calculator link is right.
// Screenshots go to <shots-dir>.
//   NODE_PATH=/opt/pw/node_modules node scripts/e2e-costs.js <base> <shots-dir>
const { chromium } = require('playwright');
const assert = (c, m) => { if (!c) { console.error('FAIL: ' + m); process.exitCode = 1; } else console.log('ok: ' + m); };
(async () => {
  const [base, out] = process.argv.slice(2);
  const b = await chromium.launch();
  for (const w of [390, 1280]) {
    const p = await b.newPage({ viewport: { width: w, height: 900 } });
    const errs = []; p.on('pageerror', e => errs.push(e.message)); p.on('console', m => { if (m.type() === 'error') errs.push(m.text()); });
    await p.goto(base + '/costs', { waitUntil: 'networkidle' });
    await p.waitForSelector('.prow');
    const sw = await p.evaluate(() => document.documentElement.scrollWidth);
    assert(sw <= w, w + ': no horizontal scroll (' + sw + ')');
    assert(errs.length === 0, w + ': no errors ' + errs.join(';'));
    const names = await p.locator('.prow .pname b').allInnerTexts();
    assert(names.slice(0, 3).join() === 'AWS,Azure,Google Cloud', w + ': AWS, Azure, Google Cloud lead (' + names + ')');
    assert(await p.locator('.prow[aria-pressed=true] .pname b').innerText() === 'AWS', w + ': AWS selected by default');
    assert(await p.locator('#example').isVisible(), w + ': 2,000-people example shown');
    const aws = async () => p.locator('.prow[data-provider=aws] .amt b').innerText();
    const own = await aws();
    await p.locator('#in-existing').uncheck();
    const fresh = await aws();
    assert(fresh !== own, w + ': new cluster changes AWS (' + own + ' → ' + fresh + ')');
    await p.locator('#in-existing').check();
    await p.selectOption('#in-traffic', 'heavy');
    const heavy = await aws();
    assert(await p.locator('#in-private').isChecked(), w + ': private link is the default');
    await p.locator('#in-private').uncheck();
    const internet = await aws();
    assert(heavy !== own && internet !== heavy, w + ': heavy ' + heavy + ', over the internet ' + internet);
    assert(/traffic=heavy/.test(p.url()) && /network=internet/.test(p.url()), w + ': state in the address bar');
    await p.reload({ waitUntil: 'networkidle' }); await p.waitForSelector('.prow');
    assert(!(await p.locator('#in-private').isChecked()) && await aws() === internet, w + ': the address restores the state');
    const cal = await p.locator('#detail a.cta').getAttribute('href');
    assert(cal === 'https://calculator.aws/#/', w + ': AWS calculator link');
    await p.goto(base + '/costs', { waitUntil: 'networkidle' }); await p.waitForSelector('.prow');
    await p.screenshot({ path: `${out}/costs-${w}.png`, fullPage: true });
    await p.goto(base + '/costs?people=2000&sites=6000&traffic=heavy&cluster=new&provider=azure', { waitUntil: 'networkidle' }); await p.waitForSelector('.prow');
    assert(await p.locator('.prow[aria-pressed=true] .pname b').innerText() === 'Azure', w + ': provider from the address');
    await p.screenshot({ path: `${out}/costs-${w}-2000-heavy-new-azure.png`, fullPage: true });
    await p.close();
  }
  await b.close();
})();
