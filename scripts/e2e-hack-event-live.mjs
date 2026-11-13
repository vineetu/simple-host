#!/usr/bin/env node
// HACK_EVENT_LIVE_FIXTURE=/tmp/hack-event-live.json TEST_PKGS=./products/hack/servertest bash scripts/test-integration.sh -run '^TestServeHackEventLiveBrowser$' -timeout 35m
// PLAYWRIGHT_DIR=/tmp/tsx/node_modules node scripts/e2e-hack-event-live.mjs /tmp/hack-event-live.json
import fs from 'node:fs'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
const require = createRequire((process.env.PLAYWRIGHT_DIR || '/tmp/tsx/node_modules') + '/')
const { chromium } = require('playwright')
const info = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'))
const browser = await chromium.launch({ executablePath: '/usr/local/bin/chromium', headless: true, args: ['--no-sandbox'] })
const errors = []
const base = `${info.url}/v1/hack/events/${info.slug}`
async function api(method, path, body) {
  const response = await fetch(base + path, {
    method, headers: { 'Content-Type': 'application/json', 'X-API-Key': info.organiser_key },
    body: JSON.stringify(body)
  })
  assert.ok(response.ok, `${method} ${path}: ${response.status} ${await response.text()}`)
}
try {
  const page = await browser.newPage()
  page.on('pageerror', error => errors.push(error.message))
  let loads = 0
  page.on('load', () => loads++)
  // Open once: all subsequent changes must arrive through the live feed.
  await page.goto(info.event_host_url)
  async function visible(text) {
    // Exercise the recipe's real refocus refresh, without changing its timer.
    const response = page.waitForResponse(r => r.url() === base + '/public')
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
    assert.equal((await response).status(), 200)
    await page.getByText(text, { exact: true }).first().waitFor()
  }
  await visible('Sign-up is open')
  await page.getByText(/^Entries close /).waitFor()
  await page.getByText('Live kickoff', { exact: true }).waitFor()
  await page.getByRole('heading', { name: 'Gallery', exact: true }).waitFor()
  for (const [stage, text] of [
    ['building', 'Teams are building'], ['closed', 'Entries are closed'],
    ['judging', 'Judging is under way'], ['results', 'Results coming soon']
  ]) {
    if (stage === 'building') {
      await api('POST', '/announcements', { title: 'Live announcement', body: 'The next update is here.' })
    }
    await api('POST', '/stage', { stage })
    if (stage === 'building') {
      // Exercise the page's own 60-second timer for this stage.
      await page.getByText(text, { exact: true }).waitFor({ timeout: 75000 })
    } else await visible(text)
    if (['closed', 'judging'].includes(stage)) await page.getByText('Winners will be posted on this page.', { exact: true }).waitFor()
    if (stage === 'building') {
      await page.getByText('Live announcement', { exact: true }).waitFor()
      await page.getByText('The next update is here.', { exact: true }).waitFor()
    }
    console.log(`PASS ${stage}: ${text}`)
  }
  await api('POST', '/results/publish', {})
  await visible('Results are out')
  const overall = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Overall winners', exact: true }) })
  await overall.getByRole('heading', { name: 'Alpha', exact: true }).waitFor()
  assert.equal(await overall.getByRole('heading', { name: 'Beta', exact: true }).count(), 0)
  const tracks = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Track winners', exact: true }) })
  await tracks.getByRole('heading', { name: 'Beta', exact: true }).waitFor()
  await tracks.getByText('Climate', { exact: true }).waitFor()
  console.log('PASS published results: overall and track winners')
  await api('POST', '/stage', { stage: 'archived' })
  await visible('This event has ended')
  await overall.getByRole('heading', { name: 'Alpha', exact: true }).waitFor()
  assert.equal(loads, 1, 'custom page must never reload')
  assert.deepEqual(errors, [], 'browser pageerrors')
  console.log('PASS archived: results remain, no reload or republish')
  await fetch(info.url + '/_fixture/stop', { method: 'POST' })
} finally { await browser.close() }
