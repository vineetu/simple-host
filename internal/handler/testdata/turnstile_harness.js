// Checks the spam check's postMessage rules both ways without a browser:
// the frame page's script (turnstile.go) and auth.js's spamCheck, each run
// in a fake window. Run by TestTurnstileMessagesJS.
"use strict";
const fs = require("fs");
const vm = require("vm");
const assert = require("assert");
const { webcrypto } = require("crypto");

const fx = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const tick = () => new Promise((r) => setTimeout(r, 20));

function fakeWindow() {
  const listeners = {};
  const w = {
    addEventListener(type, fn) { (listeners[type] = listeners[type] || []).push(fn); },
    removeEventListener(type, fn) { listeners[type] = (listeners[type] || []).filter((f) => f !== fn); },
    dispatch(type, ev) { (listeners[type] || []).slice().forEach((fn) => fn(ev)); },
    listeners,
  };
  return w;
}

async function frameChecks() {
  const sent = [];
  const parent = { postMessage(msg, origin) { sent.push({ msg, origin }); } };
  const stranger = { postMessage() {} };
  const w = fakeWindow();
  let rendered = null;
  w.parent = parent;
  w.turnstile = { render(sel, opts) { rendered = opts; } };
  const ctx = vm.createContext({ window: w, crypto: webcrypto, TextEncoder, URL, Promise, Array, Uint8Array, String });
  vm.runInContext(fx.frame, ctx);
  assert.strictEqual(JSON.stringify(sent.map((s) => s.msg.type)), JSON.stringify(["sh-turnstile-ready"]), "frame says ready once");
  const start = { type: "sh-turnstile-start", resource: "guestbook" };
  // Not from the parent window: ignored.
  w.dispatch("message", { source: stranger, origin: "https://guest.ann.simple-host.app", data: start });
  // From the parent but an opaque origin: ignored.
  w.dispatch("message", { source: parent, origin: "null", data: start });
  // A bad resource name: ignored.
  w.dispatch("message", { source: parent, origin: "https://guest.ann.simple-host.app", data: { type: "sh-turnstile-start", resource: "../x" } });
  await tick();
  assert.strictEqual(rendered, null, "frame must ignore strangers, opaque origins and bad names");
  // The parent, with a real origin: renders bound to that origin and resource.
  w.dispatch("message", { source: parent, origin: "https://guest.ann.simple-host.app", data: start });
  await tick();
  assert.ok(rendered, "frame renders for its parent");
  assert.strictEqual(rendered.cdata, fx.cdata, "cdata binds host and resource the way the server does");
  assert.strictEqual(rendered.action, "sh-post");
  // A second start (say, from a script that changed the parent's mind) is ignored.
  const first = rendered;
  w.dispatch("message", { source: parent, origin: "https://evil.example", data: start });
  await tick();
  assert.strictEqual(rendered, first, "frame starts once");
  rendered.callback("TOKEN-1");
  const last = sent[sent.length - 1];
  assert.strictEqual(JSON.stringify(last.msg), JSON.stringify({ type: "sh-turnstile-token", token: "TOKEN-1" }));
  assert.strictEqual(last.origin, "https://guest.ann.simple-host.app", "token goes to the parent's origin only, never *");
  // Opened on its own (no parent): nothing at all.
  const alone = fakeWindow();
  alone.parent = alone;
  let lonely = 0;
  alone.postMessage = () => { lonely++; };
  vm.runInContext(fx.frame, vm.createContext({ window: alone, crypto: webcrypto, TextEncoder, URL }));
  assert.strictEqual(lonely, 0);
  assert.strictEqual((alone.listeners.message || []).length, 0);
  console.log("frame: ok");
}

function fakeElement(tag) {
  const el = {
    tagName: tag.toUpperCase(), style: {}, children: [], attrs: {}, parentNode: null,
    setAttribute(k, v) { this.attrs[k] = v; },
    appendChild(c) { c.parentNode = this; this.children.push(c); return c; },
    removeChild(c) { this.children = this.children.filter((x) => x !== c); c.parentNode = null; },
    addEventListener() {}, querySelector() { return null; }, querySelectorAll() { return []; },
  };
  if (tag === "iframe") {
    el.contentWindow = { posted: [], postMessage(msg, origin) { this.posted.push({ msg, origin }); } };
  }
  return el;
}

async function authChecks() {
  const w = fakeWindow();
  const created = [];
  const body = fakeElement("body");
  const doc = {
    readyState: "complete", body, documentElement: body, cookie: "",
    createElement(tag) { const e = fakeElement(tag); created.push(e); return e; },
    addEventListener() {}, removeEventListener() {}, querySelector() { return null; }, querySelectorAll() { return []; },
    getElementById() { return null; }, head: fakeElement("head"),
  };
  const calls = [];
  let answers = [];
  function fetch(url, opts) {
    // Only storage calls; auth.js may ask other things at load.
    if (String(url).indexOf("/storage/") < 0) return Promise.resolve({ ok: true, status: 200, headers: { get() { return null; } }, text() { return Promise.resolve("{}"); }, json() { return Promise.resolve({}); } });
    calls.push({ url, opts });
    const a = answers.shift() || { status: 200, body: {} };
    return Promise.resolve({
      ok: a.status < 300, status: a.status, headers: { get() { return null; } },
      text() { return Promise.resolve(JSON.stringify(a.body)); },
      json() { return Promise.resolve(a.body); },
    });
  }
  const location = { hostname: "guest.ann.simple-host.app", pathname: "/", origin: "https://guest.ann.simple-host.app", protocol: "https:", href: "https://guest.ann.simple-host.app/", search: "", hash: "" };
  w.top = w; w.self = w; w.location = location; w.document = doc;
  const store = {};
  const storage = { getItem: (k) => (k in store ? store[k] : null), setItem: (k, v) => { store[k] = String(v); }, removeItem: (k) => { delete store[k]; } };
  const ctx = vm.createContext({
    window: w, document: doc, location, fetch, URL, URLSearchParams, Promise, setTimeout, clearTimeout,
    CustomEvent: function (type, init) { this.type = type; this.detail = init && init.detail; },
    console, localStorage: storage, sessionStorage: storage, navigator: { userAgent: "node" }, history: { replaceState() {} },
  });
  ctx.window.SH_CONFIG = undefined;
  vm.runInContext(fx.auth, ctx);
  const SH = ctx.window.SH || ctx.SH;
  assert.ok(SH && SH.storage, "auth.js defines SH");
  answers = [{ status: 403, body: { code: "spam_check_required", error: "needs the spam check" } }, { status: 200, body: { changes: 1 } }];
  const p = SH.storage.sqlite("guestbook").table("entries").add({ name: "Ann", message: "hi" });
  await tick();
  const frame = created.find((e) => e.tagName === "IFRAME");
  assert.ok(frame, "a spam check frame opens");
  assert.strictEqual(frame.src, "https://simple-host.app/v1/turnstile/frame");
  const appOrigin = "https://simple-host.app";
  const stranger = { postMessage() {} };
  // Wrong origin, or the right origin from another window: ignored.
  w.dispatch("message", { origin: "https://evil.example", source: frame.contentWindow, data: { type: "sh-turnstile-token", token: "EVIL" } });
  w.dispatch("message", { origin: appOrigin, source: stranger, data: { type: "sh-turnstile-token", token: "EVIL2" } });
  w.dispatch("message", { origin: "https://evil.example", source: frame.contentWindow, data: { type: "sh-turnstile-ready" } });
  await tick();
  assert.strictEqual(calls.length, 1, "no retry on a stranger's token");
  assert.strictEqual(frame.contentWindow.posted.length, 0, "no start sent on a stranger's ready");
  // The frame says ready: the start goes to the app's origin only.
  w.dispatch("message", { origin: appOrigin, source: frame.contentWindow, data: { type: "sh-turnstile-ready" } });
  assert.strictEqual(JSON.stringify(frame.contentWindow.posted), JSON.stringify([{ msg: { type: "sh-turnstile-start", resource: "guestbook" }, origin: appOrigin }]));
  w.dispatch("message", { origin: appOrigin, source: frame.contentWindow, data: { type: "sh-turnstile-token", token: "GOOD" } });
  const result = await p;
  assert.strictEqual(result.changes, 1);
  assert.strictEqual(calls.length, 2, "the save is sent again once");
  assert.strictEqual(calls[1].opts.headers["X-SH-Turnstile"], "GOOD", "with the token");
  assert.strictEqual(calls[0].opts.headers["X-SH-Turnstile"], undefined, "the first try carries no token");
  assert.strictEqual(frame.parentNode && frame.parentNode.parentNode && frame.parentNode.parentNode.parentNode, null, "the box closes");
  // Cancel rejects with a code and sends nothing.
  answers = [{ status: 403, body: { code: "spam_check_required", error: "x" } }];
  const before = calls.length;
  const q = SH.storage.kv("notes").set("k", "v");
  await tick();
  const buttons = created.filter((e) => e.tagName === "BUTTON");
  buttons[buttons.length - 1].onclick();
  await assert.rejects(q, (e) => e.code === "spam_check_cancelled");
  assert.strictEqual(calls.length, before + 1);
  // Other refusals pass straight through, with no frame.
  answers = [{ status: 403, body: { code: "forbidden", error: "no" } }];
  const frames = created.filter((e) => e.tagName === "IFRAME").length;
  await assert.rejects(SH.storage.sqlite("guestbook").table("entries").add({ a: 1 }), (e) => e.code === "forbidden");
  assert.strictEqual(created.filter((e) => e.tagName === "IFRAME").length, frames);
  console.log("auth.js: ok");
}

(async () => {
  await frameChecks();
  await authChecks();
})().catch((e) => { console.error(e && e.stack || e); process.exit(1); });
