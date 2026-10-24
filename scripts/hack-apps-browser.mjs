// Local Hack Apps proof. Start TestServeHackAppsBrowser with HACK_APPS_FIXTURE,
// then pass its private fixture path and an evidence directory to this script.
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
const resource = (await rpc('resources/read', {uri:'ui://simple-host/my-sites'})).contents[0];
assert.ok(!resource.text.includes(fixture.access));
fs.writeFileSync(out + '/resource.html', resource.text);
fs.writeFileSync(out + '/resource-meta.json', JSON.stringify(resource._meta, null, 2));
const frameServer = http.createServer((req, res) => {
 res.setHeader('Content-Type','text/html');
 res.setHeader('Content-Security-Policy', "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'none'; frame-src 'none'; base-uri 'none'");
 res.end(resource.text);
});
await new Promise(resolve => frameServer.listen(0, '127.0.0.1', resolve));
const frameURL = 'http://127.0.0.1:' + frameServer.address().port;
const hostHTML = `<!doctype html><meta charset="utf-8"><style>body{margin:0}iframe{border:0;width:100%;display:block}</style><iframe sandbox="allow-scripts" title="My sites" src="${frameURL}"></iframe><script>
const frame=document.querySelector('iframe'),query=new URLSearchParams(location.search);
const send=message=>frame.contentWindow.postMessage({jsonrpc:'2.0',...message},'*');
addEventListener('message',async event=>{
 if(event.source!==frame.contentWindow||event.data?.jsonrpc!=='2.0')return;
 const message=event.data;
 if(message.method==='ui/initialize')send({id:message.id,result:{protocolVersion:'2026-01-26',hostInfo:{name:query.get('host'),version:'fixture'},hostCapabilities:{serverTools:{},openLinks:{}},hostContext:{theme:query.get('theme')}}});
 else if(message.method==='ui/notifications/initialized')send({method:'ui/notifications/tool-result',params:await fetch('/list').then(r=>r.json())});
 else if(message.method==='tools/call')send({id:message.id,...await fetch('/call',{method:'POST',body:JSON.stringify(message.params)}).then(r=>r.json())});
 else if(message.method==='ui/notifications/size-changed')frame.style.height=message.params.height+'px';
 else if(message.id!==undefined)send({id:message.id,result:{}});
});</script>`;
const allowed = new Set(['who_am_i','hack_get_my_teams','hack_list_events','hack_get_event_website','storage_list_resources','storage_sql_query','hack_event_storage_list_resources','hack_event_storage_sql_query','site_analytics']);
const hostServer = http.createServer(async(req,res) => {
 try {
  if(req.url==='/list') {res.setHeader('Content-Type','application/json');res.end(JSON.stringify(await rpc('tools/call',{name:'list_sites',arguments:{}})));return;}
  if(req.url==='/call') {
   let body='';for await(const chunk of req)body+=chunk;
   const params=JSON.parse(body);assert.ok(allowed.has(params.name));calls.push(params);
   res.setHeader('Content-Type','application/json');res.end(JSON.stringify({result:await rpc('tools/call',params)}));return;
  }
  res.setHeader('Content-Type','text/html');res.end(hostHTML);
 } catch(error) {res.statusCode=500;res.end(JSON.stringify({error:{message:error.message}}));}
});
await new Promise(resolve => hostServer.listen(0,'127.0.0.1',resolve));
const hostURL='http://127.0.0.1:'+hostServer.address().port;
const browser = await chromium.launch({executablePath:process.env.CHROMIUM_PATH || '/home/ubuntu/.cache/ms-playwright/chromium-1234/chrome-linux/chrome'});
try {
 for(const host of ['Claude','ChatGPT'])for(const width of [390,1280])for(const theme of ['light','dark']) {
  const context=await browser.newContext({viewport:{width,height:900}}),page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',async d=>{errors.push('Unexpected dialog');await d.dismiss();});
  await page.goto(hostURL+'/?host='+host+'&theme='+theme);
  const frame=page.frameLocator('iframe');
  await frame.locator('.site-row').nth(2).waitFor();
  assert.equal(await frame.locator('.site-row').count(),3);
  assert.match(await frame.locator('#team-status').textContent(),/Selected team: alpha/);
  const filename=host.toLowerCase()+'-'+width+'-'+theme;
  await page.screenshot({path:out+'/'+filename+'-sites.png',fullPage:true});
  await frame.locator('#search').fill('no-matching-site');assert.equal(await frame.locator('.site-row').count(),0);
  await frame.locator('#search').fill('');
  await frame.locator('.site-row').first().click();
  await frame.locator('.chart-column').nth(6).waitFor();
  await frame.locator('.inquiry').nth(2).waitFor();
  assert.equal(await frame.locator('.inquiry').count(),3);
  assert.match(await frame.locator('#entry-rows').textContent(),/<img src=x onerror=alert\(1\)>/);
  assert.equal(await frame.locator('#entry-rows img, #entry-rows script').count(),0);
  assert.match(await frame.locator('#visitor-count').textContent(),/^\d/);
  assert.ok(await frame.locator('body').evaluate(el=>el.scrollWidth<=document.documentElement.clientWidth));
  await page.screenshot({path:out+'/'+filename+'-team.png',fullPage:true});
  await frame.locator('#back').click();
  await frame.locator('.site-row').last().click();
  await frame.locator('.inquiry').nth(2).waitFor();
  assert.equal(await frame.locator('#analytics').isVisible(),false);
  assert.equal(await frame.locator('.inquiry').count(),3);
  assert.equal(await frame.locator('iframe').count(),0);
  await page.screenshot({path:out+'/'+filename+'-event.png',fullPage:true});
  assert.deepEqual(errors,[]);
  cases.push({host,width,theme,teamChartDays:7,teamEntries:3,eventEntries:3,hostileText:true,noOverflow:true,errors});
  await context.close();
 }
 assert.ok(calls.some(c=>c.name==='hack_event_storage_sql_query'&&c.arguments.event===fixture.event));
 console.log(JSON.stringify({cases:cases.length,result:'passed'}));
} finally {
 fs.writeFileSync(out+'/results.json',JSON.stringify({cases,calls},null,2));
 await browser.close();hostServer.close();frameServer.close();
 await fetch(fixture.url+'/_fixture/stop',{method:'POST'});
}
