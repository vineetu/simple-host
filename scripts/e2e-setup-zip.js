#!/usr/bin/env node
// Real browser download, independently checked with Python's ZIP reader.
// NODE_PATH=/opt/pw/node_modules node scripts/e2e-setup-zip.js <base> <output-directory>
const { chromium } = require('playwright');
const { execFileSync } = require('child_process');
const fs = require('fs');
const path = require('path');
const base = process.argv[2], dir = process.argv[3];
if (!base || !dir) throw new Error('Provide base and output directory');
fs.mkdirSync(dir, { recursive: true });
const assert = (ok, message) => { if (!ok) throw new Error(message); };
const choose = (page, name, value) => page.locator(`label.choice:has(input[name=${name}][value=${value}])`).click();
async function check(browser, output, db, width, scheme) {
  const tag = `${output}-${db}-${width}-${scheme}`;
  const context = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: scheme });
  const page = await context.newPage(), errors = [];
  page.on('pageerror', e => errors.push(String(e)));
  await page.goto(base + '/setup?product=enterprise');
  await choose(page, 'output', output);
  await page.getByRole('button', { name: 'Next', exact: true }).click();
  await choose(page, 'postgresMode', db);
  for (const [key, value] of Object.entries({ host: 'sites.example.com', admins: 'ops@example.com', issuer: 'https://login.example.com', clientId: 'équipe-日本語-${literal}', endpoint: 'https://objects.example.com', region: 'test-1', bucketName: 'sites', context: "team's $(do-not-run)", namespace: 'team-sites' })) await page.fill('#f-' + key, value);
  if (db === 'external') await page.fill('#f-dbHost', 'postgres.db.svc.cluster.local');
  await page.getByRole('button', { name: 'Show my files', exact: true }).click();
  await page.locator('#download-zip').waitFor();
  const details = page.locator('#files details.out, #agent details.out');
  assert(await details.count() === 4, tag + ': four file previews');
  assert(await details.evaluateAll(nodes => nodes.every(n => !n.open)), tag + ': all previews collapsed');
  const contents = await details.locator('pre').allTextContents();
  const names = ['values.yaml', 'secrets.env', 'install.sh', 'simple-host-setup.md'];
  const expected = Object.fromEntries(names.map((name, i) => [name, contents[i]]));
  let requests = 0;
  const countRequest = request => { if (/^https?:/.test(request.url())) requests++; };
  page.on('request', countRequest);
  const downloadEvent = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Download ZIP', exact: true }).click();
  const download = await downloadEvent;
  assert(download.suggestedFilename() === 'simple-host-enterprise-setup.zip', tag + ': meaningful filename');
  const zipPath = path.join(dir, tag + '.zip');
  await download.saveAs(zipPath);
  page.off('request', countRequest);
  assert(requests === 0, tag + ': download makes no network request');
  const extracted = JSON.parse(execFileSync('python3', ['-c', 'import zipfile,json,sys\nwith zipfile.ZipFile(sys.argv[1]) as z:\n assert z.testzip() is None\n assert len(z.namelist()) == 4\n assert all(i.flag_bits & 0x800 for i in z.infolist())\n print(json.dumps({n:z.read(n).decode("utf-8") for n in z.namelist()}))', zipPath], { encoding: 'utf8' }));
  assert(JSON.stringify(extracted) === JSON.stringify(expected), tag + ': all archive bytes match previews, including Unicode');
  assert(extracted['values.yaml'].includes('équipe-日本語-${literal}'), tag + ': UTF-8 and literal characters preserved');
  assert(extracted['secrets.env'].split('\n').filter(line => line && !line.startsWith('#')).every(line => /^[A-Z][A-Z0-9_]*=$/.test(line)), tag + ': every secret stays blank');
  execFileSync('bash', ['-n'], { input: extracted['install.sh'] });
  assert(extracted['install.sh'].startsWith('#!/usr/bin/env bash\n'), tag + ': install script has a Bash shebang');
  assert(extracted['simple-host-setup.md'].includes('run `bash install.sh`'), tag + ': agent instructions run the saved script');
  assert(output === 'helm' ? extracted['install.sh'].includes('helm upgrade --install') : extracted['install.sh'].includes('helm template'), tag + ': selected command retained');
  const summary = details.first().locator('summary');
  await summary.focus(); await page.keyboard.press('Enter');
  assert(await details.first().getAttribute('open') !== null, tag + ': keyboard opens preview');
  assert(await details.first().locator('pre').isVisible(), tag + ': expanded content visible');
  const singleEvent = page.waitForEvent('download');
  await details.first().getByRole('button', { name: 'Download', exact: true }).click();
  const single = await singleEvent, singlePath = path.join(dir, tag + '-values.yaml');
  await single.saveAs(singlePath);
  assert(fs.readFileSync(singlePath, 'utf8') === expected['values.yaml'], tag + ': individual download still matches');
  await summary.focus(); await page.keyboard.press('Space');
  assert(await details.first().getAttribute('open') === null, tag + ': keyboard closes preview');
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), tag + ': no horizontal overflow');
  await page.evaluate(() => { document.activeElement.blur(); window.scrollTo(0, 0); });
  await page.screenshot({ path: path.join(dir, tag + '.png'), fullPage: true });
  assert(errors.length === 0, tag + ': no script errors');
  await context.close();
  console.log('PASS ' + tag + ': ZIP integrity, exact contents, local download, keyboard previews, individual download and layout');
}
(async () => {
  const browser = await chromium.launch();
  try {
    const cases = process.env.SH_SETUP_ZIP_FOCUSED === '1'
      ? [['helm', 'external', 390, 'dark'], ['yaml', 'incluster', 1280, 'light']]
      : ['helm', 'yaml'].flatMap(output => ['external', 'incluster'].flatMap(db => [320, 390, 1280].flatMap(width => ['light', 'dark'].map(scheme => [output, db, width, scheme]))));
    for (const scenario of cases) await check(browser, ...scenario);
  } finally { await browser.close(); }
  console.log('ALL OK');
})().catch(e => { console.error(e); process.exit(1); });
