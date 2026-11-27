// Runs the page helper (auth.js) in a bare page stand-in, once per answer:
// each fetch the call makes gets that recorded answer, and the result is the
// shape of what the helper's promise resolves to, or what it rejects with.
//   node helper-run.js <auth.js> <answers.json>   (answers: [{id, helper, status, headers, body}])
//   node helper-run.js <auth.js> --surface        (the names pages can call)
"use strict";
const fs = require("fs");
const vm = require("vm");
const [file, answersFile] = process.argv.slice(2);
const code = fs.readFileSync(file, "utf8");
const answers = answersFile === "--surface" ? [] : JSON.parse(fs.readFileSync(answersFile, "utf8"));

function shape(v) {
  if (Array.isArray(v)) {
    const kinds = new Map();
    v.forEach((e) => { const s = shape(e); kinds.set(JSON.stringify(s), s); });
    return [...kinds.keys()].sort().map((k) => kinds.get(k));
  }
  if (v === null || v === undefined) return v === null ? "null" : "undefined";
  if (typeof v === "object") {
    if (typeof Blob !== "undefined" && v instanceof Blob) return "Blob";
    const o = {};
    Object.keys(v).sort().forEach((k) => { o[k] = shape(v[k]); });
    return o;
  }
  return typeof v;
}

function response(a) {
  const text = a.body === "<text>" ? "<p>page</p>" : a.body == null ? "" : JSON.stringify(a.body);
  const headers = a.headers || {};
  return {
    ok: a.status >= 200 && a.status < 300, status: a.status,
    headers: { get: (n) => headers[Object.keys(headers).find((k) => k.toLowerCase() === n.toLowerCase())] || null },
    text: () => Promise.resolve(text),
    json: () => Promise.resolve().then(() => JSON.parse(text)),
    blob: () => Promise.resolve(new Blob([text], { type: headers["Content-Type"] || "" })),
  };
}

function page(answer) {
  let live = false;
  const store = () => { const m = {}; return { getItem: (k) => (k in m ? m[k] : null), setItem: (k, v) => { m[k] = String(v); }, removeItem: (k) => { delete m[k]; } }; };
  const location = { protocol: "https:", hostname: "shop.olive.simple-host.app", host: "shop.olive.simple-host.app",
    origin: "https://shop.olive.simple-host.app", pathname: "/", search: "", hash: "", href: "https://shop.olive.simple-host.app/", assign() {} };
  const el = () => ({ style: {}, setAttribute() {}, appendChild() {}, addEventListener() {}, remove() {}, querySelector: () => null, focus() {} });
  const document = { readyState: "complete", title: "", body: el(), documentElement: el(), activeElement: null,
    addEventListener() {}, removeEventListener() {}, querySelector: () => null, querySelectorAll: () => [], createElement: el, createTextNode: el, contains: () => false };
  const window = { location, document, localStorage: store(), sessionStorage: store(), addEventListener() {}, removeEventListener() {}, dispatchEvent() {} };
  window.top = window; window.self = window; window.window = window;
  window.fetch = (url) => {
    if (!live) {
      // While the helper loads (its own SH.me, the old helper's sign-in options).
      return Promise.resolve(response({ status: 200, body: /providers/.test(url) ? { providers: [], sso_enabled: true } : { signed_in: false } }));
    }
    return Promise.resolve(response(answer));
  };
  Object.assign(window, { console: { info() {}, log() {}, warn() {} }, CustomEvent: class { constructor(t, o) { this.type = t; this.detail = o && o.detail; } },
    Event: class { constructor(t) { this.type = t; } }, URL, URLSearchParams, Promise, Blob, ArrayBuffer, Symbol, JSON, Object, Array, Error, TypeError,
    setTimeout, clearTimeout, encodeURIComponent, atob, btoa, navigator: {} });
  const ctx = vm.createContext(window);
  vm.runInContext(code, ctx);
  return { SH: ctx.SH, go: () => { live = true; } };
}

function call(SH, helper) {
  const [what, op] = helper.split(".");
  const kv = () => SH.storage.kv("ns"), table = () => SH.storage.sqlite("ns").table("t"), files = () => SH.storage.files("ns");
  switch (what) {
    case "me": return SH.me({ fresh: true });
    case "kv": return { get: () => kv().get("k"), set: () => kv().set("k", 1), keys: () => kv().keys(), delete: () => kv().delete("k") }[op]();
    case "table": return { add: () => table().add({ a: 1 }), list: () => table().list({}), get: () => table().get(1), edit: () => table().edit(1, { a: 1 }), delete: () => table().delete(1) }[op]();
    case "files": return { list: () => files().list(), get: () => files().get("a.txt"), put: () => files().put("a.txt", new Blob(["x"])), delete: () => files().delete("a.txt") }[op]();
    case "sql": return SH.storage.sqlite("ns")[op]("SELECT 1", []);
  }
  throw new Error("unknown helper call " + helper);
}

// --surface: every name a page can call on SH (and on the objects its
// storage calls return), and what the stand-ins for dropped calls resolve to.
async function surface() {
  const p = page({ status: 200, body: { signed_in: false } });
  await new Promise((r) => setTimeout(r, 0));
  p.go();
  const names = {};
  const walk = (o, at, depth) => {
    Object.keys(o).sort().forEach((k) => {
      const v = o[k], name = at + "." + k;
      names[name] = typeof v;
      if (v && typeof v === "object" && !(v instanceof Promise) && depth < 3) walk(v, name, depth + 1);
    });
  };
  walk(p.SH, "SH", 0);
  walk(p.SH.storage.kv("ns"), "SH.storage.kv()", 1);
  walk(p.SH.storage.sqlite("ns"), "SH.storage.sqlite()", 1);
  walk(p.SH.storage.sqlite("ns").table("t"), "SH.storage.sqlite().table()", 1);
  walk(p.SH.storage.files("ns"), "SH.storage.files()", 1);
  const standins = {};
  for (const [name, f] of [["SH.storage.people()", () => p.SH.storage.people()], ["SH.push.subscribe()", () => p.SH.push.subscribe()],
    ["SH.push.unsubscribe()", () => p.SH.push.unsubscribe()], ["SH.push.mute()", () => p.SH.push.mute([])]]) {
    try { standins[name] = { resolves: shape(await f()) }; } catch (e) { standins[name] = { rejects: e && e.code }; }
  }
  return { names, standins };
}

(async () => {
  if (answersFile === "--surface") {
    process.stdout.write(JSON.stringify(await surface()));
    return;
  }
  const out = [];
  for (const a of answers) {
    const p = page(a);
    await new Promise((r) => setTimeout(r, 0));
    p.go();
    let result;
    try {
      const v = await call(p.SH, a.helper);
      result = { resolves: shape(v) };
    } catch (e) {
      result = { rejects: { code: e && e.code === undefined ? "undefined" : e && e.code, status: e && e.status, message: typeof (e && e.message) } };
    }
    out.push({ id: a.id, result });
  }
  process.stdout.write(JSON.stringify(out));
})();
