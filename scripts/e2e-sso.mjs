#!/usr/bin/env node
// Local SSO browser proof. Start TestServeSSOBrowser with SSO_FIXTURE set.
// PLAYWRIGHT_DIR=... node scripts/e2e-sso.mjs fixture.json --browser chromium --width 390 --shots /path --keep
import fs from 'node:fs';
import http from 'node:http';
import https from 'node:https';
import net from 'node:net';
import os from 'node:os';
import {execFileSync} from 'node:child_process';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
const require=createRequire(process.env.PLAYWRIGHT_DIR ? process.env.PLAYWRIGHT_DIR+'/' : import.meta.url);
const {chromium,webkit}=require('playwright');
const args=process.argv.slice(2), info=JSON.parse(fs.readFileSync(args[0],'utf8'));
const opt=(name,fallback)=>{const n=args.indexOf('--'+name);return n<0?fallback:args[n+1];};
const browserName=opt('browser','chromium'),width=Number(opt('width','390')),shots=opt('shots','');
const apex=info.apex, stamp=Date.now().toString(36);
// Each run is a separate visitor, including retries against a retained fixture.
const testIP='198.18.'+((process.pid>>8)&255)+'.'+(process.pid&255);
let count=0, discoveryUnavailable=false;
const siteIDs={},cookieLeaks=[];
function ok(name){count++;console.log(`ok ${browserName} ${width} ${name}`);}
async function api(method,path,body){const r=await fetch(info.url+path,{method,headers:{'X-API-Key':info.owner_key,'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});const d=await r.json().catch(()=>null);assert.ok(r.ok,`${method} ${path}: ${r.status} ${JSON.stringify(d)}`);return d;}
async function code(email){for(let n=0;n<50;n++){const d=await (await fetch(info.url+'/_fixture/mail?email='+encodeURIComponent(email))).json();if(d.code)return d.code;await new Promise(r=>setTimeout(r,100));}throw new Error('No fixture code');}
const origin=name=>`https://${name}.${info.handle}.${info.site_domain}`;
// A local TLS proxy preserves real browser redirects, cookie rules, Origin,
// and Fetch Metadata. Route.fulfill hides browser-generated Fetch Metadata
// and turns form redirects into synthetic documents, so it cannot prove SSO.
const tlsDir=fs.mkdtempSync(os.tmpdir()+'/sso-tls-');
execFileSync('openssl',['req','-x509','-newkey','rsa:2048','-nodes','-keyout',tlsDir+'/key.pem','-out',tlsDir+'/cert.pem','-days','1','-subj','/CN=sso-fixture.test'],{stdio:'ignore'});
const localAgent=new http.Agent({keepAlive:false});
function forward(req,res){
 const u=new URL(req.url,'https://'+req.headers.host);
 if(discoveryUnavailable && u.pathname==='/v1/auth/oauth/providers'){res.writeHead(503,{'content-type':'application/json','access-control-allow-origin':'*'});res.end('{}');return;}
 const headers={...req.headers,'x-forwarded-proto':'https','x-forwarded-for':testIP};
 if(u.hostname!==new URL(apex).hostname && /(?:^|;\s*)__Host-sh_sso=/.test(headers.cookie||''))cookieLeaks.push(u.hostname);
 const upstream=http.request(info.url+u.pathname+u.search,{method:req.method,headers,agent:localAgent},answer=>{
  if(process.env.E2E_DEBUG)console.log('fwd',req.method,u.hostname,u.pathname,answer.statusCode,headers.origin||'',headers['sec-fetch-site']||'');
  res.writeHead(answer.statusCode,answer.headers);answer.pipe(res);
 });
 upstream.on('error',()=>{res.writeHead(502);res.end('Fixture unavailable');});req.pipe(upstream);
}
const secure=https.createServer({key:fs.readFileSync(tlsDir+'/key.pem'),cert:fs.readFileSync(tlsDir+'/cert.pem')},forward);
await new Promise(r=>secure.listen(0,'127.0.0.1',r));
const proxy=http.createServer(forward);
proxy.on('connect',(req,client,head)=>{const upstream=net.connect(secure.address().port,'127.0.0.1',()=>{client.write('HTTP/1.1 200 Connection Established\r\n\r\n');if(head.length)upstream.write(head);client.pipe(upstream);upstream.pipe(client);});upstream.on('error',()=>client.destroy());client.on('error',()=>upstream.destroy());});
await new Promise(r=>proxy.listen(0,'127.0.0.1',r));
for(const name of ['private','ordinary','custom','locked']){
 const published=await api('PUT',`/v1/sites/${name}/files?create=1`,{files:{'index.html':`<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><title>${name}</title><h1>${name} site</h1><div id="auth"></div><script>window.SH_CONFIG={site:${JSON.stringify(name)},authBase:${JSON.stringify(apex)}};</script><script src="${apex}/auth.js"></script><script>SH.mount('#auth');</script>`}});
 siteIDs[name]=published.id;
}
await api('PUT','/v1/sites/private/access',{access:'specific'});
await api('POST','/v1/sites/private/viewers',{emails:[info.owner_email]});
await api('PUT','/v1/sites/locked/lock',{passcode:'fixture-unlock'});
assert.equal((await fetch(info.url+'/_fixture/custom',{method:'POST'})).status,204);
const browser=await (browserName==='webkit'?webkit:chromium).launch();
const context=await browser.newContext({viewport:{width,height:900},ignoreHTTPSErrors:true,proxy:{server:'http://127.0.0.1:'+proxy.address().port}});
context.setDefaultTimeout(20000);
const page=await context.newPage();
page.setDefaultTimeout(20000);
const errors=[];page.on('pageerror',e=>{errors.push(e.message);console.error('page error',new URL(page.url()).pathname,e.message);});
async function shot(name){if(shots){fs.mkdirSync(shots,{recursive:true});await page.screenshot({path:`${shots}/sso-${browserName}-${width}-${name}.png`,fullPage:true});}}
async function signedIn(){await page.getByText('Signed in as ',{exact:false}).first().waitFor();}
async function consent(){await page.getByRole('heading',{name:/Continue as/}).waitFor();await shot('consent');await page.getByRole('button',{name:'Continue',exact:true}).click();}
async function begin(){await page.getByRole('button',{name:'Sign in',exact:true}).click();}
async function apexSignIn(email){
 await page.goto(apex+'/');
 const res=await page.evaluate(async email=>{const r=await fetch('/v1/auth',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({email})});return r.status;},email);assert.equal(res,202);
 const c=await code(email);
 await page.evaluate(async({email,code})=>{const r=await fetch('/v1/auth/verify',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({email,code,name:'dashboard sign-in'})});const d=await r.json();if(!r.ok)throw new Error(d.error);await window.shStartSSO(d.api_key);localStorage.setItem('apiKey',d.api_key);},{email,code:c});
}
try{
 // A listed viewer must approve first-site disclosure too.
 await apexSignIn(info.owner_email);
 await page.evaluate(async ids=>{for(const id of Object.values(ids)){const r=await fetch('/v1/sso/consents/'+id,{method:'DELETE',headers:{'X-API-Key':localStorage.getItem('apiKey')}});if(r.status!==204)throw new Error('Reset consent '+r.status);}},siteIDs);
 await page.goto(origin('ordinary'));
 await page.evaluate(target=>{
  fetch(target+'/v1/sso/start?return_to='+encodeURIComponent(target+'/'),{mode:'no-cors',credentials:'include'}).catch(()=>{});
  const rule=document.createElement('script');rule.type='speculationrules';rule.textContent=JSON.stringify({prerender:[{source:'list',urls:[target+'/']}]});document.head.append(rule);
 },origin('private'));
 await page.waitForTimeout(1000);
 assert.ok(!(await context.cookies(origin('private'))).some(c=>c.name==='__Host-sh_vsess'||c.name==='__Host-sh_vnonce'));
 ok('cross-origin background fetch and prerender cannot start sign-in');
 await page.goto(origin('private'));
 await consent();await signedIn();assert.ok(page.url().startsWith(origin('private')));await shot('private');
 const siteCookies=(await context.cookies()).filter(c=>c.domain===new URL(origin('private')).hostname);
 assert.ok(siteCookies.some(c=>c.name==='__Host-sh_vsess'));
 assert.ok(!siteCookies.some(c=>c.name==='__Host-sh_sso'));
 assert.ok(!(await page.evaluate(()=>document.cookie)).includes('sh_vsess'));
 // Drop the site pass to force a second hop and prove consent memory.
 await context.clearCookies({name:'__Host-sh_vsess',domain:new URL(origin('private')).hostname});
 await page.goto(origin('private'));await signedIn();assert.ok(page.url().startsWith(origin('private')));
 ok('private auto hop, consent once, cookie isolation');
 discoveryUnavailable=true;
 await page.goto(origin('ordinary'));await page.getByRole('button',{name:'Sign in',exact:true}).waitFor();
 assert.ok(page.url().startsWith(origin('ordinary')));assert.equal(await page.locator('input[type=email]').count(),0);await shot('ordinary');
 await begin();await page.waitForURL(u=>u.origin===apex);discoveryUnavailable=false;await consent();await signedIn();ok('ordinary Sign in works while provider discovery fails');
 await page.goto('https://sso-custom.example.test/');await begin();await consent();await signedIn();assert.equal(new URL(page.url()).hostname,'sso-custom.example.test');await shot('custom');ok('custom domain top-level hop');
 const dashboard=await context.newPage();await dashboard.goto(apex+'/dashboard');await dashboard.waitForURL(u=>u.pathname!=='/dashboard');await dashboard.waitForFunction(()=>typeof api==='function');
 await page.getByRole('button',{name:'Sign out',exact:true}).click();await page.getByRole('button',{name:'Sign in',exact:true}).waitFor();
 assert.ok(!(await context.cookies(apex)).some(c=>c.name==='__Host-sh_sso'));
 await dashboard.bringToFront();await dashboard.evaluate(()=>window.dispatchEvent(new Event('focus')));
 await dashboard.waitForFunction(()=>!localStorage.getItem('apiKey'));
 await page.goto(origin('ordinary'));await page.getByRole('button',{name:'Sign in',exact:true}).waitFor();await shot('signed-out');ok('site sign-out revokes apex and another site');await dashboard.close();
 await begin();await page.getByRole('heading',{name:'Sign in',exact:true}).waitFor();assert.equal(new URL(page.url()).origin,apex);
 assert.match(await page.textContent('body'),/to continue to ordinary\./);await shot('apex-signin');
 const email=`sso-${browserName}-${width}-${stamp}@example.com`;
 await page.locator('input[type=email]').fill(email);await page.getByRole('button',{name:'Continue',exact:true}).click();
 await page.locator('input[autocomplete="one-time-code"]').fill(await code(email));
 await consent();await signedIn();ok('apex email card creates an account and returns to site');
 // The new account has a dashboard session, and no sites.
 const account=await context.newPage();await account.goto(apex+'/dashboard');await account.waitForURL(u=>u.pathname!=='/dashboard');
 const sites=await account.evaluate(async()=>{const r=await fetch('/v1/sites',{headers:{'X-API-Key':localStorage.getItem('apiKey')}});return r.json();});assert.deepEqual(sites,[]);await account.close();
 await page.goto(origin('locked')+'/v1/sso/start?return_to='+encodeURIComponent(origin('locked')+'/'));
 assert.match(await page.textContent('body'),/passcode|locked/i);assert.equal(new URL(page.url()).hostname,new URL(origin('locked')).hostname);
 await page.goto(origin('locked'));await shot('locked');await page.locator('input[name=passcode]').fill('fixture-unlock');await page.getByRole('button',{name:/Open|Unlock|Continue/i}).click();
 await begin();await consent();await signedIn();await shot('unlocked');ok('passcode refused until browser unlocks');
 // Both private denial actions must complete their form redirect under CSP.
 for(const label of ['Use another email','Sign out']){
  await apexSignIn(email);
  await page.goto(origin('private'));
  if(await page.getByRole('heading',{name:/Continue as/}).count())await consent();
  await page.getByRole('heading',{name:'You do not have access'}).waitFor();
  await shot(label==='Sign out'?'denial-signout':'denial-switch');
  const key=await context.newPage();await key.goto(apex+'/');
  const rejected=await key.evaluate(()=>localStorage.getItem('apiKey'));await key.close();
  const completed=page.waitForResponse(r=>new URL(r.url()).pathname==='/v1/sso/signed-out' && r.status()===200);
  await page.getByRole('button',{name:label,exact:true}).click();await completed;
  await page.getByRole('heading',{name:'Sign in',exact:true}).waitFor();
  assert.ok(!(await context.cookies(apex)).some(c=>c.name==='__Host-sh_sso'));
  assert.equal((await fetch(info.url+'/v1/me',{headers:{'X-API-Key':rejected}})).status,401);
  ok('denial '+label+' completes apex sign-out under CSP');
 }
 // Hold an old tab's request while a second tab signs in again. Its rejected
 // key must not erase the newer shared key, in either dashboard implementation.
 for(const surface of ['index','showcase']){
  await apexSignIn(info.owner_email);
  const old=await context.newPage();
  let armed=false, held, release;
  const intercepted=new Promise(r=>held=r);
  const resumed=new Promise(r=>release=r);
  await old.route('**/v1/me',async route=>{
   if(armed){armed=false;held();await resumed;await route.fulfill({status:401,contentType:'application/json',body:'{}'});return;}
   const response=await fetch(info.url+'/v1/me',{headers:{'X-API-Key':route.request().headers()['x-api-key']||''}});
   const body=await response.json();if(surface==='index')body.handle=null;
   await route.fulfill({status:response.status,contentType:'application/json',body:JSON.stringify(body)});
  });
  await old.goto(apex+'/dashboard');
  if(surface==='showcase')await old.waitForURL(u=>u.pathname!=='/dashboard');
  else await old.waitForFunction(()=>document.body.classList.contains('authed'));
  const rejected=await old.evaluate(()=>localStorage.getItem('apiKey'));
  armed=true;
  await old.evaluate(()=>{window.probe=api('GET','/v1/me').then(r=>r.status);});
  await intercepted;
  await apexSignIn(info.owner_email);
  const newer=await page.evaluate(()=>localStorage.getItem('apiKey'));assert.notEqual(newer,rejected);
  release();assert.equal(await old.evaluate(()=>window.probe),200);
  assert.equal(await old.evaluate(()=>localStorage.getItem('apiKey')),newer);
  await old.close();ok(surface+' stale 401 adopts newer sign-in');
 }
 // An anonymous site's logout link must not sign out the browser opening it.
 await apexSignIn(info.owner_email);
 const victimKey=await page.evaluate(()=>localStorage.getItem('apiKey'));
 const attacker=await browser.newContext({ignoreHTTPSErrors:true,proxy:{server:'http://127.0.0.1:'+proxy.address().port}});
 const attackPage=await attacker.newPage();await attackPage.goto(origin('ordinary'));
 const forged=await attackPage.evaluate(async()=>{const r=await fetch('/v1/visitor/logout',{method:'POST',headers:{'X-SH-CSRF':'1'}});return {status:r.status,body:await r.json()};});
 assert.equal(forged.status,200);
 const forgedURL=forged.body.redirect_url;await attacker.close();
 await page.goto(forgedURL);
 await page.getByRole('heading',{name:'Sign out of Simple Host on this browser?',exact:true}).waitFor();
 assert.equal((await fetch(info.url+'/v1/me',{headers:{'X-API-Key':victimKey}})).status,200);
 assert.ok((await context.cookies(apex)).some(c=>c.name==='__Host-sh_sso'));
 await shot('signout-confirm');
 await page.getByRole('link',{name:'Cancel',exact:true}).click();
 await page.waitForURL(u=>u.origin===origin('ordinary'));
 await page.getByRole('button',{name:'Sign in',exact:true}).waitFor();
 assert.equal((await fetch(info.url+'/v1/me',{headers:{'X-API-Key':victimKey}})).status,200);
 ok('forged logout link asks confirmation; Cancel preserves sign-in');
 // Expired/swept passes leave their dashboard key alive. The final apex page
 // must revoke that stored key itself; it must also work with no stored key.
 for(const storedKey of [true,false]){
  await apexSignIn(info.owner_email);
  const liveKey=await page.evaluate(()=>localStorage.getItem('apiKey'));
  if(!storedKey)await page.evaluate(()=>localStorage.removeItem('apiKey'));
  await page.goto(origin('ordinary'));await begin();await signedIn();
  assert.equal((await fetch(info.url+'/_fixture/sweep-pass',{method:'POST',headers:{'X-API-Key':liveKey}})).status,204);
  assert.equal((await fetch(info.url+'/v1/me',{headers:{'X-API-Key':liveKey}})).status,200);
  await page.getByRole('button',{name:'Sign out',exact:true}).click();
  await page.getByRole('heading',{name:'Sign out of Simple Host on this browser?',exact:true}).waitFor();
  assert.equal((await fetch(info.url+'/v1/me',{headers:{'X-API-Key':liveKey}})).status,200);
  await page.getByRole('button',{name:'Sign out',exact:true}).click();
  await page.waitForURL(u=>u.origin===origin('ordinary'));
  await page.getByRole('button',{name:'Sign in',exact:true}).waitFor();
  assert.ok(!(await context.cookies(apex)).some(c=>c.name==='__Host-sh_sso'));
  if(storedKey)assert.equal((await fetch(info.url+'/v1/me',{headers:{'X-API-Key':liveKey}})).status,401);
  const storage=await context.newPage();await storage.goto(apex+'/');
  assert.equal(await storage.evaluate(()=>localStorage.getItem('apiKey')),null);await storage.close();
  await shot(storedKey?'swept-signout':'no-key-signout');
  ok(storedKey?'swept pass sign-out revokes and removes dashboard key':'sign-out landing works without a stored dashboard key');
 }
 await apexSignIn(info.owner_email);
 // A second sign-in replaces the first family, including a site session whose
 // cookie remains in the browser; signing out the replacement ends its key too.
 await page.goto(origin('ordinary'));await begin();await signedIn();
 await apexSignIn(info.owner_email);
 await page.goto(origin('ordinary'));await page.getByRole('button',{name:'Sign in',exact:true}).waitFor();
 await begin();await signedIn();
 await page.getByRole('button',{name:'Sign out',exact:true}).click();
 await page.getByRole('button',{name:'Sign in',exact:true}).waitFor();
 assert.ok(!(await context.cookies(apex)).some(c=>c.name==='__Host-sh_sso'));
 ok('replacement sign-in retires previous site family');
 assert.deepEqual(cookieLeaks,[]);assert.deepEqual(errors,[]);console.log(`PASS ${browserName} ${width}: ${count} scenarios`);
} catch(e){console.error('failure page',new URL(page.url()).origin,new URL(page.url()).pathname);await shot('failure');throw e;} finally{await browser.close();proxy.closeAllConnections();secure.closeAllConnections();proxy.close();secure.close();fs.rmSync(tlsDir,{recursive:true,force:true});if(!args.includes('--keep'))await fetch(info.url+'/_fixture/stop',{method:'POST'});}

localAgent.destroy();
