// Local Hack organiser MCP App proof. Start TestServeHackOrganiserAppBrowser
// with HACK_ORGANISER_APP_FIXTURE, then pass its private fixture path and an
// evidence directory to this script. Mirrors hack-judge-app-browser.mjs's
// fake host and CSP, pointed at the organiser resource and its three
// views (overview, people, judging) instead of the judge queue.
import fs from 'node:fs';
import http from 'node:http';
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
const require = createRequire(process.env.PLAYWRIGHT_PACKAGE || '/tmp/tsx/node_modules/playwright/package.json');
const {chromium} = require('playwright');
const fixture = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
const out = process.argv[3];
fs.mkdirSync(out, {recursive:true});
assert.match(fixture.url, /^http:\/\/127\.0\.0\.1:\d+$/);
const calls = [], cases = [];
async function rpc(method, params = {}) {
 const response = await fetch(fixture.url + '/mcp', {method:'POST', headers:{
  'Content-Type':'application/json', Authorization:'Bearer ' + fixture.access, 'MCP-Protocol-Version':'2025-06-18'
 }, body:JSON.stringify({jsonrpc:'2.0', id:1, method, params})});
 const data = await response.json();
 if(data.error) throw Error(data.error.message);
 return data.result;
}
const resource = (await rpc('resources/read', {uri:'ui://simple-hack/organiser'})).contents[0];
assert.ok(!resource.text.includes(fixture.access));
assert.match(resource.text, /script-src 'nonce-/);
fs.writeFileSync(out + '/resource.html', resource.text);
fs.writeFileSync(out + '/resource-meta.json', JSON.stringify(resource._meta, null, 2));
// A realistic host-served header CSP (same reasoning as the judge
// harness): a real MCP host's own response for the resource's content
// type would plausibly allow 'self'/'unsafe-inline' broadly; the
// resource's OWN <meta> CSP (the nonce'd script-src) is what actually has
// to hold, since multiple CSPs intersect.
const frameServer = http.createServer((req, res) => {
 res.setHeader('Content-Type','text/html');
 res.setHeader('Content-Security-Policy', "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'none'; frame-src 'none'; base-uri 'none'");
 res.end(resource.text);
});
await new Promise(resolve => frameServer.listen(0, '127.0.0.1', resolve));
const frameURL = 'http://127.0.0.1:' + frameServer.address().port;
const hostHTML = `<!doctype html><meta charset="utf-8"><style>body{margin:0}iframe{border:0;width:100%;display:block}</style><iframe sandbox="allow-scripts" title="Organiser" src="${frameURL}"></iframe><script>
const frame=document.querySelector('iframe'),query=new URLSearchParams(location.search);
const send=message=>frame.contentWindow.postMessage({jsonrpc:'2.0',...message},'*');
addEventListener('message',async event=>{
 if(event.source!==frame.contentWindow||event.data?.jsonrpc!=='2.0')return;
 const message=event.data;
 if(message.method==='ui/initialize')send({id:message.id,result:{protocolVersion:'2026-01-26',hostInfo:{name:query.get('host'),version:'fixture'},hostCapabilities:{serverTools:{},openLinks:{}},hostContext:{theme:query.get('theme')}}});
 else if(message.method==='ui/notifications/initialized')send({method:'ui/notifications/tool-result',params:await fetch('/list'+location.search).then(r=>r.json())});
 else if(message.method==='tools/call')send({id:message.id,...await fetch('/call'+location.search,{method:'POST',body:JSON.stringify(message.params)}).then(r=>r.json())});
 else if(message.method==='ui/notifications/size-changed')frame.style.height=message.params.height+'px';
 else if(message.method==='ui/update-model-context')send({id:message.id,result:{}});
 else if(message.method==='ui/open-link')send({id:message.id,result:{}});
 else if(message.id!==undefined)send({id:message.id,result:{}});
});</script>`;
// See hack-judge-app-browser.mjs's own long comment on why this double
// wait (outer page + inner iframe, both via waitForStableFrame) and the
// retry-on-tiny-PNG guard in shot() exist: a real, reproduced Chromium
// fullPage-capture race, not a layout bug.
async function doubleRaf(target) {
 await target.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
}
function innerFrame(page) {
 return page.frames().find(f => f !== page.mainFrame());
}
async function waitForStableFrame(page) {
 let last = null, stable = 0;
 for (let i = 0; i < 40 && stable < 3; i++) {
  const height = await page.evaluate(() => { const f = document.querySelector('iframe'); return f ? f.getBoundingClientRect().height : 0; });
  if (height > 0 && height === last) stable++; else stable = 0;
  last = height;
  await page.waitForTimeout(75);
 }
 await doubleRaf(page);
 const inner = innerFrame(page);
 if (inner) await doubleRaf(inner);
}
async function shot(page, path) {
 let bytes = 0;
 for (let attempt = 0; attempt < 5; attempt++) {
  await waitForStableFrame(page);
  await page.screenshot({path, fullPage:true});
  bytes = fs.statSync(path).size;
  if (bytes > 8000) return;
  await page.waitForTimeout(200);
 }
 assert.ok(false, path + ': screenshot is only ' + bytes + ' bytes after 5 attempts, almost certainly the blank-capture race (see waitForStableFrame)');
}
// Live-DOM check that a locator is actually painted where a person could
// see and tap it (see hack-judge-app-browser.mjs's own note): a real,
// positive-area box, inside the iframe's own rendered width.
async function assertOnScreen(frame, locator, label) {
 const box = await frame.locator(locator).first().boundingBox();
 assert.ok(box, label + ': not in the DOM');
 const frameWidth = await frame.locator('body').evaluate(() => document.documentElement.clientWidth);
 assert.ok(box.width > 0 && box.height > 0, label + ': zero-size box ' + JSON.stringify(box));
 assert.ok(box.x >= 0 && box.x + box.width <= frameWidth + 1, label + ': box outside the frame’s own width (' + frameWidth + '): ' + JSON.stringify(box));
}
// One connection's own allowlist for every view (Grok security review
// pattern, SIM-344): every tool hack-organiser-view.js's route table or
// bootstrap can call, regardless of which view is currently open --
// including hack_decide_application once Approve is exercised below.
// hack_app_* names are app-only twins (Grok security review finding 1,
// SIM-344 phase 2): the real glue now calls these, not the originals, for
// every read it makes on its own and for the three writes whose body
// carries a secret (stage change, a new join/judge link, a co-organiser
// invite).
const allowed = new Set(['hack_show_organiser','hack_app_get_people','hack_app_get_applications','hack_app_regenerate_code','hack_app_set_event_stage','hack_app_get_registration','hack_set_registration','hack_decide_application','hack_remove_person','hack_revoke_person_key','hack_remove_organiser','hack_app_create_organiser_invite','hack_revoke_organiser_invite','hack_app_get_rubric','hack_set_rubric','hack_app_get_judging_settings','hack_set_judging_settings','hack_app_get_conflicts','hack_set_judge_conflict','hack_remove_judge_conflict','hack_app_get_teams','hack_app_get_assignments','hack_generate_assignments','hack_app_get_judging_dashboard','hack_delete_former_judge_scores','hack_lock_judging','hack_unlock_judging']);
const hostServer = http.createServer(async(req,res) => {
 try {
  const url = new URL(req.url, 'http://host');
  if(url.pathname==='/list') {
   const view = url.searchParams.get('view') || 'overview';
   res.setHeader('Content-Type','application/json');res.end(JSON.stringify(await rpc('tools/call',{name:'hack_show_organiser',arguments:{view, slug:fixture.event}})));return;
  }
  if(url.pathname==='/call') {
   let body='';for await(const chunk of req)body+=chunk;
   const params=JSON.parse(body);
   assert.ok(allowed.has(params.name), 'frame called disallowed tool: '+params.name);calls.push(params);
   res.setHeader('Content-Type','application/json');res.end(JSON.stringify({result:await rpc('tools/call',params)}));return;
  }
  res.setHeader('Content-Type','text/html');res.end(hostHTML);
 } catch(error) {res.statusCode=500;res.end(JSON.stringify({error:{message:error.message}}));}
});
await new Promise(resolve => hostServer.listen(0,'127.0.0.1',resolve));
const hostURL='http://127.0.0.1:'+hostServer.address().port;
const browser = await chromium.launch({executablePath:process.env.CHROMIUM_PATH || '/home/ubuntu/.cache/ms-playwright/chromium-1234/chrome-linux/chrome'});
try {
 // CSP injection test (Grok security review, SIM-344): bypasses the
 // reused screens' own esc() escaping entirely, simulating the worst
 // case, and proves the frame's own nonce'd script-src -- not the
 // escaping -- is what actually stops an injected onerror= handler from
 // running, even under the realistic host header above that allows
 // 'unsafe-inline'.
 {
  const context=await browser.newContext({viewport:{width:390,height:900}}),page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',async d=>{errors.push('Unexpected dialog: '+d.message());await d.dismiss();});
  await page.goto(hostURL+'/?host=Claude&theme=light&view=overview');
  const frame=page.frameLocator('iframe');
  await frame.locator('.stat').first().waitFor();
  await frame.locator('#app').evaluate(el => {
   window.__xssFired = false;
   el.insertAdjacentHTML('beforeend', '<img src=x onerror="window.__xssFired = true">');
  });
  await page.waitForTimeout(300);
  const fired = await frame.locator('#app').evaluate(() => window.__xssFired);
  assert.equal(fired, false, 'an unescaped onerror= handler ran despite the nonce CSP');
  assert.deepEqual(errors, []);
  cases.push({host:'Claude', width:390, view:'csp-xss-injection', errors});
  await context.close();
 }

 // Overview: stage track, next-step card, snapshot counts, join/judge
 // link cards. Same context carried into People and Judging below (one
 // organiser session, navigating in-frame), so Back actually has
 // somewhere to return to.
 for(const host of ['Claude','ChatGPT'])for(const width of [390,1280]) {
  const context=await browser.newContext({viewport:{width,height:900}}),page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',async d=>{errors.push('Unexpected dialog: '+d.message());await d.dismiss();});
  await page.goto(hostURL+'/?host='+host+'&theme=light&view=overview');
  const frame=page.frameLocator('iframe');
  await frame.locator('.stat').first().waitFor();
  assert.match(await frame.locator('main').textContent(), /Participants/);
  assert.ok(await frame.locator('body').evaluate(el=>el.scrollWidth<=document.documentElement.clientWidth), 'overview view overflows at width '+width);
  await assertOnScreen(frame, '#ov-change-stage', host+'-'+width+' change-stage button');
  await assertOnScreen(frame, '.stat', host+'-'+width+' snapshot stat');
  const filename=host.toLowerCase()+'-'+width;
  await shot(page, out+'/'+filename+'-overview.png');
  assert.deepEqual(errors, []);
  cases.push({host, width, view:'overview', errors});
  await context.close();
 }

 // People: the pending applicant's hostile display name and answer must
 // show as escaped TEXT (no element created from them), and the
 // Applications card's filter row and role filter must still fit at
 // phone width.
 for(const host of ['Claude','ChatGPT'])for(const width of [390,1280]) {
  const context=await browser.newContext({viewport:{width,height:900}}),page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',async d=>{errors.push('Unexpected dialog: '+d.message());await d.dismiss();});
  await page.goto(hostURL+'/?host='+host+'&theme=light&view=people');
  const frame=page.frameLocator('iframe');
  await frame.locator('[data-app-approve]').first().waitFor();
  const peopleText = await frame.locator('main').textContent();
  assert.match(peopleText, /<script>bad\(\)<\/script> Applicant/);
  assert.match(peopleText, /<img src=x onerror=alert\(1\)> my answer/);
  assert.equal(await frame.locator('main script, main img[src="x"]').count(), 0);
  assert.ok(await frame.locator('body').evaluate(el=>el.scrollWidth<=document.documentElement.clientWidth), 'people view overflows at width '+width);
  await assertOnScreen(frame, '[data-app-approve]', host+'-'+width+' approve button');
  await assertOnScreen(frame, '[data-app-reject]', host+'-'+width+' reject button');
  const filename=host.toLowerCase()+'-'+width;
  await shot(page, out+'/'+filename+'-people.png');
  assert.deepEqual(errors, []);
  cases.push({host, width, hostileText:true, noOverflow:true, view:'people', errors});
  await context.close();
 }

 // Reject's own confirm dialog (the screen's own confirm counts as
 // asking the person, per the plan's destructive-action rule): opened
 // and dismissed with Cancel, so the pending application survives for
 // the Approve exercise below.
 {
  const context=await browser.newContext({viewport:{width:390,height:900}}),page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',async d=>{errors.push('Unexpected dialog: '+d.message());await d.dismiss();});
  await page.goto(hostURL+'/?host=Claude&theme=light&view=people');
  const frame=page.frameLocator('iframe');
  await frame.locator('[data-app-reject]').first().click();
  await frame.locator('.scrim .dialog').waitFor();
  await page.waitForTimeout(150);
  await assertOnScreen(frame, '.dialog [data-cancel]', 'reject-dialog cancel button at 390');
  await shot(page, out+'/claude-390-reject-dialog.png');
  await frame.locator('.dialog [data-cancel]').click({force:true});
  await frame.locator('[data-app-approve]').first().waitFor();
  assert.deepEqual(errors, []);
  cases.push({host:'Claude', width:390, view:'reject-dialog-cancel', errors});
  await context.close();
 }

 // Approve: a write with no confirm of its own (per the real screen), so
 // this exercises hack_decide_application through the view's own tool
 // call, then the model-context summary path (MODEL_CONTEXT_SUMMARY in
 // hack-organiser-view.js). Not idempotent, so done once, last among the
 // People cases.
 {
  const context=await browser.newContext({viewport:{width:390,height:900}}),page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',async d=>{errors.push('Unexpected dialog: '+d.message());await d.dismiss();});
  await page.goto(hostURL+'/?host=Claude&theme=light&view=people');
  const frame=page.frameLocator('iframe');
  await frame.locator('[data-app-approve]').first().click();
  await page.waitForTimeout(300); // the POST decision this triggers is in flight; let it settle
  const afterApprove = await frame.locator('main').textContent();
  assert.ok(!afterApprove.includes('<script>bad()</script> Applicant') || !(await frame.locator('[data-app-approve]').count()), 'applicant still shows as pending after Approve');
  assert.deepEqual(errors, []);
  cases.push({host:'Claude', width:390, view:'people-approved', errors});
  await context.close();
 }

 // Judging: rubric setup (empty -- this fixture never saves one),
 // progress and conflicts cards, same screen reused byte-for-byte from
 // the website. Reached via the in-frame router, then Back returns to
 // People, not to Overview (Codex review pattern from the judge app:
 // neither of these screens has a Back control of its own, since that
 // normally comes from the shell this frame leaves out).
 for(const width of [390,1280]) {
  const context=await browser.newContext({viewport:{width,height:900}}),page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',async d=>{errors.push('Unexpected dialog: '+d.message());await d.dismiss();});
  await page.goto(hostURL+'/?host=Claude&theme=light&view=judging');
  const frame=page.frameLocator('iframe');
  await frame.locator('#jg-progress-card:not(:has-text("Loading"))').waitFor();
  assert.match(await frame.locator('main').textContent(), /Progress/);
  assert.match(await frame.locator('main').textContent(), /Judges who know a team/);
  assert.ok(await frame.locator('body').evaluate(el=>el.scrollWidth<=document.documentElement.clientWidth), 'judging view overflows at width '+width);
  await assertOnScreen(frame, '#jg-progress-card', 'claude-'+width+' progress card');
  const filename='claude-'+width;
  await shot(page, out+'/'+filename+'-judging.png');
  assert.deepEqual(errors, []);
  cases.push({host:'Claude', width, view:'judging', errors});
  await context.close();
 }

 // Back control: a real in-frame navigation pushes Overview onto the tiny
 // view-history stack; Back must return to it. Overview used to carry its
 // own hero-action link into Judging ("Set up judging", an `a.btn.primary`
 // built through the same href() stub as every other in-frame link), but
 // the teams-redesign's one-button Overview replaced that with a
 // confirm-driven stage-change button that stays on Overview -- correct
 // product behaviour, not a bug (owner decision 2026-11-01). Overview now
 // carries no in-frame link into People or Judging at all, and neither of
 // those two links to the other either (that normally comes from the
 // shell this frame leaves out), so the frame-level view switcher added
 // alongside this fix (hack-organiser-view.js's addViewSwitcher,
 // [data-app-view-switcher]/[data-app-view]) is the one real in-frame
 // navigation left for an organiser working from chat -- and the one
 // this test now drives: Overview -> People -> Back -> Overview.
 {
  const context=await browser.newContext({viewport:{width:390,height:900}}),page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',async d=>{errors.push('Unexpected dialog: '+d.message());await d.dismiss();});
  await page.goto(hostURL+'/?host=Claude&theme=light&view=overview');
  const frame=page.frameLocator('iframe');
  await frame.locator('.stat').first().waitFor();
  await assertOnScreen(frame, '[data-app-view="people"]', 'view switcher People tab at 390');
  await frame.locator('[data-app-view="people"]').click();
  await frame.locator('#people-list').waitFor();
  await assertOnScreen(frame, '[data-app-back]', 'People Back control at 390');
  await frame.locator('[data-app-back]').click();
  await frame.locator('.stat').first().waitFor();
  assert.match(await frame.locator('main').textContent(), /Participants/, 'Back from people did not return to overview');
  assert.deepEqual(errors, []);
  cases.push({host:'Claude', width:390, view:'people-back', errors});
  await context.close();
 }

 assert.ok(calls.some(c=>c.name==='hack_decide_application'));
 assert.ok(calls.every(c=>allowed.has(c.name)));
 console.log(JSON.stringify({cases:cases.length,result:'passed'}));
} finally {
 fs.writeFileSync(out+'/results.json',JSON.stringify({cases,calls},null,2));
 await browser.close();hostServer.close();frameServer.close();
 await fetch(fixture.url+'/_fixture/stop',{method:'POST'});
}
