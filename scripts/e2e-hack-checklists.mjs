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
  const pinned = file.endsWith('/screens/participant-home.js') || file.endsWith('/kit.css')
  const body = before && pinned ? execFileSync('git', ['show', '3ef2a7e5:' + file]) : fs.readFileSync(file)
  await route.fulfill({ body, contentType: path.endsWith('.css') ? 'text/css' : 'text/javascript' })
 })
 await page.route('**/v1/hack/preferences', route => route.fulfill({ json: { organiser_walkthrough_done: true, judge_walkthrough_done: true, participant_walkthrough_done: true } }))
 return { page, context, baseline: value => { before = value } }
}
async function home(page, role) {
 await page.goto(info.url + `/e/${info.slug}/${{organiser:'manage?s=overview',judge:'judge?s=home',participant:'team?s=home'}[role]}`)
 await page.locator(role === 'participant' ? 'main h1' : '[data-checklist-id]').first().waitFor()
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
 await api('organiser','PATCH',base+'/judging/settings',{include_organisers:true})
 // Navigation leaves results published, which closes judging.
 assert.equal((await fetch(info.url+'/_fixture/clear-results',{method:'POST'})).status,204)
 const rubric = (await api('judge', 'GET', base + '/rubric')).criteria
 const conflicts = (await api('judge','GET',base+'/conflicts?mine=1')).conflicts || []
 for (const c of conflicts) await api('judge','DELETE',base+'/conflicts/'+c.team_id)
 const judge = await person('judge',1280)
 await home(judge.page,'judge')
 await rowPresentation(judge.page,1280)
 await judge.page.route('**/checklist?role=judge', async route => {
  await judge.page.unroute('**/checklist?role=judge')
  const shownRubric=await api('organiser','GET',base+'/rubric')
  await api('organiser','PUT',base+'/rubric',{criteria:shownRubric.criteria.map((c,i)=>({...c,description:c.description+(i===0?' Updated brief.':'')}))})
  await route.continue()
 })
 await judge.page.goto(info.url+`/e/${info.slug}/judge?s=rubric`)
 const refused = judge.page.waitForResponse(r=>r.url().includes('/acknowledgements/j.rubric') && r.status()===409)
 await judge.page.getByRole('button',{name:"I've read it",exact:true}).click()
 await refused
 await judge.page.getByRole('button',{name:"I've read it",exact:true}).waitFor()
 await judge.page.getByRole('button',{name:"I've read it",exact:true}).click()
 await judge.page.getByRole('button',{name:'Done',exact:true}).waitFor()
 await home(judge.page,'judge'); await state(judge.page,'j.rubric',true)
 await api('organiser', 'POST', base + '/stage', {stage:'judging'})
 // Judge conflicts open at Judging (SIM-416).
 await judge.page.goto(info.url+`/e/${info.slug}/judge?s=queue&tab=conflicts#conflicts`)
 await judge.page.getByRole('button',{name:"I don't know any of these teams",exact:true}).click()
 await judge.page.getByRole('button',{name:'Done',exact:true}).waitFor()
 await home(judge.page,'judge'); await state(judge.page,'j.conflicts',true)
 const queue = (await api('judge','GET',base+'/judge/queue')).queue
 assert.ok(queue.length >= 2)
 await api('judge','PUT',base+'/judge/scores/'+queue[0].team_id,{ scores:rubric.map(c=>({criterion_id:c.id,points:1})),comment:'' })
 await home(judge.page,'judge')
 assert.match(await judge.page.locator('[data-checklist-id="j.scores"]').innerText(),/1 of 2/)
 const next = (await api('judge','GET',base+'/checklist?role=judge')).checklist.next
 assert.ok(!next.link.href.includes(queue[0].team_id))
 const conflictsReady = judge.page.waitForResponse(r=>r.url().includes('/checklist?role=judge'))
 await judge.page.goto(info.url+`/e/${info.slug}/judge?s=queue&tab=conflicts#conflicts`)
 await conflictsReady
 await judge.page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))))
 await judge.page.locator('[data-declare-conflict]').first().click()
 await judge.page.getByRole('button',{name:'Declare conflict',exact:true}).click()
 await judge.page.locator('[data-remove-conflict]').first().waitFor()
 await judge.context.close()
 console.log('ok judge rubric race, acknowledgements, scores and conflicts')
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
 // The row re-renders with the new control first, then focus moves to it.
 await org.page.waitForFunction(()=>document.activeElement?.textContent==='Use this item')
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
 await org.page.route('**/checklist', async route => {
  await org.page.unroute('**/checklist')
  const shownFinal=await api('organiser','GET',base+'/rubric')
  await api('organiser','PUT',base+'/rubric',{criteria:shownFinal.criteria.map((c,i)=>({...c,description:c.description+(i===0?' Final brief.':'')}))})
  await route.continue()
 })
 await org.page.goto(info.url+`/e/${info.slug}/manage?s=settings&tab=judging`)
 await Promise.all([org.page.waitForResponse(r=>r.url().includes('/acknowledgements/o.rubric-ready') && r.status()===409),org.page.getByRole('button',{name:'Mark as final',exact:true}).click()])
 await org.page.getByRole('button',{name:'Mark as final',exact:true}).waitFor()
 await Promise.all([org.page.waitForResponse(r => r.request().method() === 'POST' && r.url().includes('/acknowledgements/o.rubric-ready')), org.page.getByRole('button',{name:'Mark as final',exact:true}).click()])
 await api('organiser','POST',base+'/stage',{stage:'closed'})
 await home(org.page,'organiser'); await state(org.page,'o.rubric-ready',true)
 await api('organiser','POST',base+'/stage',{stage:'open'})
 await org.page.goto(info.url+`/e/${info.slug}/manage?s=settings&tab=entries#deadline`)
 await org.page.locator('#deadline-input').fill('2026-12-01T18:00')
 await Promise.all([org.page.waitForResponse(r => r.request().method() === 'PATCH' && new URL(r.url()).pathname === base), org.page.locator('#deadline-save').click()])
 await home(org.page,'organiser'); await state(org.page,'o.deadline',true)
 console.log('ok organiser rubric race and checklist writes')
 // Published untied standings, then reopened scoring creates a new tie.
 await api('organiser','POST',base+'/stage',{stage:'judging'})
 const tieQueue=(await api('organiser','GET',base+'/judge/queue')).queue
 const tieRubric=(await api('organiser','GET',base+'/rubric')).criteria
 for (let i=0;i<tieQueue.length;i++) await api('organiser','PUT',base+'/judge/scores/'+tieQueue[i].team_id,{scores:tieRubric.map(c=>({criterion_id:c.id,points:i+3})),comment:''})
 await api('organiser','POST',base+'/stage',{stage:'results'})
 await api('organiser','POST',base+'/results/publish',{})
 // Remove judge's conflict and align both judges' scores for the new tied group.
 await api('organiser','POST',base+'/stage',{stage:'judging'})
 for (const c of (await api('judge','GET',base+'/conflicts?mine=1')).conflicts||[]) await api('judge','DELETE',base+'/conflicts/'+c.team_id)
 for (const who of ['organiser','judge']) for (const team of (await api(who,'GET',base+'/judge/queue')).queue) await api(who,'PUT',base+'/judge/scores/'+team.team_id,{scores:tieRubric.map(c=>({criterion_id:c.id,points:3})),comment:''})
 await api('organiser','POST',base+'/stage',{stage:'results'})
 await home(org.page,'organiser'); await state(org.page,'o.ties',false)
 // The same-page hash link remounts Home (SIM-425); click the new button.
 const stale = await org.page.locator('#ov-next-move').elementHandle()
 await org.page.locator('[data-checklist-id="o.ties"] a').click()
 await org.page.waitForFunction(el => !el.isConnected, stale)
 await org.page.locator('#ov-next-move').click()
 await org.page.locator('.tie-group input').first().waitFor()
 const tieInputs=await org.page.locator('.tie-group input').all()
 assert.equal(tieInputs.length,2,'new tie must use current preview')
 for (let i=0;i<tieInputs.length;i++) await tieInputs[i].fill(String(i+1))
 await org.page.getByRole('dialog').getByRole('button',{name:'Publish again',exact:true}).click()
 await org.page.getByRole('button',{name:"Don't send",exact:true}).click()
 await home(org.page,'organiser'); await state(org.page,'o.ties',true)
 await org.page.goto(info.url+`/e/${info.slug}/manage?s=results`)
 await org.page.locator('#res-status').waitFor()
 assert.equal(await org.page.locator('.tie-group input').count(),0,'tie editors only appear in Home publish dialog')
 await api('organiser','POST',base+'/stage',{stage:'judging'})
 assert.equal((await fetch(info.url+'/_fixture/clear-results',{method:'POST'})).status,204)
 console.log('ok changed tie after publication: checklist to override and publish')
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
 for (const variant of ['building','submitted','missing-evidence','settled-incomplete','team-extension','backwards-published','no-team']) {
  if (variant === 'submitted') {
   const p = await person('participant',1280); await home(p.page,'participant')
   await p.page.locator('#ph-submit-step').click()
   await p.page.getByRole('button',{name:'Submit entry',exact:true}).click()
   await state(p.page,'p.submit',true); await p.context.close()
  }
  if (variant === 'missing-evidence') {await api('participant','POST',base+'/entry/unsubmit',{}); await api('participant','PUT',base+'/entry',{video_url:''})}
  if (variant === 'settled-incomplete') await api('organiser','POST',base+'/stage',{stage:'judging'})
  if (variant === 'team-extension') {
   await api('organiser','POST',base+'/stage',{stage:'building'})
   await api('organiser','PATCH',base,{submission_deadline:new Date(Date.now()-3600000).toISOString()})
   const ev=await api('participant','GET',base)
   // 50 s past the hour, so the minute countdown cannot tick between a before/after pair.
   await api('organiser','PUT',base+'/teams/'+ev.me.team.slug+'/deadline',{deadline:new Date(Date.now()+7250000).toISOString()})
  }
  if (variant === 'backwards-published') {
   await api('organiser','POST',base+'/stage',{stage:'results'})
   await api('organiser','POST',base+'/results/publish',{})
   await api('organiser','POST',base+'/stage',{stage:'building'})
  }
  if (variant === 'no-team') {assert.equal((await fetch(info.url+'/_fixture/clear-results',{method:'POST'})).status,204);await api('participant','POST',base+'/teams/leave',{})}
  const participantFacts=await api('participant','GET',base)
  if(variant==='settled-incomplete') assert.equal(participantFacts.progress.steps.find(s=>s.id==='submit').status,'not_submitted')
  if(variant==='team-extension') assert.equal(participantFacts.progress.deadline_is_extension,true)
  if(variant==='backwards-published') {assert.equal(participantFacts.event.stage,'building');assert.equal(participantFacts.event.results_published,true)}
  for (const width of [1280,390]) {
   const p = await person('participant',width)
   p.baseline(true); await p.page.goto(info.url+`/e/${info.slug}/team?s=home`)
   await p.page.locator('main h1').waitFor()
   await p.page.screenshot({path:`${shots}/participant-${variant}-before-${width}.png`})
   const oldPage=await p.page.locator('main').screenshot()
   const oldCard=p.page.locator('[data-tour="entry-checklist"]')
   const old = await oldCard.count() ? await oldCard.screenshot() : null
   p.baseline(false); await home(p.page,'participant')
   await p.page.screenshot({path:`${shots}/participant-${variant}-${width}.png`})
   const currentPage=await p.page.locator('main').screenshot()
   assert.ok(oldPage.equals(currentPage),`participant Home pixels: ${variant} ${width}`)
   const currentCard=p.page.locator('[data-tour="entry-checklist"]')
   const current = await currentCard.count() ? await currentCard.screenshot() : null
   assert.ok(old ? current && old.equals(current) : current === null,`participant pixels: ${variant} ${width}`)
   if (variant==='submitted') await state(p.page,'p.submit',true)
   if (variant==='missing-evidence') await state(p.page,'p.evidence',false)
   if (variant==='no-team') await state(p.page,'p.team',false)
   await p.context.close()
  }
  console.log('ok participant baseline',variant)
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
    // "Score as a judge" shows from Judging on, for an opted-in organiser (SIM-419).
    for (const control of ['#ov-change-stage', 'main .ph a']) {
     if (control !== '#ov-change-stage' && !await org.page.locator(control).count()) continue
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
