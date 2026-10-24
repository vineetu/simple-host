package handler

import (
	"os/exec"
	"strings"
	"testing"
)

func TestNotificationDashboardUnavailableSecrets(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	page, err := staticFiles.ReadFile("static/showcase.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(page)
	start := strings.Index(source, "// ==== Notifications (events) ====")
	end := strings.Index(source, "// ==== end Notifications ====")
	if start < 0 || end < start {
		t.Fatal("notifications panel missing")
	}
	program := `
const assert = require('node:assert/strict');
function element() {
  return {children: [], listeners: {}, classList: {toggle() {}},
    appendChild(child) { this.children.push(child); },
    addEventListener(name, fn) { this.listeners[name] = fn; },
    querySelectorAll() { return []; }, focus() {}, reset() {},
    showModal() { this.open = true; },
    set innerHTML(value) { this.children = []; }
  };
}
const nodes = new Map();
global.document = {
  getElementById(id) { if (!nodes.has(id)) nodes.set(id, element()); return nodes.get(id); },
  createElement: element
};
global.window = {};
let ownerMe = null, ownerSites = [], apiKey = 'test-key', HANDLE = 'owner';
const sitePathPart = name => '/v1/sites/' + name;
let enabled = false;
const notification = {id: 'sub_a', site: 'shop', source: 'owner', state: 'active',
  url: 'https://receiver.example', events: [], last_attempt_at: '2026-10-23T00:00:00Z',
  last_result: 'secret_unreadable'};
async function api(method, path) {
  let data;
  if (path.endsWith('/deliveries')) data = {deliveries: [{name: 'record.added', state: 'failed', result: 'secret_unreadable', attempts: 1}]};
  else if (path.endsWith('/test')) data = {delivered: false, result: 'secret_unreadable'};
  else data = {limits: {enabled, per_site: 10, per_minute: 60, per_day: 5000, history_days: 7}, notifications: [notification]};
  return {ok: true, status: 200, json: async () => data};
}
` + source[start:end] + `
(async () => {
  ownerMe = {handle: HANDLE};
  await window.shLoadNotifications();
  const get = id => document.getElementById(id);
  const row = get('notif-list').children[0];
  const buttons = row.children.at(-1).children;
  const button = label => buttons.find(b => b.textContent === label);
  const message = "Stored secret cannot be read with this server's key; rotate it or add the notification again.";
  assert.equal(get('notif-add').disabled, true);
  assert.equal(button('Rotate secret').disabled, true);
  assert.equal(button('Remove').disabled, false);
  assert(row.children.some(p => p.textContent && p.textContent.includes(message)));
  button('Deliveries').listeners.click();
  await new Promise(resolve => setImmediate(resolve));
  assert(get('notif-deliveries').children[0].children.some(p => p.textContent.includes(message)));
  button('Send test').listeners.click();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(get('notif-msg').textContent, message);
  enabled = true;
  await window.shLoadNotifications();
  assert.equal(get('notif-add').disabled, false);
  assert.equal(get('notif-list').children[0].children.at(-1).children.find(b => b.textContent === 'Rotate secret').disabled, false);
})().catch(error => { console.error(error); process.exitCode = 1; });
`
	if output, err := exec.Command(node, "-e", program).CombinedOutput(); err != nil {
		t.Fatalf("notifications dashboard: %v\n%s", err, output)
	}
}

func TestHackNotificationDashboard(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	page, err := staticFiles.ReadFile("static/hack-app.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(page)
	start, end := strings.Index(source, "// ==== Notifications (events) ===="), strings.Index(source, "// ==== end Notifications ====")
	if start < 0 || end < start {
		t.Fatal("missing Hack panel")
	}
	program := `
const assert = require('node:assert/strict');
function element() { return { children: [], listeners: {}, classList: {toggle() {}},
 appendChild(n) {this.children.push(n);}, addEventListener(k,fn) {this.listeners[k]=fn;},
 querySelectorAll() {return [];}, focus() {}, reset() {}, close() {this.open=false;},
 showModal() {this.open=true;}, removeAttribute() {},
 set innerHTML(x) {this.children=[];}
}; }
const nodes = new Map();
global.document={getElementById(id) {if (!nodes.has(id)) nodes.set(id,element());return nodes.get(id);},createElement:element,contains(){return true;}};
global.window={};
const stopped=e=>e.message==='signed out';
let enabled=true, canCreate=true, canManage=true, calls=[], pending=null;
const n={id:'sub_x',site:'team',source:'owner',state:'active',url:'https://receiver.example',events:['record.added']};
async function api(method,path) {
 calls.push([method,path]);
 if (pending) return new Promise(resolve=>{pending=resolve;});
 let data={limits:{enabled,can_create:canCreate,can_manage:canManage,per_site:10,history_days:7},notifications:[n]};
 if(path.endsWith('/deliveries')) data={deliveries:[{name:'record.added',state:'delivered',result:'ok',attempts:1}]};
 if(path.endsWith('/test')) data={delivered:true,result:'ok'};
 if(path.endsWith('/rotate-secret')) data={secret:'test-only'};
 return {ok:true,status:200,data};
}
` + source[start:end] + `
(async()=>{
 const get=id=>document.getElementById(id), tick=()=>new Promise(r=>setImmediate(r));
 const buttons=()=>get('notif-list').children[0].children.at(-1).children;
 const button=label=>buttons().find(b=>b.textContent===label);
 const team='/v1/sites/team/notifications', event='/v1/hack/events/event/website/notifications';
 await window.hackLoadNotifications(team,'team-notifications');
 assert.equal(get('notif-add').hidden,false);
 button('Send test').listeners.click();await tick();
 assert(calls.some(c=>c[1]===team+'/sub_x/test'));
 button('Deliveries').listeners.click();await tick();
 assert.equal(get('notif-deliveries').children.length,1);
 canCreate=false;
 await window.hackLoadNotifications(team,'team-notifications');
 assert.equal(get('notif-add').hidden,true);
 assert(button('Rotate secret'));
 enabled=false;
 await window.hackLoadNotifications(team,'team-notifications');
 assert.equal(button('Rotate secret').hidden,true);
 assert.equal(button('Remove').hidden,false);
 enabled=true;canManage=false;
 await window.hackLoadNotifications(event,'event-notifications');
 assert.equal(button('Send test'),undefined);
 assert.equal(button('Rotate secret'),undefined);
 assert.equal(button('Pause'),undefined);
 assert(button('Deliveries'));assert(button('Remove'));
 canManage=true;
 await window.hackLoadNotifications(event,'event-notifications');
 button('Rotate secret').listeners.click();await tick();
 assert.equal(get('notif-secret').value,'test-only');
 window.hackClearNotifications();
 assert.equal(get('notif-secret').value,'');
 // A late response from the old event cannot restore its rows after navigation.
 pending=true;
 const load=window.hackLoadNotifications(team,'team-notifications');
 window.hackClearNotifications();
 pending({ok:true,status:200,data:{limits:{enabled:true},notifications:[n]}});
 await load;
 assert.equal(get('owner-notifications').hidden,true);
})().catch(e=>{console.error(e);process.exitCode=1;});`
	if out, err := exec.Command(node, "-e", program).CombinedOutput(); err != nil {
		t.Fatalf("Hack notification screen: %v\n%s", err, out)
	}
}
