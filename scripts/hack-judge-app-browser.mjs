// Local Hack judge MCP App proof. Start TestServeHackJudgeAppBrowser with
// HACK_JUDGE_APP_FIXTURE, then pass its private fixture path and an
// evidence directory to this script. Mirrors hack-apps-browser.mjs's fake
// host and CSP, pointed at the judge resource instead of My sites.
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
async function rpc(method, params = {}, access = fixture.access) {
 const response = await fetch(fixture.url + '/mcp', {method:'POST', headers:{
  'Content-Type':'application/json', Authorization:'Bearer ' + access, 'MCP-Protocol-Version':'2025-06-18'
 }, body:JSON.stringify({jsonrpc:'2.0', id:1, method, params})});
 const data = await response.json();
 if(data.error) throw Error(data.error.message);
 return data.result;
}
const resource = (await rpc('resources/read', {uri:'ui://simple-hack/judge'})).contents[0];
assert.ok(!resource.text.includes(fixture.access));
assert.match(resource.text, /script-src 'nonce-/);
fs.writeFileSync(out + '/resource.html', resource.text);
fs.writeFileSync(out + '/resource-meta.json', JSON.stringify(resource._meta, null, 2));
// A realistic host-served header CSP (Grok security review, SIM-344),
// not an artificially permissive one: a real MCP host's own response for
// the resource's content type would plausibly allow 'self'/'unsafe-
// inline' broadly, the same way a page embedding third-party HTML
// commonly does, leaving the resource's OWN <meta> CSP (the nonce'd
// script-src) as the thing that actually has to hold. Multiple CSPs
// combine by intersecting each directive, so a looser header can never
// widen what the stricter meta tag allows -- this is what proves that,
// rather than asserting it.
const frameServer = http.createServer((req, res) => {
 res.setHeader('Content-Type','text/html');
 res.setHeader('Content-Security-Policy', "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'none'; frame-src 'none'; base-uri 'none'");
 res.end(resource.text);
});
await new Promise(resolve => frameServer.listen(0, '127.0.0.1', resolve));
const frameURL = 'http://127.0.0.1:' + frameServer.address().port;
const hostHTML = `<!doctype html><meta charset="utf-8"><style>body{margin:0}iframe{border:0;width:100%;display:block}</style><iframe sandbox="allow-scripts" title="Judge" src="${frameURL}"></iframe><script>
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
 else if(message.id!==undefined)send({id:message.id,result:{}});
});</script>`;
// page.screenshot({fullPage:true}) temporarily resizes Chromium's device
// metrics to the full scrollable height, then captures; an iframe whose own
// height is set asynchronously, from its own postMessage'd size-changed
// notification (apps/bridge.js's ResizeObserver), can still be mid-resize
// when that capture happens, producing a blank or truncated frame even
// though every DOM assertion (text, bounding boxes, tool calls) already
// passed -- seen on this harness's own scoring screenshot at 390px: the
// page rendered correctly (confirmed by re-querying the live DOM a moment
// later) but the saved PNG was blank. One stable height reading was not
// enough on its own (still reproduced once in three consecutive runs even
// with that guard in place): this waits for three consecutive stable
// reads, then two animation frames -- a standard way to wait for a paint
// to have actually happened, not just been scheduled -- in the OUTER
// page (whose own layout page.screenshot's temporary resize changes),
// right before capturing.
// A double requestAnimationFrame is the standard way to wait for a paint
// to have actually happened, not just been scheduled, in ONE renderer; it
// is not quite enough here because the iframe -- a different origin, on a
// different port, so Chromium's site isolation very plausibly runs it in
// its OWN renderer process -- has to receive and act on its own resize
// before ITS paint reflects the outer page's new (fullPage) size, and
// nothing guarantees that finishes just because the outer page's own
// frame has painted. So this waits in both: the outer page (whose resize
// page.screenshot's fullPage capture itself triggers) and the iframe's
// own document (the content that actually has to show up).
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
// seen on this harness's own scoring screenshot: the page rendered
// correctly (confirmed by re-querying the live DOM a moment later) but
// the saved PNG was blank, even with the waits above -- this is a timing
// race in Chromium's own fullPage capture, not a layout bug in the page
// (every assertion on the live DOM passes regardless), and it reproduced
// again, on a different view and width, even after adding those waits.
// Rather than chase an exact wait that provably eliminates a race in
// someone else's rendering pipeline, this checks the actual output
// (an implausibly small PNG byte count, for a page this harness knows
// has real content, is the same blank-capture race) and retries the
// whole wait-then-capture a few times, which a real race -- as opposed
// to a genuine layout bug, which would fail every time -- clears well
// within the retry budget.
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
// Asserts a locator is actually painted where a person could see and tap
// it: a real, positive-area box, inside the iframe's own rendered width
// (never past its right edge -- the two-column scoring layout collapsing
// back to its desktop grid would show exactly this: a near-zero-width
// sliver at x=0 while the real content sits off past width 390). This is
// a live-DOM check, independent of the screenshot PNG, so it still catches
// a real layout break even on a build where the screenshot race above
// does not reproduce.
async function assertOnScreen(frame, locator, label) {
 const box = await frame.locator(locator).first().boundingBox();
 assert.ok(box, label + ': not in the DOM');
 const frameWidth = await frame.locator('body').evaluate(() => document.documentElement.clientWidth);
 assert.ok(box.width > 0 && box.height > 0, label + ': zero-size box ' + JSON.stringify(box));
 assert.ok(box.x >= 0 && box.x + box.width <= frameWidth + 1, label + ': box outside the frame’s own width (' + frameWidth + '): ' + JSON.stringify(box));
}
// One allowlist for every mode: a single MCP connection's identity (one
// access token per simulated browser session, picked once below from the
// page's own ?mode=invite, exactly as a host passes one OAuth grant for
// the whole frame's lifetime) can legitimately call any of these,
// regardless of which view is currently open -- including after joining
// through the invite view, when the frame switches itself to the queue
// and (via hack-judge-view.js's refreshEvent) calls hack_show_judge again.
// An earlier version of this harness restricted the invite mode to three
// tool names, which made exactly that post-join refresh fail with a
// disallowed-tool error from this harness itself, not from the server.
const allowed = new Set(['hack_show_judge','hack_get_judge_queue','hack_get_rubric','hack_get_event','hack_get_judge_scores','hack_score_team','hack_get_my_conflicts','hack_declare_my_conflict','hack_remove_my_conflict','hack_preview_judge','hack_join_judge','hack_get_team_screenshot']);
const hostServer = http.createServer(async(req,res) => {
 try {
  const url = new URL(req.url, 'http://host');
  const invite = url.searchParams.get('mode') === 'invite';
  const access = invite ? fixture.inviteAccess : fixture.access;
  if(url.pathname==='/list') {
   const args = invite ? {view:'invite', code:fixture.judgeCode} : {view:'queue', slug:fixture.event};
   res.setHeader('Content-Type','application/json');res.end(JSON.stringify(await rpc('tools/call',{name:'hack_show_judge',arguments:args},access)));return;
  }
  if(url.pathname==='/call') {
   let body='';for await(const chunk of req)body+=chunk;
   const params=JSON.parse(body);
   const callAllowed = allowed;
   assert.ok(callAllowed.has(params.name), 'frame called disallowed tool: '+params.name);calls.push(params);
   res.setHeader('Content-Type','application/json');res.end(JSON.stringify({result:await rpc('tools/call',params,access)}));return;
  }
  res.setHeader('Content-Type','text/html');res.end(hostHTML);
 } catch(error) {res.statusCode=500;res.end(JSON.stringify({error:{message:error.message}}));}
});
await new Promise(resolve => hostServer.listen(0,'127.0.0.1',resolve));
const hostURL='http://127.0.0.1:'+hostServer.address().port;
const browser = await chromium.launch({executablePath:process.env.CHROMIUM_PATH || '/home/ubuntu/.cache/ms-playwright/chromium-1234/chrome-linux/chrome'});
try {
 // Grok security review (SIM-344): proves the resource's own nonce'd
 // script-src is what actually stops an injected handler from running,
 // independent of the reused screens' own esc() escaping -- this bypasses
 // that escaping entirely by writing raw, unescaped HTML straight into
 // the page's own container, simulating the worst case (an escaping bug
 // somewhere, not assumed away). An onerror= handler needs 'unsafe-
 // inline' or 'unsafe-hashes' on script-src; the frame's own <meta> CSP
 // has neither, and multiple CSPs intersect, so the realistic host header
 // above (which does allow 'unsafe-inline') cannot loosen it.
 {
  const context=await browser.newContext({viewport:{width:390,height:900}}),page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',async d=>{errors.push('Unexpected dialog: '+d.message());await d.dismiss();});
  await page.goto(hostURL+'/?host=Claude&theme=light');
  const frame=page.frameLocator('iframe');
  await frame.locator('.list .r').first().waitFor();
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

 for(const host of ['Claude','ChatGPT'])for(const width of [390,1280]) {
  const context=await browser.newContext({viewport:{width,height:900}}),page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',async d=>{errors.push('Unexpected dialog: '+d.message());await d.dismiss();});
  await page.goto(hostURL+'/?host='+host+'&theme=light');
  const frame=page.frameLocator('iframe');
  // Queue view: the hostile team should be in "To score", escaped as text.
  await frame.locator('.list .r').first().waitFor();
  const queueText = await frame.locator('main').textContent();
  assert.match(queueText, /<script>bad\(\)<\/script> Entry title/);
  assert.equal(await frame.locator('main script, main img[src="x"]').count(), 0);
  assert.ok(await frame.locator('body').evaluate(el=>el.scrollWidth<=document.documentElement.clientWidth), 'queue view overflows at width '+width);
  const filename=host.toLowerCase()+'-'+width;
  await shot(page, out+'/'+filename+'-queue.png');

  // Open scoring for the one team, tap the first criterion's top score.
  await frame.locator('a.btn.sm:has-text("Score this team")').first().click();
  await frame.locator('.scale span').first().waitFor();
  assert.match(await frame.locator('.left').textContent(), /<script>bad\(\)<\/script> Entry title/);
  assert.equal(await frame.locator('.right script, .right img[src="x"]').count(), 0);
  const chips = frame.locator('[data-crit-row]').first().locator('[role=radio]');
  await chips.last().click();
  await frame.locator('[data-crit-row]').first().locator('[role=radio][aria-checked=true]').waitFor();
  await page.waitForTimeout(200); // the PUT this triggers is in flight; let it settle before the screenshot
  assert.ok(await frame.locator('body').evaluate(el=>el.scrollWidth<=document.documentElement.clientWidth), 'scoring view overflows at width '+width);
  // The two-column focus layout collapsing back to its desktop grid at
  // phone width would leave the criteria chips with a near-zero width,
  // pushed off past the frame's own right edge -- check the live DOM, not
  // just the screenshot (see waitForStableFrame's note on the screenshot
  // race this also guards against independently).
  await assertOnScreen(frame, '[data-crit-row]', filename+' scoring criteria row');
  await assertOnScreen(frame, '.scale span', filename+' scoring chip');
  await shot(page, out+'/'+filename+'-score.png');

  // Rubric, reached from scoring's own link (no tool call: nav.js's href()
  // shim turns this into an in-frame '#v=rubric' switch).
  await frame.locator('a:has-text("What do the scores mean?")').click();
  await frame.locator('.card').first().waitFor();
  await shot(page, out+'/'+filename+'-rubric.png');

  assert.deepEqual(errors, []);
  cases.push({host, width, hostileText:true, noOverflow:true, errors});
  await context.close();
 }

 // Rubric is a dead end in the reused screen (no back link of its own --
 // that normally comes from the shell this frame leaves out); Codex review
 // (SIM-344, P2) asks for a frame-level Back that returns to the same
 // team's scoring, not just to the queue.
 {
  const context=await browser.newContext({viewport:{width:390,height:900}}),page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',async d=>{errors.push('Unexpected dialog: '+d.message());await d.dismiss();});
  await page.goto(hostURL+'/?host=Claude&theme=light');
  const frame=page.frameLocator('iframe');
  await frame.locator('a.btn.sm:has-text("Score this team")').first().click();
  await frame.locator('.scale span').first().waitFor();
  const teamHeading = await frame.locator('.left h1').textContent();
  await frame.locator('a:has-text("What do the scores mean?")').click();
  await frame.locator('.card').first().waitFor();
  await assertOnScreen(frame, '[data-app-back]', 'rubric Back control');
  await frame.locator('[data-app-back]').click();
  await frame.locator('.scale span').first().waitFor();
  assert.equal(await frame.locator('.left h1').textContent(), teamHeading, 'Back from rubric did not return to the same team’s scoring');
  assert.deepEqual(errors, []);
  cases.push({host:'Claude', width:390, view:'rubric-back', errors});
  await context.close();
 }

 // "I know someone on this team" declares a conflict and removes the team
 // from the queue -- not idempotent, so done once, after every host/width
 // screenshot above (which all share the one fixture team), not inside
 // that loop.
 {
  const context=await browser.newContext({viewport:{width:390,height:900}}),page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',async d=>{errors.push('Unexpected dialog: '+d.message());await d.dismiss();});
  await page.goto(hostURL+'/?host=Claude&theme=light');
  const frame=page.frameLocator('iframe');
  await frame.locator('a.btn.sm:has-text("Score this team")').first().click();
  await frame.locator('#jsc-conflict').waitFor();
  await frame.locator('#jsc-conflict').click();
  await frame.locator('.scrim .dialog').waitFor();
  await page.waitForTimeout(150); // let the host's size-changed resize settle before clicking inside it
  await assertOnScreen(frame, '.dialog [data-confirm]', 'conflict-dialog confirm button at 390');
  await shot(page, out+'/claude-390-conflict-dialog.png');
  await frame.locator('.dialog [data-confirm]').click({force:true});
  await frame.locator('.subtabs, .note').first().waitFor();
  assert.match(await frame.locator('main').textContent(), /you know someone on this team|know anyone on a team/i);
  await assertOnScreen(frame, '.subtabs, .note', 'after-conflict content at 390');
  await shot(page, out+'/claude-390-after-conflict.png');
  assert.deepEqual(errors, []);
  cases.push({host:'Claude', width:390, conflictDeclared:true, errors});
  await context.close();
 }

 // Invite view: a second judge who has not accepted yet, previewing the
 // event and code of conduct before joining.
 for(const width of [390,1280]) {
  const context=await browser.newContext({viewport:{width,height:900}}),page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',async d=>{errors.push('Unexpected dialog: '+d.message());await d.dismiss();});
  await page.goto(hostURL+'/?host=Claude&theme=light&mode=invite');
  const frame=page.frameLocator('iframe');
  await frame.locator('h1:has-text("invited to judge")').waitFor();
  await frame.locator('#pj-accept').waitFor();
  assert.ok(await frame.locator('body').evaluate(el=>el.scrollWidth<=document.documentElement.clientWidth), 'invite view overflows at width '+width);
  await shot(page, out+'/claude-'+width+'-invite.png');
  assert.deepEqual(errors, []);
  cases.push({host:'Claude', width, view:'invite', errors});
  await context.close();
 }

 // Accepting the invite for real (Codex review, SIM-344, P1): the real
 // screen's own success path is a page navigation
 // (location.assign('/e/<slug>/judge')) this sandboxed frame cannot make;
 // bundlegen/patches.go hands that off to window.__hackJudgeJoined
 // instead, which must land the judge on their queue, in-frame, not lose
 // them out of the app or leave them stuck back on the invite.
 {
  const context=await browser.newContext({viewport:{width:390,height:900}}),page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',async d=>{errors.push('Unexpected dialog: '+d.message());await d.dismiss();});
  await page.goto(hostURL+'/?host=Claude&theme=light&mode=invite');
  const frame=page.frameLocator('iframe');
  await frame.locator('#pj-accept').waitFor();
  await frame.locator('#pj-name').fill('Judge Two');
  await frame.locator('#pj-coc').check();
  await frame.locator('#pj-submit').click();
  await frame.locator('.list .r, .note').first().waitFor();
  const landedText = await frame.locator('main').textContent();
  assert.match(landedText, /My teams/, 'accepting the invite did not land on the judge queue');
  assert.ok(!(await frame.locator('#pj-accept').count()), 'still showing the invite form after accepting');
  await shot(page, out+'/claude-390-invite-accepted-queue.png');
  assert.deepEqual(errors, []);
  cases.push({host:'Claude', width:390, view:'invite-accept', errors});
  await context.close();
 }

 // Codex review (SIM-344, P2): an already-joined member who lands back on
 // the invite view (the model calling hack_show_judge with view:invite
 // again, e.g. from a stale link) gets "You're already in ... Open it",
 // and that link must open the queue, not just re-render the same invite.
 {
  const context=await browser.newContext({viewport:{width:390,height:900}}),page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',async d=>{errors.push('Unexpected dialog: '+d.message());await d.dismiss();});
  await page.goto(hostURL+'/?host=Claude&theme=light&mode=invite');
  const frame=page.frameLocator('iframe');
  await frame.locator('a:has-text("Open it")').waitFor();
  await frame.locator('a:has-text("Open it")').click();
  await frame.locator('.list .r, .note').first().waitFor();
  assert.match(await frame.locator('main').textContent(), /My teams/, '"Open it" did not open the judge queue for an existing member');
  assert.deepEqual(errors, []);
  cases.push({host:'Claude', width:390, view:'invite-open-it', errors});
  await context.close();
 }

 assert.ok(calls.some(c=>c.name==='hack_declare_my_conflict'));
 assert.ok(calls.some(c=>c.name==='hack_score_team'));
 assert.ok(calls.some(c=>c.name==='hack_join_judge'));
 assert.ok(calls.every(c=>allowed.has(c.name)));
 console.log(JSON.stringify({cases:cases.length,result:'passed'}));
} finally {
 fs.writeFileSync(out+'/results.json',JSON.stringify({cases,calls},null,2));
 await browser.close();hostServer.close();frameServer.close();
 await fetch(fixture.url+'/_fixture/stop',{method:'POST'});
}
