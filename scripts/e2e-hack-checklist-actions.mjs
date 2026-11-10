#!/usr/bin/env node
// Same local fixture as e2e-hack-checklists.mjs:
// KEEP_NAVIGATION_FIXTURE=1 scripts/e2e-hack-navigation.mjs, then
// node scripts/e2e-hack-checklist-actions.mjs /tmp/<navigation-fixture>.json
// This seeds its own event using the fixture accounts; never point at production.
import fs from 'node:fs'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
const require = createRequire('/tmp/tsx/node_modules/')
const { chromium } = require('playwright')
const info = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'))
assert.ok(['127.0.0.1', 'localhost', '[::1]'].includes(new URL(info.url).hostname), 'local fixture required')
const slug = 'checklist-actions-' + Date.now().toString(36)
const base = '/v1/hack/events/' + slug
async function api(role, method, path, body) {
 const r = await fetch(info.url + path, { method, headers: { 'X-API-Key': info.keys[role], 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) })
 const data = r.status === 204 ? {} : await r.json()
 assert.ok(r.ok, `${method} ${path}: ${r.status} ${JSON.stringify(data)}`)
 return data
}
const browser = await chromium.launch({ executablePath: '/usr/local/bin/chromium', args: ['--no-sandbox'] })
const errors = [], contexts = {}
const homes = {organiser:'manage?s=overview', judge:'judge?s=home', participant:'team?s=home'}
const seen = new Set()
async function person(role) {
 if (contexts[role]) return contexts[role].page
 const context = await browser.newContext({viewport:{width:1280,height:900}})
 await context.addInitScript(key => localStorage.setItem('apiKey', key), info.keys[role])
 const page = await context.newPage()
 page.on('pageerror', e => errors.push(e.message))
 await page.route('**/hack/**', async route => {
  const path = new URL(route.request().url()).pathname
  const file = 'internal/webassets/static' + path
  if (!fs.existsSync(file)) return route.continue()
  await route.fulfill({body:fs.readFileSync(file),contentType:path.endsWith('.css')?'text/css':'text/javascript'})
 })
 await page.route('**/v1/hack/preferences', route => route.fulfill({json:{organiser_walkthrough_done:true,judge_walkthrough_done:true,participant_walkthrough_done:true}}))
 contexts[role]={context,page}
 return page
}
async function home(page,role) {
 await page.goto(info.url + `/e/${slug}/${homes[role]}`)
 await page.locator('main h1').first().waitFor()
 await page.locator('main details').evaluateAll(ds => ds.forEach(d => {d.open=true}))
}
// Each item names its fixing control, independently of the navigation catalogue.
const fixes = {
 'o.entry-rules':'#rules-save', 'o.basics':'#f-title', 'o.times':'#f-start-time',
 'o.about':'#f-about', 'o.schedule':'#sched-add, #sched-add-empty',
 'o.faq':'#fq-add, #fq-add-empty', 'o.sponsors':'#sp-add, #sp-add-empty',
 'o.tracks':'#track-add', 'o.rubric':'#jg-add',
 'o.mode':'input[name="jg-mode"]', 'o.tie-break':'#jg-tie, #jg-add',
 'o.judging-closes':'#jg-closes-input', 'o.reminders':'#rem-n', 'o.announcement':'#ann-title',
 'o.deadline':'#deadline-input', 'o.judges':'[data-organiser-judge], #jg-judge-link button',
 'o.rubric-ready':'[data-rubric-final] button, #jg-add',
 'o.assignments':'[data-organiser-judge], #jg-judge-link button, #jg-grid-rows input[data-judge], #team-track-select, #track-add, #jg-conflicts-card select',
 'o.lock':'#ov-next-move', 'o.ties':'#ov-next-move',
 'o.publish':'#ov-next-move', 'o.end':'#ov-end-target button',
 'j.rubric':'main .card button',
 'j.conflicts':'[data-declare-conflict], [data-remove-conflict], #conflicts button',
 'j.scores':'#jsc-criteria [data-points], a[href*="s=scoring"]',
 'p.team':'#start-name, #copy-team-link', 'p.summary':'#en-title',
 'p.evidence':'#en-video_url, #en-code_url, #en-shot, #web-zip', 'p.submit':'#en-submit'
}
async function assertFix(page, item) {
 assert.ok(fixes[item.id], `${item.id}: no item-specific control selector`)
 const hash = new URL(page.url()).hash.slice(1)
 if (hash) await page.locator('[id=' + JSON.stringify(decodeURIComponent(hash)) + ']').waitFor({state:'visible'})
 const assignmentFixes = {
  'o.organiser-judges':'[data-organiser-judge], #jg-judge-link button',
  'o.assignments':'#jg-grid-rows input[data-judge]', 'o.panel-assignments':'#jg-grid-rows input[data-judge]',
  'o.team-track':'#team-track-select', 'o.tracks':'#track-add', 'o.conflicts':'#jg-conflicts-card select'
 }
 const selector = item.id==='o.assignments' ? assignmentFixes[item.target_id] : fixes[item.id]
 assert.ok(selector, `${item.id}: missing control for ${item.target_id}`)
 const controls = page.locator(':is(' + selector + '):visible:not([disabled]):not([readonly])')
 await controls.first().waitFor({state:'visible'})
 let found = false
 for (const control of await controls.all()) {
  if (await control.isVisible() && await control.isEnabled()) { found = true; break }
 }
 assert.ok(found, `${item.id}: its fixing control is not visible and enabled`)
}
async function audit(role,stage) {
 for (const width of [1280,390]) {
  const page = await person(role)
  await page.setViewportSize({width,height:900})
  await auditAtWidth(role,stage)
 }
}
async function auditAtWidth(role,stage) {
 const page = await person(role)
 const {checklist} = await api(role,'GET',base+'/checklist' + (role==='judge'?'?role=judge':''))
 for (const item of checklist.items) {
  await home(page,role)
  const row = page.locator(`[data-checklist-id="${item.id}"]`)
  // Participant Home intentionally hides its entire entry checklist after Judging.
  if (role==='participant' && ['judging','results','archived'].includes(stage)) {
   assert.equal(item.actionable,false); assert.equal(await row.count(),0); continue
  }
  await row.waitFor()
  const actions = row.locator('a,button:not([data-checklist-mark])')
  // Done rows keep no button on Home (unchanged); informational rows have none either.
  if (!item.actionable || item.state==='not_needed' || item.state==='done') {
   assert.equal(await actions.count(),0, `${role}/${stage}/${item.id}: informational row has action`)
   continue
  }
  if (!(await actions.count()>0)) console.log("DEBUG", JSON.stringify(item), await row.evaluate(e=>e.outerHTML))
  assert.ok(await actions.count()>0, `${role}/${stage}/${item.id}: missing action`)
  const action = actions.first()
  const href = await action.getAttribute('href')
  if (href) {
   await action.click()
   await assertFix(page,item)
  } else {
   // Submit is the existing direct participant action.
   assert.equal(item.id,'p.submit')
   const saved = page.waitForResponse(r => r.request().method()==='POST' && r.url().includes('/entry/submit'))
   await action.click()
   const dialog = page.getByRole('dialog')
   if (await dialog.count()) await dialog.getByRole('button',{name:/Submit/,exact:false}).click()
   assert.ok((await saved).ok(), 'direct submit failed')
  }
  seen.add(item.id)
 }
}
try {
 const created = await api('organiser','POST','/v1/hack/events',{slug,title:'Checklist actions',organiser_name:'Checklist organiser',expected_participants:10,time_zone:'UTC',organisation:'Checklist',contact_email:'checklist@example.com',purpose:'Exercise checklist controls',starts_at:'2027-01-01',ends_at:'2027-01-03'})
 assert.ok(created.event)
 // Draft starts without setup so all editor actions are exercised.
 await audit('organiser','draft')
 await api('organiser','PATCH',base,{tagline:'Build a useful thing',about:'Build together',submission_deadline:'2027-01-03T12:00:00Z'})
 await api('organiser','PUT',base+'/entry-rules',{preset:'video_or_website'})
 await api('organiser','POST',base+'/stage',{stage:'open'})
 const event = await api('organiser','GET',base)
 await api('participant','POST','/v1/hack/join/'+event.organiser.join_code,{accept_coc:true,display_name:'Checklist participant'})
 await api('judge','POST','/v1/hack/judge/'+event.organiser.judge_code,{accept_coc:true,display_name:'Checklist judge'})
 await audit('participant','open') // no team: Find a team
 await api('participant','POST',base+'/teams',{name:'Action team'})
 await audit('organiser','open'); await audit('judge','open'); await audit('participant','open')
 await api('organiser','POST',base+'/stage',{stage:'draft'})
 for (const role of ['organiser','judge','participant']) await audit(role,'draft')
 await api('organiser','POST',base+'/stage',{stage:'open'})
 // Make a summary complete so missing evidence controls (and direct Submit) appear.
 const fields = Object.fromEntries(['title','problem','tagline','audience','track','tools','maturity','description','video_url','site','code_url','screenshot'].map(k=>[k,k==='title'?'required':'off']))
 await api('participant','PUT',base+'/entry',{title:'Action entry'})
 for (const field of ['video_url','code_url','screenshot','site']) {
  await api('organiser','PUT',base+'/entry-rules',{fields:{...fields,[field]:'required'}})
  await audit('participant','open')
 }
 await api('organiser','PUT',base+'/entry-rules',{fields:{...fields,video_url:'required'}})
 await api('participant','PUT',base+'/entry',{video_url:'https://example.com/demo'})
 await audit('participant','open')
 await api('organiser','PUT',base+'/rubric' ,{criteria:[{name:'Impact',description:'Useful',weight:100,max_points:5}]})
 for (const stage of ['building','closed','judging','results']) {
  await api('organiser','POST',base+'/stage',{stage})
  if (stage==='closed') {
   for (const mode of ['automatic','manual','panel']) {
    await api('organiser','PATCH',base+'/judging/settings',{assignment_mode:mode,judges_per_team:2})
    await audit('organiser',stage)
   }
   // First a missing-track team, then the panel grid itself.
   const tracks = await api('organiser','PUT',base+'/tracks',{tracks:[{slug:'ideas',name:'Ideas'}]})
   await audit('organiser',stage)
   const teams = await api('organiser','GET',base+'/teams')
   await api('organiser','PUT',base+'/team/track',{team:teams.teams[0].slug,track:tracks[0].slug})
   await audit('organiser',stage)
   await api('organiser','PATCH',base+'/judging/settings',{assignment_mode:'open'})
   const rubric = await api('organiser','GET',base+'/rubric')
   await api('organiser','POST',base+'/checklist/acknowledgements/o.rubric-ready',{version:rubric.version})
   const page = await person('organiser')
   await page.goto(info.url+`/e/${slug}/manage?s=settings&tab=judging#organiser-judges`)
   await page.locator('[data-organiser-judge]').first().check()
   await Promise.all([page.waitForResponse(r=>r.request().method()==='PATCH' && r.url().endsWith('/judging/settings')),page.locator('#jg-mode-save').click()])
   const saved = await api('organiser','GET',base+'/judging/settings')
   assert.equal(saved.organiser_judges.length,1,'organiser checkbox saves with mode')
   await home(page,'organiser')
   await page.locator('#ov-next-move').click()
   await page.locator('[data-judging-setup] a').nth(3).waitFor()
   assert.equal(await page.locator('[data-judging-setup] a').count(),4,'Home confirmation has four setup links')
   await page.getByRole('button',{name:'Cancel',exact:true}).click()
   await page.locator('#ov-change-stage').click()
   await page.locator('#stage-pick').selectOption('judging')
   await page.locator('[data-judging-setup] a').nth(3).waitFor()
   assert.equal(await page.locator('[data-judging-setup] a').count(),4,'stage picker has same four setup links')
   await page.locator('[data-judging-setup] a').first().click()
   await page.locator('#mode input:not([disabled])').first().waitFor()
   assert.equal(await page.getByRole('dialog').count(),0,'fix link closes confirmation')
  }
  for (const role of ['organiser','judge','participant']) await audit(role,stage)
  const orgPage = await person('organiser')
  for (const width of [1280,390]) {
  await orgPage.setViewportSize({width,height:900})
  await home(orgPage,'organiser')
  const view = await api('organiser','GET',base)
  const expectedScoreLink = view.can.judge && ['judging','results','archived'].includes(stage)?1:0
  assert.equal(await orgPage.getByRole('link',{name:'Score as a judge',exact:true}).count(),expectedScoreLink,'Home stage gate')
  await orgPage.goto(info.url+`/e/${slug}/manage?s=judging`)
  await orgPage.locator('main h1').waitFor()
  assert.equal(await orgPage.getByRole('link',{name:'Score as a judge',exact:true}).count(),view.can.judge && ['judging','results','archived'].includes(stage)?1:0)
  }
 }
 await api('organiser','POST',base+'/stage',{stage:'archived'})
 for (const role of ['organiser','judge','participant']) await audit(role,'archived')
 assert.deepEqual(errors,[])
 console.log('Checklist action destinations verified:',[...seen].sort().join(', '))
} finally {
 await browser.close()
 // Remove the seeded event so reruns on the same fixture stay under the event cap.
 await fetch(info.url+base,{method:'DELETE',headers:{'X-API-Key':info.keys.organiser,'Content-Type':'application/json'},body:JSON.stringify({confirm:slug})}).catch(()=>{})
}
