// SIM-379: real Basics controls, PATCH payloads, event-zone display and phone layout.
// Run: node scripts/check-hack-event-times.mjs (PLAYWRIGHT_PACKAGE may override the local install).
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
const require = createRequire(process.env.PLAYWRIGHT_PACKAGE || '/opt/pw/node_modules/playwright/package.json')
const { chromium } = require('playwright')
const root = fileURLToPath(new URL('../internal/webassets/static/', import.meta.url))
const fixture = `<!doctype html><link rel="stylesheet" href="/hack/kit.css"><main class="page"></main><script type="module">
import * as ui from '/hack/ui.js';
import { render } from '/hack/screens/organiser-event-page.js';
import { merge } from '/hack/schedule.js';
window.ui = ui; window.schedule = {merge}; window.patches = [];
const ev = window.ev = { slug:'time-check', title:'Time check', tagline:'', stage:'draft', url:location.origin+'/event', time_zone:'Asia/Kolkata', starts_at:'', ends_at:'' };
const ctx = { main:document.querySelector('main'), ui, event:{event:ev}, params:new URLSearchParams('tab=basics'), alive:()=>true, refresh:async()=>{ ui.clearDirtyGuard(); await render(ctx); },
api: { api:async(method,path,body)=>{
 if(method==='PATCH') { window.patches.push(body); Object.assign(ev, body);
  for(const k of ['starts_at','ends_at']) if(body[k]) ev[k] = body[k]+(ev.time_zone==='Asia/Tokyo'?'+09:00':'+05:30');
  return {ok:true,data:ev};
 }
 return {ok:true,data:path.endsWith('/content') ? {schedule:[],fixed_schedule:[]} : {mode:'builtin',versions:[]}};
}}};
window.ctx = ctx; await render(ctx); window.ready = true;
</script>`
async function serve(route) {
  const path = new URL(route.request().url()).pathname
  try {
    if (path === '/') { await route.fulfill({contentType:'text/html', body:fixture}); return }
    if (path === '/v1/hack/directory') {
      await route.fulfill({json:{ upcoming: [
        { title:'Dates only', url:'/dates', time_zone:'Asia/Kolkata', starts_at:'2027-03-12T00:00:00+05:30', ends_at:'2027-03-14T00:00:00+05:30' },
        { title:'Timed', url:'/timed', time_zone:'Asia/Kolkata', starts_at:'2027-03-12T18:30:00+05:30', ends_at:'2027-03-14T20:15:00+05:30' }
      ]}}); return
    }
    const file = path === '/directory' ? '/hack-directory.html' : path
    await route.fulfill({contentType:file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'text/html', body:await fs.readFile(root + file)})
  } catch { await route.fulfill({status:404,body:''}) }
}
let browser
try {
  const origin = 'https://hack.test'
  browser = await chromium.launch({headless:true, args:['--no-sandbox']})
  const page = await browser.newPage({timezoneId:'America/Los_Angeles', viewport:{width:1280,height:800}})
  await page.route('**/*', serve)
  const errors = []
  page.on('pageerror', e => errors.push(e.message))
  await page.goto(origin)
  await page.waitForFunction(() => window.ready)
  await page.locator('#f-start').fill('2027-03-12')
  await page.locator('#f-end').fill('2027-03-14')
  async function save(count) {
    await page.locator('#f-save').click()
    await page.waitForFunction(n => window.patches.length === n && document.querySelector('#f-save').disabled, count)
  }
  await save(1)
  assert.deepEqual(await page.evaluate(() => [patches[0].starts_at, patches[0].ends_at]), ['2027-03-12T00:00','2027-03-14T00:00'])
  assert.equal(await page.locator('#f-start-time').inputValue(), '')
  assert.equal(await page.locator('#f-end-time').inputValue(), '')
  await page.locator('#f-start-time').fill('18:30')
  await page.locator('#f-end-time').fill('20:15')
  await save(2)
  assert.deepEqual(await page.evaluate(() => [patches[1].starts_at, patches[1].ends_at]), ['2027-03-12T18:30','2027-03-14T20:15'])
  assert.equal(await page.locator('#f-start-time').inputValue(), '18:30')
  assert.equal(await page.locator('#f-end-time').inputValue(), '20:15')
  await page.locator('#f-start-time').fill('12:00')
  await page.locator('#f-discard').click()
  assert.equal(await page.locator('#f-start-time').inputValue(), '18:30')
  await page.locator('#f-tz').selectOption('Asia/Tokyo')
  await save(3)
  assert.equal(await page.evaluate(() => 'starts_at' in patches[2] || 'ends_at' in patches[2]), false, 'zone-only save leaves boundaries for server reanchoring')
  await page.locator('#f-start-time').fill('')
  await page.locator('#f-end-time').fill('')
  await save(4)
  assert.deepEqual(await page.evaluate(() => [patches[3].starts_at, patches[3].ends_at]), ['2027-03-12T00:00','2027-03-14T00:00'])
  // Status header and fixed-row formatters see the same instant in the event
  // zone. Local midnight is a date-only default for BOTH a start and an end
  // (hack_create_event: "A date means midnight in time_zone") -- a midnight
  // end is not "explicit", it displays the same as a midnight start.
  await page.evaluate(() => {
    const ev = {time_zone:'Asia/Kolkata'};
    for(const [end, iso, timed] of [
      [false,'2099-03-12T18:30:00+05:30',true], [false,'2099-03-12T00:00:00+05:30',false],
      [true,'2099-03-12T23:59:00+05:30',true], [true,'2099-03-12T00:00:00+05:30',false]
    ]) {
      const text = ui.eventBoundaryDate(iso, ev);
      if (/\d:\d{2} [ap]m/.test(text) !== timed) throw Error('Boundary display: '+text);
      const next = ui.nextDeadline({...ev, [end?'ends_at':'starts_at']:iso});
      if(next[2] === timed) throw Error('Status header dateOnly');
      const row = schedule.merge([], [{key:end?'event_end':'event_start',start_at:iso,all_day:!timed}], ev)[0];
      if(row.all_day === timed) throw Error('Fixed row all_day');
    }
  })
  // Both date/time pairs occupy a complete phone row, with no horizontal overflow.
  await page.setViewportSize({width:390,height:844})
  const boxes = await page.locator('.field-row > .field').evaluateAll(nodes => nodes.map(n => ({x:n.getBoundingClientRect().x, width:n.getBoundingClientRect().width, y:n.getBoundingClientRect().y})))
  assert.equal(boxes[0].x, boxes[1].x)
  assert.equal(boxes[0].width, boxes[1].width)
  assert.ok(boxes[1].y > boxes[0].y)
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
  await page.goto(origin + '/directory')
  await page.locator('#upcoming-list .r').nth(1).waitFor()
  assert.doesNotMatch(await page.locator('#upcoming-list .r').nth(0).innerText(), /\d:\d{2} [ap]m/)
  assert.match(await page.locator('#upcoming-list .r').nth(1).innerText(), /6:30 pm.*8:15 pm/)
  assert.deepEqual(errors, [])
  console.log('PASS: Basics blank/set/clear times, round-trip, Discard, zone-only save, status/fixed rows, directory, phone layout')
} finally {
  await browser?.close()
}
