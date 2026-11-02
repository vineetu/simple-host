// Browser half of kind-check.sh: headless Chromium against the port-forwarded
// Service, with every *.hack.test name resolved to 127.0.0.1 by Chrome itself
// (no /etc/hosts). Checks the platform loads, a participant can sign in with
// the issued key and sees the event, the event page renders on its own host,
// and the published team site renders at its own origin <team>.<event>.<host>.
//
// Environment: PLAYWRIGHT_CHROME, PORT, HOST, SLUG, TEAM, PARTICIPANT_KEY.
import { createRequire } from 'node:module';
import assert from 'node:assert/strict';
const require = createRequire(process.env.PLAYWRIGHT_REQUIRE_FROM || '/tmp/tsx/node_modules/');
const { chromium } = require('playwright');

const { PLAYWRIGHT_CHROME, PORT, HOST, SLUG, TEAM, PARTICIPANT_KEY } = process.env;
for (const [k, v] of Object.entries({ PLAYWRIGHT_CHROME, PORT, HOST, SLUG, TEAM, PARTICIPANT_KEY })) {
  assert.ok(v, `${k} is required`);
}
const origin = `http://${HOST}:${PORT}`;
const browser = await chromium.launch({
  executablePath: PLAYWRIGHT_CHROME,
  args: [`--host-resolver-rules=MAP ${HOST} 127.0.0.1, MAP *.${HOST} 127.0.0.1`],
});
const results = [];
const check = (name, ok, detail = '') => { results.push([name, ok, detail]); if (!ok) process.exitCode = 1; };
try {
  const page = await browser.newPage();
  const home = await page.goto(`${origin}/`, { waitUntil: 'domcontentloaded' });
  check('home page loads', home.status() === 200, `status ${home.status()}`);
  const title = await page.title();
  check('home page is Simple Hack', /hack/i.test(title) || /hack/i.test(await page.content()), title);

  // The sign-in the website itself uses: the API key in localStorage.
  await page.evaluate((key) => localStorage.setItem('apiKey', key), PARTICIPANT_KEY);
  const me = await page.evaluate(async () => {
    const r = await fetch('/v1/me', { headers: { 'X-API-Key': localStorage.getItem('apiKey') } });
    return { status: r.status, body: await r.json().catch(() => null) };
  });
  check('participant signed in (/v1/me)', me.status === 200, `status ${me.status}`);
  const events = await page.evaluate(async () => {
    const r = await fetch('/v1/hack/events', { headers: { 'X-API-Key': localStorage.getItem('apiKey') } });
    return r.json();
  });
  check('participant sees the event', Array.isArray(events) && events.some((e) => e.slug === SLUG), JSON.stringify(events).slice(0, 120));
  // The signed-in app (the dashboard in Hack mode) lists the person's events.
  await page.goto(`${origin}/dashboard`, { waitUntil: 'networkidle' });
  await page.waitForFunction(() => /Kind check/i.test(document.body.innerText), null, { timeout: 15000 }).catch(() => {});
  const signedIn = await page.evaluate(() => document.body.innerText);
  check('signed-in dashboard shows the event', /Kind check/i.test(signedIn), signedIn.slice(0, 80).replace(/\s+/g, ' '));

  const eventPage = await page.goto(`http://${SLUG}.${HOST}:${PORT}/`, { waitUntil: 'domcontentloaded' });
  check('event page on its own host', eventPage.status() === 200 && /Kind check/.test(await page.content()), `status ${eventPage.status()}`);

  const site = await page.goto(`http://${TEAM}.${SLUG}.${HOST}:${PORT}/`, { waitUntil: 'domcontentloaded' });
  const h1 = await page.locator('h1').first().textContent().catch(() => '');
  check('team site renders', site.status() === 200 && h1 === 'Published from Kubernetes', `status ${site.status()}, h1 ${JSON.stringify(h1)}`);
  await page.screenshot({ path: process.env.SCREENSHOT || '/tmp/kind-check-team-site.png' });
} finally {
  await browser.close();
}
for (const [name, ok, detail] of results) console.log(`  ${ok ? 'ok  ' : 'FAIL'} ${name}${detail ? `  (${detail})` : ''}`);
