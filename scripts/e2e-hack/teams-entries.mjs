#!/usr/bin/env node
// SIM-215 production browser test, area "teams-entries" (account set 4).
//   SIM-239 participant teams, SIM-240 entry + team site + finding it later,
//   SIM-237 organiser Teams tab.
// node scripts/e2e-hack/teams-entries.mjs [outDir]
import { execFileSync } from 'node:child_process';
import { Run, accounts, stamp, SITE, goto, tab, dismissTours, visibleError, createEventViaForm, api, deleteEvent } from './lib.mjs';

const OUT = process.argv[2] || '/tmp/claude-1001/-home-ubuntu-workspace-4/2268cadb-dfb1-4db4-9617-f1516b000a56/scratchpad/e2e-prod';
const SHOT = OUT + '/teams-shot.png';
const run = new Run('teams-entries', OUT);
const acc = accounts(4);
const orgAcc = acc[process.env.ORG_ROLE || 'co-organiser'][Number(process.env.ORG_A || 0)];
const orgAccB = acc[process.env.ORG_ROLE || 'co-organiser'][Number(process.env.ORG_B || 1)];
const P = acc.participant; // 24
const created = [];
const slugA = 'e2e-teams-' + stamp;
const slugB = 'e2e-teams-' + stamp + 'm';
const titleA = 'E2E Teams ' + stamp;
const titleB = 'E2E Teams phone ' + stamp;
const sleep = ms => new Promise(r => setTimeout(r, ms));
const day = n => new Date(Date.now() + n * 864e5);
const local = d => d.toISOString().slice(0, 16); // UTC wall time; event zone is the browser's (UTC headless)

// ---------- helpers ----------
async function live(page) {
  return page.locator('#live').evaluate(n => ({ text: (n.textContent || '').trim(), err: n.classList.contains('err'), ok: n.classList.contains('ok') })).catch(() => ({ text: '', err: false, ok: false }));
}
async function waitLive(page, ms = 8000) {
  const t0 = Date.now();
  while (Date.now() - t0 < ms) { const l = await live(page); if (l.text && l.text !== 'Loading…') return l; await sleep(200); }
  return live(page);
}
async function teamCode(page) { return (await page.locator('#team-code').getAttribute('data-code').catch(() => '')) || ''; }
async function waitTeam(page, ms = 20000) {
  // A manual loop: waitForFunction polling needs eval (blocked by the CSP) and rAF stalls in background pages.
  const t0 = Date.now();
  while (Date.now() - t0 < ms) {
    const ok = await page.evaluate(() => { const t = document.querySelector('#ev-team'); const c = document.querySelector('#team-code'); return !!(t && !t.hidden && c && c.getAttribute('data-code')); }).catch(() => false);
    if (ok) return;
    await sleep(300);
  }
  throw new Error('timed out waiting for the team card');
}
async function onTeamPage(p, slug) { await goto(p.page, `/e/${slug}/team`); await p.page.waitForTimeout(600); }
async function teamName(page) { return (await page.locator('#team-name').innerText().catch(() => '')).trim(); }
async function noTeamShown(page) { return page.locator('#ev-noteam').isVisible().catch(() => false); }
async function confirmDlg(page, ms = 5000) {
  const ok = page.locator('dialog.sh-dlg[open] [data-sh-dlg="ok"]');
  await ok.waitFor({ state: 'visible', timeout: ms });
  const title = (await page.locator('dialog.sh-dlg[open] .sh-dlg-title').innerText().catch(() => '')).trim();
  const t0 = Date.now();
  while (Date.now() - t0 < ms && await ok.isDisabled().catch(() => false)) await sleep(200);
  await ok.click();
  return title;
}
async function overflowCheck(t, page, what) {
  const o = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, w: window.innerWidth }));
  if (o.sw > o.w + 1) t.fail(`${what}: page scrolls sideways at ${o.w}px (content ${o.sw}px wide)`);
  // Any visible button/input wider than the viewport or off its right edge.
  const off = await page.evaluate(() => Array.from(document.querySelectorAll('button, input, select, textarea, a.btn, h1, h2, p')).filter(n => {
    const r = n.getBoundingClientRect(); return r.width > 0 && r.height > 0 && getComputedStyle(n).visibility !== 'hidden' && (r.right > window.innerWidth + 1 || r.left < -1);
  }).slice(0, 5).map(n => (n.id ? '#' + n.id : n.tagName.toLowerCase()) + ' ' + Math.round(n.getBoundingClientRect().left) + '..' + Math.round(n.getBoundingClientRect().right)));
  if (off.length) t.fail(`${what}: elements past the screen edge: ${off.join(', ')}`);
}
async function joinEvent(t, p, joinURL, name) {
  const page = p.page;
  await goto(page, joinURL);
  await page.locator('#join-name').waitFor({ state: 'visible', timeout: 20000 });
  await page.locator('#join-name').fill(name);
  await page.locator('#join-coc').check();
  await page.locator('#join-btn').click();
  await page.waitForURL(u => u.pathname.endsWith('/team'), { timeout: 25000 }).catch(() => {});
  await page.waitForLoadState('networkidle', { timeout: 15000 }).catch(() => {});
  await dismissTours(page);
  if (!page.url().endsWith('/team')) t.fail('after Join still on ' + page.url() + ': ' + (await live(page)).text);
  else if (!(await page.locator('#start-team-form').isVisible().catch(() => false))) t.fail('after Join there is no Start a team form: ' + (await visibleError(page)));
}
async function orgTeams(slug) { return (await api(orgAcc.api_key, 'GET', `/v1/hack/events/${slug}/teams`)).data || {}; }
function teamRow(page, name) {
  return page.locator('#teams-list .team-row').filter({ has: page.locator('.team-row-name', { hasText: new RegExp('^' + name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '$') }) });
}
async function openManage(page, name) {
  const row = teamRow(page, name);
  const btn = row.locator('button', { hasText: 'Manage team' });
  if ((await btn.getAttribute('aria-expanded')) !== 'true') await btn.click();
  return row;
}
function entryCard(page, name) {
  return page.locator('#projects-list > .card').filter({ has: page.locator('h2', { hasText: new RegExp('^' + name + '$') }) });
}
async function fieldVals(page) {
  return page.evaluate(() => Object.fromEntries(['en-title', 'en-tagline', 'en-description', 'en-video', 'en-code'].map(id => [id, (document.getElementById(id) || {}).value])));
}

let cleaning = false;
for (const sig of ['SIGINT', 'SIGTERM']) process.on(sig, async () => {
  if (cleaning) return; cleaning = true;
  for (const s of created) console.log(`cleanup (${sig}): delete ${s} -> ` + await deleteEvent(s === slugB ? orgAccB.api_key : orgAcc.api_key, s).catch(e => e.message));
  process.exit(130);
});
const people = {}; // index (1-based) -> person on event A
const codes = {};  // team number -> code
let joinA = '', joinB = '';
let org, orgPage, chromium, webkit;
try {
  chromium = await run.browser('chromium');
  org = await run.person(chromium, orgAcc, { label: 'organiser' });
  orgPage = org.page;

  // ================= setup =================
  await run.step('SIM-239', 'setup: organiser creates the event with the form', orgPage, async t => {
    await createEventViaForm(orgPage, { slug: slugA, title: titleA });
    created.push(slugA);
  });
  await run.step('SIM-239', 'setup: Teams tab, most people per team = 4, Save', orgPage, async t => {
    await goto(orgPage, `/e/${slugA}/manage`); await tab(orgPage, 'teams');
    await orgPage.locator('#tmax').fill('4');
    await orgPage.locator('#tmax-save').click();
    const l = await waitLive(orgPage);
    if (!l.ok) t.fail('after Save: ' + JSON.stringify(l.text));
    await orgPage.reload(); await tab(orgPage, 'teams');
    const v = await orgPage.locator('#tmax').inputValue();
    if (v !== '4') t.fail('after reload most people per team is ' + v);
  });
  await run.step('SIM-239', 'setup: add two short tracks (AI, Climate) and Save tracks', orgPage, async t => {
    for (const n of ['AI', 'Climate']) { await orgPage.locator('#track-new-name').fill(n); await orgPage.locator('#track-add').click(); }
    await orgPage.locator('#tracks-save').click();
    const l = await waitLive(orgPage);
    if (!l.ok) t.fail('Save tracks: ' + l.text);
    await orgPage.reload(); await tab(orgPage, 'teams');
    const vals = await orgPage.locator('#tracks-editor input').evaluateAll(els => els.map(e => e.value));
    if (!(vals.includes('AI') && vals.includes('Climate'))) t.fail('tracks after reload: ' + JSON.stringify(vals));
  });
  await run.step('SIM-239', 'setup: Overview, stage -> Open, read join link', orgPage, async t => {
    await tab(orgPage, 'overview');
    await orgPage.locator('#ov-stage').selectOption('open');
    await waitLive(orgPage);
    await orgPage.reload(); await tab(orgPage, 'overview');
    const st = await orgPage.locator('#ov-stage').inputValue();
    if (st !== 'open') t.fail('stage after reload: ' + st);
    joinA = (await orgPage.locator('#ov-join-url').innerText()).trim();
    if (!/\/join\//.test(joinA)) t.fail('no join link: ' + joinA);
  });
  if (!joinA) throw new Error('no join link, cannot continue');

  // 24 participants, one context each, all join at once.
  for (let i = 1; i <= 24; i++) people[i] = await run.person(chromium, P[i - 1], { label: 'p' + i });
  const joinRes = await Promise.all(Object.entries(people).map(([i, p]) => run.step('SIM-239', `setup: p${i} joins from the join link (24 at once, code of conduct)`, p.page, t => joinEvent(t, p, joinA, 'Person ' + String(i).padStart(2, '0')))));
  const joined = joinRes.filter(Boolean).length;

  // ================= SIM-239 teams =================
  const names = {}; for (let i = 1; i <= 12; i++) names[i] = 'Team ' + String(i).padStart(2, '0');
  // twelve start teams at the same moment
  for (let i = 1; i <= 12; i++) await people[i].page.locator('#start-team-name').fill(names[i]);
  await Promise.all(Array.from({ length: 12 }, (_, k) => k + 1).map(i => run.step('SIM-239', `p${i} starts "${names[i]}" (12 at the same moment)`, people[i].page, async t => {
    const page = people[i].page;
    await page.locator('#start-team-form button[type=submit]').click();
    try { await waitTeam(page); } catch (e) {
      const msg = (await live(page)).text;
      await page.reload(); await dismissTours(page);
      const after = await teamCode(page);
      t.fail('no team on screen 20 s after Start a team (message ' + JSON.stringify(msg) + '); after a reload ' + (after ? 'the team is there' : 'still no team') + ' [' + String(e.message).split('\n')[0].slice(0, 80) + ']');
      if (!after) return;
    }
    codes[i] = await teamCode(page);
    const nm = await teamName(page);
    if (nm !== names[i]) t.fail('team shown is ' + JSON.stringify(nm));
  })));
  await run.step('SIM-239', '12 teams exist with distinct codes (organiser API check)', null, async t => {
    const d = await orgTeams(slugA);
    const teams = d.teams || [];
    if (teams.length !== 12) t.fail('teams on the server: ' + teams.length);
    const cs = Object.values(codes); if (new Set(cs).size !== cs.length || cs.length !== 12) t.fail('codes: ' + cs.length + ' shown, ' + new Set(cs).size + ' distinct');
  });

  // others join by code at the same moment
  const wave = { 13: 1, 14: 1, 15: 1, 16: 2, 17: 2, 18: 3, 19: 4, 20: 5 };
  for (const [i, tn] of Object.entries(wave)) await people[i].page.locator('#join-team-code').fill(codes[tn] || 'missing');
  await Promise.all(Object.entries(wave).map(([i, tn]) => run.step('SIM-239', `p${i} joins ${names[tn]} by team code (8 at the same moment; three into Team 01)`, people[i].page, async t => {
    const page = people[i].page;
    await page.locator('#join-team-form button[type=submit]').click();
    try { await waitTeam(page); } catch { t.fail('not on a team after Join a team: ' + (await live(page)).text); return; }
    if ((await teamName(page)) !== names[tn]) t.fail('on team ' + await teamName(page));
  })));
  await run.step('SIM-239', 'Team 01 shows 4 members and the size line for a full team', people[1].page, async t => {
    const page = people[1].page; await onTeamPage(people[1], slugA);
    const n = await page.locator('#team-members .member').count();
    const size = (await page.locator('#team-size').innerText()).trim();
    t.note('size line: ' + size);
    if (n !== 4) t.fail('members listed: ' + n);
  });
  await run.step('SIM-239', 'fifth person (p21) tries to join full Team 01: clear refusal', people[21].page, async t => {
    const page = people[21].page;
    await page.locator('#join-team-code').fill(codes[1]);
    await page.locator('#join-team-form button[type=submit]').click();
    const l = await waitLive(page);
    t.note('message: ' + JSON.stringify(l.text));
    if (!l.err || !/full/i.test(l.text)) t.fail('refusal message not clear: ' + JSON.stringify(l.text));
    if (!(await noTeamShown(page))) t.fail('p21 is shown on a team');
    const d = await orgTeams(slugA); const t1 = (d.teams || []).find(x => x.name === names[1]);
    if (t1 && (t1.members || []).length !== 4) t.fail('Team 01 has ' + t1.members.length + ' members on the server');
  });
  await run.step('SIM-239', 'p21 then joins Team 06 instead', people[21].page, async t => {
    const page = people[21].page;
    await page.locator('#join-team-code').fill(codes[6]);
    await page.locator('#join-team-form button[type=submit]').click();
    try { await waitTeam(page); } catch { t.fail('not on Team 06: ' + (await live(page)).text); }
  });
  await run.step('SIM-239', 'race for the last place: p22 and p23 join Team 02 (3 of 4) at the same moment; exactly one gets in, the other is told it is full', people[22].page, async t => {
    for (const i of [22, 23]) await people[i].page.locator('#join-team-code').fill(codes[2]);
    await Promise.all([22, 23].map(i => people[i].page.locator('#join-team-form button[type=submit]').click()));
    await sleep(2500);
    const res = {};
    for (const i of [22, 23]) { const pg = people[i].page; res[i] = { team: await pg.locator('#ev-team').isVisible(), msg: (await live(pg)).text }; }
    t.note(JSON.stringify(res));
    const ins = [22, 23].filter(i => res[i].team);
    if (ins.length !== 1) t.fail(ins.length + ' of the two got in');
    const out = [22, 23].find(i => !res[i].team);
    if (out && !/full/i.test(res[out].msg)) t.fail(`p${out} message: ` + JSON.stringify(res[out].msg));
    const d = await orgTeams(slugA); const t2 = (d.teams || []).find(x => x.name === names[2]);
    if (t2 && t2.members.length > 4) t.fail('Team 02 has ' + t2.members.length + ' members (over the cap)');
  });

  // leave and rejoin
  let oldCode3 = codes[3];
  await run.step('SIM-239', 'p18 leaves Team 03 (Leave team, confirm)', people[18].page, async t => {
    const page = people[18].page;
    await onTeamPage(people[18], slugA);
    await page.locator('#team-leave').click();
    const title = await confirmDlg(page); t.note('dialog: ' + title);
    await page.waitForTimeout(2000);
    if (!(await noTeamShown(page))) t.fail('still on a team after leaving: ' + (await live(page)).text);
  });
  await run.step('SIM-239', 'p18 rejoins Team 03 with the code they had', people[18].page, async t => {
    const page = people[18].page;
    await onTeamPage(people[3], slugA);
    const now3 = await teamCode(people[3].page);
    if (now3 !== oldCode3) t.note('Team 03 code changed after p18 left (' + (now3 ? 'new code' : 'none') + ')');
    await page.locator('#join-team-code').fill(oldCode3);
    await page.locator('#join-team-form button[type=submit]').click();
    try { await waitTeam(page, 8000); }
    catch {
      const l = await live(page);
      t.fail('the old code no longer works after leaving: ' + JSON.stringify(l.text) + (now3 !== oldCode3 ? ' (team code was rotated)' : ''));
      if (now3 && now3 !== oldCode3) { await page.locator('#join-team-code').fill(now3); await page.locator('#join-team-form button[type=submit]').click(); await waitTeam(page, 8000).catch(() => {}); codes[3] = now3; }
      return;
    }
    if ((await teamName(page)) !== names[3]) t.fail('rejoined ' + await teamName(page));
  });
  await run.step('SIM-239', 'p12 leaves Team 12 as its last member', people[12].page, async t => {
    const page = people[12].page;
    await onTeamPage(people[12], slugA);
    await page.locator('#team-leave').click();
    await confirmDlg(page);
    await page.waitForTimeout(2000);
    if (!(await noTeamShown(page))) t.fail('still on a team: ' + (await live(page)).text);
    const d = await orgTeams(slugA); const t12 = (d.teams || []).find(x => x.name === names[12]);
    t.note(t12 ? 'Team 12 still exists with ' + t12.members.length + ' members' : 'Team 12 was removed');
    if (t12 && !t12.members.length) t.fail('an empty Team 12 is left behind after the last member left');
  });

  // tracks
  await run.step('SIM-239', 'p1 chooses track AI, saves, reload keeps it', people[1].page, async t => {
    const page = people[1].page; await onTeamPage(people[1], slugA);
    const opts = await page.locator('#team-track option').allInnerTexts(); t.note('options: ' + opts.join(' | '));
    await page.locator('#team-track').selectOption({ label: 'AI' });
    await page.locator('#team-track-save').click();
    const l = await waitLive(page); if (!l.ok) t.fail('save: ' + l.text);
    await page.reload(); await dismissTours(page);
    const v = await page.locator('#team-track option:checked').innerText(); if (v !== 'AI') t.fail('after reload: ' + v);
  });
  await run.step('SIM-239', 'p1 changes the track to Climate, reload keeps it; teammate p13 sees it', people[1].page, async t => {
    const page = people[1].page;
    await page.locator('#team-track').selectOption({ label: 'Climate' });
    await page.locator('#team-track-save').click();
    const l = await waitLive(page); if (!l.ok) t.fail('save: ' + l.text);
    await page.reload(); await dismissTours(page);
    const v = await page.locator('#team-track option:checked').innerText(); if (v !== 'Climate') t.fail('after reload: ' + v);
    await onTeamPage(people[13], slugA);
    const v13 = await people[13].page.locator('#team-track option:checked').innerText(); if (v13 !== 'Climate') t.fail('p13 sees ' + v13);
  });
  await run.step('SIM-239', 'p2 (AI) and p16 (Climate) on Team 02 save a track at the same time', people[2].page, async t => {
    await onTeamPage(people[2], slugA); await onTeamPage(people[16], slugA);
    await people[2].page.locator('#team-track').selectOption({ label: 'AI' });
    await people[16].page.locator('#team-track').selectOption({ label: 'Climate' });
    await Promise.all([people[2].page.locator('#team-track-save').click(), people[16].page.locator('#team-track-save').click()]);
    await sleep(1500);
    const msgs = [await live(people[2].page), await live(people[16].page)].map(l => l.text);
    const server = ((await orgTeams(slugA)).teams || []).find(x => x.name === names[2]);
    const stored = server && server.track ? (server.track.name || server.track.slug || server.track) : '(none)';
    const before = [await people[2].page.locator('#team-track option:checked').innerText(), await people[16].page.locator('#team-track option:checked').innerText()];
    await people[2].page.reload(); await people[16].page.reload(); await dismissTours(people[2].page); await dismissTours(people[16].page);
    const after = [await people[2].page.locator('#team-track option:checked').innerText(), await people[16].page.locator('#team-track option:checked').innerText()];
    t.note(`messages ${JSON.stringify(msgs)}; stored ${JSON.stringify(stored)}; screens before reload ${JSON.stringify(before)}; after reload ${JSON.stringify(after)}`);
    if (after[0] !== after[1]) t.fail('the two members see different tracks after reload');
    if (msgs.some(m => /went wrong/i.test(m))) t.fail('one of the two simultaneous saves failed with "Something went wrong. Try again." (HTTP 500)');
    if (before[0] !== before[1] && msgs.every(m => /saved/i.test(m))) t.fail('both were told "Track choice saved." but one choice was silently replaced; the loser\'s screen still shows ' + JSON.stringify(before.find(b => b !== after[0])) + ' until reload');
  });

  await run.step('SIM-239', 'track race repeated 5 times (p2 and p16 press Save track choice together): any 500?', people[2].page, async t => {
    let fails = 0; const seen = [];
    for (let k = 0; k < 5; k++) {
      const [a, b] = k % 2 ? ['Climate', 'AI'] : ['AI', 'Climate'];
      await people[2].page.locator('#team-track').selectOption({ label: a });
      await people[16].page.locator('#team-track').selectOption({ label: b });
      const st = [];
      const watch = pg => pg.waitForResponse(r => r.url().includes('/team/track'), { timeout: 10000 }).then(r => r.status()).catch(() => 'none');
      const w = [watch(people[2].page), watch(people[16].page)];
      await Promise.all([people[2].page.locator('#team-track-save').click(), people[16].page.locator('#team-track-save').click()]);
      st.push(...await Promise.all(w)); seen.push(st.join('/'));
      if (st.some(x => x >= 500)) fails++;
      await sleep(400);
    }
    t.note('statuses per round: ' + seen.join(', '));
    if (fails) t.fail(`${fails} of 5 rounds had a 500 on PUT /team/track`);
  });

  // ================= SIM-240 entry =================
  const E = { title: 'SafeRoute', tagline: 'Safer walks to school', description: 'A map that lets parents and teachers mark safe routes.\nSecond line.', video: 'https://www.youtube.com/watch?v=dQw4w9WgXcQ', code: 'https://github.com/example/saferoute' };
  await run.step('SIM-240', 'p1 fills the entry (title, tagline, description, video, code) and saves; reload keeps it', people[1].page, async t => {
    const page = people[1].page; await onTeamPage(people[1], slugA);
    await page.locator('#en-title').fill(E.title); await page.locator('#en-tagline').fill(E.tagline);
    await page.locator('#en-description').fill(E.description); await page.locator('#en-video').fill(E.video); await page.locator('#en-code').fill(E.code);
    await page.locator('#en-save').click();
    const l = await waitLive(page); if (!l.ok) t.fail('save: ' + l.text + ' ' + await visibleError(page));
    t.note('status: ' + (await page.locator('#entry-status').innerText()).trim());
    await page.reload(); await dismissTours(page); await page.waitForTimeout(800);
    const v = await fieldVals(page);
    const want = { 'en-title': E.title, 'en-tagline': E.tagline, 'en-description': E.description, 'en-video': E.video, 'en-code': E.code };
    for (const k in want) if (v[k] !== want[k]) t.fail(`${k} after reload ${JSON.stringify(v[k])}`);
  });
  await run.step('SIM-240', 'p1 uploads a PNG screenshot (Pillow); it shows after reload', people[1].page, async t => {
    const page = people[1].page;
    await page.locator('#en-shot').setInputFiles(SHOT);
    const l = await waitLive(page); if (!l.ok) t.fail('upload: ' + l.text + ' ' + await visibleError(page));
    await page.reload(); await dismissTours(page);
    await page.locator('#en-shot-img').waitFor({ state: 'visible', timeout: 10000 }).catch(() => t.fail('no screenshot shown after reload'));
    const w = await page.locator('#en-shot-img').evaluate(i => i.naturalWidth).catch(() => 0); if (!w) t.fail('screenshot image did not load');
  });
  await run.step('SIM-240', 'p1 edits the entry (title and tagline), saves; reload keeps it', people[1].page, async t => {
    const page = people[1].page;
    E.title = 'SafeRoute 2'; E.tagline = 'Safer walks to school, now with buddies';
    await page.locator('#en-title').fill(E.title); await page.locator('#en-tagline').fill(E.tagline);
    await page.locator('#en-save').click();
    const l = await waitLive(page); if (!l.ok) t.fail('save: ' + l.text);
    await page.reload(); await dismissTours(page); await page.waitForTimeout(600);
    const v = await fieldVals(page); if (v['en-title'] !== E.title || v['en-tagline'] !== E.tagline) t.fail('after reload: ' + JSON.stringify(v));
  });
  await run.step('SIM-240', 'p1 and p13 (same team) save different fields at the same time (title / code link): both kept', people[13].page, async t => {
    await onTeamPage(people[1], slugA); await onTeamPage(people[13], slugA);
    await people[1].page.locator('#en-title').fill('SafeRoute by P1');
    await people[13].page.locator('#en-code').fill('https://github.com/example/saferoute-p13');
    await Promise.all([people[1].page.locator('#en-save').click(), people[13].page.locator('#en-save').click()]);
    await sleep(2000);
    await people[1].page.reload(); await dismissTours(people[1].page); await people[1].page.waitForTimeout(600);
    const v = await fieldVals(people[1].page);
    if (v['en-title'] !== 'SafeRoute by P1') t.fail('title lost: ' + v['en-title']);
    if (v['en-code'] !== 'https://github.com/example/saferoute-p13') t.fail('code link lost: ' + v['en-code']);
    E.title = 'SafeRoute by P1'; E.code = 'https://github.com/example/saferoute-p13';
  });
  await run.step('SIM-240', 'entry save race repeated 5 times (p1 title, p13 tagline, Save together): any 500?', people[1].page, async t => {
    let fails = 0; const seen = [];
    for (let k = 0; k < 5; k++) {
      await people[1].page.locator('#en-title').fill('SafeRoute by P1 r' + k);
      await people[13].page.locator('#en-tagline').fill('Tagline round ' + k);
      const watch = pg => pg.waitForResponse(r => r.url().endsWith('/entry') && r.request().method() === 'PUT', { timeout: 10000 }).then(r => r.status()).catch(() => 'none');
      const w = [watch(people[1].page), watch(people[13].page)];
      await Promise.all([people[1].page.locator('#en-save').click(), people[13].page.locator('#en-save').click()]);
      const st = await Promise.all(w); seen.push(st.join('/'));
      if (st.some(x => x >= 500)) fails++;
      await sleep(400);
    }
    t.note('statuses per round: ' + seen.join(', '));
    if (fails) t.fail(`${fails} of 5 rounds had a 500 on PUT /entry`);
    await people[1].page.locator('#en-title').fill(E.title); await people[1].page.locator('#en-save').click(); await waitLive(people[1].page);
  });
  await run.step('SIM-240', 'p1 and p13 both edit the description at the same time: what happens to the other\'s text', people[1].page, async t => {
    await onTeamPage(people[1], slugA); await onTeamPage(people[13], slugA);
    const d1 = 'P1 wrote this long description about the safe routes map.';
    const d13 = 'P13 wrote a different description about buddies.';
    await people[1].page.locator('#en-description').fill(d1);
    await people[13].page.locator('#en-description').fill(d13);
    await people[1].page.locator('#en-save').click(); await waitLive(people[1].page);
    await people[13].page.locator('#en-save').click(); const l13 = await waitLive(people[13].page);
    const l1 = await live(people[1].page);
    const p1screen = await people[1].page.locator('#en-description').inputValue();
    await people[1].page.reload(); await dismissTours(people[1].page); await people[1].page.waitForTimeout(600);
    const stored = await people[1].page.locator('#en-description').inputValue();
    t.note(`p1 told ${JSON.stringify(l1.text)}, p13 told ${JSON.stringify(l13.text)}; p1's screen before reload: ${p1screen === d1 ? 'own text' : 'other'}; stored: ${stored === d13 ? "p13's text" : stored === d1 ? "p1's text" : JSON.stringify(stored)}`);
    if (stored === d13 && /saved/i.test(l13.text) && !/teammate|changed|someone|overwr/i.test(l13.text)) t.fail("p1's description was silently overwritten by p13's later save; neither was warned, and p1's screen still shows p1's own text as saved until a reload");
    E.description = stored;
  });

  // publish a team site from the website
  let siteURL = '';
  await run.step('SIM-240', 'Publish section: Get my key, see what the page offers to publish a site', people[1].page, async t => {
    const page = people[1].page; await onTeamPage(people[1], slugA);
    t.note('publish text: ' + (await page.locator('#ev-publish').innerText()).replace(/\s+/g, ' ').slice(0, 400));
    await page.locator('#pub-get').click();
    await page.locator('#pub-once').waitFor({ state: 'visible', timeout: 10000 }).catch(() => t.fail('no key shown after Get my key: ' + 'none'));
    const cmd = await page.locator('#pub-curl').inputValue().catch(() => '');
    const hasUpload = await page.locator('#ev-publish input[type=file], #ev-publish textarea').count();
    t.note('offers: key + ' + (cmd ? 'a curl command (' + cmd.replace(/X-API-Key: [^"]+/, 'X-API-Key: ***').slice(0, 160) + ')' : 'no command') + (hasUpload ? '' : '; no file upload or editor on the page'));
    if (!hasUpload) t.fail('the website offers no way to publish a team site without an AI app (MCP) or the command line (curl): only "Connect your AI app" and "Get my key" + a curl command');
    people[1].curl = cmd;
  });
  await run.step('SIM-240', 'publish via the page\'s own curl command (command line, to get past the website-only gap) and the site address loads', people[1].page, async t => {
    if (!people[1].curl) { t.fail('no command to run'); return; }
    let out = '';
    try { out = execFileSync('bash', ['-c', people[1].curl + ' -s -w "\\nHTTP %{http_code}"'], { encoding: 'utf8', timeout: 30000 }); }
    catch (e) { out = String(e.stdout || e.message); }
    const code = (out.match(/HTTP (\d+)/) || [])[1];
    t.note('curl HTTP ' + code);
    if (!/^2/.test(code || '')) { t.fail('curl publish returned ' + code + ': ' + out.slice(0, 300)); return; }
    const page = people[1].page;
    for (let i = 0; i < 18; i++) {
      await onTeamPage(people[1], slugA);
      siteURL = (await page.locator('#site-url').getAttribute('href').catch(() => '')) || '';
      if (siteURL && await page.locator('#site-url-p').isVisible()) break;
      await sleep(10000);
    }
    t.note('site line: ' + (await page.locator('#site-live').innerText().catch(() => '')).trim() + '; address ' + siteURL);
    if (!siteURL) { t.fail('no team site address shown after publishing (still "' + (await page.locator('#site-setup').innerText().catch(() => '')) + '")'); return; }
    const sp = await run.person(chromium, null, { label: 'visitor' });
    let body = '';
    for (let i = 0; i < 12; i++) {
      const r = await sp.page.goto(siteURL, { waitUntil: 'domcontentloaded' }).catch(e => null);
      body = await sp.page.locator('body').innerText().catch(() => '');
      if (r && r.status() < 400 && /Hello/.test(body)) break;
      await sleep(10000);
    }
    if (!/Hello/.test(body)) t.fail('team site at ' + siteURL + ' does not show the published page: ' + body.slice(0, 200));
    await sp.context.close();
  });

  // returning participant finds entry and site
  await run.step('SIM-240', 'returning p1 starts at /events and finds the entry and the team site', null, async t => {
    const back = await run.person(chromium, P[0], { label: 'p1-returning' });
    const page = back.page;
    await goto(page, '/events');
    const card = page.locator('#events-list a.card').filter({ hasText: titleA });
    if (!(await card.count())) { t.fail('event not listed on /events'); await page.screenshot({ path: OUT + '/teams-entries-events.png', fullPage: true }); return; }
    const cardText = (await card.innerText()).replace(/\s+/g, ' ');
    t.note('card: ' + cardText + ' (no team, entry or site named on the card)');
    await card.click(); await page.waitForLoadState('networkidle').catch(() => {}); await dismissTours(page);
    const url = page.url();
    const title = await page.locator('#en-title').inputValue().catch(() => '');
    const site = await page.locator('#site-url').getAttribute('href').catch(() => '');
    const order = await page.evaluate(() => ['ev-team', 'ev-site', 'ev-publish', 'ev-entry'].map(id => { const n = document.getElementById(id); return id + '@' + (n ? Math.round(n.getBoundingClientRect().top + scrollY) : -1); }).join(' '));
    t.note(`one click -> ${url}; entry title ${JSON.stringify(title)}; site ${site ? 'link shown' : 'not shown'}; sections at ${order}`);
    if (title !== E.title) t.fail('entry not found on the event page');
    if (!site) t.fail('team site address not shown');
    await page.screenshot({ path: OUT + '/teams-entries-returning-team.png', fullPage: true });
    await goto(page, '/account');
    const acctText = (await page.locator('#view-account').innerText()).replace(/\s+/g, ' ');
    const links = await page.locator('#view-account a').evaluateAll(as => as.map(a => a.getAttribute('href')));
    t.note('/account links: ' + JSON.stringify(links));
    if (!new RegExp(titleA).test(acctText) && !links.some(h => /\/e\/|\/events/.test(h || ''))) t.fail('/account has no link to your events, entry or team site (dead end for a returning participant)');
    const nav = await page.locator('header a, nav a').evaluateAll(as => as.map(a => (a.textContent || '').trim() + '=' + a.getAttribute('href'))).catch(() => []);
    t.note('header links on /account: ' + JSON.stringify(nav));
    await page.screenshot({ path: OUT + '/teams-entries-returning-account.png', fullPage: true });
    // the public event page
    const pub = await page.goto(`https://${slugA}.simple-hack.app/`, { waitUntil: 'domcontentloaded' }).catch(() => null);
    const pubText = await page.locator('body').innerText().catch(() => '');
    t.note(`public event page ${pub && pub.status()}: entry ${pubText.includes(E.title) ? 'listed' : 'not listed'}, site ${siteURL && pubText.includes(siteURL.replace(/^https?:\/\//, '').replace(/\/$/, '')) ? 'linked' : 'not linked'}`);
    await back.context.close();
  });

  // ================= SIM-237 organiser =================
  const expectTeams = async () => (await orgTeams(slugA));
  await run.step('SIM-237', 'Overview and Teams tab counts match reality (participants, teams, on no team, member counts)', orgPage, async t => {
    const d = await expectTeams();
    const teams = d.teams || [], none = d.on_no_team || d.unteamed || d.no_team || [];
    const onTeams = teams.reduce((s, x) => s + x.members.length, 0);
    await goto(orgPage, `/e/${slugA}/manage`); await tab(orgPage, 'overview');
    const stats = (await orgPage.locator('#ov-stats').innerText()).replace(/\s+/g, ' ');
    t.note(`server: ${teams.length} teams, ${onTeams} on teams, ${none.length} on no team, ${joined} joined; Overview: ${stats}`);
    const num = label => { const m = stats.match(new RegExp('(\\d+)\\s*' + label, 'i')) || stats.match(new RegExp(label + '\\s*(\\d+)', 'i')); return m ? Number(m[1]) : null; };
    if (num('Participants') !== joined) t.fail('Participants shows ' + num('Participants') + ', expected ' + joined);
    if (num('Teams') !== teams.length) t.fail('Teams shows ' + num('Teams') + ', expected ' + teams.length);
    if (num('On no team') !== none.length) t.fail('On no team shows ' + num('On no team') + ', expected ' + none.length);
    if (onTeams + none.length !== joined) t.fail(`on teams (${onTeams}) + on no team (${none.length}) != participants (${joined})`);
    await tab(orgPage, 'teams');
    const rows = await orgPage.locator('#teams-list .team-row-head').allInnerTexts();
    if (rows.length !== teams.length) t.fail('Teams tab lists ' + rows.length + ' teams');
    for (const x of teams) {
      const r = rows.find(s => s.startsWith(x.name)); if (!r) { t.fail('missing ' + x.name); continue; }
      if (!r.includes(String(x.members.length))) t.fail(`${x.name} row "${r.replace(/\s+/g, ' ')}" does not show ${x.members.length}`);
    }
    const un = await orgPage.locator('#unteamed-list .person').allInnerTexts();
    t.note('Not on a team yet: ' + un.join(', '));
    if (un.length !== none.length) t.fail('"Not on a team yet" lists ' + un.length + ', expected ' + none.length);
    await tab(orgPage, 'projects');
    t.note('Entries tab: ' + ((await orgPage.locator('#projects-list > p').first().innerText().catch(() => '')).trim()));
  });
  await run.step('SIM-237', 'rename Team 05 to "Team 05 Renamed"; p5 sees the new name', orgPage, async t => {
    await tab(orgPage, 'teams');
    const row = await openManage(orgPage, names[5]);
    await row.locator('form input').fill('Team 05 Renamed');
    await row.locator('button', { hasText: 'Save team name' }).click();
    const l = await waitLive(orgPage); t.note('message: ' + l.text); if (!l.ok) t.fail('rename: ' + l.text);
    if (!(await teamRow(orgPage, 'Team 05 Renamed').count())) t.fail('renamed team not listed');
    await onTeamPage(people[5], slugA);
    if ((await teamName(people[5].page)) !== 'Team 05 Renamed') t.fail('p5 sees ' + await teamName(people[5].page));
    names[5] = 'Team 05 Renamed';
  });
  await run.step('SIM-237', 'move p19 from Team 04 to Team 07 (Move someone here); p19 and p4 see it', orgPage, async t => {
    const row = await openManage(orgPage, names[7]);
    await row.locator('select').selectOption({ label: 'Person 19' });
    await row.locator('button', { hasText: /^Move$/ }).click();
    const title = await confirmDlg(orgPage).catch(() => '(no confirm)'); t.note('dialog: ' + title);
    const l = await waitLive(orgPage); if (!l.ok) t.fail('move: ' + l.text);
    const d = await expectTeams();
    const t7 = d.teams.find(x => x.name === names[7]), t4 = d.teams.find(x => x.name === names[4]);
    if (!t7 || !t7.members.some(m => m.display_name === 'Person 19')) t.fail('p19 not on Team 07 on the server');
    if (t4 && t4.members.some(m => m.display_name === 'Person 19')) t.fail('p19 still on Team 04');
    await onTeamPage(people[19], slugA); if ((await teamName(people[19].page)) !== names[7]) t.fail('p19 sees team ' + JSON.stringify(await teamName(people[19].page)));
    await onTeamPage(people[4], slugA); const m4 = await people[4].page.locator('#team-members').innerText(); if (/Person 19/.test(m4)) t.fail('p4 still sees Person 19 on Team 04');
  });
  await run.step('SIM-237', 'take p20 off Team 05; p20 sees no team, p5 sees the new team code', orgPage, async t => {
    const oldCode = codes[5];
    const row = await openManage(orgPage, names[5]);
    await row.locator('.member', { hasText: 'Person 20' }).locator('button', { hasText: 'Take off team' }).click();
    await confirmDlg(orgPage);
    const l = await waitLive(orgPage); if (!l.ok) t.fail('take off: ' + l.text);
    await onTeamPage(people[20], slugA); if (!(await noTeamShown(people[20].page))) t.fail('p20 still sees a team: ' + await teamName(people[20].page));
    t.note('p20 sees: ' + (await people[20].page.locator('#ev-noteam').innerText().catch(() => '')).replace(/\s+/g, ' ').slice(0, 120));
    await onTeamPage(people[5], slugA); const nc = await teamCode(people[5].page);
    t.note('Team 05 code ' + (nc === oldCode ? 'unchanged' : 'changed'));
    if (nc === oldCode) t.fail('dialog said "Their team gets a new code" but the code did not change');
    codes[5] = nc;
  });
  await run.step('SIM-237', 'remove Team 09; p9 sees no team and is in "Not on a team yet"', orgPage, async t => {
    const row = await openManage(orgPage, names[9]);
    await row.locator('button', { hasText: 'Remove team' }).click();
    await confirmDlg(orgPage);
    const l = await waitLive(orgPage); if (!l.ok) t.fail('remove: ' + l.text);
    // The message shows before the list reloads; give the list a few seconds.
    for (let k = 0; k < 20 && await teamRow(orgPage, names[9]).count(); k++) await sleep(250);
    if (await teamRow(orgPage, names[9]).count()) t.fail('Team 09 still listed 5 s after "Team removed"');
    const un = await orgPage.locator('#unteamed-list').innerText(); if (!/Person 09/.test(un)) t.fail('Person 09 not in Not on a team yet');
    await onTeamPage(people[9], slugA); if (!(await noTeamShown(people[9].page))) t.fail('p9 still sees a team');
    const msg = (await people[9].page.locator('#ev-noteam').innerText().catch(() => '')).replace(/\s+/g, ' ').slice(0, 160);
    t.note('p9 sees: ' + msg);
  });
  await run.step('SIM-237', 'counts after the changes still match ("Not on a team yet", Overview)', orgPage, async t => {
    const d = await expectTeams(); const none = d.on_no_team || d.unteamed || d.no_team || [];
    await orgPage.reload(); await tab(orgPage, 'teams');
    const un = await orgPage.locator('#unteamed-list .person').allInnerTexts();
    if (un.length !== none.length) t.fail('Not on a team yet lists ' + un.length + ', server ' + none.length);
    await tab(orgPage, 'overview');
    const stats = (await orgPage.locator('#ov-stats').innerText()).replace(/\s+/g, ' ');
    t.note('Overview: ' + stats + '; server teams ' + d.teams.length + ', no team ' + none.length);
    if (!new RegExp('\\b' + d.teams.length + '\\b').test(stats)) t.fail('team count not on Overview');
  });
  await run.step('SIM-237', 'set an event deadline (Settings, +3 days) so teams can be given more time', orgPage, async t => {
    await tab(orgPage, 'settings');
    await orgPage.locator('#set-deadline').fill(local(day(3)));
    await orgPage.locator('#set-deadline-save').click();
    const l = await waitLive(orgPage); if (!l.ok) t.fail('save deadline: ' + l.text);
  });
  await run.step('SIM-237', 'give Team 02 more time (+5 days) on Entries; p2 sees it', orgPage, async t => {
    await tab(orgPage, 'projects');
    const card = entryCard(orgPage, names[2]);
    await card.locator('button', { hasText: 'Give more time' }).click();
    await card.locator('form.more-time input').fill(local(day(5)));
    await card.locator('form.more-time button[type=submit]').click();
    const l = await waitLive(orgPage); t.note('message: ' + l.text); if (!l.ok) t.fail('give more time: ' + l.text);
    const c2 = (await entryCard(orgPage, names[2]).innerText()).replace(/\s+/g, ' '); if (!/More time/.test(c2)) t.fail('card does not show More time: ' + c2.slice(0, 200));
    await onTeamPage(people[2], slugA);
    const ext = await people[2].page.locator('#site-extended').isVisible(); const dl = (await people[2].page.locator('#site-deadline').innerText()).trim();
    t.note('p2 sees: ' + dl + (ext ? ' + "The organiser gave your team more time."' : ''));
    if (!ext) t.fail('p2 does not see the extension');
    await onTeamPage(people[1], slugA); t.note('p1 (no extension) sees: ' + (await people[1].page.locator('#site-deadline').innerText()).trim());
  });
  await run.step('SIM-237', 'take Team 01 site down with a reason; p1 sees it and why; the site address stops serving', orgPage, async t => {
    await tab(orgPage, 'projects');
    const card = entryCard(orgPage, names[1]);
    const btn = card.locator('button', { hasText: 'Take site down' });
    if (!(await btn.count())) { t.fail('no Take site down button on Team 01 (site published: ' + !!siteURL + ')'); return; }
    await btn.click();
    await orgPage.locator('dialog.sh-dlg[open] .sh-dlg-field input').fill('Uses someone else\'s logo (e2e test)');
    await confirmDlg(orgPage);
    const l = await waitLive(orgPage); if (!l.ok) t.fail('take down: ' + l.text);
    await onTeamPage(people[1], slugA);
    const s = (await people[1].page.locator('#ev-site').innerText()).replace(/\s+/g, ' ');
    t.note('p1 site card: ' + s.slice(0, 300));
    if (!/Taken down/.test(s) || !/logo/.test(s)) t.fail('p1 does not see the takedown and reason');
    if (await people[1].page.locator('#en-save').isVisible()) t.fail('entry still editable while site is taken down');
    if (siteURL) { const r = await fetch(siteURL); const b = await r.text(); t.note('site address while down: HTTP ' + r.status); if (/<h1>Hello<\/h1>/.test(b) && r.ok) t.fail('site still serves the page while taken down'); }
  });
  await run.step('SIM-237', 'put Team 01 site back; p1 sees it published and can edit again', orgPage, async t => {
    await tab(orgPage, 'projects');
    const card = entryCard(orgPage, names[1]);
    await card.locator('button', { hasText: 'Put site back' }).click();
    await confirmDlg(orgPage);
    const l = await waitLive(orgPage); if (!l.ok) t.fail('restore: ' + l.text);
    await onTeamPage(people[1], slugA);
    const s = (await people[1].page.locator('#site-live').innerText()).trim(); t.note('p1: ' + s);
    if (/Taken down/.test(s)) t.fail('p1 still sees Taken down');
    if (!(await people[1].page.locator('#en-save').isVisible())) t.fail('entry not editable after restore');
    if (siteURL) { let ok = false; for (let i = 0; i < 6 && !ok; i++) { const r = await fetch(siteURL); ok = r.ok && /Hello/.test(await r.text()); if (!ok) await sleep(5000); } if (!ok) t.fail('site address does not serve the page after restore'); }
  });

  // ================= SIM-240 entries closed =================
  await onTeamPage(people[13], slugA); // p13 keeps this page open (stale) across the close
  await run.step('SIM-240', 'organiser moves the event to Entries closed (Overview, confirm)', orgPage, async t => {
    await tab(orgPage, 'overview');
    const opts = await orgPage.locator('#ov-stage option').evaluateAll(os => os.map(o => o.value));
    t.note('stages offered: ' + opts.join(','));
    if (!opts.includes('closed')) {
      if (opts.includes('building')) { await orgPage.locator('#ov-stage').selectOption('building'); await waitLive(orgPage); await orgPage.reload(); await tab(orgPage, 'overview'); }
      const o2 = await orgPage.locator('#ov-stage option').evaluateAll(os => os.map(o => o.value));
      if (!o2.includes('closed')) { t.fail('no Entries closed stage offered: ' + o2.join(',')); return; }
      t.note('Entries closed only offered after Building');
    }
    await orgPage.locator('#ov-stage').selectOption('closed');
    const title = await confirmDlg(orgPage); t.note('dialog: ' + title);
    const l = await waitLive(orgPage); if (!l.ok) t.fail('stage: ' + l.text);
  });
  await run.step('SIM-240', 'after Entries closed p1 cannot edit the entry or publish, and the page says why', people[1].page, async t => {
    const page = people[1].page; await onTeamPage(people[1], slugA);
    const formVis = await page.locator('#entry-form').isVisible();
    const pubVis = await page.locator('#ev-publish').isVisible();
    const why = [(await page.locator('#entry-status').innerText()).trim(), (await page.locator('#site-deadline').innerText()).trim()].join(' | ');
    t.note('p1 sees: ' + why + (pubVis ? ' (Publish still shown)' : ''));
    if (formVis) t.fail('entry form still editable');
    if (pubVis) t.fail('Publish section still offered');
    if (!/closed/i.test(why)) t.fail('no message saying entries are closed');
    const ro = (await page.locator('#entry-readonly').innerText().catch(() => '')).replace(/\s+/g, ' ');
    if (!ro.includes(E.title)) t.fail('read-only entry does not show the title: ' + ro.slice(0, 200));
  });
  await run.step('SIM-240', 'p13 had the page open before the close and presses Save: refused with a reason', people[13].page, async t => {
    const page = people[13].page;
    if (!(await page.locator('#en-save').isVisible())) { t.note('form already hidden'); return; }
    await page.locator('#en-tagline').fill('late edit after close');
    await page.locator('#en-save').click();
    const l = await waitLive(page); t.note('message: ' + JSON.stringify(l.text));
    if (!l.err || !/closed/i.test(l.text)) t.fail('late save not clearly refused: ' + JSON.stringify(l.text));
    await onTeamPage(people[1], slugA); const ro = await people[1].page.locator('#entry-readonly').innerText().catch(() => '');
    if (/late edit/.test(ro)) t.fail('late edit was saved');
  });
  await run.step('SIM-240', 'after Entries closed, the publish key/command for Team 01 is refused', null, async t => {
    if (!people[1].curl) { t.note('no command'); return; }
    let out = ''; try { out = execFileSync('bash', ['-c', people[1].curl.replace('<h1>Hello</h1>', '<h1>Late</h1>') + ' -s -w "\\nHTTP %{http_code}"'], { encoding: 'utf8', timeout: 30000 }); } catch (e) { out = String(e.stdout || ''); }
    const code = (out.match(/HTTP (\d+)/) || [])[1]; t.note('HTTP ' + code + ' ' + out.split('\n')[0].slice(0, 200));
    if (/^2/.test(code || '')) t.fail('publish after close accepted');
  });
  await run.step('SIM-240', 'Team 02 (given more time) can still edit after Entries closed, as the close dialog promised', people[2].page, async t => {
    const page = people[2].page; await onTeamPage(people[2], slugA);
    const vis = await page.locator('#entry-form').isVisible();
    t.note('p2: ' + (await page.locator('#site-deadline').innerText()).trim() + ' | ' + (await page.locator('#entry-status').innerText()).trim());
    if (!vis) { t.fail('Team 02 entry is frozen although it was given more time'); return; }
    await page.locator('#en-title').fill('Team 02 late but extended');
    await page.locator('#en-save').click(); const l = await waitLive(page); if (!l.ok) t.fail('save: ' + l.text);
  });

  // ================= phone (event B) =================
  const orgB = await run.person(chromium, orgAccB, { label: 'organiser-B' });
  await run.step('SIM-239', 'setup (phone): a second organiser creates a second event and opens it', orgB.page, async t => {
    const pg = orgB.page;
    await createEventViaForm(pg, { slug: slugB, title: titleB }); created.push(slugB);
    await goto(pg, `/e/${slugB}/manage`); await tab(pg, 'overview');
    await pg.locator('#ov-stage').selectOption('open'); await waitLive(pg);
    joinB = (await pg.locator('#ov-join-url').innerText()).trim();
    if (!/\/join\//.test(joinB)) t.fail('no join link');
  });
  webkit = await run.browser('webkit');
  for (const [engine, br, a, b] of [['Chromium', chromium, 0, 1], ['WebKit', webkit, 2, 3]]) {
    const starter = await run.person(br, P[a], { width: 390, label: `p${a + 1}-${engine}-390` });
    const joiner = await run.person(br, P[b], { width: 390, label: `p${b + 1}-${engine}-390` });
    let code = '';
    await run.step('SIM-239', `phone ${engine} 390px: p${a + 1} joins the event and starts a team`, starter.page, async t => {
      await joinEvent(t, starter, joinB, 'Phone ' + (a + 1));
      await overflowCheck(t, starter.page, 'no-team screen');
      await starter.page.locator('#start-team-name').fill('Phone ' + engine);
      await starter.page.locator('#start-team-form button[type=submit]').click();
      try { await waitTeam(starter.page); } catch { t.fail('no team: ' + (await live(starter.page)).text); return; }
      code = await teamCode(starter.page);
      await overflowCheck(t, starter.page, 'team screen');
      for (const id of ['team-copy', 'team-leave', 'team-track-save', 'en-save']) { const bb = await starter.page.locator('#' + id).boundingBox(); if (bb && bb.height < 32) t.fail(`#${id} tap target only ${Math.round(bb.height)}px tall`); }
    });
    await run.step('SIM-239', `phone ${engine} 390px: p${b + 1} joins the event and joins the team by code`, joiner.page, async t => {
      await joinEvent(t, joiner, joinB, 'Phone ' + (b + 1));
      await joiner.page.locator('#join-team-code').fill(code);
      await joiner.page.locator('#join-team-form button[type=submit]').click();
      try { await waitTeam(joiner.page); } catch { t.fail('not on team: ' + (await live(joiner.page)).text); return; }
      if ((await teamName(joiner.page)) !== 'Phone ' + engine) t.fail('team ' + await teamName(joiner.page));
      await overflowCheck(t, joiner.page, 'team screen');
    });
    await run.step('SIM-240', `phone ${engine} 390px: p${a + 1} fills and edits the entry with a screenshot`, starter.page, async t => {
      const page = starter.page;
      await page.locator('#en-title').scrollIntoViewIfNeeded();
      await page.locator('#en-title').fill('Phone entry ' + engine); await page.locator('#en-tagline').fill('Typed on a phone');
      await page.locator('#en-description').fill('Entry written at 390 pixels wide.'); await page.locator('#en-video').fill('https://vimeo.com/123456');
      await page.locator('#en-code').fill('https://github.com/example/phone');
      await page.locator('#en-save').click(); const l = await waitLive(page); if (!l.ok) t.fail('save: ' + l.text + ' ' + await visibleError(page));
      await page.locator('#en-shot').setInputFiles(SHOT);
      let l2 = await live(page); for (let k = 0; k < 40 && !/Screenshot|image|PNG/i.test(l2.text); k++) { await sleep(250); l2 = await live(page); }
      if (!/Screenshot saved/.test(l2.text)) t.fail('screenshot: ' + l2.text);
      await page.locator('#en-title').fill('Phone entry ' + engine + ' edited'); await page.locator('#en-save').click(); await waitLive(page);
      await page.reload(); await dismissTours(page); await page.waitForTimeout(800);
      const v = await fieldVals(page); if (v['en-title'] !== 'Phone entry ' + engine + ' edited') t.fail('after reload title ' + v['en-title']);
      await overflowCheck(t, page, 'entry screen');
      const shotBox = await page.locator('#en-shot-img').boundingBox(); if (shotBox && shotBox.width > 390) t.fail('screenshot preview wider than screen');
      await page.screenshot({ path: `${OUT}/teams-entries-phone-${engine}.png`, fullPage: true });
    });
    await starter.context.close(); await joiner.context.close();
  }
} catch (e) {
  console.log('aborted: ' + (e.stack || e));
  run.results.push({ sim: '-', name: 'script aborted', ok: false, notes: [String(e.message || e)] });
} finally {
  for (const s of created) {
    const st = await deleteEvent(s === slugB ? orgAccB.api_key : orgAcc.api_key, s).catch(e => 'error ' + e.message);
    console.log(`cleanup: delete ${s} -> ${st}`);
    run.results.push({ sim: 'cleanup', name: `delete ${s}`, ok: st === 200 || st === 204, notes: ['status ' + st] });
  }
  console.log('console errors: ' + JSON.stringify([...new Set(run.console)].slice(0, 40), null, 1));
  console.log('failed requests: ' + JSON.stringify([...new Set(run.failed.map(f => f.replace(/^\[[^\]]+\] /, '')))], null, 1));
  await run.done();
}
