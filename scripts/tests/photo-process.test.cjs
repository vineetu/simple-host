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

function picker({commit = true, fail = true, failAt = 1, maxBodyBytes = 16000000} = {}) {
  const nodes = new Map(), handlers = {}, writes = []; let files = [], failed = false;
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
    SHPhotos:{slug, dataURL:async()=> 'data:image/webp;base64,YWJj',shrink:async f=>{if(f.bad)throw Error('bad');return {blob:{size:3},ext:'.webp'};}}, TextEncoder});
  vm.runInContext(fs.readFileSync('internal/mcp/apps/photo-picker.js','utf8'),ctx);
  handlers['tool-input']({arguments:{site:'blog'}});handlers['tool-result']({structuredContent:{}});
  return {get,document,handlers,writes,setFiles:value=>files=value,
    add:files=>get('files').events.change({target:{files,value:''}}),save:()=>get('finish').events.click()};
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

async function photosPage({account='alice',handle='bob',sites=[{name:'blog',user_id:'owner'}],signedIn=true}={}) {
 const elements=new Map(),session=new Map();let signIn;
 const element=()=>({children:[],events:{},value:'',textContent:'',hidden:false,
  appendChild(el){this.children.push(el);},replaceChildren(){this.children=[];this.value='';},
  addEventListener(k,fn){this.events[k]=fn;},querySelectorAll(){return [];},
  set selectedIndex(value){if(value===-1)this.value='';}});
 const get=id=>{if(!elements.has(id))elements.set(id,element());return elements.get(id);};
 const search='?site=blog'+(account?'&account='+account:'');
 const ctx=vm.createContext({document:{getElementById:get,createElement:element},
  window:{addEventListener(){},SHSignIn:{mount:(_,options)=>{signIn=options;return {};}}},
  location:{search,hash:'',origin:'https://example.test',assign(){}},history:{},
  localStorage:{getItem:()=>signedIn?'fixture-key':null},sessionStorage:{setItem:(k,v)=>session.set(k,v)},
  fetch:async path=>({ok:true,json:async()=>path==='/v1/me'?{id:'owner',handle}:path==='/v1/sites'?sites:{}}),
  crypto:require('node:crypto').webcrypto,btoa,TextEncoder,Uint8Array,URLSearchParams,SHPhotos:{slug}});
 vm.runInContext(fs.readFileSync('internal/handler/static/photos.js','utf8'),ctx);
 await new Promise(resolve=>setImmediate(resolve));
 return {get,session,signIn,search};
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
