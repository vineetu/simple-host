#!/usr/bin/env node
// Existing-cluster installation regression (keeps the old script name).
// Browser-generated values are rendered by the actual Enterprise Helm chart.
// NODE_PATH=/opt/pw/node_modules SH_ENTERPRISE_CHART=/path/to/chart node scripts/e2e-setup-cloud.js <base> <shots-dir>
const { chromium } = require('playwright');
const { execFileSync } = require('child_process');
const fs = require('fs');
const path = require('path');
const base = process.argv[2], shots = process.argv[3];
const chart = process.env.SH_ENTERPRISE_CHART;
if (!base || !shots || !chart) throw new Error('Provide base, shots directory and SH_ENTERPRISE_CHART');
fs.mkdirSync(shots, { recursive: true });
const assert = (v, m) => { if (!v) throw new Error(m); console.log('ok: ' + m); };
const yaml = (s, all = false) => JSON.parse(execFileSync('python3', ['-c', 'import sys,yaml,json;json.dump(' + (all ? 'list(filter(None,yaml.safe_load_all(sys.stdin)))' : 'yaml.safe_load(sys.stdin)') + ',sys.stdout)'], { input: s, encoding: 'utf8' }));
const get = (v, p) => p.split('.').reduce((a, k) => a && a[k], v);
const clickChoice = (p, n, v) => p.locator(`label.choice:has(input[name=${n}][value=${v}])`).click();
const fakeCA = path.join(shots, 'render-only-db-ca.crt');
fs.writeFileSync(fakeCA, 'render-only CA placeholder\n');

function render(values, tag) {
  const file = path.join(shots, `${tag}-values.yaml`);
  fs.writeFileSync(file, JSON.stringify(yaml(values).enterprise));
  const v = yaml(values).enterprise;
  const args = ['template', 'setup-test', chart, '-n', 'setup-test', '-f', file];
  if (v.postgres.mode === 'external') args.push('--set-file', `postgres.external.caCert=${fakeCA}`);
  const a = execFileSync('helm', args, { encoding: 'utf8' });
  const b = execFileSync('helm', args, { encoding: 'utf8' });
  assert(a === b, `${tag}: repeated rendering preserves credentials and resources`);
  const docs = yaml(a, true);
  const obj = (kind, name) => docs.find(x => x.kind === kind && x.metadata.name === name);
  assert(!obj('Secret', 'simple-host-secrets'), `${tag}: no newly generated application Secret`);
  assert(!docs.some(x => x.kind === 'ClusterIssuer'), `${tag}: existing cluster issuer reused`);
  assert(!!obj('StatefulSet', 'postgres') === (v.postgres.mode === 'incluster'), `${tag}: only chosen database is installed`);
  const cm = obj('ConfigMap', 'simple-host-config').data;
  assert(cm.SESSION_TTL === '4h' && cm.API_KEY_MAX_DAYS === '30', `${tag}: typed and extra settings reach the application`);
  assert(cm.BACKUP_STORAGE_ENDPOINT === 'https://objects.example.com' && cm.BACKUP_STORAGE_REGION === 'eu-test-1', `${tag}: existing bucket configuration rendered`);
  assert(cm.DB_HOST === (v.postgres.mode === 'external' ? 'postgres.db.svc.cluster.local' : 'postgres.setup-test.svc.cluster.local'), `${tag}: database host rendered`);
  const pod = obj('Deployment', 'simple-host').spec.template.spec;
  assert(pod.containers[0].envFrom.some(x => x.secretRef && x.secretRef.name === 'simple-host-secrets'), `${tag}: app references the persistent Secret`);
  return { docs, obj };
}

async function walk(browser, output, postgresMode, width, scheme) {
  const tag = `${output}-${postgresMode}-${width}-${scheme}`;
  const context = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: scheme });
  const page = await context.newPage(), errors = [];
  page.on('pageerror', e => errors.push(String(e)));
  await page.goto(`${base}/setup?product=enterprise&cloud=aws`);
  assert(await page.locator('input[name=cloud],input[name=cluster],#f-cloudRegion,#f-clusterName').count() === 0, `${tag}: old cloud URL cannot enable cluster creation`);
  await clickChoice(page, 'output', output);
  await page.getByRole('button', { name: 'Next', exact: true }).click();
  await clickChoice(page, 'postgresMode', postgresMode);
  const fields = { host: 'sites.acme-sites.com', admins: 'platform@example.com', issuer: 'https://login.example.com', clientId: 'client-${literal}%value', issuerName: 'company-ca',
    endpoint: 'https://objects.example.com', region: 'eu-test-1', bucketName: 'site-files', namespace: 'setup-test', context: 'company-cluster', ingressClass: 'nginx' };
  if (postgresMode === 'external') Object.assign(fields, { dbHost: 'postgres.db.svc.cluster.local', dbPort: '6432' });
  else Object.assign(fields, { dbSize: '8Gi', dbStorageClass: 'existing-storage' });
  for (const [k, v] of Object.entries(fields)) await page.fill('#f-' + k, v);
  const changed = await page.evaluate(async () => {
    await window.shSetup.ready();
    return [window.shSetup.apply('SESSION_TTL', '4h'), window.shSetup.apply('API_KEY_MAX_DAYS', '30'), window.shSetup.apply('API_KEY_DEFAULT_DAYS', '30')];
  });
  assert(changed.every(x => !x), `${tag}: Advanced settings accepted`);
  await page.getByRole('button', { name: 'Show my files', exact: true }).click();
  await page.waitForSelector('#files pre', { state: 'attached' });
  const pres = await page.locator('#files pre').allTextContents(), values = await page.locator('[data-file="values.yaml"] pre').textContent(), v = yaml(values).enterprise;
  assert(v.oidc.clientId === fields.clientId, `${tag}: literal template characters preserved in YAML`);
  assert(v.postgres.mode === postgresMode && get(v, 'secrets.existingSecret') === 'simple-host-secrets', `${tag}: selected database and persistent Secret`);
  assert(get(v, 'oidc.sessionTTL') === '4h' && get(v, 'extraConfig.API_KEY_MAX_DAYS') === '30' && !get(v, 'extraConfig.SESSION_TTL'), `${tag}: no duplicate chart-owned settings`);
  const text = pres.join('\n');
  assert(/version: "0\.2\.0"/.test(text) && /company-cluster/.test(text) && /setup-test/.test(text), `${tag}: commands carry version, context and namespace`);
  assert(!/terraform|CloudShell|create_cluster/.test(text), `${tag}: files do not provision infrastructure`);
  assert(output === 'yaml' ? /helm template/.test(text) && /kubectl.*apply/.test(text) : /helm upgrade --install|helm install/.test(text), `${tag}: selected install method`);
  if (postgresMode === 'external') assert(/--set-file enterprise.postgres.external.caCert=db-ca.crt/.test(text), `${tag}: database certificate wired`);
  assert(/SESSION_SIGNING_KEY=\n/.test(text) && /DB_APP_PASSWORD=\n/.test(text), `${tag}: browser does not invent credentials`);
  render(values, tag);
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${tag}: no horizontal overflow`);
  await page.screenshot({ path: path.join(shots, `${tag}.png`), fullPage: true });
  assert(errors.length === 0, `${tag}: no JavaScript errors: ${errors}`);
  await context.close();
}

(async () => {
  const browser = await chromium.launch();
  for (const output of ['helm', 'yaml']) for (const db of ['external', 'incluster']) for (const width of [390, 1280]) for (const scheme of ['light', 'dark']) await walk(browser, output, db, width, scheme);
  await browser.close();
  console.log('ALL OK');
})().catch(e => { console.error(e); process.exit(1); });
