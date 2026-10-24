const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync('internal/handler/static/arrival.js', 'utf8');
function visit(path, referrer = '') {
  const location = new URL(path, 'https://simple-host.app');
  const listeners = {};
  const links = [];
  const document = { referrer, addEventListener: (name, fn) => { listeners[name] = fn; }, querySelectorAll: () => links };
  const window = {};
  // Any attempted browser storage access fails this test.
  Object.defineProperty(document, 'cookie', { get() { throw Error('cookie read'); }, set() { throw Error('cookie write'); } });
  vm.runInNewContext(source, { location, document, window, URL, URLSearchParams });
  return { url: window.SHArrival.url, links, listeners };
}
test('landing labels follow sign-in, verify, and OAuth return URLs without storage', () => {
  const first = visit('/?from=artifact-hub&utm_source=launch&utm_medium=web', 'https://artifact-hub.com/path?private=value');
  const dashboard = first.url('/dashboard');
  assert.equal(new URL(dashboard).searchParams.get('referrer'), 'artifact-hub.com');
  assert.ok(!dashboard.includes('private'));
  const signIn = visit(dashboard);
  for (const path of ['/v1/auth/verify', '/?cn=nonce', '/oauth/authorize?client_id=app&cn=nonce', '/signin?next=%2Fevents%2Fnew']) {
    const q = new URL(signIn.url(path)).searchParams;
    assert.equal(q.get('from'), 'artifact-hub');
    assert.equal(q.get('utm_source'), 'launch');
    assert.equal(q.get('utm_medium'), 'web');
  }
  assert.equal(signIn.url('https://external.test/'), 'https://external.test/');
  assert.equal(visit('/dashboard').url('/v1/auth/verify'), '/v1/auth/verify');
  assert.equal(visit('/?from=%3Cscript%3E').url('/dashboard'), '/dashboard');
});
test('landing action links carry the query and retain their destination', () => {
  const page = visit('/?from=agentcon&utm_campaign=event');
  const a = { href: '/events/new', getAttribute() { return this.href; } };
  page.links.push(a);
  page.listeners.DOMContentLoaded();
  assert.equal(new URL(a.href).pathname, '/events/new');
  assert.equal(new URL(a.href).searchParams.get('from'), 'agentcon');
});
