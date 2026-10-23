const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const context = vm.createContext({URL});
vm.runInContext(fs.readFileSync('internal/mcp/apps/my-sites.js', 'utf8'), context);
const {renderEntry, node, badge, identifier} = context.SHSites;
// A DOM double rejects HTML parsing; tests exercise the production constructors.
class Element {
  constructor(tag) { this.tag = tag; this.children = []; }
  set innerHTML(_) { throw Error('HTML parsing is forbidden'); }
  set textContent(value) { this.text = value; }
  append(...children) { this.children.push(...children); }
}
const document = {createElement: tag => new Element(tag)};
test('hostile site names and visitor entries are text, never markup', () => {
  for (const hostile of ['<img src=x onerror=alert(1)>', '\"><script>alert(1)</script>']) {
    const site = node(document, 'strong', '', hostile);
    assert.equal(site.text, hostile);
    assert.equal(site.children.length, 0);
    const entry = renderEntry(document, {name: hostile, message: hostile, created_at: '2026-10-23'});
    assert.equal(entry.children[0].text, hostile);
    assert.equal(entry.children[1].text, hostile);
    assert.equal(entry.children[1].children.length, 0);
  }
});
test('site access badges remain available with initial tiles', () => {
  assert.equal(badge({listed:true}), 'Access unknown');
  assert.equal(badge({display:{access:'anyone'}}), 'Anyone');
  assert.equal(badge({display:{access:'specific'}}), 'Named viewers');
  assert.equal(badge({display:{passcode_protected:true}}), 'Passcode');
  assert.equal(badge({offline:true}), 'Offline');
});
test('discovered SQLite identifiers stay identifiers', () => {
  assert.equal(identifier('messages"; DROP TABLE messages; --'), '"messages""; DROP TABLE messages; --"');
});

function mountSites(callTool=async()=>({})) {
 const elements=new Map(),handlers={},queries=[];
 const element=()=>({children:[],dataset:{},style:{},value:'',hidden:false,textContent:'',events:{},
  append(...nodes){this.children.push(...nodes);},replaceChildren(){this.children=[];this.textContent='';},setAttribute(k,v){this[k]=v;},
  addEventListener(k,fn){this.events[k]=fn;},focus(){},remove(){}});
 const get=id=>{if(!elements.has(id))elements.set(id,element());return elements.get(id);};
 const app={hostCapabilities:{serverTools:{}},on:(k,fn)=>handlers[k]=fn,start:async()=>{},
  callTool:async(name,args)=>{queries.push({name,...args});return callTool(name,args);},callToolResult:async()=>{throw Error('unavailable');}};
 const ctx=vm.createContext({document:{getElementById:get,createElement:tag=>{assert.notEqual(tag,'iframe');return element();},addEventListener(){},body:{dataset:{dashboard:'true'}}},SHApp:app,URL,IntersectionObserver:class {constructor(){throw Error('My sites must not observe thumbnails');}}});
 vm.runInContext(fs.readFileSync('internal/mcp/apps/my-sites.js','utf8'),ctx);
 return {get,handlers,queries};
}
test('every site uses its initial tile without nested frames or preview URLs',()=>{
 const m=mountSites();
 const sites=[
  {name:'public',summary:{title:'Harbor'},display:{access:'anyone',passcode_protected:false}},
  {name:'private',display:{access:'specific',passcode_protected:false}},
  {name:'locked',display:{access:'anyone',passcode_protected:true}},
  {name:'offline',offline:true}, {name:'unknown'}, {name:''}
 ].map(site=>({...site,url:'https://shop.alice.simple-host.app/'}));
 m.handlers['tool-result']({structuredContent:{sites}});
 assert.deepEqual(m.get('rows').children.map(row=>row.children[0].textContent),['H','P','L','O','U','?']);
 for(const row of m.get('rows').children){
  assert.equal(row.children[0].children.length,0);
  assert.equal(row.children[0].dataset.url,undefined);
 }
 m.get('search').value='harbor';m.get('search').events.input();
 assert.equal(m.get('rows').children.length,1);
 assert.equal(m.get('rows').children[0].children[0].textContent,'H');
});
test('initial list errors and cancellation end loading',()=>{
 const m=mountSites();
 m.handlers['tool-result']({isError:true});assert.match(m.get('list-status').textContent,/Could not load/);assert.equal(m.get('rows').children.length,0);
 m.handlers['tool-cancelled']({});assert.match(m.get('list-status').textContent,/cancelled/);
});
test('filtered inputs label local search scope',()=>{
 const m=mountSites();
 for(const key of ['query','filters','limit','offset']){
  m.handlers['tool-input']({arguments:{[key]:1}});
  m.handlers['tool-result']({structuredContent:{sites:[{name:'blog'}]}});
  assert.equal(m.get('view-label').textContent,'Showing 1 sites');
 }
});
test('later tool errors and cancellations preserve the loaded list and dashboard back navigation',()=>{
 for(const sites of [[],[{name:'blog'}]]){
  const m=mountSites();m.handlers['tool-result']({structuredContent:{sites}});
  if(sites.length)m.get('rows').children[0].events.click();
  for(const notify of [()=>m.handlers['tool-result']({isError:true}),()=>m.handlers['tool-cancelled']({})]){
   notify();assert.equal(m.get('list-status').textContent,sites.length?'':'No sites yet.');
  }
  if(sites.length)m.get('back').events.click();
  assert.equal(m.get('rows').children.length,sites.length);
  assert.equal(m.get('list-status').textContent,sites.length?'':'No sites yet.');
 }
});
test('dashboard normalizes own policies, discovers literal sqlite prefix, orders available columns, and isolates failures',async()=>{
 const m=mountSites(async(name,args)=>{
  if(name==='run_tool')return {response:{resources:[
   {kind:'sqlite',name:'broken-resource',preset:'inbox'},
   {kind:'sqlite',name:'db',preset:'inbox',tables:{secrets:{preset:'private'}}},
   {kind:'sqlite',name:'private-db',preset:'private',tables:{leads:{preset:'inbox'}}}
  ]}};
  if(args.name==='broken-resource')throw Error('discovery failed');
  if(args.sql.includes('sqlite_master')){assert.match(args.sql,/NOT GLOB 'sqlite_\*'/);return {response:{columns:['name'],rows:args.name==='db'?[['Secrets'],['Broken'],['constructor'],['sqliteleads'],['Dates'],['Plain']]:[['Leads']]}};}
  if(args.sql.includes('"Broken"'))throw Error('table failed');
  assert.ok(!args.sql.includes('"Secrets"'));
  const columns=args.sql.includes('"Dates"')?['created_at','name']:args.sql.includes('"Plain"')?['name']:['id','name'];
  if(args.sql.endsWith('LIMIT 0'))return {response:{columns,rows:[]}};
  assert.match(args.sql,new RegExp('ORDER BY '+(columns[0]==='name'?'rowid':columns[0])+' DESC LIMIT 3'));
  return {response:{columns,rows:columns[0]==='name'?[['Plain']]:[[1,args.name]]}};
 });
 m.handlers['tool-result']({structuredContent:{sites:[{name:'blog'}]}});m.get('rows').children[0].events.click();
 await new Promise(resolve=>setImmediate(resolve));
 assert.equal(m.get('entries').hidden,false);assert.match(m.get('entries-title').textContent,/constructor/);assert.match(m.get('entries-title').textContent,/sqliteleads/);assert.match(m.get('entries-title').textContent,/Leads/);
 assert.match(m.get('entries-error').textContent,/broken-resource/);assert.match(m.get('entries-error').textContent,/Broken/);assert.equal(m.get('entry-rows').children.length,3);
});

test('dashboard SQL keeps the newest IDs when timestamps tie and supports missing columns',async()=>{
 for(const columns of [['created_at','id','name'],['created_at','name'],['id','name'],['name']]){
  const m=mountSites(async(name,args)=>{
   if(name==='run_tool')return {response:{resources:[{kind:'sqlite',name:'db',preset:'inbox'}]}};
   if(args.sql.includes('sqlite_master'))return {response:{columns:['name'],rows:[['entries']]}};
   if(args.sql.endsWith('LIMIT 0'))return {response:{columns,rows:[]}};
   const order=columns.includes('created_at')?(columns.includes('id')?'created_at DESC, id DESC':'created_at DESC'):columns.includes('id')?'id DESC':'rowid DESC';
   assert.ok(args.sql.endsWith('ORDER BY '+order+' LIMIT 3'));
   const output=require('node:child_process').execFileSync('python3',['-c',`
import json, sqlite3, sys
columns, query = json.loads(sys.argv[1]), sys.argv[2]
db = sqlite3.connect(':memory:')
db.execute('CREATE TABLE entries (' + ', '.join(c + (' INTEGER PRIMARY KEY' if c == 'id' else ' TEXT') for c in columns) + ')')
for i in range(1, 6):
    db.execute('INSERT INTO entries VALUES (' + ','.join('?' for c in columns) + ')', [i if c == 'id' else '2026-10-23' if c == 'created_at' else 'Entry ' + str(i) for c in columns])
print(json.dumps(db.execute(query).fetchall()))
`,JSON.stringify(columns),args.sql],{encoding:'utf8'});
   return {response:{columns,rows:JSON.parse(output)}};
  });
  m.handlers['tool-result']({structuredContent:{sites:[{name:'blog'}]}});m.get('rows').children[0].events.click();
  await new Promise(resolve=>setImmediate(resolve));
  assert.equal(m.get('entries-error').textContent,'');
  if(columns.includes('id')||!columns.includes('created_at'))assert.deepEqual(m.get('entry-rows').children.map(row=>row.children[0].textContent),['Entry 5','Entry 4','Entry 3']);
  else assert.equal(m.get('entry-rows').children.length,3);
 }
});
