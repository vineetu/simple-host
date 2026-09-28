#!/usr/bin/env node
// Browser end-to-end check of the setup helper's quick path for Enterprise on
// a cloud (static/setup/setup.js, CLOUDS): Enterprise → Where will it run? →
// AWS → cluster yes/no → a few questions → one line for the cloud's
// browser shell.
//
// Proves: ?product=enterprise&cloud=aws preselects both answers, and Azure
// (switched off) is neither shown nor taken from ?cloud=; the cloud path
// hides the Basic/Advanced question and "Something else" brings it back; the
// Basics step asks only address, admins, issuer, client ID, region (preselected)
// and, for an existing cluster, its name; company email domains appear only
// with Google (prefilled from the admins) and are required; the output line is
// one line that fetches deploy/terraform/<cloud>/apply.sh at ENT_CLOUD_REF,
// checks ENT_APPLY_SHA256, and carries --tfvars whose base64 decodes to exactly
// the terraform.tfvars shown; "More settings" adds extra_config; the
// assistant's view (window.shSetup) offers and applies the new basic answers;
// no horizontal scroll at 390 px. Screenshots of every quick-path screen at
// 390 and 1280 px, light and dark, go to <shots-dir>.
//
// Needs Playwright (NODE_PATH pointing at a node_modules that has it) and a
// Chromium, and a server serving /setup (any of them: the page makes no
// request on this path except the optional check).
//   NODE_PATH=/opt/pw/node_modules node scripts/e2e-setup-cloud.js <base> <shots-dir>
const { chromium } = require('playwright');
const base = process.argv[2], shots = process.argv[3];
const assert = (c, m) => { if (!c) { console.error('FAIL: ' + m); process.exit(1); } else console.log('ok: ' + m); };

async function top(page) { await page.evaluate(() => window.scrollTo(0, 0)); }

async function walk(browser, cloud, have, width, scheme) {
  const ctx = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: scheme, timezoneId: 'America/Los_Angeles' });
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push(String(e)));
  const tag = `${cloud}-${have}-${width}-${scheme}`;
  await page.goto(`${base}/setup?product=enterprise&cloud=${cloud}`);
  assert(await page.locator('input[name=product][value=ent]').isChecked(), `${tag}: Enterprise preselected`);
  assert(await page.locator(`input[name=cloud][value=${cloud}]`).isChecked(), `${tag}: cloud preselected`);
  assert(await page.locator('input[name=mode]').count() === 0, `${tag}: no Basic/Advanced question on the quick path`);
  await page.locator(`label.choice:has(input[name=cluster][value=${have}])`).click();
  await top(page);
  await page.screenshot({ path: `${shots}/cloud-1-choose-${tag}.png`, fullPage: true });
  await page.getByRole('button', { name: 'Next' }).click();
  await page.waitForSelector('#f-host');

  const fields = await page.locator('#app input[type=text], #app input[type=email], #app select').evaluateAll(els => els.map(e => e.id));
  const want = ['f-host', 'f-admins', 'f-issuer', 'f-clientId', 'f-cloudRegion'].concat(have === 'yes' ? ['f-clusterName'] : []);
  assert(JSON.stringify(fields) === JSON.stringify(want), `${tag}: Basics asks only ${want.join(', ')} (got ${fields.join(', ')})`);
  const region = await page.locator('#f-cloudRegion').inputValue();
  assert(region === 'us-west-2', `${tag}: region preselected from the time zone (${region})`);
  const questions = 2 + want.length;
  assert(questions <= 8, `${tag}: ${questions} questions in all`);

  await page.fill('#f-host', 'sites.acme-sites.com');
  await page.fill('#f-admins', 'platform@acme.com, alex@acme.com');
  // Google in any spelling the server would treat as Google.
  await page.fill('#f-issuer', 'https://ACCOUNTS.google.com:443/');
  await page.locator('#f-issuer').dispatchEvent('input');
  assert(await page.locator('#f-domains').count() === 1, `${tag}: Google shows company email domains`);
  assert(await page.locator('#f-domains').inputValue() === 'acme.com', `${tag}: domains prefilled from the admins`);
  await page.fill('#f-domains', '');
  // Terraform template sequences must reach the tfvars escaped.
  await page.fill('#f-clientId', '1234-abc${x}%{y}.apps.googleusercontent.com');
  if (have === 'yes') await page.fill('#f-clusterName', 'prod-cluster');
  await page.getByRole('button', { name: 'Show my commands' }).click();
  assert(await page.locator('#f-domains.bad').count() === 1, `${tag}: Google without domains is refused`);
  await page.fill('#f-domains', 'acme.com');
  await top(page);
  await page.screenshot({ path: `${shots}/cloud-2-basics-${tag}.png`, fullPage: true });
  await page.getByRole('button', { name: 'Show my commands' }).click();
  await page.waitForSelector('#commands');

  const pres = await page.locator('pre').allTextContents();
  const cmd = pres[0], tfvars = pres[pres.length - 1];
  assert(!cmd.includes('\n'), `${tag}: the command is one line`);
  const m = /^f=\$\(mktemp\) && curl -fsSL https:\/\/raw\.githubusercontent\.com\/vineetu\/simple-host-enterprise\/(\S+)\/deploy\/terraform\/(\w+)\/apply\.sh -o "\$f" && printf '%s  %s\\n' (\S+) "\$f" \| sha256sum -c --quiet - && bash "\$f" --ref (\S+) --tfvars ([A-Za-z0-9+\/=]+)$/.exec(cmd);
  assert(m && m[2] === cloud && m[1] === m[4], `${tag}: command fetches apply.sh for ${cloud} at the pinned commit and checks its sha256`);
  const decoded = Buffer.from(m[5], 'base64').toString('utf8');
  assert(decoded === tfvars, `${tag}: --tfvars is exactly the terraform.tfvars shown`);
  for (const line of [`create_cluster        = ${have === 'no'}`, `region                = "${region}"`, 'base_domain           = "sites.acme-sites.com"',
    'admin_emails          = ["platform@acme.com", "alex@acme.com"]', 'allowed_email_domains = ["acme.com"]', 'oidc_issuer           = "https://accounts.google.com"']) {
    assert(tfvars.includes(line + '\n'), `${tag}: tfvars has ${line}`);
  }
  assert(tfvars.includes('cluster_name') === (have === 'yes'), `${tag}: cluster_name only for an existing cluster`);
  assert(!/secret\s*=/.test(tfvars), `${tag}: no secret in tfvars`);
  assert(tfvars.includes('oidc_client_id        = "1234-abc$${x}%%{y}.apps.googleusercontent.com"\n'), `${tag}: \${ and %{ are escaped for Terraform`);
  try { require('child_process').execFileSync('bash', ['-n'], { input: cmd }); assert(true, `${tag}: the line parses in bash`); }
  catch (e) { assert(false, `${tag}: the line parses in bash`); }
  const details = page.locator('details#other-ways');
  assert(await details.count() === 1 && !(await details.evaluate(d => d.open)), `${tag}: the AI agent and Terraform blocks sit in one closed "Other ways to run this"`);
  const order = await page.locator('#app > .card h2, #app > details summary b').allTextContents();
  assert(order.indexOf('What you chose') >= 0 && order.indexOf('What you chose') < order.indexOf('Other ways to run this'), `${tag}: "What you chose" comes before the other ways`);
  const dns = await page.locator('#commands ol.steps').nth(1).innerText();
  assert(/domain you already manage/.test(dns) && /name servers at your registrar/.test(dns) && /already a Route 53 zone/.test(dns), `${tag}: the DNS step covers a subdomain, a domain of its own and an existing zone`);
  const agent = pres.find(p => p.startsWith('# Set up Simple Host Enterprise on'));
  assert(agent && agent.includes(cmd) && agent.includes('--plan') && agent.includes('TF_VAR_oidc_client_secret'), `${tag}: agent block has the line, --plan first, the secret from the terminal`);
  if (width === 390) {
    const sw = await page.evaluate(() => document.documentElement.scrollWidth);
    assert(sw <= 390, `${tag}: no horizontal scroll (${sw})`);
  }
  await top(page);
  await page.screenshot({ path: `${shots}/cloud-3-commands-${tag}.png`, fullPage: true });
  assert(errors.length === 0, `${tag}: no page errors ${errors.join(' ')}`);
  await ctx.close();
}

(async () => {
  const browser = await chromium.launch();
  for (const have of ['no', 'yes']) for (const scheme of ['light', 'dark']) for (const width of [390, 1280]) await walk(browser, 'aws', have, width, scheme);

  // The issuer is checked as typed, before any normalising.
  {
    const p2 = await browser.newPage();
    await p2.goto(`${base}/setup?product=enterprise&cloud=aws`);
    await p2.getByRole('button', { name: 'Next' }).click();
    await p2.waitForSelector('#f-host');
    await p2.fill('#f-host', 'sites.acme-sites.com');
    await p2.fill('#f-admins', 'platform@acme.com');
    await p2.fill('#f-clientId', '0oa123');
    const refused = [['a space', 'https://acme.okta.com/oauth2 default'], ['a tab', 'https://acme.okta.com/oauth2\tdefault'],
      ['a quote', 'https://x.okta.com/a"b'], ['a backslash', 'https://x.okta.com/a\\b'], ['user:password@', 'https://user:pw@acme.okta.com'],
      ['a query', 'https://acme.okta.com/?x=1'], ['Google with a trailing dot', 'https://accounts.google.com.'], ['Google with a path', 'https://accounts.google.com/o/oauth2'],
      ['Google on another port', 'https://accounts.google.com:8443']];
    for (const [what, v] of refused) {
      await p2.locator('#f-issuer').evaluate((el, val) => { el.value = val; el.dispatchEvent(new Event('input')); }, v);
      if (await p2.locator('#f-domains').count()) await p2.fill('#f-domains', 'acme.com');
      await p2.getByRole('button', { name: 'Show my commands' }).click();
      assert(await p2.locator('#f-issuer.bad').count() === 1 && await p2.locator('#commands').count() === 0, `issuer with ${what} is refused`);
    }
    // A newline cannot reach the field (the browser drops it from a one-line input); what is kept is one line.
    await p2.locator('#f-issuer').evaluate(el => { el.value = 'https://acme.okta.com/a\nb'; el.dispatchEvent(new Event('input')); });
    assert(!(await p2.locator('#f-issuer').inputValue()).includes('\n'), 'a newline never reaches the issuer');
    await p2.close();
  }

  // Azure is not offered yet: no choice, and ?cloud=azure is ignored.
  const page = await browser.newPage();
  await page.goto(`${base}/setup?product=enterprise&cloud=azure`);
  assert(await page.locator('input[name=cloud]').count() === 2 && await page.locator('input[name=cloud][value=azure]').count() === 0, 'only AWS and Something else are offered');
  assert(await page.locator('input[name=cloud][value=diy]').isChecked(), '?cloud=azure falls back to Something else');

  // Something else keeps the file path, with its Basic/Advanced question.
  assert(await page.locator('input[name=mode]').count() === 2, 'Something else asks Basic/Advanced');
  await page.locator('label.choice:has(input[name=cloud][value=aws])').click();
  assert(await page.locator('input[name=mode]').count() === 0 && await page.locator('input[name=cluster]').count() === 2, 'AWS asks about the cluster instead');
  await page.locator('label.choice:has(input[name=cloud][value=diy])').click();
  assert(await page.locator('input[name=mode]').count() === 2, 'back to Something else');

  // The assistant's view: the new answers are offered and applied like a click.
  let ctx = await page.evaluate(() => window.shSetup.ready().then(() => window.shSetup.context()));
  assert(ctx.basics.cloud === 'diy' && !('cloudRegion' in ctx.basics) && 'bucket' in ctx.basics, 'context: Something else sends the file questions, not the cloud ones');
  assert(await page.evaluate(() => window.shSetup.applyBasic('cloud', 'azure')) !== '', 'assistant cannot pick Azure');
  const r = await page.evaluate(() => { const e = window.shSetup.applyBasic('cloud', 'aws'); window.shSetup.refresh(['cloud']); return e; });
  assert(r === '' && await page.locator('input[name=cloud][value=aws]').isChecked(), 'assistant applies cloud=aws');
  ctx = await page.evaluate(() => window.shSetup.context());
  assert(ctx.basics.cloud === 'aws' && ctx.basics.cluster === 'no' && /^[a-z]{2}-[a-z]+-\d$/.test(ctx.basics.cloudRegion) && !('bucket' in ctx.basics), 'context: AWS sends cluster and region, not the file questions');
  assert(await page.evaluate(() => window.shSetup.describeBasic('cloudRegion', 'westeurope')) === null, 'an Azure region is not an answer');
  assert(await page.evaluate(() => window.shSetup.applyBasic('cloudRegion', 'eu-west-1')) === '', 'assistant applies a region');
  assert(await page.evaluate(() => window.shSetup.applyBasic('cluster', 'yes')) === '', 'assistant applies cluster=yes');

  // More settings: a changed setting becomes extra_config.
  await page.evaluate(() => window.shSetup.refresh([]));
  await page.getByRole('button', { name: 'Next' }).click();
  await page.waitForSelector('#f-host');
  await page.fill('#f-host', 'sites.acme-sites.com');
  await page.fill('#f-admins', 'platform@acme.com');
  await page.fill('#f-issuer', 'https://acme.okta.com');
  await page.fill('#f-clientId', '0oa123');
  await page.fill('#f-clusterName', 'prod-eks');
  assert(await page.locator('#f-cloudRegion').inputValue() === 'eu-west-1', 'the applied region shows');
  await page.getByRole('button', { name: 'More settings (optional)' }).click();
  await page.waitForSelector('.area-head');
  assert(await page.locator('#f-PORT').count() === 0, 'settings the module sets (PORT) are not offered');
  const set = await page.evaluate(() => window.shSetup.apply('SESSION_TTL', '12h'));
  assert(set === '', 'SESSION_TTL=12h accepted');
  assert(await page.locator('#app input[type=checkbox]').count() === 0, 'no secret blanks offered on the quick path');
  await page.getByRole('button', { name: 'Skip to my commands' }).click();
  await page.waitForSelector('#commands, .finding, .checking', { timeout: 40000 });
  if (!(await page.locator('#commands').count())) {
    const show = page.getByRole('button', { name: 'Show my commands' });
    if (await show.count()) await show.click();
    await page.waitForSelector('#commands', { timeout: 40000 });
  }
  const tf = (await page.locator('pre').allTextContents()).pop();
  assert(tf.includes('extra_config = {\n  SESSION_TTL = "12h"\n}\n'), 'extra_config carries the setting');
  assert(/\ncluster_name +\= "prod-eks"\n/.test(tf) && /\nregion +\= "eu-west-1"\n/.test(tf), 'tfvars with the cluster and region');
  await browser.close();
  console.log('all ok');
})().catch(e => { console.error(e); process.exit(1); });
