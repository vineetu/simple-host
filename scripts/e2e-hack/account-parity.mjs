// SIM-228 (sign-in and account, key-only accounts) and SIM-246 (MCP parity)
// against production simple-hack.app. Account set 6; set 10's last
// participant is the only account this script deletes.
//   node scripts/e2e-hack/account-parity.mjs            # both parts
//   PART=account node ...  /  PART=mcp node ...         # one part
import fs from 'node:fs';
import { accounts, Run, SITE, ACCOUNTS_DIR, stamp, goto, tab, dismissTours, visibleError, api, deleteEvent } from './lib.mjs';

const OUT = process.env.HACK_OUT || '/tmp/claude-1001/-home-ubuntu-workspace-4/2268cadb-dfb1-4db4-9617-f1516b000a56/scratchpad/e2e-prod';
const PART = process.env.PART || 'all';
const run = new Run('account-parity', OUT);
const S6 = accounts(6);
const S10 = accounts(10);
const day = n => new Date(Date.now() + n * 864e5).toISOString().slice(0, 10);
const created = [];
let creatorKey = null;
const shots = [];

// ---------------------------------------------------------------- helpers
// A person whose key is put in the browser once only, so that signing out
// really leaves the browser without a key (run.person re-adds it on every load).
async function personOnce(browser, account, { width = 1280, label } = {}) {
  const context = await browser.newContext({ viewport: { width, height: 900 } });
  await context.addInitScript(k => {
    try { if (!localStorage.getItem('e2e-seeded')) { localStorage.setItem('apiKey', k); localStorage.setItem('e2e-seeded', '1'); } } catch (e) {}
  }, account.api_key);
  const page = await context.newPage();
  const who = label || account.username;
  page.on('pageerror', e => run.console.push(`error: [${who}] pageerror ${e.message.slice(0, 300)}`));
  page.on('console', m => { if (m.type() === 'error') run.console.push(`error: [${who}] ${m.text().slice(0, 300)}`); });
  page.on('response', r => { if (r.status() >= 400) run.failed.push(`[${who}] ${r.status()} ${r.request().method()} ${new URL(r.url()).pathname}`); });
  return { account, label: who, context, page, key: account.api_key };
}
let rpc = 0;
async function mcpRaw(key, method, params) {
  const r = await fetch(SITE + '/mcp', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-API-Key': key }, body: JSON.stringify({ jsonrpc: '2.0', id: ++rpc, method, params }) });
  let b = null; try { b = await r.json(); } catch (e) {}
  return { http: r.status, body: b };
}
// Returns { ok, err, data } where data is the REST response for pass-through tools.
async function mcp(key, name, args) {
  const { http, body } = await mcpRaw(key, 'tools/call', { name, arguments: args });
  const res = body && body.result;
  const text = res && res.content && res.content[0] && res.content[0].text;
  if (!res || res.isError) return { ok: false, err: (body && body.error && body.error.message) || text || ('HTTP ' + http), data: null, text };
  let data = res.structuredContent;
  if (data && Object.prototype.hasOwnProperty.call(data, 'response') && Object.prototype.hasOwnProperty.call(data, 'status')) data = data.response;
  if (data == null) { try { data = JSON.parse(text); } catch (e) { data = text; } }
  return { ok: true, data, text };
}
async function must(t, key, name, args) {
  const r = await mcp(key, name, args);
  if (!r.ok) t.fail(`MCP ${name} failed: ${String(r.err).slice(0, 300)}`);
  return r;
}
const val = (page, sel) => page.locator(sel).first().inputValue({ timeout: 5000 }).catch(() => null);
const isChecked = (page, sel) => page.locator(sel).first().isChecked({ timeout: 5000 }).catch(() => null);
const txt = (page, sel) => page.locator(sel).first().innerText({ timeout: 5000 }).catch(() => '');
function eq(t, label, got, want) {
  const g = typeof got === 'string' ? got.trim() : got;
  if (g !== want) t.fail(`${label}: website shows ${JSON.stringify(got)}, expected ${JSON.stringify(want)}`);
}
async function waitVal(page, sel, pred, ms = 8000) {
  const end = Date.now() + ms; let v = null;
  while (Date.now() < end) { v = await val(page, sel); if (pred(v)) return v; await page.waitForTimeout(250); }
  return v;
}
async function liveText(page) {
  return (await page.locator('[role=status]:visible, .ok:visible, .live:visible, [aria-live]:visible').allInnerTexts().catch(() => [])).map(s => s.trim()).filter(Boolean).join(' / ');
}
async function dlg(page) {
  const box = page.locator('dialog[open] .sh-dlg-box, .sh-dlg-box:visible').first();
  await box.waitFor({ state: 'visible', timeout: 6000 });
  return { title: await txt(page, '#sh-dlg-title'), body: await txt(page, '#sh-dlg-body') };
}
async function shot(page, name) {
  const p = `${OUT}/account-parity-${name}.png`;
  await page.screenshot({ path: p, fullPage: true }).catch(() => {});
  shots.push(p); return p;
}
async function signInShown(page) {
  await page.waitForTimeout(800);
  return page.locator('#view-signin').isVisible().catch(() => false);
}
async function keyAlive(key) { return (await api(key, 'GET', '/v1/me')).status === 200; }
async function manage(page, slug, name) {
  await goto(page, `/e/${slug}/manage`);
  await page.locator('#view-manage').waitFor({ state: 'visible', timeout: 15000 });
  if (name) await tab(page, name);
  await page.waitForLoadState('networkidle', { timeout: 8000 }).catch(() => {});
}

// ================================================================ SIM-228
async function accountPart(chromium, webkit) {
  const sim = 'SIM-228';
  const P = S6.participant;
  const p1 = P[0];

  // 1. Account page for a key-only account (no email)
  const a = await run.person(chromium, p1);
  await run.step(sim, 'account page /account loads for an account with no email', a.page, async t => {
    await goto(a.page, '/account');
    try { await a.page.locator('#acct-body').waitFor({ state: 'visible', timeout: 10000 }); }
    catch { t.fail('account body not shown; message: ' + (await liveText(a.page) || await visibleError(a.page))); return; }
    const shown = (await txt(a.page, '#acct-email-row')).replace(/\s+/g, ' ');
    t.note('Email section reads: ' + shown);
    if (shown.includes(p1.username) && !p1.username.includes('@')) t.fail(`Email section says "${shown.slice(0, 120)}" but this account has no email address: it shows the username as if it were the sign-in email`);
    await shot(a.page, 'account-desktop');
  });

  await run.step(sim, 'set a display name on /account (persists after reload)', a.page, async t => {
    const name = 'Parity Tester ' + stamp;
    await a.page.locator('#acct-name').fill(name);
    await a.page.locator('#acct-name-save').click();
    await a.page.waitForTimeout(1500);
    const err = await txt(a.page, '#acct-name-err');
    if (err.trim()) t.fail('name error: ' + err);
    await goto(a.page, '/account');
    await a.page.locator('#acct-body').waitFor({ state: 'visible', timeout: 10000 });
    eq(t, 'name after reload', await val(a.page, '#acct-name'), name);
    const me = await api(p1.api_key, 'GET', '/v1/me');
    if (me.data && me.data.display_name !== name) t.fail('GET /v1/me display_name = ' + JSON.stringify(me.data.display_name));
  });

  await run.step(sim, 'Email section: "Change email" for an account with no email', a.page, async t => {
    await a.page.locator('#acct-email-open').click();
    await a.page.locator('#acct-email-input').fill(`parity-${stamp}@example.com`);
    await a.page.locator('#acct-email-send').click();
    await a.page.waitForTimeout(2500);
    const codeForm = await a.page.locator('#acct-code-form').isVisible();
    if (codeForm) {
      const cur = await txt(a.page, '#acct-current-to');
      t.note('code form shown; second code "sent to": ' + cur);
      if (!cur.includes('@')) t.fail(`after Send code the page asks for a "Code sent to ${cur}", an address that does not exist (the account has no email), so the change can never be completed`);
      await a.page.locator('#acct-code-cancel').click();
    } else {
      const msg = await visibleError(a.page) || await liveText(a.page);
      t.note('no code form; message: ' + msg);
      if (!msg) t.fail('Send code did nothing visible');
      else t.fail('Change email refused for an account with no email: "' + msg + '" (section never says the account has no email or how to add one)');
      await a.page.locator('#acct-email-cancel').click().catch(() => {});
    }
    await shot(a.page, 'account-email');
  });

  await run.step(sim, 'Google sign-in section for an account with no email', a.page, async t => {
    const ids = (await txt(a.page, '#acct-ids')).trim();
    t.note('linked sign-ins: ' + ids);
    const link = a.page.locator('#acct-google');
    if (await link.isVisible()) {
      await link.click();
      const d = await dlg(a.page);
      t.note('Link Google dialog: ' + d.title + ' | ' + d.body);
      if (d.body.includes(p1.username)) t.fail(`Link Google dialog says "${d.body}": it names the username as an email; with no email on the account a Google sign-in can never match it, and the page does not say so`);
      await a.page.locator('[data-sh-dlg="cancel"]:visible').click();
    } else t.note('Link Google button hidden');
  });

  await run.step(sim, 'Sign-in alerts toggle off and on', a.page, async t => {
    const box = a.page.locator('#acct-alerts');
    const before = await box.isChecked();
    await box.click(); await a.page.waitForTimeout(1500);
    const m1 = await liveText(a.page); const e1 = await visibleError(a.page);
    await box.click(); await a.page.waitForTimeout(1500);
    const m2 = await liveText(a.page);
    t.note(`before=${before} after1="${m1}" after2="${m2}"`);
    if (e1) t.fail('toggle error: ' + e1);
    const me = await api(p1.api_key, 'GET', '/v1/me');
    if (me.data && me.data.signin_alerts !== before) t.fail('signin_alerts did not return to ' + before);
    t.note('copy says "Email me after each new sign-in" although the account has no email; no hint that alerts cannot be delivered');
  });

  await run.step(sim, 'Delete account panel opens, needs the exact word, cancels', a.page, async t => {
    await a.page.locator('#acct-delete-open').click();
    const word = (await txt(a.page, '#acct-delete-word')).trim();
    t.note('confirm word shown: ' + word);
    if (!word) t.fail('no confirm word shown');
    if (!(await a.page.locator('#acct-delete-go').isDisabled())) t.fail('delete button enabled before typing');
    await a.page.locator('#acct-delete-input').fill(word + 'x');
    if (!(await a.page.locator('#acct-delete-go').isDisabled())) t.fail('delete button enabled with the wrong word');
    await a.page.locator('#acct-delete-cancel').click();
    if (!(await a.page.locator('#acct-delete').isHidden())) t.fail('cancel did not close the panel');
  });

  // Phone: account page at 390 px, Chromium and WebKit
  for (const [kind, b] of [['chromium', chromium], ['webkit', webkit]]) {
    const ph = await run.person(b, p1, { width: 390, label: p1.username + '@' + kind + '390' });
    await run.step(sim, `phone 390px ${kind}: account page readable, nothing off-screen`, ph.page, async t => {
      await goto(ph.page, '/account');
      await ph.page.locator('#acct-body').waitFor({ state: 'visible', timeout: 12000 });
      const m = await ph.page.evaluate(() => {
        const vw = document.documentElement.clientWidth;
        const off = [];
        document.querySelectorAll('#view-account button:not([hidden]), #view-account input, #view-account h2, #view-account .acct-row span').forEach(e => {
          const r = e.getBoundingClientRect(); if (r.width && (r.right > vw + 1 || r.left < -1)) off.push((e.id || e.tagName) + ' ' + Math.round(r.left) + '..' + Math.round(r.right));
        });
        return { sw: document.documentElement.scrollWidth, vw, off };
      });
      if (m.sw > m.vw + 1) t.fail(`page scrolls sideways: scrollWidth ${m.sw} > ${m.vw}`);
      if (m.off.length) t.fail('elements off-screen: ' + m.off.join(', '));
      t.note('screenshot ' + await shot(ph.page, 'account-390-' + kind));
    });
    await ph.context.close();
  }
  await a.context.close();

  // Sign out: a separate participant (signing out ends that key on the server)
  let outAcct = null;
  for (let i = 37; i >= 30 && !outAcct; i--) if (await keyAlive(P[i].api_key)) outAcct = P[i];
  if (!outAcct) await run.step(sim, 'Sign out (no live set-6 participant left for it)', null, async t => t.fail('no live account'));
  else {
    const s = await personOnce(chromium, outAcct);
    await run.step(sim, `Sign out from the header (${outAcct.username}) returns to a sensible page`, s.page, async t => {
      await goto(s.page, '/events');
      if (!(await s.page.locator('#view-events').isVisible())) t.fail('not signed in to start with');
      await s.page.locator('button.sh-signout:visible, [data-sh-signout]:visible').first().click();
      await s.page.waitForURL(u => u.pathname === '/' || u.pathname === '/signin', { timeout: 10000 }).catch(() => {});
      await s.page.waitForLoadState('domcontentloaded');
      const u = new URL(s.page.url());
      t.note('after sign out: ' + u.pathname + ' title=' + await s.page.title());
      if (!['/', '/signin'].includes(u.pathname)) t.fail('landed on ' + u.pathname);
      const k = await s.page.evaluate(() => localStorage.getItem('apiKey'));
      if (k) t.fail('key still in browser storage after sign out');
      const hdr = await txt(s.page, '#dash-link');
      if (hdr.trim() !== 'Sign in') t.fail('header still says "' + hdr + '" after sign out');
    });
    await run.step(sim, 'after sign out, /events shows sign-in and the key is dead on the server', s.page, async t => {
      await goto(s.page, '/events');
      if (!(await signInShown(s.page))) t.fail('signed-in page did not show sign-in after sign out');
      const st = (await api(outAcct.api_key, 'GET', '/v1/me')).status;
      t.note('GET /v1/me with the old key: ' + st + ' (sign out deletes the key; a key-only account has no way back in)');
      if (st !== 401) t.fail('old key still works after sign out: ' + st);
    });
    await s.context.close();
  }

  // Sign out everywhere: participant 39 in two browsers
  const ev = P[38];
  if (!(await keyAlive(ev.api_key))) await run.step(sim, 'Sign out everywhere (participant 39 key already dead)', null, async t => t.fail('key dead before test; rerun with a fresh key'));
  else {
    const A1 = await personOnce(chromium, ev, { label: ev.username + '#A' });
    const B1 = await personOnce(chromium, ev, { label: ev.username + '#B' });
    let newKey = null;
    await run.step(sim, 'Sign out everywhere in browser A: confirm, new key shown', A1.page, async t => {
      await goto(B1.page, '/events');
      if (!(await B1.page.locator('#view-events').isVisible())) t.fail('browser B not signed in to start with');
      await goto(A1.page, '/account');
      await A1.page.locator('#acct-body').waitFor({ state: 'visible', timeout: 10000 });
      await A1.page.locator('#acct-everywhere').click();
      const d = await dlg(A1.page);
      t.note('confirm: ' + d.title + ' | ' + d.body);
      await A1.page.locator('[data-sh-dlg="ok"]:visible').click();
      const d2 = await dlg(A1.page).catch(() => null);
      if (!d2) { t.fail('no confirmation after sign out everywhere; message: ' + (await visibleError(A1.page) || await liveText(A1.page))); return; }
      t.note('then: ' + d2.title + ' | ' + d2.body);
      newKey = await A1.page.evaluate(() => localStorage.getItem('apiKey'));
      await A1.page.locator('[data-sh-dlg="ok"]:visible').click();
      if (!newKey || newKey === ev.api_key) t.fail('browser A did not get a new key');
      else {
        // keep set 6 usable: store the rotated key back in the set file
        const file = `${ACCOUNTS_DIR}/set-06.json`;
        const j = JSON.parse(fs.readFileSync(file, 'utf8'));
        for (const x of j.accounts) if (x.username === ev.username) x.api_key = newKey;
        fs.writeFileSync(file, JSON.stringify(j, null, 2));
      }
      await goto(A1.page, '/events');
      if (!(await A1.page.locator('#view-events').isVisible())) t.fail('browser A not signed in after sign out everywhere');
    });
    await run.step(sim, 'Sign out everywhere: browser B is signed out, old key revoked, new key works', B1.page, async t => {
      await goto(B1.page, '/events');
      if (!(await signInShown(B1.page))) t.fail('browser B still signed in after sign out everywhere');
      const old = (await api(ev.api_key, 'GET', '/v1/me')).status;
      const neu = newKey ? (await api(newKey, 'GET', '/v1/me')).status : 'n/a';
      t.note(`GET /v1/me old key ${old}, new key ${neu}`);
      if (old !== 401) t.fail('old key still works: ' + old);
      if (neu !== 200) t.fail('new key does not work: ' + neu);
    });
    await A1.context.close(); await B1.context.close();
  }

  // Signed-in pages with no key
  const v = await run.person(chromium, null, { label: 'visitor' });
  for (const path of ['/events', '/account', '/events/new', '/e/e2e-parity-nokey/manage', '/e/e2e-parity-nokey/team']) {
    await run.step(sim, `visitor with no key on ${path} sees sign-in`, v.page, async t => {
      await goto(v.page, path);
      if (!(await signInShown(v.page))) t.fail('no sign-in shown; page says: ' + (await txt(v.page, 'main, body')).slice(0, 200));
    });
  }
  await v.context.close();

  // Delete account: set 10's last participant, the only account this may delete
  const victim = S10.participant[S10.participant.length - 1];
  if (victim.username !== 'e2e-s10-participant-100') throw new Error('unexpected set 10 last participant ' + victim.username);
  if (!(await keyAlive(victim.api_key))) await run.step(sim, 'delete account (set 10 participant-100 already deleted on an earlier run)', null, async t => t.note('skipped: key already dead'));
  else {
    const d = await personOnce(chromium, victim);
    await run.step(sim, 'delete account (set 10 last participant): confirm, then key stops working', d.page, async t => {
      await goto(d.page, '/account');
      await d.page.locator('#acct-body').waitFor({ state: 'visible', timeout: 10000 });
      await d.page.locator('#acct-delete-open').click();
      const word = (await txt(d.page, '#acct-delete-word')).trim();
      await d.page.locator('#acct-delete-input').fill(word);
      await d.page.locator('#acct-delete-go').click();
      const box = await dlg(d.page).catch(() => null);
      if (!box) { t.fail('no confirmation; message: ' + (await visibleError(d.page) || await liveText(d.page))); return; }
      t.note('after delete: ' + box.title + ' | ' + box.body);
      await shot(d.page, 'delete-confirmation');
      if (/email/i.test(box.body)) t.fail(`confirmation says "${box.body}" but the account has no email address`);
      await d.page.locator('[data-sh-dlg="ok"]:visible').click();
      await d.page.waitForURL(u => u.pathname === '/', { timeout: 10000 }).catch(() => {});
      t.note('landed on ' + new URL(d.page.url()).pathname);
      const st = (await api(victim.api_key, 'GET', '/v1/me')).status;
      if (st !== 401) t.fail('deleted account key still answers ' + st);
      if (await d.page.evaluate(() => localStorage.getItem('apiKey'))) t.fail('key still in the browser');
    });
    await d.context.close();
  }
}

// ================================================================ SIM-246
async function mcpPart(chromium) {
  const sim = 'SIM-246';
  // The organiser account hit the 3-events-a-day limit during script development; ORG_ALT=1 picks
  // another set-6 account to create the event (it is that event's organiser either way).
  const org = process.env.ORG_ALT ? S6['co-organiser'][1] : S6.organiser[0], co = S6['co-organiser'][0];
  creatorKey = org.api_key;
  const [p2, p3, p4] = S6.participant.slice(1, 4);
  const j1 = S6.judge[0];
  const slug = 'e2e-parity-' + stamp;
  const title = 'Parity ' + stamp;
  const TRACK2 = 'Track 2: Good Health and Wellbeing (SDG 3)';
  let ev = null, teamMcp = null;

  await run.step(sim, 'MCP tools/list as organiser and as participant', null, async t => {
    const a = await mcpRaw(org.api_key, 'tools/list', {});
    const b = await mcpRaw(p2.api_key, 'tools/list', {});
    const na = (a.body && a.body.result && a.body.result.tools || []).map(x => x.name);
    const nb = (b.body && b.body.result && b.body.result.tools || []).map(x => x.name);
    t.note(`organiser sees ${na.length} tools, participant sees ${nb.length}`);
    fs.writeFileSync(`${OUT}/account-parity-mcp-tools.json`, JSON.stringify(na, null, 1));
    if (!na.length) t.fail('tools/list empty: ' + JSON.stringify(a.body).slice(0, 200));
  });

  const o = await run.person(chromium, org);
  try {
    await run.step(sim, 'MCP hack_create_event -> website manage shows the same event', o.page, async t => {
      const chk = await must(t, org.api_key, 'hack_check_event_name', { slug });
      if (chk.ok) t.note('check name: ' + String(chk.text).slice(0, 120));
      const r = await must(t, org.api_key, 'hack_create_event', { slug, title, organiser_name: 'Parity Org', organisation: 'Simple Hack e2e', contact_email: 'e2e@example.com', purpose: 'Automated parity test. Safe to delete.', starts_at: day(7), ends_at: day(9), expected_participants: 40, time_zone: 'Europe/London' });
      if (!r.ok) throw new Error('create failed');
      created.push(slug); ev = r.data;
      await manage(o.page, slug, 'page');
      eq(t, 'title', await waitVal(o.page, '#pg-title', v => !!v), title);
      eq(t, 'start date', await val(o.page, '#pg-start'), day(7));
      eq(t, 'end date', await val(o.page, '#pg-end'), day(9));
      eq(t, 'time zone', await val(o.page, '#pg-tz'), 'Europe/London');
      await tab(o.page, 'settings');
      eq(t, 'organiser name', await waitVal(o.page, '#set-name', v => !!v), 'Parity Org');
      eq(t, 'organisation', await val(o.page, '#set-org'), 'Simple Hack e2e');
      eq(t, 'contact email', await val(o.page, '#set-email'), 'e2e@example.com');
      eq(t, 'expected', await val(o.page, '#set-n'), '40');
    });

    await run.step(sim, 'MCP hack_update_event (page text, team size, deadline, entry needs, gallery) -> website', o.page, async t => {
      const deadline = day(8) + 'T17:30';
      const r = await must(t, org.api_key, 'hack_update_event', { slug, tagline: 'Tag via MCP', about: 'About via MCP', rules: 'Rules via MCP', prizes: 'Prizes via MCP', coc_text: 'Be kind (MCP).', team_size_max: 3, submission_deadline: deadline, entry_required: ['title', 'description', 'code_url'], gallery_open: true });
      if (r.ok) {
        const keys = Object.keys(r.data || {});
        for (const k of ['submission_deadline', 'entry_required', 'gallery_open', 'accent_color', 'tracks']) if (!keys.includes(k)) t.note(`hack_update_event/get_event answer has no "${k}"`);
      }
      await manage(o.page, slug, 'page');
      eq(t, 'tagline', await waitVal(o.page, '#pg-tagline', v => !!v), 'Tag via MCP');
      eq(t, 'about', await val(o.page, '#pg-about'), 'About via MCP');
      eq(t, 'rules', await val(o.page, '#pg-rules'), 'Rules via MCP');
      eq(t, 'prizes', await val(o.page, '#pg-prizes'), 'Prizes via MCP');
      eq(t, 'code of conduct', await val(o.page, '#pg-coc'), 'Be kind (MCP).');
      await tab(o.page, 'teams');
      eq(t, 'team size', await waitVal(o.page, '#tmax', v => !!v), '3');
      await tab(o.page, 'settings');
      eq(t, 'deadline', await waitVal(o.page, '#set-deadline', v => !!v), deadline);
      for (const [f, want] of [['title', true], ['tagline', false], ['description', true], ['video_url', false], ['code_url', true], ['screenshot', false]]) eq(t, 'entry needs ' + f, await isChecked(o.page, '#er-' + f), want);
      eq(t, 'gallery', await isChecked(o.page, '#set-gallery'), true);
    });

    await run.step(sim, `MCP hack_set_tracks incl. "${TRACK2}" -> website Teams tab`, o.page, async t => {
      const noSlug = await mcp(org.api_key, 'hack_set_tracks', { slug, body: { tracks: [{ name: TRACK2 }] } });
      if (!noSlug.ok) t.note('tracks with name only (as the website sends a name): ' + String(noSlug.err).split('\n')[0]);
      const r = await must(t, org.api_key, 'hack_set_tracks', { slug, body: { tracks: [{ slug: 'track-1-open', name: 'Track 1: Open innovation', challenge: 'Anything goes', prize: 'Stickers' }, { slug: 'sdg-3-health', name: TRACK2, challenge: 'Build for wellbeing', prize: '£500' }] } });
      if (!r.ok) return;
      await manage(o.page, slug, 'teams');
      await o.page.locator('#tracks-editor input').first().waitFor({ timeout: 8000 }).catch(() => {});
      const names = await o.page.locator('#tracks-editor input').evaluateAll(es => es.map(e => e.value));
      if (!names.includes(TRACK2)) t.fail('website tracks: ' + JSON.stringify(names));
      const hints = await o.page.locator('#tracks-editor .hint').allInnerTexts();
      if (!hints.some(h => h.includes('sdg-3-health'))) t.fail('track slug not shown: ' + hints.join(','));
      const areas = await o.page.locator('#tracks-editor textarea').evaluateAll(es => es.map(e => e.value));
      if (!areas.includes('Build for wellbeing') || !areas.includes('£500')) t.fail('challenge/prize not shown: ' + JSON.stringify(areas));
    });

    let crit = null;
    await run.step(sim, 'MCP hack_set_rubric -> website Judging > Rubric', o.page, async t => {
      const r = await must(t, org.api_key, 'hack_set_rubric', { slug, criteria: [{ name: 'Impact on health', description: 'Does it help?', weight: 40, max_points: 10 }, { name: 'Build quality', weight: 30, max_points: 5 }, { name: 'Pitch', weight: 30, max_points: 5 }] });
      if (!r.ok) return;
      crit = (r.data && r.data.criteria) || null;
      await manage(o.page, slug, 'judging');
      const det = o.page.locator('#jg-setup');
      if (!(await det.evaluate(e => e.open))) await o.page.locator('#jg-setup > summary').click();
      await o.page.locator('#jg-rubric input[aria-label="Criterion name"]').first().waitFor({ timeout: 8000 }).catch(() => {});
      const names = await o.page.locator('#jg-rubric input[aria-label="Criterion name"]').evaluateAll(es => es.map(e => e.value));
      const weights = await o.page.locator('#jg-rubric input[aria-label="Weight"]').evaluateAll(es => es.map(e => e.value));
      const pts = await o.page.locator('#jg-rubric input[aria-label="Points scale"]').evaluateAll(es => es.map(e => e.value));
      eq(t, 'criteria', JSON.stringify(names), JSON.stringify(['Impact on health', 'Build quality', 'Pitch']));
      eq(t, 'weights', JSON.stringify(weights), JSON.stringify(['40', '30', '30']));
      eq(t, 'points', JSON.stringify(pts), JSON.stringify(['10', '5', '5']));
    });

    await run.step(sim, 'MCP hack_set_registration (questions, approval) -> website People > Sign-up', o.page, async t => {
      const r = await must(t, org.api_key, 'hack_set_registration', { slug, body: { questions: [{ id: 'q1', prompt: 'What do you want to build?', required: true }, { id: 'q2', prompt: 'Dietary needs', required: false }], approval_required: true } });
      if (!r.ok) return;
      await manage(o.page, slug, 'people');
      eq(t, 'questions', await waitVal(o.page, '#signup-questions', v => !!v), '* What do you want to build?\nDietary needs');
      eq(t, 'approval', await isChecked(o.page, '#signup-approval'), true);
    });

    await run.step(sim, 'MCP hack_set_event_stage open + hack_get_event links -> website Overview', o.page, async t => {
      await must(t, org.api_key, 'hack_set_event_stage', { slug, stage: 'open' });
      const g = await must(t, org.api_key, 'hack_get_event', { slug });
      if (!g.ok) return; ev = g.data;
      const og = ev.organiser || {};
      await manage(o.page, slug, 'overview');
      eq(t, 'stage', await waitVal(o.page, '#ov-stage', v => v === 'open'), 'open');
      eq(t, 'join link', await txt(o.page, '#ov-join-url'), og.join_url);
      eq(t, 'judge link', await txt(o.page, '#ov-judge-url'), og.judge_url);
      if (!og.join_url || !og.judge_url) t.fail('hack_get_event gave no join/judge links');
    });

    const annTitle = 'MCP announcement ' + stamp;
    await run.step(sim, 'MCP hack_post_announcement (no email) -> website Content + public page', o.page, async t => {
      const r = await must(t, org.api_key, 'hack_post_announcement', { slug, body: { title: annTitle, body: 'Posted through MCP, no email.', email_participants: false } });
      if (!r.ok) return;
      await manage(o.page, slug, 'content');
      await o.page.waitForTimeout(800);
      if (!(await txt(o.page, '#content-announcements')).includes(annTitle)) t.fail('announcement not in Content tab');
      const pub = await fetch(`https://${slug}.simple-hack.app/`).then(r => r.text()).catch(() => '');
      if (!pub.includes(annTitle)) t.fail('announcement not on the public event page');
    });

    await run.step(sim, 'MCP hack_set_judging_settings -> website Judging setup', o.page, async t => {
      const r = await must(t, org.api_key, 'hack_set_judging_settings', { slug, body: { assignment_mode: 'automatic', judges_per_team: 1, score_mode: 'normalised', public_scores: false, tie_criterion_id: crit && crit[0] && crit[0].id } });
      if (!r.ok) return;
      await manage(o.page, slug, 'judging');
      if (!(await o.page.locator('#jg-setup').evaluate(e => e.open))) await o.page.locator('#jg-setup > summary').click();
      await o.page.waitForTimeout(800);
      eq(t, 'automatic', await isChecked(o.page, '#jg-mode-automatic'), true);
      eq(t, 'judges per team', await val(o.page, '#jg-per'), '1');
      eq(t, 'score mode', await val(o.page, '#jg-score-mode'), 'normalised');
      eq(t, 'public scores', await isChecked(o.page, '#jg-public-scores'), false);
      eq(t, 'tie criterion', await val(o.page, '#jg-tie-criterion'), crit && crit[0] && crit[0].id);
    });

    await run.step(sim, 'MCP hack_set_content (sponsor, FAQ, schedule) -> website Content', o.page, async t => {
      const r = await must(t, org.api_key, 'hack_set_content', { slug, body: { sponsors: [{ name: 'Acme Health', tier: 'Gold', url: 'https://example.com' }], faq: [{ question: 'Is it free?', answer: 'Yes.' }], schedule: [{ title: 'Kick-off', description: 'Welcome', start_at: day(7) + 'T09:00', end_at: day(7) + 'T10:00' }] } });
      if (!r.ok) return;
      await manage(o.page, slug, 'content');
      await o.page.waitForTimeout(800);
      const vals = await o.page.locator('#p-content input, #p-content textarea').evaluateAll(es => es.map(e => e.value));
      for (const w of ['Acme Health', 'Is it free?', 'Kick-off']) if (!vals.includes(w)) t.fail(`"${w}" not in Content tab fields`);
    });

    await run.step(sim, 'MCP co-organiser invite + accept -> website People lists co-organiser', o.page, async t => {
      const inv = await must(t, org.api_key, 'hack_create_organiser_invite', { slug });
      if (!inv.ok) return;
      const url = inv.data.url || ''; const code = url.split('/').pop();
      const pv = await must(t, co.api_key, 'hack_preview_organiser_invite', { code });
      const ac = await must(t, co.api_key, 'hack_accept_organiser_invite', { code, body: { accept_coc: true, display_name: 'Parity Co-org' } });
      if (!ac.ok) return;
      await manage(o.page, slug, 'people');
      await o.page.waitForTimeout(800);
      if (!(await txt(o.page, '#people-list')).includes('Parity Co-org')) t.fail('co-organiser not in People list');
      const c = await run.person(chromium, co);
      await manage(c.page, slug, 'overview');
      if (!(await c.page.locator('#view-manage').isVisible())) t.fail('co-organiser cannot open manage');
      await c.context.close();
    });

    // ---- participant journey through MCP
    const code = (ev && ev.organiser && ev.organiser.join_code) || '';
    const pa = await run.person(chromium, p2);
    await run.step(sim, 'MCP participant: preview + join with answers -> website shows pending approval', pa.page, async t => {
      const pv = await must(t, p2.api_key, 'hack_preview_join', { code });
      if (pv.ok) { const qs = JSON.stringify(pv.data).includes('What do you want to build?'); if (!qs) t.fail('preview has no signup questions'); }
      const j = await must(t, p2.api_key, 'hack_join_event', { code, accept_coc: true, display_name: 'Parity P2', answers: { q1: 'A water reminder app', q2: 'None' } });
      if (!j.ok) return;
      const st = await must(t, p2.api_key, 'hack_application_status', { slug });
      t.note('application status: ' + JSON.stringify(st.data).slice(0, 160));
      await goto(pa.page, `/e/${slug}/team`);
      await pa.page.waitForTimeout(1000);
      const line = await txt(pa.page, '#ev-approval-line');
      t.note('website registration line: ' + line);
      if (!(await pa.page.locator('#ev-approval').isVisible())) t.fail('website shows no registration/approval state');
    });

    await run.step(sim, 'MCP organiser reads application, approves -> website People and participant view agree', o.page, async t => {
      const apps = await must(t, org.api_key, 'hack_get_applications', { slug });
      const mine = (apps.data || []).find(x => x.display_name === 'Parity P2');
      if (!mine) { t.fail('application not in hack_get_applications'); return; }
      await manage(o.page, slug, 'people');
      await o.page.waitForTimeout(800);
      const card = await txt(o.page, '#applications-list');
      t.note('website application card: ' + card.replace(/\s+/g, ' ').slice(0, 200));
      if (!card.includes('A water reminder app')) t.fail('answers not shown on website application');
      if (/undefined/.test(card)) t.fail(`application card shows "undefined": "${card.replace(/\s+/g, ' ').slice(0, 120)}" (the account has no email)`);
      const d = await must(t, org.api_key, 'hack_decide_application', { slug, user_id: mine.user_id, body: { decision: 'approved' } });
      if (!d.ok) return;
      await manage(o.page, slug, 'people');
      await o.page.waitForTimeout(800);
      if ((await txt(o.page, '#applications-list')).includes('Parity P2')) t.fail('still pending on website after MCP approval');
      await goto(pa.page, `/e/${slug}/team`);
      await pa.page.waitForTimeout(800);
      if (!(await pa.page.locator('#ev-noteam').isVisible())) t.fail('participant not offered team start after approval: ' + await txt(pa.page, '#ev-approval-line'));
    });

    await run.step(sim, 'MCP participant: create team, choose track, fill entry -> website (participant and organiser)', pa.page, async t => {
      const c = await must(t, p2.api_key, 'hack_create_team', { slug, name: 'Parity Team MCP' });
      if (!c.ok) return;
      const tr = await must(t, p2.api_key, 'hack_choose_track', { slug, track: 'sdg-3-health' });
      const en = await must(t, p2.api_key, 'hack_update_entry', { slug, title: 'Hydrate', tagline: 'Drink water', description: 'Reminds you to drink water.', code_url: 'https://example.com/code', video_url: 'https://example.com/video' });
      const ts = await must(t, p2.api_key, 'hack_get_team_status', { slug });
      teamMcp = ts.data; t.note('team status: ' + JSON.stringify(ts.data).slice(0, 200));
      await goto(pa.page, `/e/${slug}/team`);
      await pa.page.locator('#ev-team').waitFor({ state: 'visible', timeout: 10000 }).catch(() => {});
      eq(t, 'team name', await txt(pa.page, '#team-name'), 'Parity Team MCP');
      eq(t, 'track', await val(pa.page, '#team-track'), 'sdg-3-health');
      eq(t, 'entry title', await waitVal(pa.page, '#en-title', v => !!v), 'Hydrate');
      eq(t, 'entry code link', await val(pa.page, '#en-code'), 'https://example.com/code');
      await manage(o.page, slug, 'teams');
      await o.page.waitForTimeout(800);
      const tl = await txt(o.page, '#teams-list');
      if (!tl.includes('Parity Team MCP')) t.fail('organiser Teams tab lacks the team');
      const mt = await must(t, org.api_key, 'hack_get_teams', { slug });
      const mcpHasTrack = JSON.stringify(mt.data || '').includes('sdg-3-health');
      t.note('MCP hack_get_teams includes the track: ' + mcpHasTrack);
      await o.page.locator('#teams-list button', { hasText: 'Manage team' }).first().click().catch(() => {});
      await o.page.waitForTimeout(500);
      const tl2 = await txt(o.page, '#teams-list');
      if (!tl2.includes(TRACK2) && !tl2.includes('sdg-3-health')) t.fail('organiser Teams tab never shows which track the team chose (MCP hack_get_teams ' + (mcpHasTrack ? 'does' : 'does not') + '): "' + tl2.replace(/\s+/g, ' ').slice(0, 200) + '"');
      await tab(o.page, 'projects');
      await o.page.waitForTimeout(800);
      if (!(await txt(o.page, '#projects-list')).includes('Hydrate')) t.fail('organiser Entries tab lacks the entry');
    });

    await run.step(sim, 'MCP second participant joins the team by code -> website team members', pa.page, async t => {
      const j = await must(t, p3.api_key, 'hack_join_event', { code, accept_coc: true, display_name: 'Parity P3', answers: { q1: 'Same team' } });
      const apps = await mcp(org.api_key, 'hack_get_applications', { slug });
      const a3 = (apps.data || []).find(x => x.display_name === 'Parity P3');
      if (a3) await must(t, org.api_key, 'hack_decide_application', { slug, user_id: a3.user_id, body: { decision: 'approved' } });
      const tcode = teamMcp && teamMcp.me && teamMcp.me.team && teamMcp.me.team.code;
      if (!tcode) { t.fail('hack_get_team_status gives no team code to share: ' + JSON.stringify(teamMcp).slice(0, 200)); return; }
      const jt = await must(t, p3.api_key, 'hack_join_team', { slug, code: tcode });
      await goto(pa.page, `/e/${slug}/team`);
      await pa.page.waitForTimeout(800);
      if (!(await txt(pa.page, '#team-members')).includes('Parity P3')) t.fail('website team members lack P3');
      const webCode = (await txt(pa.page, '#team-code')).trim();
      if (webCode && !webCode.includes(tcode)) t.fail(`team code differs: website "${webCode}" vs MCP "${tcode}"`);
    });
    await pa.context.close();

    await run.step(sim, 'MCP judge: preview + join -> website People shows judge, judge view opens', o.page, async t => {
      const jcode = ev && ev.organiser && ev.organiser.judge_code;
      await must(t, j1.api_key, 'hack_preview_judge', { code: jcode });
      const r = await must(t, j1.api_key, 'hack_join_judge', { code: jcode, accept_coc: true, display_name: 'Parity Judge' });
      if (!r.ok) return;
      const q = await must(t, j1.api_key, 'hack_get_judge_queue', { slug });
      t.note('judge queue: ' + JSON.stringify(q.data).slice(0, 160));
      await manage(o.page, slug, 'people');
      await o.page.waitForTimeout(800);
      if (!(await txt(o.page, '#people-list')).includes('Parity Judge')) t.fail('judge not in People list');
      const jb = await run.person(chromium, j1);
      await goto(jb.page, `/e/${slug}/judge`);
      await jb.page.waitForTimeout(1000);
      if (!(await jb.page.locator('#ev-judge').isVisible())) t.fail('judge view not shown on website');
      await jb.context.close();
    });

    // ---- reverse: website first, MCP reads back
    const webAnn = 'Web announcement ' + stamp;
    await run.step(sim, 'website: post announcement (no email) -> MCP hack_get_announcements', o.page, async t => {
      await manage(o.page, slug, 'content');
      await o.page.locator('#content-announce-title').fill(webAnn);
      await o.page.locator('#content-announce-body').fill('Posted on the website.');
      if (await o.page.locator('#content-announce-email').isChecked()) await o.page.locator('#content-announce-email').uncheck();
      await o.page.locator('#content-announce-send').click();
      await o.page.waitForTimeout(1500);
      const e = await visibleError(o.page); if (e) t.fail('website error: ' + e);
      const r = await must(t, org.api_key, 'hack_get_announcements', { slug });
      if (r.ok && !JSON.stringify(r.data).includes(webAnn)) t.fail('MCP does not list the website announcement');
    });

    const webTrack = 'Track 5: Climate Action (SDG 13)';
    await run.step(sim, 'website: add a track on Teams tab -> MCP hack_get_tracks', o.page, async t => {
      await manage(o.page, slug, 'teams');
      await o.page.locator('#track-new-name').fill(webTrack);
      await o.page.locator('#track-add').click();
      await o.page.locator('#tracks-save').click();
      await o.page.waitForTimeout(1500);
      const e = await visibleError(o.page); if (e) t.fail('website error: ' + e + (e.includes('track slugs') ? ' (SIM-214)' : ''));
      const r = await must(t, org.api_key, 'hack_get_tracks', { slug });
      const tr = (r.data || []).find(x => x.name === webTrack);
      if (!tr) t.fail('MCP does not list the website track: ' + JSON.stringify(r.data).slice(0, 200));
      else t.note('slug made by the website: ' + tr.slug);
      if (r.ok && !(r.data || []).some(x => x.name === TRACK2)) t.fail('MCP lost the SDG 3 track after the website save');
    });

    await run.step(sim, 'website: change tagline and event colour -> MCP hack_get_event', o.page, async t => {
      await manage(o.page, slug, 'page');
      await waitVal(o.page, '#pg-title', v => !!v);
      await o.page.locator('#pg-tagline').fill('Tag via website');
      await o.page.locator('#pg-color').selectOption('teal');
      await o.page.locator('#pg-save').click();
      await o.page.waitForTimeout(1500);
      const e = await visibleError(o.page); if (e) t.fail('website error: ' + e);
      const r = await must(t, org.api_key, 'hack_get_event', { slug });
      if (!r.ok) return;
      eq(t, 'MCP tagline', r.data.tagline, 'Tag via website');
      if (!('accent_color' in r.data)) t.fail('MCP hack_get_event does not report the event colour (accent_color), and hack_update_event cannot set it');
      else eq(t, 'MCP accent_color', r.data.accent_color, 'teal');
    });

    const pw = await run.person(chromium, p4);
    await run.step(sim, 'website: participant joins with answers -> MCP organiser sees pending application', pw.page, async t => {
      await goto(pw.page, `/join/${code}`);
      await pw.page.locator('#join-form').waitFor({ state: 'visible', timeout: 10000 });
      await pw.page.locator('#join-name').fill('Parity P4');
      const qs = pw.page.locator('#join-questions [data-question]');
      const n = await qs.count();
      for (let i = 0; i < n; i++) await qs.nth(i).fill(i === 0 ? 'A sleep tracker' : 'Vegetarian');
      await pw.page.locator('#join-coc').check();
      await pw.page.locator('#join-btn').click();
      await pw.page.waitForURL(u => u.pathname.startsWith('/e/'), { timeout: 10000 }).catch(() => {});
      const e = await visibleError(pw.page); if (e) t.fail('website join error: ' + e);
      const apps = await must(t, org.api_key, 'hack_get_applications', { slug });
      const a4 = (apps.data || []).find(x => x.display_name === 'Parity P4');
      if (!a4) t.fail('MCP has no application for the website join');
      else { eq(t, 'MCP status', a4.status, 'pending'); if (!JSON.stringify(a4).includes('A sleep tracker')) t.fail('MCP application lacks the answer'); }
    });

    await run.step(sim, 'website: organiser approves -> MCP hack_application_status says approved', o.page, async t => {
      await manage(o.page, slug, 'people');
      await o.page.waitForTimeout(800);
      const card = o.page.locator('#applications-list .card', { hasText: 'Parity P4' });
      await card.locator('button', { hasText: 'Approve' }).click();
      await o.page.waitForTimeout(1500);
      const e = await visibleError(o.page); if (e) t.fail('website error: ' + e);
      const st = await must(t, p4.api_key, 'hack_application_status', { slug });
      t.note('MCP status: ' + JSON.stringify(st.data).slice(0, 160));
      if (st.ok && !/approved/.test(JSON.stringify(st.data))) t.fail('MCP status not approved');
    });

    await run.step(sim, 'website: participant starts a team and saves entry -> MCP teams/entries', pw.page, async t => {
      await goto(pw.page, `/e/${slug}/team`);
      await pw.page.locator('#start-team-name').fill('Parity Team Web');
      await pw.page.locator('#start-team-form button[type=submit]').click();
      await pw.page.locator('#ev-team').waitFor({ state: 'visible', timeout: 10000 }).catch(() => {});
      await pw.page.locator('#team-track').selectOption('track-1-open').catch(e => t.note('track select: ' + e.message.split('\n')[0]));
      await pw.page.locator('#team-track-save').click(); await pw.page.waitForTimeout(800);
      await pw.page.locator('#en-title').fill('Sleepwell');
      await pw.page.locator('#en-description').fill('Tracks sleep.');
      await pw.page.locator('#en-code').fill('https://example.com/sleep');
      await pw.page.locator('#en-save').click();
      await pw.page.waitForTimeout(1500);
      const e = await visibleError(pw.page); if (e) t.fail('website error: ' + e);
      const teams = await must(t, org.api_key, 'hack_get_teams', { slug });
      const tw = JSON.stringify(teams.data || '');
      if (!tw.includes('Parity Team Web')) t.fail('MCP hack_get_teams lacks the website team');
      const ents = await must(t, org.api_key, 'hack_get_entries', { slug });
      if (!JSON.stringify(ents.data || '').includes('Sleepwell')) t.fail('MCP hack_get_entries lacks the website entry');
      const mine = await must(t, p4.api_key, 'hack_get_entry', { slug });
      if (!JSON.stringify(mine.data || '').includes('Sleepwell')) t.fail('MCP hack_get_entry (participant) lacks the title');
      const ts = await must(t, p4.api_key, 'hack_get_team_status', { slug });
      if (!JSON.stringify(ts.data || '').includes('track-1-open')) t.fail('MCP team status lacks the track chosen on the website: ' + JSON.stringify(ts.data).slice(0, 160));
    });
    await pw.context.close();

    await run.step(sim, 'website: judging mode manual -> MCP hack_get_judging_settings', o.page, async t => {
      await manage(o.page, slug, 'judging');
      if (!(await o.page.locator('#jg-setup').evaluate(e => e.open))) await o.page.locator('#jg-setup > summary').click();
      await o.page.locator('#jg-mode-manual').check();
      await o.page.locator('#jg-settings-save').click();
      await o.page.waitForTimeout(1500);
      const e = await visibleError(o.page); if (e) t.fail('website error: ' + e);
      const r = await must(t, org.api_key, 'hack_get_judging_settings', { slug });
      if (r.ok) eq(t, 'MCP assignment_mode', r.data.assignment_mode, 'manual');
    });

    await run.step(sim, 'MCP hack_set_voting_settings -> website voting settings page, and back', o.page, async t => {
      const r = await must(t, org.api_key, 'hack_set_voting_settings', { slug, body: { enabled: true, opens_at: day(8) + 'T09:00', closes_at: day(9) + 'T09:00', eligibility: 'participants' } });
      if (!r.ok) return;
      t.note('MCP answer: ' + JSON.stringify(r.data).slice(0, 200));
      await goto(o.page, `/e/${slug}/voting-settings`);
      await o.page.locator('#settings-panel').waitFor({ state: 'visible', timeout: 10000 }).catch(() => {});
      await o.page.waitForTimeout(800);
      eq(t, 'enabled', await isChecked(o.page, '#enabled'), true);
      eq(t, 'opens', await val(o.page, '#opens'), day(8) + 'T09:00');
      eq(t, 'closes', await val(o.page, '#closes'), day(9) + 'T09:00');
      eq(t, 'eligibility', await val(o.page, '#eligibility'), 'participants');
      await o.page.locator('#eligibility').selectOption('event_members');
      await o.page.locator('#save-voting').click();
      await o.page.waitForTimeout(1500);
      const g = await must(t, org.api_key, 'hack_get_voting_settings', { slug });
      if (g.ok) eq(t, 'MCP eligibility after website save', g.data.eligibility, 'event_members');
      if (g.ok) t.note('directory_listed (default for a new event): ' + g.data.directory_listed);
    });

    await run.step(sim, 'website: entry needs + gallery toggled in Settings -> MCP reads them back', o.page, async t => {
      await manage(o.page, slug, 'settings');
      await o.page.locator('#er-screenshot').check(); await o.page.waitForTimeout(1000);
      await o.page.locator('#set-gallery').uncheck(); await o.page.waitForTimeout(1000);
      const r = await must(t, org.api_key, 'hack_get_event', { slug });
      if (!r.ok) return;
      const d = r.data;
      if (!('entry_required' in d)) t.fail('MCP hack_get_event does not return entry_required (settable through hack_update_event, but not readable)');
      else if (!JSON.stringify(d.entry_required).includes('screenshot')) t.fail('MCP entry_required lacks screenshot: ' + JSON.stringify(d.entry_required));
      if (!('gallery_open' in d)) t.fail('MCP hack_get_event does not return gallery_open');
      else eq(t, 'MCP gallery_open', d.gallery_open, false);
      if (!('submission_deadline' in d)) t.fail('MCP hack_get_event does not return submission_deadline');
    });
  } finally {
    await o.context.close().catch(() => {});
  }
}

// ---------------------------------------------------------------- main
const chromium = await run.browser('chromium');
const webkit = PART !== 'mcp' ? await run.browser('webkit') : null;
const deletes = [];
try {
  if (PART === 'all' || PART === 'mcp') await mcpPart(chromium);
  if (PART === 'all' || PART === 'account') await accountPart(chromium, webkit);
} catch (e) {
  console.log('ABORTED: ' + (e.stack || e));
} finally {
  for (const s of created) deletes.push(`${s} -> ${await deleteEvent(creatorKey || S6.organiser[0].api_key, s).catch(e => 'error ' + e.message)}`);
  console.log('deleted: ' + deletes.join(', '));
  fs.writeFileSync(`${OUT}/account-parity-deletes.txt`, deletes.join('\n') + '\n');
  await run.done();
}
