#!/usr/bin/env node
// Browser end-to-end check that nothing on the setup helper is required
// (static/setup/setup.js): on every path of both products, Next, Show my
// files and Show my commands work with every field left empty.
//
// Proves, per path (small box on UpCloud or your own server, Basic and
// Advanced; Enterprise do-it-myself, Basic and Advanced; the AWS quick path
// with a new cluster or yours, and through More settings):
//   - the output is reached with no field touched, and no page error;
//   - small box: the install command has no --host (the installer's setup
//     mode asks for the domain), parses in bash, and the settings' blanks
//     (MAIL_FROM, RESEND_API_KEY) are named in one line above them;
//   - Enterprise: config.env keeps each default (DB_NAME, DB_USER, no DB_PORT
//     line) and every value only the person knows is an empty NAME= under a
//     "# Fill in:" line, named in one line above the files;
//   - AWS: terraform.tfvars has only create_cluster and region, --tfvars
//     decodes to it exactly, the line parses in bash, the page says the line
//     asks for the rest, and the agent block says how to give them;
//   - no horizontal scroll at 390 px.
// Then: the Postgres host in the forms real installs use (one label, a
// Kubernetes service, IPv4 or IPv6 with or without a port, a trailing dot, a
// postgres:// URL) is accepted and written as DB_HOST (and DB_PORT); other
// real-world forms (an internal admin domain, an issuer without https://, an
// address with a port, an endpoint without a scheme) are accepted; hostile
// input is still refused and never reaches a file or a command.
// Screenshots of every all-empty output at 390 and 1280 px, light and dark,
// go to <shots-dir>.
//
//   NODE_PATH=/opt/pw/node_modules node scripts/e2e-setup-empty.js <base> <shots-dir>
const { chromium } = require('playwright');
const { execFileSync } = require('child_process');
const base = process.argv[2], shots = process.argv[3];
const assert = (c, m) => { if (!c) { console.error('FAIL: ' + m); process.exit(1); } else console.log('ok: ' + m); };
const parses = cmd => { try { execFileSync('bash', ['-n'], { input: cmd }); return true; } catch (e) { return false; } };

// Each path: how to get from the page to its output touching no field.
const PATHS = {
  'small-upcloud': { q: '?product=small-box', steps: async p => { await next(p); await click(p, 'Show my files'); } },
  'small-server': { q: '?product=small-box', steps: async p => { await next(p); await p.locator('label.choice:has(input[name=where][value=server])').click(); await click(p, 'Show my files'); } },
  'small-advanced': { q: '?product=small-box', steps: async p => { await p.locator('label.choice:has(input[name=mode][value=advanced])').click(); await next(p); await click(p, 'Next: every setting'); await click(p, 'Skip to my files'); } },
  'ent-diy': { q: '?product=enterprise', steps: async p => { await next(p); await click(p, 'Show my files'); } },
  'ent-diy-advanced': { q: '?product=enterprise', steps: async p => { await p.locator('label.choice:has(input[name=mode][value=advanced])').click(); await next(p); await click(p, 'Next: every setting'); await click(p, 'Skip to my files'); } },
  'aws-new': { q: '?product=enterprise&cloud=aws', steps: async p => { await next(p); await click(p, 'Show my commands'); } },
  'aws-yours': { q: '?product=enterprise&cloud=aws', steps: async p => { await p.locator('label.choice:has(input[name=cluster][value=yes])').click(); await next(p); await click(p, 'Show my commands'); } },
  'aws-more': { q: '?product=enterprise&cloud=aws', steps: async p => { await next(p); await click(p, 'More settings (optional)'); await click(p, 'Skip to my commands'); } }
};
async function next(p) { await p.getByRole('button', { name: 'Next', exact: true }).click(); await p.waitForSelector('#app .card h2'); }
async function click(p, name) { await p.getByRole('button', { name, exact: true }).click(); }

async function emptyPath(browser, name, width, scheme) {
  const ctx = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: scheme, timezoneId: 'America/New_York' });
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push(String(e)));
  const tag = `${name}-${width}-${scheme}`;
  await page.goto(base + '/setup' + PATHS[name].q);
  await PATHS[name].steps(page);
  await page.waitForSelector('#files, #commands', { timeout: 20000 });
  assert(await page.locator('.bad').count() === 0 && await page.locator('p.err:not([hidden])').count() === 0, `${tag}: no field refused`);
  const fill = (await page.locator('.fill').allTextContents()).join(' ');
  const pres = await page.locator('pre').allTextContents();
  if (name.startsWith('small')) {
    const cmd = pres.find(t => t.startsWith('f=$(mktemp)'));
    assert(cmd && /sudo bash "\$f"$/.test(cmd) && !cmd.includes('--host'), `${tag}: the install command has no --host (setup mode asks for the domain)`);
    assert(parses(cmd), `${tag}: the install command parses in bash`);
    assert(/^Fill in before you save the settings: Send email from \(MAIL_FROM\), Resend API key \(RESEND_API_KEY\)\.$/.test(fill), `${tag}: one line names the blanks (${fill})`);
    const env = pres.find(t => t.startsWith('# Simple Host settings'));
    assert(/# Fill in: the sender[^\n]*\nMAIL_FROM=\n/.test(env) && /\nRESEND_API_KEY=\n/.test(env), `${tag}: MAIL_FROM and RESEND_API_KEY are marked blanks`);
    assert(/chosen on the server after the install/.test(await page.locator('.summary').innerText()), `${tag}: the summary says where the domain is chosen`);
    const agent = pres.find(t => t.startsWith('# Set up Simple Host on a small box'));
    assert(/ask me for the domain Simple Host lives at/.test(agent) && /--host <domain> --content <sites>/.test(agent), `${tag}: the agent asks for the domain`);
  } else if (name.startsWith('ent')) {
    const cfg = pres[0], sec = pres[1];
    assert(cfg.startsWith('# Simple Host Enterprise: deploy/overlays/byo/config.env'), `${tag}: config.env shown`);
    for (const line of ['DB_NAME=simplehost', 'DB_USER=simplehost', 'SECURE_MODE=true', 'BACKUP_STORAGE_ENDPOINT=https://s3.us-east-1.amazonaws.com', 'BACKUP_STORAGE_REGION=us-east-1'])
      assert(cfg.split('\n').includes(line), `${tag}: config.env keeps ${line}`);
    assert(!/^DB_PORT=/m.test(cfg), `${tag}: no DB_PORT line (5432 is the default)`);
    const lines = cfg.split('\n');
    const empty = lines.map((l, i) => [l, i]).filter(([l]) => /^[A-Z_]+=$/.test(l));
    assert(empty.length === 7 && empty.every(([l, i]) => lines[i - 1].startsWith('# Fill in: ')), `${tag}: every empty value is under a "# Fill in:" line (${empty.map(x => x[0]).join(' ')})`);
    for (const n of ['PUBLIC_BASE_URL', 'ADMIN_EMAILS', 'OIDC_ISSUER', 'OIDC_CLIENT_ID', 'OWNER_CERT_ISSUER', 'DB_HOST', 'BACKUP_STORAGE_BUCKET'])
      assert(lines.includes(n + '='), `${tag}: ${n} is a marked blank`);
    assert(/^Fill in before you run it: Address \(PUBLIC_BASE_URL\), .*Postgres host \(DB_HOST\), Bucket name \(BACKUP_STORAGE_BUCKET\), the secrets in secrets\.env\.$/.test(fill), `${tag}: one line names the blanks (${fill})`);
    assert(/\nOIDC_CLIENT_SECRET=\n/.test(sec), `${tag}: secrets.env still names the secrets`);
    const agent = pres.find(t => t.startsWith('# Set up Simple Host Enterprise on Kubernetes'));
    assert(/ask me for each one \(Address \(PUBLIC_BASE_URL\)/.test(agent) && /'https:\/\/<address>\/readyz'/.test(agent), `${tag}: the agent asks for the blanks`);
  } else {
    const cmd = pres[0], tfvars = pres[pres.length - 1];
    const m = /--tfvars ([A-Za-z0-9+\/=]+)$/.exec(cmd);
    assert(m && Buffer.from(m[1], 'base64').toString('utf8') === tfvars, `${tag}: --tfvars is exactly the terraform.tfvars shown`);
    const vars = tfvars.split('\n').filter(l => l && !l.startsWith('#')).map(l => l.split(/\s*=/)[0]);
    assert(JSON.stringify(vars) === JSON.stringify(['create_cluster', 'region']), `${tag}: tfvars has only create_cluster and region (${vars.join(', ')})`);
    assert(tfvars.includes('create_cluster = ' + (name === 'aws-yours' ? 'false' : 'true') + '\n'), `${tag}: create_cluster as chosen`);
    assert(parses(cmd) && !cmd.includes('\n'), `${tag}: one line, and it parses in bash`);
    const want = 'It asks for these when it runs: Address, Admin emails, Issuer URL, Client ID' + (name === 'aws-yours' ? ', Cluster name' : '') + '.';
    assert(fill === want, `${tag}: the page says what the line asks for (${fill})`);
    const agent = pres.find(t => t.startsWith('# Set up Simple Host Enterprise on AWS'));
    assert(/Before step 3: the line does not have these/.test(agent) && agent.includes("TF_VAR_base_domain='sites.example.com'"), `${tag}: the agent block says how to give them without a terminal`);
  }
  if (width === 390) {
    const sw = await page.evaluate(() => document.documentElement.scrollWidth);
    assert(sw <= 390, `${tag}: no horizontal scroll (${sw})`);
  }
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: `${shots}/empty-${tag}.png`, fullPage: true });
  assert(errors.length === 0, `${tag}: no page errors ${errors.join(' ')}`);
  await ctx.close();
}

// diy fills the do-it-myself Basics with the given values (others empty)
// and returns config.env, or the fields refused.
async function diy(browser, fields) {
  const page = await browser.newPage();
  await page.goto(base + '/setup?product=enterprise');
  await next(page);
  for (const [k, v] of Object.entries(fields)) await page.fill('#f-' + k, v);
  await click(page, 'Show my files');
  await page.waitForSelector('#files, .bad', { timeout: 20000 });
  const r = { bad: await page.locator('.bad').evaluateAll(els => els.map(e => e.id.slice(2))) };
  if (await page.locator('#files').count()) r.cfg = await page.locator('#files pre').first().innerText();
  if (await page.locator('#files').count()) r.all = (await page.locator('pre').allTextContents()).join('\n');
  await page.close();
  return r;
}

(async () => {
  const browser = await chromium.launch();
  for (const name of Object.keys(PATHS)) for (const scheme of ['light', 'dark']) for (const width of [390, 1280]) await emptyPath(browser, name, width, scheme);

  // The Postgres host as people write it.
  const hosts = [['postgres', 'postgres', ''], ['postgres.db.svc.cluster.local', 'postgres.db.svc.cluster.local', ''], ['pg-rw.db', 'pg-rw.db', ''],
    ['10.0.0.5', '10.0.0.5', ''], ['10.0.0.5:6432', '10.0.0.5', '6432'], ['[fd00::1]:5433', '[fd00::1]', '5433'], ['fd00::1', '[fd00::1]', ''],
    ['db.example.com.', 'db.example.com', ''], ['PG.Example.COM', 'pg.example.com', ''], ['public-sh-abc.db.upclouddatabases.com', 'public-sh-abc.db.upclouddatabases.com', ''],
    ['postgres://pg.db:5433/simplehost', 'pg.db', '5433'], ['my_pg.internal', 'my_pg.internal', '']];
  for (const [typed, host, port] of hosts) {
    const r = await diy(browser, { dbHost: typed });
    assert(r.cfg && r.cfg.split('\n').includes('DB_HOST=' + host) && (port ? r.cfg.split('\n').includes('DB_PORT=' + port) : !/^DB_PORT=/m.test(r.cfg)),
      `Postgres host ${typed} is accepted: DB_HOST=${host}${port ? ', DB_PORT=' + port : ''} (${r.bad})`);
  }

  // Other real-world forms.
  let r = await diy(browser, { host: 'Sites.Corp.Internal:8443', admins: 'platform@corp; Alex@Example.com', issuer: 'keycloak.corp:8443/realms/main/', clientId: 'simple-host',
    issuerName: 'Internal-CA', endpoint: 'minio.minio.svc:9000', region: 'auto', bucketName: 'My_Bucket', dbName: 'simple-host', dbUser: 'sh_owner', proxies: '10.0.0.0/8 fd00::/8' });
  for (const line of ['PUBLIC_BASE_URL=https://sites.corp.internal:8443', 'ADMIN_EMAILS=platform@corp,alex@example.com', 'OIDC_ISSUER=https://keycloak.corp:8443/realms/main', 'OIDC_CLIENT_ID=simple-host',
    'OWNER_CERT_ISSUER=internal-ca', 'BACKUP_STORAGE_ENDPOINT=https://minio.minio.svc:9000', 'BACKUP_STORAGE_REGION=auto', 'BACKUP_STORAGE_BUCKET=My_Bucket', 'DB_NAME=simple-host', 'DB_USER=sh_owner', 'TRUSTED_PROXY_CIDRS=10.0.0.0/8,fd00::/8'])
    assert(r.cfg && r.cfg.split('\n').includes(line), 'real-world form accepted: ' + line + ' ' + JSON.stringify(r.bad));

  // Hostile input is refused and never reaches a file.
  const hostile = [['dbHost', 'a;id'], ['dbHost', '$(id)'], ['dbHost', 'x"y'], ['dbHost', 'a b'], ['dbHost', 'pg:99999'], ['host', 'x.com;id'], ['host', '10.0.0.5'],
    ['admins', 'a@x.com`id`'], ['admins', 'a$(id)@x.com'], ['issuer', 'https://x.okta.com/"a'], ['issuer', 'http://x.okta.com'], ['endpoint', 'https://x" y'], ['endpoint', 'http://minio:9000'],
    ['bucketName', 'a;b'], ['region', 'us east'], ['dbName', 'x;drop'], ['dbUser', "o'x"], ['issuerName', 'a b'], ['proxies', 'not a range'], ['clientId', 'a"b']];
  for (const [k, v] of hostile) {
    r = await diy(browser, { [k]: v });
    assert(!r.cfg && r.bad.includes(k), `${k} ${JSON.stringify(v)} is refused`);
  }
  // The small box: its public names and the command.
  const small = async fields => {
    const page = await browser.newPage();
    await page.goto(base + '/setup?product=small-box');
    await next(page);
    for (const [k, v] of Object.entries(fields)) await page.fill('#f-' + k, v);
    await click(page, 'Show my files');
    await page.waitForSelector('#files, .bad', { timeout: 20000 });
    const out = { bad: await page.locator('.bad').evaluateAll(els => els.map(e => e.id.slice(2))), cmd: await page.locator('#files pre').first().innerText().catch(() => '') };
    await page.close();
    return out;
  };
  r = await small({ domain: 'HTTPS://Hack.Example.com./', email: 'ops@corp' });
  assert(/ --host hack\.example\.com --content sites\.hack\.example\.com --email ops@corp$/.test(r.cmd) && parses(r.cmd), 'small box: a domain typed as a URL is tidied; an internal email is accepted');
  for (const [k, v] of [['domain', '$(id).com'], ['domain', 'hack.example.com:8443'], ['domain', '203.0.113.5'], ['domain', 'localhost'], ['content', 'x y'], ['email', 'a;id@x'], ['email', "a'b@x`id`"]]) {
    r = await small({ domain: k === 'domain' ? v : 'hack.example.com', [k]: v });
    assert(!r.cmd && r.bad.includes(k), `small box: ${k} ${JSON.stringify(v)} is refused`);
  }
  await browser.close();
  console.log('all ok');
})().catch(e => { console.error(e); process.exit(1); });
