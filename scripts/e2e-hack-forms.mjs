#!/usr/bin/env node
// HACK_FORMS_FIXTURE=/tmp/hack-forms.json TEST_PKGS=./internal/hackhttp bash scripts/test-integration.sh -run '^TestServeHackFormsBrowser$' -timeout 35m
// PLAYWRIGHT_DIR=/tmp/tsx/node_modules node scripts/e2e-hack-forms.mjs /tmp/hack-forms.json
import fs from 'node:fs'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
const require = createRequire((process.env.PLAYWRIGHT_DIR || '/tmp/tsx/node_modules') + '/')
const { chromium } = require('playwright')
const info = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'))
const browser = await chromium.launch({ executablePath: '/usr/local/bin/chromium', headless: true, args: ['--no-sandbox'] })
const errors = []
const manage = `/e/${info.slug}/manage`
const screens = [
  { name: 'Basics', role: 'organiser', path: manage + '?s=event-page&tab=basics', field: '#f-tag', save: '#f-save', endpoint: `/events/${info.slug}` },
  { name: 'Settings Entries', role: 'organiser', path: manage + '?s=settings&tab=entries', field: '[data-field="code_url"] [role="radio"][aria-checked="true"]', save: '#rules-save', endpoint: '/entry-rules', segmented: true },
  { name: 'Participant Entry', role: 'participant', path: `/e/${info.slug}/team?s=entry`, field: '#en-title', save: '#en-save', endpoint: '/entry' },
  { name: 'Settings Judging', role: 'organiser', path: manage + '?s=settings&tab=judging', field: '#jg-closes-input', save: '#jg-closes-save', endpoint: `/events/${info.slug}`, datetime: true },
  { name: 'Registration questions', role: 'organiser', path: manage + '?s=settings&tab=signup', field: '#q-add', save: '#q-save', endpoint: '/registration', questions: true }
]
const leaveDialog = page => page.getByRole('dialog', { name: 'Leave without saving?', exact: true })
async function start(screen) {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } })
  await context.addInitScript(key => localStorage.setItem('apiKey', key), info.keys[screen.role])
  const page = await context.newPage()
  page.on('pageerror', e => errors.push(e.message))
  await page.route('**/hack/**', async route => {
    const path = new URL(route.request().url()).pathname
    const file = 'internal/webassets/static' + path
    if (!fs.existsSync(file)) return route.continue()
    await route.fulfill({ body: fs.readFileSync(file), contentType: path.endsWith('.css') ? 'text/css' : 'text/javascript' })
  })
  await page.route('**/v1/hack/preferences', route => route.fulfill({ json: { organiser_walkthrough_done: true, judge_walkthrough_done: true } }))
  await page.goto(info.url + '/events')
  await page.locator('main h1').waitFor()
  // Enter through the router so Back has a genuine in-app history entry.
  await page.evaluate(async path => (await import('/hack/app.js')).go(path), screen.path)
  await page.locator(screen.field).first().waitFor()
  return { context, page }
}
async function edit(page, screen) {
  if (screen.questions) {
    if (!await page.locator('[data-q-prompt]').count()) await page.locator('#q-add').click()
    const field = page.locator('[data-q-prompt]').first()
    const value = 'Question ' + Date.now()
    await field.fill(value)
    return async () => assert.equal(await field.inputValue(), value)
  }
  if (screen.segmented) {
    const current = await page.locator(screen.field).getAttribute('data-val')
    const next = current === 'off' ? 'optional' : 'off'
    const field = page.locator(`[data-field="code_url"] [data-val="${next}"]`)
    await field.click()
    return async () => assert.equal(await field.getAttribute('aria-checked'), 'true')
  }
  const field = page.locator(screen.field)
  const old = await field.inputValue()
  const value = screen.datetime ? (old === '2027-06-01T12:00' ? '2027-06-02T12:00' : '2027-06-01T12:00') : 'Forms ' + Date.now()
  await field.fill(value)
  return async () => assert.equal(await field.inputValue(), value)
}
async function destination(page) {
  const link = page.locator('.side a[href="/events"]').first()
  await link.waitFor()
  return { link, url: new URL(await link.getAttribute('href'), page.url()).href }
}
async function navigated(page, url) {
  await page.waitForURL(url)
  await page.getByRole('heading', { name: 'Your events', exact: true }).waitFor()
  assert.equal(await leaveDialog(page).count(), 0)
}
try {
  for (const screen of screens) {
    for (const check of ['Stay', 'Leave', 'save', 'clean', 'Back', ...(screen === screens[0] ? ['Back Leave'] : [])]) {
      const { context, page } = await start(screen)
      try {
        const original = page.url()
        const verify = check === 'clean' ? null : await edit(page, screen)
        if (check === 'Back' || check === 'Back Leave') {
          await page.goBack()
          await leaveDialog(page).waitFor()
          await leaveDialog(page).getByRole('button', { name: check === 'Back Leave' ? 'Leave' : 'Stay', exact: true }).click()
          if (check === 'Back Leave') await navigated(page, info.url + '/events')
          else { await page.waitForURL(original); await verify() }
        } else {
          if (check === 'save') {
            const response = page.waitForResponse(r => ['PUT', 'PATCH'].includes(r.request().method()) && new URL(r.url()).pathname.endsWith(screen.endpoint) && !r.request().postDataJSON()?.dry_run)
            await page.locator(screen.save).click()
            assert.equal((await response).status(), 200, screen.name + ' save failed')
            await page.getByText(screen.questions ? 'Questions saved.' : 'Saved', { exact: true }).first().waitFor()
            await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
          }
          const { link, url } = await destination(page)
          await link.click()
          if (check === 'Stay' || check === 'Leave') {
            const dialog = leaveDialog(page)
            await dialog.waitFor()
            assert.equal(await dialog.locator('p').count(), 0)
            assert.equal(await dialog.getByRole('button', { name: 'Stay', exact: true }).evaluate(n => n === document.activeElement), true)
            await dialog.getByRole('button', { name: check, exact: true }).click()
            if (check === 'Stay') { assert.equal(page.url(), original); await verify() }
            else await navigated(page, url)
          } else await navigated(page, url)
        }
        console.log(`PASS ${screen.name}: ${check}`)
      } finally { await context.close() }
    }
  }
  for (const dirty of [true, false]) {
    const { context, page } = await start(screens[0])
    try {
      if (dirty) await edit(page, screens[0])
      else await page.locator('#f-tag').click() // Give beforeunload the same user activation.
      let prompts = 0
      page.on('dialog', async dialog => {
        assert.equal(dialog.type(), 'beforeunload')
        prompts++
        await dialog.accept()
      })
      await page.reload()
      await page.locator('#f-tag').waitFor()
      assert.equal(prompts, dirty ? 1 : 0)
      console.log(`PASS ${dirty ? 'dirty' : 'clean'} reload: beforeunload ${dirty ? 'appears' : 'absent'}`)
    } finally { await context.close() }
  }
  assert.deepEqual(errors, [], 'browser pageerrors')
  await fetch(info.url + '/_fixture/stop', { method: 'POST' })
} finally { await browser.close() }
