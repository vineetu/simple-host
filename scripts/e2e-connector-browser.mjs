#!/usr/bin/env node
// Browser end-to-end check of the connector consent page (internal/handler/
// static/connect.html), in real headless Chromium driven over CDP. No npm
// dependencies: Node 22's WebSocket and fetch.
//
// Proves:
//   1. A consent URL carrying ANOTHER account's sign-in token does not sign the
//      browser in and does not change localStorage (login CSRF, the 2026-09-24
//      review finding) — with a signed-in victim and with a signed-out one.
//   2. The legitimate Google path still works: the button carries a nonce hash,
//      the server accepts it as return_to, and the landing in the same tab signs
//      in and lets the person Allow.
//   3. The typed 6-digit code path works end to end, through Allow, the token
//      endpoint and a /mcp tool call.
//
// Needs a running server started with GOOGLE_OAUTH_CLIENT_ID/SECRET set to any
// values (so the Google button shows; Google itself is never contacted),
// RESEND_API_KEY set to any value, and psql access to its database:
//   BASE=http://localhost:18080 ADMIN_API_KEY=... PSQL_URL=postgres://... node scripts/e2e-connector-browser.mjs
import { spawn, execFileSync } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHash, randomBytes } from 'node:crypto';

const BASE = process.env.BASE || 'http://localhost:18080';
const ADMIN = process.env.ADMIN_API_KEY;
const PSQL_URL = process.env.PSQL_URL;
const CHROME = process.env.CHROME || 'chromium';
const REDIRECT = 'https://chat.example.com/oauth/callback';
const VERIFIER = 'dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk';
const CHALLENGE = 'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM';
let passed = 0;

function check(cond, label, extra = '') {
  if (!cond) { console.log('FAIL', label, extra); process.exit(1); }
  passed++; console.log('ok  ', label);
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const b64url = (buf) => buf.toString('base64').replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
const sql = (q) => execFileSync('psql', [PSQL_URL, '-tAc', q]).toString().trim();
function linkTokenFor(email) {
  const t = randomBytes(24).toString('hex');
  sql(`INSERT INTO auth_tokens (email, code, link_token, expires_at) VALUES ('${email}', '000000', '${t}', now() + interval '15 minutes')`);
  return t;
}

async function api(method, path, body, headers = {}) {
  const res = await fetch(BASE + path, { method, headers: { 'Content-Type': 'application/json', ...headers }, body: body && JSON.stringify(body), redirect: 'manual' });
  let json = null; try { json = await res.json(); } catch {}
  return { status: res.status, json, headers: res.headers };
}

// ---- minimal CDP client ------------------------------------------------------
class Page {
  constructor(ws) { this.ws = ws; this.id = 0; this.pending = new Map(); this.listeners = []; }
  static async open(port) {
    const res = await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: 'PUT' });
    const { webSocketDebuggerUrl } = await res.json();
    const ws = new WebSocket(webSocketDebuggerUrl);
    await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
    const p = new Page(ws);
    ws.onmessage = (m) => {
      const msg = JSON.parse(m.data);
      if (msg.id && p.pending.has(msg.id)) { p.pending.get(msg.id)(msg); p.pending.delete(msg.id); }
      else if (msg.method) p.listeners.forEach((f) => f(msg));
    };
    await p.send('Page.enable'); await p.send('Runtime.enable'); await p.send('Network.enable');
    return p;
  }
  send(method, params = {}) {
    const id = ++this.id;
    this.ws.send(JSON.stringify({ id, method, params }));
    return new Promise((r) => this.pending.set(id, (msg) => r(msg.result || msg)));
  }
  on(f) { this.listeners.push(f); }
  async eval(expr) {
    const r = await this.send('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true });
    return r.result ? r.result.value : undefined;
  }
  async goto(url, settle = 1500) { await this.send('Page.navigate', { url }); await sleep(settle); }
  async waitFor(expr, ms = 5000) {
    const end = Date.now() + ms;
    while (Date.now() < end) { if (await this.eval(expr)) return true; await sleep(100); }
    return false;
  }
  close() { this.ws.close(); }
}

const visibleStep = `['loading','error','signin','consent','done'].find(s => !document.getElementById('step-' + s).hidden)`;

// ---- setup -------------------------------------------------------------------
const run = randomBytes(3).toString('hex');
const created = (await api('POST', '/v1/admin/users', { emails: [`victim-${run}@example.com`, `attacker-${run}@example.com`] }, { 'X-API-Key': ADMIN })).json.created;
const victim = created.find((u) => u.username.startsWith('victim'));
const attacker = created.find((u) => u.username.startsWith('attacker'));
const clientId = (await api('POST', '/oauth/register', { client_name: 'Browser Test', redirect_uris: [REDIRECT], token_endpoint_auth_method: 'none' })).json.client_id;
const Q = new URLSearchParams({ response_type: 'code', client_id: clientId, redirect_uri: REDIRECT, code_challenge: CHALLENGE,
  code_challenge_method: 'S256', state: 'st', scope: 'sites', resource: BASE + '/mcp' }).toString();
const AUTHZ = BASE + '/oauth/authorize?' + Q;

const profile = mkdtempSync(join(tmpdir(), 'sh-consent-'));
const port = 9300 + Math.floor(Math.random() * 500);
const chrome = spawn(CHROME, ['--headless=new', '--no-sandbox', '--disable-gpu', `--remote-debugging-port=${port}`, `--user-data-dir=${profile}`, 'about:blank'], { stdio: 'ignore' });
process.on('exit', () => { chrome.kill(); rmSync(profile, { recursive: true, force: true }); });
for (let i = 0; i < 50; i++) { try { await fetch(`http://127.0.0.1:${port}/json/version`); break; } catch { await sleep(100); } }

// ---- 1. login CSRF: another account's token in the URL -------------------------
{
  const page = await Page.open(port);
  await page.goto(BASE + '/privacy.html', 800);
  await page.eval(`localStorage.setItem('apiKey', ${JSON.stringify(victim.api_key)})`);
  const attackerToken = linkTokenFor(attacker.username);
  const fakeHash = b64url(createHash('sha256').update('attacker-nonce').digest());
  for (const extra of [`&token=${attackerToken}`, `&cn=${fakeHash}&token=${attackerToken}`]) {
    await page.goto(AUTHZ + extra, 2000);
    check(await page.eval(`localStorage.getItem('apiKey')`) === victim.api_key, `signed-in victim: localStorage unchanged (${extra.startsWith('&cn') ? 'with forged cn' : 'bare token'})`);
    check(await page.eval(`document.getElementById('who-email').textContent`) === victim.username, 'consent still shows the victim account');
    check(!(await page.eval(`location.search.includes('token=')`)), 'token stripped from the address bar');
  }
  // The attacker's token was never redeemed by the page: it still works.
  const still = await api('POST', '/v1/auth/verify', { token: attackerToken });
  check(still.status === 200 && still.json.username === attacker.username, 'the page never sent the foreign token to /v1/auth/verify');
  // Signed-out victim.
  await page.eval(`localStorage.clear(); sessionStorage.clear()`);
  const t2 = linkTokenFor(attacker.username);
  await page.goto(AUTHZ + `&cn=${fakeHash}&token=${t2}`, 2000);
  check(await page.eval(`localStorage.getItem('apiKey')`) === null, 'signed-out victim: localStorage stays empty');
  check(await page.eval(visibleStep) === 'signin', 'signed-out victim sees the sign-in step, not consent');
  // Even a victim tab that holds a nonce is safe: the hash has to match it.
  await page.eval(`sessionStorage.setItem('sh-connect-nonce', 'victims-own-nonce')`);
  const t3 = linkTokenFor(attacker.username);
  await page.goto(AUTHZ + `&cn=${fakeHash}&token=${t3}`, 2000);
  check(await page.eval(`localStorage.getItem('apiKey')`) === null, 'a tab with its own nonce ignores a token bound to another nonce');
  page.close();
}

// ---- 2. legitimate Google path -------------------------------------------------
{
  const page = await Page.open(port);
  await page.goto(BASE + '/privacy.html', 800);
  await page.eval(`localStorage.clear(); sessionStorage.clear()`);
  // Never let the browser reach Google; record where it was sent.
  const seen = [];
  page.on((m) => { if (m.method === 'Network.requestWillBeSent') seen.push(m.params.request.url); });
  await page.send('Fetch.enable', { patterns: [{ urlPattern: 'https://accounts.google.com/*' }, { urlPattern: 'https://chat.example.com/*' }] });
  page.on((m) => {
    if (m.method === 'Fetch.requestPaused') page.send('Fetch.fulfillRequest', { requestId: m.params.requestId, responseCode: 200, body: Buffer.from('stub').toString('base64') });
  });
  await page.goto(AUTHZ, 1500);
  check(await page.waitFor(`!document.getElementById('google-btn').hidden`), 'Google button offered');
  await page.eval(`document.getElementById('google-btn').click()`);
  await sleep(1500);
  const start = seen.find((u) => u.startsWith(BASE + '/v1/auth/oauth/google?'));
  check(!!start, 'Google button starts the server-side sign-in');
  const returnTo = new URL(start).searchParams.get('return_to');
  const cn = new URL(returnTo).searchParams.get('cn');
  // The tab is on (stubbed) Google now; come back to this origin, where the
  // tab's sessionStorage lives, to read what the button stored.
  await page.goto(BASE + '/privacy.html', 800);
  const nonce = await page.eval(`sessionStorage.getItem('sh-connect-nonce')`);
  check(cn && nonce && b64url(createHash('sha256').update(nonce).digest()) === cn, 'return_to carries the hash of the nonce kept in this tab');
  check(seen.some((u) => u.startsWith('https://accounts.google.com/')), 'server accepted that return_to and redirected to Google');
  // Stand in for Google + the callback: the callback's landing URL is return_to
  // plus the one-time token for the account Google vouched for.
  const lt = linkTokenFor(victim.username);
  await page.goto(returnTo + '&token=' + lt, 2500);
  check(await page.eval(`localStorage.getItem('apiKey')`) === victim.api_key, 'Google landing in the same tab signs in');
  check(await page.eval(visibleStep) === 'consent', 'and shows the consent step');
  check(await page.eval(`sessionStorage.getItem('sh-connect-nonce')`) === null, 'nonce is single-use');
  await page.eval(`document.getElementById('allow-btn').click()`);
  await sleep(1500);
  const back = seen.find((u) => u.startsWith(REDIRECT));
  check(back && new URL(back).searchParams.get('code'), 'Allow returns a code to the app');
  page.close();
}

// ---- 3. typed code path, through /mcp -------------------------------------------
{
  const page = await Page.open(port);
  await page.goto(BASE + '/privacy.html', 800);
  await page.eval(`localStorage.clear(); sessionStorage.clear()`);
  const email = `typed-${run}@example.com`;
  const seen = [];
  page.on((m) => { if (m.method === 'Network.requestWillBeSent') seen.push(m.params.request.url); });
  // Stand in for the mail provider: answer POST /v1/auth as the server would
  // after sending, and put the code where the real request would have.
  await page.send('Fetch.enable', { patterns: [{ urlPattern: BASE + '/v1/auth' }, { urlPattern: 'https://chat.example.com/*' }] });
  page.on((m) => {
    if (m.method !== 'Fetch.requestPaused') return;
    if (m.params.request.url.startsWith('https://chat.example.com/')) {
      page.send('Fetch.fulfillRequest', { requestId: m.params.requestId, responseCode: 200, body: Buffer.from('stub').toString('base64') });
      return;
    }
    sql(`INSERT INTO auth_tokens (email, code, link_token, expires_at) VALUES ('${email}', '123456', '${randomBytes(24).toString('hex')}', now() + interval '15 minutes')`);
    page.send('Fetch.fulfillRequest', { requestId: m.params.requestId, responseCode: 202,
      responseHeaders: [{ name: 'Content-Type', value: 'application/json' }],
      body: Buffer.from(JSON.stringify({ message: 'sent', email, expires_in_seconds: 900 })).toString('base64') });
  });
  await page.goto(AUTHZ, 1500);
  check(await page.waitFor(`!document.getElementById('email-form').hidden`), 'email form offered');
  await page.eval(`document.getElementById('email').value = ${JSON.stringify(email)}; document.getElementById('email-form').requestSubmit()`);
  check(await page.waitFor(`!document.getElementById('code-form').hidden`), 'code step shown');
  await page.eval(`document.getElementById('code').value = '123456'; document.getElementById('code-form').requestSubmit()`);
  check(await page.waitFor(`${visibleStep} === 'consent'`), 'typed code signs in and shows consent');
  check(await page.eval(`document.getElementById('who-email').textContent`) === email, 'as the person who typed the code');
  await page.eval(`document.getElementById('allow-btn').click()`);
  await sleep(1500);
  const back = seen.find((u) => u.startsWith(REDIRECT));
  const code = back && new URL(back).searchParams.get('code');
  check(!!code, 'Allow returns a code');
  const tok = await fetch(BASE + '/oauth/token', { method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ grant_type: 'authorization_code', code, redirect_uri: REDIRECT, client_id: clientId, code_verifier: VERIFIER, resource: BASE + '/mcp' }) }).then((r) => r.json());
  check(!!tok.access_token, 'code exchanged for tokens');
  const who = await api('POST', '/mcp', { jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: 'who_am_i', arguments: {} } },
    { Authorization: 'Bearer ' + tok.access_token, 'MCP-Protocol-Version': '2025-06-18' });
  check(who.json.result.structuredContent.email === email, '/mcp acts as the person who signed in');
  page.close();
}

console.log(`\nALL ${passed} BROWSER CHECKS PASSED`);
process.exit(0);
