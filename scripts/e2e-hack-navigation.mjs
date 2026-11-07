#!/usr/bin/env node
// HACK_NAVIGATION_FIXTURE=/tmp/slice1-fixture.json TEST_PKGS=./internal/hackhttp ... scripts/test-integration.sh -run TestServeHackNavigationBrowser
// PLAYWRIGHT_DIR=/tmp/tsx/node_modules node scripts/e2e-hack-navigation.mjs /tmp/slice1-fixture.json
import fs from 'node:fs'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
const require = createRequire('/tmp/tsx/node_modules/')
const { chromium } = require('playwright')
const info = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'))
const map = JSON.parse(fs.readFileSync('internal/rules/hackevents/navigation.json', 'utf8'))
const shots = process.env.NAVIGATION_SHOTS || '.shots/slice1'
fs.mkdirSync(shots, { recursive: true })
const browser = await chromium.launch({ executablePath: '/usr/local/bin/chromium', headless: true, args: ['--no-sandbox'] })
const errors = []
async function person(role, width = 1280) {
  const context = await browser.newContext({ viewport: { width, height: width === 390 ? 844 : 800 } })
  await context.addInitScript(key => { localStorage.setItem('apiKey', key) }, info.keys[role])
  const page = await context.newPage()
  page.on('pageerror', error => errors.push(error.message))
  // Serve the current source modules while the DB fixture stays alive between iterations.
  await page.route('**/hack/**', async route => {
    const path = new URL(route.request().url()).pathname
    const file = 'internal/webassets/static' + path
    if (!fs.existsSync(file)) return route.continue()
    await route.fulfill({ body: fs.readFileSync(file), contentType: path.endsWith('.css') ? 'text/css' : 'text/javascript' })
  })
  // Tours are unrelated to destination assertions; their real source remains bundled.
  await page.route('**/v1/hack/preferences', route => route.fulfill({ json: { organiser_walkthrough_done: true, judge_walkthrough_done: true } }))
  return { context, page }
}
async function open(page, path, control = 'main h1') {
  await page.goto(info.url + path)
  await page.locator(control).first().waitFor({ state: 'visible' })
  const failure = page.getByText('Something went wrong', { exact: false })
  assert.equal(await failure.count(), 0, 'screen failed at ' + path)
}
const control = {
 'o.tracks': '#track-add', 'o.team-rules': '#ts-size', 'o.signup': '#signup-approval',
 'o.organisers': '#coorg-create', 'o.deadline': '#deadline-input', 'o.entry-rules': '#rules-save',
 'o.reminders': '#rem-chips', 'o.rubric': '#jg-rubric-save', 'o.tie-break': '#jg-tie',
 'o.judging-mode': '#jg-mode-save', 'o.judging-closes': '#jg-closes-input',
 'o.result-rules': '#res-score-mode', 'o.voting': '#res-vote-enabled', 'o.directory': '#res-directory-listed',
 'o.notifications': 'main h2', 'o.details': '#set-name', 'o.leave': '#leave-open', 'o.delete': '#delete-open',
 'o.basics': '#f-title', 'o.schedule': '#sched-add', 'o.sponsors': '#sp-add', 'o.faq': '#fq-add',
 'o.look': '#look-preview', 'o.website': '#addr-copy', 'o.announcements': 'main h1'
}
try {
 for (const role of ['organiser', 'judge', 'participant', 'multi', 'new']) {
  const { context, page } = await person(role)
  await open(page, '/signin')
  const destination = ['multi', 'new'].includes(role) ? '/events' : `/e/${info.slug}/${{ organiser: 'manage', judge: 'judge', participant: 'team' }[role]}`
  await page.waitForURL(u => u.pathname === destination)
  if (role !== 'new') await page.locator('.side .pane-event').first().waitFor()
  if (role === 'multi') {
   assert.equal(await page.locator('.side .past-events[open]').count(), 0)
   await page.locator(`.side a.pane-event[href*="${info.multi_slug}"]`).click()
   await page.waitForURL(u => u.pathname.includes(info.multi_slug))
   await page.locator('.event-sections').waitFor(); assert.equal(await page.locator('.event-sections').count(), 1)
  }
  await open(page, '/signin?next=%2Fevents')
  await page.waitForURL(u => u.pathname === '/events')
  if (role === 'organiser') {
   for (const next of ['/account', '/join/navigation-code', '/judge/navigation-code', '/organiser/navigation-code', '/team/navigation-code']) {
    await page.goto(info.url + '/signin?next=' + encodeURIComponent(next))
    await page.waitForURL(u => u.pathname === next)
   }
  }
  await context.close()
  console.log('ok landing and pane:', role)
 }
 const { context, page } = await person('organiser')
 const manage = `/e/${info.slug}/manage`
 for (const a of map.aliases) {
  const query = new URLSearchParams({ s: a.section, keep: 'unchanged' })
  if (a.tab) query.set('tab', a.tab)
  const target = map.targets.find(t => t.id === a.target)
  // Some legacy aliases select an editor with an internally named control.
  const selector = ['o.schedule','o.sponsors','o.faq'].includes(a.target) ? 'main .sp h2' : control[a.target] || 'main h2'
  await open(page, manage + '?' + query + (a.anchor ? '#' + a.anchor : ''), selector)
  const url = new URL(page.url())
  assert.equal(url.searchParams.get('s'), target.section, JSON.stringify(a))
  assert.equal(url.searchParams.get('tab'), target.tab, JSON.stringify(a))
  assert.equal(url.hash, target.anchor ? '#' + target.anchor : '', JSON.stringify(a))
  assert.equal(url.searchParams.get('keep'), 'unchanged')
 }
 console.log('ok all', map.aliases.length, 'aliases')
 await open(page, manage + '?s=settings&tab=judging#set-name', '#jg-rubric-save')
 assert.equal(new URL(page.url()).searchParams.get('tab'), 'judging')
 await open(page, manage + '?s=overview', '#ov-change-stage')
 await page.getByRole('link', { name: 'Score as a judge', exact: true }).click()
 await page.waitForURL(u => u.pathname.endsWith('/judge') && u.searchParams.get('s') === 'home')
 await page.getByRole('link', { name: 'Manage event', exact: true }).waitFor()
 assert.equal(await page.getByText('You know someone on every team').count(), 0)
 await open(page, `/e/${info.slug}/judge?s=queue&tab=conflicts#conflicts`, '#conflicts')
 assert.equal(await page.locator('[data-tab="conflicts"]').getAttribute('aria-current'), 'page')
 assert.equal(await page.locator('[data-declare-conflict]').count(), 2, 'another judge conflict leaked into organiser review')
 await open(page, `/e/${info.slug}/judge?s=queue`, '[data-panel="to-score"] a[href*="s=scoring"]')
 await page.locator('[data-panel="to-score"] a[href*="s=scoring"]').first().click()
 await page.locator('#jsc-conflict').waitFor()
 assert.ok(new URL(page.url()).pathname.endsWith('/judge'))
 await context.close()
 console.log('ok organiser judge Home, own conflicts, queue and scoring')
 const editor = await person('multi')
 const settings = `/e/${info.multi_slug}/manage?s=settings&tab=`
 await open(editor.page, settings + 'teams', '#ts-size')
 const newSize = (await editor.page.locator('#ts-size').inputValue()) === '5' ? '6' : '5'
 await editor.page.locator('#ts-size').fill(newSize)
 await editor.page.locator('#ts-save').click()
 await editor.page.waitForFunction(() => document.querySelector('#ts-save')?.disabled)
 assert.equal(await editor.page.locator('#ts-size').inputValue(), newSize)
 await open(editor.page, settings + 'entries', '#rules-save')
 await editor.page.locator('input[name="org-preset"][value="video_website_code"]').check()
 await editor.page.locator('#rules-save').click()
 await editor.page.waitForFunction(() => document.querySelector('.live')?.textContent?.includes('Saved') || document.querySelector('[role="status"]')?.textContent?.includes('Saved'))
 await open(editor.page, settings + 'judging', '#jg-rubric-save')
 const rubricSave = editor.page.waitForResponse(r => r.request().method() === 'PUT' && r.url().endsWith('/rubric'))
 await editor.page.locator('#jg-rubric-save').click()
 assert.equal((await rubricSave).status(), 200, 'rubric save failed')
 await open(editor.page, settings + 'details', '#set-name')
 await editor.page.locator('#set-name').fill('Unsaved name')
 await Promise.all([editor.page.waitForResponse(r => r.url().endsWith('/v1/hack/events')), editor.page.evaluate(() => window.dispatchEvent(new Event('focus')))])
 assert.equal(await editor.page.locator('#set-name').inputValue(), 'Unsaved name', 'focus refresh erased a draft')
 await open(editor.page, `/e/${info.multi_slug}/manage?s=event-page&tab=tracks`, '#track-add')
 await editor.page.locator('#track-add').click()
 await editor.page.locator('#trk-name').fill('Navigation track')
 await editor.page.locator('.dialog .acts .btn.primary').click()
 await editor.page.getByText('Navigation track', { exact: true }).waitFor()
 await editor.page.locator('.subnav a[href*="tab=about"]').click()
 await editor.page.locator('#f-about').waitFor()
 await editor.page.locator('.subnav a[href*="tab=tracks"]').click()
 await editor.page.getByText('Navigation track', { exact: true }).waitFor()
 await editor.page.route('**/v1/hack/events/*/teams', async route => { await new Promise(resolve => setTimeout(resolve, 250)); await route.continue() })
 await editor.page.locator('.subnav a[href*="tab=about"]').click()
 const teamsRead = editor.page.waitForResponse(r => /\/teams$/.test(r.url()))
 await editor.page.locator('.subnav a[href*="tab=tracks"]').click()
 await editor.page.locator('.subnav a[href*="tab=about"]').click()
 await (await teamsRead).finished()
 await editor.page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
 assert.equal(await editor.page.locator('#f-about').count(), 1, 'late Tracks load replaced About')
 await editor.context.close()
 console.log('ok moved save handlers, Tracks tab lifetime and focus draft preservation')
 for (const width of [1280, 390]) {
  const org = await person('organiser', width)
  const capture = async (path, name, selector) => { await open(org.page, path, selector); await org.page.screenshot({ path: `${shots}/${name}-${width}.png` }) }
  await capture(manage + '?s=overview', 'organiser-home', '#ov-change-stage')
  await capture(manage + '?s=settings&tab=judging', 'settings-judging', '#jg-rubric-save')
  if (width === 390) {
   await org.page.getByLabel('Settings section', { exact: true }).selectOption('entries')
   await org.page.locator('#rules-save').waitFor()
   assert.equal(new URL(org.page.url()).searchParams.get('tab'), 'entries')
  }
  await capture(manage + '?s=event-page&tab=tracks', 'event-page-tracks', '#track-add')
  await capture(`/e/${info.slug}/judge?s=home`, 'organiser-judge-home', '#jh-body h2')
  await org.context.close()
  const multi = await person('multi', width)
  await open(multi.page, `/e/${info.multi_slug}/manage?s=overview`, '#ov-change-stage')
  if (width === 390) await multi.page.locator('[data-menu]').first().click()
  await multi.page.locator('.side .past-events summary').click()
  await multi.page.locator('.side .past-events a').last().scrollIntoViewIfNeeded()
  await multi.page.screenshot({ path: `${shots}/pane-past-open-${width}.png` })
  await open(multi.page, '/events', 'main h1')
  await multi.page.screenshot({ path: `${shots}/events-${width}.png` })
  await multi.context.close()
  const participant = await person('participant', width)
  await open(participant.page, `/e/${info.slug}/team?s=home`, 'main h1')
  await participant.page.screenshot({ path: `${shots}/participant-home-${width}.png` })
  await participant.context.close()
 }
 assert.deepEqual(errors, [])
 console.log('ok responsive select; 14 screenshots;', errors.length, 'browser errors')
 if (!process.env.KEEP_NAVIGATION_FIXTURE) await fetch(info.url + '/_fixture/stop', { method: 'POST' })
} finally { await browser.close() }
