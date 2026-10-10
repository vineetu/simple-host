// Caveat regression: delayed glyphs stay visible, with room for slanted ink.
import assert from 'node:assert/strict';
import {mkdir} from 'node:fs/promises';
import {checkWatch} from './check-hack-watch.mjs';
const pw=await import(process.env.PLAYWRIGHT_MODULE || '/tmp/tsx/node_modules/playwright/index.mjs');
const shots=process.env.FILM_SHOTS || '/tmp/film-letters';await mkdir(shots,{recursive:true});
const urls=process.argv.slice(2).length?process.argv.slice(2):['https://simple-hack.app/','https://simple-host-film.vineetu.simple-host.app/','https://simple-hack.app/watch'];
for(const engine of (process.env.FILM_ENGINE?[process.env.FILM_ENGINE]:['chromium','webkit'])) {
 const b=await pw[engine].launch(engine==='chromium'?{executablePath:process.env.CHROMIUM_PATH || '/home/ubuntu/.cache/ms-playwright/chromium-1234/chrome-linux/chrome'}:{headless:!process.env.DISPLAY});
 try {for(const [i,url] of urls.entries()) {
  const p=await b.newPage({viewport:{width:390,height:844},isMobile:true,hasTouch:true});
  // Inspect the real intro without changing the letter animation timing.
  await p.addInitScript(()=>{const timer=setTimeout;window.setTimeout=(fn,ms,...args)=>timer(fn,ms===1500?60000:ms,...args)});
  await p.goto(url,{waitUntil:'domcontentloaded'});await p.evaluate(()=>document.fonts.ready);await p.waitForTimeout(1100);
  if(await p.locator('#organizer-stage').count()) {
   await p.close();await checkWatch({browser:b,engine,url,shots});continue;
  }
  const intro=await p.locator('#loader .l').evaluateAll(es=>es.map(e=>({letter:e.textContent,opacity:getComputedStyle(e).opacity})));
  assert(intro.some(e=>e.letter==='l'));assert(intro.every(e=>e.opacity==='1'),JSON.stringify(intro));
  await p.screenshot({path:`${shots}/${engine}-${i}-intro.png`});
  await p.evaluate(()=>{document.getElementById('loader').remove();const box=document.createElement('div');box.id='letter-probe';box.style.cssText='position:fixed;inset:100px 12px auto;z-index:999;background:var(--paper);font:700 40px Caveat;line-height:2;color:var(--line)';box.innerHTML='<div id="written">simple half build live all</div><div id="scrubbed"></div>';document.body.append(box);Ink.write(box.querySelector('#written'));for(const c of 'simple half build live all'){const s=document.createElement('span');s.className='sl';s.textContent=c;s.style.transform='rotate(-2deg)';box.querySelector('#scrubbed').append(s)}});
  await p.waitForTimeout(1600);
  assert(await p.locator('#written .l').evaluateAll(es=>es.every(e=>getComputedStyle(e).opacity==='1')));
  assert(await p.locator('#letter-probe .l, #letter-probe .sl').evaluateAll(es=>es.every(e=>{const s=getComputedStyle(e);return s.overflow==='visible'&&parseFloat(s.paddingInlineStart)>0&&Math.abs(parseFloat(s.paddingInlineStart)+parseFloat(s.marginInlineStart))<.01})));
  await p.screenshot({path:`${shots}/${engine}-${i}-words.png`});
  await p.evaluate(()=>document.getElementById('letter-probe').remove());
  const count=await p.locator('.cap').count(), timeline=await p.locator('#scrub').count();
  await p.close();
  if(timeline) for(const [width,height] of [[320,640],[390,844],[1280,800]]) {
   // New contexts also exercise each layout's first paint in WebKit.
   const q=await b.newPage({viewport:{width,height},isMobile:width<860,hasTouch:width<860});
   await q.addInitScript(()=>{const timer=setTimeout;window.setTimeout=(fn,ms,...args)=>timer(fn,ms===1500?60000:ms,...args)});
   await q.goto(url,{waitUntil:'domcontentloaded'});await q.evaluate(()=>document.fonts.ready);
   for(let scene=0;scene<count;scene++) {
    await q.evaluate(n=>{location.hash='scene-'+n},scene);await q.waitForTimeout(80);
    const clipped=await q.locator(`.cap[data-i="${scene}"] .sl`).evaluateAll(es=>es.filter(e=>{const r=e.getBoundingClientRect(),s=getComputedStyle(e);return s.opacity!=='1'||r.left<0||r.right>innerWidth||r.top<0||r.bottom>innerHeight||s.overflow!=='visible'}).map(e=>e.textContent));
    assert.deepEqual(clipped,[],`${engine} ${width} scene ${scene}: clipped letters`);
   }
   await q.close();
  }
  console.log(`${engine}: letters visible, spacing preserved${timeline?`, all ${count} captions fit`:""}: ${url}`);
 }}finally{await b.close()}
}
