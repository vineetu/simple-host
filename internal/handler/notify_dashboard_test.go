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
