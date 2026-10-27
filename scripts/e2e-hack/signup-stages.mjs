// SIM-235 sign-up/registration, SIM-238 join + code of conduct,
// SIM-236 stages and deadline, SIM-245 public event page at every stage.
// Account set 3. Run: node scripts/e2e-hack/signup-stages.mjs
import { Run, accounts, stamp, goto, tab, dismissTours, visibleError, createEventViaForm, api, deleteEvent, SITE } from './lib.mjs';

const OUT = process.env.E2E_OUT || '/tmp/claude-1001/-home-ubuntu-workspace-4/2268cadb-dfb1-4db4-9617-f1516b000a56/scratchpad/e2e-prod';
const S = 'SIM-235', J = 'SIM-238', ST = 'SIM-236', PUB = 'SIM-245';
const A = accounts(3);
const ORG = A.organiser[0];
const BUSY = A['co-organiser'][0];
const ONLY = process.env.ONLY || '';
const P = A.participant;
const JD = A.judge;
const slug = 'e2e-signup-' + stamp;
const slugB = 'e2e-signup-' + stamp + 'b';
const created = [];
const run = new Run(process.env.ONLY ? 'signup-stages-' + process.env.ONLY : 'signup-stages', OUT);
const sleep = ms => new Promise(r => setTimeout(r, ms));
const PNG = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAIAAACQkWg2AAAAF0lEQVR4nGP8z8DAwMDAxMDAwMDAAAANHQEDasKb6QAAAABJRU5ErkJggg==';
const day = (n, hm = '18:00') => new Date(Date.now() + n * 864e5).toISOString().slice(0, 10) + 'T' + hm;

async function live(page) { return ((await page.locator('#live').textContent().catch(() => '')) || '').trim(); }
async function clearLive(page) { await page.evaluate(() => { const l = document.getElementById('live'); if (l) l.textContent = ''; }).catch(() => {}); }
async function waitLive(page, re, timeout = 8000) {
  const t0 = Date.now();
  while (Date.now() - t0 < timeout) { const s = await live(page); if (re.test(s)) return s; await sleep(200); }
  return await live(page);
}
// The shared dialog (partials/dialog.html): read it, optionally type, press OK.
async function dialog(page, { type, cancel } = {}) {
  const d = page.locator('dialog.sh-dlg[open]');
  await d.waitFor({ state: 'visible', timeout: 6000 });
  const text = (await d.innerText()).replace(/\s+/g, ' ').trim();
  if (type != null) await d.locator('.sh-dlg-field input').fill(type);
  await d.locator(cancel ? '[data-sh-dlg="cancel"]' : '[data-sh-dlg="ok"]').click();
  return text;
}
async function txt(page, sel) { return ((await page.locator(sel).first().innerText().catch(() => '')) || '').replace(/\s+/g, ' ').trim(); }
async function visible(page, sel) { return page.locator(sel).first().isVisible().catch(() => false); }

// Phone layout: horizontal scroll, things past the right edge, clipped text, overlapping text.
async function layoutProblems(page) {
  return page.evaluate(() => {
    const W = window.innerWidth;
    const out = { overflowX: document.documentElement.scrollWidth - W, wide: [], clipped: [], overlaps: [] };
    const desc = el => el.tagName.toLowerCase() + (el.id ? '#' + el.id : '') + (el.className && typeof el.className === 'string' ? '.' + el.className.trim().split(/\s+/).join('.') : '') + ' "' + (el.textContent || '').trim().slice(0, 40) + '"';
    const vis = el => { const s = getComputedStyle(el); const r = el.getBoundingClientRect(); return s.visibility !== 'hidden' && s.display !== 'none' && r.width > 1 && r.height > 1 && parseFloat(s.opacity) > 0; };
    const all = [...document.body.querySelectorAll('*')].filter(el => !el.closest('dialog:not([open]),[hidden],script,style,noscript,svg') && vis(el));
    for (const el of all) {
      const r = el.getBoundingClientRect();
      if (r.right > W + 1) {
        let p = el.parentElement, contained = false;
        while (p && p !== document.body) { const ox = getComputedStyle(p).overflowX; if (ox === 'auto' || ox === 'scroll' || ox === 'hidden' || ox === 'clip') { contained = true; break; } p = p.parentElement; }
        if (!contained) out.wide.push(desc(el) + ' right=' + Math.round(r.right));
      }
      const s = getComputedStyle(el);
      if (el.children.length === 0 && (el.textContent || '').trim() && (s.overflowX === 'hidden' || s.overflowX === 'clip' || s.textOverflow === 'ellipsis') && el.scrollWidth > el.clientWidth + 1) out.clipped.push(desc(el));
    }
    const leaves = all.filter(el => el.children.length === 0 && (el.textContent || '').trim() && !['OPTION', 'TITLE'].includes(el.tagName)).slice(0, 500);
    const rects = leaves.map(el => el.getBoundingClientRect());
    for (let i = 0; i < leaves.length; i++) for (let k = i + 1; k < leaves.length; k++) {
      const a = rects[i], b = rects[k];
      const w = Math.min(a.right, b.right) - Math.max(a.left, b.left), h = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top);
      if (w > 4 && h > 4 && !leaves[i].contains(leaves[k]) && !leaves[k].contains(leaves[i])) out.overlaps.push(desc(leaves[i]) + ' x ' + desc(leaves[k]));
    }
    out.wide = out.wide.slice(0, 6); out.clipped = out.clipped.slice(0, 6); out.overlaps = out.overlaps.slice(0, 6);
    return out;
  });
}
function reportLayout(t, where, L) {
  if (L.overflowX > 1) t.fail(`${where}: page scrolls sideways by ${L.overflowX}px`);
  if (L.wide.length) t.fail(`${where}: past the right edge: ${L.wide.join(' | ')}`);
  if (L.clipped.length) t.fail(`${where}: text cut off: ${L.clipped.join(' | ')}`);
  if (L.overlaps.length) t.fail(`${where}: text overlaps: ${L.overlaps.join(' | ')}`);
}

const linkCache = new Map();
async function urlStatus(u) {
  if (linkCache.has(u)) return linkCache.get(u);
  let s;
  try {
    const ac = new AbortController(); const tm = setTimeout(() => ac.abort(), 20000);
    const r = await fetch(u, { redirect: 'follow', signal: ac.signal, headers: { 'User-Agent': 'Mozilla/5.0 (e2e link check) Chrome/140' } });
    clearTimeout(tm); s = r.status;
  } catch (e) { s = 'ERR ' + String(e.message || e).slice(0, 80); }
  linkCache.set(u, s); return s;
}

// The public event page as a signed-out visitor.
async function publicPage(stage, browser, width, label, expect = {}) {
  const name = `public page at ${stage} (${label}, ${width}px)`;
  let v;
  await run.step(PUB, name, null, async t => {
    v = await run.person(browser, null, { width, label: 'visitor-' + label });
    const page = v.page;
    linkCache.clear();
    const resp = await page.goto(`https://${slug}.simple-hack.app/`, { waitUntil: 'domcontentloaded' });
    await page.waitForLoadState('networkidle', { timeout: 15000 }).catch(() => {});
    if (!resp || resp.status() >= 400) t.fail(`page answered ${resp && resp.status()}`);
    const status = await txt(page, '.hero .status');
    const heads = (await page.locator('main section > h2').allInnerTexts()).map(s => s.trim());
    const count = await txt(page, '.hero .count');
    const deadline = await txt(page, '#deadline-countdown');
    t.note(`status="${status}" sections=[${heads.join(', ')}] count="${count}" deadline="${deadline}"`);
    if (expect.status && status !== expect.status) t.fail(`status line "${status}", expected "${expect.status}"`);
    for (const h of expect.sections || []) if (!heads.includes(h)) t.fail(`missing section "${h}"`);
    for (const h of expect.absent || []) if (heads.includes(h)) t.fail(`section "${h}" shown but should not be`);
    if (expect.count && !count) t.fail('no team/participant count line');
    if (expect.deadline && !deadline) t.fail('no "Entries close" line');
    if (expect.text) { const body = await page.locator('main').innerText(); for (const s of expect.text) if (!body.includes(s)) t.fail(`page does not say "${s}"`); }
    // Every link and image answers < 400, and images actually decoded.
    const links = await page.evaluate(() => [...document.querySelectorAll('a[href]')].map(a => a.href));
    const imgs = await page.evaluate(() => [...document.querySelectorAll('img')].map(i => ({ src: i.currentSrc || i.src, ok: i.complete && i.naturalWidth > 0, vis: !!(i.offsetWidth || i.offsetHeight) })));
    const urls = [...new Set([...links, ...imgs.map(i => i.src)])].filter(u => /^https?:/.test(u));
    const bad = [];
    await Promise.all(urls.map(async u => { const s = await urlStatus(u.split('#')[0]); if (typeof s !== 'number' || s >= 400) bad.push(`${s} ${u}`); }));
    for (const i of imgs) if (!i.ok && i.vis) bad.push(`image did not load: ${String(i.src).slice(0, 90)}`);
    t.note(`${links.length} links, ${imgs.length} images checked`);
    if (bad.length) t.fail('broken: ' + bad.join(' ; '));
    if (width < 500) reportLayout(t, 'phone', await layoutProblems(page));
    if (!v || !run.results.at(-1).ok) await page.screenshot({ path: `${OUT}/signup-stages-pub-${stage}-${label}-${width}.png`, fullPage: true }).catch(() => {});
  });
  // keep the screenshot path in the result
  const r = run.results.at(-1); if (!r.ok) r.screenshot = `${OUT}/signup-stages-pub-${stage}-${label}-${width}.png`;
  if (v) await v.context.close().catch(() => {});
}

async function setStage(org, stage, { cancel = false } = {}) {
  await goto(org.page, `/e/${slug}/manage`);
  await tab(org.page, 'overview');
  await clearLive(org.page);
  await org.page.selectOption('#ov-stage', stage);
  let dlg = '';
  if (stage === 'closed' || stage === 'archived') dlg = await dialog(org.page, { cancel }).catch(() => '');
  const msg = await waitLive(org.page, /Stage updated|\S/, 10000);
  await sleep(400);
  return { msg, dlg, value: await org.page.inputValue('#ov-stage'), help: await txt(org.page, '#ov-stage-help'), next: await txt(org.page, '#ov-next-action'), options: await org.page.locator('#ov-stage option').allInnerTexts(), disabled: await org.page.locator('#ov-stage').isDisabled() };
}

async function joinViaUI(p, url, { name, answers = [], coc = true, expectLand = true } = {}) {
  await goto(p.page, url);
  await p.page.locator('#join-form-wrap').waitFor({ state: 'visible', timeout: 15000 });
  await p.page.fill('#join-name', name);
  const qs = p.page.locator('#join-questions input[data-question]');
  const n = await qs.count();
  for (let i = 0; i < n; i++) await qs.nth(i).fill(answers[i] || '');
  if (coc) await p.page.check('#join-coc');
  await p.page.click('#join-btn');
  if (expectLand) await p.page.waitForURL(u => u.pathname === `/e/${slug}/team`, { timeout: 20000 });
  return n;
}

// What a participant sees on /e/<slug>/team, and whether the entry can be edited.
async function participantView(p, { tryEdit = false, label = '' } = {}) {
  const page = p.page;
  await goto(page, `/e/${slug}/team`);
  await page.locator('#view-event, .err, #view-error').first().waitFor({ state: 'visible', timeout: 15000 }).catch(() => {});
  await sleep(1200);
  const v = {
    stage: await txt(page, '#ev-stage'),
    approval: (await visible(page, '#ev-approval')) ? await txt(page, '#ev-approval-line') : '',
    startTeam: await visible(page, '#start-team-form'),
    joinTeam: await visible(page, '#join-team-form'),
    shut: (await visible(page, '#ev-join-shut')) ? await txt(page, '#ev-join-shut') : '',
    fixed: await visible(page, '#ev-fixed'),
    team: (await visible(page, '#ev-team')) ? await txt(page, '#team-name') : '',
    leave: await visible(page, '#team-leave'),
    trackSave: (await visible(page, '#team-track-save')) && !(await page.locator('#team-track-save').isDisabled()),
    entrySave: (await visible(page, '#en-save')) && !(await page.locator('#en-save').isDisabled()),
    entryStatus: await txt(page, '#entry-status'),
    pubClosed: (await visible(page, '#pub-closed')) ? await txt(page, '#pub-closed') : '',
    error: await visibleError(page),
  };
  if (tryEdit && v.entrySave) {
    await page.fill('#en-tagline', 'Edited at ' + label);
    await clearLive(page); await page.click('#en-save');
    v.editResult = await waitLive(page, /\S/, 8000);
  }
  return v;
}

// What a judge sees on /e/<slug>/judge, and whether a score saves.
async function judgeView(j, { tryScore = false } = {}) {
  const page = j.page;
  await goto(page, `/e/${slug}/judge`);
  await page.locator('#judge-projects').waitFor({ state: 'visible', timeout: 15000 }).catch(() => {});
  for (let i = 0; i < 20 && /Loading/.test(await txt(page, '#judge-projects')); i++) await sleep(300);
  await dismissTours(page);
  const v = { stage: await txt(page, '#ev-stage'), line: await txt(page, '#ev-judge-line'), list: (await txt(page, '#judge-projects')).slice(0, 200), scoreButtons: await page.locator('#judge-projects button', { hasText: /score/i }).count(), error: await visibleError(page) };
  if (tryScore && v.scoreButtons) {
    await page.locator('#judge-projects button', { hasText: /score/i }).first().click();
    await page.locator('#judge-score-view').waitFor({ state: 'visible', timeout: 8000 }).catch(() => {});
    await dismissTours(page);
    const chips = page.locator('#jsc-criteria .chip');
    v.chips = await chips.count();
    if (v.chips) {
      const enabled = !(await chips.nth(2).isDisabled().catch(() => true));
      v.chipsEnabled = enabled;
      if (enabled) { await clearLive(page); await chips.nth(2).click(); v.scoreResult = await waitLive(page, /\S/, 4000); }
      v.scoreError = await visibleError(page);
    }
  }
  return v;
}

let org, coorg, chromium, webkit;
const people = {};
let joinUrl = '', oldJoinUrl = '', judgeUrl = '';
let sitePromise = Promise.resolve('not started'), sitePublished = false, eventMadeAt = 0;
const QUESTIONS8 = ['* What do you want to build?', 'Your company, school or community', '* T-shirt size', 'Dietary needs', 'How did you hear about us?', 'Any accessibility needs?', 'Your GitHub or portfolio link', 'Anything else we should know?'];
const QUESTIONS3 = ['* What do you want to build?', 'Your company or school', '* T-shirt size'];

try {
  chromium = await run.browser('chromium');
  webkit = await run.browser('webkit');
  org = await run.person(chromium, ORG, { label: 'organiser' });
  await org.context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: SITE });
  const person = async (i, opts = {}) => (people[i] ||= await run.person(opts.browser || chromium, P[i], { width: opts.width || 1280, label: 'p' + (i + 1) + (opts.width ? '@' + opts.width : '') }));

  if (ONLY !== 'delete') {
  // ---------------- draft ----------------
  await run.step(ST, 'organiser creates the event through the form (stage starts as Draft)', org.page, async t => {
    await createEventViaForm(org.page, { slug, title: 'E2E Signup ' + stamp });
    created.push(slug); eventMadeAt = Date.now();
    await goto(org.page, `/e/${slug}/manage`);
    await tab(org.page, 'overview');
    const v = await org.page.inputValue('#ov-stage');
    if (v !== 'draft') t.fail('stage is ' + v);
    t.note('stage options: ' + (await org.page.locator('#ov-stage option').allInnerTexts()).join(' / '));
    t.note('help: ' + await txt(org.page, '#ov-stage-help') + ' | next: ' + await txt(org.page, '#ov-next-action'));
  });

  await run.step(S, 'Overview: copy the participant join link (draft shows "works once Open")', org.page, async t => {
    joinUrl = await txt(org.page, '#ov-join-url');
    judgeUrl = await txt(org.page, '#ov-judge-url');
    if (!/^https:\/\/.+\/join\/\S+$/.test(joinUrl)) t.fail('join link looks wrong: ' + joinUrl);
    if (!(await visible(org.page, '#ov-join-draft'))) t.fail('no "works once Open" hint at draft');
    await org.page.click('#ov-join-copy');
    await sleep(500);
    const clip = await org.page.evaluate(() => navigator.clipboard.readText()).catch(e => 'ERR ' + e.message);
    if (clip !== joinUrl) t.fail(`clipboard holds "${clip}", expected the join link`);
    const btn = await txt(org.page, '#ov-join-copy');
    t.note('after click the button says: ' + btn + ' / live: ' + await live(org.page));
  });

  // Content for the public page: another worker's area, so REST setup.
  await run.step(PUB, 'setup (REST): schedule, sponsor with logo, FAQ, announcement (no email), organiser code of conduct, tagline', null, async t => {
    const c = await api(ORG.api_key, 'PUT', `/v1/hack/events/${slug}/content`, {
      sponsors: [{ name: 'Acme Test Sponsor', tier: 'Gold', logo_data: PNG, url: 'https://example.com/' }],
      faq: [{ question: 'Do I need to code?', answer: 'No. Describe your idea and your AI builds it.' }],
      schedule: [{ title: 'Kick-off', description: 'Welcome and team forming', start_at: day(7, '09:00'), end_at: day(7, '10:00') }, { title: 'Demos', description: 'Show what you built', start_at: day(9, '15:00'), end_at: day(9, '17:00') }],
    });
    if (c.status >= 300) t.fail('content PUT ' + c.status + ' ' + JSON.stringify(c.data).slice(0, 200));
    const a = await api(ORG.api_key, 'POST', `/v1/hack/events/${slug}/announcements`, { title: 'Welcome', body: 'Doors open at 9.', email_participants: false });
    if (a.status >= 300) t.fail('announcement POST ' + a.status + ' ' + JSON.stringify(a.data).slice(0, 200));
    const p = await api(ORG.api_key, 'PATCH', `/v1/hack/events/${slug}`, { coc_text: 'Be kind. No harassment. E2E organiser rule.', tagline: 'Automated test event' });
    if (p.status >= 300) t.fail('patch ' + p.status + ' ' + JSON.stringify(p.data).slice(0, 200));
  });

  const baseSections = ['Schedule', 'Sponsors', 'FAQ', 'Announcements'];
  const pubAll = async (stage, expect) => {
    await publicPage(stage, chromium, 1280, 'chromium', expect);
    await publicPage(stage, chromium, 390, 'chromium', expect);
    await publicPage(stage, webkit, 390, 'webkit', expect);
  };
  await pubAll('draft', { status: 'Not open yet.', sections: baseSections });

  await run.step(J, 'join link at Draft: a signed-in person is told the event is not open yet', null, async t => {
    const p = await person(4);
    await goto(p.page, joinUrl);
    await sleep(1000);
    const closed = await txt(p.page, '#join-closed');
    if (!/not open yet/i.test(closed)) t.fail('join page says: ' + (closed || await txt(p.page, '#view-join')).slice(0, 200));
    if (await visible(p.page, '#join-form-wrap')) t.fail('join form is shown at draft');
  });

  // ---------------- SIM-235: registration questions ----------------
  await run.step(S, 'People tab: save eight registration questions', org.page, async t => {
    await goto(org.page, `/e/${slug}/manage`);
    await tab(org.page, 'people');
    await org.page.fill('#signup-questions', QUESTIONS8.join('\n'));
    await clearLive(org.page); await org.page.click('#signup-save');
    const m = await waitLive(org.page, /\S/);
    if (!/Questions saved/.test(m)) t.fail('after Save: "' + m + '"');
    await org.page.reload(); await dismissTours(org.page); await tab(org.page, 'people');
    const back = (await org.page.inputValue('#signup-questions')).split('\n').filter(Boolean);
    if (back.length !== 8) t.fail('after reload ' + back.length + ' questions: ' + back.join(' | '));
  });

  await run.step(S, 'People tab: a ninth question is refused with a clear message, the eight stay', org.page, async t => {
    await org.page.fill('#signup-questions', [...QUESTIONS8, 'Ninth question?'].join('\n'));
    await clearLive(org.page); await org.page.click('#signup-save');
    const m = await waitLive(org.page, /\S/);
    t.note('message: "' + m + '"');
    if (/Questions saved/.test(m)) t.fail('nine questions were saved');
    else if (!/eight|8/i.test(m)) t.fail('message does not say the limit is eight: "' + m + '"');
    else if (/^[a-z]/.test(m)) t.fail('raw server message shown (lower-case, unmapped code too_many_questions): "' + m + '"');
    const reg = await api(ORG.api_key, 'GET', `/v1/hack/events/${slug}/registration`);
    if ((reg.data.questions || []).length !== 8) t.fail('server now has ' + (reg.data.questions || []).length);
  });

  await run.step(S, 'People tab: cut to three questions and turn require-approval on (tick saves at once, survives reload)', org.page, async t => {
    await org.page.fill('#signup-questions', QUESTIONS3.join('\n'));
    await clearLive(org.page); await org.page.click('#signup-save');
    const m = await waitLive(org.page, /\S/);
    if (!/Questions saved/.test(m)) t.fail('save: ' + m);
    await org.page.check('#signup-approval');
    await sleep(1200);
    if (!(await visible(org.page, '#signup-saved'))) t.fail('no "Saved" after ticking approval; live: ' + await live(org.page));
    await org.page.reload(); await dismissTours(org.page); await tab(org.page, 'people');
    if (!(await org.page.isChecked('#signup-approval'))) t.fail('approval not ticked after reload');
    const back = (await org.page.inputValue('#signup-questions')).split('\n').filter(Boolean);
    if (back.length !== 3) t.fail('questions after reload: ' + back.join(' | '));
  });

  // ---------------- open ----------------
  let st;
  await run.step(ST, 'Overview: move Draft -> Open', org.page, async t => {
    st = await setStage(org, 'open');
    t.note(`live="${st.msg}" help="${st.help}" next="${st.next}" options=${st.options.join('/')}`);
    if (st.value !== 'open' || !/Stage updated/.test(st.msg)) t.fail('stage change: ' + st.msg);
    if (await visible(org.page, '#ov-join-draft')) t.fail('"works once Open" hint still shown');
  });

  await run.step(ST, 'judge joins with the judge link (at Open)', null, async t => {
    const j = people.j1 = await run.person(chromium, JD[0], { label: 'j1' });
    await goto(j.page, judgeUrl);
    await j.page.locator('#join-form-wrap').waitFor({ state: 'visible', timeout: 15000 });
    await j.page.fill('#join-name', 'Judge One');
    await j.page.check('#join-coc');
    await j.page.click('#join-btn');
    await j.page.waitForURL(u => u.pathname === `/e/${slug}/judge`, { timeout: 20000 });
  });

  await run.step(J, 'join form: code of conduct (default + organiser text) and the three questions are shown', null, async t => {
    const p = await person(0);
    await goto(p.page, joinUrl);
    await p.page.locator('#join-form-wrap').waitFor({ state: 'visible', timeout: 15000 });
    const coc = await txt(p.page, '#join-coc-default');
    const orgCoc = await txt(p.page, '#join-coc-org');
    const labels = await p.page.locator('#join-questions label').allInnerTexts();
    t.note(`coc ${coc.length} chars; organiser coc "${orgCoc}"; questions: ${labels.join(' | ')}`);
    if (coc.length < 40) t.fail('default code of conduct missing');
    if (!/E2E organiser rule/.test(orgCoc)) t.fail('organiser code of conduct not shown');
    if (labels.length !== 3) t.fail('expected 3 questions, got ' + labels.length);
    if (!/\*/.test(labels[0]) || !/optional/i.test(labels[1])) t.fail('required/optional not marked: ' + labels.join(' | '));
  });

  await run.step(J, 'join without ticking the code of conduct: clear message, not joined', null, async t => {
    const p = people[0];
    await p.page.fill('#join-name', 'Participant One');
    const qs = p.page.locator('#join-questions input[data-question]');
    await qs.nth(0).fill('A tool for hackathon judges'); await qs.nth(2).fill('M');
    await p.page.click('#join-btn');
    const m = await waitLive(p.page, /\S/, 3000);
    if (!/code of conduct/i.test(m)) t.fail('message after Join without tick: "' + m + '"');
    if (!(await p.page.locator('#live').isVisible())) t.fail('message is not visible');
    if (p.page.url().includes('/team')) t.fail('joined anyway');
    const r = await api(P[0].api_key, 'GET', `/v1/hack/events/${slug}`);
    if (r.status === 200) t.fail('server says p1 is a member');
  });

  await run.step(J, 'join with a required question left empty: blocked with a message', null, async t => {
    const p = people[0];
    const qs = p.page.locator('#join-questions input[data-question]');
    await qs.nth(2).fill('');
    await p.page.check('#join-coc');
    await p.page.click('#join-btn');
    await sleep(800);
    const native = await qs.nth(2).evaluate(el => el.validationMessage);
    t.note('browser validation message: "' + native + '"');
    if (p.page.url().includes('/team')) t.fail('joined with a required answer empty');
    if (!native && !(await live(p.page))) t.fail('no message at all');
  });

  await run.step(S, 'participant applies (approval on): answers questions, lands on the event, sees the pending state', null, async t => {
    const p = people[0];
    const qs = p.page.locator('#join-questions input[data-question]');
    await qs.nth(2).fill('M');
    await qs.nth(1).fill('Acme Uni');
    await p.page.click('#join-btn');
    await p.page.waitForURL(u => u.pathname === `/e/${slug}/team`, { timeout: 20000 });
    const v = await participantView(p);
    t.note(JSON.stringify(v));
    if (!/waiting for an organiser/i.test(v.approval)) t.fail('pending message not shown: ' + v.approval);
    if (v.startTeam || v.joinTeam) t.fail('team forms visible while pending');
  });

  await run.step(S, 'two more participants apply through the join link', null, async t => {
    await joinViaUI(await person(1), joinUrl, { name: 'Participant Two', answers: ['A chatbot', 'Beta Corp', 'L'] });
    await joinViaUI(await person(2), joinUrl, { name: 'Participant Three', answers: ['A game', '', 'S'] });
    for (const i of [1, 2]) { const v = await participantView(people[i]); if (!/waiting/i.test(v.approval)) t.fail(`p${i + 1}: ${v.approval}`); }
  });

  await run.step(S, 'organiser People tab: three pending applications with their answers', org.page, async t => {
    await goto(org.page, `/e/${slug}/manage`);
    await tab(org.page, 'people');
    await sleep(800);
    const cards = org.page.locator('#applications-list .card');
    const n = await cards.count();
    const all = await txt(org.page, '#applications-list');
    t.note(all.slice(0, 500));
    if (n !== 3) t.fail('pending cards: ' + n);
    if (!/A tool for hackathon judges/.test(all) || !/Acme Uni/.test(all)) t.fail('answers not shown');
    const stats = await txt(org.page, '#ov-stats');
    t.note('stats: ' + stats);
  });

  await run.step(S, 'organiser approves Participant One and rejects Participant Two', org.page, async t => {
    const card = n => org.page.locator('#applications-list .card', { hasText: n });
    await clearLive(org.page); await card('Participant One').locator('button', { hasText: 'Approve' }).click();
    let m = await waitLive(org.page, /Application|\S/);
    if (!/approved/i.test(m)) t.fail('after approve: ' + m);
    await sleep(500);
    await clearLive(org.page); await card('Participant Two').locator('button', { hasText: 'Reject' }).click();
    m = await waitLive(org.page, /rejected|\S/);
    await sleep(500);
    m = await live(org.page);
    if (!/rejected/i.test(m)) t.fail('after reject: ' + m);
    const left = await org.page.locator('#applications-list .card').count();
    if (left !== 1) t.fail('pending cards left: ' + left);
    const ppl = await txt(org.page, '#people-list');
    t.note('people list: ' + ppl.slice(0, 600));
    const row = (ppl.match(/Participant Two[^]*?Remove/) || [''])[0];
    if (!/reject|not approved/i.test(row)) t.fail('People list shows rejected Participant Two as a plain "Participant" with no status: "' + row + '"');
    const row3 = (ppl.match(/Participant Three[^]*?Remove/) || [''])[0];
    if (!/pending|waiting/i.test(row3)) t.fail('People list shows pending Participant Three with no status: "' + row3 + '"');
  });

  await run.step(S, 'approved participant sees team options; rejected sees "not approved"; third still pending', null, async t => {
    const a = await participantView(people[0]);
    const r = await participantView(people[1]);
    const p3 = await participantView(people[2]);
    t.note('approved: ' + JSON.stringify(a)); t.note('rejected: ' + JSON.stringify(r)); t.note('pending: ' + JSON.stringify(p3));
    if (a.approval || !a.startTeam) t.fail('approved participant cannot start a team');
    if (!/not approved/i.test(r.approval)) t.fail('rejected participant sees: ' + r.approval);
    if (r.startTeam || r.joinTeam) t.fail('rejected participant can see team forms');
    if (!/waiting/i.test(p3.approval)) t.fail('pending participant sees: ' + p3.approval);
  });

  await run.step(S, 'rejected participant opens the join link again: told they were not approved (not "You\'re already in")', people[1].page, async t => {
    const p = people[1];
    await goto(p.page, joinUrl);
    await sleep(1500);
    const already = (await visible(p.page, '#join-already')) ? await txt(p.page, '#join-already') : '';
    const page = await txt(p.page, '#view-join');
    t.note('join page: ' + page.slice(0, 300));
    if (/already in/i.test(already) && !/not approved/i.test(page)) t.fail(`rejected person is told "${already}"`);
  });

  await run.step(S, 'edit a question\'s wording after answers exist: saves; old applications keep the old wording, the join form shows the new one', org.page, async t => {
    await goto(org.page, `/e/${slug}/manage`);
    await tab(org.page, 'people');
    const q = (await org.page.inputValue('#signup-questions')).split('\n');
    q[1] = 'Your company, school or community';
    await org.page.fill('#signup-questions', q.join('\n'));
    await clearLive(org.page); await org.page.click('#signup-save');
    const m = await waitLive(org.page, /\S/);
    if (!/Questions saved/.test(m)) t.fail('save: ' + m);
    await org.page.reload(); await dismissTours(org.page); await tab(org.page, 'people');
    const app = await txt(org.page, '#applications-list');
    t.note('pending application now shows: ' + app.slice(0, 300));
    if (!/Your company or school/.test(app)) t.fail('pending application lost its original wording');
    const p = await person(3);
    await goto(p.page, joinUrl);
    await p.page.locator('#join-form-wrap').waitFor({ state: 'visible', timeout: 15000 });
    const labels = await p.page.locator('#join-questions label').allInnerTexts();
    if (!labels.some(l => /school or community/.test(l))) t.fail('join form still shows old wording: ' + labels.join(' | '));
  });

  await run.step(S, 'turn require-approval off: tick saves; note what the still-pending participant sees', org.page, async t => {
    await org.page.uncheck('#signup-approval');
    await sleep(1200);
    if (!(await visible(org.page, '#signup-saved'))) t.fail('no "Saved"; live: ' + await live(org.page));
    const reg = await api(ORG.api_key, 'GET', `/v1/hack/events/${slug}/registration`);
    if (reg.data.approval_required) t.fail('server still requires approval');
    if ((reg.data.questions || [])[1]?.prompt !== 'Your company, school or community') t.fail('turning approval off changed the questions: ' + JSON.stringify(reg.data.questions));
    const v = await participantView(people[2]);
    t.note('pending participant after approval off: ' + JSON.stringify(v));
    if (/waiting/i.test(v.approval)) t.fail('approval is off, but the person who applied earlier is still stuck: "' + v.approval + '"');
    const apps = await txt(org.page, '#applications-list');
    t.note('applications list: ' + apps.slice(0, 200));
  });

  // ---------------- SIM-238 join as a new person (approval off) ----------------
  await run.step(J, 'new person joins: accepts the code of conduct, answers questions, lands on the event with team options', null, async t => {
    const p = people[3];
    const n = await joinViaUI(p, joinUrl, { name: 'Participant Four', answers: ['Alpha app', 'Delta Labs', 'XL'] });
    const v = await participantView(p);
    t.note(`${n} questions; ` + JSON.stringify(v));
    if (v.approval) t.fail('approval message shown with approval off: ' + v.approval);
    if (!v.startTeam) t.fail('no "Start a team" form');
  });

  await run.step(ST, 'participant starts team "Alpha" at Open (page)', null, async t => {
    const p = people[3];
    await p.page.fill('#start-team-name', 'Alpha');
    await p.page.locator('#start-team-form button[type=submit]').click();
    await p.page.locator('#ev-team').waitFor({ state: 'visible', timeout: 10000 });
    t.note('team: ' + await txt(p.page, '#team-name') + ' ' + await txt(p.page, '#team-code'));
  });

  await run.step(ST, 'setup (REST): team Beta for p1, entries for both teams, Alpha publishes a team site', null, async t => {
    const b = await api(P[0].api_key, 'POST', `/v1/hack/events/${slug}/teams`, { name: 'Beta' });
    if (b.status >= 300) t.fail('Beta: ' + b.status + JSON.stringify(b.data).slice(0, 200));
    for (const [k, title] of [[P[3].api_key, 'Alpha entry'], [P[0].api_key, 'Beta entry']]) {
      const e = await api(k, 'PUT', `/v1/hack/events/${slug}/entry`, { title, tagline: 'Initial tagline', description: 'Built in an automated test.' });
      if (e.status >= 300) t.fail(title + ': ' + e.status + JSON.stringify(e.data).slice(0, 200));
    }
    const key = await api(P[3].api_key, 'POST', `/v1/hack/events/${slug}/key`);
    if (key.status >= 300) { t.fail('team key: ' + key.status + JSON.stringify(key.data).slice(0, 200)); return; }
    const site = key.data.site && key.data.site.name;
    const t0 = Date.now();
    // Team addresses take a while after an event is made: keep trying in the background.
    sitePromise = (async () => {
      let last = '';
      while (Date.now() - t0 < 12 * 60e3) {
        const r = await fetch(SITE + `/v1/sites/${site}/files`, { method: 'POST', headers: { 'X-API-Key': key.data.key, 'Content-Type': 'application/json' }, body: JSON.stringify({ files: { 'index.html': '<!doctype html><title>Alpha</title><h1>Alpha team site</h1>' } }) });
        const d = await r.json().catch(() => ({}));
        last = r.status + ' ' + (d.code || d.site_url || '');
        if (r.status < 300) { sitePublished = true; return `published after ${Math.round((Date.now() - eventMadeAt) / 1000)}s from event creation: ${d.site_url}`; }
        if (d.code !== 'team_sites_not_ready') return 'publish failed: ' + last + JSON.stringify(d).slice(0, 200);
        await sleep(30000);
      }
      return 'still team_sites_not_ready after 12 minutes of retries: ' + last;
    })();
  });

  await run.step(ST, 'Settings: tick "Show entries on the event page" (gallery)', org.page, async t => {
    await goto(org.page, `/e/${slug}/manage`);
    await tab(org.page, 'settings');
    await org.page.check('#set-gallery');
    await sleep(1200);
    const m = await live(org.page);
    t.note('live: ' + m);
    const ev = await api(ORG.api_key, 'GET', `/v1/hack/events/${slug}`);
    if (!ev.data.event.gallery_open) t.fail('gallery_open not saved');
  });

  const stageChecks = async (stage, { participantEdit, judgeScore, pub, alphaEdit }) => {
    await run.step(ST, `at ${stage}: what the organiser sees on Overview`, org.page, async t => {
      await goto(org.page, `/e/${slug}/manage`); await tab(org.page, 'overview');
      t.note(`stage=${await org.page.inputValue('#ov-stage')} help="${await txt(org.page, '#ov-stage-help')}" next="${await txt(org.page, '#ov-next-action')}" options=${(await org.page.locator('#ov-stage option').allInnerTexts()).join('/')} selectDisabled=${await org.page.locator('#ov-stage').isDisabled()} joinNewDisabled=${await org.page.locator('#ov-join-new').isDisabled()}`);
      const e = await visibleError(org.page); if (e) t.fail('error on Overview: ' + e);
    });
    await run.step(ST, `at ${stage}: participant on team Alpha (extended later) - can they edit the entry?`, people[3].page, async t => {
      const v = await participantView(people[3], { tryEdit: true, label: stage });
      t.note(JSON.stringify(v));
      if (participantEdit === true && !v.entrySave) t.fail('entry is locked but should be editable at ' + stage);
      if (participantEdit === true && v.editResult && !/saved/i.test(v.editResult)) t.fail('entry save: ' + v.editResult);
      if (alphaEdit === false && v.editResult && /saved/i.test(v.editResult)) t.fail(`team with extra time could still change its entry at ${stage} ("${v.editResult}")`);
      if (participantEdit === false && v.entrySave && v.editResult && /saved/i.test(v.editResult)) t.fail(`participant could still edit the entry at ${stage}: "${v.editResult}"`);
      if (v.entrySave && v.editResult && !/saved/i.test(v.editResult)) t.fail(`Save button enabled but save failed: "${v.editResult}"`);
    });
    await run.step(ST, `at ${stage}: participant on team Beta - can they edit the entry?`, people[0].page, async t => {
      const v = await participantView(people[0], { tryEdit: true, label: stage });
      t.note(JSON.stringify(v));
      const want = typeof participantEdit === 'object' ? participantEdit.beta : participantEdit;
      if (want === true && !v.entrySave) t.fail('entry is locked but should be editable at ' + stage);
      if (want === false && v.entrySave && v.editResult && /saved/i.test(v.editResult)) t.fail(`participant could still edit the entry at ${stage}: "${v.editResult}"`);
      if (v.entrySave && v.editResult && !/saved/i.test(v.editResult)) t.fail(`Save button enabled but save failed: "${v.editResult}"`);
    });
    await run.step(ST, `at ${stage}: judge - can they score?`, people.j1.page, async t => {
      const v = await judgeView(people.j1, { tryScore: true });
      t.note(JSON.stringify(v));
      if (v.error) t.fail('error: ' + v.error);
      const fut = (v.list.match(/As submitted at (\d+ \w+ \d{4}, \d\d:\d\d)/g) || []).filter(x => Date.parse(x.replace('As submitted at ', '').replace(',', '') + ' UTC') > Date.now());
      if (fut.length) t.fail('judge list says "' + fut[0] + '", a time in the future');
      if (judgeScore === true && !(v.scoreResult !== undefined && !/locked|cannot|not|error|wrong/i.test(v.scoreResult + v.scoreError))) t.fail('judge could not score at ' + stage + ': ' + (v.scoreResult || v.scoreError || 'no score button'));
    });
    people.p16 ||= await run.person(chromium, P[15], { label: 'p16' });
    await run.step(J, `at ${stage}: what the participant join link and judge link say to a new person`, people.p16.page, async t => {
      const p = people.p16;
      await goto(p.page, joinUrl); await sleep(1200);
      const pj = (await visible(p.page, '#join-form-wrap')) ? 'join form' : (await visible(p.page, '#join-closed')) ? await txt(p.page, '#join-closed') : (await visible(p.page, '#join-dead')) ? 'dead link' : (await txt(p.page, '#view-join')).slice(0, 120);
      const j = people.j3 ||= await run.person(chromium, JD[2], { label: 'j3' });
      await goto(j.page, judgeUrl); await sleep(1200);
      const jj = (await visible(j.page, '#join-form-wrap')) ? 'join form' : (await visible(j.page, '#join-closed')) ? await txt(j.page, '#join-closed') : (await txt(j.page, '#view-join')).slice(0, 120);
      t.note(`participant link: ${pj} | judge link: ${jj}`);
      if (['closed', 'judging', 'results'].includes(stage) && /over/i.test(pj)) t.fail(`participant link says "${pj}" while the event is at ${stage}`);
      if (stage === 'results' && /over/i.test(jj)) t.fail(`judge link says "${jj}" at Results`);
    });
    if (pub) await pubAll(stage, pub);
  };

  await stageChecks('open', { participantEdit: true, pub: { status: 'Sign-up is open. Ask the organisers for the join link.', sections: baseSections, count: true } });

  // ---------------- SIM-235/238: new link, old one dies ----------------
  await run.step(S, 'Overview: New link replaces the join link (confirm dialog), new link is shown', org.page, async t => {
    await goto(org.page, `/e/${slug}/manage`); await tab(org.page, 'overview');
    oldJoinUrl = await txt(org.page, '#ov-join-url');
    await clearLive(org.page); await org.page.click('#ov-join-new');
    const d = await dialog(org.page);
    t.note('dialog: ' + d);
    const m = await waitLive(org.page, /\S/);
    joinUrl = await txt(org.page, '#ov-join-url');
    if (!/New link ready/.test(m)) t.fail('after New link: ' + m);
    if (!joinUrl || joinUrl === oldJoinUrl) t.fail('link did not change: ' + joinUrl);
    await org.page.reload(); await dismissTours(org.page); await tab(org.page, 'overview');
    const after = await txt(org.page, '#ov-join-url');
    if (after !== joinUrl) t.fail(`after reload the page shows ${after}, not the new ${joinUrl}`);
  });

  await run.step(J, 'replaced link: signed-in person sees "This link does not work any more"', null, async t => {
    const p = await person(4);
    await goto(p.page, oldJoinUrl);
    await sleep(1200);
    if (!(await visible(p.page, '#join-dead'))) t.fail('dead-link message not shown; page: ' + (await txt(p.page, '#view-join')).slice(0, 200));
    const h = await txt(p.page, '#join-dead h1');
    if (h !== 'This link does not work any more') t.fail('heading: ' + h);
  });

  await run.step(J, 'replaced link: signed-out visitor sees "This link does not work any more"', null, async t => {
    const v = await run.person(chromium, null, { label: 'visitor-oldlink' });
    await goto(v.page, oldJoinUrl);
    await sleep(1200);
    const ok = await visible(v.page, '#join-dead');
    if (!ok) { t.fail('dead-link message not shown to a visitor; page: ' + (await v.page.locator('body').innerText()).slice(0, 200)); await v.page.screenshot({ path: `${OUT}/signup-stages-oldlink-visitor.png`, fullPage: true }); }
    await v.context.close();
  });

  await run.step(S, 'the new join link works (p5 joins with it)', null, async t => {
    await joinViaUI(people[4], joinUrl, { name: 'Participant Five', answers: ['Five app', '', 'M'] });
  });

  await run.step(J, 'six participants join at the same moment (Promise.all)', null, async t => {
    const six = [];
    for (const i of [5, 6, 7, 8, 9, 10]) six.push(await person(i));
    await Promise.all(six.map(async (p, k) => {
      await goto(p.page, joinUrl);
      await p.page.locator('#join-form-wrap').waitFor({ state: 'visible', timeout: 20000 });
      await p.page.fill('#join-name', 'Concurrent ' + (k + 1));
      const qs = p.page.locator('#join-questions input[data-question]');
      await qs.nth(0).fill('Idea ' + k); await qs.nth(2).fill('M');
      await p.page.check('#join-coc');
    }));
    const res = await Promise.allSettled(six.map(async p => { await p.page.click('#join-btn'); await p.page.waitForURL(u => u.pathname === `/e/${slug}/team`, { timeout: 25000 }); }));
    res.forEach((r, k) => { if (r.status !== 'fulfilled') t.fail(`person ${k + 1}: ${String(r.reason).split('\n')[0]}; live: ` + ''); });
    const ppl = await api(ORG.api_key, 'GET', `/v1/hack/events/${slug}/people`);
    const list = Array.isArray(ppl.data) ? ppl.data : (ppl.data.people || []);
    const names = list.map(x => x.display_name);
    const missing = [1, 2, 3, 4, 5, 6].filter(k => !names.includes('Concurrent ' + k));
    if (missing.length) t.fail('missing from People: ' + missing.join(','));
    t.note('people total: ' + list.length);
  });

  const phoneJoin = async (i, browser, label) => {
    await run.step(J, `phone: join at 390px in ${label}`, null, async t => {
      const p = await person(i, { browser, width: 390 });
      await goto(p.page, joinUrl);
      await p.page.locator('#join-form-wrap').waitFor({ state: 'visible', timeout: 15000 });
      reportLayout(t, 'join form', await layoutProblems(p.page));
      await p.page.screenshot({ path: `${OUT}/signup-stages-phone-join-${label}.png`, fullPage: true });
      const btnBox = await p.page.locator('#join-btn').boundingBox();
      if (!btnBox || btnBox.width < 200) t.fail('Join button too small or missing: ' + JSON.stringify(btnBox));
      await p.page.fill('#join-name', 'Phone ' + label);
      const qs = p.page.locator('#join-questions input[data-question]');
      await qs.nth(0).fill('Phone idea'); await qs.nth(2).fill('S');
      await p.page.check('#join-coc');
      await p.page.click('#join-btn');
      await p.page.waitForURL(u => u.pathname === `/e/${slug}/team`, { timeout: 20000 });
      await sleep(1200); await dismissTours(p.page);
      reportLayout(t, 'event page after join', await layoutProblems(p.page));
      await p.page.screenshot({ path: `${OUT}/signup-stages-phone-joined-${label}.png`, fullPage: true });
    });
  };
  await phoneJoin(11, chromium, 'chromium');
  await phoneJoin(12, webkit, 'webkit');

  // ---------------- building + deadlines ----------------
  await run.step(ST, 'Overview: move Open -> Building', org.page, async t => {
    st = await setStage(org, 'building');
    t.note(`live="${st.msg}" help="${st.help}" next="${st.next}"`);
    if (st.value !== 'building' || !/Stage updated/.test(st.msg)) t.fail('stage change: ' + st.msg);
  });

  await run.step(ST, 'Settings: set the overall deadline, then change it', org.page, async t => {
    await goto(org.page, `/e/${slug}/manage`); await tab(org.page, 'settings');
    await org.page.fill('#set-deadline', day(2, '18:00'));
    await clearLive(org.page); await org.page.click('#set-deadline-save');
    let m = await waitLive(org.page, /\S/);
    t.note('first save: ' + m + ' | zone: ' + await txt(org.page, '#set-deadline-zone'));
    let ev = await api(ORG.api_key, 'GET', `/v1/hack/events/${slug}`);
    const d1 = ev.data.event.submission_deadline;
    if (!d1) t.fail('deadline not saved: ' + m);
    await org.page.fill('#set-deadline', day(3, '12:00'));
    await clearLive(org.page); await org.page.click('#set-deadline-save');
    await sleep(300);
    m = await waitLive(org.page, /\S/);
    ev = await api(ORG.api_key, 'GET', `/v1/hack/events/${slug}`);
    const d2 = ev.data.event.submission_deadline;
    t.note(`second save: ${m}; ${d1} -> ${d2}`);
    if (!d2 || d2 === d1) t.fail('deadline did not change');
    await org.page.reload(); await dismissTours(org.page); await tab(org.page, 'settings');
    const shown = await org.page.inputValue('#set-deadline');
    if (shown !== day(3, '12:00')) t.note(`after reload input shows ${shown} (typed ${day(3, '12:00')}; may be zone-shifted)`);
  });

  await run.step(ST, 'participant sees the new deadline on their event page', people[3].page, async t => {
    await goto(people[3].page, `/e/${slug}/team`); await sleep(1200);
    const c = await txt(people[3].page, '#ev-deadline-countdown');
    const sd = await txt(people[3].page, '#site-deadline');
    t.note(`countdown="${c}" site-deadline="${sd}"`);
    if (!c && !sd) t.fail('no deadline shown to the participant');
  });

  await run.step(ST, 'Entries tab: give team Alpha more time (one team\'s deadline)', org.page, async t => {
    await goto(org.page, `/e/${slug}/manage`); await tab(org.page, 'projects');
    await sleep(800);
    const card = org.page.locator('#projects-list .card', { hasText: 'Alpha' }).first();
    await card.locator('button', { hasText: 'Give more time' }).click();
    const form = card.locator('form.more-time');
    t.note('form says: ' + (await form.innerText()).replace(/\s+/g, ' '));
    await form.locator('input[type=datetime-local]').fill(day(4, '12:00'));
    await clearLive(org.page); await form.locator('button[type=submit]').click();
    const m = await waitLive(org.page, /\S/);
    t.note('after save: ' + m);
    if (!/now has until/i.test(m)) t.fail('extension save: ' + m);
    const c2 = await txt(org.page, '#projects-list');
    if (!/More time: until/.test(c2)) t.fail('card does not show the extension');
    const v = await participantView(people[3]);
    const extended = await visible(people[3].page, '#site-extended');
    t.note('Alpha member sees extension note: ' + extended);
    if (!extended) t.fail('Alpha member is not told about the extra time');
  });

  await stageChecks('building', { participantEdit: true, judgeScore: undefined, pub: { status: 'Teams are building.', sections: baseSections, count: true, deadline: true } });

  await run.step(ST, 'team site for Alpha goes live (waits for team addresses; REST publish)', null, async t => {
    const r = await sitePromise; t.note(r);
    if (!sitePublished) t.fail(r);
  });
  await run.step(PUB, 'gallery shows on the public page at Building once Alpha site is live', null, async t => {
    const r = await fetch(`https://${slug}.simple-hack.app/`); const h = await r.text();
    t.note('Entries section on page: ' + /<h2>Entries<\/h2>/.test(h));
  });

  // ---------------- closed ----------------
  await run.step(ST, 'Overview: move Building -> Entries closed (confirm dialog)', org.page, async t => {
    st = await setStage(org, 'closed');
    t.note(`dialog="${st.dlg}" live="${st.msg}" help="${st.help}" next="${st.next}"`);
    if (!st.dlg) t.fail('no confirmation dialog');
    if (st.value !== 'closed' || !/Stage updated/.test(st.msg)) t.fail('stage change: ' + st.msg);
  });
  await run.step(ST, 'at closed: Settings deadline area says entries closed and how to reopen', org.page, async t => {
    await tab(org.page, 'settings');
    const box = await txt(org.page, '#set-deadline-locked');
    t.note(box);
    if (!/Entries closed/.test(box)) t.fail('no "Entries closed" in Settings');
  });
  await stageChecks('closed', { participantEdit: { beta: false }, pub: { status: 'Submissions are closed.', sections: baseSections.concat(sitePublished ? ['Entries'] : []), count: true } });

  // ---------------- judging ----------------
  await run.step(ST, 'Overview: move Entries closed -> Judging', org.page, async t => {
    st = await setStage(org, 'judging');
    t.note(`live="${st.msg}" help="${st.help}" next="${st.next}"`);
    if (st.value !== 'judging' || !/Stage updated/.test(st.msg)) t.fail('stage change: ' + st.msg);
  });
  await stageChecks('judging', { participantEdit: { beta: false }, judgeScore: true, alphaEdit: false, pub: { status: 'Judging is under way.', sections: baseSections.concat(sitePublished ? ['Entries'] : []), count: true } });

  await run.step(ST, 'moving backwards: Judging -> Building is offered and works; note what happens to the deadline', org.page, async t => {
    const before = (await api(ORG.api_key, 'GET', `/v1/hack/events/${slug}`)).data.event.submission_deadline;
    await goto(org.page, `/e/${slug}/manage`); await tab(org.page, 'overview');
    await org.page.selectOption('#ov-stage', 'building');
    const confirmShown = await org.page.locator('dialog.sh-dlg[open]').isVisible().catch(() => false);
    if (confirmShown) await dialog(org.page);
    await sleep(1500);
    st = { msg: await live(org.page), value: await org.page.inputValue('#ov-stage'), help: await txt(org.page, '#ov-stage-help'), next: await txt(org.page, '#ov-next-action'), options: await org.page.locator('#ov-stage option').allInnerTexts() };
    t.note('confirmation before reopening frozen entries and dropping the deadline: ' + confirmShown);
    if (!confirmShown) t.fail('no confirmation: moving back to Building reopens every frozen entry and deletes the deadline in one click');
    const after = (await api(ORG.api_key, 'GET', `/v1/hack/events/${slug}`)).data.event.submission_deadline;
    t.note(`options were ${st.options.join('/')}; live="${st.msg}" help="${st.help}" next="${st.next}" deadline ${before} -> ${after}`);
    if (st.value !== 'building' || !/Stage updated/.test(st.msg)) t.fail('could not move back: ' + st.msg);
    const v = await participantView(people[0], { tryEdit: true, label: 'back-to-building' });
    t.note('Beta member after moving back: ' + JSON.stringify(v));
    if (!v.entrySave) t.fail('Beta still frozen after moving back to Building');
    if (/in Entries/.test(st.next)) {
      await tab(org.page, 'projects');
      const has = await org.page.locator('#p-projects input[type=datetime-local]:visible').count();
      if (!has) t.fail(`Overview says "${st.next}" but the Entries tab has no deadline control (it is in Settings)`);
    }
  });
  await run.step(ST, 'moving backwards: Building -> Draft after the event ran (what the UI allows)', org.page, async t => {
    st = await setStage(org, 'draft');
    t.note(`live="${st.msg}" value=${st.value} help="${st.help}"`);
    const j = await api(P[0].api_key, 'GET', `/v1/hack/join/${joinUrl.split('/').pop()}`);
    t.note('join info joinable=' + (j.data && j.data.joinable));
    // forward again
    const back = await setStage(org, 'judging');
    if (back.value !== 'judging') t.fail('could not go forward to judging again: ' + back.msg);
  });

  await run.step(ST, 'setup (REST): lock judging (judging is another worker\'s area)', null, async t => {
    const l = await api(ORG.api_key, 'POST', `/v1/hack/events/${slug}/judging/lock`);
    t.note('lock ' + l.status + ' ' + JSON.stringify(l.data).slice(0, 150));
    if (l.status >= 300) t.fail('lock ' + l.status);
  });

  // ---------------- results ----------------
  await run.step(ST, 'Overview: move Judging -> Results (before results are published)', org.page, async t => {
    st = await setStage(org, 'results');
    t.note(`live="${st.msg}" help="${st.help}" next="${st.next}"`);
    if (st.value !== 'results' || !/Stage updated/.test(st.msg)) t.fail('stage change: ' + st.msg);
  });
  await run.step(PUB, 'public page at Results before results are published: does it claim results are in?', null, async t => {
    const r = await fetch(`https://${slug}.simple-hack.app/`); const h = await r.text();
    const status = (h.match(/<p class="status">([^<]*)<\/p>/) || [])[1];
    const winners = /<h2>(Winners|Results)<\/h2>/.test(h);
    t.note(`status="${status}" results section=${winners}`);
    if (/Results are in/.test(status || '') && !winners) t.fail(`public page says "${status}" but shows no results (nothing published yet)`);
  });
  await run.step(ST, 'setup (REST): publish results', null, async t => {
    const p = await api(ORG.api_key, 'POST', `/v1/hack/events/${slug}/results/publish`, {});
    t.note('publish ' + p.status + ' ' + JSON.stringify(p.data).slice(0, 200));
    if (p.status >= 300) t.fail('publish results ' + p.status + ' ' + JSON.stringify(p.data).slice(0, 200));
  });
  await stageChecks('results', { participantEdit: { beta: false }, alphaEdit: false, pub: { status: 'Results are in.', sections: baseSections.concat(sitePublished ? ['Entries'] : [], ['Winners']), count: true } });

  await run.step(ST, 'judge link at Results: a new judge cannot join (clear message)', null, async t => {
    const j = people.j2 = await run.person(chromium, JD[1], { label: 'j2' });
    await goto(j.page, judgeUrl);
    await sleep(1500);
    const page = await txt(j.page, '#view-join');
    t.note(page.slice(0, 300));
    if (await visible(j.page, '#join-form-wrap')) {
      await j.page.fill('#join-name', 'Judge Two'); await j.page.check('#join-coc'); await j.page.click('#join-btn');
      const m = await waitLive(j.page, /\S/, 5000);
      t.note('after Join: ' + m + ' url=' + j.page.url());
      if (j.page.url().includes('/judge') && !m) t.fail('a judge could join at Results');
      else if (m && /^[a-z]/.test(m)) t.fail('raw server message: ' + m);
    }
  });

  // ---------------- ended ----------------
  await run.step(ST, 'Overview: move Results -> Ended (confirm), stage control locks', org.page, async t => {
    st = await setStage(org, 'archived');
    t.note(`dialog="${st.dlg}" live="${st.msg}" help="${st.help}" disabled=${st.disabled}`);
    if (!st.dlg) t.fail('no confirmation dialog');
    if (st.value !== 'archived' || !/Stage updated/.test(st.msg)) t.fail('stage change: ' + st.msg);
    if (!st.disabled) t.fail('stage control still enabled after Ended');
  });
  await run.step(ST, 'at Ended: moving backwards is impossible (control disabled, API says ended)', org.page, async t => {
    await goto(org.page, `/e/${slug}/manage`); await tab(org.page, 'overview');
    const dis = await org.page.locator('#ov-stage').isDisabled();
    const r = await api(ORG.api_key, 'POST', `/v1/hack/events/${slug}/stage`, { stage: 'building' });
    t.note(`select disabled=${dis}; API back to building -> ${r.status} ${r.data && r.data.code}`);
    if (!dis) t.fail('stage select is enabled at Ended');
    const rq = await visible(org.page, '#p-overview .ended-note');
    await tab(org.page, 'people');
    t.note('People tab note: ' + await txt(org.page, '#p-people .ended-note') + ' | signup-save disabled=' + await org.page.locator('#signup-save').isDisabled());
  });
  await run.step(J, 'join link at Ended: "This event is over."', null, async t => {
    const p = await person(13);
    await goto(p.page, joinUrl); await sleep(1200);
    const c = await txt(p.page, '#join-closed');
    if (!/over/i.test(c)) t.fail('join page says: ' + (c || (await txt(p.page, '#view-join')).slice(0, 200)));
  });
  await stageChecks('archived', { participantEdit: { beta: false }, pub: { status: 'This event has ended.', sections: baseSections.concat(sitePublished ? ['Entries'] : [], ['Winners']), count: true } });

  }
  // ---------------- delete a busy event ----------------
  // Owned by the co-organiser account of set 3: the organiser account hits the
  // events-per-day cap (too_many_events_today) after a couple of runs.
  const busy = await run.person(chromium, BUSY, { label: 'busy-owner' });
  await run.step(ST, 'setup: busy event B created through the form; three people join, one team (REST)', busy.page, async t => {
    await createEventViaForm(busy.page, { slug: slugB, title: 'E2E Signup busy ' + stamp });
    created.push(slugB);
    const s = await api(BUSY.api_key, 'POST', `/v1/hack/events/${slugB}/stage`, { stage: 'open' });
    if (s.status >= 300) t.fail('open: ' + s.status);
    const ev = await api(BUSY.api_key, 'GET', `/v1/hack/events/${slugB}`);
    const code = (ev.data.organiser.join_url || '').split('/').pop();
    for (const i of [13, 14, 15]) {
      const r = await api(P[i].api_key, 'POST', `/v1/hack/join/${code}`, { accept_coc: true, display_name: 'Busy ' + i, answers: {} });
      if (r.status >= 300) t.fail(`join p${i + 1}: ${r.status} ${JSON.stringify(r.data).slice(0, 120)}`);
    }
    const tm = await api(P[15].api_key, 'POST', `/v1/hack/events/${slugB}/teams`, { name: 'Busy team' });
    if (tm.status >= 300) t.fail('team: ' + tm.status);
  });
  await run.step(ST, 'Settings: delete the busy event - confirmation makes sense, wrong name refused, right name deletes', busy.page, async t => {
    await goto(busy.page, `/e/${slugB}/manage`); await tab(busy.page, 'settings');
    const section = await txt(busy.page, '#set-delete-wrap');
    t.note('section: ' + section);
    await busy.page.click('#set-delete');
    const d = busy.page.locator('dialog.sh-dlg[open]');
    await d.waitFor({ state: 'visible', timeout: 5000 });
    const body = (await d.innerText()).replace(/\s+/g, ' ');
    t.note('dialog: ' + body);
    if (!body.includes(slugB)) t.fail('dialog does not name the event to type');
    if (!/3 participants|three participants|participants/i.test(body)) t.fail('dialog does not mention people');
    await d.locator('.sh-dlg-field input').fill('wrong-name');
    const okBtn = d.locator('[data-sh-dlg="ok"]');
    const okDisabled = await okBtn.isDisabled();
    await okBtn.click().catch(() => {});
    await sleep(500);
    const msg = await txt(busy.page, 'dialog.sh-dlg[open] .sh-dlg-msg');
    t.note(`wrong name: ok disabled=${okDisabled}, message="${msg}"`);
    const stillThere = await api(BUSY.api_key, 'GET', `/v1/hack/events/${slugB}`);
    if (stillThere.status !== 200) t.fail('event deleted after the wrong name');
    if (!(await d.isVisible())) { t.fail('dialog closed after the wrong name'); await busy.page.click('#set-delete'); }
    await d.locator('.sh-dlg-field input').fill(slugB);
    await d.locator('[data-sh-dlg="ok"]').click();
    await busy.page.waitForURL(u => u.pathname === '/events', { timeout: 15000 }).catch(() => t.fail('did not return to Your events; live: '));
    const gone = await api(BUSY.api_key, 'GET', `/v1/hack/events/${slugB}`);
    t.note('GET after delete: ' + gone.status);
    if (gone.status !== 404) t.fail('event still answers ' + gone.status);
    else created.splice(created.indexOf(slugB), 1, slugB + ' (deleted via UI)');
    const list = await txt(busy.page, 'main');
    if (list.includes('E2E Signup busy ' + stamp)) t.fail('deleted event still listed on Your events');
  });
  await run.step(ST, 'after delete: a participant of the deleted event opening it gets a sensible message', null, async t => {
    const p = await person(14);
    await goto(p.page, `/e/${slugB}/team`); await sleep(1200);
    const body = (await p.page.locator('main, body').first().innerText()).replace(/\s+/g, ' ').slice(0, 300);
    t.note(body);
    if (/Something went wrong/i.test(body)) t.fail('generic error: ' + body);
  });
} catch (e) {
  console.log('FATAL ' + (e.stack || e));
} finally {
  const statuses = [];
  for (const s of [slug, slugB]) {
    if (!created.some(c => c === s || c === s + ' (deleted via UI)')) continue;
    if (created.includes(s + ' (deleted via UI)')) { statuses.push(`${s}: deleted via UI`); continue; }
    const k = s === slugB ? BUSY.api_key : ORG.api_key;
    try { statuses.push(`${s}: DELETE ${await deleteEvent(k, s)}`); } catch (e) { statuses.push(`${s}: ${e.message}`); }
    const chk = await api(k, 'GET', `/v1/hack/events/${s}`);
    statuses.push(`${s}: GET after cleanup ${chk.status}`);
  }
  console.log('CLEANUP ' + statuses.join(' ; '));
  await run.done();
}
