#!/usr/bin/env node
// Browser end-to-end check of the setup helper's assistant
// (static/setup/assist.js → POST /v1/setup/assist) against a server whose
// model backend is the fake in scripts/e2e-setup-assist-sidecar.py.
//
// Proves, for both products: a question streams an answer; a plain request
// ("a 200-person company with Microsoft sign-in", "a hackathon for 150")
// shows only the changes the server kept (a looser security value and a no-op
// are dropped), with no hostname, email or ID from the form in the request;
// Apply and Apply all change the form exactly as typing would (the identity
// provider list, the fields) and the files follow; "Clean up my choices"
// offers a reset that, applied on the files step, updates the files at once;
// a pasted error is redacted, shown for review, and sent only on "Send for
// help", and its diagnosis offers a setting to apply; /setup#help opens the
// assistant ready for a paste; the files step has the block for your AI agent;
// the panel's basics of accessibility (dialog name, button names, Escape and
// focus). Screenshots at 390 and 1280 px go to <shots-dir>.
//
// Needs Playwright (NODE_PATH pointing at a node_modules that has it) and a
// Chromium. Start the fake sidecar and a server with
//   LLM_API_KEY=fake LLM_BASE_URL=http://127.0.0.1:<sidecar port>/v1 PUBLIC_BASE_URL=<base>
//   ASK_BURST=50 ASK_EVERY_SECONDS=1 (the run asks more than one address may by default)
// then: NODE_PATH=/opt/pw/node_modules node scripts/e2e-setup-assist.js <base> <shots-dir>
const { chromium } = require('playwright');
const base = process.argv[2], shots = process.argv[3];
const assert = (c, m) => { if (!c) { console.error('FAIL: ' + m); process.exit(1); } else console.log('ok: ' + m); };

// Requests to the assistant, as sent.
function watch(page) {
  const sent = [];
  page.on('request', r => { if (r.url().endsWith('/v1/setup/assist')) sent.push(JSON.parse(r.postData())); });
  page.on('response', async r => { if (r.url().endsWith('/v1/setup/assist') && r.status() !== 200) console.log('assist answered ' + r.status() + ': ' + await r.text().catch(() => '')); });
  return sent;
}
async function openPanel(page) {
  await page.getByRole('button', { name: 'Open the setup assistant' }).click();
  await page.waitForSelector('#sh-assist-panel:not([hidden])');
}
// ask types a message and waits for its answer to finish.
async function ask(page, text) {
  const turns = await page.locator('.sh-ask-turn').count();
  await page.fill('#sh-assist-q', text);
  await page.press('#sh-assist-q', 'Enter');
  await page.waitForFunction(n => {
    const t = document.querySelectorAll('.sh-ask-turn');
    return t.length > n && !t[t.length - 1].querySelector('.is-loading') && !document.querySelector('#sh-assist-panel[aria-busy]');
  }, turns);
  return page.locator('.sh-ask-turn').last();
}
async function shot(page, name) { await page.screenshot({ path: `${shots}/${name}.png` }); }
async function a11y(page, label) {
  const r = await page.evaluate(() => {
    const p = document.getElementById('sh-assist-panel');
    const title = document.getElementById(p.getAttribute('aria-labelledby'));
    const unnamed = [...document.querySelectorAll('.sh-assist button')].filter(b => !(b.getAttribute('aria-label') || b.textContent).trim());
    const input = document.getElementById('sh-assist-q');
    return { role: p.getAttribute('role'), title: title && title.textContent, unnamed: unnamed.length, label: !!document.querySelector('label[for="sh-assist-q"]') && !!input };
  });
  assert(r.role === 'dialog' && r.title === 'Setup assistant' && r.unnamed === 0 && r.label, label + ': panel is a named dialog, every button has a name, the field has a label');
}
async function closeWithEscape(page) {
  await page.press('#sh-assist-q', 'Escape');
  assert(await page.locator('#sh-assist-panel').isHidden(), 'Escape closes the panel');
  assert(await page.evaluate(() => document.activeElement && document.activeElement.classList.contains('sh-ask-fab')), 'focus returns to the Assistant button');
}

async function enterprise(browser, width) {
  const page = await browser.newPage({ viewport: { width, height: width < 600 ? 844 : 900 } });
  const sent = watch(page);
  const tag = `enterprise-${width}`;
  await page.goto(base + '/setup?product=enterprise');
  await page.getByRole('button', { name: 'Next' }).click();
  await page.fill('#f-host', 'sites.example.com');
  await page.fill('#f-admins', 'platform@example.com');
  await page.fill('#f-clientId', 'client-123');
  await page.fill('#f-issuer', 'https://acme.okta.com');
  await page.fill('#f-issuerName', 'internal-ca');
  await page.fill('#f-bucketName', 'sh-sites');
  await page.fill('#f-dbHost', 'db.example.com');
  await openPanel(page);
  await a11y(page, tag);
  assert((await page.locator('.sh-assist-for').innerText()) === 'For Simple Host Enterprise', 'the panel names the product');
  assert(await page.locator('.sh-assist-example').count() === 2, 'two example requests while the panel is empty');

  // 1. A question.
  let turn = await ask(page, 'How long do people stay signed in?');
  assert(/SESSION_TTL/.test(await turn.locator('.sh-ask-a').innerText()), 'a question gets an answer');
  assert(await turn.locator('.sh-assist-item').count() === 0, 'a question proposes nothing');
  await shot(page, `${tag}-1-ask`);

  // 2. Fill in the form from a plain request.
  turn = await ask(page, 'Set this up for a 200-person company with Microsoft sign-in and stricter security');
  const body = JSON.stringify(sent[sent.length - 1]);
  assert(!/example\.com|client-123|internal-ca|sh-sites|okta\.com/.test(body), 'the request carries no hostname, email or ID from the form: ' + body);
  assert(sent[sent.length - 1].basics.idp === 'okta' && sent[sent.length - 1].step === 'basics', 'the request says where the visitor is and the provider picked');
  assert(sent[sent.length - 1].history.length === 1, 'the earlier turn goes with it');
  const items = turn.locator('.sh-assist-item');
  assert(await items.count() === 5, 'five changes shown (the looser access log setting was dropped): ' + await items.count());
  assert(!/ACCESS_LOG_VISIBILITY/.test(await turn.innerText()), 'the dropped change is not offered');
  assert(/Identity provider: Microsoft Entra ID/.test(await items.first().innerText()), 'the provider is offered as a basic answer');
  assert(/Set SESSION_TTL to 4h — A shorter working session\./.test(await turn.innerText()), 'an item reads "Set X to Y — why"');
  await shot(page, `${tag}-2-chips`);

  // 3. Apply one, then the rest.
  await items.first().getByRole('button', { name: /^Apply:/ }).click();
  assert(await page.locator('#f-idp').inputValue() === 'entra', 'Apply sets the identity provider in the form');
  assert(/microsoftonline/.test(await page.locator('#f-issuer').inputValue()), 'as picking it does: the issuer template follows');
  assert(await page.locator('.field.assisted #f-idp').count() === 1, 'the applied field is highlighted');
  await turn.getByRole('button', { name: /Apply all/ }).click();
  assert(await turn.locator('.sh-assist-done.is-applied').count() === 5, 'Apply all applies the rest');
  await page.locator('#f-idp').scrollIntoViewIfNeeded();
  await shot(page, `${tag}-3-applied`);
  await page.fill('#f-issuer', 'https://login.microsoftonline.com/0000-tenant/v2.0');
  if (width < 600) await closeWithEscape(page);
  await page.getByRole('button', { name: 'Show my files' }).click();
  await page.waitForSelector('#files pre');
  let cfg = await page.locator('#files pre').first().innerText();
  for (const line of ['SESSION_TTL=4h', 'SESSION_IDLE=15m', 'API_KEY_MAX_DAYS=30', 'NETWORK_ACCESS_APPROVALS=2', 'OIDC_ISSUER=https://login.microsoftonline.com/0000-tenant/v2.0']) {
    assert(cfg.includes(line), 'config.env has ' + line);
  }
  const agent = await page.locator('#agent pre').last().innerText();
  assert(await page.locator('#where').count() === 0 && !/UpCloud/.test(agent), 'Enterprise has no UpCloud step');
  assert(/deploy\/overlays\/byo\/config\.env/.test(agent) && agent.includes('SESSION_TTL=4h') && agent.includes('/setup?product=enterprise#help') && agent.includes('OIDC_CLIENT_SECRET='), 'the block for your AI agent has the steps, the files, blanks for secrets and the help link');
  assert(!/Claude|Codex|Cursor|ChatGPT/.test(agent + await page.locator('#agent').innerText()), 'the agent block names no vendor');
  await page.locator('#agent').screenshot({ path: `${shots}/${tag}-files-agent.png` });

  // 4. Clean up, on the files step.
  if (await page.locator('#sh-assist-panel').isHidden()) await openPanel(page);
  await page.getByRole('button', { name: 'Clean up my choices' }).click();
  await page.waitForFunction(() => !document.querySelector('#sh-assist-panel[aria-busy]') && document.querySelectorAll('.sh-ask-turn').length === 3 && !document.querySelector('.sh-ask-turn:last-child .is-loading'));
  turn = page.locator('.sh-ask-turn').last();
  assert(sent[sent.length - 1].message === 'Clean up my choices' && sent[sent.length - 1].choices.SESSION_IDLE === '15m', 'clean-up sends the current choices');
  assert(await turn.locator('.sh-assist-item').count() === 1 && /SESSION_IDLE to 30m/.test(await turn.innerText()), 'clean-up offers a reset to the default');
  await turn.getByRole('button', { name: /^Apply:/ }).click();
  cfg = await page.locator('#files pre').first().innerText();
  assert(!/SESSION_IDLE/.test(cfg) && cfg.includes('SESSION_TTL=4h'), 'the files follow at once: SESSION_IDLE back to its default');
  assert((await page.locator('.check-note').innerText()) === 'Updated with the assistant.', 'the files say they were updated, with no second check');
  await shot(page, `${tag}-4-cleanup`);

  // 5. A pasted error: redacted, reviewed, then sent.
  await page.getByRole('button', { name: 'Paste an error' }).click();
  await page.fill('#sh-assist-paste-in', 'simple-host  | 2026/09/27 load config ok\nsimple-host  | discover OIDC provider: Get "https://login.microsoftonline.com/0000-tenant/v2.0/.well-known/openid-configuration": dial tcp: i/o timeout\nDB_PASSWORD=hunter2\nnotify alex@example.com');
  const before = sent.length;
  await page.getByRole('button', { name: 'Review what will be sent' }).click();
  const review = await page.locator('.sh-assist-sent').innerText();
  assert(!/hunter2|alex@example/.test(review) && /DB_PASSWORD=\[redacted\]/.test(review) && /\[email\]/.test(review), 'the review shows the output redacted');
  assert(/2 things hidden/.test(await page.locator('.sh-assist-paste').innerText()), 'the review says how many things were hidden');
  assert(sent.length === before, 'nothing is sent before Send for help');
  await shot(page, `${tag}-5-review`);
  await page.getByRole('button', { name: 'Send for help' }).click();
  await page.waitForFunction(n => document.querySelectorAll('.sh-ask-turn').length === n && !document.querySelector('#sh-assist-panel[aria-busy]'), 4);
  const p = sent[sent.length - 1];
  assert(p.pasted && !/hunter2|alex@example/.test(p.pasted) && p.message === 'Help me fix this error.', 'the paste is sent redacted, with the default message');
  assert(/identity provider/.test(await page.locator('.sh-ask-turn').last().innerText()), 'the diagnosis is shown');
  await shot(page, `${tag}-6-diagnosis`);
  await page.close();
}

// The small box's "Where it runs": UpCloud recommended and chosen, its
// sign-up button the referral link (new tab, noopener) with the note beside
// it, the steps, and nowhere on the page a field for a credential.
async function upcloud(page, tag) {
  assert(await page.getByRole('radio', { name: /UpCloud \(recommended\)/ }).isChecked(), 'UpCloud is the recommended choice, picked by default');
  const btn = page.getByRole('link', { name: 'Create your UpCloud account — $25 in credits' });
  assert(await btn.count() === 1, 'the sign-up button says what it gives');
  assert(await btn.getAttribute('href') === 'https://signup.upcloud.com/?promo=JF2WCV', 'the button is the referral link, exactly');
  assert(await btn.getAttribute('target') === '_blank' && /\bnoopener\b/.test(await btn.getAttribute('rel')), 'it opens in a new tab with rel=noopener');
  const where = await page.locator('#where').innerText();
  assert(where.includes('Referral link. New accounts through this link get $25 of UpCloud credit; their terms apply.'), 'the referral note sits under it');
  assert(where.includes('The smallest UpCloud server (1 CPU, 1 GB, about $5/month) runs Simple Host comfortably; we test on it.'), 'with one line on why');
  assert(/create an API user/.test(where) && /copy the prompt into your AI agent/.test(where), 'and the steps: account, API user, this page, the prompt');
  const creds = await page.evaluate(() => [...document.querySelectorAll('input, textarea, select')].filter(i => i.type !== 'radio' && i.type !== 'checkbox').filter(i => i.type === 'password' || /upcloud|password|token|secret|credential/i.test(i.id + ' ' + i.name + ' ' + (i.labels && i.labels[0] ? i.labels[0].textContent : ''))).map(i => i.id || i.name));
  assert(creds.length === 0, 'no field on the page takes a credential: ' + creds.join(', '));
  await page.locator('#where').screenshot({ path: `${shots}/${tag}-0-upcloud.png` });
  await page.locator('label.choice', { hasText: 'A server I already have' }).click();
  assert(await btn.count() === 0 && /fresh Ubuntu server/.test(await page.locator('#where').innerText()), 'a server of your own says what it needs instead');
  await page.locator('label.choice', { hasText: 'UpCloud (recommended)' }).click();
}

async function smallBox(browser, width) {
  const page = await browser.newPage({ viewport: { width, height: width < 600 ? 844 : 900 } });
  const sent = watch(page);
  const tag = `small-box-${width}`;
  await page.goto(base + '/setup?product=small-box');
  await page.getByRole('button', { name: 'Next' }).click();
  await upcloud(page, tag);
  await page.fill('#f-domain', 'hack.example.com');
  await openPanel(page);
  await a11y(page, tag);
  assert((await page.locator('.sh-assist-for').innerText()) === 'For a small box', 'the panel names the product');

  let turn = await ask(page, 'What does KEEP_VERSIONS do?');
  assert(/versions of each site/.test(await turn.innerText()), 'a question gets an answer: ' + await turn.innerText());
  assert(sent[0].choices.KEEP_VERSIONS === '1', 'the installer\'s own KEEP_VERSIONS goes as a choice');
  await shot(page, `${tag}-1-ask`);

  turn = await ask(page, 'Set this up for a weekend hackathon with 150 people signing in by emailed code');
  assert(!/hack\.example\.com/.test(JSON.stringify(sent[sent.length - 1])), 'the request carries no hostname');
  assert(await turn.locator('.sh-assist-item').count() === 3, 'three changes shown (a too-loose sign-in limit and a no-op were dropped)');
  await shot(page, `${tag}-2-chips`);
  await turn.getByRole('button', { name: /Apply all/ }).click();
  assert(await turn.locator('.sh-assist-done.is-applied').count() === 3, 'Apply all applies every change');
  await shot(page, `${tag}-3-applied`);
  if (width < 600) await closeWithEscape(page);
  await page.getByRole('button', { name: 'Show my files' }).click();
  await page.waitForSelector('#files pre');
  let env = await page.locator('#files pre').nth(1).innerText();
  for (const line of ['RATE_LIMIT_UPLOAD=120,2s', 'RATE_LIMIT_STATE=240,250ms', 'MAX_SITES_PER_ACCOUNT=20']) assert(env.includes(line), '.env has ' + line);
  assert(await page.locator('#agent').evaluate(n => n === document.querySelector('#app .card')), 'on UpCloud the block for your agent comes first');
  const creds = await page.locator('#agent pre').first().innerText();
  assert(/read -r UPCLOUD_USERNAME/.test(creds) && /stty -echo; read -r UPCLOUD_PASSWORD; stty echo/.test(creds) && /export UPCLOUD_USERNAME UPCLOUD_PASSWORD$/.test(creds) && !creds.includes('\n'), 'the terminal line asks for the API user and keeps the password unseen: ' + creds);
  const agent = await page.locator('#agent pre').last().innerText();
  const pinned = /curl -fsSL https:\/\/raw\.githubusercontent\.com\/vineetu\/simple-host\/v\d+\.\d+\.\d+\/deploy\/install\/install\.sh -o \/tmp\/install\.sh && sudo bash \/tmp\/install\.sh --host hack\.example\.com --content sites\.hack\.example\.com/;
  assert(pinned.test(agent) && !/simple-host\/main\//.test(agent), 'the prompt runs the installer from the pinned release with this page\'s flags');
  assert(pinned.test(await page.locator('#files pre').first().innerText()), 'so does the install command');
  for (const want of ['UpCloud', 'UPCLOUD_USERNAME', 'upctl', 'STARTER-1xCPU-1GB', 'Ubuntu Server 24.04 LTS', 'tier `standard`', '~/.ssh/simple-host.pub', 'hack.example.com, *.hack.example.com', 'https://hack.example.com/admin', 'https://hack.example.com/healthz', 'root@<the server’s IPv4>'])
    assert(agent.includes(want), 'the UpCloud prompt has ' + want);
  assert(/never ask me to paste them into this chat, never print them, and never write them to a file/.test(agent), 'the prompt keeps the UpCloud credentials in the environment');
  assert(!/UPCLOUD_PASSWORD=\S/.test(agent) && !/curl[^\n]* -u /.test(agent), 'the prompt holds no credential value and never puts one in a command\'s arguments');
  assert(agent.includes(`printf 'header = "Authorization: Basic %s"\\n' "$(printf '%s:%s' "$UPCLOUD_USERNAME" "$UPCLOUD_PASSWORD" | base64 | tr -d '\\n')" | curl -fsS -K - https://api.upcloud.com/1.3/account`), 'curl reads the API user from its standard input');
  assert(agent.includes('dig +short hack.example.com') && agent.includes('RATE_LIMIT_UPLOAD=120,2s') && agent.includes('/healthz') && agent.includes('/setup?product=small-box#help'), 'the block for your AI agent has DNS, the files, the checks and the help link');
  await page.locator('#agent').screenshot({ path: `${shots}/${tag}-files-agent.png` });

  if (await page.locator('#sh-assist-panel').isHidden()) await openPanel(page);
  await page.getByRole('button', { name: 'Clean up my choices' }).click();
  await page.waitForFunction(() => !document.querySelector('#sh-assist-panel[aria-busy]') && document.querySelectorAll('.sh-ask-turn').length === 3 && !document.querySelector('.sh-ask-turn:last-child .is-loading'));
  turn = page.locator('.sh-ask-turn').last();
  await turn.getByRole('button', { name: /^Apply:/ }).click();
  env = await page.locator('#files pre').nth(1).innerText();
  assert(!/RATE_LIMIT_STATE/.test(env) && env.includes('RATE_LIMIT_UPLOAD=120,2s'), 'clean-up applied: the files follow');
  await shot(page, `${tag}-4-cleanup`);
  await page.close();

  // /setup#help: the assistant opens ready for a paste, on the first step.
  const help = await browser.newPage({ viewport: { width, height: width < 600 ? 844 : 900 } });
  const hs = watch(help);
  await help.goto(base + '/setup?product=small-box#help');
  await help.waitForSelector('#sh-assist-paste:not([hidden])');
  assert(await help.evaluate(() => document.activeElement.id === 'sh-assist-paste-in'), '#help opens the paste box with focus in it');
  await help.fill('#sh-assist-paste-in', 'FAILED: the instance did not answer within two minutes.\napp-1  | 2026/09/27 10:00:01 load config: KEEP_VERSIONS="-1": want a whole number from 0 to 1000000\napp-1  | ADMIN_API_KEY=3f7a9c0e1b2d4f6a8c0e2b4d6f8a0c2e4b6d8f0a2c4e6b8d0f2a4c6e8b0d2f4a');
  await help.getByRole('button', { name: 'Review what will be sent' }).click();
  assert(!/3f7a9c0e/.test(await help.locator('.sh-assist-sent').innerText()), 'the admin key is hidden in the review');
  await shot(help, `${tag}-5-review`);
  await help.getByRole('button', { name: 'Send for help' }).click();
  await help.waitForSelector('.sh-assist-item');
  assert(hs.length === 1 && !/3f7a9c0e/.test(hs[0].pasted) && hs[0].step === 'choose', 'the paste went redacted, from the first step');
  await shot(help, `${tag}-6-diagnosis`);
  await help.locator('.sh-assist-item').getByRole('button', { name: /^Apply:/ }).click();
  if (width < 600) await closeWithEscape(help); else await help.getByRole('button', { name: 'Close the assistant' }).click();
  await help.getByRole('button', { name: 'Next' }).click();
  await help.locator('label.choice', { hasText: 'A server I already have' }).click();
  await help.fill('#f-domain', 'hack.example.com');
  await help.getByRole('button', { name: 'Show my files' }).click();
  await help.waitForSelector('#files pre');
  assert((await help.locator('#files pre').first().innerText()).includes('--keep-versions 5'), 'the applied fix is in the install command');
  const own = await help.locator('#agent pre').innerText();
  assert(own.includes('--keep-versions 5') && !/UpCloud|upctl|UPCLOUD/.test(own) && own.includes('fresh Ubuntu server'), 'on a server of your own the prompt has no UpCloud steps');
  assert(await help.locator('#files').evaluate(n => n === document.querySelector('#app .card')), 'and the files come first');
  await help.close();
}

(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROMIUM || '/usr/local/bin/chromium' });
  for (const width of [1280, 390]) {
    await enterprise(browser, width);
    await smallBox(browser, width);
  }
  await browser.close();
  console.log('ALL OK');
})().catch(e => { console.error(e); process.exit(1); });
