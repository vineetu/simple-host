const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const context = vm.createContext({ TextEncoder });
vm.runInContext(fs.readFileSync('internal/handler/static/photo-process.js', 'utf8'), context);
vm.runInContext(fs.readFileSync('internal/mcp/apps/photo-picker.js', 'utf8'), context);
const {slug, shrink} = context.SHPhotos;
const {occupiedPaths, photoPath, batches} = context.SHPhotoBatch;
test('readable names match /photos', () => {
  assert.equal(slug('Front door.HEIC'), 'front-door');
  assert.equal(slug('...☀.jpg'), 'photo');
  assert.equal(slug('x'.repeat(100) + '.png').length, 60);
});
test('collisions, suffix truncation, and directory conflicts match /photos', () => {
  const occupied = occupiedPaths([{path:'index.html'}, {path:'images/front.webp'}, {path:'images/front-2.webp'}]);
  assert.equal(photoPath(occupied, 'front.webp'), 'images/front-3.webp');
  assert.equal(photoPath(occupied, 'front.webp'), 'images/front-4.webp');
  const name = 'x'.repeat(60) + '.jpg';
  assert.equal(photoPath(occupied, name), 'images/' + name);
  assert.equal(photoPath(occupied, name), 'images/' + 'x'.repeat(58) + '-2.jpg');
  assert.throws(() => photoPath(occupiedPaths([{path:'images'}]), 'front.jpg'), /images path is a file/);
  assert.throws(() => photoPath(occupiedPaths([{path:'images/front.jpg/child'}]), 'front.jpg'), /folder uses/);
});
test('one version normally, split only when the MCP envelope requires it', () => {
  const photos = Array.from({length:30}, (_, i) => ({path:'images/photo-'+i+'.webp', base64:'a'.repeat(400000)}));
  assert.equal(batches(photos, 'test', 16*1024*1024).length, 1);
  const split = batches(photos.slice(0,3), 'test', 810000);
  assert.equal(split.length, 2);
  assert.equal(split.flat().length, 3);
  for (const batch of split) {
    const message = {jsonrpc:'2.0',id:1,method:'tools/call',params:{name:'update_site',arguments:{site:'test',keep_other_files:true,files_base64:Object.fromEntries(batch.map(p => [p.path,p.base64]))}}};
    assert.ok(Buffer.byteLength(JSON.stringify(message)) + 4096 <= 810000);
  }
  assert.throws(() => batches(photos, 'test', 1000), /small enough/);
});
test('reject oversized originals before decoding', async () => {
  await assert.rejects(shrink({size:80000001}), /Could not read this photo/);
});
test('bridge ignores foreign sources and malformed messages and handles the standard handshake', async () => {
  const sent = [], origins = [], listeners = {}, parent = {postMessage: (message, origin) => {sent.push(message); origins.push(origin);}};
  const root = {dataset:{},style:{setProperty(){}}};
  const bridge = vm.createContext({
    window:{parent,addEventListener:(type,fn)=>{listeners[type]=fn;}},
    document:{documentElement:root,body:{getBoundingClientRect:()=>({height:120})}},
    matchMedia:()=>({matches:false}), ResizeObserver:class{observe(){}}, URL, setTimeout, clearTimeout
  });
  vm.runInContext(fs.readFileSync('internal/mcp/apps/bridge.js','utf8'),bridge);
  const app=bridge.window.SHApp, start=app.start();
  assert.equal(sent[0].method,'ui/initialize');assert.equal(origins[0],'*');
  const reply={jsonrpc:'2.0',id:sent[0].id,result:{hostInfo:{name:'Claude'},hostContext:{theme:'dark'}}};
  listeners.message({source:{},data:reply});
  assert.equal(root.dataset.theme,'light');
  listeners.message({source:parent,data:{...reply,jsonrpc:'1.0'}});
  assert.equal(root.dataset.theme,'light');
  listeners.message({source:parent,origin:'https://chat.example',data:reply});await start;assert.equal(origins[1],'https://chat.example');
  assert.equal(root.dataset.theme,'dark');
  assert.equal(sent[1].method,'ui/notifications/initialized');
  assert.equal(sent[2].method,'ui/notifications/size-changed');
  let input;
  app.on('tool-input',p=>input=p.arguments);
  listeners.message({source:parent,data:{jsonrpc:'2.0',method:'ui/notifications/tool-input',params:{arguments:{site:'test'}}}});
  assert.equal(input.site,'test');
  const call=app.callTool('list_sites');
  const request=sent.at(-1);
  assert.equal(request.method,'tools/call');assert.equal(origins.at(-1),'https://chat.example');
  listeners.message({source:parent,data:{jsonrpc:'2.0',id:request.id,result:{structuredContent:{sites:[]}}}});
  assert.equal((await call).sites.length,0);
  await assert.rejects(app.openLink('http://example.com'),/HTTPS/);
});

function picker({commit = true, fail = true, failAt = 1, maxBodyBytes = 16000000, workerAllowed = true, wasmAllowed = true} = {}) {
  const nodes = new Map(), handlers = {}, writes = [], shrunk = []; let files = [], failed = false;
  const node = () => ({hidden:false,children:[],events:{},textContent:'',
    appendChild(el){this.children.push(el);}, setAttribute(k,v){this[k]=v;},removeAttribute(k){delete this[k];},
    addEventListener(k,fn){this.events[k]=fn;},remove(){this.removed=true;},replaceChildren(){this.children=[];}});
  const get = id => {if(!nodes.has(id))nodes.set(id,node());return nodes.get(id);};
  const document = {getElementById:get,createElement:node,body:{hidden:false}};
  const app = {hostCapabilities:{serverTools:{}},on:(k,fn)=>handlers[k]=fn,start:async()=>{},callTool:async(name,args)=>{
    if(name==='get_site')return {files};
    if(name==='update_site'){
      writes.push(args);if(commit || failed) files=Object.keys(args.files_base64).map(path=>({path,size:3}));
      if(fail && writes.length === failAt && !failed){failed=true;throw Error('The chat app did not respond. Try again.');}
      return {};
    }
  }};
  const ctx = vm.createContext({document,window:{addEventListener(){}},SHApp:app,SHPhotoConfig:{maxBodyBytes},
    Blob, URL, setTimeout, clearTimeout, Uint8Array,
    Worker:class { constructor(){if(!workerAllowed)throw Error('CSP');} terminate(){} postMessage(){queueMicrotask(()=>this.onmessage({data:wasmAllowed?{ready:true}:{error:'CSP'}}));} },
    SHPhotos:{slug, codecKind:context.SHPhotos.codecKind, dataURL:async()=> 'data:image/webp;base64,YWJj',shrink:async f=>{shrunk.push(f);if(f.bad)throw Error('bad');return {blob:{size:3},ext:'.webp'};}}, TextEncoder});
  vm.runInContext(fs.readFileSync('internal/mcp/apps/photo-picker.js','utf8'),ctx);
  handlers['tool-input']({arguments:{site:'blog'}});handlers['tool-result']({structuredContent:{}});
  return {get,document,handlers,writes,shrunk,setFiles:value=>files=value,
    add:files=>get('files').events.change({target:{files:files.map(f=>({slice:()=>({arrayBuffer:async()=>new ArrayBuffer(0)}),...f})),value:''}}),save:()=>get('finish').events.click()};
}
test('lost save reply reconciles the reserved path and byte size without a second version', async()=>{
 const m=picker();await m.add([{name:'front.jpg'}]);await m.save();assert.equal(m.writes.length,1);
 await m.save();assert.equal(m.writes.length,1);assert.equal(m.get('done').textContent,'Added 1 photo to images/');
});
test('unconfirmed save retries the reserved path, including a mismatched size', async()=>{
 for(const files of [[],[{path:'images/front.webp',size:9}]]){
  const m=picker({commit:false});await m.add([{name:'front.jpg'}]);await m.save();m.setFiles(files);await m.save();
  assert.equal(m.writes.length,2);assert.deepEqual(Object.keys(m.writes[0].files_base64),Object.keys(m.writes[1].files_base64));
  assert.equal(m.get('done').textContent,'Added 1 photo to images/');
 }
});
test('failed selections release capacity; unsaved photos can be removed and only saved photos count', async()=>{
 const m=picker({fail:false});await m.add(Array.from({length:30},()=>({name:'bad.heic',bad:true})));
 assert.equal(m.get('add').hidden,false);assert.equal(m.get('finish').disabled,true);
 await m.add([{name:'front.jpg'},{name:'back.jpg'}]);assert.equal(m.get('add').hidden,false);
 const row=m.get('photos').children.at(-1);await row.children.at(-1).events.click();assert.equal(row.removed,true);
 await m.save();assert.equal(m.get('done').textContent,'Added 1 photo to images/');assert.equal(Object.keys(m.writes[0].files_base64).length,1);
});
test('archive input hides the entire frame before any result and stays hidden',()=>{
 const m=picker();m.handlers['tool-input']({arguments:{site:'blog',archive:true}});assert.equal(m.document.body.hidden,true);
 m.handlers['tool-result']({structuredContent:{upload_url:'https://example.test/upload'}});
 assert.equal(m.document.body.hidden,true);
});

test('partial save can finish after every remaining unsaved row is removed', async()=>{
 const m=picker({commit:false,failAt:2,maxBodyBytes:4280});
 await m.add([{name:'front.jpg'},{name:'back.jpg'},{name:'bad.heic',bad:true}]);
 await m.save();assert.equal(m.writes.length,2);
 assert.equal(m.get('photos').children[0].children[3].textContent,'Added');
 assert.match(m.get('status').textContent,/did not respond/);
 m.get('photos').children[1].children.at(-1).events.click();
 assert.equal(m.get('finish').disabled,true); // Failed decode still awaits removal.
 m.get('photos').children[2].children.at(-1).events.click();
 assert.equal(m.get('status').textContent,'');assert.equal(m.get('finish').disabled,false);
 await m.save();assert.equal(m.writes.length,2);
 assert.equal(m.get('done').hidden,false);assert.equal(m.get('done').textContent,'Added 1 photo to images/');
 assert.equal(m.get('status').textContent,'');
});

async function photosPage({account='alice',handle='bob',sites=[{name:'blog',user_id:'owner'}],signedIn=true,post,token=false}={}) {
 const elements=new Map(),session=new Map(),posts=[];let signIn;
 const element=()=>({children:[],events:{},value:'',textContent:'',hidden:false,dataset:{},setAttribute(){},
  appendChild(el){this.children.push(el);},replaceChildren(){this.children=[];this.value='';},
  addEventListener(k,fn){this.events[k]=fn;},querySelectorAll(){return [];},
  set selectedIndex(value){if(value===-1)this.value='';}});
 const get=id=>{if(!elements.has(id))elements.set(id,element());return elements.get(id);};
 const search='?site=blog'+(account?'&account='+account:'');
 const ctx=vm.createContext({document:{getElementById:get,createElement:element,querySelector:element},
  window:{addEventListener(){},SHSignIn:{mount:(_,options)=>{signIn=options;return {};}}},
  location:{search,hash:token?'#t=fixture':'',origin:'https://example.test',assign(){}},history:{replaceState(){}},
  localStorage:{getItem:()=>signedIn?'fixture-key':null},sessionStorage:{setItem:(k,v)=>session.set(k,v)},
  fetch:async (path,options)=>{
   if(options?.method==='POST'){posts.push({path,options});return post(options,posts.length);}
   return {ok:true,json:async()=>path==='/v1/me'?{id:'owner',handle}:path==='/v1/sites'?sites:{site:'blog'}};
  },
  crypto:require('node:crypto').webcrypto,btoa,TextEncoder,Uint8Array,URLSearchParams,URL:{createObjectURL:()=> 'blob:fixture',revokeObjectURL(){}},
  FormData:class{append(){}},SHPhotos:{slug,shrink:async()=>({blob:{size:3},ext:'.webp'})}});
 vm.runInContext(fs.readFileSync('internal/handler/static/photos.js','utf8'),ctx);
 await new Promise(resolve=>setImmediate(resolve));
 get('site').value ||= token?'blog':'';
 return {get,session,signIn,search,posts,
  add:()=>get('files').events.change({target:{files:[{name:'front.jpg',size:3}]}}),save:()=>get('save').events.click()};
}
test('photo sign-in link names a different account without selecting its same-named site',async()=>{
 for(const sites of [[{name:'blog',user_id:'owner'}],[]]){
  const m=await photosPage({sites});
  assert.equal(m.get('site').value,'');assert.equal(m.get('save').disabled,true);
  assert.equal(m.get('status').textContent,'This link is for alice; you are signed in as bob.');
 }
 for(const options of [{handle:'alice'},{account:''}]){
  const m=await photosPage(options);assert.equal(m.get('site').value,'blog');assert.equal(m.get('status').textContent,'');
 }
});
test('provider sign-in preserves the photo link account and site',async()=>{
 const m=await photosPage({signedIn:false});await m.signIn.startProvider('google');
 assert.equal(m.session.get('sh-photos-return'),'/photos'+m.search);
});

for (const capabilities of [{workerAllowed:false},{wasmAllowed:false}]) {
 test('picker refuses HEIC/TIFF before shrinking when '+JSON.stringify(capabilities), async()=>{
  const m=picker(capabilities);
  await m.add([{name:'phone.heic'},{name:'scan.tiff'},{name:'good.jpg'}]);
  assert.equal(m.shrunk.length,1);assert.equal(m.shrunk[0].name,'good.jpg');
  assert.equal(m.get('fallback').hidden,false);assert.equal(m.get('photos').children.length,1);
 });
}

function decodeHarness(overrides = {}) {
 const ctx=vm.createContext({setTimeout,clearTimeout,Date,Uint8Array,DataView,ArrayBuffer,...overrides});
 const source=fs.readFileSync('internal/handler/static/photo-process.js','utf8').replace('root.SHPhotos = {','root.SHPhotos = { decode, nativeDimensions,');
 vm.runInContext(source,ctx);return ctx.SHPhotos;
}
const photoFile = (bytes, name='photo.png') => ({name,size:bytes.length,arrayBuffer:async()=>Uint8Array.from(bytes).buffer});
function png(width=1,height=1) {
 const b=Buffer.alloc(33);Buffer.from('89504e470d0a1a0a','hex').copy(b);b.writeUInt32BE(13,8);b.write('IHDR',12);b.writeUInt32BE(width,16);b.writeUInt32BE(height,20);return b;
}
function quickClock() {
 let now=0;
 return {Date:{now:()=>now},setTimeout:fn=>setTimeout(()=>{now=20001;fn();},10),clearTimeout};
}
test('native headers reject oversized images before any browser decoder starts',async()=>{
 let calls=0;
 const h=decodeHarness({createImageBitmap:async()=>{calls++;throw Error('must not decode');}});
 const jpeg=Buffer.from('ffd8ffc00011080001000103011100021100031100ffda','hex');jpeg.writeUInt16BE(10000,7);jpeg.writeUInt16BE(10000,9);
 const bmp=Buffer.alloc(54);bmp.write('BM');bmp.writeUInt32LE(40,14);bmp.writeInt32LE(10000,18);bmp.writeInt32LE(10000,22);
 const gif=Buffer.from('GIF89a'+ '\0'.repeat(8));gif.writeUInt16LE(10000,6);gif.writeUInt16LE(10000,8);
 const webp=Buffer.alloc(30);webp.write('RIFF');webp.writeUInt32LE(22,4);webp.write('WEBPVP8X',8);webp.writeUInt32LE(10,16);webp.writeUIntLE(9999,24,3);webp.writeUIntLE(9999,27,3);
 const box=(kind,payload)=>{const b=Buffer.alloc(8+payload.length);b.writeUInt32BE(b.length);b.write(kind,4);payload.copy(b,8);return b;};
 const extent=Buffer.alloc(12);extent.writeUInt32BE(10000,4);extent.writeUInt32BE(10000,8);
 const avif=Buffer.concat([box('ftyp',Buffer.from('avif\0\0\0\0avif')),box('meta',Buffer.concat([Buffer.alloc(4),box('iprp',box('ipco',box('ispe',extent)))]))]);
 for(const b of [png(10000,10000),jpeg,bmp,gif,webp,avif,Buffer.from('<svg width="1" height="1"/>')])await assert.rejects(h.decode(photoFile(b)),/Could not read/);
 assert.equal(calls,0);
});
test('native header parser accepts small canvas JPEG and WebP fixtures',()=>{
 const h=decodeHarness();h.nativeDimensions(Uint8Array.from(png()).buffer);
 for(const name of fs.readdirSync('internal/handler/testdata/photos').filter(n=>/\.(jpg|webp)$/.test(n))) {
  const bytes=fs.readFileSync('internal/handler/testdata/photos/'+name);
  h.nativeDimensions(Uint8Array.from(bytes).buffer);
 }
});
test('native bitmap deadline closes a late result and does not start Image fallback',async()=>{
 let resolve,closed=0,images=0;
 const h=decodeHarness({...quickClock(),createImageBitmap:()=>new Promise(r=>resolve=r),Image:class{constructor(){images++;}}});
 await assert.rejects(h.decode(photoFile(png())),/Could not read/);
 resolve({width:1,height:1,close(){closed++;}});await new Promise(r=>setImmediate(r));
 assert.equal(closed,1);assert.equal(images,0);
});
test('native Image and FileReader fallbacks have deadlines and release resources',async()=>{
 let aborted=0;
 const stalled=decodeHarness({...quickClock(),FileReader:class{readAsDataURL(){}abort(){aborted++;}}});
 await assert.rejects(stalled.decode(photoFile(png())),/Could not read/);assert.equal(aborted,1);
 let img;
 const loaded=decodeHarness({...quickClock(),FileReader:class{readAsDataURL(){this.result='data:image/png;base64,AA==';this.onload();}abort(){}},Image:class{constructor(){img=this;}}});
 await assert.rejects(loaded.decode(photoFile(png())),/Could not read/);assert.equal(img.src,'');
});
test('native and codec workers are terminated when decoding stalls',async()=>{
 for(const name of ['small.png','small.heic','small.tiff']){
  let terminated=0,kind;
  const h=decodeHarness({...quickClock(),Worker:class{postMessage(data){kind=data.kind;}terminate(){terminated++;}}});
  await assert.rejects(h.decode(photoFile(png(),name)),/Could not read/);
  assert.equal(kind,name.endsWith('png')?'native':name.endsWith('heic')?'heif':'tiff');assert.ok(terminated>0);
 }
});
test('native worker returns a transferable bitmap and releases the worker',async()=>{
 let terminated=0,closed=0;
 const bitmap={width:1,height:1,close(){closed++;}};
 const h=decodeHarness({Worker:class{postMessage(data){assert.equal(data.kind,'native');queueMicrotask(()=>this.onmessage({data:{bitmap}}));}terminate(){terminated++;}}});
 const result=await h.decode(photoFile(png()));assert.equal(result.source,bitmap);result.close();assert.equal(closed,1);assert.equal(terminated,1);
});

for(const token of [false,true]) {
 test('/photos checks an uncertain '+(token?'token':'owner')+' batch without uploading again',async()=>{
  let writes=0;
  const m=await photosPage({account:'',token,post:async options=>{
   if(!options.headers['X-Photo-Reconcile']){writes++;throw Error('reply lost after commit');}
   return {ok:true,json:async()=>({site:'blog',version:2,reconciled:true,photos:[{path:'images/front.webp',url:'https://example.test/images/front.webp'}]})};
  }});
  await m.add();await m.save();assert.equal(m.get('save').textContent,'Check previous save');
  assert.equal(m.get('files').disabled,true);assert.equal(m.get('site').disabled,true);
  await m.save();assert.equal(writes,1);assert.equal(m.posts.length,2);
  assert.equal(m.get('status').textContent,'Photos confirmed in version 2.');
 });
 test('/photos keeps checking an unconfirmed '+(token?'token':'owner')+' batch and never repeats the write',async()=>{
  const m=await photosPage({account:'',token,post:async(options,n)=>{
   if(n===1)throw Error('lost reply');
   return {ok:false,status:409,json:async()=>({error:'not found'})};
  }});
  await m.add();await m.save();await m.save();await m.save();
  assert.equal(m.posts.length,3);
  assert.ok(m.posts.slice(1).every(p=>p.options.headers['X-Photo-Reconcile']==='1'));
  assert.equal(m.get('save').textContent,'Check previous save');assert.equal(m.get('files').disabled,true);
 });
}
test('/photos allows an ordinary retry after a definitive validation refusal',async()=>{
 const m=await photosPage({account:'',post:async()=>({ok:false,status:400,json:async()=>({error:'bad name'})})});
 await m.add();await m.save();assert.equal(m.get('files').disabled,false);await m.save();
 assert.ok(m.posts.every(p=>!p.options.headers['X-Photo-Reconcile']));
});
