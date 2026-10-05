// WebKit regression: the fourth delayed Caveat glyph used to finish at opacity 0.
import assert from 'node:assert/strict';
import {mkdir} from 'node:fs/promises';
const {webkit}=await import(process.env.PLAYWRIGHT_MODULE || '/tmp/tsx/node_modules/playwright/index.mjs');
const shots=process.env.FILM_SHOTS || '/tmp/film-letters';await mkdir(shots,{recursive:true});
const b=await webkit.launch();
try {for(const [i,url] of (process.argv.slice(2).length?process.argv.slice(2):['https://simple-hack.app/','https://simple-host-film.vineetu.simple-host.app/']).entries()) {
 const p=await b.newPage({viewport:{width:390,height:844},isMobile:true,hasTouch:true});
 // Keep the real intro available for inspection, without changing letter timing.
 await p.addInitScript(()=>{const timer=setTimeout;window.setTimeout=(fn,ms,...args)=>timer(fn,ms===1500?60000:ms,...args)});
 await p.goto(url,{waitUntil:'domcontentloaded'});await p.evaluate(()=>document.fonts.ready);await p.waitForTimeout(1100);
 const intro=await p.locator('#loader .l').evaluateAll(es=>es.map(e=>({letter:e.textContent,opacity:getComputedStyle(e).opacity})));
 assert(intro.some(e=>e.letter==='l'));assert(intro.every(e=>e.opacity==='1'),JSON.stringify(intro));
 await p.screenshot({path:`${shots}/${i}-intro.png`});
 // Exercise both letter renderers with every narrow/slanted-word example.
 await p.evaluate(()=>{const box=document.createElement('div');box.id='letter-probe';box.style.cssText='position:fixed;inset:100px 12px auto;z-index:999;background:var(--paper);font:700 40px Caveat;line-height:2;color:var(--line)';box.innerHTML='<div id="written">simple half build live all</div><div id="scrubbed"></div>';document.body.append(box);Ink.write(box.querySelector('#written'));for(const c of 'simple half build live all'){const s=document.createElement('span');s.className='sl';s.textContent=c;s.style.transform='rotate(-2deg)';box.querySelector('#scrubbed').append(s)}});
 await p.waitForTimeout(1600);
 assert(await p.locator('#written .l').evaluateAll(es=>es.every(e=>getComputedStyle(e).opacity==='1')));
 const spacing=await p.locator('#letter-probe .l, #letter-probe .sl').evaluateAll(es=>es.every(e=>{const s=getComputedStyle(e);return Math.abs(parseFloat(s.paddingInlineStart)+parseFloat(s.marginInlineStart))<.01}));assert(spacing);
 await p.screenshot({path:`${shots}/${i}-words.png`});await p.close();console.log('WebKit letters visible, spacing preserved: '+url);
 }}finally{await b.close()}
