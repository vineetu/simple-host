#!/usr/bin/env node
// Uses TestServeHackNavigationBrowser's real API/DB fixture. Run navigation first,
// with KEEP_NAVIGATION_FIXTURE=1, then this script against the same fixture file.
import fs from 'node:fs'
import { execFileSync } from 'node:child_process'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
const require = createRequire('/tmp/tsx/node_modules/')
const { chromium } = require('playwright')
const info = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'))
const shots = '.shots/slice2'
fs.mkdirSync(shots, { recursive: true })
const browser = await chromium.launch({ executablePath: '/usr/local/bin/chromium', args: ['--no-sandbox'] })
const errors = []
const base = '/v1/hack/events/' + info.slug
async function api(role, method, path, body) {
 const r = await fetch(info.url + path, { method, headers: { 'X-API-Key': info.keys[role], 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) })
 const data = r.status === 204 ? {} : await r.json()
 assert.ok(r.ok, `${method} ${path}: ${r.status} ${JSON.stringify(data)}`)
 return data
}
async function person(role, width) {
 const context = await browser.newContext({ viewport: { width, height: width === 390 ? 844 : 800 } })
 await context.addInitScript(key => localStorage.setItem('apiKey', key), info.keys[role])
 const page = await context.newPage()
 page.on('pageerror', e => errors.push(e.message))
 let before = false
 await page.route('**/hack/**', async route => {
  const path = new URL(route.request().url()).pathname
  const file = 'internal/webassets/static' + path
  if (!fs.existsSync(file)) return route.continue()
  const body = before ? execFileSync('git', ['show', 'HEAD:' + file]) : fs.readFileSync(file)
  await route.fulfill({ body, contentType: path.endsWith('.css') ? 'text/css' : 'text/javascript' })
 })
 await page.route('**/v1/hack/preferences', route => route.fulfill({ json: { organiser_walkthrough_done: true, judge_walkthrough_done: true, participant_walkthrough_done: true } }))
 return { page, context, baseline: value => { before = value } }
}
async function home(page, role) {
 await page.goto(info.url + `/e/${info.slug}/${{organiser:'manage?s=overview',judge:'judge?s=home',participant:'team?s=home'}[role]}`)
 await page.locator('[data-checklist-id]').first().waitFor()
}
async function state(page, id, done) {
 await page.waitForFunction(({id, done}) => document.querySelector(`[data-checklist-id="${id}"]`)?.classList.contains('done') === done, {id, done})
 assert.equal(await page.locator(`[data-checklist-id="${id}"]`).evaluate(el => el.classList.contains('done')), done, id)
}
async function rowPresentation(page, width) {
 const folded = await page.locator('main details').evaluateAll(ds => ds.map(d => d.open))
 await page.locator('main details').evaluateAll(ds => ds.forEach(d => { d.open = true }))
 const rows = page.locator('[data-checklist-id]')
 let number = 0
 for (const row of await rows.all()) {
  number++
  const num = row.locator('.num')
  if (await num.count()) assert.equal(await num.innerText(), String(number), 'continuous numbering')
  const id = await row.getAttribute('data-checklist-id')
  const verb = {'j.rubric':'Read','j.conflicts':'Check','j.scores':'Score'}[id]
  if (verb && await row.locator('a').count()) assert.equal(await row.locator('a').innerText(), verb)
  if (width !== 390 || !(await row.isVisible())) continue
  const label = await row.locator('.what b').boundingBox()
  for (const action of await row.locator('.act').all()) {
   const box = await action.boundingBox()
   assert.ok(box.height <= 36, 'compact row action')
   assert.ok(Math.abs(box.y - label.y) <= 3, 'action on label line')
   assert.ok(Math.abs(box.x + box.width - (await row.boundingBox()).x - (await row.boundingBox()).width) <= 1, 'action right aligned')
  }
 }
 await page.locator('main details').evaluateAll((ds, states) => ds.forEach((d, i) => { d.open = states[i] }), folded)
}
const org = await person('organiser', 1280)
try {
 // Both judges and organiser see the judge projection, never mixed organiser rows.
 await api('organiser', 'POST', base + '/stage', {stage:'closed'})
 const rubric = (await api('judge', 'GET', base + '/rubric')).criteria
 const conflicts = (await api('judge','GET',base+'/conflicts?mine=1')).conflicts || []
 for (const c of conflicts) await api('judge','DELETE',base+'/conflicts/'+c.team_id)
 const judge = await person('judge',1280)
 await home(judge.page,'judge')
 await rowPresentation(judge.page,1280)
 await judge.page.goto(info.url+`/e/${info.slug}/judge?s=rubric`)
 await judge.page.getByRole('button',{name:"I've read it",exact:true}).click()
 await home(judge.page,'judge'); await state(judge.page,'j.rubric',true)
 await judge.page.goto(info.url+`/e/${info.slug}/judge?s=queue&tab=conflicts#conflicts`)
 await judge.page.getByRole('button',{name:"I don't know any of these teams",exact:true}).click()
 await home(judge.page,'judge'); await state(judge.page,'j.conflicts',true)
 const queue = (await api('judge','GET',base+'/judge/queue')).queue
 assert.ok(queue.length >= 2)
 await api('judge','PUT',base+'/judge/scores/'+queue[0].team_id,{ scores:rubric.map(c=>({criterion_id:c.id,points:1})),comment:'' })
 await home(judge.page,'judge')
 assert.match(await judge.page.locator('[data-checklist-id="j.scores"]').innerText(),/1 of 2/)
 const next = (await api('judge','GET',base+'/checklist?role=judge')).checklist.next
 assert.ok(!next.link.href.includes(queue[0].team_id))
 await judge.page.goto(info.url+`/e/${info.slug}/judge?s=queue&tab=conflicts#conflicts`)
 await judge.page.locator('[data-declare-conflict]').first().click()
 await judge.page.getByRole('button',{name:'Declare conflict',exact:true}).click()
 await judge.page.locator('[data-remove-conflict]').first().waitFor()
 await judge.context.close()
 for (const width of [1280,390]) {
  const j = await person('judge',width); await home(j.page,'judge');
  assert.ok(!(await j.page.locator('[data-checklist-id="j.closes"]').innerText()).includes('Optional'))
  fs.writeFileSync(`${shots}/judge-home-main.html`, await j.page.locator('main').innerHTML()); await rowPresentation(j.page,width); await j.page.screenshot({path:`${shots}/judge-home-${width}.png`}); await j.context.close()
  const o = await person('organiser',width)
  await o.page.goto(info.url+`/e/${info.slug}/judge?s=home`); await o.page.locator('[data-checklist-id="j.rubric"]').waitFor()
  assert.equal(await o.page.locator('[data-checklist-id="j.invite"], [data-checklist-id="j.coc"]').count(),0)
  await rowPresentation(o.page,width)
  fs.writeFileSync(`${shots}/judge-home-organiser-main.html`, await o.page.locator('main').innerHTML()); await o.page.screenshot({path:`${shots}/judge-home-organiser-${width}.png`}); await o.context.close()
 }
 await api('organiser','POST',base+'/stage',{stage:'draft'})
 await api('organiser','PATCH',base,{about:''})
 await home(org.page,'organiser'); await state(org.page,'o.about',false)
 await org.page.goto(info.url+`/e/${info.slug}/manage?s=event-page&tab=about`)
 const about = org.page.locator('textarea').first(); await about.fill('Build something useful.')
 await Promise.all([org.page.waitForResponse(r => r.request().method() === 'PATCH' && new URL(r.url()).pathname === base), org.page.getByRole('button',{name:'Save',exact:true}).first().click()])
 await home(org.page,'organiser'); await state(org.page,'o.about',true)
 // Dismiss and restore the same row; focus survives its changed control.
 const faq = org.page.locator('[data-checklist-id="o.faq"]')
 await faq.getByRole('button',{name:'Not needed',exact:true}).click()
 await org.page.locator('[data-checklist-id="o.faq"]').getByRole('button',{name:'Use this item',exact:true}).waitFor()
 assert.equal(await org.page.evaluate(()=>document.activeElement.textContent),'Use this item')
 await org.page.locator('[data-checklist-id="o.faq"]').getByRole('button',{name:'Use this item',exact:true}).click()
 await org.page.locator('[data-checklist-id="o.faq"]').getByRole('button',{name:'Not needed',exact:true}).waitFor()
 await org.page.goto(info.url+`/e/${info.slug}/manage?s=event-page&tab=faq`)
 await org.page.locator('#fq-add').click()
 await org.page.locator('#d-q').fill('When?'); await org.page.locator('#d-a').fill('Today.')
 await org.page.getByRole('dialog').getByRole('button',{name:'Add question',exact:true}).click()
 await org.page.getByRole('dialog').waitFor({state:'hidden'})
 await home(org.page,'organiser'); await state(org.page,'o.faq',true)
 assert.equal(await org.page.locator('[data-checklist-id="o.faq"] button').count(),0)
 assert.ok(!(await org.page.locator('[data-checklist-id="o.faq"]').innerText()).includes('Optional'))
 await org.page.goto(info.url+`/e/${info.slug}/manage?s=settings&tab=judging`)
 await Promise.all([org.page.waitForResponse(r => r.request().method() === 'POST' && r.url().includes('/acknowledgements/o.rubric-ready')), org.page.getByRole('button',{name:'Mark as final',exact:true}).click()])
 await api('organiser','POST',base+'/stage',{stage:'closed'})
 await home(org.page,'organiser'); await state(org.page,'o.rubric-ready',true)
 await api('organiser','POST',base+'/stage',{stage:'open'})
 await org.page.goto(info.url+`/e/${info.slug}/manage?s=settings&tab=entries#deadline`)
 await org.page.locator('#deadline-input').fill('2026-12-01T18:00')
 await Promise.all([org.page.waitForResponse(r => r.request().method() === 'PATCH' && new URL(r.url()).pathname === base), org.page.locator('#deadline-save').click()])
 await home(org.page,'organiser'); await state(org.page,'o.deadline',true)
 // Participant visual proof uses the identical API facts for both renderers.
 await api('organiser','POST',base+'/stage',{stage:'building'})
 await api('participant','POST',base+'/entry/unsubmit',{})
 const full={title:'A Title',problem:'A problem',tagline:'A tagline',audience:'Everyone',tools:['Go'],maturity:'working',video_url:'https://example.com/v'}
 await api('participant','PUT',base+'/entry',{problem:'',video_url:''})
 const participant = await person('participant',1280)
 await home(participant.page,'participant'); await state(participant.page,'p.summary',false)
 await api('participant','PUT',base+'/entry',{...full,video_url:''})
 await home(participant.page,'participant'); await state(participant.page,'p.summary',true); await state(participant.page,'p.evidence',false)
 await api('participant','PUT',base+'/entry',full)
 await home(participant.page,'participant'); await state(participant.page,'p.evidence',true)
 await participant.context.close()
 for (const variant of ['building','submitted','missing-evidence','no-team']) {
  if (variant === 'submitted') {
   const p = await person('participant',1280); await home(p.page,'participant')
   await p.page.locator('#ph-submit-step').click()
   await p.page.getByRole('button',{name:'Submit entry',exact:true}).click()
   await state(p.page,'p.submit',true); await p.context.close()
  }
  if (variant === 'missing-evidence') {await api('participant','POST',base+'/entry/unsubmit',{}); await api('participant','PUT',base+'/entry',{video_url:''})}
  if (variant === 'no-team') await api('participant','POST',base+'/teams/leave',{})
  for (const width of [1280,390]) {
   const p = await person('participant',width)
   p.baseline(true); await p.page.goto(info.url+`/e/${info.slug}/team?s=home`)
   await p.page.locator('[data-tour="entry-checklist"]').waitFor()
   const oldPage = await p.page.screenshot({path:`${shots}/participant-${variant}-before-${width}.png`})
   const old = await p.page.locator('[data-tour="entry-checklist"]').screenshot()
   p.baseline(false); await home(p.page,'participant')
   const currentPage = await p.page.screenshot({path:`${shots}/participant-${variant}-${width}.png`})
   assert.ok(oldPage.equals(currentPage),`participant Home pixels: ${variant} ${width}`)
   const current = await p.page.locator('[data-tour="entry-checklist"]').screenshot()
   assert.ok(old.equals(current),`participant pixels: ${variant} ${width}`)
   if (variant==='submitted') await state(p.page,'p.submit',true)
   if (variant==='missing-evidence') await state(p.page,'p.evidence',false)
   if (variant==='no-team') await state(p.page,'p.team',false)
   await p.context.close()
  }
 }
 for (const stage of ['draft','open','building','closed','judging','results','archived']) {
  await api('organiser','POST',base+'/stage',{stage})
  await home(org.page,'organiser')
  fs.writeFileSync(`${shots}/organiser-home-${stage}-main.html`, await org.page.locator('main').innerHTML())
  const checklist=(await api('organiser','GET',base+'/checklist')).checklist
  await rowPresentation(org.page, org.page.viewportSize().width)
  for (const item of checklist.items) {
   assert.ok(!/0 of 0/.test(item.detail), 'empty count detail')
   if (item.group === 'earlier_required') assert.ok(item.actionable && item.state !== 'done')
   if (item.id === 'o.entry-rules' && ['closed','judging','results'].includes(stage)) {
    assert.equal(item.group,'earlier_optional')
    assert.equal(await org.page.locator('[data-checklist-id="o.entry-rules"]').evaluate(row => row.closest('details').open),false)
   }
  }
  const expected = {draft:['o.entry-rules','o.basics','o.about','o.rubric'],open:['o.announcement','o.deadline'],building:['o.announcement','o.deadline'],closed:['o.judges','o.rubric-ready'],judging:['o.scores','o.lock'],results:['o.scores','o.lock','o.ties','o.publish','o.end'],archived:['o.end']}[stage]
  for (const id of expected) assert.ok(checklist.items.some(i=>i.id===id),stage+' '+id)
  const ids=await org.page.locator('[data-checklist-id]').evaluateAll(rows=>rows.map(r=>r.dataset.checklistId))
  assert.deepEqual(ids.slice().sort(),checklist.items.map(i=>i.id).sort(),stage)
  if (['draft','closed','results'].includes(stage)) for (const width of [1280,390]) {
   await org.page.setViewportSize({width,height:width===390?844:800}); await home(org.page,'organiser')
   await rowPresentation(org.page,width)
   if (width === 390) {
    const title = await org.page.locator('main h1').boundingBox()
    for (const control of ['#ov-change-stage', 'main .ph a']) {
     const box = await org.page.locator(control).first().boundingBox()
     assert.ok(box.y >= title.y + title.height, 'Home actions must follow title')
    }
    for (const button of await org.page.locator('[data-checklist-id] .act.btn').all()) {
     if (await button.isVisible()) assert.ok((await button.boundingBox()).height <= 36, 'checklist row action too tall')
    }
   }
   await org.page.screenshot({path:`${shots}/organiser-home-${stage}-${width}.png`})
  }
  console.log('ok organiser stage',stage)
 }
 assert.deepEqual(errors,[])
 console.log('ok checklist actions, judge projection and scores, participant exact card pixels; screenshots',shots)
 if (!process.env.KEEP_NAVIGATION_FIXTURE) await fetch(info.url+'/_fixture/stop',{method:'POST'})
} finally { await org.context.close(); await browser.close() }
