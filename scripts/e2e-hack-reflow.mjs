#!/usr/bin/env node
// HACK_NAVIGATION_FIXTURE=/tmp/reflow-fixture.json TEST_PKGS=./internal/hackhttp scripts/test-integration.sh -run TestServeHackNavigationBrowser
// PLAYWRIGHT_DIR=/tmp/tsx/node_modules/ node scripts/e2e-hack-reflow.mjs /tmp/reflow-fixture.json
// REFLOW_SHOTS=.shots/reflow REFLOW_BEFORE=/tmp/baseline REFLOW_WIDTHS=390,1280 REFLOW_ONLY=judge REFLOW_STATES=building,judging,results
// Seeds its own event; local fixtures only. KEEP_NAVIGATION_FIXTURE=1 leaves the fixture running.
import fs from 'node:fs'
import path from 'node:path'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
const require = createRequire(process.env.PLAYWRIGHT_DIR || '/tmp/tsx/node_modules/')
const { chromium } = require('playwright')
const info = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'))
assert.ok(['127.0.0.1', 'localhost', '[::1]'].includes(new URL(info.url).hostname), 'local fixture required')
const widths = [...new Set((process.env.REFLOW_WIDTHS || '390,700,800,820,900,1000,1100,1280,1440').split(',').map(Number))]
assert.ok(widths.every(w => Number.isInteger(w) && w > 0), 'REFLOW_WIDTHS must contain positive integers')
const concurrency = Number(process.env.REFLOW_CONCURRENCY ?? 4)
assert.ok(Number.isInteger(concurrency) && concurrency >= 1, 'REFLOW_CONCURRENCY must be a positive integer')
const states = new Set((process.env.REFLOW_STATES ?? 'building,judging,results').split(',').map(s => s.trim()))
if (process.env.REFLOW_BEFORE) for (const name of ['app.js','kit.css']) {
 const file = path.join(process.env.REFLOW_BEFORE, 'internal/webassets/static/hack', name)
 assert.ok(fs.existsSync(file) && fs.statSync(file).isFile(), `REFLOW_BEFORE is missing ${file}`)
}
const shots = process.env.REFLOW_SHOTS || '.shots/reflow'
const variants = process.env.REFLOW_BEFORE ? ['after', 'before'] : ['after']
const slug = 'reflow-' + Date.now().toString(36), base = '/v1/hack/events/' + slug
const report = [], errors = [], contexts = new Map()
let browser, seeded = false
async function api(role, method, endpoint, body) {
 const r = await fetch(info.url + endpoint, {method, headers:{'X-API-Key':info.keys[role], 'Content-Type':'application/json'}, body:body === undefined ? undefined : JSON.stringify(body)})
 const data = r.status === 204 ? {} : await r.json()
 assert.ok(r.ok, `${method} ${endpoint}: ${r.status} ${JSON.stringify(data)}`)
 return data
}
async function person(role, variant, worker = 0) {
 const key = role + '/' + variant + '/' + worker
 if (contexts.has(key)) return contexts.get(key)
 const context = await browser.newContext({viewport:{width:1280,height:900}})
 await context.addInitScript(key => { localStorage.setItem('apiKey', key); localStorage.setItem('sh_side', 'expanded') }, info.keys[role])
 const page = await context.newPage()
 const session = {context,page,at:key}
 page.on('pageerror', e => errors.push({variant, at:session.at, message:e.message}))
 const root = variant === 'before' ? path.join(process.env.REFLOW_BEFORE, 'internal/webassets/static') : 'internal/webassets/static'
 await page.route('**/hack/**', async route => {
  const pathname = new URL(route.request().url()).pathname
  const file = path.join(root, pathname)
  // API calls (/v1/hack/...) and assets outside the source tree go to the fixture.
  if (!fs.existsSync(file) || !fs.statSync(file).isFile()) {
   const current = path.join('internal/webassets/static', pathname)
   if (variant === 'before' && pathname.startsWith('/hack/') && fs.existsSync(current) && fs.statSync(current).isFile()) {
    errors.push({variant,at:session.at,message:`Baseline asset missing: ${file}`,fatal:true})
    return route.abort()
   }
   return route.continue()
  }
  const types = {'.css':'text/css','.js':'text/javascript','.html':'text/html','.svg':'image/svg+xml','.woff2':'font/woff2','.png':'image/png'}
  await route.fulfill({body:fs.readFileSync(file),contentType:types[path.extname(file)] || 'application/octet-stream'})
 })
 await page.route('**/v1/hack/preferences', route => route.fulfill({json:{organiser_walkthrough_done:true,judge_walkthrough_done:true,participant_walkthrough_done:true}}))
 // The fixture has no site service, so give judges site links in both variants.
 if (role === 'judge') await page.route('**/v1/hack/events/*/judge/queue', async route => {
  const response = await route.fetch()
  const json = await response.json()
  for (const row of json.queue || []) {
   row.site_exists = true
   row.site_url = 'https://' + row.team.slug + '.' + slug + '.simple-hack.app/'
  }
  await route.fulfill({response, json})
 })
 contexts.set(key, session)
 return session
}
// Runs at the original 900px height; screenshots expand the internal scroll area afterwards.
function measure() {
 const main = document.querySelector('main'), content = document.querySelector('.content'), doc = document.documentElement
 const issues = [], seen = new Set()
 function cssPath(el) {
  const parts = []
  for (let e = el; e && parts.length < 4; e = e.parentElement) {
   let part = e.tagName.toLowerCase()
   if (e.id) { parts.unshift(part + '#' + CSS.escape(e.id)); break }
   part += [...e.classList].slice(0,2).map(c => '.' + CSS.escape(c)).join('')
   if (e.parentElement) part += ':nth-child(' + ([...e.parentElement.children].indexOf(e) + 1) + ')'
   parts.unshift(part)
   if (e === main || e.matches('.status')) break
  }
  return parts.join(' > ')
 }
 function add(kind, el) {
  const issue = {kind,path:cssPath(el),text:(el.textContent || '').trim().replace(/\s+/g,' ').slice(0,40),width:Math.round(el.getBoundingClientRect().width * 100) / 100}
  const key = JSON.stringify(issue)
  if (!seen.has(key)) { seen.add(key); issues.push(issue) }
 }
 function visible(el) {
  const r = el.getBoundingClientRect(), s = getComputedStyle(el)
  return r.width >= 2 && r.height >= 2 && s.visibility !== 'hidden' && s.visibility !== 'collapse' && s.display !== 'none' && Number(s.opacity) !== 0 && !el.closest('[hidden],.sr-only,[aria-hidden="true"]') && el.checkVisibility({checkVisibilityCSS:true,checkOpacity:true})
 }
 if (doc.scrollWidth > doc.clientWidth + 1) add('overflow', doc)
 if (content && content.scrollWidth > content.clientWidth + 1) {
  // Name the element that sticks out, not just the scroller.
  const edge = content.getBoundingClientRect().left + content.clientWidth + 1
  const out = [...content.querySelectorAll('*')].filter(e => e.getBoundingClientRect().right > edge && !e.querySelector('*'))
  add('overflow', out[0] || content)
 }
 const roots = [main, document.querySelector('.status')].filter(Boolean)
 for (const root of roots) for (const el of [root, ...root.querySelectorAll('*')]) {
  if (!visible(el)) continue
  const r = el.getBoundingClientRect(), bounds = root.getBoundingClientRect(), s = getComputedStyle(el)
  const clipsText = el.textContent.trim() && (s.textOverflow === 'ellipsis' || ['hidden','clip'].includes(s.overflowX)) && el.clientWidth < el.scrollWidth
  let scroller = false, clipBounds
  for (let p = el === root ? null : el.parentElement; p; p = p.parentElement) {
   const overflow = getComputedStyle(p).overflowX
   if (['auto','scroll'].includes(overflow)) { scroller = true; break }
   if (!clipBounds && ['hidden','clip'].includes(overflow)) clipBounds = p.getBoundingClientRect()
   if (p === root) break
  }
  if (clipBounds && !clipsText && (r.right > clipBounds.right + 1 || r.left < clipBounds.left - 1)) add('overflow', el)
  if (!scroller && (r.right > bounds.right + 1 || r.left < bounds.left - 1)) add('overflow', el)
  // A text field squeezed below 5em cannot show what is typed in it.
  if (el.matches('input:not([type=checkbox],[type=radio],[type=file],[type=hidden],[type=color],[type=range]),textarea,select') && r.width < 5 * parseFloat(s.fontSize)) add('squeezed', el)
  if (el.matches('input,textarea,select,option')) continue
  const font = parseFloat(s.fontSize)
  if (clipsText && el.clientWidth < 4 * font) add('hidden-text', el)
  // Inline text (a link or bold run inside a sentence) wrapping at a line end is normal flow;
  // its block parent is what gets measured.
  if (['nowrap','pre'].includes(s.whiteSpace) || s.display === 'inline') continue
  const nodes = [...el.childNodes].filter(n => n.nodeType === Node.TEXT_NODE && n.textContent.trim())
  if (!nodes.length) continue
  const tops = new Set()
  for (const node of nodes) {
   const range = document.createRange(); range.selectNodeContents(node)
   for (const rect of range.getClientRects()) if (rect.width > 0 && rect.height > 0) tops.add(Math.round(rect.top / 2) * 2)
  }
  const words = nodes.map(n => n.textContent).join(' ').trim().split(/\s+/).length
  // Inline text owners have clientWidth=0; their bounding box still has the text width.
  const contentWidth = r.width - parseFloat(s.paddingLeft) - parseFloat(s.paddingRight) - parseFloat(s.borderLeftWidth) - parseFloat(s.borderRightWidth)
  // Squeezed: three or more lines in a box under 9em, or about one word per line under 16em.
  // A two-line label or one long token (an email, an address) breaking is not squeezing.
  if (tops.size >= 3 && (contentWidth < 9 * font || (words >= 2 && words / tops.size < 1.5 && contentWidth < 16 * font))) add('squeezed', el)
 }
 return issues
}
async function capture(state, screen, width, variant, worker) {
 const png = path.join(shots,state,`${screen.id}@${width}.${variant}.png`)
 const row = {state,screen:screen.id,width,variant,issues:[],png}
 report.push(row)
 let session
 try {
  session = await person(screen.role,variant,worker)
  session.at = `${state}/${screen.id}@${width}`
  const {page} = session
  await page.setViewportSize({width,height:900})
  await page.goto(info.url + screen.url)
  await page.waitForLoadState('networkidle')
  await page.locator(screen.selector || 'main h1').first().waitFor({state:'visible',timeout:10000})
  await page.locator('main details').evaluateAll(ds => ds.forEach(d => { d.open = true }))
  // Opening a <details> can start a lazy load; wait for it so before and after match.
  await page.waitForLoadState('networkidle')
  await page.waitForFunction(() => !/Loading…|Loading\.\.\./.test(document.querySelector('main')?.innerText || ''), null, {timeout:8000})
  await page.evaluate(async () => { await document.fonts.ready; await new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))) })
  if (await page.locator('main').innerText().then(t => t.includes('Something went wrong'))) throw new Error('Something went wrong card')
  row.issues = await page.evaluate(measure)
  const height = await page.evaluate(() => {
   const d = document.documentElement, c = document.querySelector('.content')
   return Math.min(6000,Math.max(900,d.scrollHeight,document.body.scrollHeight,c ? c.scrollHeight + innerHeight - c.clientHeight : 0))
  })
  await page.setViewportSize({width,height:Math.ceil(height)})
  fs.mkdirSync(path.dirname(png),{recursive:true})
  await page.screenshot({path:png,animations:'disabled'})
 } catch (e) {
  errors.push({variant,at:`${state}/${screen.id}@${width}`,message:e.message})
  if (session) {
   fs.mkdirSync(path.dirname(png),{recursive:true})
   await session.page.screenshot({path:png,animations:'disabled'}).catch(() => {})
  }
 } finally { if (session) await session.page.setViewportSize({width,height:900}).catch(() => {}) }
}
try {
 browser = await chromium.launch({executablePath:'/usr/local/bin/chromium',args:['--no-sandbox']})
 await api('organiser','POST','/v1/hack/events',{slug,title:'Small ideas, useful futures',organiser_name:'Reflow organiser',organisation:'Open Workshop Collective',contact_email:'workshop@example.com',purpose:'Build practical tools for communities',expected_participants:30,time_zone:'UTC',starts_at:'2099-01-01',ends_at:'2099-01-03'})
 seeded = true
 await api('organiser','PATCH',base,{tagline:'A weekend for useful tools and a healthier planet',about:'Build something that helps a neighbour, a colleague or your local community. Share your progress, learn from other teams and show what your idea can do.',submission_deadline:'2099-01-03T12:00:00Z',overall_prizes:['$300','$200','$100']})
 await api('organiser','PUT',base+'/registration',{questions:[],approval_required:false,admit_pending:true})
 await api('organiser','PUT',base+'/tracks',{tracks:[{slug:'developer-tools',name:'Developer tools'},{slug:'climate',name:'Climate'}]})
 const fields = Object.fromEntries('title problem tagline audience track tools maturity description video_url site code_url screenshot'.split(' ').map(k => [k,k === 'title' ? 'required' : 'optional']))
 await api('organiser','PUT',base+'/entry-rules',{fields})
 const rubric = await api('organiser','PUT',base+'/rubric',{criteria:[
  {name:'Community impact',description:'Solves a clear problem for real people.',weight:40,max_points:5},
  {name:'Clarity of the idea',description:'Explains the audience and approach clearly.',weight:30,max_points:5},
  {name:'Quality of demonstration',description:'Shows the useful parts working together.',weight:30,max_points:5}
 ]})
 await api('organiser','PATCH',base+'/judging/settings',{assignment_mode:'open'})
 await api('organiser','PUT',base+'/content',{
  schedule:[{title:'Welcome and team introductions',description:'Meet the teams and share the problem you want to solve.',start_at:'2099-01-01T09:00:00Z',end_at:'2099-01-01T10:00:00Z'},{title:'Show your progress',description:'Bring a short demo and get feedback from mentors.',start_at:'2099-01-02T14:00:00Z',end_at:'2099-01-02T15:30:00Z'}],
  sponsors:[{name:'Community Technology Foundation',tier:'Community partner',url:'https://example.com/foundation'},{name:'Green Neighbourhood Network',tier:'Workshop partner',url:'https://example.com/network'}],
  faq:[{question:'Do I need to know how to code?',answer:'Everyone is welcome. Bring an idea and work with tools and teammates who can help you build it.'},{question:'What should our demo include?',answer:'Explain the problem, show the useful part of your project and tell us what you would build next.'}]
 })
 for (const [title,body] of [['Welcome to the workshop','Introduce yourself to the other teams and tell us about the problem you want to solve.'],['Demo preparation and mentor sessions','Keep your demo focused on the people it helps. Mentors can review your entry before the deadline.']]) await api('organiser','POST',base+'/announcements',{title,body,email_participants:false})
 await api('organiser','POST',base+'/stage',{stage:'open'})
 const event = await api('organiser','GET',base), teams = []
 const names = ['Rocket Panda Collective','Climate Ledger Builders','Tiny Tools']
 const titles = ['A calmer release checklist for small teams','Neighbourhood carbon budgets made practical','Tiny tools for everyday community organising']
 for (const [i,role] of ['participant','multi','new'].entries()) {
  await api(role,'POST','/v1/hack/join/'+event.organiser.join_code,{accept_coc:true,display_name:['Alex Rivers','Sam Patel','Jo Morgan'][i]})
  teams.push(await api(role,'POST',base+'/teams',{name:names[i]}))
  await api(role,'PUT',base+'/entry',{title:titles[i],tagline:'Helping small teams turn a complicated daily task into a clear, practical plan that everyone can understand and use together.',problem:'People lose time coordinating scattered information instead of taking the next useful step.',audience:'Small community groups and volunteer teams',track:i === 1 ? 'climate' : 'developer-tools',tools:['JavaScript','SQLite'],maturity:['working','partial','concept'][i],description:'A shared workspace brings the essential information together, explains what needs attention and makes the next action easy to find.',video_url:'https://example.com/demos/'+['rocket','climate','tiny'][i],code_url:'https://example.com/code/'+['rocket','climate','tiny'][i]})
  if (i < 2) await api(role,'POST',base+'/entry/submit',{})
 }
 await api('judge','POST','/v1/hack/judge/'+event.organiser.judge_code,{accept_coc:true,display_name:'Taylor Chen'})
 const allTeams = (await api('organiser','GET',base+'/teams')).teams
 const first = allTeams.find(t => t.slug === teams[0].slug)
 assert.ok(first?.id, 'first team id missing')
 const SCREENS = []
 const add = (id,role,url,selector) => SCREENS.push({id,role,url,selector})
 const manage = `/e/${slug}/manage?s=`, judge = `/e/${slug}/judge?s=`, participant = `/e/${slug}/team?s=`
 for (const s of ['overview','announcements','people','teams','entries','judging','assignments','results']) add('organiser-'+s,'organiser',manage+s)
 for (const tab of ['basics','about','schedule','tracks','sponsors','faq','look','website']) add('organiser-event-page-'+tab,'organiser',manage+'event-page&tab='+tab)
 for (const tab of ['signup','teams','entries','reminders','judging','results','details','organisers','links','notifications','danger']) add('organiser-settings-'+tab,'organiser',manage+'settings&tab='+tab)
 for (const s of ['team','entry']) add('organiser-'+s,'organiser',manage+s+'&team='+teams[0].slug)
 add('public-event','organiser',`/e/${slug}`,'h1'); add('events','organiser','/events'); add('account','organiser','/account'); add('organiser-create-event','organiser','/events/new')
 for (const s of ['home','rubric']) add('judge-'+s,'judge',judge+s)
 for (const tab of ['to-score','scored','conflicts']) add('judge-queue-'+tab,'judge',judge+'queue&tab='+tab)
 add('judge-scoring','judge',judge+'scoring&team='+first.id,'main h1, main .locked h2, main .page > .note')
 for (const s of ['home','schedule','announcements','team','entry','website','results']) add('participant-'+s,'participant',participant+s)
 for (const state of ['building','judging','results']) {
  if (state === 'judging') await api('organiser','POST',base+'/stage',{stage:'closed'})
  await api('organiser','POST',base+'/stage',{stage:state})
  if (state === 'judging') for (const [i,team] of teams.slice(0,2).entries()) {
   const id = allTeams.find(t => t.slug === team.slug).id
   await api('judge','PUT',base+'/judge/scores/'+id,{scores:rubric.criteria.map(c => ({criterion_id:c.id,points:4-i})),comment:'A clear idea with a useful demonstration and thoughtful next steps.'})
  }
  if (!states.has(state)) continue
  console.log('Capturing',state)
  const jobs = []
  for (const screen of SCREENS.filter(s => s.id.includes(process.env.REFLOW_ONLY || ''))) for (const width of widths) for (const variant of variants) jobs.push([state,screen,width,variant])
  // A few browser contexts per role work through the queue in parallel.
  await Promise.all([...Array(concurrency).keys()].map(async worker => { while (jobs.length) await capture(...jobs.shift(),worker) }))
 }
} catch (e) { errors.push({variant:'after',at:'setup/run',message:e.message}) }
finally {
 fs.mkdirSync(shots,{recursive:true})
 fs.writeFileSync(path.join(shots,'report.json'),JSON.stringify(report,null,2)+'\n')
 try { if (seeded) await api('organiser','DELETE',base,{confirm:slug}) }
 catch (e) { errors.push({variant:'after',at:'cleanup',message:e.message}) }
 finally {
  try { if (browser) await browser.close() }
  finally { if (!process.env.KEEP_NAVIGATION_FIXTURE) await fetch(info.url+'/_fixture/stop',{method:'POST'}).catch(() => {}) }
 }
}
for (const variant of variants) for (const screen of new Set(report.map(r => r.screen))) {
 const bad = widths.filter(w => report.some(r => r.variant === variant && r.screen === screen && r.width === w && r.issues.length))
 console.log(`${variant} ${screen}: ${bad.length ? bad.join(',') : 'ok'}`)
}
if (!report.some(r => r.variant === 'after')) errors.push({variant:'after',at:'capture',message:'No after captures ran'})
for (const e of errors) console.error(`${e.variant} ${e.at}: ${e.message}`)
process.exitCode = report.some(r => r.variant === 'after' && r.issues.length) || errors.some(e => e.variant === 'after' || e.fatal) ? 1 : 0
