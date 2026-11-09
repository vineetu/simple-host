#!/usr/bin/env node
// SIM-394 organiser assistant -- browser checks against the real API/DB
// fixture from TestServeHackNavigationBrowser (same pattern as
// scripts/e2e-hack-checklists.mjs). The fixture server has no assistant
// configured (no ASSISTANT_BASE_URL), so GET/POST
// **/v1/hack/events/*/assistant is mocked here with page.route -- the
// contract (api-contract.md) is what is being built to, not a live model.
//
//   HACK_NAVIGATION_FIXTURE=/tmp/sim394-assistant-fixture.json TEST_PKGS=./internal/hackhttp \
//     bash scripts/test-integration.sh -run TestServeHackNavigationBrowser   # separate shell, stays up
//   node scripts/e2e-hack-assistant.mjs /tmp/sim394-assistant-fixture.json
import fs from 'node:fs'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
const require = createRequire('/tmp/tsx/node_modules/')
const { chromium } = require('playwright')
const info = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'))
const shots = '.shots/assistant'
fs.mkdirSync(shots, { recursive: true })
const browser = await chromium.launch({ executablePath: '/usr/local/bin/chromium', args: ['--no-sandbox'] })
const pageErrors = []
const base = '/v1/hack/events/' + info.slug

function sse(events) { return events.map(e => 'data: ' + JSON.stringify(e) + '\n\n').join('') }
const GET_OK = { enabled: true, daily_limit: 30, used_today: 3, remaining_today: 27, budget_reached: false, resets: '2026-12-01' }

// `getData` and `postHandler` are swapped between steps; one route per
// context dispatches on method so tests can reassign behaviour freely.
function state() { return { getData: { ...GET_OK }, postHandler: null } }

async function person(role, width, { walkthroughDone = true, markTourSeen = jsonVal => {} } = {}) {
  const st = state()
  const context = await browser.newContext({ viewport: { width, height: width === 390 ? 844 : 900 } })
  await context.addInitScript(key => localStorage.setItem('apiKey', key), info.keys[role])
  const page = await context.newPage()
  page.on('pageerror', e => pageErrors.push(e.message))
  await page.route('**/hack/**', async route => {
    const path = new URL(route.request().url()).pathname
    const file = 'internal/webassets/static' + path
    if (!fs.existsSync(file)) return route.continue()
    await route.fulfill({ body: fs.readFileSync(file), contentType: path.endsWith('.css') ? 'text/css' : 'text/javascript' })
  })
  await page.route('**/v1/hack/preferences', route => {
    if (route.request().method() === 'PATCH') { markTourSeen(route.request().postDataJSON()); return route.fulfill({ json: { ok: true } }) }
    return route.fulfill({ json: { organiser_walkthrough_done: walkthroughDone, judge_walkthrough_done: true, participant_walkthrough_done: true } })
  })
  await page.route('**' + base + '/assistant', async route => {
    if (route.request().method() === 'GET') { await route.fulfill({ json: st.getData }); return }
    if (st.postHandler) { await st.postHandler(route); return }
    await route.fulfill({ status: 500, json: { error: 'no handler set for this test' } })
  })
  return { page, context, st }
}

async function openOverview(page) {
  await page.goto(info.url + `/e/${info.slug}/manage?s=overview`)
  await page.locator('[data-tour="assistant"]').first().waitFor()
}
async function openPanel(page) {
  await page.locator('[data-tour="assistant"]').click()
  await page.locator('.assistant-panel').waitFor()
}
async function sendText(page, text) {
  await page.locator('[data-assistant-input]').fill(text)
  await page.locator('[data-assistant-input]').press('Enter')
}

const org = await person('organiser', 1280)
try {
  const { page, st } = org

  // 1) Hidden when disabled, and on a GET error -- no button at all.
  {
    const off = await person('organiser', 1280)
    off.st.getData = { enabled: false }
    await off.page.goto(info.url + `/e/${info.slug}/manage?s=overview`)
    await off.page.locator('#ov-change-stage').waitFor()
    assert.equal(await off.page.locator('[data-tour="assistant"]').count(), 0, 'hidden when enabled:false')
    await off.context.close()
    const errored = await person('organiser', 1280)
    await errored.page.route('**' + base + '/assistant', route => route.fulfill({ status: 500, json: { error: 'internal' } }))
    await errored.page.goto(info.url + `/e/${info.slug}/manage?s=overview`)
    await errored.page.locator('#ov-change-stage').waitFor()
    await errored.page.waitForTimeout(300)
    assert.equal(await errored.page.locator('[data-tour="assistant"]').count(), 0, 'hidden on a GET error')
    await errored.context.close()
    console.log('ok: Ask hidden when disabled / on GET error')
  }

  // 2) Button present, open/close, focus, starter chips, sub line.
  await openOverview(page)
  await page.screenshot({ path: `${shots}/before-open.png` })
  await openPanel(page)
  assert.equal(await page.locator('.assistant-panel').getAttribute('role'), 'complementary', 'desktop role')
  assert.equal(await page.locator('.assistant-panel').getAttribute('aria-label'), 'Ask about this event')
  assert.ok(await page.locator('[data-assistant-input]').evaluate(el => el === document.activeElement), 'opening focuses textarea')
  assert.match(await page.locator('[data-assistant-sub]').innerText(), /Short answers.*can't change anything/)
  const chips = page.locator('.assistant-chips .chip')
  assert.equal(await chips.count(), 3, 'three starter chips')
  const chipTexts = await chips.allInnerTexts()
  assert.equal(new Set(chipTexts).size, 3, 'chips deduped/unique')
  assert.ok(chipTexts.includes('Where do I find the join and judge links?'), 'fixed chip present')
  console.log('ok: Ask button opens the panel; starters and sub line shown')

  // 3) Esc with nothing streaming closes the panel and returns focus to Ask.
  await page.keyboard.press('Escape')
  await page.locator('.assistant-panel').waitFor({ state: 'detached' })
  assert.ok(await page.locator('[data-tour="assistant"]').evaluate(el => el === document.activeElement), 'Esc returns focus to Ask')
  console.log('ok: Esc closes panel, focus returns to Ask')

  // 4) Enter sends, Shift+Enter inserts a newline, IME composition is respected, auto-grow happens.
  await openPanel(page)
  st.postHandler = async route => { await route.fulfill({ status: 200, contentType: 'text/event-stream', body: sse([{ delta: 'ok' }, { done: true, remaining_today: 26 }]) }) }
  const ta = page.locator('[data-assistant-input]')
  await ta.fill('line one')
  await ta.press('Shift+Enter')
  await ta.type('line two')
  assert.match(await ta.inputValue(), /line one\nline two/, 'Shift+Enter inserts a newline, does not send')
  assert.equal(await page.locator('.assistant-msg.user').count(), 0, 'nothing sent yet')
  const shortHeight = await ta.evaluate(el => el.clientHeight)
  await ta.fill('one\ntwo\nthree\nfour\nfive\nsix\nseven\neight')
  await page.waitForTimeout(50)
  const grownHeight = await ta.evaluate(el => el.clientHeight)
  assert.ok(grownHeight > shortHeight, 'textarea grows with content')
  // isComposing: a synthetic Enter mid-composition must not send.
  await ta.evaluate(el => { el.value = 'mid ime'; el.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true, isComposing: true })) })
  assert.equal(await page.locator('.assistant-msg.user').count(), 0, 'Enter while composing does not send')
  await ta.fill('')
  console.log('ok: Enter/Shift+Enter, IME guard, auto-grow')

  // 5) Thinking -> streamed markdown (strong, ul, code, pre, a), link allowlist, no raw **.
  st.postHandler = async route => {
    await new Promise(r => setTimeout(r, 250))
    await route.fulfill({ status: 200, contentType: 'text/event-stream', body: sse([
      { delta: "**Bold** and a list:\n- one\n- two\n\n`code` and a block:\n```\nline(1)\n```\n" },
      { delta: 'Go to [Teams](/e/' + info.slug + '/manage?s=teams) or [docs](/docs/start). ' },
      { delta: 'Not a link: [elsewhere](https://evil.example/x) or [js](javascript:alert(1)).' },
      { done: true, remaining_today: 25 }
    ]) })
  }
  await sendText(page, 'What should I do next?')
  assert.equal(await page.locator('.assistant-msg.user').last().innerText(), 'What should I do next?')
  await page.locator('.assistant-thinking').waitFor({ timeout: 2000 })
  await page.locator('.assistant-thinking').waitFor({ state: 'detached', timeout: 3000 })
  const reply = page.locator('.assistant-msg.assistant').last().locator('.assistant-bubble')
  await reply.locator('a').first().waitFor()
  assert.equal(await reply.locator('strong').count(), 1)
  assert.equal(await reply.locator('ul li').count(), 2)
  assert.equal(await reply.locator('code').count(), 2) // inline + fenced
  assert.equal(await reply.locator('pre').count(), 1)
  const links = reply.locator('a')
  assert.equal(await links.count(), 2, 'only the two allowed links render as <a>')
  assert.equal(await links.nth(0).getAttribute('href'), '/e/' + info.slug + '/manage?s=teams')
  assert.equal(await links.nth(1).getAttribute('href'), '/docs/start')
  const bubbleText = await reply.innerText()
  assert.ok(bubbleText.includes('elsewhere') && bubbleText.includes('js'), 'disallowed link labels still shown as text')
  assert.ok(!bubbleText.includes('**'), 'no raw markdown syntax left behind')
  console.log('ok: Thinking -> streamed markdown; link allowlist enforced')

  // 6) Copy copies the reply's plain text.
  await org.context.grantPermissions(['clipboard-read', 'clipboard-write'])
  await page.locator('.assistant-msg.assistant').last().locator('.assistant-acts button:has-text("Copy")').click()
  const clip = await page.evaluate(() => navigator.clipboard.readText())
  assert.ok(clip.includes('Bold') && clip.includes('**'), 'Copy uses the raw reply text, not the rendered HTML')
  console.log('ok: Copy writes the reply to the clipboard')

  // 7) Clicking the allowed /e/ link navigates (same tab, client router).
  await reply.locator('a').first().click()
  await page.waitForURL(u => u.pathname === `/e/${info.slug}/manage` && u.search.includes('s=teams'))
  console.log('ok: clicking an /e/ link navigates')
  await openOverview(page) // back to Overview for the rest

  // 8) Starter chip click sends it; remaining-hint appears at <=5; singular wording at 1.
  // Fresh thread first -- step 5/6/7 already put messages in this slug's
  // history, which mountAssistant read into memory at page load, so New
  // chat (not a localStorage poke after the fact) is what actually empties
  // the live thread.
  await openPanel(page)
  await page.locator('.assistant-head-acts button:has-text("New chat")').click()
  await page.locator('.assistant-chips .chip').first().waitFor()
  st.postHandler = async route => { await route.fulfill({ status: 200, contentType: 'text/event-stream', body: sse([{ delta: 'Sure.' }, { done: true, remaining_today: 4 }]) }) }
  const firstChip = await page.locator('.assistant-chips .chip').first().innerText()
  await page.locator('.assistant-chips .chip').first().click()
  assert.equal(await page.locator('.assistant-msg.user').last().innerText(), firstChip, 'clicking a chip sends its text')
  assert.equal(await page.locator('[data-assistant-input]').inputValue(), '', 'textarea clears after a chip sends')
  await page.locator('[data-assistant-hint]').waitFor()
  assert.equal(await page.locator('[data-assistant-hint]').innerText(), '4 questions left today.')
  st.postHandler = async route => { await route.fulfill({ status: 200, contentType: 'text/event-stream', body: sse([{ delta: 'One left.' }, { done: true, remaining_today: 1 }]) }) }
  await sendText(page, 'another one')
  await page.waitForFunction(() => document.querySelector('[data-assistant-hint]')?.innerText === '1 question left today.')
  console.log('ok: starter chip sends; remaining-hint singular/plural')

  // 9) Retry on the last reply replaces it with a fresh answer to the same question.
  st.postHandler = async route => { await route.fulfill({ status: 200, contentType: 'text/event-stream', body: sse([{ delta: 'Second answer.' }, { done: true, remaining_today: 0 }]) }) }
  const beforeRetryCount = await page.locator('.assistant-msg.assistant').count()
  await page.locator('.assistant-msg.assistant').last().locator('[data-assistant-retry]').click()
  await page.waitForFunction(() => document.querySelector('.assistant-msg.assistant:last-child')?.innerText.includes('Second answer'))
  assert.equal(await page.locator('.assistant-msg.assistant').count(), beforeRetryCount, 'retry replaces, does not add a new bubble')
  console.log('ok: Retry replaces the last reply')

  // 10) New chat clears the thread and localStorage.
  await page.locator('.assistant-head-acts button:has-text("New chat")').click()
  await page.locator('.assistant-msg').waitFor({ state: 'detached' }).catch(() => {})
  assert.equal(await page.locator('.assistant-msg').count(), 0, 'thread cleared')
  assert.equal(await page.locator('.assistant-chips .chip').count(), 3, 'starters back')
  const cleared = await page.evaluate(slug => localStorage.getItem('sh-assistant-' + slug), info.slug)
  assert.equal(cleared, null, 'localStorage cleared')
  console.log('ok: New chat clears the thread and storage')

  // 11) Errors: offline, fetch-throws, generic 4xx/5xx, daily_limit (disables input), mid-stream provider_error.
  await org.context.setOffline(true)
  await sendText(page, 'are you there')
  await page.locator('.assistant-error').waitFor()
  assert.match(await page.locator('.assistant-error').last().innerText(), /You're offline\. Reconnect and try again\./)
  await org.context.setOffline(false)
  const errorsBeforeThrow = await page.locator('.assistant-error').count()
  st.postHandler = async route => route.abort('failed')
  await page.locator('[data-assistant-input]').fill('ping')
  await page.locator('[data-assistant-input]').press('Enter')
  await page.waitForFunction(n => document.querySelectorAll('.assistant-error').length > n, errorsBeforeThrow)
  assert.match(await page.locator('.assistant-error').last().innerText(), /Cannot reach the server\./)
  console.log('ok: offline and fetch-throws error lines')

  st.postHandler = async route => route.fulfill({ status: 500, json: {} })
  await sendText(page, 'ping again')
  await page.waitForFunction(() => Array.from(document.querySelectorAll('.assistant-error')).some(n => n.innerText.includes('Something went wrong. Refresh the page and try again.')))
  console.log('ok: generic 4xx/5xx error line')

  st.postHandler = async route => route.fulfill({ status: 429, json: { error: 'daily_limit', message: "You've asked 30 questions today. The limit resets at midnight UTC.", remaining_today: 0 } })
  await sendText(page, 'one more')
  await page.waitForFunction(() => Array.from(document.querySelectorAll('.assistant-error')).some(n => n.innerText.includes("You've asked 30 questions today")))
  assert.ok(await page.locator('[data-assistant-input]').isDisabled(), 'input disabled on daily_limit')
  assert.ok(await page.locator('[data-assistant-send]').isDisabled(), 'send disabled on daily_limit')
  assert.match(await page.locator('[data-assistant-hint]').innerText(), /You've asked 30 questions today/)
  console.log('ok: daily_limit disables the input with the server message')

  // 12) Mid-stream provider_error keeps the partial text and shows an error line.
  {
    const p2 = await person('organiser', 1280)
    await openOverview(p2.page)
    await openPanel(p2.page)
    p2.st.postHandler = async route => route.fulfill({ status: 200, contentType: 'text/event-stream', body: sse([{ delta: 'Partial answer here.' }, { error: 'provider_error', message: "The assistant couldn't answer. Try again." }]) })
    await sendText(p2.page, 'explain scoring')
    await p2.page.waitForFunction(() => Array.from(document.querySelectorAll('.assistant-error')).some(n => n.innerText.includes("couldn't answer")))
    assert.match(await p2.page.locator('.assistant-msg.assistant').last().innerText(), /Partial answer here/, 'partial text kept on mid-stream error')
    assert.equal(await p2.page.locator('.assistant-msg.assistant').last().locator('[data-assistant-retry]').count(), 1, 'Retry on the kept partial reply')
    assert.equal(await p2.page.locator('.assistant-error').last().locator('button:has-text("Retry")').count(), 1, 'Retry also offered on the error line')
    await p2.context.close()
    console.log('ok: mid-stream provider_error keeps partial text, offers Retry')
  }

  // 13) Stop: a slow-to-arrive reply can be aborted cleanly (no crash, no stray error, Send re-enabled).
  {
    const p3 = await person('organiser', 1280)
    await openOverview(p3.page)
    await openPanel(p3.page)
    p3.st.postHandler = async route => { await new Promise(r => setTimeout(r, 2500)); await route.fulfill({ status: 200, contentType: 'text/event-stream', body: sse([{ delta: 'too late' }, { done: true, remaining_today: 20 }]) }) }
    await sendText(p3.page, 'a slow question')
    await p3.page.locator('[data-assistant-send]:has-text("Stop")').waitFor()
    await p3.page.locator('[data-assistant-send]').click()
    await p3.page.locator('[data-assistant-send]:has-text("Send")').waitFor()
    assert.equal(await p3.page.locator('.assistant-thinking').count(), 0, 'the placeholder is gone once stopped')
    assert.equal(await p3.page.locator('.assistant-error').count(), 0, 'stopping is not reported as an error')
    await p3.context.close()
    console.log('ok: Stop aborts a pending reply cleanly')
  }

  // 14) Esc while streaming aborts the stream without closing the panel.
  {
    const p4 = await person('organiser', 1280)
    await openOverview(p4.page)
    await openPanel(p4.page)
    p4.st.postHandler = async route => { await new Promise(r => setTimeout(r, 2500)); await route.fulfill({ status: 200, contentType: 'text/event-stream', body: sse([{ done: true, remaining_today: 20 }]) }) }
    await sendText(p4.page, 'another slow one')
    await p4.page.locator('[data-assistant-send]:has-text("Stop")').waitFor()
    await p4.page.keyboard.press('Escape')
    await p4.page.locator('[data-assistant-send]:has-text("Send")').waitFor()
    assert.equal(await p4.page.locator('.assistant-panel').count(), 1, 'panel stays open; Esc only stopped the stream')
    await p4.context.close()
    console.log('ok: Esc while streaming aborts, panel stays open')
  }

  // 15) Persistence across reload.
  {
    const p5 = await person('organiser', 1280)
    await openOverview(p5.page)
    await openPanel(p5.page)
    p5.st.postHandler = async route => route.fulfill({ status: 200, contentType: 'text/event-stream', body: sse([{ delta: 'Remembered reply.' }, { done: true, remaining_today: 20 }]) })
    await sendText(p5.page, 'remember this')
    await p5.page.waitForFunction(() => document.querySelector('.assistant-msg.assistant')?.innerText.includes('Remembered reply'))
    await p5.page.reload()
    await p5.page.locator('[data-tour="assistant"]').waitFor()
    await openPanel(p5.page)
    assert.equal(await p5.page.locator('.assistant-msg.user').last().innerText(), 'remember this')
    assert.match(await p5.page.locator('.assistant-msg.assistant').last().innerText(), /Remembered reply/)
    await p5.context.close()
    console.log('ok: history persists across reload')
  }

  // 16) Walkthrough step targets the Ask button (and tour.js skips it if the target were missing).
  {
    const p6 = await person('organiser', 1280, { walkthroughDone: false })
    await p6.page.goto(info.url + `/e/${info.slug}/manage?s=overview&walkthrough=1`)
    await p6.page.locator('.tour-card').waitFor()
    // Step through until the assistant step (or fail if it never appears).
    let found = false
    for (let i = 0; i < 10; i++) {
      const title = await p6.page.locator('.tour-title').innerText()
      if (title === 'Ask about your event') { found = true; break }
      const nextBtn = p6.page.locator('[data-tour-next]')
      if ((await nextBtn.innerText()) === 'Done') break
      await nextBtn.click()
      await p6.page.waitForTimeout(80)
    }
    assert.ok(found, 'walkthrough reaches the Ask step')
    assert.match(await p6.page.locator('.tour-body').innerText(), /It answers in a line or two and links to the place to change it\./)
    assert.ok(await p6.page.locator('[data-tour="assistant"]').evaluate(el => el.classList.contains('tour-ring')), 'the Ask button is ringed')
    await p6.context.close()
    console.log('ok: walkthrough step targets the Ask button')
  }

  // 17) Phone (390px): dialog role, aria-modal, no horizontal scroll, focus trap.
  {
    const ph = await person('organiser', 390)
    await openOverview(ph.page)
    await openPanel(ph.page)
    assert.equal(await ph.page.locator('.assistant-panel').getAttribute('role'), 'dialog', 'phone role=dialog')
    assert.equal(await ph.page.locator('.assistant-panel').getAttribute('aria-modal'), 'true')
    const overflow = await ph.page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    assert.ok(overflow <= 1, 'no horizontal scroll at 390px, got ' + overflow)
    await ph.page.screenshot({ path: `${shots}/phone-sheet.png` })
    await ph.context.close()
    console.log('ok: phone sheet role/aria-modal/no horizontal scroll')
  }

  // ---- round-2 review fixes (H2, M1, M2, M3, M7, L2, M8) -----------------

  // 18) H2: a window focus mid-stream re-renders Overview (app.js's own
  // focus handler calls activeContext.refresh()). The panel must reopen on
  // its own (it was open) showing the same thread, and the reply already
  // in flight must still land once it arrives -- nothing dropped by the
  // re-render tearing the old DOM down.
  {
    const p7 = await person('organiser', 1280)
    await openOverview(p7.page)
    await openPanel(p7.page)
    let resolveSlow
    p7.st.postHandler = async route => {
      await new Promise(r => { resolveSlow = r })
      await route.fulfill({ status: 200, contentType: 'text/event-stream', body: sse([{ delta: 'Answer after refresh.' }, { done: true, remaining_today: 10 }]) })
    }
    await sendText(p7.page, 'will this survive a refresh?')
    await p7.page.locator('.assistant-thinking').waitFor()
    await p7.page.evaluate(() => window.dispatchEvent(new Event('focus')))
    await p7.page.waitForTimeout(500)
    assert.equal(await p7.page.locator('.assistant-panel').count(), 1, 'panel reopened on its own after the re-render')
    assert.equal(await p7.page.locator('.assistant-msg.user').last().innerText(), 'will this survive a refresh?', 'the question survived the re-render')
    resolveSlow()
    await p7.page.waitForFunction(() => document.querySelector('.assistant-msg.assistant')?.innerText.includes('Answer after refresh'))
    assert.equal(await p7.page.locator('.assistant-panel').count(), 1, 'panel still open once the delayed reply lands')
    await p7.context.close()
    console.log('ok: window-focus re-render keeps the panel open and the in-flight turn completes into it (H2)')
  }

  // 19) M1: New chat while a reply is streaming must never show that
  // stale answer once it finally arrives.
  {
    const p8 = await person('organiser', 1280)
    await openOverview(p8.page)
    await openPanel(p8.page)
    let resolveStale
    p8.st.postHandler = async route => {
      await new Promise(r => { resolveStale = r })
      await route.fulfill({ status: 200, contentType: 'text/event-stream', body: sse([{ delta: 'STALE ANSWER' }, { done: true, remaining_today: 5 }]) })
    }
    await sendText(p8.page, 'first question')
    await p8.page.locator('.assistant-thinking').waitFor()
    await p8.page.locator('.assistant-head-acts button:has-text("New chat")').click()
    await p8.page.locator('.assistant-chips .chip').first().waitFor()
    resolveStale()
    await p8.page.waitForTimeout(500)
    assert.equal(await p8.page.locator('.assistant-msg').count(), 0, 'the stale answer never lands in the fresh chat')
    await p8.context.close()
    console.log('ok: New chat during a stream discards the stale answer (M1)')
  }

  // 20) M2: closing with X mid-stream aborts cleanly -- no stuck "Stop"
  // button on reopen, no stray error line.
  {
    const p9 = await person('organiser', 1280)
    await openOverview(p9.page)
    await openPanel(p9.page)
    p9.st.postHandler = async route => { await new Promise(r => setTimeout(r, 2500)); await route.fulfill({ status: 200, contentType: 'text/event-stream', body: sse([{ delta: 'too late' }, { done: true, remaining_today: 8 }]) }) }
    await sendText(p9.page, 'close me mid stream')
    await p9.page.locator('[data-assistant-send]:has-text("Stop")').waitFor()
    await p9.page.locator('[data-assistant-close]').click()
    await p9.page.locator('.assistant-panel').waitFor({ state: 'detached' })
    await openPanel(p9.page)
    await p9.page.locator('[data-assistant-send]:has-text("Send")').waitFor()
    assert.equal(await p9.page.locator('.assistant-error').count(), 0, 'closing mid-stream is not reported as an error')
    await p9.context.close()
    console.log('ok: closing with X mid-stream aborts cleanly (M2)')
  }

  // 21) M3/M4: a 400 pops the trailing question and restores its exact
  // text to the textarea. The textarea's own maxlength=2000 means code
  // points typed through the UI can never exceed MAX_MSG_CHARS (UTF-16
  // length is always >= code-point length, so a maxlength cap on the
  // former caps the latter too) -- so content that actually reaches the
  // server here is never "too long" by the client's own count, and the
  // generic line (the server's own message) is what shows. The too-long
  // line itself (assistant.js's cpLen(poppedText) > MAX_MSG_CHARS branch)
  // is there for any path that bypasses the textarea; not reachable
  // through typed/pasted input, so not exercised here.
  {
    const p10 = await person('organiser', 1280)
    await openOverview(p10.page)
    await openPanel(p10.page)
    p10.st.postHandler = async route => route.fulfill({ status: 400, json: { error: 'bad_request', message: 'Something about this question confused the assistant.' } })
    const text = 'a question that gets refused for some other reason'
    await sendText(p10.page, text)
    await p10.page.waitForFunction(() => Array.from(document.querySelectorAll('.assistant-error')).some(n => n.innerText.includes('confused the assistant')))
    assert.equal(await p10.page.locator('[data-assistant-input]').inputValue(), text, 'the rejected text is restored to the textarea verbatim')
    assert.equal(await p10.page.locator('.assistant-msg.user').count(), 0, 'the rejected question is popped off the thread, not left dangling')
    assert.equal(await p10.page.locator('.assistant-error').last().locator('button:has-text("Retry")').count(), 1, 'a non-limit 400 still offers Retry')
    await p10.context.close()
    console.log("ok: 400 pops the question and restores it to the textarea, with the server's own line (M3/M4)")
  }

  // 22) M7: the history key is scoped to the signed-in user, and the
  // module's own clearAllAssistantHistory (what sign-out calls, in both
  // api.js and app.js) removes every sh-assistant- key.
  {
    const p11 = await person('organiser', 1280)
    await openOverview(p11.page)
    const me = await p11.page.evaluate(async () => {
      const r = await fetch('/v1/me', { headers: { 'X-API-Key': localStorage.getItem('apiKey') } })
      return r.json()
    })
    await openPanel(p11.page)
    p11.st.postHandler = async route => route.fulfill({ status: 200, contentType: 'text/event-stream', body: sse([{ delta: 'keyed reply' }, { done: true, remaining_today: 9 }]) })
    await sendText(p11.page, 'whose history is this')
    await p11.page.waitForFunction(() => document.querySelector('.assistant-msg.assistant')?.innerText.includes('keyed reply'))
    const expectedKey = 'sh-assistant-' + me.id + '-' + info.slug
    const stored = await p11.page.evaluate(k => localStorage.getItem(k), expectedKey)
    assert.ok(stored, 'history key is sh-assistant-<userId>-<slug>: ' + expectedKey)
    const remainingAfterClear = await p11.page.evaluate(async () => {
      const mod = await import('/hack/assistant.js')
      mod.clearAllAssistantHistory()
      return Object.keys(localStorage).filter(k => k.indexOf('sh-assistant-') === 0).length
    })
    assert.equal(remainingAfterClear, 0, 'clearAllAssistantHistory (called by sign-out in api.js/app.js) removes every sh-assistant- key')
    await p11.context.close()
    console.log('ok: history key includes the signed-in user id; sign-out cleanup clears it (M7)')
  }

  // 23) L2: GET's budget_reached disables the input at mount, with the
  // monthly-budget line (formatted resets date included).
  {
    const p12 = await person('organiser', 1280)
    p12.st.getData = { enabled: true, daily_limit: 30, used_today: 30, remaining_today: 0, budget_reached: true, resets: '2026-12-01' }
    await openOverview(p12.page)
    await openPanel(p12.page)
    assert.ok(await p12.page.locator('[data-assistant-input]').isDisabled(), 'input disabled when budget_reached at mount')
    assert.ok(await p12.page.locator('[data-assistant-send]').isDisabled(), 'send disabled when budget_reached at mount')
    assert.match(await p12.page.locator('[data-assistant-hint]').innerText(), /used this month's budget.*1 December/)
    await p12.context.close()
    console.log('ok: GET budget_reached disables input with the monthly line at mount (L2)')
  }

  // 24) M8: Esc must not close the assistant panel while a ui.js confirm
  // dialog (.scrim) is open on top of it -- the dialog owns Esc.
  {
    const p13 = await person('organiser', 1280)
    await openOverview(p13.page)
    await openPanel(p13.page)
    const endBtn = p13.page.locator('#ov-end')
    if (await endBtn.count()) {
      // The desktop assistant panel is position:fixed on the right 400px
      // of the viewport and visually sits on top of this danger-zone
      // button at this viewport width -- that's an existing, intentional
      // layout (the panel and the page underneath both stay usable), not
      // what this check is about. A real click at that screen position
      // would hit the panel, not the button, so dispatch the click event
      // on the button directly instead of simulating a pointer click.
      await endBtn.dispatchEvent('click')
      await p13.page.locator('.scrim').waitFor()
      await p13.page.keyboard.press('Escape')
      await p13.page.waitForTimeout(150)
      assert.equal(await p13.page.locator('.assistant-panel').count(), 1, 'Esc with a confirm dialog open does not close the assistant panel')
      if (await p13.page.locator('.scrim').count()) await p13.page.keyboard.press('Escape')
      console.log('ok: Esc with a ui.js dialog open leaves the assistant panel alone (M8)')
    } else {
      console.log('skip: no danger-zone End button on this fixture event to open a scrim with (M8 guard still present in code)')
    }
    await p13.context.close()
  }

  // Desktop screenshot of the open panel with a short exchange, for a visual check.
  // Fresh thread first -- step 11's failed sends still logged their user
  // messages (a send always records what was asked, even on error).
  await openOverview(page)
  await openPanel(page)
  await page.locator('.assistant-head-acts button:has-text("New chat")').click()
  await page.locator('.assistant-chips .chip').first().waitFor()
  st.postHandler = async route => route.fulfill({ status: 200, contentType: 'text/event-stream', body: sse([{ delta: 'Press **Close entries** on Home when building time is over.' }, { done: true, remaining_today: 19 }]) })
  await page.locator('.assistant-chips .chip').first().click()
  await page.waitForFunction(() => document.querySelectorAll('.assistant-msg').length >= 2)
  await page.screenshot({ path: `${shots}/desktop-panel.png` })

  assert.deepEqual(pageErrors, [], 'no page errors: ' + pageErrors.join(' | '))
  console.log('ALL ASSISTANT CHECKS PASSED')
} finally {
  if (!process.env.KEEP_NAVIGATION_FIXTURE) await fetch(info.url + '/_fixture/stop', { method: 'POST' }).catch(() => {})
  await browser.close()
}
