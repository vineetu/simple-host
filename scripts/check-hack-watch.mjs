// Local browser check for the role films. Also used by check-film-letters.mjs.
import assert from 'node:assert/strict';
import {mkdir} from 'node:fs/promises';
import {pathToFileURL} from 'node:url';

export async function checkWatch({browser, engine, url, shots}) {
 await mkdir(shots,{recursive:true});
 for(const [width,height] of [[320,640],[390,844],[1280,800]]) {
  const p=await browser.newPage({viewport:{width,height},isMobile:width<700,hasTouch:width<700,colorScheme:'dark'});
  // waitForFunction uses eval in this Playwright build; preserve the page's strict CSP.
  const waitFor=async(fn,arg)=>{
   for(let n=0;n<80;n++){if(await p.evaluate(fn,arg))return;await p.waitForTimeout(100);}
   assert.fail('Browser condition timed out');
  };
  const errors=[],audioRequests=[];
  p.on('pageerror',e=>errors.push(e.message));
  p.on('request',r=>{if(/hack-watch-.*\.mp3/.test(r.url()))audioRequests.push(r.url());});
  await p.addInitScript(()=>{
   localStorage.setItem('sh-theme','dark');
   const OriginalAudio=window.Audio;window.watchAudio=[];
   window.Audio=function(...args){const a=new OriginalAudio(...args);window.watchAudio.push(a);return a;};
  });
  // Deterministic demo response covers the successful link state on this page.
  await p.route('**/v1/hack/demo',r=>r.fulfill({status:200,contentType:'application/json',body:JSON.stringify({title:'Simple Hack demo',event_url:'/e/demo',join_url:'/join/demo-code',judge_url:'/judge/demo-code'})}));
  await p.goto(url,{waitUntil:'networkidle'});await p.evaluate(()=>document.fonts.ready);
  assert.equal(audioRequests.length,0,'no narration downloads before play');
  assert.equal(await p.evaluate(()=>watchAudio.length),0,'Audio is lazy');
  assert(await p.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'no horizontal scroll');
  const colors=await p.evaluate(()=>[document.body,document.querySelector('.sh-header'),document.querySelector('.sh-footer')].map(e=>({bg:getComputedStyle(e).backgroundColor,scheme:getComputedStyle(e).colorScheme})));
  assert(colors.every(c=>c.bg==='rgb(244, 239, 223)'&&c.scheme==='light'),JSON.stringify(colors));
  assert(await p.locator('#judge .demo-link').isVisible());assert(await p.locator('#participant .demo-link').isVisible());
  for(const [index,role] of ['organizer','judge','participant'].entries()) {
   const stage=p.locator('#'+role+'-stage'),scrub=stage.locator('.scrub'),big=stage.locator('.big-play');
   await stage.scrollIntoViewIfNeeded();
   assert.equal(await stage.getAttribute('data-playing'),'false');assert.equal(await scrub.inputValue(),'0');
   assert.equal((await big.innerText()).trim(),'play with sound');assert.equal((await stage.locator('.sound').innerText()).trim(),'sound on');
   await big.click();
   await waitFor(i=>watchAudio[i]&&!watchAudio[i].paused&&watchAudio[i].currentTime>0,index);
   assert.equal(await p.evaluate(i=>watchAudio[i].muted,index),false);
   await stage.locator('.play').click();
   const paused=+(await scrub.inputValue());await p.waitForTimeout(160);
   assert(Math.abs(+(await scrub.inputValue())-paused)<.02,`pause freezes clock: ${paused} -> ${await scrub.inputValue()}, playing=${await stage.getAttribute('data-playing')}`);
   await stage.locator('.restart').click();await p.waitForTimeout(120);
   assert(+(await scrub.inputValue())<.5,'restart rewinds');
   await stage.locator('.sound').click();assert.equal(await p.evaluate(i=>watchAudio[i].muted,index),true);
   await stage.locator('.sound').click();assert.equal(await p.evaluate(i=>watchAudio[i].muted,index),false);
   // Tap the artwork to pause, then keyboard controls are scoped to this stage.
   await stage.locator('.picture').click({position:{x:20,y:20}});assert.equal(await stage.getAttribute('data-playing'),'false');
   await stage.focus();await p.keyboard.press('ArrowRight');assert.equal(await stage.getAttribute('data-scene'),'1');
   await p.keyboard.press('Home');assert.equal(await stage.getAttribute('data-scene'),'0');
   await p.keyboard.press('m');assert.equal((await stage.locator('.sound').innerText()).trim(),'sound off');await p.keyboard.press('m');
   await p.keyboard.press('k');assert.equal(await stage.getAttribute('data-playing'),'true');
   // Real pointer dragging pauses while scrubbing and resumes on release.
   const box=await scrub.boundingBox();
   await p.mouse.move(box.x+box.width*.25,box.y+22);await p.mouse.down();
   assert.equal(await stage.getAttribute('data-playing'),'false');
   await p.mouse.move(box.x+box.width*.5,box.y+22);await p.mouse.up();
   assert.equal(await stage.getAttribute('data-playing'),'true');
   assert(+(await scrub.inputValue())>12,'drag seeks');
   await stage.locator('.play').click();
   for(let scene=0;scene<6;scene++) {
    await scrub.evaluate((el,t)=>{el.value=t;el.dispatchEvent(new Event('input',{bubbles:true}));},scene*5+2);
    assert.equal(await stage.getAttribute('data-scene'),String(scene));
    const clipped=await stage.locator(`.cap[data-i="${scene}"] .sl`).evaluateAll(es=>es.filter(e=>{
     const r=e.getBoundingClientRect(),s=getComputedStyle(e),box=e.closest('.stage').getBoundingClientRect();
     return s.opacity!=='1'||s.overflow!=='visible'||r.left<box.left||r.right>box.right||r.top<box.top||r.bottom>box.bottom;
    }).map(e=>e.textContent));
    assert.deepEqual(clipped,[],`${engine} ${width} ${role} scene ${scene}: clipped letters`);
    const capFits=await stage.locator(`.cap[data-i="${scene}"]`).evaluate(el=>{
     const control=el.closest('.stage').querySelector('.ctrl').getBoundingClientRect();
     return [...el.children].every(child=>child.getBoundingClientRect().bottom<=control.top);
    });
    assert(capFits,`${engine} ${width} ${role} scene ${scene}: captions hit controls`);
    if([0,2,5].includes(scene)) {
     await stage.locator('.play').click();
     await stage.screenshot({path:`${shots}/${engine}-${role}-${width}-scene-${scene}.png`});
     await stage.locator('.play').click();
    }
   }
   await scrub.evaluate(el=>{el.value='30.4';el.dispatchEvent(new Event('input',{bubbles:true}));});
   await stage.locator('.play').click();
   await waitFor(id=>document.querySelector('#'+id+' .big-play span').textContent==='watch again',role+'-stage');
   assert.equal(await stage.getAttribute('data-playing'),'false');assert.equal(await scrub.inputValue(),'31');
   await p.waitForTimeout(120);assert.equal(await scrub.inputValue(),'31','does not loop');
   await big.click();await p.waitForTimeout(100);assert(+(await scrub.inputValue())<.5,'watch again rewinds');
   await stage.locator('.play').click();
  }
  // Clipboard uses the literal prompt, including the participant placeholder.
  await p.context().grantPermissions(['clipboard-read','clipboard-write']).catch(()=>{});
  const copy=p.locator('#participant .copy');await copy.click();
  if(engine==='chromium')assert.equal(await p.evaluate(()=>navigator.clipboard.readText()),await p.locator('#participant-prompt-0').innerText());
  assert.deepEqual(errors,[],`${engine} page errors`);
  await p.close();console.log(`${engine} ${width}: all three players, audio, captions, light chrome and demo links passed`);
 }
 const p=await browser.newPage();
 await p.route('**/v1/hack/demo',r=>r.fulfill({status:404,contentType:'application/json',body:'{"error":"no demo event"}'}));
 await p.goto(url,{waitUntil:'networkidle'});
 for(const role of ['judge','participant']){
  assert(await p.locator('#'+role+' .demo-missing').isVisible());assert(await p.locator('#'+role+' .demo-link').isHidden());
 }
 await p.close();
}

if(process.argv[1]&&import.meta.url===pathToFileURL(process.argv[1]).href) {
 const pw=await import(process.env.PLAYWRIGHT_MODULE || '/tmp/tsx/node_modules/playwright/index.mjs');
 for(const engine of process.env.FILM_ENGINE?[process.env.FILM_ENGINE]:['chromium','webkit']){
  const browser=await pw[engine].launch(engine==='chromium'?{executablePath:process.env.CHROMIUM_PATH || '/home/ubuntu/.cache/ms-playwright/chromium-1234/chrome-linux/chrome'}:{headless:!process.env.DISPLAY});
  try{await checkWatch({browser,engine,url:process.argv[2]||'http://127.0.0.1:18482/watch',shots:process.env.FILM_SHOTS||'/home/ubuntu/workspace/sh-hack-watch-shots'});}finally{await browser.close();}
 }
}
