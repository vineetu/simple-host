#!/usr/bin/env node
// Real local REST/router/Postgres/SQLite check. Go provisions isolated sessions
// and invokes this script with a private temporary fixture, never production.
const fs = require('node:fs');
const http = require('node:http');
const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const fixture = JSON.parse(fs.readFileSync(process.env.STORAGE_SHOP_FIXTURE, 'utf8'));
const auth = fs.readFileSync('internal/handler/static/auth.js', 'utf8');
const html = `<form id="order"><label>Item <input name="item" required></label><button>Order</button></form><p id="message"></p><form id="change"><label>Note <input name="details" required></label><button>Add note</button></form><p id="changed"></p><pre id="orders"></pre><pre id="history"></pre>
<script>window.SH_CONFIG={site:'shop'};</script><script src="https://simple-host.test/auth.js"></script><script>
const orders=SH.storage.sqlite('orders').table('orders');
const changes=SH.storage.sqlite('orders').table('order_changes');
async function refresh(){const mine=await orders.list({order:'id',desc:1,limit:50}),history=await changes.list({order:'id',limit:50});document.querySelector('#orders').textContent=JSON.stringify(mine);document.querySelector('#history').textContent=JSON.stringify(history);return {mine,history};}
document.querySelector('form').addEventListener('submit',async e=>{e.preventDefault();try{await SH.requireSignIn();await orders.add({item:e.target.item.value,quantity:2,visitor_id:'forged',created_at:'1900-01-01'});await refresh();document.querySelector('#message').textContent='Saved';}catch(err){document.querySelector('#message').textContent=err.code||err.message;}});
document.querySelector('#change').addEventListener('submit',async e=>{e.preventDefault();try{await SH.requireSignIn();const {mine}=await refresh();await changes.add({order_id:mine.rows[0][mine.columns.indexOf('id')],kind:'note',details:e.target.details.value,visitor_id:'forged',created_at:'1900-01-01'});await refresh();document.querySelector('#changed').textContent='Added';}catch(err){document.querySelector('#changed').textContent=err.code||err.message;}});
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
    await pages[i].locator('#order input').fill(i===0?'Alice chai':'Bob cloves');await pages[i].locator('#order button').click();
    await pages[i].waitForFunction(()=>document.querySelector('#message').textContent==='Saved');
    await pages[i].locator('#change input').fill(i===0?'Alice no sugar':'Bob delivery note');await pages[i].locator('#change button').click();
    await pages[i].waitForFunction(()=>document.querySelector('#changed').textContent==='Added');
  }
  let {mine:a,history:ah}=await pages[0].evaluate(()=>refresh()),{mine:b,history:bh}=await pages[1].evaluate(()=>refresh());
  assert.equal(ah.rows.length,1);assert.equal(bh.rows.length,1);
  assert.equal(ah.rows[0][ah.columns.indexOf('details')],'Alice no sugar');assert.equal(bh.rows[0][bh.columns.indexOf('details')],'Bob delivery note');
  for(const data of [a,b,ah,bh]) {assert.notEqual(data.rows[0][data.columns.indexOf('visitor_id')],'forged');const stamp=Date.parse(data.rows[0][data.columns.indexOf('created_at')]);assert.ok(stamp>Date.now()-60000&&stamp<=Date.now());}
  assert.equal(a.rows.length,1);assert.equal(b.rows.length,1);
  assert.equal(a.rows[0][a.columns.indexOf('item')],'Alice chai');assert.equal(b.rows[0][b.columns.indexOf('item')],'Bob cloves');
  assert.notEqual(a.rows[0][a.columns.indexOf('visitor_id')],b.rows[0][b.columns.indexOf('visitor_id')]);
  const context=await browser.newContext();await context.route('**/*',forward);const owner=await context.newPage();await owner.goto('https://simple-host.test/');
  const all=await owner.evaluate(async key=>{const r=await fetch('/v1/sites/shop/storage/sqlite/orders/query',{method:'POST',headers:{'X-API-Key':key,'Content-Type':'application/json'},body:JSON.stringify({sql:'SELECT id,item,status FROM orders ORDER BY id'})});if(!r.ok)throw Error('owner query failed');return r.json();},fixture.ownerKey);
  assert.equal(all.rows.length,2);
  const history=await owner.evaluate(async key=>{const r=await fetch('/v1/sites/shop/storage/sqlite/orders/query',{method:'POST',headers:{'X-API-Key':key,'Content-Type':'application/json'},body:JSON.stringify({sql:'SELECT order_id,kind,details FROM order_changes ORDER BY id'})});if(!r.ok)throw Error('owner history query failed');return r.json();},fixture.ownerKey);assert.equal(history.rows.length,2);
  const result=await owner.evaluate(async ({key,id})=>{const r=await fetch('/v1/sites/shop/storage/sqlite/orders/execute',{method:'POST',headers:{'X-API-Key':key,'Content-Type':'application/json'},body:JSON.stringify({sql:'UPDATE orders SET status=? WHERE id=?',params:['packed',id]})});return {status:r.status,body:await r.json()};},{key:fixture.ownerKey,id:all.rows[0][0]});
  assert.equal(result.status,200);assert.equal(result.body.changes,1);
  ({mine:a,history:ah}=await pages[0].evaluate(()=>refresh()));({mine:b,history:bh}=await pages[1].evaluate(()=>refresh()));
  assert.equal(ah.rows.length,1);assert.equal(bh.rows.length,1);
  assert.equal(a.rows[0][a.columns.indexOf('status')],'packed');assert.equal(b.rows[0][b.columns.indexOf('status')],'placed');
  const refused=await pages[0].evaluate(async otherOrder=>{
    const base='/v1/sites/shop/storage/sqlite/orders';
    const statuses=[];
    for(const route of ['query','execute'])statuses.push((await fetch(base+'/'+route,{method:'POST',headers:{'Content-Type':'application/json','X-SH-CSRF':'1'},body:JSON.stringify({sql:'SELECT * FROM orders'})})).status);
    for(const order_id of [otherOrder,999999]) statuses.push((await fetch(base+'/tables/order_changes/rows',{method:'POST',headers:{'Content-Type':'application/json','X-SH-CSRF':'1'},body:JSON.stringify({order_id,kind:'note',details:'forged'})})).status);
    return statuses;
  },b.rows[0][b.columns.indexOf('id')]);assert.deepEqual(refused,[403,403,403,404]);
  const anonymous=await browser.newContext();await anonymous.route('**/*',forward);const anon=await anonymous.newPage();await anon.goto('https://'+fixture.host+'/');assert.equal(await anon.evaluate(async()=> (await fetch('/v1/sites/shop/storage/sqlite/orders/tables/order_changes/rows',{method:'POST',headers:{'Content-Type':'application/json','X-SH-CSRF':'1'},body:JSON.stringify({order_id:1,kind:'note'})})).status),401);
  console.log('PASS shop browser: two customers each place an order and add history; own reads isolate both tables; owner sees all and updates status; forged/missing references and anonymous inserts refused; client identity/timestamps ignored.');
 } finally {await browser.close();}
})().catch(err=>{console.error(err.message);process.exit(1);});
