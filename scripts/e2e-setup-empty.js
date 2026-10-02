#!/usr/bin/env node
// Empty navigation, input validation and small-box regression coverage.
// Enterprise output is parsed as YAML; cloud URLs use the same existing-cluster flow.
// NODE_PATH=/opt/pw/node_modules node scripts/e2e-setup-empty.js <base> <shots-dir>
const { chromium } = require('playwright');
const { execFileSync } = require('child_process');
const base = process.argv[2], shots = process.argv[3];
const assert = (c, m) => { if (!c) { console.error('FAIL: ' + m); process.exit(1); } else console.log('ok: ' + m); };
const parses = cmd => { try { execFileSync('bash', ['-n'], { input: cmd }); return true; } catch (e) { return false; } };

const yaml = text => JSON.parse(execFileSync('python3', ['-c', 'import sys,yaml,json;json.dump(yaml.safe_load(sys.stdin),sys.stdout)'], { input: text, encoding: 'utf8' }));
// Each path: how to get from the page to its output touching no field.
const PATHS = {
  'small-upcloud': { q: '?product=small-box', steps: async p => { await next(p); await click(p, 'Show my files'); } },
  'small-server': { q: '?product=small-box', steps: async p => { await next(p); await p.locator('label.choice:has(input[name=where][value=server])').click(); await click(p, 'Show my files'); } },
  'small-advanced': { q: '?product=small-box', steps: async p => { await p.locator('label.choice:has(input[name=mode][value=advanced])').click(); await next(p); await click(p, 'Next: every setting'); await click(p, 'Skip to my files'); } },
  'ent-diy': { q: '?product=enterprise', steps: async p => { await next(p); await click(p, 'Show my files'); } },
  'ent-diy-advanced': { q: '?product=enterprise', steps: async p => { await p.locator('label.choice:has(input[name=mode][value=advanced])').click(); await next(p); await click(p, 'Next: every setting'); await click(p, 'Skip to my files'); } },
  'ent-yaml': { q: '?product=enterprise', steps: async p => { await p.locator('label.choice:has(input[name=output][value=yaml])').click(); await next(p); await click(p, 'Show my files'); } },
  'ent-incluster': { q: '?product=enterprise', steps: async p => { await next(p); await p.locator('label.choice:has(input[name=postgresMode][value=incluster])').click(); await click(p, 'Show my files'); } },
  'ent-old-cloud-url': { q: '?product=enterprise&cloud=aws', steps: async p => { await next(p); await click(p, 'Show my files'); } }

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
  } else {
    const cfg = yaml(await page.locator('[data-file="values.yaml"] pre').textContent()).enterprise, sec = await page.locator('[data-file="secrets.env"] pre').textContent();
    assert(cfg.postgres.database === 'simplehost' && cfg.postgres.user === 'simplehost', `${tag}: database defaults`);
    assert(cfg.storage.endpoint === '' && cfg.storage.region === '', `${tag}: no storage provider assumed`);
    assert(cfg.secrets.existingSecret === 'simple-host-secrets', `${tag}: persistent Secret on both paths`);
    assert(cfg.postgres.mode === (name === 'ent-incluster' ? 'incluster' : 'external'), `${tag}: Postgres mode`);
    if (cfg.postgres.mode === 'external') assert(Number(cfg.postgres.external.port) === 5432 && cfg.postgres.external.host === '', `${tag}: external database placeholder and standard port`);
    else assert(cfg.postgres.incluster.size === '5Gi', `${tag}: persisted in-cluster database size`);
    assert(/Fill in/i.test(fill) && /secret/i.test(fill), `${tag}: missing values named before installation`);
    assert(/\nOIDC_CLIENT_SECRET=\n/.test(sec), `${tag}: secret template has blanks`);
    const all = pres.join('\n');
    assert(all.includes('helm ') && all.includes('version: "0.2.0"') && all.includes('--context') && !/terraform|CloudShell|create_cluster/.test(all), `${tag}: pinned install into an existing context`);
    assert(name !== 'ent-yaml' || /helm template/.test(all), `${tag}: YAML comes from same chart`);
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
// and returns parsed values.yaml, or the fields refused.
async function diy(browser, fields) {
  const page = await browser.newPage();
  await page.goto(base + '/setup?product=enterprise');
  await next(page);
  for (const [k, v] of Object.entries(fields)) await page.fill('#f-' + k, v);
  await click(page, 'Show my files');
  await page.waitForSelector('#files, .bad', { timeout: 20000 });
  const r = { bad: await page.locator('.bad').evaluateAll(els => els.map(e => e.id.slice(2))) };
  if (await page.locator('#files').count()) r.cfg = yaml(await page.locator('[data-file="values.yaml"] pre').textContent()).enterprise;
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
    assert(r.cfg && r.cfg.postgres.external.host === host && Number(r.cfg.postgres.external.port) === Number(port || 5432),
      `Postgres host ${typed} is accepted with the right port (${r.bad})`);
  }

  let r = await diy(browser, { host: 'Sites.Corp.Internal', admins: 'platform@corp; Alex@Example.com', issuer: 'keycloak.corp:8443/realms/main/', clientId: 'simple-host',
    issuerName: 'Internal-CA', endpoint: 'minio.minio.svc:9000', region: 'auto', bucketName: 'My_Bucket', dbName: 'simple-host', dbUser: 'sh_owner', proxies: '10.0.0.0/8 fd00::/8' });
  const v = r.cfg;
  assert(v && v.host === 'sites.corp.internal' && v.oidc.adminEmails === 'platform@corp,alex@example.com' && v.oidc.issuer === 'https://keycloak.corp:8443/realms/main', 'host and OIDC fields normalized');
  assert(v.certificates.issuer === 'internal-ca' && v.storage.endpoint === 'https://minio.minio.svc:9000' && v.storage.region === 'auto' && v.storage.bucket === 'My_Bucket', 'existing issuer and bucket values');
  assert(v.postgres.database === 'simple-host' && v.postgres.user === 'sh_owner' && v.trustedProxyCIDRs === '10.0.0.0/8,fd00::/8', 'database role and trusted proxy values');

  // Hostile input is refused and never reaches a file.
  const hostile = [['dbHost', 'a;id'], ['dbHost', '$(id)'], ['dbHost', 'x"y'], ['dbHost', 'a b'], ['dbHost', 'pg:99999'], ['host', 'x.com;id'], ['host', '10.0.0.5'], ['host', 'sites.example.com:8443'],
    ['admins', 'a@x.com`id`'], ['admins', 'a$(id)@x.com'], ['issuer', 'https://x.okta.com/"a'], ['issuer', 'http://x.okta.com'], ['endpoint', 'https://x" y'], ['endpoint', 'http://minio:9000'],
    ['bucketName', 'a;b'], ['region', 'us east'], ['dbName', 'x;drop'], ['dbUser', "o'x"], ['issuerName', 'a b'], ['proxies', 'not a range'], ['clientId', 'a"b']];
  for (const [k, v] of hostile) {
    r = await diy(browser, { [k]: v });
    assert(!r.cfg && r.bad.includes(k), `${k} ${JSON.stringify(v)} is refused`);
  }
  // Invisible characters (zero width, direction marks, byte order mark) are
  // refused in every field, with a plain reason; so are credentials in the
  // Postgres host and an IPv6 host:port without brackets.
  const INVIS = /invisible character/;
  for (const [k, v] of [['dbHost', 'pg\u200b.db'], ['admins', 'a@x.com\u202e'], ['clientId', 'c\ufeffid'], ['issuer', 'https://acme.okta.com\u2060'], ['bucketName', 'b\u200dx'], ['host', 'sites.acme\u200c.com']]) {
    const page = await browser.newPage();
    await page.goto(base + '/setup?product=enterprise');
    await next(page);
    await page.fill('#f-' + k, v);
    await click(page, 'Show my files');
    await page.waitForSelector('.bad');
    assert(await page.locator('#files').count() === 0 && INVIS.test(await page.locator('#f-' + k).locator('xpath=..').innerText()), `${k} with an invisible character is refused, saying why`);
    await page.close();
  }
  r = await diy(browser, { dbHost: 'fd00::1:5432' });
  assert(!r.cfg && r.bad.includes('dbHost'), 'an IPv6 host:port without brackets is refused');
  r = await diy(browser, { dbHost: 'postgres://sh:secret@pg.db:5432/simplehost' });
  assert(!r.cfg && r.bad.includes('dbHost'), 'a Postgres host with a user and password in it is refused');
  for (const [v, hint] of [['fd00::1:5432', /\[fd00::1\]:5432/], ['sh:secret@pg.db', /hostname only/]]) {
    const page = await browser.newPage();
    await page.goto(base + '/setup?product=enterprise');
    await next(page);
    await page.fill('#f-dbHost', v);
    await click(page, 'Show my files');
    await page.waitForSelector('#f-dbHost.bad');
    assert(hint.test(await page.locator('#f-dbHost').locator('xpath=..').innerText()), `Postgres host ${v}: the hint says ${hint}`);
    await page.close();
  }
  r = await diy(browser, { issuer: 'HTTPS://Acme.Okta.COM/' });
  assert(r.cfg && r.cfg.oidc.issuer === 'https://acme.okta.com', 'an issuer in capitals is accepted and written in lower case');
  {
    const page = await browser.newPage();
    await page.goto(base + '/setup?product=enterprise&cloud=aws');
    await next(page);
    await page.fill('#f-issuer', 'HTTPS://Acme.Okta.COM/');
    await page.fill('#f-host', 'sites.acme\u200b.com');
    await click(page, 'Show my files');
    await page.waitForSelector('#f-host.bad');
    assert(await page.locator('#f-issuer.bad').count() === 0, 'old cloud URL: an issuer in capitals is accepted');
    assert(INVIS.test(await page.locator('#app').innerText()), 'old cloud URL: an invisible character in the address is refused');
    await page.fill('#f-host', 'sites.acme.com');
    await click(page, 'Show my files');
    await page.waitForSelector('#files');
    assert(yaml(await page.locator('[data-file="values.yaml"] pre').textContent()).enterprise.oidc.issuer === 'https://acme.okta.com', 'old cloud URL: the issuer is written in lower case');
    await page.close();
  }
  {
    const page = await browser.newPage();
    await page.goto(base + '/setup?product=enterprise');
    await page.locator('label.choice:has(input[name=mode][value=advanced])').click();
    await next(page);
    const err = await page.evaluate(() => window.shSetup.ready().then(() => window.shSetup.describe('SESSION_TTL', '8h\u200b')));
    assert(err && INVIS.test(err.error), 'an Advanced value with an invisible character is refused');
    await page.close();
  }

  // The small box: its public names and the command.
  const small = async fields => {
    const page = await browser.newPage();
    await page.goto(base + '/setup?product=small-box');
    await next(page);
    for (const [k, v] of Object.entries(fields)) await page.fill('#f-' + k, v);
    await click(page, 'Show my files');
    await page.waitForSelector('#files, .bad', { timeout: 20000 });
    const out = { bad: await page.locator('.bad').evaluateAll(els => els.map(e => e.id.slice(2))), cmd: await page.locator('#files pre').count() ? await page.locator('#files pre').first().textContent() : '' };
    await page.close();
    return out;
  };
  r = await small({ domain: 'HTTPS://Hack.Example.com./', email: 'ops@corp' });
  assert(/ --host hack\.example\.com --content sites\.hack\.example\.com --email ops@corp$/.test(r.cmd) && parses(r.cmd), 'small box: a domain typed as a URL is tidied; an internal email is accepted');
  {
    const page = await browser.newPage();
    await page.goto(base + '/setup?product=small-box');
    await next(page);
    await page.fill('#f-content', 'sites.hack.example.com');
    await click(page, 'Show my files');
    await page.waitForSelector('#f-content.bad');
    assert(/goes with a domain/.test(await page.locator('#f-content').locator('xpath=..').innerText()), 'small box: a sites hostname without a domain says why instead of being dropped');
    await page.close();
  }
  for (const [k, v] of [['domain', 'hack.example.com\u200b'], ['domain', '$(id).com'], ['domain', 'hack.example.com:8443'], ['domain', '203.0.113.5'], ['domain', 'localhost'], ['content', 'x y'], ['email', 'a;id@x'], ['email', "a'b@x`id`"]]) {
    r = await small({ domain: k === 'domain' ? v : 'hack.example.com', [k]: v });
    assert(!r.cmd && r.bad.includes(k), `small box: ${k} ${JSON.stringify(v)} is refused`);
  }
  await browser.close();
  console.log('all ok');
})().catch(e => { console.error(e); process.exit(1); });
