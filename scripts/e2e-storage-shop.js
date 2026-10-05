#!/usr/bin/env node
// Real local REST/router/Postgres/SQLite check. Go provisions isolated sessions
// and invokes this script with a private temporary fixture, never production.
const fs = require('node:fs');
const http = require('node:http');
const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const fixture = JSON.parse(fs.readFileSync(process.env.STORAGE_SHOP_FIXTURE, 'utf8'));
const auth = fs.readFileSync('internal/handler/static/auth.js', 'utf8');
const html = `<form id="order"><label>Item <input name="item" required></label><button>Order</button></form><p id="message"></p><pre id="orders"></pre>
<script>window.SH_CONFIG={site:'shop'};</script><script src="https://simple-host.test/auth.js"></script><script>
const orders=SH.storage.sqlite('orders').table('orders');
async function refresh(){const mine=await orders.list({order:'id',desc:1,limit:50});document.querySelector('#orders').textContent=JSON.stringify(mine);return mine;}
document.querySelector('form').addEventListener('submit',async e=>{e.preventDefault();try{await SH.requireSignIn();await orders.add({item:e.target.item.value,quantity:2});await refresh();document.querySelector('#message').textContent='Saved';}catch(err){document.querySelector('#message').textContent=err.code||err.message;}});
</script>`;
async function forward(route) {
  const req = route.request(), u = new URL(req.url());
  if (u.pathname === '/auth.js') return route.fulfill({contentType:'text/javascript',body:auth});
  if (u.pathname === '/') return route.fulfill({contentType:'text/html',body:u.hostname===fixture.host?html:'<p>Local owner dashboard</p>'});
  const headers={...req.headers(),host:u.host,'x-forwarded-proto':'https'};
  delete headers['content-length'];
  const body=req.postDataBuffer();
  const response=await new Promise((resolve,reject)=>{
    const request=http.request(fixture.url+u.pathname+u.search,{method:req.method(),headers},res=>{
      const chunks=[];res.on('data',c=>chunks.push(c));res.on('end',()=>resolve({status:res.statusCode,headers:res.headers,body:Buffer.concat(chunks)}));
    });request.on('error',reject);request.end(body);
  });
  delete response.headers['content-length'];
  await route.fulfill(response);
}
(async()=>{
 const browser=await chromium.launch({executablePath:process.env.CHROMIUM||'/usr/local/bin/chromium',headless:true,args:['--no-sandbox']});
 try {
  const pages=[];
  for (const cookie of fixture.cookies) {
    const context=await browser.newContext();
    const split=cookie.indexOf('=');
    await context.addCookies([{name:cookie.slice(0,split),value:cookie.slice(split+1),url:'https://'+fixture.host+'/',secure:true,sameSite:'Lax'}]);
    await context.route('**/*',forward);
    const page=await context.newPage();await page.goto('https://'+fixture.host+'/');pages.push(page);
  }
  for (let i=0;i<pages.length;i++) {
    await pages[i].locator('input').fill(i===0?'Alice chai':'Bob cloves');await pages[i].locator('button').click();
    await pages[i].waitForFunction(()=>document.querySelector('#message').textContent==='Saved');
  }
  let a=await pages[0].evaluate(()=>refresh()),b=await pages[1].evaluate(()=>refresh());
  assert.equal(a.rows.length,1);assert.equal(b.rows.length,1);
  assert.equal(a.rows[0][a.columns.indexOf('item')],'Alice chai');assert.equal(b.rows[0][b.columns.indexOf('item')],'Bob cloves');
  assert.notEqual(a.rows[0][a.columns.indexOf('visitor_id')],b.rows[0][b.columns.indexOf('visitor_id')]);
  const context=await browser.newContext();await context.route('**/*',forward);const owner=await context.newPage();await owner.goto('https://simple-host.test/');
  const all=await owner.evaluate(async key=>{const r=await fetch('/v1/sites/shop/storage/sqlite/orders/query',{method:'POST',headers:{'X-API-Key':key,'Content-Type':'application/json'},body:JSON.stringify({sql:'SELECT id,item,status FROM orders ORDER BY id'})});if(!r.ok)throw Error('owner query failed');return r.json();},fixture.ownerKey);
  assert.equal(all.rows.length,2);
  const result=await owner.evaluate(async ({key,id})=>{const r=await fetch('/v1/sites/shop/storage/sqlite/orders/execute',{method:'POST',headers:{'X-API-Key':key,'Content-Type':'application/json'},body:JSON.stringify({sql:'UPDATE orders SET status=? WHERE id=?',params:['packed',id]})});return {status:r.status,body:await r.json()};},{key:fixture.ownerKey,id:all.rows[0][0]});
  assert.equal(result.status,200);assert.equal(result.body.changes,1);
  a=await pages[0].evaluate(()=>refresh());b=await pages[1].evaluate(()=>refresh());
  assert.equal(a.rows[0][a.columns.indexOf('status')],'packed');assert.equal(b.rows[0][b.columns.indexOf('status')],'placed');
  const refused=await pages[0].evaluate(async ()=>{
    const base='/v1/sites/shop/storage/sqlite/orders';
    const statuses=[];
    for(const route of ['query','execute'])statuses.push((await fetch(base+'/'+route,{method:'POST',headers:{'Content-Type':'application/json','X-SH-CSRF':'1'},body:JSON.stringify({sql:'SELECT * FROM orders'})})).status);
    statuses.push((await fetch(base+'/tables/orders/rows',{method:'POST',headers:{'Content-Type':'application/json','X-SH-CSRF':'1'},body:JSON.stringify({item:'forged',quantity:1,visitor_id:'other'})})).status);
    return statuses;
  });assert.deepEqual(refused,[403,403,400]);
  console.log('PASS shop browser: two isolated visitor receipts; owner sees both, changes status; only the correct receipt changes; raw SQL and forged identity refused.');
 } finally {await browser.close();}
})().catch(err=>{console.error(err.message);process.exit(1);});
