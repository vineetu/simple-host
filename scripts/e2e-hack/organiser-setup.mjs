// SIM-229..234: organiser setup journeys on production simple-hack.app.
// Run: node scripts/e2e-hack/organiser-setup.mjs [outDir]
import fs from 'node:fs';
import { Run, accounts, stamp, SITE, goto, tab, dismissTours, visibleError, createEventViaForm, api, deleteEvent } from './lib.mjs';

const OUT = process.argv[2] || '/tmp/claude-1001/-home-ubuntu-workspace-4/2268cadb-dfb1-4db4-9617-f1516b000a56/scratchpad/e2e-prod';
const FILES = OUT + '/orgsetup-files';
const run = new Run('organiser-setup', OUT);
const A = accounts(1);
// The event creator. Production allows 3 event creates per account per rolling
// 24 h (deleted events still count) and 2 live events at once, so reruns pick
// another set 1 account: CREATOR=organiser:0 | participant:3 | judge:0 ...
const [cRole, cIdx] = (process.env.CREATOR || 'participant:3').split(':');
const org = A[cRole][Number(cIdx || 0)];
const co = A['co-organiser'][0], p1 = A.participant[0], p2 = A.participant[1];
const created = [];
const deleted = {};
const base = 'e2e-orgsetup-' + stamp;
const S1 = base + '-a';        // main desktop event
const SP1 = base + '-pc';      // phone chromium
const SP2 = base + '-pw';      // phone webkit
const pub = slug => `https://${slug}.simple-hack.app/`;
const KNOWN = /website\/notifications/;

const day = n => new Date(Date.now() + n * 864e5).toISOString().slice(0, 10);
async function liveText(page) { return ((await page.locator('#live').innerText().catch(() => '')) || '').trim(); }
async function liveClass(page) { return (await page.locator('#live').getAttribute('class').catch(() => '')) || ''; }
async function waitLive(page, re, timeout = 10000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) { const t = await liveText(page); if (re.test(t)) return t; await page.waitForTimeout(200); }
  return await liveText(page);
}
async function noHScroll(page) {
  return page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1);
}
async function overflowers(page) {
  return page.evaluate(() => {
    const w = document.documentElement.clientWidth; const out = [];
    document.querySelectorAll('body *').forEach(e => {
      const r = e.getBoundingClientRect();
      if (r.width && r.right > w + 1 && getComputedStyle(e).position !== 'fixed') out.push((e.id ? '#' + e.id : e.tagName.toLowerCase() + '.' + (e.className || '')) + ' right=' + Math.round(r.right));
    });
    return out.slice(0, 6);
  });
}
// Phone: the tab strip collapses behind a "Sections" menu button.
async function mtab(page, name) {
  await dismissTours(page);
  if (!(await page.locator('#tab-' + name).isVisible().catch(() => false))) {
    if (await page.locator('#mg-menu-toggle').isVisible().catch(() => false)) { await page.locator('#mg-menu-toggle').click(); await page.waitForTimeout(300); }
  }
  await tab(page, name);
}
async function confirmDialog(page) {
  const ok = page.locator('[data-sh-dlg="ok"]:visible');
  await ok.waitFor({ timeout: 5000 }); await ok.click(); await page.waitForTimeout(500);
}
async function publicText(page, slug) {
  await page.goto(pub(slug), { waitUntil: 'domcontentloaded' });
  await page.waitForLoadState('networkidle', { timeout: 10000 }).catch(() => {});
  return await page.locator('body').innerText();
}
function realFailed(r) { return (r || []).filter(x => !KNOWN.test(x)); }

async function main() {
  const chromium = await run.browser('chromium');
  const O = await run.person(chromium, org, { label: 'organiser' });
  const pg = O.page;
  pg.setDefaultTimeout(10000);

  // ---------------- SIM-229 ----------------
  await run.step('SIM-229', 'create event from the form (name, slug available hint, description, dates, time zone, expected participants)', pg, async t => {
    await goto(pg, '/events/new');
    await pg.locator('#new-title').fill('E2E Orgsetup ' + stamp);
    await pg.locator('#new-slug').fill(S1);
    const hint = await pg.locator('#new-slug-msg').filter({ hasText: /available|use/ }).innerText({ timeout: 6000 }).catch(() => '');
    if (!/is available/.test(hint)) t.fail('slug hint for a free slug: "' + hint + '"');
    await pg.locator('#new-organiser').fill('E2E Organiser');
    await pg.locator('#new-org').fill('Simple Hack e2e');
    const pre = await pg.locator('#new-email').inputValue();
    if (pre && !pre.includes('@')) t.note('Contact email was prefilled with the username "' + pre + '" (not an email); the browser blocks Create until it is replaced');
    await pg.locator('#new-email').fill('e2e@example.com');
    await pg.locator('#new-purpose').fill('Automated end-to-end test event. Safe to delete.');
    await pg.locator('#new-start').fill(day(7));
    await pg.locator('#new-end').fill(day(9));
    await pg.locator('#new-tz').selectOption('Asia/Kolkata');
    await pg.locator('#new-n').fill('120');
    await pg.locator('#new-submit').click();
    try { await pg.waitForURL(u => u.pathname.includes('/e/' + S1 + '/manage'), { timeout: 20000 }); created.push(S1); }
    catch (e) { t.fail('did not reach manage view; on-screen: ' + (await visibleError(pg) || await liveText(pg))); return; }
    await dismissTours(pg);
    const title = await pg.locator('#mg-title').innerText();
    if (!title.includes('E2E Orgsetup')) t.fail('manage title: ' + title);
    const ev = await api(org.api_key, 'GET', '/v1/hack/events/' + S1);
    const e = ev.data && ev.data.event || {};
    if (e.time_zone !== 'Asia/Kolkata') t.fail('time zone saved as ' + e.time_zone);
    t.note(`saved: tz=${e.time_zone} starts=${e.starts_at} ends=${e.ends_at} expected=${(ev.data.organiser || {}).expected_participants || e.expected_participants}`);
  });

  await run.step('SIM-229', 'slug check: a taken slug is flagged before submit and on submit', pg, async t => {
    await goto(pg, '/events/new');
    await pg.locator('#new-title').fill('Taken slug check');
    await pg.locator('#new-slug').fill(S1);
    await pg.waitForTimeout(1500);
    const hint = await pg.locator('#new-slug-msg').innerText();
    if (!/already (in use|taken)/i.test(hint)) t.fail('taken-slug hint: "' + hint + '"');
    await pg.locator('#new-organiser').fill('E2E'); await pg.locator('#new-purpose').fill('x');
    await pg.locator('#new-email').fill('e2e@example.com');
    await pg.locator('#new-start').fill(day(7)); await pg.locator('#new-n').fill('10');
    await pg.locator('#new-submit').click(); await pg.waitForTimeout(2000);
    const err = (await pg.locator('#ferr-slug').innerText().catch(() => '')) || await liveText(pg);
    if (!/in use|taken/i.test(err)) t.fail('submit with taken slug: "' + err + '" url=' + pg.url());
    t.note('hint="' + hint + '" submit="' + err + '"');
  });

  await run.step('SIM-229', 'slug check: invalid characters (capitals, underscore, !, spaces, leading hyphen)', pg, async t => {
    await goto(pg, '/events/new');
    const cases = ['E2E_Bad Slug!', '-e2e-lead', 'e2e-ünïcode', 'e2e-trail-'];
    for (const c of cases) {
      await pg.locator('#new-slug').fill(c); await pg.waitForTimeout(1300);
      const hint = await pg.locator('#new-slug-msg').innerText();
      const cls = await pg.locator('#new-slug-msg').getAttribute('class');
      t.note(`"${c}" -> "${hint}" (${cls})`);
      if (/is available/.test(hint) || !/bad/.test(cls || '')) t.fail(`"${c}" not flagged as invalid: "${hint}"`);
    }
    // Submit with an invalid slug shows a field error, does not create.
    await pg.locator('#new-title').fill('Invalid slug submit');
    await pg.locator('#new-slug').fill('e2e_bad_slug');
    await pg.locator('#new-organiser').fill('E2E'); await pg.locator('#new-purpose').fill('x');
    await pg.locator('#new-email').fill('e2e@example.com');
    await pg.locator('#new-start').fill(day(7)); await pg.locator('#new-n').fill('10');
    await pg.locator('#new-submit').click(); await pg.waitForTimeout(2000);
    const err = (await pg.locator('#ferr-slug').innerText().catch(() => '')) || await liveText(pg);
    t.note('submit e2e_bad_slug -> "' + err + '"');
    if (!err) t.fail('no message for invalid slug submit');
    if (pg.url().includes('/manage')) { t.fail('event created with invalid slug'); }
  });

  await run.step('SIM-229', 'slug check: a 40-character slug', pg, async t => {
    await goto(pg, '/events/new');
    const s40 = (base + '-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx').slice(0, 40);
    await pg.locator('#new-slug').click();
    await pg.locator('#new-slug').pressSequentially(s40, { delay: 5 });
    await pg.waitForTimeout(1500);
    const v = await pg.locator('#new-slug').inputValue();
    const hint = await pg.locator('#new-slug-msg').innerText();
    t.note(`typed ${s40.length} chars, field holds ${v.length}: hint "${hint}"`);
    if (v.length === 40 && /is available/.test(hint)) t.fail('40-char slug reported available but server max is 39');
    if (v.length < 40 && !/39|long|characters/i.test(hint)) t.note('field silently cut the 40th character; no message says the limit is 39');
  });

  await run.step('SIM-229', 'new event appears in Your events and opens the manage view', pg, async t => {
    await goto(pg, '/events');
    const card = pg.locator('#events-list a.card', { hasText: 'E2E Orgsetup ' + stamp });
    if (!(await card.count())) { t.fail('event not listed in Your events'); return; }
    t.note('card: ' + (await card.first().innerText()).replace(/\s+/g, ' '));
    await card.first().click();
    await pg.waitForURL(u => u.pathname === '/e/' + S1 + '/manage', { timeout: 10000 });
    await dismissTours(pg);
  });

  for (const name of ['overview', 'page', 'content', 'people', 'teams', 'projects', 'judging', 'settings']) {
    await run.step('SIM-229', 'manage tab loads without errors: ' + name, pg, async t => {
      const c0 = run.console.length, f0 = run.failed.length;
      await tab(pg, name);
      await pg.waitForLoadState('networkidle', { timeout: 8000 }).catch(() => {});
      if (!(await pg.locator('#p-' + name).isVisible())) t.fail('panel #p-' + name + ' not visible');
      const err = await visibleError(pg); if (err) t.fail('on-screen error: ' + err);
      if (/err/.test(await liveClass(pg)) && await liveText(pg)) t.fail('live error: ' + await liveText(pg));
      const cons = run.console.slice(c0).filter(x => !KNOWN.test(x)); const fl = realFailed(run.failed.slice(f0));
      if (cons.length) t.fail('console: ' + cons.join(' | '));
      if (fl.length) t.fail('failed requests: ' + fl.join(' | '));
    });
  }

  // Phone create (Chromium and WebKit)
  const webkit = await run.browser('webkit');
  for (const [kind, br, slug] of [['chromium', chromium, SP1], ['webkit', webkit, SP2]]) {
    const P = await run.person(br, org, { width: 390, label: 'organiser-phone-' + kind });
    P.page.setDefaultTimeout(10000);
    await run.step('SIM-229', `phone 390px ${kind}: create an event from the form`, P.page, async t => {
      await goto(P.page, '/events/new');
      if (!(await noHScroll(P.page))) t.fail('horizontal scroll on create form: ' + (await overflowers(P.page)).join(', '));
      await createEventViaForm(P.page, { slug, title: 'E2E Phone ' + kind + ' ' + stamp });
      created.push(slug);
      if (!(await noHScroll(P.page))) t.fail('horizontal scroll on manage view: ' + (await overflowers(P.page)).join(', '));
      const err = await visibleError(P.page); if (err) t.fail('on-screen error: ' + err);
    });
    await run.step('SIM-234', `phone 390px ${kind}: tracks editor (add, fill challenge/prize, save)`, P.page, async t => {
      if (!P.page.url().includes('/manage')) await goto(P.page, '/e/' + slug + '/manage');
      await mtab(P.page, 'teams');
      await P.page.locator('#track-new-name').fill('Phone track ' + kind);
      await P.page.locator('#track-add').click();
      const card = P.page.locator('#tracks-editor .card').last();
      await card.locator('textarea').nth(0).fill('Build something for phones');
      await card.locator('textarea').nth(1).fill('A phone');
      await P.page.locator('#tracks-save').click();
      const msg = await waitLive(P.page, /saved|track|wrong/i);
      if (!/Tracks saved/.test(msg)) t.fail('save: "' + msg + '"');
      if (!(await noHScroll(P.page))) t.fail('horizontal scroll in tracks editor: ' + (await overflowers(P.page)).join(', '));
      await P.page.screenshot({ path: `${OUT}/organiser-setup-phone-tracks-${kind}.png`, fullPage: true });
    });
    await P.context.close();
    // Free the live-event slot (max 2 at once) before the next phone event.
    deleted[slug] = await deleteEvent(org.api_key, slug).catch(e => 'ERR ' + e.message);
  }

  // ---------------- SIM-230 ----------------
  await goto(pg, '/e/' + S1 + '/manage');
  const pageVals = { title: 'E2E Orgsetup Edited ' + stamp, tagline: 'Tagline ' + stamp, about: 'About line one\nAbout line two', rules: 'Rule one ' + stamp, prizes: 'Prize pool ' + stamp, coc: 'Be kind ' + stamp };
  await run.step('SIM-230', 'Event page tab: edit every field (colour, title, tagline, about, rules, prizes, dates, time zone, code of conduct) and save', pg, async t => {
    await tab(pg, 'page');
    await pg.locator('#pg-color').selectOption('teal');
    await pg.locator('#pg-title').fill(pageVals.title);
    await pg.locator('#pg-tagline').fill(pageVals.tagline);
    await pg.locator('#pg-about').fill(pageVals.about);
    await pg.locator('#pg-rules').fill(pageVals.rules);
    await pg.locator('#pg-prizes').fill(pageVals.prizes);
    await pg.locator('#pg-start').fill(day(10));
    await pg.locator('#pg-end').fill(day(12));
    await pg.locator('#pg-tz').selectOption('Europe/London').catch(async () => pg.locator('#pg-tz').selectOption('UTC'));
    await pg.locator('#pg-coc').fill(pageVals.coc);
    await pg.locator('#pg-save').click();
    const msg = await waitLive(pg, /Saved|wrong|not|use/i);
    if (msg !== 'Saved') t.fail('save message: "' + msg + '"');
    await goto(pg, '/e/' + S1 + '/manage'); await tab(pg, 'page');
    const got = { title: await pg.locator('#pg-title').inputValue(), tagline: await pg.locator('#pg-tagline').inputValue(), about: await pg.locator('#pg-about').inputValue(), rules: await pg.locator('#pg-rules').inputValue(), prizes: await pg.locator('#pg-prizes').inputValue(), coc: await pg.locator('#pg-coc').inputValue(), color: await pg.locator('#pg-color').inputValue(), start: await pg.locator('#pg-start').inputValue(), end: await pg.locator('#pg-end').inputValue(), tz: await pg.locator('#pg-tz').inputValue() };
    for (const k of ['title', 'tagline', 'about', 'rules', 'prizes', 'coc']) if (got[k] !== pageVals[k]) t.fail(`${k} after reload: "${got[k]}"`);
    if (got.color !== 'teal') t.fail('colour after reload: ' + got.color);
    if (got.start !== day(10) || got.end !== day(12)) t.fail(`dates after reload: ${got.start}..${got.end}`);
    t.note('tz after reload ' + got.tz);
  });

  await run.step('SIM-230', 'public page shows the edited Event page fields (organiser, draft)', pg, async t => {
    const txt = await publicText(pg, S1);
    for (const k of ['title', 'tagline', 'rules', 'prizes']) if (!txt.includes(pageVals[k])) t.fail(`public page missing ${k} "${pageVals[k]}"`);
    if (!txt.includes('About line one')) t.fail('public page missing about');
    t.note('status line: ' + ((txt.match(/Not open yet\.?/) || ['(no draft status)'])[0]));
    await goto(pg, '/e/' + S1 + '/manage');
  });

  // Icon
  const iconStep = async (file, label) => run.step('SIM-230', 'event icon: upload ' + label, pg, async t => {
    await tab(pg, 'page');
    await pg.locator('#event-icon-file').setInputFiles(`${FILES}/${file}`);
    const msg = await waitLive(pg, /icon|wrong|Choose/i);
    if (!/Event icon saved/.test(msg)) { t.fail('upload message: "' + msg + '"'); return; }
    if (!(await pg.locator('#event-icon-preview').isVisible())) t.fail('preview not shown');
    const ok = await pg.locator('#event-icon-preview').evaluate(i => i.complete && i.naturalWidth > 0).catch(() => false);
    if (!ok) { await pg.waitForTimeout(1500); if (!(await pg.locator('#event-icon-preview').evaluate(i => i.naturalWidth > 0))) t.fail('preview image did not load'); }
    const P = await run.person(chromium, null, { label: 'visitor' });
    await P.page.goto(pub(S1));
    const src = await P.page.locator('img.event-icon').getAttribute('src').catch(() => null);
    if (!src) t.fail('public page shows no event icon');
    else { const r = await P.page.request.get(new URL(src, pub(S1)).href); t.note(`public icon ${src} -> ${r.status()} ${r.headers()['content-type']}`); if (r.status() !== 200) t.fail('icon url ' + r.status()); }
    await P.context.close();
  });
  await iconStep('icon.png', 'PNG');
  await iconStep('icon.jpg', 'JPEG (replace)');
  await iconStep('icon.webp', 'WebP (replace)');
  await run.step('SIM-230', 'event icon: a file over 128 KB is refused with a clear message', pg, async t => {
    await pg.locator('#event-icon-file').setInputFiles(`${FILES}/big.png`);
    const msg = await waitLive(pg, /128|large|Choose|wrong/i);
    t.note('message: "' + msg + '"');
    if (!/128 KB/.test(msg)) t.fail('no clear size message: "' + msg + '"');
  });
  await run.step('SIM-230', 'event icon: remove (clear)', pg, async t => {
    if (!(await pg.locator('#event-icon-remove').isVisible())) { t.fail('Remove icon button not visible'); return; }
    await pg.locator('#event-icon-remove').click();
    const msg = await waitLive(pg, /removed|wrong/i);
    if (!/Event icon removed/.test(msg)) t.fail('message: "' + msg + '"');
    const preview = await pg.locator('#event-icon-preview').isVisible();
    t.note('preview still visible after remove: ' + preview + ' (src ' + await pg.locator('#event-icon-preview').getAttribute('src') + ')');
    const P = await run.person(chromium, null, { label: 'visitor' });
    await P.page.goto(pub(S1));
    const n = await P.page.locator('img.event-icon').count();
    if (n) t.fail('public page still shows an icon after removing it');
    await P.context.close();
    await goto(pg, '/e/' + S1 + '/manage'); await tab(pg, 'page');
    if (await pg.locator('#event-icon-preview').isVisible()) {
      const nat = await pg.locator('#event-icon-preview').evaluate(i => i.naturalWidth);
      t.note('after reload the preview is visible (naturalWidth ' + nat + ')');
    }
  });

  // Content
  const contentRow = (box, i) => pg.locator(`#${box} .content-row`).nth(i);
  const fld = (row, label) => row.locator('label.fld', { hasText: label }).locator('input, textarea').first();
  await run.step('SIM-230', 'Content: add two sponsors (with logo), two FAQ entries, two schedule items and save', pg, async t => {
    await tab(pg, 'content');
    for (let i = 0; i < 2; i++) {
      await pg.locator('#content-sponsor-add').click();
      const r = contentRow('content-sponsors', i);
      await fld(r, 'Sponsor name').fill(`Sponsor ${i + 1} ${stamp}`);
      await fld(r, 'Tier').fill(i ? 'Silver' : 'Gold');
      await fld(r, 'Website').fill('https://example.com/s' + (i + 1));
      if (i === 0) await fld(r, 'Logo').setInputFiles(`${FILES}/logo.png`);
      await pg.locator('#content-faq-add').click();
      const f = contentRow('content-faq', i);
      await fld(f, 'Question').fill(`Question ${i + 1} ${stamp}?`);
      await fld(f, 'Answer').fill(`Answer ${i + 1}`);
      await pg.locator('#content-schedule-add').click();
      const s = contentRow('content-schedule', i);
      await fld(s, 'What happens').fill(`Session ${i + 1} ${stamp}`);
      await fld(s, 'Details').fill('Details ' + (i + 1));
      await fld(s, 'Starts').fill(`${day(10)}T${i ? '13' : '10'}:00`);
      await fld(s, 'Ends').fill(`${day(10)}T${i ? '14' : '11'}:00`);
    }
    await pg.waitForTimeout(500);
    await pg.locator('#content-save').click();
    const msg = await waitLive(pg, /saved|wrong|use|invalid|must/i);
    if (msg !== 'Content saved') t.fail('save: "' + msg + '"');
  });
  await run.step('SIM-230', 'Content: public page shows sponsors (logo), FAQ and schedule', pg, async t => {
    const txt = await publicText(pg, S1);
    for (const s of [`Sponsor 1 ${stamp}`, `Sponsor 2 ${stamp}`, `Question 1 ${stamp}?`, `Question 2 ${stamp}?`, `Session 1 ${stamp}`, `Session 2 ${stamp}`]) if (!txt.includes(s)) t.fail('public page missing "' + s + '"');
    const logos = await pg.locator('.sponsor img').count(); if (!logos) t.fail('sponsor logo not shown');
    await goto(pg, '/e/' + S1 + '/manage'); await tab(pg, 'content');
  });
  await run.step('SIM-230', 'Content: edit a sponsor, FAQ answer and schedule title; reload keeps edits; public page updates', pg, async t => {
    if ((await pg.locator('#content-sponsors .content-row').count()) !== 2) t.fail('sponsors not reloaded: ' + await pg.locator('#content-sponsors .content-row').count());
    await fld(contentRow('content-sponsors', 1), 'Sponsor name').fill(`Sponsor Two Edited ${stamp}`);
    await fld(contentRow('content-faq', 0), 'Answer').fill('Edited answer ' + stamp);
    await fld(contentRow('content-schedule', 1), 'What happens').fill(`Session Two Edited ${stamp}`);
    await pg.locator('#content-save').click();
    const msg = await waitLive(pg, /saved|wrong|use|invalid|must/i);
    if (msg !== 'Content saved') t.fail('save: "' + msg + '"');
    await goto(pg, '/e/' + S1 + '/manage'); await tab(pg, 'content');
    const v = await fld(contentRow('content-sponsors', 1), 'Sponsor name').inputValue();
    if (v !== `Sponsor Two Edited ${stamp}`) t.fail('sponsor edit lost after reload: ' + v);
    const logoKept = await contentRow('content-sponsors', 0).locator('img').isVisible();
    if (!logoKept) t.fail('sponsor 1 logo not shown after reload');
    const txt = await publicText(pg, S1);
    for (const s of [`Sponsor Two Edited ${stamp}`, `Session Two Edited ${stamp}`]) if (!txt.includes(s)) t.fail('public missing "' + s + '"');
    if (!(await pg.locator('.faq-item', { hasText: 'Question 1' }).innerText().catch(() => '')).length) t.fail('faq missing');
    await goto(pg, '/e/' + S1 + '/manage'); await tab(pg, 'content');
  });
  await run.step('SIM-230', 'Content: reorder sponsors / FAQ / schedule', pg, async t => {
    const ctrls = await pg.locator('#p-content button', { hasText: /^\s*(move up|move down|up|down|reorder|↑|↓)\s*$/i }).count();
    const draggable = await pg.locator('#p-content [draggable=true]').count();
    if (!ctrls && !draggable) t.fail('no way to reorder sponsors, FAQ entries or schedule items: rows only have Remove (no move up/down, no drag handle)');
  });
  await run.step('SIM-230', 'Content: remove one sponsor, one FAQ, one schedule item; save; public page drops them', pg, async t => {
    for (const box of ['content-sponsors', 'content-faq', 'content-schedule']) await contentRow(box, 0).getByRole('button', { name: 'Remove', exact: true }).click();
    await pg.locator('#content-save').click();
    const msg = await waitLive(pg, /saved|wrong|use|invalid|must/i);
    if (msg !== 'Content saved') t.fail('save: "' + msg + '"');
    const txt = await publicText(pg, S1);
    for (const s of [`Sponsor 1 ${stamp}`, `Question 1 ${stamp}?`, `Session 1 ${stamp}`]) if (txt.includes(s)) t.fail('public page still shows removed "' + s + '"');
    for (const s of [`Sponsor Two Edited ${stamp}`, `Question 2 ${stamp}?`, `Session Two Edited ${stamp}`]) if (!txt.includes(s)) t.fail('public page lost kept "' + s + '"');
    await goto(pg, '/e/' + S1 + '/manage');
  });

  // Settings
  await run.step('SIM-230', 'Settings tab: deadline, entry requirements, gallery, organiser details; save each', pg, async t => {
    await tab(pg, 'settings');
    await pg.locator('#set-deadline').fill(`${day(11)}T18:00`);
    await pg.locator('#set-deadline-save').click();
    let msg = await waitLive(pg, /saved|deadline|wrong|must|before/i);
    t.note('deadline save: "' + msg + '"');
    if (/err/.test(await liveClass(pg))) t.fail('deadline save error: "' + msg + '"');
    for (const id of ['er-title', 'er-description', 'er-code_url']) {
      await pg.locator('#' + id).check(); await pg.waitForTimeout(800);
      if (/err/.test(await liveClass(pg)) && await liveText(pg)) t.fail(`tick ${id}: ${await liveText(pg)}`);
    }
    if (!(await pg.locator('#entry-req-saved').isVisible())) t.note('"Saved" next to entry requirements not visible after ticking');
    await pg.locator('#set-gallery').check(); await pg.waitForTimeout(1000);
    if (/err/.test(await liveClass(pg)) && await liveText(pg)) t.fail('gallery tick: ' + await liveText(pg));
    await pg.locator('#set-name').fill('E2E Organiser Renamed');
    await pg.locator('#set-org').fill('E2E Org Renamed ' + stamp);
    await pg.locator('#set-email').fill('e2e-renamed@example.com');
    await pg.locator('#set-purpose').fill('Edited purpose ' + stamp);
    await pg.locator('#set-n').fill('75');
    await pg.locator('#set-save').click();
    msg = await waitLive(pg, /Saved|wrong|must|invalid/i);
    if (!/Saved/.test(msg)) t.fail('organiser save: "' + msg + '"');
    await goto(pg, '/e/' + S1 + '/manage'); await tab(pg, 'settings');
    const after = { dl: await pg.locator('#set-deadline').inputValue(), name: await pg.locator('#set-name').inputValue(), org: await pg.locator('#set-org').inputValue(), n: await pg.locator('#set-n').inputValue(), title: await pg.locator('#er-title').isChecked(), gallery: await pg.locator('#set-gallery').isChecked(), purpose: await pg.locator('#set-purpose').inputValue() };
    t.note(JSON.stringify(after));
    if (!after.dl.startsWith(day(11))) t.fail('deadline after reload: ' + after.dl);
    if (after.name !== 'E2E Organiser Renamed' || after.n !== '75' || !after.title || !after.gallery || after.purpose !== 'Edited purpose ' + stamp) t.fail('settings not all kept after reload');
  });
  await run.step('SIM-230', 'Settings: public page shows the new organiser line and deadline', pg, async t => {
    const txt = await publicText(pg, S1);
    if (!txt.includes('E2E Organiser Renamed')) t.fail('organiser name not on public page');
    if (!txt.includes('E2E Org Renamed ' + stamp)) t.fail('organisation not on public page');
    if (!/Entries close/.test(txt)) t.fail('deadline not on public page');
    await goto(pg, '/e/' + S1 + '/manage');
  });
  await run.step('SIM-230', 'signed-out visitor: what the draft public page shows', null, async t => {
    const P = await run.person(chromium, null, { label: 'visitor' });
    const r = await P.page.goto(pub(S1));
    const txt = await P.page.locator('body').innerText();
    t.note(`status ${r.status()}, robots ${r.headers()['x-robots-tag'] || '-'}; shows title=${txt.includes(pageVals.title)} about=${txt.includes('About line one')} sponsors=${txt.includes('Sponsor Two Edited')} status-line="${(txt.match(/Not open yet\.?/) || [''])[0]}"`);
    await P.page.screenshot({ path: `${OUT}/organiser-setup-draft-visitor.png`, fullPage: true });
    await P.context.close();
  });

  // ---------------- SIM-231 (event still draft) ----------------
  await run.step('SIM-231', 'Event page: "Website on your event address" offers a way to upload a custom site in the web UI', pg, async t => {
    await tab(pg, 'page');
    const box = pg.locator('#p-page .box', { hasText: 'Website on your event address' });
    const files = await box.locator('input[type=file]').count();
    const upload = await box.locator('button, a', { hasText: /upload|choose|publish/i }).count();
    t.note('box text: ' + (await box.innerText()).replace(/\s+/g, ' '));
    if (!files && !upload) t.fail('no upload control: the box only says "Publish a custom website through the event website API or connector"; an organiser on the website cannot upload a site');
  });
  await run.step('SIM-231', 'Custom website chosen before anything is published: clear message', pg, async t => {
    await pg.locator('#website-custom').click();
    const msg = await waitLive(pg, /publish|website|wrong/i);
    t.note('message: "' + msg + '" pressed=' + await pg.locator('#website-custom').getAttribute('aria-pressed'));
    if (!/publish/i.test(msg)) t.fail('unclear message: "' + msg + '"');
  });
  await run.step('SIM-231', 'publish a tiny custom site while draft (REST: no upload in UI) and it serves on the event address', pg, async t => {
    const r = await api(org.api_key, 'PUT', `/v1/hack/events/${S1}/website/files?create=1`, { files: { 'index.html': fs.readFileSync(`${FILES}/site/index.html`, 'utf8') } });
    t.note('PUT website/files -> ' + r.status + ' ' + JSON.stringify(r.data).slice(0, 200));
    if (r.status >= 300) { t.fail('publish refused while draft: ' + r.status + ' ' + JSON.stringify(r.data)); return; }
    const P = await run.person(chromium, null, { label: 'visitor' });
    const resp = await P.page.goto(pub(S1)).catch(e => ({ status: () => 'ERR ' + e.message.split('\n')[0] }));
    const txt = await P.page.locator('body').innerText().catch(() => '');
    t.note('visitor on event address: ' + resp.status() + ' "' + txt.slice(0, 80) + '"');
    if (!txt.includes('E2E custom site v1')) t.fail('custom site not served on ' + pub(S1) + ' while draft');
    await P.context.close();
    await goto(pg, '/e/' + S1 + '/manage'); await tab(pg, 'page');
    const line = await pg.locator('#website-mode-line').innerText();
    if (!/custom website is live/i.test(line)) t.fail('manage page mode line after publish: "' + line + '"');
    if ((await pg.locator('#website-custom').getAttribute('aria-pressed')) !== 'true') t.fail('Custom website chip not pressed');
  });
  await run.step('SIM-231', 'switch to Built-in page in the UI while draft; event address shows the built-in page', pg, async t => {
    await pg.locator('#website-builtin').click();
    const msg = await waitLive(pg, /updated|wrong|publish/i);
    if (!/Event website updated/.test(msg)) t.fail('switch: "' + msg + '"');
    const P = await run.person(chromium, null, { label: 'visitor' });
    await P.page.goto(pub(S1)); const txt = await P.page.locator('body').innerText();
    if (txt.includes('E2E custom site v1') || !txt.includes(pageVals.title)) t.fail('event address does not show the built-in page after switching');
    await P.context.close();
  });
  await run.step('SIM-231', 'switch back to Custom website in the UI while draft; event address shows the custom site', pg, async t => {
    await pg.locator('#website-custom').click();
    const msg = await waitLive(pg, /updated|wrong|publish/i);
    if (!/Event website updated/.test(msg)) t.fail('switch: "' + msg + '"');
    const P = await run.person(chromium, null, { label: 'visitor' });
    await P.page.goto(pub(S1)); const txt = await P.page.locator('body').innerText();
    if (!txt.includes('E2E custom site v1')) t.fail('custom site not shown after switching back');
    await P.context.close();
  });
  await run.step('SIM-231', 'version history: publish v2 (REST) and look for versions in the UI', pg, async t => {
    const r = await api(org.api_key, 'PUT', `/v1/hack/events/${S1}/website/files`, { files: { 'index.html': '<!doctype html><title>E2E custom</title><h1>E2E custom site v2</h1>' } });
    const st = await api(org.api_key, 'GET', `/v1/hack/events/${S1}/website`);
    t.note(`v2 PUT ${r.status}; GET website ${JSON.stringify(st.data)}`);
    const P = await run.person(chromium, null, { label: 'visitor' });
    await P.page.goto(pub(S1)); const txt = await P.page.locator('body').innerText();
    if (!txt.includes('v2')) t.fail('v2 not served');
    await P.context.close();
    await goto(pg, '/e/' + S1 + '/manage'); await tab(pg, 'page');
    const hasVersions = await pg.locator('#p-page', { hasText: /version/i }).count();
    t.note('manage page mentions versions: ' + !!hasVersions);
  });
  await run.step('SIM-231', 'back to Built-in page for the remaining checks', pg, async t => {
    await pg.locator('#website-builtin').click();
    const msg = await waitLive(pg, /updated|wrong/i);
    if (!/updated/.test(msg)) t.fail('switch: "' + msg + '"');
  });

  // ---------------- SIM-232 ----------------
  const ann = { title: 'E2E announcement ' + stamp, body: 'Hello participants.\nSecond line.' };
  await run.step('SIM-232', 'post an announcement with email OFF (draft event)', pg, async t => {
    await tab(pg, 'content');
    await pg.locator('#content-announce-title').fill(ann.title);
    await pg.locator('#content-announce-body').fill(ann.body);
    if (await pg.locator('#content-announce-email').isChecked()) await pg.locator('#content-announce-email').uncheck();
    await pg.locator('#content-announce-send').click();
    const msg = await waitLive(pg, /posted|wrong|not/i);
    if (msg !== 'Announcement posted.') t.fail('post: "' + msg + '"');
    if (!(await pg.locator('#content-announcements', { hasText: ann.title }).count())) t.fail('announcement not listed');
  });
  await run.step('SIM-232', 'edit / delete a posted announcement (if offered)', pg, async t => {
    const btns = await pg.locator('#content-announcements button').allInnerTexts();
    t.note('controls on posted announcement: ' + JSON.stringify(btns));
    if (!btns.length) t.fail('no edit or delete on a posted announcement: a typo cannot be fixed or withdrawn from the website');
  });
  let joinUrl = '';
  await run.step('SIM-232', 'open the event (stage Open from Overview)', pg, async t => {
    await tab(pg, 'overview');
    const opts = await pg.locator('#ov-stage option').allInnerTexts();
    t.note('stage options: ' + opts.join(', '));
    const openVal = await pg.locator('#ov-stage option').filter({ hasText: /^Open/ }).first().getAttribute('value');
    await pg.locator('#ov-stage').selectOption(openVal || 'open');
    const msg = await waitLive(pg, /updated|wrong|not|must/i);
    if (msg !== 'Stage updated') t.fail('stage change: "' + msg + '"');
    joinUrl = (await pg.locator('#ov-join-url').innerText()).trim();
    if (!/\/join\//.test(joinUrl)) t.fail('no join link: ' + joinUrl);
  });
  const P1 = await run.person(chromium, p1, { label: 'participant-1' });
  await run.step('SIM-232', 'participant joins via the join link from Overview', P1.page, async t => {
    await goto(P1.page, joinUrl);
    await P1.page.locator('#join-name').fill('E2E Participant One');
    await P1.page.locator('#join-coc').check();
    await P1.page.locator('#join-btn').click();
    await P1.page.waitForURL(u => u.pathname.includes('/e/' + S1 + '/team'), { timeout: 15000 }).catch(async () => t.fail('did not reach team view: ' + await liveText(P1.page)));
    await dismissTours(P1.page);
  });
  await run.step('SIM-232', 'participant sees the announcement on their event view', P1.page, async t => {
    await P1.page.locator('#ev-updates').waitFor({ state: 'visible', timeout: 8000 }).catch(() => {});
    const txt = await P1.page.locator('#ev-updates').innerText().catch(() => '');
    if (!txt.includes(ann.title) || !txt.includes('Second line')) t.fail('announcement not on event view: "' + txt.slice(0, 200) + '"');
  });
  await run.step('SIM-232', 'announcement on the public page (participant and signed-out visitor)', P1.page, async t => {
    const txt = await publicText(P1.page, S1);
    if (!txt.includes(ann.title)) t.fail('announcement not on public page for participant');
    const V = await run.person(chromium, null, { label: 'visitor' });
    await V.page.goto(pub(S1)); const vt = await V.page.locator('body').innerText();
    if (!vt.includes(ann.title)) t.fail('announcement not on public page for signed-out visitor');
    await V.context.close();
  });

  // ---------------- SIM-234 tracks ----------------
  await goto(pg, '/e/' + S1 + '/manage');
  const addTrack = async name => {
    await pg.locator('#track-new-name').fill(name); await pg.locator('#track-add').click(); await pg.waitForTimeout(300);
    return liveText(pg);
  };
  const saveTracks = async () => { await pg.locator('#live').evaluate(e => { e.textContent = ''; }); await pg.locator('#tracks-save').click(); return waitLive(pg, /saved|track|wrong|name|must|use/i); };
  const tracksNow = async () => (await api(org.api_key, 'GET', `/v1/hack/events/${S1}/tracks`)).data || [];
  const T1 = 'Track 1: Quality Education (SDG 4)', T2 = 'Track 2: Good Health and Wellbeing (SDG 3)', T3 = 'Track 3: Gender Equality (SDG 5)', T4 = 'Café Ñandú für Kinder 🚀', T5a = 'Health & Wellbeing', T5b = 'Health / Wellbeing';
  await run.step('SIM-234', 'add and save "' + T1 + '"', pg, async t => {
    await tab(pg, 'teams'); await addTrack(T1);
    const msg = await saveTracks(); if (msg !== 'Tracks saved.') t.fail('save: "' + msg + '"');
  });
  await run.step('SIM-234', 'add and save "' + T2 + '" (SIM-214 expected)', pg, async t => {
    await addTrack(T2);
    const slug = await pg.locator('#tracks-editor .card').last().locator('.hint').innerText();
    const msg = await saveTracks();
    t.note(slug + ' (' + slug.replace('Track: ', '').length + ' chars) -> "' + msg + '"');
    if (msg !== 'Tracks saved.') { t.fail('save: "' + msg + '" (SIM-214)'); await pg.locator('#tracks-editor .card').last().getByRole('button', { name: 'Remove track' }).click(); }
  });
  await run.step('SIM-234', 'add and save "' + T3 + '"', pg, async t => {
    await addTrack(T3); const msg = await saveTracks(); if (msg !== 'Tracks saved.') t.fail('save: "' + msg + '"');
  });
  await run.step('SIM-234', 'add and save a name with accents and an emoji "' + T4 + '"', pg, async t => {
    await addTrack(T4); const msg = await saveTracks(); if (msg !== 'Tracks saved.') t.fail('save: "' + msg + '"');
    const got = (await tracksNow()).find(x => x.name === T4); if (!got) t.fail('name not stored exactly'); else t.note('slug ' + got.slug);
  });
  await run.step('SIM-234', `two tracks differing only in punctuation: "${T5a}" and "${T5b}"`, pg, async t => {
    let m = await addTrack(T5a); if (/distinct/.test(m)) t.fail(`"${T5a}" refused: ${m}`);
    m = await addTrack(T5b); if (/distinct/i.test(m)) t.fail(`"${T5b}" refused at Add: "${m}" (both names slugify to health-wellbeing)`);
    const msg = await saveTracks(); if (msg !== 'Tracks saved.') t.fail('save: "' + msg + '"');
    const names = (await tracksNow()).map(x => x.name);
    t.note('saved names: ' + JSON.stringify(names));
    if (!names.includes(T5a) || !names.includes(T5b)) t.fail('both punctuation variants not saved');
  });
  await run.step('SIM-234', 'edit challenge and prize of a saved track; public page shows them', pg, async t => {
    const card = pg.locator('#tracks-editor .card', { has: pg.locator('input') }).filter({ hasText: 'track-1-quality' }).first();
    await card.locator('textarea').nth(0).fill('Challenge for track 1 ' + stamp);
    await card.locator('textarea').nth(1).fill('Prize for track 1 ' + stamp);
    const msg = await saveTracks(); if (msg !== 'Tracks saved.') t.fail('save: "' + msg + '"');
    const txt = await publicText(pg, S1);
    if (!txt.includes('Challenge for track 1 ' + stamp) || !txt.includes('Prize for track 1 ' + stamp)) t.fail('public page missing challenge/prize');
    if (!txt.includes(T1)) t.fail('public page missing track name');
    await goto(pg, '/e/' + S1 + '/manage'); await tab(pg, 'teams');
  });
  await run.step('SIM-234', 'rename a saved track (keeps its slug, public page shows new name)', pg, async t => {
    const card = pg.locator('#tracks-editor .card').filter({ hasText: 'track-1-quality' }).first();
    await card.locator('input').first().fill('Track 1: Quality Education Renamed');
    const msg = await saveTracks(); if (msg !== 'Tracks saved.') t.fail('save: "' + msg + '"');
    const tr = (await tracksNow()).find(x => x.slug.startsWith('track-1-quality'));
    if (!tr || tr.name !== 'Track 1: Quality Education Renamed') t.fail('rename not stored: ' + JSON.stringify(tr));
    const txt = await publicText(pg, S1);
    if (!txt.includes('Track 1: Quality Education Renamed')) t.fail('public page still shows old name');
    await goto(pg, '/e/' + S1 + '/manage'); await tab(pg, 'teams');
  });
  await run.step('SIM-234', 'rename a track to a name already used by another track', pg, async t => {
    const card = pg.locator('#tracks-editor .card').filter({ hasText: 'track-3-gender' }).first();
    await card.locator('input').first().fill(T5a);
    const msg = await saveTracks();
    t.note('duplicate display name save: "' + msg + '"');
    const names = (await tracksNow()).map(x => x.name);
    if (names.filter(n => n === T5a).length > 1) t.note('two tracks now share the name "' + T5a + '"; teams cannot tell them apart in the dropdown');
    await card.locator('input').first().fill(T3); await saveTracks();
  });
  await run.step('SIM-234', 'participant starts a team and chooses Track 3', P1.page, async t => {
    await goto(P1.page, '/e/' + S1 + '/team');
    await P1.page.locator('#start-team-name').fill('E2E Team ' + stamp);
    await P1.page.locator('#start-team-form button[type=submit]').click();
    await P1.page.locator('#ev-team').waitFor({ state: 'visible', timeout: 10000 });
    const opts = await P1.page.locator('#team-track option').allInnerTexts();
    t.note('track options: ' + JSON.stringify(opts));
    const v = await P1.page.locator('#team-track option', { hasText: 'Gender Equality' }).first().getAttribute('value');
    await P1.page.locator('#team-track').selectOption(v);
    await P1.page.locator('#team-track-save').click();
    const msg = await waitLive(P1.page, /saved|wrong|track/i);
    if (msg !== 'Track choice saved.') t.fail('choose: "' + msg + '"');
  });
  await run.step('SIM-234', 'organiser removes the track a team has chosen', pg, async t => {
    await goto(pg, '/e/' + S1 + '/manage'); await tab(pg, 'teams');
    const before = await pg.locator('#teams-list').innerText();
    t.note('teams list shows track before: ' + /Gender/.test(before));
    const card = pg.locator('#tracks-editor .card').filter({ hasText: 'track-3-gender' }).first();
    const c0 = run.console.length;
    await card.getByRole('button', { name: 'Remove track' }).click();
    await pg.waitForTimeout(500);
    const dlg = await pg.locator('[data-sh-dlg="ok"]:visible').count();
    t.note('confirmation shown before removing a chosen track: ' + !!dlg);
    if (dlg) await confirmDialog(pg);
    const msg = await saveTracks();
    t.note('save: "' + msg + '"');
    if (msg !== 'Tracks saved.' && !/team/i.test(msg)) t.fail('save: "' + msg + '"');
    if (!dlg && msg === 'Tracks saved.') t.note('no warning that a team had chosen this track; the team\'s choice is cleared silently');
    const team = await api(p1.api_key, 'GET', `/v1/hack/events/${S1}`);
    const tr = team.data && team.data.me && team.data.me.team && team.data.me.team.track;
    t.note('team track after removal: ' + JSON.stringify(tr));
  });
  await run.step('SIM-234', 'participant view after their track was removed', P1.page, async t => {
    await goto(P1.page, '/e/' + S1 + '/team');
    const sel = await P1.page.locator('#team-track').inputValue().catch(() => '?');
    const opts = await P1.page.locator('#team-track option').allInnerTexts();
    t.note(`selected="${sel}" options=${JSON.stringify(opts)}`);
    const err = await visibleError(P1.page); if (err) t.fail('error: ' + err);
    if (opts.some(o => /Gender/.test(o))) t.fail('removed track still offered');
  });
  await run.step('SIM-234', 'remove a track that no team chose and save', pg, async t => {
    await goto(pg, '/e/' + S1 + '/manage'); await tab(pg, 'teams');
    const card = pg.locator('#tracks-editor .card').filter({ hasText: /health/ }).last();
    await card.getByRole('button', { name: 'Remove track' }).click();
    const msg = await saveTracks(); if (msg !== 'Tracks saved.') t.fail('save: "' + msg + '"');
  });

  // ---------------- SIM-233 co-organisers ----------------
  let inv1 = '';
  await run.step('SIM-233', 'create an organiser invitation link (People tab)', pg, async t => {
    await goto(pg, '/e/' + S1 + '/manage'); await tab(pg, 'people');
    await pg.locator('#coorg-invite').click();
    const msg = await waitLive(pg, /Invitation|wrong/i);
    inv1 = (await pg.locator('#coorg-url').innerText()).trim();
    t.note(`msg "${msg}" url ${inv1.replace(/[^/]+$/, '<code>')} expiry "${await pg.locator('#coorg-expiry').innerText()}"`);
    if (!/\/organiser\//.test(inv1)) t.fail('no invitation url shown');
  });
  const C = await run.person(chromium, co, { label: 'co-organiser' });
  await run.step('SIM-233', 'co-organiser accepts the invitation (code of conduct) and lands in manage', C.page, async t => {
    await goto(C.page, inv1);
    const title = await C.page.locator('#join-step-title').innerText().catch(() => '');
    t.note('join title: "' + title + '"');
    if (await C.page.locator('#join-dead').isVisible()) { t.fail('fresh invitation shows "does not work any more"'); return; }
    await C.page.locator('#join-name').fill('E2E Co Organiser');
    await C.page.locator('#join-coc').check();
    await C.page.locator('#join-btn').click();
    await C.page.waitForURL(u => u.pathname.includes('/e/' + S1 + '/manage'), { timeout: 15000 }).catch(async () => t.fail('did not reach manage: ' + await liveText(C.page) + ' url ' + C.page.url()));
    await dismissTours(C.page);
  });
  await run.step('SIM-233', 'both can manage: co-organiser edits the tagline, organiser sees it', C.page, async t => {
    await tab(C.page, 'page');
    await C.page.locator('#pg-tagline').fill('Edited by co-organiser ' + stamp);
    await C.page.locator('#pg-save').click();
    const msg = await waitLive(C.page, /Saved|wrong|not/i);
    if (msg !== 'Saved') t.fail('co-organiser save: "' + msg + '"');
    await goto(pg, '/e/' + S1 + '/manage'); await tab(pg, 'page');
    if ((await pg.locator('#pg-tagline').inputValue()) !== 'Edited by co-organiser ' + stamp) t.fail('organiser does not see the co-organiser edit');
    await goto(C.page, '/events');
    if (!(await C.page.locator('#events-list a.card[href*="' + S1 + '/manage"]').count())) t.fail('event not in co-organiser Your events as organiser');
  });
  await run.step('SIM-233', 'the used invitation cannot be reused by someone else', null, async t => {
    const P = await run.person(chromium, p2, { label: 'participant-2' });
    await goto(P.page, inv1);
    const dead = await P.page.locator('#join-dead').isVisible();
    t.note('used invitation shows dead page: ' + dead);
    if (!dead) t.fail('used invitation still shows a join form');
    await P.context.close();
  });
  let inv2 = '', inv3 = '';
  await run.step('SIM-233', 'create a new invitation, then another: the previous link stops working', pg, async t => {
    await tab(pg, 'people');
    await pg.locator('#coorg-invite').click(); await waitLive(pg, /Invitation|wrong/i); inv2 = (await pg.locator('#coorg-url').innerText()).trim();
    await pg.locator('#live').evaluate(e => { e.textContent = ''; });
    await pg.locator('#coorg-invite').click(); await waitLive(pg, /Invitation|wrong/i); inv3 = (await pg.locator('#coorg-url').innerText()).trim();
    if (!inv2 || inv2 === inv3) t.fail('new invitation did not change the link');
    const P = await run.person(chromium, p2, { label: 'participant-2' });
    await goto(P.page, inv2);
    const dead = await P.page.locator('#join-dead').isVisible();
    if (!dead) {
      t.fail('replaced invitation still shows a join form');
      await P.page.locator('#join-name').fill('E2E P2'); await P.page.locator('#join-coc').check(); await P.page.locator('#join-btn').click();
      await P.page.waitForTimeout(3000); t.note('accepting replaced link: url ' + P.page.url() + ' live "' + await liveText(P.page) + '"');
    }
    await goto(P.page, inv3);
    t.note('latest invitation shows a join form: ' + await P.page.locator('#join-form').isVisible());
    await P.context.close();
  });
  await run.step('SIM-233', 'revoke the invitation: the latest link stops working', pg, async t => {
    await pg.locator('#coorg-revoke').click();
    const msg = await waitLive(pg, /revoked|wrong|no/i);
    if (msg !== 'Invitation revoked.') t.fail('revoke: "' + msg + '"');
    const P = await run.person(chromium, p2, { label: 'participant-2' });
    await goto(P.page, inv3);
    if (!(await P.page.locator('#join-dead').isVisible())) t.fail('revoked invitation still shows a join form');
    await P.context.close();
  });
  await run.step('SIM-233', 'remove the co-organiser from People', pg, async t => {
    await goto(pg, '/e/' + S1 + '/manage'); await tab(pg, 'people');
    const row = pg.locator('#people-list > *', { hasText: 'E2E Co Organiser' }).first();
    if (!(await row.count())) { t.fail('co-organiser not listed in People: ' + (await pg.locator('#people-list').innerText()).slice(0, 300)); return; }
    await row.getByRole('button', { name: 'Remove' }).click();
    await confirmDialog(pg);
    const msg = await waitLive(pg, /Removed|wrong|not/i);
    if (msg !== 'Removed') t.fail('remove: "' + msg + '"');
    if (await pg.locator('#people-list', { hasText: 'E2E Co Organiser' }).count()) t.fail('co-organiser still listed after removal');
  });
  await run.step('SIM-233', 'removed co-organiser loses access (manage view, Your events, API write)', C.page, async t => {
    await goto(C.page, '/e/' + S1 + '/manage');
    await C.page.waitForTimeout(1500);
    const url = C.page.url();
    const manageVisible = await C.page.locator('#view-manage').isVisible();
    const errTitle = await C.page.locator('#err-title:visible').innerText().catch(() => '');
    t.note(`url ${url}; manage visible ${manageVisible}; error "${errTitle}" "${await C.page.locator('#err-body').innerText().catch(() => '')}"`);
    if (manageVisible && await C.page.locator('#pg-title').inputValue().catch(() => '')) t.fail('removed co-organiser still sees the manage view with event data');
    await goto(C.page, '/events');
    if (await C.page.locator('#events-list a.card[href*="' + S1 + '"]').count()) t.fail('event still in removed co-organiser Your events');
  });

  await run.step('SIM-232', 'phone 390px WebKit: participant event view shows the announcement without overflow', null, async t => {
    const W = await run.person(webkit, p1, { width: 390, label: 'participant-1-phone-webkit' });
    await goto(W.page, '/e/' + S1 + '/team');
    const txt = await W.page.locator('#ev-updates').innerText().catch(() => '');
    if (!txt.includes(ann.title)) t.fail('announcement not shown on phone');
    if (!(await noHScroll(W.page))) t.fail('horizontal scroll: ' + (await overflowers(W.page)).join(', '));
    await W.context.close();
  });
}

let cleaning = false;
async function cleanup() {
  if (cleaning) return; cleaning = true;
  for (const slug of [S1, SP1, SP2]) {
    if (deleted[slug]) { console.log('delete', slug, deleted[slug], '(deleted after its phone steps)'); continue; }
    const st = await deleteEvent(org.api_key, slug).catch(e => 'ERR ' + e.message);
    console.log('delete', slug, st, created.includes(slug) ? '' : '(not recorded as created)');
  }
}
process.on('SIGTERM', async () => { await cleanup(); await run.done(); process.exit(1); });
try { await main(); }
catch (e) { console.error('main threw', e); }
finally {
  await cleanup();
  await run.done();
}
