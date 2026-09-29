// auth.js's choice of API address, per page host.  node web/auth-base.test.js [auth.js]
//
// The file under test is the served auth.js (the Go test TestAuthJSBases
// passes the copy served while addresses move between two domains; with no
// argument, the embedded file as written). Each case loads it in a stub
// browser and records the first API call SH.me() makes.
const assert = require('assert');
const fs = require('fs');
const path = require('path');

const file = process.argv[2] || path.join(__dirname, '..', 'internal', 'handler', 'static', 'auth.js');
const src = fs.readFileSync(file, 'utf8');
const cases = JSON.parse(process.argv[3] || '[]');

function load(href) {
  const u = new URL(href);
  const calls = [];
  const window = {};
  const location = { hostname: u.hostname, pathname: u.pathname, origin: u.origin, protocol: u.protocol, href: href, search: '' };
  const fetch = function (url) {
    calls.push(String(url));
    // The person-path probe (/v1/sites/<first segment>/me) finds the site.
    return Promise.resolve({ ok: true, status: 200, headers: { get: function () { return null; } }, json: function () { return Promise.resolve({}); }, text: function () { return Promise.resolve('{}'); } });
  };
  const document = { readyState: 'complete', addEventListener: function () {}, querySelector: function () { return null; }, querySelectorAll: function () { return []; }, createElement: function () { return { style: {}, setAttribute: function () {}, appendChild: function () {} }; }, head: { appendChild: function () {} }, body: { appendChild: function () {} }, cookie: '' };
  new Function('window', 'location', 'fetch', 'document', 'console', 'CustomEvent', 'localStorage', 'sessionStorage', src)(
    window, location, fetch, document, { info: function () {}, warn: function () {}, log: function () {}, error: function () {} },
    function () {}, { getItem: function () { return null; }, setItem: function () {} }, { getItem: function () { return null; }, setItem: function () {} });
  return { SH: window.SH, calls: calls };
}

let n = 0, failed = 0;
async function check(href, want) {
  n++;
  const b = load(href);
  try {
    await b.SH.me().catch(function () {});
    const last = b.calls[b.calls.length - 1] || '';
    assert.strictEqual(last, want);
    console.log('ok ' + n + ' - ' + href);
  } catch (e) {
    failed++;
    console.log('not ok ' + n + ' - ' + href + '\n    want ' + want + '\n    got  ' + (b.calls.join(', ') || '(no call)'));
  }
}

(async function () {
  const all = cases.length ? cases : [
    // As written: the person-host test is one label under simple-host.app.
    ['https://olive.simple-host.app/shop/', 'https://olive.simple-host.app/v1/sites/shop/me'],
    ['https://shop.olive.simple-host.app/', 'https://shop.olive.simple-host.app/v1/sites/shop/me'],
    ['https://clay.simple-host.app/', 'https://clay.simple-host.app/v1/sites/clay/me'],
    ['https://sites.simple-host.app/olive/shop/', 'https://sites.simple-host.app/v1/u/olive/sites/shop/me'],
  ];
  for (const [href, want] of all) await check(href, want);
  console.log('# ' + n + ' tests, ' + failed + ' failed');
  process.exit(failed ? 1 : 0);
})();
