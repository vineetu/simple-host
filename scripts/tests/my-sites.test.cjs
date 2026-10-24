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

function mountSites(callTool=async()=>({}), hack=false) {
 const elements=new Map(),handlers={},queries=[];
 const element=()=>({children:[],dataset:{},style:{},value:'',hidden:false,textContent:'',events:{},
  append(...nodes){this.children.push(...nodes);},replaceChildren(){this.children=[];this.textContent='';},setAttribute(k,v){this[k]=v;},
  addEventListener(k,fn){this.events[k]=fn;},focus(){},remove(){}});
 const get=id=>{if(!elements.has(id))elements.set(id,element());return elements.get(id);};
 const app={hostCapabilities:{serverTools:{}},on:(k,fn)=>handlers[k]=fn,start:async()=>{},
  callTool:async(name,args)=>{queries.push({...args,tool:name});return callTool(name,args);},callToolResult:async(name,args)=>{queries.push({...args,tool:name});return callTool(name,args);},openLink:async url=>{queries.push({open:url});}};
 const ctx=vm.createContext({document:{getElementById:get,createElement:tag=>{assert.notEqual(tag,'iframe');return element();},addEventListener(){},body:{dataset:{dashboard:'true',mode:hack?'events':''}}},SHApp:app,URL,IntersectionObserver:class {constructor(){throw Error('My sites must not observe thumbnails');}}});
 if(hack)vm.runInContext(fs.readFileSync('internal/mcp/apps/hack-sites.js','utf8'),ctx);
 vm.runInContext(fs.readFileSync('internal/mcp/apps/my-sites.js','utf8'),ctx);
 return {get,handlers,queries,app};
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

const settle = () => new Promise(resolve => setImmediate(resolve));
function hackFixture() {
 let team = {event:'spring',team:'alpha',site:'alpha-site'};
 const call = async(name,args) => {
  if(name==='who_am_i')return {team};
  if(name==='hack_get_my_teams')return {teams:[
   {team_slug:'alpha',event_slug:'spring'},
   {team_slug:'beta',event_slug:'autumn',team_name:'Beta',event_title:'Autumn',site_url:'https://beta.autumn.example/'}]};
  if(name==='hack_list_events')return {events:[
   {slug:'organised',title:'Our event',role:'organiser',url:'https://organised.example/'},
   {slug:'judging',role:'judge'}, {slug:'spring',role:'participant'}]};
  if(name==='hack_get_event_website'){assert.equal(args.slug,'organised');return {has_custom_page:true,mode:'custom'};}
  if(name==='site_analytics')return {structuredContent:{totals:{person:{visitors:21}}},_meta:{'simple-host/daily':Array.from({length:7},(_,i)=>({day:'2026-10-'+(18+i),person:{visitors:i}}))}};
  if(name.endsWith('storage_list_resources'))return {response:{resources:[{name:'inbox',kind:'sqlite',preset:'inbox'}]}};
  if(name.endsWith('storage_sql_query')) {
   if(args.sql.includes('sqlite_master'))return {response:{columns:['name'],rows:[['entries']]}};
   if(args.sql.endsWith('LIMIT 0'))return {response:{columns:['id','name','message','created_at'],rows:[]}};
   return {response:{columns:['id','name','message','created_at'],rows:[3,2,1].map(id=>[id,'<img src=x onerror=alert(1)>','<script>bad()</script>','2026-10-24'])}};
  }
  throw Error('unexpected tool '+name);
 };
 return {call, switchTeam:value=>{team=value;}};
}
test('Hack lists selected and other teams, organiser websites, and routes read-only dashboards',async()=>{
 const f=hackFixture(),m=mountSites(f.call,true);
 await m.handlers['tool-result']({structuredContent:{sites:[{name:'alpha-site',url:'https://alpha.spring.example/'}]}});
 assert.equal(m.get('rows').children.length,3);
 assert.match(m.get('team-status').textContent,/Selected team: alpha · spring/);
 assert.match(m.get('rows').children[0].children[1].children[1].textContent,/Selected team/);
 await m.get('rows').children[0].events.click();await settle();
 assert.equal(m.get('visitor-count').textContent,'21');
 assert.equal(m.get('chart').children.length,7);
 assert.equal(m.get('entry-rows').children.length,3);
 assert.equal(m.get('entry-rows').children[0].children[0].textContent,'<img src=x onerror=alert(1)>');
 assert.equal(m.get('entry-rows').children[0].children[1].textContent,'<script>bad()</script>');
 assert.ok(m.queries.some(q=>q.tool==='storage_sql_query'&&q.site==='alpha-site'));
 m.get('back').events.click();
 await m.get('rows').children[1].events.click();await settle();
 assert.equal(m.queries.at(-1).open,'https://beta.autumn.example/');
 const before=m.queries.length;
 await m.get('rows').children[2].events.click();await settle();
 assert.equal(m.get('analytics').hidden,true);
 assert.equal(m.get('entry-rows').children.length,3);
 assert.ok(m.queries.slice(before).every(q=>q.tool.startsWith('hack_event_storage_')&&q.event==='organised'&&!q.site));
 assert.ok(m.queries.every(q=>!['run_tool','hack_select_team','create_upload_link','get_site'].includes(q.tool)));
});
test('Hack does not read a stale selected team or relabel old list results as the new team',async()=>{
 const f=hackFixture(),m=mountSites(f.call,true);
 await m.handlers['tool-result']({structuredContent:{sites:[{name:'alpha-site'}]}});
 f.switchTeam({event:'autumn',team:'beta',site:'beta-site'});
 const before=m.queries.length;
 await m.get('rows').children[0].events.click();await settle();
 assert.match(m.get('entries-error').textContent,/selected team changed/);
 assert.deepEqual(m.queries.slice(before).map(q=>q.tool),['who_am_i']);
 await m.handlers['tool-result']({structuredContent:{sites:[{name:'alpha-site'}]}});
 assert.match(m.get('rows').children[0].children[1].children[1].textContent,/Refresh in chat/);
});
test('Hack organiser can browse event websites without selecting a team',async()=>{
 const f=hackFixture();f.switchTeam(undefined);
 const m=mountSites(f.call,true);
 await m.handlers['tool-result']({structuredContent:{sites:[]}});
 assert.match(m.get('team-status').textContent,/No team selected/);
 assert.equal(m.get('rows').children.length,3);
 await m.get('rows').children[2].events.click();await settle();
 assert.equal(m.get('analytics').hidden,true);
 assert.equal(m.get('entry-rows').children.length,3);
});
test('Hack partial list and revoked storage permissions show errors without cross-role fallback',async()=>{
 const f=hackFixture(),m=mountSites(async(name,args)=>{
  if(name==='hack_get_my_teams'||name==='hack_event_storage_list_resources')throw Error('permission revoked');
  return f.call(name,args);
 },true);
 await m.handlers['tool-result']({structuredContent:{sites:[{name:'alpha-site'}]}});
 assert.equal(m.get('rows').children.length,2);
 assert.match(m.get('team-status').textContent,/Could not load other teams/);
 const before=m.queries.length;
 await m.get('rows').children[1].events.click();await settle();
 assert.match(m.get('entries-error').textContent,/Could not load entries/);
 assert.deepEqual(m.queries.slice(before).map(q=>q.tool),['hack_event_storage_list_resources']);
});
test('filtered Hack list preserves scope and does not append unfiltered teams or events',async()=>{
 const f=hackFixture(),m=mountSites(f.call,true);
 m.handlers['tool-input']({arguments:{query:'nothing'}});
 await m.handlers['tool-result']({structuredContent:{sites:[]}});
 assert.equal(m.get('rows').children.length,0);
 assert.deepEqual(m.queries.map(q=>q.tool),['who_am_i']);
});
test('Hack keeps returned links usable without host tool access',async()=>{
 const m=mountSites(async()=>{throw Error('must not call tools');},true);
 m.app.hostCapabilities={};
 await m.handlers['tool-result']({structuredContent:{sites:[{name:'alpha',url:'https://alpha.example/'}]}});
 assert.equal(m.get('rows').children.length,1);
 m.get('rows').children[0].events.click();await settle();
 assert.deepEqual(m.queries,[{open:'https://alpha.example/'}]);
});
test('Hack custom event dashboard shows an empty state when no entry storage exists',async()=>{
 const f=hackFixture(),m=mountSites(async(name,args)=>name==='hack_event_storage_list_resources'?{response:{resources:[]}}:f.call(name,args),true);
 await m.handlers['tool-result']({structuredContent:{sites:[{name:'alpha-site'}]}});
 m.get('rows').children[2].events.click();await settle();
 assert.equal(m.get('entries').hidden,false);
 assert.equal(m.get('entry-rows').children[0].textContent,'No entries yet.');
});
