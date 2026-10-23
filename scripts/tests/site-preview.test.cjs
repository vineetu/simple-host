const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

function mount({clipboard = true, modes = ['inline', 'fullscreen'], response = 'fullscreen'} = {}) {
  const elements = new Map(), handlers = {}, events = {}, requests = [], timers = [];
  const element = () => ({hidden:false, style:{}, clientWidth:390, textContent:'', children:[],
    setAttribute(key,value){this[key]=value;}, append(child){this.children.push(child);},
    remove(){this.removed=true;}, focus(){this.focused=true;}, select(){this.selected=true;}});
  const get = id => { if (!elements.has(id)) elements.set(id,element()); return elements.get(id); };
  const app = {hostContext:{availableDisplayModes:modes,displayMode:'inline'}, on:(name,fn)=>handlers[name]=fn,
    start:async()=>{}, openLink:async url=>requests.push({url}), requestDisplayMode:async mode=>{
      requests.push({mode}); if(response instanceof Error) throw response; return {mode:response};
    }};
  const context = vm.createContext({window:{SHApp:app}, document:{getElementById:get,
    addEventListener:(name,fn)=>events[name]=fn,createElement:element,body:{classList:{toggle(){}}}}, URL,
    navigator:{clipboard:{writeText:async value=>{if(!clipboard) throw Error('denied'); requests.push({copied:value});}}},
    ResizeObserver:class{observe(){}}, setTimeout:(fn,ms)=>{timers.push({fn,ms});return timers.length;},clearTimeout(){}});
vm.runInContext(fs.readFileSync('internal/mcp/apps/frames.js','utf8'),context);
  vm.runInContext(fs.readFileSync('internal/mcp/apps/site-preview.js','utf8'),context);
  const result = (extra={}) => handlers['tool-result']({structuredContent:{name:'<img src=x onerror=alert(1)>',url:'https://blog.alice.simple-host.app/',site_preview:{open_to_anyone:true},...extra}});
  return {get:id=>get('preview-'+id),result,handlers,events,requests,timers,app};
}

test('thumbnail is inert; protected and unknown results never load an iframe', async()=>{
  const m=mount(); await Promise.resolve();
  m.result();const frame=m.get('visual').children.at(-1);
  assert.equal(frame.src,'https://blog.alice.simple-host.app/?_sh_preview=1');assert.equal(frame.inert,true);assert.equal(frame.tabIndex,-1);assert.equal(frame.sandbox,'');assert.equal(frame.credentialless,'');assert.equal(frame.referrerpolicy,'no-referrer');assert.equal(frame.style.pointerEvents,'none');
  assert.equal(m.get('title').textContent,'<img src=x onerror=alert(1)>');
  for(const preview of [{}, {open_to_anyone:false}, {open_to_anyone:'true'}]) {
    const before=m.get('visual').children.length;m.result({site_preview:preview});
    assert.equal(m.get('visual').children.length,before);assert.equal(m.get('tile').hidden,false);
  }
  assert.equal(frame.removed,true);
});
test('custom-domain link opens unchanged, but only its returned platform URL embeds', async()=>{
  const m=mount();await Promise.resolve();
  const url='https://example.org/';m.result({url,site_preview:{open_to_anyone:true,canonical_url:'https://blog.alice.simple-host.app/'}});
  assert.equal(m.get('visual').children.at(-1).src,'https://blog.alice.simple-host.app/?_sh_preview=1');
  await m.get('open').onclick();assert.deepEqual(m.requests,[{url}]);
  for(const bad of ['https://simple-host.app.evil.org/','https://simple-host.app/','http://blog.alice.simple-host.app/','https://evil.org/','https://user@blog.alice.simple-host.app/']) {
    const count=m.get('visual').children.length;m.result({url:bad});assert.equal(m.get('visual').children.length,count);
  }
  m.result({url:'javascript:alert(1)'});assert.equal(m.get('open').disabled,true);
});
test('fullscreen follows actual response and notifications, then Back requests inline', async()=>{
  const m=mount();await Promise.resolve();m.result();
  await m.get('full').onclick();assert.equal(m.get('details').hidden,true);
  assert.equal(m.get('visual').children.at(-1).inert,false);
  assert.equal(m.get('visual').children.at(-1).sandbox,'allow-scripts');assert.equal(m.get('back').focused,true);assert.equal(m.get('visual').children[0].removed,true);
  m.handlers['host-context-changed']({theme:'dark'});assert.equal(m.get('details').hidden,true);
  await m.get('back').onclick();assert.deepEqual(m.requests,[{mode:'fullscreen'},{mode:'inline'}]);
  m.handlers['host-context-changed']({displayMode:'inline'});assert.equal(m.get('details').hidden,false);
  assert.equal(m.get('visual').children.at(-1).inert,true);assert.equal(m.get('visual').children.at(-1).sandbox,'');assert.equal(m.get('full').focused,true);
});
test('unsupported or refused fullscreen stays inline', async()=>{
  for(const response of ['inline',null,new Error('not supported')]) {
    const m=mount({response});await Promise.resolve();m.result();await m.get('full').onclick();
    assert.equal(m.get('details').hidden,false);
  }
  const m=mount({modes:['inline']});await Promise.resolve();m.result();
  assert.equal(m.get('full').hidden,true);await m.get('full').onclick();assert.equal(m.requests.length,0);
});
test('clipboard success lasts 1.5 seconds; denial selects the returned URL', async()=>{
  const m=mount();await Promise.resolve();m.result();await m.get('copy').onclick();
  assert.equal(m.get('copy').textContent,'Copied');assert.equal(m.timers.at(-1).ms,1500);
  m.timers.at(-1).fn();assert.equal(m.get('copy').textContent,'Copy link');
  const denied=mount({clipboard:false});await Promise.resolve();denied.result();await denied.get('copy').onclick();
  assert.equal(denied.get('copy-fallback').hidden,false);assert.equal(denied.get('link').selected,true);
  assert.equal(denied.get('link').value,'https://blog.alice.simple-host.app/');assert.equal(denied.get('copy').textContent,'Copy link');
});

test('host frame denial keeps a tile and prevents further nested loads', async()=>{
  const m=mount();await Promise.resolve();m.result();
  const frame=m.get('visual').children.at(-1);
  m.events.securitypolicyviolation({effectiveDirective:'frame-src'});
  assert.equal(frame.removed,true);assert.equal(m.get('tile').hidden,false);
  const count=m.get('visual').children.length;m.result();
  assert.equal(m.get('visual').children.length,count);assert.equal(m.get('tile').hidden,false);
  const denied=mount();denied.app.hostCapabilities={sandbox:{csp:{frameDomains:[]}}};
  denied.result();assert.equal(denied.get('visual').children.length,0);assert.equal(denied.get('tile').hidden,false);
});

test('unrelated results cannot replace the get_site card or its links',async()=>{
 const m=mount();await Promise.resolve();m.result();const count=m.get('visual').children.length;
 for(const site_preview of [undefined,null,[],true,'yes'])m.handlers['tool-result']({structuredContent:{url:'https://other.example/',site_preview}});
 assert.equal(m.get('visual').children.length,count);await m.get('open').onclick();assert.equal(m.requests.at(-1).url,'https://blog.alice.simple-host.app/');
});
test('shared frame URL refuses ports and non-platform authorities and preserves queries',()=>{
 const ctx=vm.createContext({URL});vm.runInContext(fs.readFileSync('internal/mcp/apps/frames.js','utf8'),ctx);
 for(const value of ['https://user@a.simple-host.app/','https://a.simple-host.app:443/','https://a.simple-host.app:8443/','https://a_b.simple-host.app/','https://simple-host.app/','http://a.simple-host.app/','https://a.simple-host.app.evil/']) assert.equal(ctx.SHFrameURL(value),'',value);
 assert.equal(ctx.SHFrameURL('https://a.simple-host.app/path?x=2#part'),'https://a.simple-host.app/path?x=2&_sh_preview=1#part');
});
test('Claude secondary light text reaches 4.5 contrast on both surfaces',()=>{
 const css=fs.readFileSync('internal/mcp/apps/shared.css','utf8');const tokens=css.match(/:root\[data-host=claude\]\{([^}]+)/)[1];
 const color=name=>tokens.match(new RegExp('--'+name+':(#[a-f0-9]{6})'))[1];
 const luminance=hex=>[1,3,5].map(i=>parseInt(hex.slice(i,i+2),16)/255).map(v=>v<=.04045?v/12.92:((v+.055)/1.055)**2.4).reduce((sum,v,i)=>sum+v*[.2126,.7152,.0722][i],0);
 for(const background of ['bg','surface'])assert.ok((luminance(color(background))+.05)/(luminance(color('sub'))+.05)>=4.5);
});
