// PLAYWRIGHT_DIR=/path/to/node_modules/playwright node scripts/e2e-photo-safety.cjs [fixture.json]
// No fixture: regenerate real browser canvas samples. With TestServePhotoLinkForBrowser:
// verify worker cycle/timeout recovery and save through the real token route.
const {chromium, firefox, webkit} = require(process.env.PLAYWRIGHT_DIR || 'playwright');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const assert = require('node:assert/strict');
(async () => {
  const fixture = process.argv[2] && JSON.parse(fs.readFileSync(process.argv[2]));
  for (const [name, engine] of Object.entries({chromium, firefox, webkit})) {
    const browser = await engine.launch({headless:true});
    try {
      const page = await browser.newPage();
      if (!fixture) {
        const images = await page.evaluate(() => {
          const c = document.createElement('canvas'); c.width=32; c.height=24;
          const ctx=c.getContext('2d'); ctx.fillStyle='#428a65'; ctx.fillRect(0,0,32,24); ctx.fillStyle='#e09b12'; ctx.fillRect(3,4,18,10);
          return ['image/jpeg','image/webp'].map(type=>c.toDataURL(type,0.8));
        });
        for (const uri of images) {
          const ext = uri.startsWith('data:image/jpeg') ? 'jpg' : uri.startsWith('data:image/webp') ? 'webp' : null;
          if (ext) fs.writeFileSync(path.join(__dirname, '../internal/handler/testdata/photos/canvas-'+name+'.'+ext), Buffer.from(uri.split(',')[1],'base64'));
        }
        console.log('PASS '+name+' '+browser.version()+' canvas fixtures');
        continue;
      }
      await page.goto(fixture.url);
      await page.waitForFunction(() => !document.getElementById('editor').hidden);
      const png = await page.evaluate(() => { const c=document.createElement('canvas'); c.width=c.height=2; return c.toDataURL('image/png').split(',')[1]; });
      const good = {name:'good.png', mimeType:'image/png', buffer:Buffer.from(png,'base64')};
      await page.locator('#files').setInputFiles([{name:'cycle.tiff',mimeType:'image/tiff',buffer:Buffer.from('49492a0008000000000008000000','hex')},good]);
      await page.waitForFunction(() => !document.getElementById('files').disabled);
      assert.match(await page.locator('#status').textContent(), /cycle.tiff: Could not read this photo/);
      assert.equal(await page.locator('#photos img').count(),1);
      assert.equal(await page.locator('#save').isEnabled(),true);
      // Force a genuinely wedged worker; the production 20s watchdog must recover.
      await page.route('**/photo-codecs/v2/decoder.js', route=>route.fulfill({contentType:'text/javascript',body:'self.onmessage = () => { while (true) {} };'}));
      const started = Date.now();
      await page.locator('#files').setInputFiles([{name:'stuck.heic',mimeType:'image/heic',buffer:Buffer.from('ftypheic')},good]);
      await page.waitForFunction(() => !document.getElementById('files').disabled, null, {timeout:30000});
      assert.ok(Date.now()-started >= 19000);
      assert.match(await page.locator('#status').textContent(), /stuck.heic: Could not read this photo/);
      assert.equal(await page.locator('#photos img').count(),2);
      assert.equal(await page.locator('#save').isEnabled(),true);
      // Oversized selected input is rejected before any worker starts.
      const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'photo-input-'));
      const large = path.join(temporary, 'large.heic');
      fs.closeSync(fs.openSync(large, 'w')); fs.truncateSync(large, 80000001);
      try { await page.locator('#files').setInputFiles(large); } finally { fs.rmSync(temporary, {recursive:true}); }
      await page.waitForFunction(() => !document.getElementById('files').disabled);
      assert.match(await page.locator('#status').textContent(), /large.heic: Could not read this photo/);
      assert.equal(await page.locator('#save').isEnabled(),true);
      await page.locator('#save').click();
      await page.waitForFunction(() => !document.getElementById('done').hidden);
      console.log('PASS '+name+' cycle, real 20s worker termination, input cap, controls, next photo and token save');
    } finally { await browser.close(); }
  }
  if (fixture) await fetch(fixture.base+'/test-done', {method:'POST'});
})().catch(e => { console.error(e.message.replace(/#t=[^\s]+/g, '#t=[redacted]')); process.exitCode=1; });
