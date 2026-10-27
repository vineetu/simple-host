// SIM-215 area "judging-results": SIM-241 (judges), SIM-242 (rubric and
// judging setup), SIM-243 (lock, results, publish, exports), SIM-244
// (gallery, people's choice, directory). Account set 5.
//
// Setup that is another worker's job (opening the event, participants,
// teams, tracks, entries, team sites) goes through REST and is named
// "(setup via REST)". Everything under test is clicked and typed.
import fs from 'node:fs';
import { execSync } from 'node:child_process';
import { Run, accounts, stamp, goto, tab, dismissTours, visibleError, createEventViaForm, api, deleteEvent, SITE } from './lib.mjs';

const OUT = process.env.OUT || '/tmp/claude-1001/-home-ubuntu-workspace-4/2268cadb-dfb1-4db4-9617-f1516b000a56/scratchpad/e2e-prod';
const AREA = 'judging-results';
const run = new Run(AREA, OUT);
const acc = accounts(5);
const ORG = process.env.ORG_ROLE === 'organiser' ? acc.organiser[0] : acc['co-organiser'][0]; // organiser-01 used its 3 events/day
const OUTSIDER = acc['co-organiser'][1]; // signed in, never joins the event
const P = acc.participant;
const J = acc.judge;

const SLUG = 'e2e-judging-' + stamp;
const SLUG_R = 'e2e-judging-r' + stamp; // rubric-replace-after-scores check
const created = [];

const S242 = 'SIM-242', S241 = 'SIM-241', S243 = 'SIM-243', S244 = 'SIM-244';

// Eight teams over two tracks. Every judge gives a team the same points
// (vector per team), so totals do not depend on who judged, and teams 2 and
// 3 tie exactly.
const TEAMS = [
  { name: 'Alpha Ants', title: 'Project Alpha', track: 'ai', v: [5, 5, 5, 5] },
  { name: 'Bravo Bees', title: 'Project Bravo', track: 'ai', v: [4, 4, 4, 4] },
  { name: 'Charlie Crows', title: 'Project Charlie', track: 'ai', v: [4, 4, 4, 4] },
  { name: 'Delta Ducks', title: 'Project Delta', track: 'ai', v: [3, 4, 3, 3] },
  { name: 'Echo Eels', title: 'Project Echo', track: 'climate', v: [3, 3, 3, 3] },
  { name: 'Foxtrot Finches', title: 'Project Foxtrot', track: 'climate', v: [2, 3, 2, 2] },
  { name: 'Golf Geckos', title: 'Project Golf', track: 'climate', v: [2, 2, 2, 2] },
  { name: 'Hotel Hawks', title: 'Project Hotel', track: 'climate', v: [1, 1, 1, 1] },
];
// Final rubric after the edits: Idea 25, Execution 30, Design 20, Impact 25.
const FINAL_RUBRIC = [['Idea', 25], ['Execution', 30], ['Design', 20], ['Impact', 25]];
// Manual plan, edited by hand: five entries per judge, two for the organiser.
const PLAN = { J1: [0, 1, 2, 3, 4], J2: [3, 4, 5, 6, 7], J3: [0, 1, 5, 6, 7], J4: [0, 2, 3, 5, 7], J5: [1, 2, 4, 6, 7], O: [0, 4] };
const ORG_CONFLICT = { judge: 'J5', team: 7 };    // organiser records J5 <-> Hotel Hawks
const JUDGE_CONFLICT = { judge: 'J2', team: 3 };  // J2 skips Delta Ducks with the button
const JUDGE_NAMES = { J1: 'Judge One', J2: 'Judge Two', J3: 'Judge Three', J4: 'Judge Four', J5: 'Judge Five', O: 'E2E Organiser' };

const state = { teams: [], tracks: [], ids: {}, judgeUrl: '', votes: {} };
const titleToIdx = t => TEAMS.findIndex(x => x.title === String(t).trim());

// ---------- helpers ----------
// page.waitForFunction trips the app's CSP (no unsafe-eval); poll with evaluate instead.
async function waitFn(page, fn, arg, opts = {}) {
  const end = Date.now() + ((opts && opts.timeout) || 30000);
  let last;
  while (Date.now() < end) {
    try { if (await page.evaluate(fn, arg)) return true; } catch (e) { last = e; }
    await page.waitForTimeout(150);
  }
  throw new Error('waitFn timeout' + (last ? ': ' + last.message.split('\n')[0] : ''));
}
async function clearLive(page) { await page.evaluate(() => { const n = document.getElementById('live'); if (n) n.textContent = ''; }).catch(() => {}); }
async function clickLive(page, locator, re, timeout = 10000) { await clearLive(page); await locator.click(); return waitLive(page, re, timeout); }
async function live(page) {
  return page.locator('#live').evaluate(n => ({ text: n.textContent.trim(), cls: n.className })).catch(() => ({ text: '', cls: '' }));
}
async function waitLive(page, re, timeout = 10000) {
  const end = Date.now() + timeout;
  let l;
  while (Date.now() < end) { l = await live(page); if (re.test(l.text)) return l; await page.waitForTimeout(150); }
  return l || await live(page);
}
async function confirmDialog(page, timeout = 5000) {
  const dlg = page.locator('dialog.sh-dlg[open]');
  try { await dlg.waitFor({ state: 'visible', timeout }); } catch { return null; }
  const title = (await dlg.locator('.sh-dlg-title').innerText().catch(() => '')).trim();
  const body = (await dlg.locator('.sh-dlg-body').innerText().catch(() => '')).trim();
  await dlg.locator('[data-sh-dlg="ok"]').click();
  await page.waitForTimeout(300);
  return { title, body };
}
async function openSetup(page) {
  const d = page.locator('#jg-setup');
  if (!(await d.evaluate(n => n.open))) await page.locator('#jg-setup > summary').click();
  await page.waitForTimeout(200);
}
async function manage(page, tabName) {
  await goto(page, `/e/${SLUG}/manage`);
  await page.waitForSelector('#tab-overview', { timeout: 15000 });
  if (tabName) await tab(page, tabName);
  if (tabName === 'judging') {
    await waitFn(page, () => document.querySelectorAll('#jg-rubric .member').length > 0, null, { timeout: 15000 }).catch(() => {});
    await page.waitForTimeout(800);
  }
}
async function rubricRows(page) {
  return page.$$eval('#jg-rubric .member', rows => rows.map(r => {
    const ins = r.querySelectorAll('input');
    return { name: ins[0].value, weight: Number(ins[1].value), points: Number(ins[2].value), desc: r.querySelector('textarea').value };
  }));
}
async function dashText(page) {
  return {
    teams: await page.locator('#jg-dash-teams .member').allInnerTexts(),
    judges: await page.locator('#jg-dash-judges .member').allInnerTexts(),
  };
}
async function download(page, locator, label) {
  const [dl] = await Promise.all([page.waitForEvent('download', { timeout: 45000 }), locator.click()]);
  const file = `${OUT}/${AREA}-${label}-${dl.suggestedFilename()}`;
  await dl.saveAs(file);
  return file;
}
function parseCSV(text) {
  const rows = []; let row = [], f = '', q = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (q) { if (c === '"') { if (text[i + 1] === '"') { f += '"'; i++; } else q = false; } else f += c; }
    else if (c === '"') q = true;
    else if (c === ',') { row.push(f); f = ''; }
    else if (c === '\n') { row.push(f.replace(/\r$/, '')); rows.push(row); row = []; f = ''; }
    else f += c;
  }
  if (f || row.length) { row.push(f); rows.push(row); }
  return rows.filter(r => r.length > 1 || r[0] !== '');
}
function localInZone(date, zone) {
  const parts = new Intl.DateTimeFormat('en-GB', { timeZone: zone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts(date);
  const o = {}; parts.forEach(p => { o[p.type] = p.value; });
  return `${o.year}-${o.month}-${o.day}T${o.hour}:${o.minute}`;
}
async function noHScroll(page) {
  return page.evaluate(() => ({ sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth }));
}
async function publicPage(page) {
  // The event's own address first; the built-in path when its certificate is not ready.
  try {
    const r = await page.goto(`https://${SLUG}.simple-hack.app/`, { waitUntil: 'domcontentloaded', timeout: 20000 });
    if (r && r.ok()) return `https://${SLUG}.simple-hack.app/`;
  } catch (e) {}
  await page.goto(`${SITE}/e/${SLUG}`, { waitUntil: 'domcontentloaded' });
  return `${SITE}/e/${SLUG}`;
}

// ---------- setup via REST ----------
async function must(r, what) {
  if (r.status >= 300) throw new Error(`${what}: ${r.status} ${JSON.stringify(r.data).slice(0, 200)}`);
  return r.data;
}
async function setupMain() {
  const k = ORG.api_key;
  await must(await api(k, 'PUT', `/v1/hack/events/${SLUG}/tracks`, { tracks: [
    { slug: 'ai', name: 'AI for good', challenge: 'Use AI to help people.', prize: 'GPU credits' },
    { slug: 'climate', name: 'Climate', challenge: 'Cut emissions.', prize: 'Tree planting' }] }), 'tracks');
  await must(await api(k, 'POST', `/v1/hack/events/${SLUG}/stage`, { stage: 'open' }), 'stage open');
  let ev = await must(await api(k, 'GET', `/v1/hack/events/${SLUG}`), 'get event');
  const t0 = Date.now();
  while (!ev.event.team_sites_ready && Date.now() - t0 < 600000) { await new Promise(r => setTimeout(r, 10000)); ev = await must(await api(k, 'GET', `/v1/hack/events/${SLUG}`), 'get event'); }
  state.readySecs = Math.round((Date.now() - t0) / 1000);
  const joinCode = ev.organiser.join_code;
  state.tracks = ev.event.tracks;
  // 32 participants join, 4 per team.
  await Promise.all(P.map((p, i) => api(p.api_key, 'POST', `/v1/hack/join/${joinCode}`, { accept_coc: true, display_name: `Participant ${i + 1}` }).then(r => must(r, 'join ' + i))));
  const siteFails = [];
  await Promise.all(TEAMS.map(async (t, ti) => {
    const lead = P[ti * 4];
    const team = await must(await api(lead.api_key, 'POST', `/v1/hack/events/${SLUG}/teams`, { name: t.name }), 'team ' + t.name);
    for (let m = 1; m < 4; m++) await must(await api(P[ti * 4 + m].api_key, 'POST', `/v1/hack/events/${SLUG}/teams/join`, { code: team.code }), 'join team');
    await must(await api(lead.api_key, 'PUT', `/v1/hack/events/${SLUG}/team/track`, { track: t.track }), 'track');
    await must(await api(lead.api_key, 'PUT', `/v1/hack/events/${SLUG}/entry`, {
      title: t.title, tagline: `${t.name} tagline`, description: `What ${t.name} built. Automated test entry.`,
      video_url: 'https://example.com/video', code_url: 'https://example.com/code' }), 'entry');
    const key = await must(await api(lead.api_key, 'POST', `/v1/hack/events/${SLUG}/key`), 'team key');
    const keyStr = key.key;
    const d = await api(keyStr, 'PUT', `/v1/sites/${team.slug}/files?create=1`, { files: { 'index.html': `<!doctype html><title>${t.name}</title><h1>${t.name} site</h1><p>${t.title}</p>` } });
    if (d.status >= 300) siteFails.push(`${t.name}: ${d.status} ${JSON.stringify(d.data).slice(0, 160)}`);
    state.teams[ti] = { ...t, slug: team.slug };
  }));
  const tl = await must(await api(k, 'GET', `/v1/hack/events/${SLUG}/teams`), 'teams');
  const list = tl.teams || tl;
  for (const t of state.teams) { const m = list.find(x => x.slug === t.slug); t.id = m && m.id; }
  return siteFails;
}

// ---------- main ----------
let orgB, orgP;
try {
  const chromium = await run.browser('chromium');
  orgP = await run.person(chromium, ORG, { label: 'organiser' });
  const op = orgP.page;

  await run.step(S242, 'Create the event with the form', op, async t => {
    await createEventViaForm(op, { slug: SLUG, title: 'E2E Judging ' + stamp });
    created.push(SLUG);
  });
  if (!created.includes(SLUG)) throw new Error('event not created');

  // ===== SIM-242: rubric (event still in draft; nothing scored) =====
  await run.step(S242, 'Judging tab: read the default rubric', op, async t => {
    await manage(op, 'judging');
    const open = await op.locator('#jg-setup').evaluate(n => n.open);
    if (!open) t.note('"Rubric and setup" was collapsed on a fresh draft event; opened it');
    await openSetup(op);
    const rows = await rubricRows(op);
    t.note('default rubric: ' + rows.map(r => `${r.name} ${r.weight}% 0-${r.points}`).join(', '));
    const want = ['Idea', 'Execution', 'Design', 'Demo'];
    if (rows.map(r => r.name).join() !== want.join()) t.fail('default rubric names: ' + rows.map(r => r.name).join());
    if (rows.some(r => r.weight !== 25 || r.points !== 5)) t.fail('default weights/points are not 25%/5');
    const w = (await op.locator('#jg-weight').innerText()).trim();
    if (!/Total weight: 100%/.test(w)) t.fail('weight line: ' + w);
  });

  await run.step(S242, 'Rubric: add a criterion, rename one, reweight, save', op, async t => {
    await openSetup(op);
    await op.locator('#jg-add').click();
    let rows = op.locator('#jg-rubric .member');
    if (await rows.count() !== 5) t.fail('Add a criterion did not add a row: ' + await rows.count());
    const row = i => rows.nth(i);
    await row(4).locator('input').nth(0).fill('Impact');
    await row(4).locator('textarea').fill('Does it matter to real people?');
    await row(3).locator('input').nth(0).fill('Pitch'); // rename Demo
    // Reweight: Idea 20, Execution 30, Design 20, Pitch 10, Impact 20.
    for (const [i, w] of [[0, 20], [1, 30], [2, 20], [3, 10], [4, 20]]) await row(i).locator('input').nth(1).fill(String(w));
    const w = (await op.locator('#jg-weight').innerText()).trim();
    if (!/Total weight: 100%$/.test(w)) t.fail('weight line after reweight: ' + w);
    const l = await clickLive(op, op.locator('#jg-rubric-save'), /saved|must|error|wrong/i);
    if (!/Rubric saved/.test(l.text)) t.fail('save said: ' + l.text);
    await manage(op, 'judging'); await openSetup(op);
    const after = await rubricRows(op);
    t.note('after reload: ' + after.map(r => `${r.name} ${r.weight}%`).join(', '));
    if (after.map(r => r.name).join() !== 'Idea,Execution,Design,Pitch,Impact') t.fail('names after reload: ' + after.map(r => r.name).join());
    if (after.map(r => r.weight).join() !== '20,30,20,10,20') t.fail('weights after reload: ' + after.map(r => r.weight).join());
  });

  await run.step(S242, 'Rubric: weights not adding to 100 are refused with a message', op, async t => {
    await openSetup(op);
    await op.locator('#jg-rubric .member').nth(0).locator('input').nth(1).fill('35');
    const w = (await op.locator('#jg-weight').innerText()).trim();
    t.note('weight line: ' + w);
    if (!/must add up to 100/.test(w)) t.fail('no hint while weights add to 115: ' + w);
    const l = await clickLive(op, op.locator('#jg-rubric-save'), /100|saved/i);
    if (!/add up to 100/.test(l.text)) t.fail('save with 115% said: ' + JSON.stringify(l.text));
  });

  await run.step(S242, 'Rubric: remove a criterion, reweight to the final rubric, save', op, async t => {
    await manage(op, 'judging'); await openSetup(op);
    const rows = op.locator('#jg-rubric .member');
    await rows.nth(3).locator('button', { hasText: 'Remove' }).click(); // Pitch
    if (await rows.count() !== 4) t.fail('Remove did not remove the row');
    for (let i = 0; i < 4; i++) await rows.nth(i).locator('input').nth(1).fill(String(FINAL_RUBRIC[i][1]));
    const l = await clickLive(op, op.locator('#jg-rubric-save'), /saved|must|error/i);
    if (!/Rubric saved/.test(l.text)) t.fail('save said: ' + l.text);
    await manage(op, 'judging'); await openSetup(op);
    const after = await rubricRows(op);
    const got = after.map(r => `${r.name}:${r.weight}`).join();
    if (got !== FINAL_RUBRIC.map(x => x.join(':')).join()) t.fail('rubric after reload: ' + got);
  });

  // ===== setup: open, people, teams, entries, sites =====
  await run.step(S242, 'Open the event, 32 participants in 8 teams on 2 tracks, entries and team sites (setup via REST)', null, async t => {
    const fails = await setupMain();
    if (fails.length) t.fail('team site deploys failed: ' + fails.join(' | '));
    t.note(`team sites ready after ${state.readySecs}s; teams: ` + state.teams.map(x => `${x.name}=${x.slug}`).join(', '));
    if (state.teams.some(x => !x.id)) t.fail('a team id is missing');
  });

  // ===== SIM-241: five judges join at once from the Overview link =====
  await run.step(S241, 'Organiser copies "Link for judges" from Overview', op, async t => {
    await manage(op, 'overview');
    state.judgeUrl = (await op.locator('#ov-judge-url').innerText()).trim();
    t.note('judge link: ' + state.judgeUrl.replace(/\/judge\/.*/, '/judge/<code>'));
    if (!/^https:\/\/.+\/judge\/[A-Za-z0-9-]+$/.test(state.judgeUrl)) t.fail('judge link looks wrong: ' + state.judgeUrl);
  });

  const judges = {};
  for (let i = 0; i < 5; i++) judges['J' + (i + 1)] = await run.person(chromium, J[i], { label: 'judge-' + (i + 1) });
  await run.step(S241, 'Five judges open the judge link and join at the same time', judges.J1.page, async t => {
    const res = await Promise.all(Object.entries(judges).map(async ([id, jp]) => {
      const pg = jp.page;
      await goto(pg, state.judgeUrl);
      await pg.locator('#join-form').waitFor({ state: 'visible', timeout: 15000 });
      const btn = (await pg.locator('#join-btn').innerText()).trim();
      await pg.locator('#join-name').fill(JUDGE_NAMES[id]);
      await pg.locator('#join-coc').check();
      await pg.locator('#join-btn').click();
      try { await pg.waitForURL(u => u.pathname.endsWith(`/e/${SLUG}/judge`), { timeout: 20000 }); }
      catch { return `${id}: did not reach the judge page (${pg.url()}) ${await visibleError(pg)} ${(await live(pg)).text}`; }
      await dismissTours(pg);
      const line = (await pg.locator('#ev-judge-line').innerText().catch(() => '')).trim();
      return btn !== 'Join as a judge' ? `${id}: join button said "${btn}"` : (/as a judge/.test(line) ? '' : `${id}: judge line "${line}"`);
    }));
    res.filter(Boolean).forEach(x => t.fail(x));
    const people = await api(ORG.api_key, 'GET', `/v1/hack/events/${SLUG}/people`);
    const list = (people.data && (people.data.people || people.data)) || [];
    for (const [id, jp] of Object.entries(judges)) {
      const m = list.find(p => p.display_name === JUDGE_NAMES[id] && p.role === 'judge');
      if (!m) t.fail(`${id} is not a judge in People`); else state.ids[id] = m.user_id;
    }
    const me = list.find(p => p.role === 'organiser'); if (me) state.ids.O = me.user_id;
  });

  // ===== SIM-242: replacing the rubric after scores exist =====
  await run.step(S242, 'Rubric change after a score exists warns that scores are cleared (score by Judge Five via REST)', op, async t => {
    const k5 = J[4].api_key;
    const rub = await must(await api(k5, 'GET', `/v1/hack/events/${SLUG}/rubric`), 'rubric');
    const team = state.teams[0].id;
    await must(await api(k5, 'PUT', `/v1/hack/events/${SLUG}/judge/scores/${team}`, { scores: rub.criteria.map(c => ({ criterion_id: c.id, points: 3 })), comment: 'early score' }), 'scores');
    await manage(op, 'judging');
    const d0 = await dashText(op);
    t.note('before: ' + d0.teams.filter(x => /Alpha/.test(x)).join(''));
    await openSetup(op);
    await op.locator('#jg-rubric .member').nth(0).locator('textarea').fill('How original is it? (clarified)');
    await clearLive(op);
    await op.locator('#jg-rubric-save').click();
    const dlg = await confirmDialog(op, 3000);
    const l = await waitLive(op, /saved|error|locked/i);
    t.note('dialog: ' + (dlg ? `"${dlg.title}" ${dlg.body}` : 'none') + ' ; message: ' + l.text);
    const after = await api(k5, 'GET', `/v1/hack/events/${SLUG}/judge/scores/${team}`);
    const n = (after.data.scores || []).length;
    t.note(`Judge Five's scores for Alpha Ants after the save: ${n} (were 4); comment ${JSON.stringify(after.data.comment)}`);
    await op.locator('#jg-refresh').click(); await op.waitForTimeout(1000);
    t.note('dashboard after: ' + (await dashText(op)).teams.filter(x => /Alpha/.test(x)).join(''));
    if (!dlg) t.fail(`Save rubric gave no warning and ${n === 0 ? 'silently deleted all 4 of Judge Five\'s scores (and the comment)' : 'kept ' + n + ' scores'}; only a description was edited`);
    else if (!/score/i.test(dlg.title + dlg.body)) t.fail('the warning does not mention scores: ' + dlg.title + ' ' + dlg.body);
  });

  // ===== SIM-242: how judges are assigned =====
  await run.step(S242, 'Assignment: Open mode, every judge can score every team (dashboard)', op, async t => {
    await manage(op, 'judging'); await openSetup(op);
    await op.locator('#jg-mode-open').check();
    const l = await clickLive(op, op.locator('#jg-settings-save'), /Saved|error|wrong/i);
    if (!/Saved/.test(l.text)) t.fail('save said: ' + l.text);
    await op.locator('#jg-refresh').click(); await op.waitForTimeout(1200);
    const d = await dashText(op);
    t.note('judges: ' + d.judges.join(' | '));
    if (d.judges.length !== 6) t.fail(`dashboard lists ${d.judges.length} judges, expected 6 (5 judges + organiser)`);
    if (d.judges.some(x => !/0 of 8 done/.test(x))) t.fail('open mode should be "0 of 8 done" for each judge');
    if (d.teams.length !== 8) t.fail('dashboard lists ' + d.teams.length + ' teams');
  });

  let generated = 0;
  await run.step(S242, 'Assignment: Automatic, 2 judges per team, Generate assignments', op, async t => {
    await op.locator('#jg-mode-automatic').check();
    if (await op.locator('#jg-per-wrap').isHidden()) t.fail('"Judges per team" did not appear');
    await op.locator('#jg-per').fill('2');
    let l = await clickLive(op, op.locator('#jg-settings-save'), /Saved|error|wrong/i);
    if (!/Saved/.test(l.text)) t.fail('save said: ' + l.text);
    await op.locator('#jg-generate').click();
    await op.locator('#jg-assign-note').waitFor({ state: 'visible', timeout: 10000 }).catch(() => {});
    const note = (await op.locator('#jg-assign-note').innerText().catch(() => '')).trim();
    t.note('note: ' + note);
    generated = Number((note.match(/^(\d+) assignment/) || [])[1] || 0);
    if (generated !== 16) t.fail('expected 16 assignments (8 teams x 2), note says: ' + note);
    await op.waitForTimeout(1200);
    const d = await dashText(op);
    t.note('judges: ' + d.judges.join(' | '));
    const sum = d.judges.reduce((s, x) => s + Number((x.match(/of (\d+) done/) || [])[1] || 0), 0);
    if (sum !== generated) t.fail(`dashboard assigned counts add to ${sum}, generated ${generated}`);
    if (d.judges.some(x => /E2E Organiser/.test(x) && !/of 0 done/.test(x))) t.note('the organiser was given entries by Generate (organisers count as judges)');
  });

  await run.step(S242, 'Assignment: switch to Manual, the grid shows the generated assignments', op, async t => {
    await op.locator('#jg-mode-manual').check();
    const l = await clickLive(op, op.locator('#jg-settings-save'), /Saved|error|wrong/i);
    if (!/Saved/.test(l.text)) t.fail('save said: ' + l.text);
    await op.locator('#jg-assign-editor').waitFor({ state: 'visible', timeout: 5000 });
    const checked = await op.locator('#jg-assign-grid input[type=checkbox]:checked').count();
    const total = await op.locator('#jg-assign-grid input[type=checkbox]').count();
    t.note(`grid: ${checked} of ${total} boxes ticked right after switching`);
    if (checked !== generated) t.fail(`after Generate then Manual, the grid shows ${checked} ticked, but ${generated} assignments exist (it shows the list from page load)`);
    await manage(op, 'judging'); await openSetup(op);
    const checked2 = await op.locator('#jg-assign-grid input[type=checkbox]:checked').count();
    t.note(`after reload: ${checked2} ticked`);
    if (checked2 !== generated) t.fail(`after reload the grid shows ${checked2} ticked, expected ${generated}`);
  });

  const setGrid = async (page, plan, keyOf) => {
    const boxes = page.locator('#jg-assign-grid input[type=checkbox]');
    const n = await boxes.count();
    for (let i = 0; i < n; i++) {
      const b = boxes.nth(i);
      const subj = await b.getAttribute('data-subject'), judge = await b.getAttribute('data-judge');
      const want = keyOf(subj, judge, plan);
      if ((await b.isChecked()) !== want) await b.setChecked(want);
    }
  };
  const manualWant = (subj, judge, plan) => Object.entries(plan).some(([jid, teams]) => state.ids[jid] === judge && teams.some(ti => state.teams[ti].id === subj));

  await run.step(S242, 'Assignment: edit by hand to 5 entries per judge (2 for the organiser), save', op, async t => {
    await openSetup(op);
    await setGrid(op, PLAN, manualWant);
    const l = await clickLive(op, op.locator('#jg-assign-save'), /saved|error|cannot|not/i);
    if (!/Judging access saved/.test(l.text)) t.fail('save said: ' + l.text);
    await op.waitForTimeout(1200);
    const d = await dashText(op);
    t.note('judges: ' + d.judges.join(' | '));
    for (const [jid, teams] of Object.entries(PLAN)) {
      const line = d.judges.find(x => x.startsWith(JUDGE_NAMES[jid] + ':'));
      if (!line || !line.includes(`0 of ${teams.length} done`)) t.fail(`${JUDGE_NAMES[jid]} shows "${line}", expected 0 of ${teams.length}`);
    }
    await manage(op, 'judging'); await openSetup(op);
    const checked = await op.locator('#jg-assign-grid input[type=checkbox]:checked').count();
    const want = Object.values(PLAN).reduce((s, x) => s + x.length, 0);
    if (checked !== want) t.fail(`after reload ${checked} ticked, expected ${want}`);
  });

  await run.step(S242, 'Assignment: Panels per track (AI: J1-J3, Climate: J3-J5), dashboard counts', op, async t => {
    await openSetup(op);
    await op.locator('#jg-mode-panel').check();
    let l = await clickLive(op, op.locator('#jg-settings-save'), /Saved|error|wrong/i);
    if (!/Saved/.test(l.text)) t.fail('save said: ' + l.text);
    const title = (await op.locator('#jg-assign-title').innerText().catch(() => '')).trim();
    if (title !== 'Track panels') t.fail('editor title: ' + title);
    const trackId = slug => (state.tracks.find(x => x.slug === slug) || {}).id;
    const panel = { ai: ['J1', 'J2', 'J3'], climate: ['J3', 'J4', 'J5'] };
    await setGrid(op, panel, (subj, judge) => Object.entries(panel).some(([tr, js]) => trackId(tr) === subj && js.some(j => state.ids[j] === judge)));
    l = await clickLive(op, op.locator('#jg-assign-save'), /saved|error|cannot|not/i);
    if (!/Judging access saved/.test(l.text)) t.fail('save said: ' + l.text);
    await op.waitForTimeout(1200);
    const d = await dashText(op);
    t.note('judges: ' + d.judges.join(' | '));
    const want = { J1: 4, J2: 4, J3: 8, J4: 4, J5: 4, O: 0 };
    for (const [jid, n] of Object.entries(want)) {
      const line = d.judges.find(x => x.startsWith(JUDGE_NAMES[jid] + ':'));
      if (!line || !line.includes(`of ${n} done`)) t.fail(`${JUDGE_NAMES[jid]} shows "${line}", expected of ${n}`);
    }
  });

  await run.step(S242, 'Assignment: back to Manual keeps the hand-edited plan', op, async t => {
    await op.locator('#jg-mode-manual').check();
    const l = await clickLive(op, op.locator('#jg-settings-save'), /Saved|error|wrong/i);
    if (!/Saved/.test(l.text)) t.fail('save said: ' + l.text);
    await manage(op, 'judging'); await openSetup(op);
    const checked = await op.locator('#jg-assign-grid input[type=checkbox]:checked').count();
    if (checked !== 27) t.fail(`manual grid has ${checked} ticked, expected 27`);
    const d = await dashText(op);
    const line = d.judges.find(x => x.startsWith('Judge One:'));
    if (!line || !/0 of 5 done/.test(line)) t.fail('Judge One: ' + line);
  });

  await run.step(S242, 'Conflict of interest recorded by the organiser (Judge Five <-> Hotel Hawks)', op, async t => {
    await openSetup(op);
    await op.locator('#jg-conflict-judge').selectOption({ label: JUDGE_NAMES[ORG_CONFLICT.judge] });
    await op.locator('#jg-conflict-team').selectOption({ label: TEAMS[ORG_CONFLICT.team].name });
    const l = await clickLive(op, op.locator('#jg-conflict-add'), /added|error|not|cannot/i);
    if (!/Conflict added/.test(l.text)) t.fail('add said: ' + l.text);
    const rows = await op.locator('#jg-conflicts .member').allInnerTexts();
    t.note('conflicts: ' + rows.join(' | '));
    if (!rows.some(r => r.includes('Judge Five') && r.includes('Hotel Hawks'))) t.fail('conflict not listed');
    await op.locator('#jg-refresh').click(); await op.waitForTimeout(1200);
    const d = await dashText(op);
    const line = d.judges.find(x => x.startsWith('Judge Five:'));
    t.note('Judge Five: ' + line);
    if (!line || !/0 of 4 done/.test(line)) t.fail('Judge Five should drop to 4 assigned: ' + line);
  });

  await run.step(S242, 'Manual grid: "Save judging access" again after the conflict (grid still ticks the conflicted pair)', op, async t => {
    await openSetup(op);
    const pairTicked = await op.locator(`#jg-assign-grid input[data-subject="${state.teams[ORG_CONFLICT.team].id}"][data-judge="${state.ids[ORG_CONFLICT.judge]}"]`).isChecked();
    t.note('conflicted pair still ticked in the grid: ' + pairTicked);
    const l = await clickLive(op, op.locator('#jg-assign-save'), /saved|conflict|error|cannot/i);
    t.note('save said: ' + l.text);
    if (!/Judging access saved/.test(l.text)) t.fail(`saving the unchanged grid fails: "${l.text}" (hint above the grid says "Conflicted pairs stay excluded")`);
  });

  await run.step(S243, 'Results rules: ranking score, first tie criterion, public ranks and scores save as you tick', op, async t => {
    await openSetup(op);
    await op.locator('#jg-score-mode').selectOption('normalised');
    let ok = await op.locator('#jg-rules-saved').waitFor({ state: 'visible', timeout: 6000 }).then(() => true).catch(() => false);
    if (!ok) t.fail('no "Saved" after choosing Normalised mean: ' + (await live(op)).text);
    await op.locator('#jg-score-mode').selectOption('raw');
    await op.waitForTimeout(1500);
    await op.locator('#jg-tie-criterion').selectOption({ label: 'Idea' });
    ok = await op.locator('#jg-rules-saved').waitFor({ state: 'visible', timeout: 6000 }).then(() => true).catch(() => false);
    if (!ok) t.fail('no "Saved" after choosing tie criterion: ' + (await live(op)).text);
    await op.locator('#jg-public-scores').uncheck(); await op.waitForTimeout(1500);
    await op.locator('#jg-public-scores').check(); await op.waitForTimeout(1500);
    await manage(op, 'judging'); await openSetup(op);
    const mode = await op.locator('#jg-score-mode').inputValue();
    const tie = await op.locator('#jg-tie-criterion option:checked').innerText();
    const ranks = await op.locator('#jg-public-ranks').isChecked(), scores = await op.locator('#jg-public-scores').isChecked();
    t.note(`after reload: mode=${mode} tie=${tie} ranks=${ranks} scores=${scores}`);
    if (mode !== 'raw' || tie.trim() !== 'Idea' || !ranks || !scores) t.fail('rules did not persist');
  });

  // ===== SIM-241: judging =====
  await run.step(S241, 'Organiser moves the event to Judging (Overview stage)', op, async t => {
    await manage(op, 'overview');
    await op.locator('#ov-stage').selectOption('judging');
    await confirmDialog(op, 2000);
    await op.waitForTimeout(1500);
    const r = await api(ORG.api_key, 'GET', `/v1/hack/events/${SLUG}`);
    if (r.data.event.stage !== 'judging') t.fail('stage is ' + r.data.event.stage + ' ' + (await live(op)).text);
    await manage(op, 'judging');
    const tieDisabled = await op.locator('#jg-tie-criterion').isDisabled();
    if (!tieDisabled) t.note('tie criterion still editable in judging stage');
  });

  // Score the entry on screen with the team's vector. Returns team index.
  async function scoreCurrent(page, opts = {}) {
    await page.locator('#judge-score-view').waitFor({ state: 'visible', timeout: 15000 });
    await waitFn(page, () => document.querySelectorAll('#jsc-criteria .member').length > 0, null, { timeout: 10000 });
    const title = (await page.locator('#jsc-title').innerText()).trim();
    const ti = titleToIdx(title);
    if (ti < 0) throw new Error('unknown entry on screen: ' + title);
    const v = opts.vector || TEAMS[ti].v;
    const crit = page.locator('#jsc-criteria .member');
    const n = await crit.count();
    if (n !== 4) throw new Error(`${n} criteria on the scoring screen, expected 4`);
    for (let k = 0; k < n; k++) {
      const chip = crit.nth(k).locator('button.chip', { hasText: new RegExp(`^${v[k]}$`) });
      if ((await chip.getAttribute('aria-pressed')) === 'true' && !opts.force) continue;
      const [resp] = await Promise.all([
        page.waitForResponse(r => r.url().includes('/judge/scores/') && r.request().method() === 'PUT', { timeout: 15000 }),
        chip.click(),
      ]);
      if (resp.status() !== 200) throw new Error(`score save ${resp.status()}: ${(await live(page)).text}`);
    }
    return ti;
  }
  async function nextEntry(page) {
    const before = (await page.locator('#jsc-title').innerText()).trim();
    await page.locator('#jsc-next').click();
    await waitFn(page, b => document.querySelector('#judge-score-view').hidden || document.querySelector('#jsc-title').textContent.trim() !== b, before, { timeout: 15000 });
    await page.waitForTimeout(400);
  }
  const scoredBy = {}; // jid -> [team idx]

  async function judgeFlow(t, jid, page) {
    scoredBy[jid] = [];
    await goto(page, `/e/${SLUG}/judge`);
    await waitFn(page, () => document.querySelectorAll('#judge-projects .card').length > 0, null, { timeout: 20000 });
    const cards = await page.locator('#judge-projects > .card').count();
    const scoreBtns = page.locator('#judge-projects button', { hasText: /Score this team|Edit your score/ });
    const nq = await scoreBtns.count();
    t.note(`${cards} entries listed, ${nq} to score`);
    const planned = PLAN[jid].filter(ti => !(jid === ORG_CONFLICT.judge && ti === ORG_CONFLICT.team));
    if (nq !== planned.length) t.fail(`${nq} entries to score, expected ${planned.length}`);
    const firstCard = (await page.locator('#judge-projects > .card').filter({ has: page.locator('button', { hasText: /Score this team|Edit your score/ }) }).first().locator('h2').first().innerText()).trim();
    await scoreBtns.first().click();
    const view = page.locator('#judge-score-view');
    await view.waitFor({ state: 'visible', timeout: 15000 });
    await waitFn(page, () => document.querySelectorAll('#jsc-criteria .member').length > 0, null, { timeout: 10000 });
    const firstProg = (await page.locator('#jsc-progress').innerText()).trim();
    t.note(`clicked the first card (${firstCard}); scoring screen says "${firstProg}"`);
    if (!/^1 of /.test(firstProg)) state.orderNotes = (state.orderNotes || []).concat(`${JUDGE_NAMES[jid]}: first card ${firstCard} opened as "${firstProg}"`);
    let first = true, needFix = null, fixDone = false, reopened = 0, guard = 0, conflictDone = false;
    while (guard++ < 20) {
      if (await view.isHidden()) {
        await page.waitForTimeout(1200);
        const todo = page.locator('#judge-projects button', { hasText: 'Score this team' });
        if (!(await todo.count())) break;
        reopened++;
        await todo.first().click();
        await view.waitFor({ state: 'visible', timeout: 15000 });
      }
      await waitFn(page, () => document.querySelectorAll('#jsc-criteria .member').length > 0, null, { timeout: 10000 });
      const title = (await page.locator('#jsc-title').innerText()).trim();
      const ti = titleToIdx(title);
      if (jid === JUDGE_CONFLICT.judge && ti === JUDGE_CONFLICT.team) {
        await page.locator('#jsc-conflict').click();
        const dlg = await confirmDialog(page);
        t.note(`conflict on ${title}: dialog "${dlg && dlg.title}": ${dlg && dlg.body}`);
        if (!dlg) t.fail('no confirm dialog for the conflict button');
        conflictDone = true;
        await waitFn(page, b => document.querySelector('#judge-score-view').hidden || document.querySelector('#jsc-title').textContent.trim() !== b, title, { timeout: 15000 }).catch(() => t.fail('screen did not move on after the conflict'));
        await page.waitForTimeout(500);
        continue;
      }
      if (first) {
        first = false;
        const open = page.locator('#jsc-links a', { hasText: /Open website/ });
        if (!(await open.count())) t.fail(`no "Open website" on ${title}`);
        else {
          const [site] = await Promise.all([page.context().waitForEvent('page', { timeout: 15000 }), open.first().click()]);
          await site.waitForLoadState('domcontentloaded', { timeout: 20000 }).catch(() => {});
          const h = await site.locator('h1').innerText({ timeout: 10000 }).catch(e => 'ERR ' + e.message.split('\n')[0]);
          if (!h.includes(TEAMS[ti].name)) t.fail(`team site for ${title} showed "${h}" (${site.url()})`);
          else t.note(`opened ${site.url()}`);
          await site.close();
          const label = (await open.first().innerText()).trim();
          if (!/Viewed/.test(label)) t.note('Open website label did not change to Viewed: ' + label);
        }
        const wrong = TEAMS[ti].v.slice(); wrong[0] = wrong[0] === 0 ? 1 : wrong[0] - 1;
        await scoreCurrent(page, { vector: wrong });
        needFix = { ti, title, wrong: wrong[0] };
      } else {
        await scoreCurrent(page);
      }
      await page.locator('#jsc-comment').fill(`Nice work, ${TEAMS[ti].name}. (${JUDGE_NAMES[jid]})`);
      if (!scoredBy[jid].includes(ti)) scoredBy[jid].push(ti);
      // Edit a score with Previous once there is an entry before this one.
      if (needFix && !fixDone && needFix.ti !== ti && !(await page.locator('#jsc-prev').isDisabled())) {
        await page.locator('#jsc-prev').click();
        await waitFn(page, b => document.querySelector('#jsc-title').textContent.trim() !== b, title, { timeout: 10000 }).catch(() => {});
        await page.waitForTimeout(400);
        const ptitle = (await page.locator('#jsc-title').innerText()).trim();
        const pti = titleToIdx(ptitle);
        if (pti === needFix.ti) {
          const pressed = await page.locator('#jsc-criteria .member').nth(0).locator('button.chip[aria-pressed=true]').innerText().catch(() => '');
          if (pressed.trim() !== String(needFix.wrong)) t.fail(`Previous shows ${pressed || 'nothing'} pressed for ${ptitle}, saved ${needFix.wrong}`);
          const comment = await page.locator('#jsc-comment').inputValue();
          if (!comment.includes(TEAMS[pti].name)) t.fail('comment not kept after Previous: ' + JSON.stringify(comment));
          await scoreCurrent(page);
          fixDone = true;
          t.note(`edited ${ptitle} with Previous (${needFix.wrong} -> ${TEAMS[pti].v[0]})`);
        } else if (!(jid === JUDGE_CONFLICT.judge && pti === JUDGE_CONFLICT.team)) {
          await scoreCurrent(page);
          if (!scoredBy[jid].includes(pti)) scoredBy[jid].push(pti);
        }
        await nextEntry(page); // back to the entry we came from (already scored)
      }
      await nextEntry(page);
    }
    if (needFix && !fixDone) {
      // Previous never reached it: fix it from the list instead.
      t.note(`Previous never led back to ${needFix.title}; fixed it from the list`);
      await page.locator('#judge-projects > .card', { hasText: needFix.title }).locator('button').last().click();
      await view.waitFor({ state: 'visible' });
      await scoreCurrent(page);
      await page.locator('#jsc-back').click();
    }
    if (reopened) t.note(`after "Save & finish" the list still had unscored entries; reopened ${reopened} time(s) from the list`);
    if (!(await page.locator('#judge-list-view').isVisible())) t.fail('did not return to the entry list after the last entry');
    await page.waitForTimeout(1200);
    const edits = await page.locator('#judge-projects button', { hasText: 'Edit your score' }).count();
    const todo = await page.locator('#judge-projects button', { hasText: 'Score this team' }).count();
    t.note(`after finishing: ${edits} "Edit your score", ${todo} "Score this team", scored ${scoredBy[jid].map(x => TEAMS[x].name).join(', ')}`);
    if (todo) t.fail(`${todo} entries still say "Score this team" after finishing`);
    if (jid === JUDGE_CONFLICT.judge && !conflictDone) t.fail('never reached the conflicted entry');
  }

  // Phone first: Judge Three on Chromium 390 and Judge Four on WebKit 390 score their first entry.
  const webkit = await run.browser('webkit');
  for (const [jid, br, name] of [['J3', chromium, 'Chromium'], ['J4', webkit, 'WebKit']]) {
    const ph = await run.person(br, J[Number(jid[1]) - 1], { width: 390, label: `judge-${jid[1]}-phone-${name}` });
    await run.step(S241, `Phone 390px ${name}: ${JUDGE_NAMES[jid]} scores one entry`, ph.page, async t => {
      const page = ph.page;
      await goto(page, `/e/${SLUG}/judge`);
      await waitFn(page, () => document.querySelectorAll('#judge-projects .card').length > 0, null, { timeout: 20000 });
      let hs = await noHScroll(page);
      if (hs.sw > hs.cw + 1) t.fail(`entry list scrolls sideways: ${hs.sw}px wide in ${hs.cw}px`);
      await page.locator('#judge-projects button', { hasText: /Score this team/ }).first().click();
      const ti = await scoreCurrent(page);
      hs = await noHScroll(page);
      if (hs.sw > hs.cw + 1) t.fail(`scoring screen scrolls sideways: ${hs.sw}px wide in ${hs.cw}px`);
      // Chips must fit and not overlap.
      const bad = await page.$$eval('#jsc-criteria .chip', bs => {
        const rs = bs.map(b => b.getBoundingClientRect());
        const out = [];
        rs.forEach((r, i) => { if (r.right > innerWidth + 1) out.push('chip off screen'); if (r.width < 32 || r.height < 32) out.push(`chip ${Math.round(r.width)}x${Math.round(r.height)}`); });
        for (let i = 0; i < rs.length; i++) for (let j = i + 1; j < rs.length; j++) { const a = rs[i], b = rs[j]; if (a.left < b.right - 1 && b.left < a.right - 1 && a.top < b.bottom - 1 && b.top < a.bottom - 1) out.push('chips overlap'); }
        return [...new Set(out)];
      });
      if (bad.length) t.fail('chips: ' + bad.join(', '));
      for (const id of ['#jsc-next', '#jsc-prev', '#jsc-conflict', '#jsc-back']) {
        const b = await page.locator(id).boundingBox();
        if (b && b.x + b.width > 391) t.fail(`${id} runs off the screen`);
      }
      await page.screenshot({ path: `${OUT}/${AREA}-phone-${name}.png`, fullPage: true });
      t.note(`scored ${TEAMS[ti].name}; screenshot ${OUT}/${AREA}-phone-${name}.png`);
      await page.locator('#jsc-back').click();
      await page.waitForTimeout(800);
    });
  }

  for (const jid of ['J1', 'J2', 'J3', 'J4', 'J5']) {
    await run.step(S241, `${JUDGE_NAMES[jid]}: queue, open entry and website, score every criterion, comment, edit with Previous${jid === JUDGE_CONFLICT.judge ? ', skip Delta Ducks with "I have a conflict"' : ''}, finish`, judges[jid].page, async t => {
      await judgeFlow(t, jid, judges[jid].page);
    });
    if (jid === 'J2') {
      await run.step(S241, 'After Judge Two\'s conflict: Delta Ducks shows as conflicted for Judge Two and is still judged by others', judges.J2.page, async t => {
        const card = judges.J2.page.locator('#judge-projects > .card', { hasText: 'Project Delta' });
        const txt = await card.innerText().catch(() => '');
        if (!/conflict with this team/.test(txt)) t.fail('Judge Two\'s card for Delta Ducks: ' + txt.slice(0, 200));
        await manage(op, 'judging');
        const d = await dashText(op);
        t.note('teams: ' + d.teams.join(' | '));
        t.note('judges: ' + d.judges.join(' | '));
        const c = (await op.locator('#jg-conflicts .member').allTextContents()).join(' | ');
        if (!/Judge Two.*Delta Ducks/.test(c)) t.fail('organiser conflict list lacks Judge Two <-> Delta Ducks: ' + c);
        const j2 = d.judges.find(x => x.startsWith('Judge Two:'));
        if (!/4 of 4 done/.test(j2 || '')) t.fail('Judge Two progress: ' + j2);
        // Nobody new is given the entry; it keeps J1 and J4 from the plan.
        const people = await api(ORG.api_key, 'GET', `/v1/hack/events/${SLUG}/assignments`);
        const holders = (people.data.assignments || []).filter(a => a.team_id === state.teams[3].id).map(a => a.judge_name || a.judge_id);
        t.note('Delta Ducks is assigned to: ' + holders.join(', ') + ' (no replacement judge is added for Judge Two)');
      });
    }
  }

  await run.step(S241, 'Organiser judges under their own account via "Judge entries"', op, async t => {
    await manage(op, 'judging');
    await op.locator('#jg-judge-href').click();
    await op.waitForURL(u => u.pathname.endsWith(`/e/${SLUG}/judge`), { timeout: 15000 });
    await dismissTours(op);
    const back = await op.locator('#ev-judge-manage').isVisible();
    if (!back) t.fail('no "Back to organiser dashboard" link');
    await judgeFlow(t, 'O', op);
  });

  // ===== SIM-242: dashboard after judging =====
  await run.step(S242, 'Judging dashboard progress matches what the judges did', op, async t => {
    await manage(op, 'judging');
    const d = await dashText(op);
    t.note('teams: ' + d.teams.join(' | '));
    t.note('judges: ' + d.judges.join(' | '));
    // Reality from what each judge scored.
    const perTeam = TEAMS.map(() => 0);
    Object.values(scoredBy).forEach(ts => ts.forEach(ti => perTeam[ti]++));
    TEAMS.forEach((tm, ti) => {
      const line = d.teams.find(x => x.startsWith(tm.name + ':'));
      const n = Number(((line || '').match(/: (\d+) judge/) || [])[1]);
      if (n !== perTeam[ti]) t.fail(`${tm.name}: dashboard "${line}", judges actually scored ${perTeam[ti]}`);
    });
    for (const [jid, ts] of Object.entries(scoredBy)) {
      const line = d.judges.find(x => x.startsWith(JUDGE_NAMES[jid] + ':'));
      const m = (line || '').match(/(\d+) of (\d+) done/);
      if (!m || Number(m[1]) !== ts.length || Number(m[2]) !== ts.length) t.fail(`${JUDGE_NAMES[jid]}: "${line}", actually scored ${ts.length} of ${ts.length}`);
    }
    const flagged = await op.locator('#jg-dash-teams .jg-flag').allInnerTexts();
    if (flagged.length) t.note('flagged: ' + flagged.join(' | '));
  });

  // ===== SIM-243: lock, tie, publish, results views =====
  await run.step(S243, 'Preview current standings before publishing (tie between Bravo Bees and Charlie Crows)', op, async t => {
    await manage(op, 'judging');
    await op.locator('#jg-preview').click();
    await op.waitForTimeout(1500);
    const lines = await op.locator('#jg-results .member').allInnerTexts();
    t.note('preview: ' + lines.join(' | '));
    if (lines.length !== 8) t.fail('preview lists ' + lines.length + ' teams');
    const b = lines.find(x => x.includes('Bravo Bees')), c = lines.find(x => x.includes('Charlie Crows'));
    const rb = (b || '').match(/^#(\d+)/), rc = (c || '').match(/^#(\d+)/);
    if (!rb || !rc) t.fail('Bravo/Charlie missing in preview');
    else if (rb[1] !== rc[1]) t.note(`preview ranks the tied pair differently: Bravo #${rb[1]}, Charlie #${rc[1]}`);
    if (!lines.some(x => /tied/i.test(x))) t.note('the preview shows the tie only as two equal ranks; it does not say "tied" or offer a way to break it');
    const tg = await api(ORG.api_key, 'GET', `/v1/hack/events/${SLUG}/judging/dashboard`);
    t.note('dashboard tied_groups: ' + JSON.stringify(tg.data.tied_groups));
  });

  await run.step(S243, 'Lock judging (confirm), then a judge cannot change a score', op, async t => {
    await clearLive(op);
    await op.locator('#jg-lock button', { hasText: 'Lock judging' }).click();
    const dlg = await confirmDialog(op);
    if (!dlg) t.fail('no confirm dialog');
    const l = await waitLive(op, /locked|error/i);
    if (!/Judging locked/.test(l.text)) t.fail('lock said: ' + l.text);
    const jp = judges.J1.page;
    await goto(jp, `/e/${SLUG}/judge`);
    await waitFn(jp, () => document.querySelectorAll('#judge-projects .card').length > 0, null, { timeout: 20000 });
    await jp.locator('#judge-projects button', { hasText: /Edit your score|Score this team/ }).first().click();
    await jp.locator('#judge-score-view').waitFor({ state: 'visible' });
    await waitFn(jp, () => document.querySelectorAll('#jsc-criteria .member').length > 0);
    const locked = await jp.locator('#jsc-criteria .chip').first().isDisabled();
    const note = (await jp.locator('#judge-score-view').innerText()).match(/lock[^\n]*/i);
    t.note('judge screen while locked: chips disabled=' + locked + ', lock text=' + (note ? note[0] : 'none'));
    const chip = jp.locator('#jsc-criteria .member').nth(1).locator('button.chip', { hasText: /^0$/ });
    const [resp] = await Promise.all([jp.waitForResponse(r => r.url().includes('/judge/scores/') && r.request().method() === 'PUT', { timeout: 8000 }).catch(() => null), chip.click().catch(() => {})]);
    await jp.waitForTimeout(600);
    const jl = await live(jp);
    t.note(`judge tap while locked -> ${resp ? resp.status() : 'no request'}; message "${jl.text}"`);
    if (resp && resp.status() === 200) t.fail('a score saved while judging is locked');
    if (resp && !/locked/i.test(jl.text)) t.fail('judge sees no "locked" message: ' + jl.text);
    if (!note && !locked) t.note('the judge is not told judging is locked until they tap');
    await jp.locator('#jsc-back').click().catch(() => {});
  });

  let published = [];
  await run.step(S243, 'Publish results: what the organiser sees about the tie', op, async t => {
    await manage(op, 'judging');
    await clearLive(op);
    await op.locator('#jg-publish').click();
    const pd = await confirmDialog(op);
    t.note('publish dialog: ' + (pd ? `"${pd.title}" ${pd.body}` : 'none'));
    const l = await waitLive(op, /published|error|wrong/i, 15000);
    if (!/Results published/.test(l.text)) t.fail('publish said: ' + l.text);
    published = await op.locator('#jg-results .member').allInnerTexts();
    t.note('results: ' + published.join(' | '));
    const form = op.locator('#jg-results form');
    if (await form.count()) {
      t.note('tie form: ' + (await form.innerText()).replace(/\s+/g, ' ').slice(0, 300));
    } else t.fail('no way to resolve the tie is offered (no "Some teams tie" form)');
    const b = published.find(x => x.includes('Bravo Bees')) || '', c = published.find(x => x.includes('Charlie Crows')) || '';
    if (!/tied/.test(b) || !/tied/.test(c)) t.fail(`tied pair not marked: "${b}" / "${c}"`);
    const a = published.find(x => x.includes('Alpha Ants')) || '';
    if (!/^#1 /.test(a)) t.fail('Alpha Ants is not #1: ' + a);
    if (!/AI for good winner/.test(a)) t.fail('Alpha Ants not marked AI track winner: ' + a);
    const e = published.find(x => x.includes('Echo Eels')) || '';
    if (!/Climate winner/.test(e)) t.fail('Echo Eels not marked Climate winner: ' + e);
  });

  await run.step(S243, 'Resolve the tie: give Bravo Bees #2 and Charlie Crows #3, Save order', op, async t => {
    const form = op.locator('#jg-results form');
    if (!(await form.count())) { t.fail('no tie form'); return; }
    const labels = form.locator('label');
    const n = await labels.count();
    for (let i = 0; i < n; i++) {
      const txt = await labels.nth(i).innerText();
      if (txt.includes('Bravo Bees')) await labels.nth(i).locator('input').fill('2');
      if (txt.includes('Charlie Crows')) await labels.nth(i).locator('input').fill('3');
    }
    await clearLive(op);
    await form.locator('button', { hasText: 'Save order' }).click();
    const l = await waitLive(op, /published|error|invalid|rank/i, 15000);
    t.note('message: ' + l.text);
    const rows = await op.locator('#jg-results .member').allInnerTexts();
    t.note('results now: ' + rows.join(' | '));
    const b = rows.find(x => x.includes('Bravo Bees')) || '', c = rows.find(x => x.includes('Charlie Crows')) || '';
    if (!/^#2 /.test(b) || !/^#3 /.test(c)) t.fail(`order not applied: "${b}" / "${c}"`);
    if (/tied/.test(b + c)) t.note('still marked tied after the organiser chose the order');
    await manage(op, 'judging');
    const again = await op.locator('#jg-results .member').allInnerTexts();
    if (again.join() !== rows.join()) t.fail('after reload results differ: ' + again.join(' | '));
  });

  const visitor = await run.person(chromium, null, { label: 'visitor' });
  await run.step(S243, 'Public event page shows the winners with links to their sites', visitor.page, async t => {
    const url = await publicPage(visitor.page);
    await visitor.page.waitForTimeout(500);
    const sec = visitor.page.locator('section', { has: visitor.page.locator('h2', { hasText: /^(Winners|Results)$/ }) });
    if (!(await sec.count())) { t.fail('no Winners section on ' + url); return; }
    const txt = (await sec.innerText()).replace(/\s+/g, ' ');
    t.note(`${url}: ${txt.slice(0, 300)}`);
    if (!/Alpha Ants/.test(txt)) t.fail('Alpha Ants not shown as winner');
    if (!/Echo Eels/.test(txt)) t.fail('Climate winner Echo Eels not shown');
    const links = await sec.locator('a').evaluateAll(as => as.map(a => a.href));
    if (!links.some(h => h.includes(state.teams[0].slug))) t.fail(`winners have no link to their sites (links in section: ${JSON.stringify(links)})`);
  });

  await run.step(S243, 'Results view: Full ranking, then Winners only, as the public page shows them', op, async t => {
    await clearLive(op);
    await op.locator('#jg-vis-full').check();
    let l = await waitLive(op, /Showing|error/i);
    if (!/full ranking/.test(l.text)) t.fail('full ranking said: ' + l.text);
    await publicPage(visitor.page);
    let rows = await visitor.page.locator('.results-row').allInnerTexts();
    const h2 = await visitor.page.locator('section h2', { hasText: /^(Winners|Results)$/ }).innerText().catch(() => '');
    t.note(`full: "${h2}" ${rows.length} rows: ${rows.map(r => r.replace(/\s+/g, ' ')).join(' | ')}`);
    if (rows.length !== 8) t.fail('full ranking shows ' + rows.length + ' rows, expected 8');
    await clearLive(op);
    await op.locator('#jg-vis-winners').check();
    l = await waitLive(op, /Showing|error/i);
    if (!/winners only/.test(l.text)) t.fail('winners only said: ' + l.text);
    await publicPage(visitor.page);
    rows = await visitor.page.locator('.results-row').allInnerTexts();
    t.note(`winners only: ${rows.map(r => r.replace(/\s+/g, ' ')).join(' | ')}`);
    if (rows.length !== 2) t.fail('winners only shows ' + rows.length + ' rows, expected 2 (overall #1 and Climate winner)');
    // Hide scores publicly.
    await manage(op, 'judging'); await openSetup(op);
    await op.locator('#jg-public-scores').uncheck();
    await op.locator('#jg-rules-saved').waitFor({ state: 'visible', timeout: 6000 }).catch(() => t.fail('no Saved for public scores'));
    await publicPage(visitor.page);
    const withScores = (await visitor.page.locator('.results-list').innerText().catch(() => '')).match(/\d+\.\d\d \(/);
    if (withScores) t.fail('scores still shown publicly after unticking "Show scores"');
    await op.locator('#jg-public-scores').check(); await op.waitForTimeout(1500);
  });

  await run.step(S243, 'Unlock (with a reason) and relock', op, async t => {
    await manage(op, 'judging');
    const reason = op.locator('#jg-unlock-reason');
    if (!(await reason.count())) { t.fail('no unlock form while locked'); return; }
    await op.locator('#jg-unlock-form button[type=submit]').click();
    await op.waitForTimeout(500);
    const stillLocked = await reason.count();
    const validity = await reason.evaluate(n => n.validationMessage).catch(() => '');
    t.note('Unlock with empty reason: ' + (stillLocked ? `blocked ("${validity}")` : 'went through'));
    await reason.fill('Judge asked to fix a typo');
    let l = await clickLive(op, op.locator('#jg-unlock-form button[type=submit]'), /unlocked|error/i);
    if (!/Judging unlocked/.test(l.text)) t.fail('unlock said: ' + l.text);
    if (await op.locator('#jg-lock-note').isVisible()) t.fail('lock note still visible after unlocking');
    await clearLive(op);
    await op.locator('#jg-lock button', { hasText: 'Lock judging' }).click();
    await confirmDialog(op);
    l = await waitLive(op, /locked|error/i);
    if (!/Judging locked/.test(l.text)) t.fail('relock said: ' + l.text);
    await manage(op, 'judging');
    if (!(await op.locator('#jg-unlock-reason').count())) t.fail('after reload the page does not show judging as locked');
    const pub = await op.locator('#jg-results .member').allInnerTexts();
    if (pub.join() !== (await op.locator('#jg-results .member').allInnerTexts()).join()) t.fail('results changed');
  });

  // ===== SIM-243: exports =====
  const exportsDone = {};
  const csvCheck = (t, file, wantCols, minRows) => {
    const text = fs.readFileSync(file, 'utf8');
    if (!text.trim()) { t.fail(`${file}: empty`); return null; }
    const rows = parseCSV(text);
    const head = rows[0] || [];
    t.note(`${file.split('/').pop()}: ${fs.statSync(file).size} bytes, ${rows.length - 1} rows, columns: ${head.join(', ')}`);
    for (const c of wantCols) if (!head.some(h => h.toLowerCase().includes(c))) t.fail(`${file.split('/').pop()}: no "${c}" column`);
    if (rows.length - 1 < minRows) t.fail(`${file.split('/').pop()}: ${rows.length - 1} rows, expected at least ${minRows}`);
    const ragged = rows.filter(r => r.length !== head.length).length;
    if (ragged) t.fail(`${ragged} rows have a different number of columns from the header`);
    return rows;
  };
  await run.step(S243, 'Export: participants CSV (People tab)', op, async t => {
    await manage(op, 'people');
    const f = await download(op, op.locator('#p-people [data-read-download="participants.csv"]'), 'people');
    const rows = csvCheck(t, f, ['name', 'team'], 32);
    if (rows) { exportsDone.participants = rows.length - 1; }
  });
  await run.step(S243, 'Export: teams CSV (Teams tab)', op, async t => {
    await manage(op, 'teams');
    const f = await download(op, op.locator('#p-teams [data-read-download="teams.csv"]'), 'teams');
    const rows = csvCheck(t, f, ['name', 'slug', 'members', 'site_url'], 8);
    if (rows && rows.length - 1 !== 8) t.fail('teams.csv has ' + (rows.length - 1) + ' teams');
  });
  await run.step(S243, 'Export: entries CSV (Entries tab)', op, async t => {
    await manage(op, 'projects');
    const f = await download(op, op.locator('#p-projects [data-read-download="entries.csv"]'), 'entries');
    const rows = csvCheck(t, f, ['title', 'team'], 8);
    if (rows && !rows.some(r => r.includes('Project Alpha'))) t.fail('entries.csv lacks Project Alpha');
  });
  await run.step(S243, 'Export: all team sites archive (Entries tab)', op, async t => {
    await manage(op, 'projects');
    const f = await download(op, op.locator('#p-projects [data-read-download="projects.tar.gz"]'), 'sites');
    const size = fs.statSync(f).size;
    let list = '';
    try { list = execSync(`tar -tzf '${f}'`, { encoding: 'utf8' }); } catch (e) { t.fail('not a readable tar.gz: ' + e.message.split('\n')[0]); }
    const files = list.split('\n').filter(Boolean);
    t.note(`${f.split('/').pop()}: ${size} bytes, ${files.length} entries: ${files.slice(0, 12).join(', ')}`);
    const idx = files.filter(x => x.endsWith('index.html')).length;
    if (idx < 8) t.fail(`archive has ${idx} index.html files, expected 8 team sites`);
  });
  await run.step(S243, 'Export: scores CSV (Judging tab)', op, async t => {
    await manage(op, 'judging');
    const f = await download(op, op.locator('#jg-csv-scores'), 'scores');
    const rows = csvCheck(t, f, ['judge', 'team', 'criterion', 'points'], 1);
    if (rows) {
      const head = rows[0].map(h => h.toLowerCase());
      const scoreRows = rows.length - 1;
      const expected = Object.values(scoredBy).reduce((s, x) => s + x.length, 0) * 4;
      t.note(`score rows ${scoreRows}, judge x team x criterion expected ${expected}`);
      if (scoreRows !== expected && scoreRows !== expected / 4) t.fail(`scores.csv has ${scoreRows} rows; judges scored ${expected / 4} entries x 4 criteria`);
      if (!head.some(h => h.includes('comment'))) t.fail('scores.csv has no comment column (judges left comments)');
      else if (!rows.some(r => r.join(' ').includes('Nice work'))) t.fail('judge comments missing from scores.csv');
    }
  });
  await run.step(S243, 'Export: results CSV (Judging tab)', op, async t => {
    const f = await download(op, op.locator('#jg-csv-results'), 'results');
    const rows = csvCheck(t, f, ['rank', 'team', 'total'], 8);
    if (rows) {
      const head = rows[0].map(h => h.toLowerCase());
      const ri = head.findIndex(h => h.includes('rank')), ti = head.findIndex(h => h.includes('team'));
      const bravo = rows.find(r => r[ti] && r[ti].includes('Bravo')), charlie = rows.find(r => r[ti] && r[ti].includes('Charlie'));
      t.note(`Bravo rank ${bravo && bravo[ri]}, Charlie rank ${charlie && charlie[ri]}`);
      if (bravo && charlie && !(bravo[ri] === '2' && charlie[ri] === '3')) t.fail(`results.csv does not carry the organiser's tie order (Bravo ${bravo[ri]}, Charlie ${charlie[ri]})`);
    }
  });

  // ===== SIM-244: gallery, people's choice, directory =====
  await run.step(S244, 'Open the entry gallery (Settings) and see it on the public page', op, async t => {
    await manage(op, 'settings');
    await op.locator('#set-gallery').check();
    await op.waitForTimeout(1500);
    const l = await live(op);
    t.note('message: ' + l.text);
    await manage(op, 'settings');
    if (!(await op.locator('#set-gallery').isChecked())) t.fail('gallery setting did not stay on after reload');
    await publicPage(visitor.page);
    const cards = visitor.page.locator('.gallery .gcard');
    const n = await cards.count();
    t.note(`public gallery: ${n} cards`);
    if (n !== 8) t.fail(`gallery shows ${n} entries, expected 8`);
    const href = await cards.first().locator('a.gcard-open').getAttribute('href').catch(() => '');
    if (!/simple-hack\.app/.test(href || '')) t.fail('gallery "Open team site" link: ' + href);
  });

  const vs = await run.person(chromium, ORG, { label: 'organiser-voting' });
  let zone = 'UTC';
  async function saveVoting(t, opensIn, closesIn, elig) {
    const page = vs.page;
    await goto(page, `/e/${SLUG}/voting-settings`);
    await page.locator('#settings-panel').waitFor({ state: 'visible', timeout: 15000 });
    zone = ((await page.locator('#zone').innerText()).match(/zone: (.+)$/) || [])[1] || 'UTC';
    await page.locator('#enabled').check();
    await page.locator('#opens').fill(localInZone(new Date(Date.now() + opensIn * 60000), zone));
    await page.locator('#closes').fill(localInZone(new Date(Date.now() + closesIn * 60000), zone));
    await page.locator('#eligibility').selectOption(elig);
    await page.locator('#save-voting').click();
    await waitFn(page, () => document.querySelector('#live').textContent.trim() !== '', null, { timeout: 8000 }).catch(() => {});
    const msg = (await page.locator('#live').innerText()).trim();
    if (!/Voting settings saved/.test(msg)) t.fail(`save voting (${elig}, opens ${opensIn}m, closes ${closesIn}m) said: ${msg}`);
    return msg;
  }
  async function voteAs(person, teamIdx) {
    const page = person.page;
    await page.goto(`${SITE}/e/${SLUG}/vote`, { waitUntil: 'domcontentloaded' });
    await page.locator('#vote-panel').waitFor({ state: 'visible', timeout: 15000 });
    await page.waitForTimeout(600);
    const box = page.locator('#options article', { hasText: TEAMS[teamIdx].title });
    const btn = box.locator('button');
    if (!(await btn.count())) return { msg: 'no vote button', intro: (await page.locator('#intro').innerText()).trim() };
    const label = (await btn.innerText()).trim();
    if (await btn.isDisabled()) return { msg: 'button disabled: ' + label };
    await btn.click();
    await waitFn(page, () => document.querySelector('#live').textContent.trim() !== '', null, { timeout: 8000 }).catch(() => {});
    await page.waitForTimeout(400);
    return { msg: (await page.locator('#live').innerText()).trim(), current: (await page.locator('#vote-current').innerText()).trim() };
  }

  await run.step(S244, 'Settings link opens voting settings; set opening and closing in the future -> "Voting opens soon"', vs.page, async t => {
    await manage(op, 'settings');
    const href = await op.locator('#set-voting-link').getAttribute('href');
    if (href !== `/e/${SLUG}/voting-settings`) t.fail('voting link: ' + href);
    await saveVoting(t, 60, 120, 'all_signed_in');
    await visitor.page.goto(`${SITE}/e/${SLUG}/vote`, { waitUntil: 'domcontentloaded' });
    await visitor.page.locator('#vote-panel').waitFor({ state: 'visible', timeout: 15000 });
    const intro = (await visitor.page.locator('#intro').innerText()).trim();
    const when = (await visitor.page.locator('#vote-when').innerText()).trim();
    t.note(`vote page: "${intro}" ${when}`);
    if (!/opens soon/.test(intro)) t.fail('upcoming voting shows: ' + intro);
  });

  const voters = {
    p1: await run.person(chromium, P[0], { label: 'voter-p1' }),  // Alpha Ants
    p5: await run.person(chromium, P[4], { label: 'voter-p5' }),  // Bravo Bees
    p9: await run.person(chromium, P[8], { label: 'voter-p9' }),  // Charlie Crows
    out: await run.person(chromium, OUTSIDER, { label: 'outsider-coorg2' }),
    j1: judges.J1,
  };
  await run.step(S244, 'Voting open to anyone signed in: participants and an outsider vote; own team refused; change vote', vs.page, async t => {
    await saveVoting(t, -10, 60, 'all_signed_in');
    await visitor.page.goto(`${SITE}/e/${SLUG}/vote`, { waitUntil: 'domcontentloaded' });
    await visitor.page.locator('#vote-panel').waitFor({ state: 'visible' });
    await visitor.page.waitForTimeout(500);
    const signin = await visitor.page.locator('#vote-signin').isVisible();
    const opts = await visitor.page.locator('#options article').count();
    t.note(`signed-out visitor: ${opts} options, "Sign in to vote" shown=${signin}`);
    if (!signin) t.fail('signed-out visitor is not told to sign in');
    if (opts !== 8) t.fail(`${opts} voting options, expected 8`);
    let r = await voteAs(voters.p1, 0); t.note('p1 -> own team Alpha: ' + r.msg);
    if (!/own team/.test(r.msg)) t.fail('voting for own team: ' + r.msg);
    r = await voteAs(voters.p1, 1); t.note(`p1 -> Bravo: ${r.msg} / ${r.current}`);
    if (!/Vote saved/.test(r.msg)) t.fail('p1 vote: ' + r.msg);
    if (r.current && !/Bravo Bees/.test(r.current)) t.fail(`after voting the page says "${r.current}" (team slug, not its name)`);
    r = await voteAs(voters.p1, 1); t.note('p1 -> Bravo again: ' + r.msg);
    if (!/disabled|Your vote/.test(r.msg)) t.fail('second vote for the same team was allowed: ' + r.msg);
    r = await voteAs(voters.p1, 4); t.note('p1 changes to Echo: ' + r.msg);
    if (!/Vote changed/.test(r.msg)) t.fail('changing vote: ' + r.msg);
    r = await voteAs(voters.p5, 0); t.note('p5 -> Alpha: ' + r.msg); if (!/Vote saved/.test(r.msg)) t.fail('p5: ' + r.msg);
    r = await voteAs(voters.out, 0); t.note('outsider -> Alpha: ' + r.msg); if (!/Vote saved/.test(r.msg)) t.fail('outsider (signed in, not in the event): ' + r.msg);
    state.votes.all = { Alpha: 2, Echo: 1 };
  });

  await run.step(S244, 'Voting for event members only: outsider refused, judge allowed', vs.page, async t => {
    await saveVoting(t, -10, 60, 'event_members');
    let r = await voteAs(voters.out, 1); t.note('outsider -> Bravo: ' + r.msg);
    if (!/not eligible/.test(r.msg)) t.fail('outsider under event_members: ' + r.msg);
    r = await voteAs(voters.j1, 0); t.note('judge -> Alpha: ' + r.msg);
    if (!/Vote saved/.test(r.msg)) t.fail('judge under event_members: ' + r.msg);
    r = await voteAs(voters.p9, 0); t.note('p9 -> Alpha: ' + r.msg); if (!/Vote saved/.test(r.msg)) t.fail('p9: ' + r.msg);
    // The outsider's earlier vote: does it still count?
    const vote = await api(ORG.api_key, 'GET', `/v1/hack/events/${SLUG}/vote`);
    t.note('status ' + vote.data.status);
  });

  await run.step(S244, 'Voting for participants only: judge refused, participant allowed', vs.page, async t => {
    await saveVoting(t, -10, 60, 'participants');
    let r = await voteAs(voters.j1, 1); t.note('judge -> Bravo: ' + r.msg);
    if (!/not eligible/.test(r.msg)) t.fail('judge under participants-only: ' + r.msg);
    r = await voteAs(voters.p9, 1); t.note('p9 changes to Bravo: ' + r.msg);
    if (!/Vote changed/.test(r.msg)) t.fail('p9 change: ' + r.msg);
  });

  await run.step(S244, 'Close voting (closing time passed): final votes shown', vs.page, async t => {
    await saveVoting(t, -120, -60, 'participants');
    await visitor.page.goto(`${SITE}/e/${SLUG}/vote`, { waitUntil: 'domcontentloaded' });
    await visitor.page.locator('#vote-panel').waitFor({ state: 'visible' });
    await visitor.page.waitForTimeout(600);
    const intro = (await visitor.page.locator('#intro').innerText()).trim();
    const ranking = await visitor.page.locator('#ranking-list .rank').allInnerTexts();
    t.note(`"${intro}" ranking: ${ranking.map(x => x.replace(/\s+/g, ' ')).join(' | ')}`);
    if (!/closed/.test(intro)) t.fail('closed voting shows: ' + intro);
    if (!ranking.length) t.fail('no final votes listed');
    // Cast: p1 Echo, p5 Alpha, outsider Alpha (all_signed_in), j1 Alpha (members), p9 Bravo.
    t.note('votes cast: Alpha Ants by p5, outsider, judge-1; Echo Eels by p1; Bravo Bees by p9 (after changing from Alpha)');
    let r = await voteAs(voters.p5, 2); t.note('p5 votes after close: ' + (r.msg || r.intro));
    if (/Vote saved|Vote changed/.test(r.msg)) t.fail('a vote was accepted after voting closed');
    const pub = await publicPage(visitor.page);
    const vtxt = (await visitor.page.locator('body').innerText()).match(/[^\n]*(vote|People's choice)[^\n]*/gi) || [];
    t.note('event page mentions: ' + vtxt.slice(0, 4).join(' | '));
  });

  await run.step(S244, 'Directory: list the event, see it on /directory, remove it, gone', vs.page, async t => {
    const page = vs.page;
    await goto(page, `/e/${SLUG}/voting-settings`);
    await page.locator('#settings-panel').waitFor({ state: 'visible' });
    await page.locator('#listed').check();
    await page.locator('#save-directory').click();
    await waitFn(page, () => /Directory/.test(document.querySelector('#live').textContent), null, { timeout: 8000 }).catch(() => {});
    let msg = (await page.locator('#live').innerText()).trim();
    if (!/Directory setting saved/.test(msg)) t.fail('list said: ' + msg);
    await visitor.page.goto(`${SITE}/directory`, { waitUntil: 'domcontentloaded' });
    await waitFn(visitor.page, () => !/Loading/.test(document.querySelector('#status').textContent), null, { timeout: 10000 }).catch(() => {});
    let body = await visitor.page.locator('main').innerText();
    const title = 'E2E Judging ' + stamp;
    const found = body.includes(title);
    const group = await visitor.page.evaluate(t => { for (const id of ['now', 'upcoming', 'past']) if (document.getElementById(id).textContent.includes(t)) return id; return ''; }, title);
    t.note(`listed: found=${found} in "${group}"; status "${(await visitor.page.locator('#status').innerText()).trim()}"`);
    if (!found) t.fail('event not on /directory after listing it');
    const link = await visitor.page.locator('a', { hasText: title }).first().getAttribute('href').catch(() => '');
    t.note('directory link: ' + link);
    const hs = await noHScroll(visitor.page); if (hs.sw > hs.cw + 1) t.fail('directory scrolls sideways');
    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.locator('#settings-panel').waitFor({ state: 'visible' });
    if (!(await page.locator('#listed').isChecked())) t.fail('"List this event" not ticked after reload');
    await page.locator('#listed').uncheck();
    await page.locator('#save-directory').click();
    await page.waitForTimeout(1500);
    msg = (await page.locator('#live').innerText()).trim();
    if (!/Directory setting saved/.test(msg)) t.fail('unlist said: ' + msg);
    await visitor.page.goto(`${SITE}/directory`, { waitUntil: 'domcontentloaded' });
    await visitor.page.waitForTimeout(1500);
    body = await visitor.page.locator('main').innerText();
    if (body.includes(title)) t.fail('event still on /directory after removing it');
  });

  await run.step(S243, 'Move the event to Results stage; public page still shows winners', op, async t => {
    await manage(op, 'overview');
    await op.locator('#ov-stage').selectOption('results');
    await confirmDialog(op, 1500);
    await op.waitForTimeout(1500);
    const r = await api(ORG.api_key, 'GET', `/v1/hack/events/${SLUG}`);
    if (r.data.event.stage !== 'results') t.fail('stage: ' + r.data.event.stage + ' ' + (await live(op)).text);
    await publicPage(visitor.page);
    const rows = await visitor.page.locator('.results-row').count();
    if (!rows) t.fail('no winners on the public page in Results stage');
  });
} catch (e) {
  console.log('ABORT: ' + (e.stack || e));
  run.results.push({ sim: '-', name: 'script aborted', ok: false, notes: [String(e.message || e)] });
} finally {
  for (const slug of created) {
    const st = await deleteEvent(ORG.api_key, slug).catch(e => 'error ' + e.message);
    console.log(`cleanup: DELETE ${slug} -> ${st}`);
    run.results.push({ sim: '-', name: `cleanup delete ${slug}`, ok: st === 200 || st === 204, notes: [String(st)] });
  }
  if (run.console.length) fs.writeFileSync(`${OUT}/${AREA}-console.txt`, [...new Set(run.console)].join('\n'));
  if (run.failed.length) fs.writeFileSync(`${OUT}/${AREA}-failed-requests.txt`, [...new Set(run.failed)].join('\n'));
  await run.done();
}
