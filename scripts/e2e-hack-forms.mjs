#!/usr/bin/env node
// HACK_FORMS_FIXTURE=/tmp/forms.json TEST_PKGS=./internal/hackhttp bash scripts/test-integration.sh -run '^TestServeHackFormsBrowser$' -v
// node scripts/e2e-hack-forms.mjs /tmp/forms.json
import fs from 'node:fs'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
const require = createRequire(process.env.PLAYWRIGHT_DIR || '/tmp/tsx/node_modules/')
const { chromium } = require('playwright')
const info = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'))
const browser = await chromium.launch({ executablePath: '/usr/local/bin/chromium', headless: true, args: ['--no-sandbox'] })
const errors = []
const manage = `/e/${info.slug}/manage`, team = `/e/${info.slug}/team`, base = `/v1/hack/events/${info.slug}`
async function person(role, width = 1280) {
  const context = await browser.newContext({ viewport: { width, height: 900 } })
  await context.addInitScript(key => localStorage.setItem('apiKey', key), info.keys[role])
  const page = await context.newPage()
  page.on('pageerror', e => errors.push(e.message))
  page.on('console', m => { if (m.type() === 'error' && !m.text().includes('status of')) console.error(m.text()) })
  await page.route('**/hack/**', async route => {
    const path = new URL(route.request().url()).pathname, file = 'internal/webassets/static' + path
    if (!fs.existsSync(file)) return route.continue()
    await route.fulfill({ body: fs.readFileSync(file), contentType: path.endsWith('.css') ? 'text/css' : 'text/javascript' })
  })
  await page.route('**/v1/hack/preferences', route => route.fulfill({ json: { organiser_walkthrough_done: true, judge_walkthrough_done: true } }))
  return { context, page }
}
async function open(page, path, control) {
  await page.goto(info.url + path)
  await page.locator(control).first().waitFor()
}
async function choice(page, action) {
  const d = page.getByRole('dialog', { name: 'Unsaved changes', exact: true })
  await d.waitFor()
  assert.equal(await d.getByRole('button', { name: 'Stay', exact: true }).evaluate(n => n === document.activeElement), true, 'Stay must be default')
  await d.getByRole('button', { name: action, exact: true }).click()
}
async function noErrors(page, root = 'main') {
  assert.equal(await page.locator(root + ' [aria-invalid=true]').count(), 0)
  assert.equal(await page.locator(root + ' [data-form-banner]').count(), 0)
  assert.equal(await page.locator(root + ' .err').filter({ hasText: /\S/ }).count(), 0)
}
try {
  // Reset only this disposable fixture so the suite can be rerun during development.
  async function api(method, path, body) {
    const r = await fetch(info.url + base + path, { method, headers: { 'X-API-Key': info.keys.participant, 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
    assert.ok(r.ok, `fixture reset ${path}: ${r.status}`)
    return r.json()
  }
  if ((await api('GET', '/entry')).status === 'submitted') await api('POST', '/entry/unsubmit', {})
  const rulesReset = await fetch(info.url + base + '/entry-rules', { method: 'PUT', headers: { 'X-API-Key': info.keys.organiser, 'Content-Type': 'application/json' }, body: JSON.stringify({ preset: 'video_or_website' }) })
  assert.ok(rulesReset.ok, 'fixture rules reset')
  const tracksReset = await fetch(info.url + base + '/tracks', { method: 'PUT', headers: { 'X-API-Key': info.keys.organiser, 'Content-Type': 'application/json' }, body: JSON.stringify({ tracks: [{ slug: 'design', name: 'Design' }] }) })
  assert.ok(tracksReset.ok, 'fixture tracks reset')
  await api('PUT', '/entry', { title: '', problem: '', tagline: '', audience: '', tools: [], maturity: '', video_url: '', track: '' })
  const org = await person('organiser'), p = org.page
  await open(p, manage + '?s=event-page&tab=basics', '#f-title')
  const original = await p.locator('#f-title').inputValue(), savedTitle = 'Saved through guard ' + Date.now()
  await p.locator('#f-title').fill('Keep this draft')
  assert.equal(await p.locator('.hint.dirty').innerText(), 'Unsaved changes')
  await p.locator('.subnav a[href*="tab=about"]').click()
  await choice(p, 'Stay')
  assert.equal(await p.locator('#f-title').inputValue(), 'Keep this draft')
  assert.equal(new URL(p.url()).searchParams.get('tab'), 'basics')
  // The browser warning is installed and prevents unloading a dirty form.
  assert.equal(await p.evaluate(() => { const e = new Event('beforeunload', { cancelable: true }); window.dispatchEvent(e); return e.defaultPrevented }), true)
  await p.locator('.subnav a[href*="tab=about"]').click(); await choice(p, 'Discard')
  await p.locator('#f-about').waitFor()
  await p.locator('.subnav a[href*="tab=basics"]').click(); await p.locator('#f-title').waitFor()
  assert.equal(await p.locator('#f-title').inputValue(), original)
  await p.locator('#f-title').fill(savedTitle)
  await p.locator('.subnav a[href*="tab=about"]').click(); await choice(p, 'Save')
  await p.locator('#f-about').waitFor()
  await p.locator('.subnav a[href*="tab=basics"]').click(); await p.locator('#f-title').waitFor()
  assert.equal(await p.locator('#f-title').inputValue(), savedTitle)
  // Back restores the cursor while asking; Stay preserves both history directions.
  await p.locator('#f-title').fill('Back draft')
  await p.evaluate(() => history.back()); await choice(p, 'Stay')
  assert.equal(await p.locator('#f-title').inputValue(), 'Back draft')
  await p.evaluate(() => history.back()); await choice(p, 'Discard')
  await p.locator('#f-about').waitFor()
  await p.evaluate(() => history.forward()); await p.locator('#f-title').waitFor()
  assert.equal(await p.locator('#f-title').inputValue(), savedTitle)
  console.log('ok shared guard: Stay, Discard, Save, Back/Forward, beforeunload')

  const participant = await person('participant'), e = participant.page
  await open(e, team + '?s=entry', '#en-title')
  await e.locator('#en-title').fill('Owner case draft')
  await e.locator('#en-description').evaluate(n => { n.closest('details').open = true })
  for (const [id, text] of [['problem','Owner problem'],['tagline','Owner solution'],['audience','Owner audience'],['video_url','https://example.com/owner'],['description','Owner details']]) await e.locator('#en-' + id).fill(text)
  await e.locator('#en-track').selectOption('design')
  await e.locator('#en-tools-input').fill('Owner tool'); await e.locator('#en-tools-input').press('Enter')
  await e.locator('[name=en-mat][value=partial]').check()
  await e.getByRole('link', { name: 'Publish your website', exact: true }).click()
  await choice(e, 'Stay')
  assert.equal(await e.locator('#en-title').inputValue(), 'Owner case draft')
  await e.getByRole('link', { name: 'Publish your website', exact: true }).click(); await choice(e, 'Save')
  await e.waitForURL(u => u.searchParams.get('s') === 'website')
  await open(e, team + '?s=entry', '#en-title')
  assert.equal(await e.locator('#en-title').inputValue(), 'Owner case draft')
  assert.equal(await e.locator('#en-problem').inputValue(), 'Owner problem')
  assert.equal(await e.locator('#en-video_url').inputValue(), 'https://example.com/owner')
  assert.equal(await e.locator('#en-track').inputValue(), 'design')
  assert.equal(await e.locator('#en-description').inputValue(), 'Owner details')
  assert.equal(await e.locator('.hint.dirty').count(), 0)
  console.log('ok Entry -> Publish website: Save saves and leaves; Stay keeps typing')

  for (const [id, text] of [['title','Complete project'],['problem','A problem'],['tagline','A solution'],['audience','Everyone'],['video_url','https://example.com/demo']]) await e.locator('#en-' + id).fill(text)
  await e.locator('#en-track').selectOption('')
  await e.getByRole('button', { name: '+ Claude', exact: true }).click()
  await e.locator('[name=en-mat][value=working]').check()
  await e.locator('#en-submit-foot').click()
  await e.locator('#en-track[aria-invalid=true]').waitFor()
  fs.mkdirSync('.shots/sim397', { recursive: true })
  await e.setViewportSize({ width: 390, height: 844 })
  await e.screenshot({ path: '.shots/sim397/entry-error-390.png', fullPage: true })
  assert.equal(await e.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true, 'Entry overflows at 390px')
  await e.setViewportSize({ width: 1280, height: 900 })
  assert.equal(await e.locator('#en-track').evaluate(n => n === document.activeElement), true)
  assert.equal(await e.locator('#en-track').locator('..').getByText('Pick a track', { exact: true }).count(), 1)
  await e.locator('[data-form-banner]').getByRole('button', { name: 'a track', exact: true }).click()
  assert.equal(await e.locator('#en-track').evaluate(n => n === document.activeElement), true)
  await e.locator('#en-need [data-field-focus=track]').click()
  assert.equal(await e.locator('#en-track').evaluate(n => n === document.activeElement), true)
  await e.locator('#en-track').selectOption('design')
  assert.equal(await e.locator('#en-track').getAttribute('aria-invalid'), null)
  await e.locator('#en-save').click()
  await e.locator('[data-form-status]').filter({ hasText: 'Saved' }).waitFor()
  await noErrors(e)
  await e.locator('#en-submit-foot').click()
  await e.locator('.entry-status.submitted').waitFor()
  await noErrors(e)
  console.log('ok Entry errors: missing track marked and focused; links focus; edit clears; save clears; Submit works')
  await e.locator('#en-title').fill('Screenshot draft')
  await e.locator('#en-tools-input').fill('My tool')
  await e.locator('#en-tools-input').press('Enter')
  const screenshot = await e.screenshot({ type: 'png' })
  await e.locator('#en-shot').setInputFiles({ name: 'entry.png', mimeType: 'image/png', buffer: screenshot })
  await e.locator('[data-form-status]').filter({ hasText: 'Screenshot saved' }).waitFor()
  await e.locator('#en-shot-remove').waitFor()
  assert.equal(await e.locator('#en-title').inputValue(), 'Screenshot draft')
  await e.locator('#en-tags .tag').filter({ hasText: 'My tool' }).waitFor()
  await e.locator('.hint.dirty').waitFor()
  await e.locator('#en-save').click()
  await e.locator('[data-form-status]').filter({ hasText: 'Saved' }).waitFor()
  await noErrors(e)
  console.log('ok screenshot upload preserves unsaved Entry fields, tags, and save baseline')


  await open(p, manage + '?s=settings&tab=teams', '#ts-size')
  const newSize = (await p.locator('#ts-size').inputValue()) === '5' ? '6' : '5'
  await p.locator('#ts-size').fill('0'); await p.locator('#ts-save').click()
  await p.locator('#ts-size[aria-invalid=true]').waitFor()
  await p.locator('#ts-size').fill(newSize); await p.locator('#ts-save').click()
  await p.locator('[data-form-status]').filter({ hasText: 'Saved' }).waitFor()
  await noErrors(p)
  console.log('ok Team size errors: error -> edit -> save -> no error')

  await open(p, manage + '?s=event-page&tab=tracks', '#track-add')
  await p.locator('#track-add').click()
  await p.getByRole('dialog').getByRole('button', { name: 'Add track', exact: true }).click()
  await p.getByText('Enter a track name.', { exact: true }).first().waitFor()
  const trackName = 'New track ' + Date.now()
  await p.locator('#trk-name').fill(trackName)
  await noErrors(p, '.dialog')
  await p.getByRole('dialog').getByRole('button', { name: 'Add track', exact: true }).click()
  await p.getByRole('dialog').waitFor({ state: 'hidden' })
  await p.getByText(trackName, { exact: true }).waitFor()
  await noErrors(p)
  console.log('ok Track dialog errors: error -> edit -> save -> no error')

  await p.locator('#track-add').click(); await p.locator('#trk-name').fill('Dialog draft')
  await p.keyboard.press('Escape'); await choice(p, 'Stay')
  assert.equal(await p.locator('#trk-name').inputValue(), 'Dialog draft')
  await p.keyboard.press('Escape'); await choice(p, 'Discard')
  await p.getByRole('dialog').waitFor({ state: 'hidden' })
  console.log('ok dialog close: Escape asks; Stay keeps text; Discard closes')

  // A refused Save from the leave dialog must retain the form and its draft.
  await open(p, manage + '?s=event-page&tab=basics', '#f-title')
  await p.locator('#f-title').fill('Rejected draft')
  await p.route('**' + base, async route => {
    if (route.request().method() !== 'PATCH') return route.continue()
    await route.fulfill({ status: 422, json: { code: 'invalid_title', error: 'Choose another title.' } })
  })
  await p.locator('.subnav a[href*="tab=about"]').click(); await choice(p, 'Save')
  await p.locator('#f-title[aria-invalid=true]').waitFor()
  assert.equal(await p.locator('#f-title').inputValue(), 'Rejected draft')
  assert.equal(new URL(p.url()).searchParams.get('tab'), 'basics')
  await p.unroute('**' + base)
  await p.locator('#f-title').fill('Recovered title ' + Date.now())
  assert.equal(await p.locator('#f-title').getAttribute('aria-invalid'), null)
  await p.locator('#f-save').click()
  await p.locator('[data-form-status]').filter({ hasText: 'Saved' }).waitFor()
  await noErrors(p)
  console.log('ok failed guard Save stays; Basics server field error -> edit -> save -> no error')

  await p.locator('#f-title').fill('Programmatic draft')
  await p.evaluate(path => { import('/hack/app.js').then(app => app.go(path)) }, '/account')
  await choice(p, 'Stay')
  assert.equal(await p.locator('#f-title').inputValue(), 'Programmatic draft')
  await p.locator('[data-signout]').click(); await choice(p, 'Stay')
  await p.evaluate(() => { location.hash = 'about' }); await choice(p, 'Stay')
  assert.equal(new URL(p.url()).hash, '')
  assert.equal(await p.locator('#f-title').inputValue(), 'Programmatic draft')
  await p.locator('.subnav a[href*="tab=about"]').click(); await choice(p, 'Discard')
  await p.locator('#f-about').waitFor()
  console.log('ok programmatic navigation, sign-out, and hash changes share the guard')

  async function guarded(path, selector, edit) {
    await open(p, manage + path, selector)
    await edit()
    await p.locator('.hint.dirty').first().waitFor()
    await p.locator('.side a[data-tip=Account]').click(); await choice(p, 'Stay')
    await p.locator(selector).first().waitFor()
    await p.locator('.side a[data-tip=Account]').click(); await choice(p, 'Discard')
    await p.locator('#acct-name').waitFor()
    assert.equal(await p.evaluate(async () => (await import('/hack/forms.js')).dirty()), false, 'Account became dirty before editing')
  }
  await guarded('?s=settings&tab=details', '#set-name', () => p.locator('#set-name').fill('Unsent organiser details'))
  await guarded('?s=settings&tab=signup', '#q-add', async () => {
    await p.locator('#q-add').click()
    await p.locator('[data-q-prompt]').last().fill('New required question')
  })
  await guarded('?s=settings&tab=judging', '#jg-rubric-save', async () => {
    await p.locator('#jg-rubric input').first().waitFor()
    await p.locator('#jg-rubric input').first().fill('Edited criterion')
    await p.locator('#jg-add').click()
    assert.equal(await p.locator('#jg-rubric input').first().inputValue(), 'Edited criterion')
  })
  await guarded('?s=settings&tab=entries', '#rules-save', async () => {
    const off = await p.locator('[data-field=problem] [data-val=off]').getAttribute('aria-checked')
    await p.locator('[data-field=problem] [data-val=' + (off === 'true' ? 'required' : 'off') + ']').click()
  })
  await guarded('?s=settings&tab=reminders', '#rem-n', async () => {
    await p.locator('#rem-n').fill('87')
    await p.locator('#rem-unit').selectOption('minutes')
    await p.locator('#rem-add-btn').click()
  })
  await guarded('?s=settings&tab=results', '#res-score-mode', async () => {
    const value = await p.locator('#res-score-mode').inputValue()
    await p.locator('#res-score-mode').selectOption(value === 'raw' ? 'normalised' : 'raw')
  })
  console.log('ok details, questions, rubric rows, entry rules, reminder chips, and results register drafts')
  await open(p, manage + '?s=settings&tab=results', '#res-vote-enabled')
  await p.route('**' + base + '/voting', async route => {
    if (route.request().method() !== 'PUT') return route.continue()
    await route.fulfill({ status: 409, json: { code: 'gallery_closed', error: 'Show entries on the event page first.' } })
  })
  await p.locator('#res-vote-enabled').check()
  await p.locator('#res-vote-window-save').click()
  await p.locator('#res-vote-gallery-note').waitFor()
  await p.locator('#res-vote-enabled').uncheck()
  assert.equal(await p.locator('#res-vote-gallery-note').count(), 0)
  await p.unroute('**' + base + '/voting')
  await p.locator('#res-vote-window-save').click()
  await p.locator('#voting [data-form-status]').filter({ hasText: 'Saved' }).waitFor()
  await noErrors(p)
  console.log('ok voting errors: custom server banner clears on edit; save leaves no earlier error')


  // Several dirty forms can share a screen. Saving one must preserve the other.
  await open(p, manage + '?s=settings&tab=entries', '#rules-save')
  await p.locator('#deadline-input').fill('2030-01-01T10:00')
  const ruleValue = (await p.locator('[data-field=problem] [data-val=off]').getAttribute('aria-checked')) === 'true' ? 'required' : 'off'
  await p.locator('[data-field=problem] [data-val=' + ruleValue + ']').click()
  await p.locator('#deadline-save').click()
  await p.locator('#deadline [data-form-status]').filter({ hasText: 'Saved' }).waitFor()
  assert.equal(await p.locator('[data-field=problem] [data-val=' + ruleValue + ']').getAttribute('aria-checked'), 'true')
  await p.locator('.side a[data-tip=Account]').click(); await choice(p, 'Stay')
  await p.locator('.side a[data-tip=Account]').click(); await choice(p, 'Save')
  await p.locator('#acct-name').waitFor()
  console.log('ok saving one form preserves another form draft; leave Save saves the remainder')
  await open(p, manage + '?s=settings&tab=entries', '#rules-save')
  await p.locator('#deadline-input').fill('2031-01-01T10:00')
  const nextRule = (await p.locator('[data-field=problem] [data-val=off]').getAttribute('aria-checked')) === 'true' ? 'required' : 'off'
  await p.locator('[data-field=problem] [data-val=' + nextRule + ']').click()
  await p.route('**' + base + '/entry-rules', async route => {
    if (route.request().method() !== 'PUT') return route.continue()
    await route.fulfill({ status: 422, json: { code: 'invalid_request', error: 'Rules were not saved.' } })
  })
  await p.locator('.side a[data-tip=Account]').click(); await choice(p, 'Save')
  await p.locator('#handin [data-form-status]').filter({ hasText: 'Rules were not saved.' }).waitFor()
  assert.equal(new URL(p.url()).searchParams.get('tab'), 'entries')
  assert.equal(await p.locator('#deadline-input').inputValue(), '2031-01-01T10:00')
  await p.unroute('**' + base + '/entry-rules')
  await p.locator('.side a[data-tip=Account]').click(); await choice(p, 'Save')
  await p.locator('#acct-name').waitFor()
  console.log('ok Save across two forms: repaint keeps the second form; its save error stays visible; retry leaves')


  await open(p, manage + '?s=event-page&tab=tracks', '#track-add')
  const dialogName = 'Saved from dialog guard ' + Date.now()
  await p.locator('#track-add').click(); await p.locator('#trk-name').fill(dialogName)
  await p.keyboard.press('Escape'); await choice(p, 'Save')
  await p.getByRole('dialog').waitFor({ state: 'hidden' })
  await p.getByText(dialogName, { exact: true }).waitFor()
  console.log('ok dialog Save from the leave guard saves and closes')
  await open(p, manage + '?s=team&team=' + info.team, '#msg-team-btn')
  await p.locator('#msg-team-btn').click()
  await p.locator('#tm-text').fill('Unsent message')
  await p.keyboard.press('Escape')
  const sendGuard = p.getByRole('dialog', { name: 'Unsaved changes', exact: true })
  await sendGuard.waitFor()
  assert.equal(await sendGuard.getByRole('button', { name: 'Save', exact: true }).count(), 0)
  await choice(p, 'Stay')
  assert.equal(await p.locator('#tm-text').inputValue(), 'Unsent message')
  await p.keyboard.press('Escape'); await choice(p, 'Discard')
  await p.getByRole('dialog').waitFor({ state: 'hidden' })
  console.log('ok Send-only dialog: Discard/Stay protects the text without sending it')


  await open(p, manage + '?s=event-page&tab=basics', '#f-title')
  await p.locator('#f-title').fill('First saved title ' + Date.now())
  let release
  const paused = new Promise(resolve => { release = resolve })
  await p.route('**' + base, async route => {
    if (route.request().method() !== 'PATCH') return route.continue()
    const response = await route.fetch()
    await paused
    await route.fulfill({ response })
  })
  const request = p.waitForRequest(r => r.method() === 'PATCH' && r.url().endsWith(base))
  await p.locator('#f-save').click(); await request
  await p.locator('#f-title').fill('Typed while saving')
  await p.locator('.subnav a[href*=\"tab=about\"]').click(); await choice(p, 'Stay')
  release()
  await p.locator('[data-form-status]').filter({ hasText: 'Saved' }).waitFor()
  assert.equal(await p.locator('#f-title').inputValue(), 'Typed while saving')
  await p.locator('.hint.dirty').waitFor()
  await p.unroute('**' + base)
  await p.locator('.subnav a[href*="tab=about"]').click(); await choice(p, 'Stay')
  await p.locator('.subnav a[href*="tab=about"]').click(); await choice(p, 'Discard')
  await p.locator('#f-about').waitFor()
  console.log('ok edits typed during a save remain visible and dirty')

  // A: re-registering a key inside save must paint its successful baseline.
  await open(p, manage + '?s=settings&tab=teams', '#ts-size')
  await p.locator('#ts-size').fill((await p.locator('#ts-size').inputValue()) === '7' ? '8' : '7'); await p.locator('#ts-save').click()
  await p.locator('[data-form-status]').filter({ hasText: 'Saved' }).waitFor()
  await p.waitForFunction(() => document.querySelector('#ts-save')?.disabled)
  assert.equal(await p.locator('.hint.dirty').count(), 0)
  console.log('ok A same-key save repaint disables Save and becomes clean')

  // P1.1: overlap two saves sharing one root; only eligibility fails.
  await open(p, manage + '?s=settings&tab=results', '#res-vote-eligibility')
  let releaseWindow, windowStarted
  const windowHeld = new Promise(r => { releaseWindow = r })
  const windowRequest = new Promise(r => { windowStarted = r })
  await p.route('**' + base + '/voting', async route => {
    if (route.request().method() !== 'PUT') return route.continue()
    if (route.request().postDataJSON().eligibility === 'participants') {
      return route.fulfill({ status: 422, json: { code: 'invalid_eligibility', error: 'Eligibility was rejected.' } })
    }
    const response = await route.fetch(); windowStarted(); await windowHeld; await route.fulfill({ response })
  })
  await p.locator('#res-vote-opens').fill('2030-01-01T10:00')
  await p.locator('#res-vote-window-save').click(); await windowRequest
  await p.locator('#res-vote-eligibility').selectOption('participants')
  await p.locator('#res-vote-eligibility-save').click()
  await p.locator('#res-vote-eligibility-save').locator('..').locator('[data-form-status]').filter({ hasText: 'Eligibility was rejected.' }).waitFor()
  releaseWindow()
  await p.locator('#res-vote-window-save').locator('..').locator('[data-form-status]').filter({ hasText: 'Saved' }).waitFor()
  assert.equal(await p.locator('#res-vote-eligibility').inputValue(), 'participants')
  assert.equal(await p.locator('#res-vote-eligibility').getAttribute('aria-invalid'), 'true')
  assert.match(await p.locator('#res-vote-eligibility-save').locator('..').innerText(), /Eligibility was rejected/)
  assert.equal(await p.evaluate(async () => (await import('/hack/forms.js')).forElement(document.querySelector('#res-vote-eligibility')).dirty()), true)
  await p.unroute('**' + base + '/voting')
  console.log('ok P1.1 concurrent save success cannot clear another form failure')

  // P1.2: collecting a snapshot cannot write old rows into a shortened array.
  const reg = await fetch(info.url + base + '/registration', { method: 'PUT', headers: { 'X-API-Key': info.keys.organiser, 'Content-Type': 'application/json' }, body: JSON.stringify({ questions: [{ id: 'q1', prompt: 'First', required: false }, { id: 'q2', prompt: 'Second', required: false }], approval_required: false }) })
  assert.ok(reg.ok)
  await open(p, manage + '?s=settings&tab=signup', '[data-q-rm]')
  await p.locator('[data-q-rm]').first().click()
  assert.equal(await p.locator('[data-q-prompt]').inputValue(), 'Second')
  await p.locator('#q-save').click()
  await p.locator('[data-form-status]').filter({ hasText: 'Questions saved' }).waitFor()
  const questions = await fetch(info.url + base + '/registration', { headers: { 'X-API-Key': info.keys.organiser } }).then(r => r.json())
  assert.deepEqual(questions.questions.map(q => [q.id, q.prompt]), [['q2', 'Second']])
  console.log('ok P1.2 removing First preserves Second and its identity on the server')

  // P1.3: capture the latest visible draft when refresh replaces the DOM.
  await open(p, manage + '?s=event-page&tab=basics', '#f-title')
  await p.locator('#f-title').fill('Refresh A')
  let releaseRefresh, refreshStarted
  const refreshHeld = new Promise(r => { releaseRefresh = r })
  const refreshRequest = new Promise(r => { refreshStarted = r })
  await p.route('**/v1/me', async route => { const response = await route.fetch(); refreshStarted(); await refreshHeld; await route.fulfill({ response }) })
  await p.evaluate(() => { import('/hack/forms.js').then(forms => { window.formRefresh = forms.forElement(document.querySelector('#f-title')).ctx.refresh() }) })
  await refreshRequest
  await p.locator('#f-title').fill('Refresh B')
  assert.equal(await p.evaluate(() => { const e = new Event('beforeunload', { cancelable: true }); window.dispatchEvent(e); return e.defaultPrevented }), true)
  releaseRefresh(); await p.evaluate(() => window.formRefresh)
  assert.equal(await p.locator('#f-title').inputValue(), 'Refresh B')
  await p.locator('.hint.dirty').waitFor()
  await p.unroute('**/v1/me')
  console.log('ok P1.3 refresh retains edits typed during reads and keeps unload guarded')

  // 12: identical destinations never ask or add a history entry.
  assert.equal(await p.evaluate(async () => { const before = history.length; const result = await (await import('/hack/app.js')).go(location.pathname + location.search); return result && before === history.length }), true)
  assert.equal(await p.getByRole('dialog').count(), 0)
  assert.equal(await p.locator('#f-title').inputValue(), 'Refresh B')
  console.log('ok 12 self-navigation keeps the form and history without prompting')

  // 5 and 6: server IDs do not count as edits; a tie draft survives option rebuilds.
  await open(p, manage + '?s=settings&tab=judging', '#jg-rubric-save')
  await p.locator('#jg-rubric input').first().waitFor()
  await p.locator('#jg-tie').selectOption({ index: 1 })
  const tieDraft = await p.locator('#jg-tie').inputValue()
  await p.locator('#jg-add').click()
  await p.locator('#jg-rubric .crit').last().locator('input').first().fill('Zero weight criterion')
  await p.locator('#jg-rubric-save').click()
  await p.locator('#rubric [data-form-status]').filter({ hasText: 'Rubric saved' }).waitFor()
  await p.waitForFunction(() => !document.querySelector('#rubric .hint.dirty'))
  assert.equal(await p.locator('#rubric .hint.dirty').count(), 0)
  assert.equal(await p.locator('#jg-tie').inputValue(), tieDraft)
  assert.equal(await p.evaluate(async () => (await import('/hack/forms.js')).forElement(document.querySelector('#jg-tie')).dirty()), true)
  console.log('ok 5 and 6 rubric server normalization is clean and tie draft survives')

  // 10: Clear is a save, including its baseline.
  await p.locator('#jg-closes-input').fill('2030-02-01T10:00')
  await p.locator('#jg-closes-save').click()
  await p.locator('#closes [data-form-status]').filter({ hasText: 'Saved' }).waitFor()
  await p.locator('#jg-closes-clear').click()
  await p.getByRole('dialog', { name: 'Clear judging closes?', exact: true }).getByRole('button', { name: 'Clear', exact: true }).click()
  await p.waitForFunction(() => document.querySelector('#jg-closes-clear').disabled)
  assert.equal(await p.locator('#closes .hint.dirty').count(), 0)
  console.log('ok 10 clearing judging close time completes its save lifecycle')

  // 8: canceling a confirmation during guarded Save must stay.
  await p.locator('#jg-mode-automatic').check(); await p.locator('#jg-mode-save').click()
  await p.locator('#mode [data-form-status]').filter({ hasText: 'Saved' }).waitFor()
  await open(p, manage + '?s=judging', '#jg-include-organisers')
  await p.locator('#jg-include-organisers').check()
  await p.locator('.side a[data-tip=Account]').click(); await choice(p, 'Save')
  await p.getByRole('dialog', { name: 'Generate assignments?', exact: true }).getByRole('button', { name: 'Cancel', exact: true }).click()
  await p.locator('#jg-include-organisers:checked').waitFor()
  assert.equal(new URL(p.url()).searchParams.get('s'), 'judging')
  await p.locator('.hint.dirty').waitFor()
  console.log('ok 8 canceled assignment generation blocks guarded navigation')

  // 16: failed membership write stays inside the dialog with its selection.
  await p.route('**' + base + '/teams', async route => {
    const response = await route.fetch(), data = await response.json()
    data.on_no_team = [{ user_id: 'solo-first', display_name: 'First solo' }, { user_id: 'solo-second', display_name: 'Second solo' }]
    await route.fulfill({ response, json: data })
  })
  await open(p, manage + '?s=team&team=' + info.team, '#add-someone')
  await p.locator('#add-someone').click(); await p.locator('#add-pick').selectOption('solo-second')
  await p.route('**/teams/*/members', route => route.fulfill({ status: 422, json: { error: 'Membership was rejected.' } }))
  await p.keyboard.press('Escape'); await choice(p, 'Save')
  await p.getByRole('dialog').locator('[data-form-status]').filter({ hasText: 'Membership was rejected.' }).waitFor()
  assert.equal(await p.locator('#add-pick').inputValue(), 'solo-second')
  await p.keyboard.press('Escape'); await choice(p, 'Discard')
  await p.unroute('**/teams/*/members'); await p.unroute('**' + base + '/teams')
  console.log('ok 16 failed dialog Save retains its choice and displays the write error inside')

  // 7: the close-time form is guarded while the unrelated rubric read waits.
  let releaseRubric, rubricStarted
  const rubricHeld = new Promise(r => { releaseRubric = r })
  const rubricRequest = new Promise(r => { rubricStarted = r })
  await p.route('**' + base + '/rubric', async route => { const response = await route.fetch(); rubricStarted(); await rubricHeld; await route.fulfill({ response }) })
  await open(p, manage + '?s=settings&tab=judging', '#jg-closes-input')
  await rubricRequest
  await p.locator('#jg-closes-input').fill('2033-01-01T10:00')
  releaseRubric(); await p.locator('#jg-rubric input').first().waitFor()
  await p.locator('.side a[data-tip=Account]').click(); await choice(p, 'Stay')
  await p.unroute('**' + base + '/rubric')
  console.log('ok 7 close-time edits during initial rubric loading stay guarded')

  // 14: a later instant-save error replaces the successful result.
  await open(p, manage + '?s=settings&tab=teams', '#tf-on')
  await p.locator('#tf-on').click()
  await p.locator('[data-form-status]').filter({ hasText: 'Saved' }).waitFor()
  await p.route('**' + base, route => route.request().method() === 'PATCH'
    ? route.fulfill({ status: 422, json: { error: 'Team finding was rejected.' } }) : route.continue())
  await p.locator('#tf-on').click()
  await p.locator('[data-form-status]').filter({ hasText: 'Team finding was rejected.' }).waitFor()
  assert.equal(await p.locator('[data-form-status]').filter({ hasText: /^Saved$/ }).count(), 0)
  assert.equal(await p.locator('#tf-saved').isVisible(), false)
  await p.unroute('**' + base)
  console.log('ok 14 instant-save error replaces its earlier Saved message')

  // 15: the save result belongs to the replacement form after refresh.
  await open(p, manage + '?s=settings&tab=details', '#set-name')
  const oldDetails = await p.locator('#set-name').elementHandle()
  await p.locator('#set-name').fill('Saved organiser ' + Date.now())
  await p.locator('#set-save').click()
  await p.waitForFunction(n => !n.isConnected, oldDetails)
  await p.locator('[data-form-status]').filter({ hasText: 'Saved' }).waitFor()
  await p.waitForFunction(() => !document.querySelector('.hint.dirty'))
  await open(p, manage + '?s=announcements', '#ann-title')
  const oldAnnouncement = await p.locator('#ann-title').elementHandle()
  await p.locator('#ann-title').fill('Lifecycle announcement')
  await p.locator('#ann-body').fill('A saved message.')
  await p.locator('#ann-post').click()
  await p.waitForFunction(n => !n.isConnected, oldAnnouncement)
  await p.locator('[data-form-status]').filter({ hasText: 'Posted' }).waitFor()
  assert.equal(await p.locator('.hint.dirty').count(), 0)
  console.log('ok 15 organiser details and Posted confirmations survive refresh')

  // 13: Website links use the publish control, including the Missing link.
  await open(e, team + '?s=entry', '#en-title')
  await e.evaluate(async () => {
    const h = (await import('/hack/forms.js')).forElement(document.querySelector('#en-title'))
    h.fieldError('site', 'Publish your website')
    h.summary([{ field: 'site', label: 'a published website' }])
    const missing = document.createElement('button'); missing.dataset.fieldFocus = 'site'; missing.id = 'test-missing-site'; missing.textContent = 'Missing: Website'; h.root.append(missing)
  })
  await e.locator('#test-missing-site').click()
  assert.equal(await e.locator('main a[href*="s=website"]').first().evaluate(n => n === document.activeElement), true)
  await e.locator('[data-form-banner]').getByRole('button', { name: 'a published website', exact: true }).click()
  assert.equal(await e.locator('main a[href*="s=website"]').first().evaluate(n => n === document.activeElement), true)
  console.log('ok 13 Website field marks, Missing links, and banner links focus Publish')

  // 17: exercise the generated organiser bundle and its real frame router.
  const frame = await browser.newPage()
  frame.on('pageerror', err => errors.push(err.message))
  await frame.setContent('<main id="app"></main>')
  await frame.addStyleTag({ content: fs.readFileSync('internal/mcp/apps/hack-organiser-kit.css', 'utf8') })
  await frame.evaluate(() => {
    const listeners = {}, data = { slug: 'frame-event', view: 'judging', event: { slug: 'frame-event', title: 'Frame event', stage: 'judging', judging_locked_at: '2030-01-01T00:00:00Z' } }
    const toolData = name => name === 'hack_get_checklist' ? { checklist: { items: [], next: null } }
      : name === 'hack_app_get_people' || name === 'hack_app_get_applications' ? [] : {}
    window.SHApp = {
      on: (name, callback) => { listeners[name] = callback },
      callTool: async name => toolData(name),
      callToolResult: async name => name === 'hack_show_organiser' ? { structuredContent: data }
        : { _meta: { 'simple-hack/app-only': toolData(name) } },
      openLink: async () => {}, updateModelContext: async () => {},
      start: async () => { listeners['tool-result']({ structuredContent: data }) }
    }
  })
  await frame.addScriptTag({ content: fs.readFileSync('internal/mcp/apps/hack-organiser-bundle.js', 'utf8') })
  await frame.addScriptTag({ content: fs.readFileSync('internal/mcp/apps/hack-organiser-view.js', 'utf8') })
  await frame.locator('#jg-lockzone textarea').fill('Keep this unlock reason')
  await frame.locator('[data-app-view=people]').click(); await choice(frame, 'Stay')
  assert.equal(await frame.locator('#jg-lockzone textarea').inputValue(), 'Keep this unlock reason')
  await frame.evaluate(async () => {
    const h = window.__HACKO.ui.forms.forElement(document.querySelector('#jg-lockzone textarea'))
    await h.ctx.refresh()
  })
  assert.equal(await frame.locator('#jg-lockzone textarea').inputValue(), 'Keep this unlock reason')
  await frame.locator('[data-app-view=people]').click(); await choice(frame, 'Discard')
  await frame.locator('#role-filter').waitFor()
  await frame.close()
  console.log('ok 17 generated MCP organiser frame guards navigation and preserves refresh drafts')

  assert.deepEqual(errors, [])
  console.log('ok browser errors:', errors.length)
  await org.context.close(); await participant.context.close()
  if (!process.env.KEEP_FORMS_FIXTURE) await fetch(info.url + '/_fixture/stop', { method: 'POST' })
} finally { await browser.close() }
