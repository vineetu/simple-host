/*
 * Simple Host page helper: visitor sign-in and saved data, for pages on a
 * site's own address. Load it with a plain script tag in the head:
 *
 *   <script src="https://simple-host.app/auth.js"></script>
 *   <section id="sh-auth"></section>
 *   <script>document.addEventListener('DOMContentLoaded', function () { SH.mount('#sh-auth'); });</script>
 *
 * On your own domain the site is found from the address; on a local file set
 * window.SH_CONFIG = {site: "my-site"} first.
 *
 * SH.me()            -> {signed_in, email, ...} for this site (cached; {fresh: true} to re-ask)
 * SH.signIn()        -> goes to sign-in (Google or an emailed code) and back to this page
 * SH.signOut()       -> ends this site's sign-in
 * SH.requireSignIn() -> resolves once signed in; asks in a small box if not
 * SH.mount(el)       -> a Sign in button, or "Signed in as ... Sign out"
 * SH.ready           -> a promise of SH.me() at load
 * Typed form values survive the trip to sign-in and back.
 *
 * Saved data (the owner makes the namespace and its rules first):
 *   var t = SH.storage.sqlite('shop').table('orders');
 *   t.add({...}) t.list({order: 'id', desc: 1, limit: 50, after, where: {status: 'new'}})
 *   t.get(id) t.edit(id, {...}) t.delete(id)
 *   SH.storage.sqlite('shop').query(sql, params) / .execute(sql, params)   (owner only)
 *   SH.storage.kv('settings').keys(prefix, {after, limit}) .get(key) .set(key, value) .delete(key)
 *   SH.storage.files('gallery').list(prefix) .get(path) .put(path, file) .upload(path, file) .url(path) .delete(path)
 *   SH.storage.toObjects(result) -> rows as objects
 * A refusal rejects with err.code and err.message (the plain fix). A save that
 * needs the spam check runs it on its own, then sends the save again.
 * Events: sh:signin-required, sh:signed-in, sh:signed-out.
 * Theme the buttons with --sh-accent and --sh-radius.
 */
(function () {
  "use strict";
  var cfg = window.SH_CONFIG || {};
  var APP = "__SH_APP__";
  if (APP.indexOf("__") === 0) APP = "https://simple-host.app";
  var host = location.hostname, noBackend = false;
  if (!cfg.site && (location.protocol === "file:" || host === "localhost" || host === "127.0.0.1")) {
    console.info("[SH] set window.SH_CONFIG = {site: 'your-site'}, or publish this page on Simple Host.");
    noBackend = true;
  }
  var SITE = cfg.site || host.split(".")[0].split("--")[0];
  var API = location.origin + "/v1/sites/" + encodeURIComponent(SITE);
  var meCache = null, mounted = null, pendingSignIn = null;

  function unavailable() {
    var e = new Error("no backend configured");
    e.code = "no_backend";
    return Promise.reject(e);
  }
  function refusal(status, body) {
    var e = new Error((body && (body.fix || body.error)) || "Request failed");
    e.status = status;
    e.code = body && body.code;
    e.body = body;
    if (e.status === 401 || e.code === "sign_in" || e.code === "sign_in_required") {
      meCache = null;
      window.dispatchEvent(new CustomEvent("sh:signin-required"));
    }
    return e;
  }
  function request(url, options) {
    if (noBackend) return unavailable();
    options = options || {};
    options.credentials = "include";
    return fetch(url, options).then(function (r) {
      return r.text().then(function (text) {
        var body = null;
        try { body = text ? JSON.parse(text) : null; } catch (e) { body = text; }
        if (!r.ok) throw refusal(r.status, body);
        return body;
      });
    });
  }
  function write(url, method, body, headers) {
    headers = headers || {};
    headers["Content-Type"] = "application/json";
    headers["X-SH-CSRF"] = "1";
    return request(url, {method: method, headers: headers, body: JSON.stringify(body)});
  }
  function remove(url) { return request(url, {method: "DELETE", headers: {"X-SH-CSRF": "1"}}); }

  // The spam check runs in a small frame from the service's own address (a
  // Turnstile key lists only a few host names). Messages are taken only from
  // that frame; the token comes back the same way and goes with one save.
  var APP_ORIGIN = (function () { try { return new URL(APP).origin; } catch (e) { return ""; } })();
  var spamBox = null;
  function spamCheck() {
    if (spamBox) return spamBox;
    spamBox = new Promise(function (resolve, reject) {
      var wrap = document.createElement("div"), panel = document.createElement("div");
      var note = document.createElement("p"), frame = document.createElement("iframe"), cancel = button("Cancel");
      wrap.setAttribute("role", "dialog"); wrap.setAttribute("aria-modal", "true"); wrap.setAttribute("aria-label", "Spam check");
      wrap.style.cssText = SHADE;
      panel.style.cssText = PANEL;
      note.textContent = "One quick check that you are not a bot, then your post is sent.";
      note.style.margin = "0 0 10px";
      frame.title = "Spam check";
      frame.src = APP_ORIGIN + "/spam-check";
      frame.style.cssText = "border:0;width:300px;max-width:100%;height:70px;display:block";
      frame.setAttribute("referrerpolicy", "no-referrer");
      cancel.style.marginTop = "10px";
      panel.appendChild(note); panel.appendChild(frame); panel.appendChild(cancel); wrap.appendChild(panel);
      var timer;
      function done(err, token) {
        window.removeEventListener("message", onMessage);
        clearTimeout(timer);
        wrap.remove();
        spamBox = null;
        if (err) reject(err); else resolve(token);
      }
      function fail(code, message) { var e = new Error(message); e.code = code; done(e); }
      function onMessage(e) {
        if (e.origin !== APP_ORIGIN || e.source !== frame.contentWindow) return;
        var d = e.data || {};
        if (d.type === "sh-turnstile-ready") frame.contentWindow.postMessage({type: "sh-turnstile-start"}, APP_ORIGIN);
        else if (d.type === "sh-turnstile-token" && typeof d.token === "string" && d.token) done(null, d.token);
        else if (d.type === "sh-turnstile-error") fail("spam_check_failed", "The spam check did not pass. Try again.");
      }
      window.addEventListener("message", onMessage);
      cancel.onclick = function () { fail("spam_check_cancelled", "The spam check was cancelled, so nothing was sent."); };
      timer = setTimeout(function () { fail("spam_check_failed", "The spam check took too long. Try again."); }, 120000);
      (document.body || document.documentElement).appendChild(wrap);
    });
    return spamBox;
  }
  // checkedWrite sends a save; when the namespace needs the spam check, it
  // runs the check and sends the save once more with the token.
  function checkedWrite(url, method, body) {
    return write(url, method, body).catch(function (e) {
      if (!e || e.code !== "spam_check_required") throw e;
      return spamCheck().then(function (token) {
        return write(url, method, body, {"X-SH-Turnstile": token});
      });
    });
  }

  function segment(value, label) {
    if (typeof value !== "string" || !value || value === "." || value === ".." || /[\/\x00-\x1f\x7f]/.test(value)) {
      throw new TypeError(label + " must be a nonempty name without /");
    }
    return encodeURIComponent(value);
  }
  function filePath(value) {
    if (typeof value !== "string" || !value || value.split("/").some(function (s) { return !s || s === "." || s === ".."; })) {
      throw new TypeError("path must be a relative file path");
    }
    return value.split("/").map(function (part) { return segment(part, "path"); }).join("/");
  }
  function rowID(id) {
    var n = Number(id);
    if (!Number.isInteger(n) || n < 1) throw new TypeError("row id must be a positive whole number");
    return String(n);
  }
  function pageQuery(prefix, options) {
    var q = new URLSearchParams();
    if (prefix != null && prefix !== "") q.set("prefix", String(prefix));
    if (options && options.after != null && options.after !== "") q.set("after", String(options.after));
    if (options && options.limit != null) q.set("limit", String(options.limit));
    return q.toString() ? "?" + q.toString() : "";
  }

  // What the visitor typed, kept across the trip to sign-in and back: only
  // plain text-like fields (never passwords, card details, codes, or fields
  // marked data-sh-nosave), for 15 minutes, and filled back in only when the
  // visitor returns signed in.
  var TYPED_KEY = "sh:typed:" + location.pathname + location.search;
  var TYPED_TYPES = ["text", "email", "tel", "url", "number", "date", "time", "datetime-local", "month", "week", "checkbox", "radio", "textarea", "select-one"];
  var TYPED_SKIP = /card|cc_?num|cvc|cvv|iban|ssn|passw|pwd|otp|secret|token|account|routing|security|birth|bank|national_?id|(^|[_\-\s#@])(pin|code|key|pass|pw|tax|dob)([_\-\s]|$)/i;
  function typedField(el) {
    var type = (el.type || "").toLowerCase(), key = el.id ? "#" + el.id : (el.name ? "@" + el.name : "");
    if (!key || TYPED_TYPES.indexOf(type) < 0 || el.readOnly || el.disabled) return null;
    if (el.closest && el.closest("[data-sh-signin], [data-sh-nosave]")) return null;
    if (/cc-|one-time-code|password/.test(el.autocomplete || "") || TYPED_SKIP.test(el.id + " " + el.name)) return null;
    return {key: key, type: type};
  }
  function fields() { return Array.prototype.slice.call(document.querySelectorAll("input, textarea, select")); }
  function saveTyped() {
    try {
      var values = {};
      fields().forEach(function (el) {
        var f = typedField(el);
        if (!f) return;
        if (f.type === "radio") { if (el.checked) values["@" + el.name] = "r:" + el.value; return; }
        if (f.type === "checkbox") { values[f.key + ":" + el.value] = el.checked ? "on" : "off"; return; }
        if (el.value) values[f.key] = el.value;
      });
      sessionStorage.setItem(TYPED_KEY, JSON.stringify({at: Date.now(), values: values}));
    } catch (e) {}
  }
  function restoreTyped(me) {
    var saved = null;
    try { saved = JSON.parse(sessionStorage.getItem(TYPED_KEY) || "null"); sessionStorage.removeItem(TYPED_KEY); } catch (e) {}
    if (!me || !me.signed_in || !saved || !saved.values || Date.now() - saved.at > 15 * 60 * 1000) return;
    fields().forEach(function (el) {
      var f = typedField(el), v;
      if (!f) return;
      if (f.type === "radio") { if (saved.values["@" + el.name] !== "r:" + el.value) return; el.checked = true; }
      else if (f.type === "checkbox") { v = saved.values[f.key + ":" + el.value]; if (!v) return; el.checked = v === "on"; }
      else { v = saved.values[f.key]; if (typeof v !== "string" || el.value) return; el.value = v; }
      el.dispatchEvent(new Event("input", {bubbles: true}));
      el.dispatchEvent(new Event("change", {bubbles: true}));
    });
  }

  // The few things this helper draws: a button, a signed-in chip, a box.
  // The sign-in box takes the owner app's tokens (web/app/src/styles/tokens.css:
  // card #fffdf5, ink #103f49, ink-soft #45646a), written out: a site loads no platform CSS.
  var FONT = "font:16px/1.45 system-ui,-apple-system,'Segoe UI',sans-serif;";
  var SHADE = "position:fixed;inset:0;z-index:2147483646;background:rgba(0,0,0,.45);display:flex;align-items:center;justify-content:center;padding:16px;box-sizing:border-box";
  var PANEL = FONT + "position:relative;box-sizing:border-box;background:#fffdf5;color:#103f49;border:1.5px solid #45646a;border-radius:var(--sh-radius,14px);padding:24px 22px 18px;max-width:380px;width:100%;box-shadow:0 10px 40px rgba(0,0,0,.25)";
  function button(label, primary) {
    var b = document.createElement("button");
    b.type = "button";
    b.textContent = label;
    b.style.cssText = FONT + "cursor:pointer;min-height:44px;padding:8px 16px;border-radius:var(--sh-radius,8px);" +
      (primary ? "font-weight:600;color:#fff;background:var(--sh-accent,#5b5ef4);border:0" : "color:inherit;background:transparent;border:1px solid #c9c9d1");
    return b;
  }
  function text(tag, words, css) {
    var el = document.createElement(tag);
    el.textContent = words;
    el.style.cssText = css;
    return el;
  }
  var chip = null, CHIP_KEY = "sh:chip:" + location.host;
  function showChip(me) {
    if (mounted || !me || !me.signed_in || !document.body) return;
    if (!chip) {
      chip = document.createElement("div");
      chip.setAttribute("data-sh-signin", "");
      chip.style.cssText = FONT + "font-size:14px;position:fixed;right:max(12px,env(safe-area-inset-right));bottom:max(12px,env(safe-area-inset-bottom));z-index:2147483000;max-width:calc(100vw - 24px);box-sizing:border-box;display:flex;gap:10px;align-items:center;padding:6px 6px 6px 14px;background:#fff;color:#1a1a1a;border:1px solid rgba(128,128,128,.4);border-radius:999px;box-shadow:0 2px 10px rgba(0,0,0,.15)";
      document.body.appendChild(chip);
    }
    chip.textContent = "";
    var who = document.createElement("span");
    who.textContent = me.email ? "Signed in as " + me.email : "Signed in";
    var out = button("Sign out");
    out.onclick = function () { SH.signOut().catch(function (e) { who.textContent = e.message; }); };
    chip.appendChild(who);
    chip.appendChild(out);
  }
  function signInBox() {
    return new Promise(function (resolve, reject) {
      var wrap = document.createElement("div"), panel = document.createElement("div");
      wrap.setAttribute("data-sh-signin", "");
      wrap.style.cssText = SHADE;
      panel.setAttribute("role", "dialog"); panel.setAttribute("aria-modal", "true"); panel.setAttribute("aria-label", "Sign in");
      panel.style.cssText = PANEL;
      var title = String(document.title || location.host).replace(/\s+/g, " ").trim().slice(0, 60);
      var go = button("Sign in", true), no = button("\u00d7");
      go.style.cssText += ";display:block;width:100%;margin-top:4px";
      no.setAttribute("aria-label", "Close");
      no.style.cssText = "position:absolute;top:4px;right:4px;width:44px;height:44px;border:0;background:transparent;color:#45646a;font:24px/1 system-ui,sans-serif;cursor:pointer";
      panel.appendChild(no);
      panel.appendChild(text("p", title, "font-size:13px;color:#45646a;margin:0 40px 4px 0;overflow-wrap:anywhere"));
      panel.appendChild(text("h2", "Sign in to continue", "font:650 21px/1.25 system-ui,-apple-system,'Segoe UI',sans-serif;margin:0 0 6px;color:#103f49"));
      panel.appendChild(text("p", "No password and no account to make. The site owner sees the email you sign in with.", "margin:0 0 16px;font-size:15px;color:#45646a"));
      panel.appendChild(go);
      panel.appendChild(text("p", "Use Google or a 6-digit code we email you. Sign-in by Simple Host.", "margin:14px 0 0;font-size:13px;color:#45646a"));
      wrap.appendChild(panel);
      function cancel() {
        wrap.remove();
        document.removeEventListener("keydown", onKey, true);
        var e = new Error("sign-in cancelled");
        e.code = "cancelled";
        reject(e);
      }
      function onKey(e) { if (e.key === "Escape") cancel(); }
      go.onclick = function () { SH.signIn(); };
      no.onclick = cancel;
      wrap.addEventListener("click", function (e) { if (e.target === wrap) cancel(); });
      document.addEventListener("keydown", onKey, true);
      (document.body || document.documentElement).appendChild(wrap);
      go.focus();
      window.addEventListener("sh:signed-in", function () { wrap.remove(); SH.me().then(resolve, reject); }, {once: true});
    });
  }

  var SH = window.SH = {
    me: function (options) {
      if (noBackend) return unavailable();
      if (!meCache || (options && options.fresh)) {
        meCache = request(location.origin + "/_sh/me", {cache: "no-store"});
        meCache.catch(function () { meCache = null; });
      }
      return meCache;
    },
    signIn: function (options) {
      if (noBackend) return unavailable();
      options = options || {};
      // Where to come back to: a path on this site (the page itself by default).
      var back = location.pathname + location.search + location.hash;
      try {
        var u = new URL(options.returnTo || location.href, location.href);
        if (u.origin === location.origin) back = u.pathname + u.search + u.hash;
      } catch (e) {}
      try { localStorage.setItem(CHIP_KEY, "1"); } catch (e) {}
      saveTyped();
      // A browser leaves \ (and spaces) as they are in an address; send them encoded.
      back = back.replace(/[\\\s]/g, function (c) { return encodeURIComponent(c); });
      location.href = location.origin + "/_sh/signin?return=" + encodeURIComponent(back);
      return new Promise(function () {});
    },
    signOut: function () {
      return write(location.origin + "/_sh/signout", "POST", {}).then(function () {
        meCache = null;
        try { localStorage.removeItem(CHIP_KEY); } catch (e) {}
        if (chip) { chip.remove(); chip = null; }
        window.dispatchEvent(new CustomEvent("sh:signed-out"));
      });
    },
    requireSignIn: function () {
      if (pendingSignIn) return pendingSignIn;
      pendingSignIn = SH.me({fresh: true}).then(function (me) {
        if (me.signed_in) return me;
        window.dispatchEvent(new CustomEvent("sh:signin-required"));
        if (mounted) {
          if (mounted.scrollIntoView) mounted.scrollIntoView({block: "center"});
          return new Promise(function (resolve) {
            window.addEventListener("sh:signed-in", function () { SH.me({fresh: true}).then(resolve); }, {once: true});
          });
        }
        return signInBox();
      }).then(function (me) { pendingSignIn = null; return me; }, function (e) { pendingSignIn = null; throw e; });
      return pendingSignIn;
    },
    // Sign-in codes are entered on the sign-in page now: request() goes
    // there; verify() is not needed any more.
    email: {
      request: function () { return SH.signIn(); },
      verify: function () {
        var e = new Error("Enter the code on the sign-in page. Call SH.signIn() to go there.");
        e.code = "use_sign_in";
        return Promise.reject(e);
      }
    },
    storage: {
      kv: function (ns) {
        var base = API + "/storage/kv/" + segment(ns, "namespace");
        return {
          keys: function (prefix, options) { return request(base + "/keys" + pageQuery(prefix, options)); },
          get: function (key) { return request(base + "/keys/" + segment(key, "key")); },
          set: function (key, value) { return checkedWrite(base + "/keys/" + segment(key, "key"), "PUT", {value: value}); },
          delete: function (key) { return remove(base + "/keys/" + segment(key, "key")); }
        };
      },
      sqlite: function (ns) {
        var base = API + "/storage/sqlite/" + segment(ns, "namespace");
        return {
          table: function (table) {
            var rows = base + "/tables/" + segment(table, "table name") + "/rows";
            return {
              add: function (values) { return checkedWrite(rows, "POST", values); },
              // where: {column: value}, equality only. order "-id" or "id desc" means newest first.
              list: function (options) {
                options = Object.assign({}, options || {});
                if (typeof options.order === "string") {
                  var o = options.order.trim(), m = o.match(/^(\w+)\s+(asc|desc)$/i);
                  if (o.charAt(0) === "-") { options.order = o.slice(1); options.desc = 1; }
                  else if (m) { options.order = m[1]; options.desc = m[2].toLowerCase() === "desc" ? 1 : 0; }
                }
                if (typeof options.desc === "boolean") options.desc = options.desc ? 1 : 0;
                var q = new URLSearchParams();
                ["order", "desc", "limit", "after"].forEach(function (k) { if (options[k] != null) q.set(k, String(options[k])); });
                if (options.where) Object.keys(options.where).forEach(function (c) { q.set("where." + c, String(options.where[c])); });
                // The answer {columns, rows, next_after, mine} plus items (the rows as
                // objects). It also reads like that array (result[0], .length, .map ...).
                return request(rows + (q.toString() ? "?" + q.toString() : "")).then(function (r) {
                  if (!r || !Array.isArray(r.rows)) return r;
                  var items = SH.storage.toObjects(r);
                  r.items = items;
                  function hidden(k, v) { if (!Object.prototype.hasOwnProperty.call(r, k)) Object.defineProperty(r, k, {value: v, configurable: true}); }
                  hidden("length", items.length);
                  items.forEach(function (o, i) { hidden(String(i), o); });
                  ["forEach", "map", "filter", "find", "findIndex", "some", "every", "reduce", "slice", "indexOf", "includes", "sort", "reverse", "concat", "join"].forEach(function (m) {
                    hidden(m, function () { return items[m].apply(items, arguments); });
                  });
                  hidden(Symbol.iterator, function () { return items[Symbol.iterator](); });
                  return r;
                });
              },
              get: function (id) { return request(rows + "/" + rowID(id)); },
              edit: function (id, values) { return write(rows + "/" + rowID(id), "PATCH", values); },
              delete: function (id) { return remove(rows + "/" + rowID(id)); }
            };
          },
          // The owner's own SQL. Pages for visitors use table().
          query: function (sql, params) { return write(base + "/query", "POST", {sql: sql, params: params || []}); },
          execute: function (sql, params) { return write(base + "/execute", "POST", {sql: sql, params: params || []}); }
        };
      },
      // Rows as objects: {column: value, ..., mine} (mine where the answer says whose rows are whose).
      toObjects: function (result) {
        if (Array.isArray(result) && !(result.columns && result.rows)) return result.slice();
        var cols = (result && result.columns) || [];
        var list = (result && result.rows) || (result && result.row ? [result.row] : []);
        var mine = result && (Array.isArray(result.mine) ? result.mine : (result.mine != null ? [result.mine] : null));
        return list.map(function (r, i) {
          var o = {};
          cols.forEach(function (c, j) { if (c !== "__proto__") o[c] = r[j]; });
          if (mine && !Object.prototype.hasOwnProperty.call(o, "mine")) o.mine = !!mine[i];
          return o;
        });
      },
      files: function (ns) {
        var base = API + "/storage/files/" + segment(ns, "namespace");
        function objectURL(path) { return base + "/objects/" + filePath(path); }
        function put(path, file, options) {
          if (noBackend) return unavailable();
          if (!(file instanceof Blob) && !(file instanceof ArrayBuffer) && !ArrayBuffer.isView(file)) {
            return Promise.reject(new TypeError("put(path, file) needs a Blob, File, ArrayBuffer or typed array"));
          }
          var type = (options && options.contentType) || file.type || "application/octet-stream";
          var url = objectURL(path);
          var send = function (headers) {
            headers["Content-Type"] = type;
            headers["X-SH-CSRF"] = "1";
            return request(url, {method: "PUT", headers: headers, body: file});
          };
          return send({}).catch(function (e) {
            if (!e || e.code !== "spam_check_required") throw e;
            return spamCheck().then(function (token) { return send({"X-SH-Turnstile": token}); });
          });
        }
        return {
          list: function (prefix, options) { return request(base + "/objects" + pageQuery(prefix, options)); },
          get: function (path) {
            if (noBackend) return unavailable();
            return fetch(objectURL(path), {credentials: "include"}).then(function (r) {
              if (r.ok) return r.blob().then(function (blob) { return {blob: blob, contentType: r.headers.get("Content-Type") || blob.type}; });
              return r.json().catch(function () { return null; }).then(function (body) { throw refusal(r.status, body); });
            });
          },
          put: put,
          upload: put,
          delete: function (path) { return remove(objectURL(path)); },
          url: function (path) { return noBackend ? unavailable() : Promise.resolve(objectURL(path)); }
        };
      }
    },
    mount: function (target) {
      if (noBackend) return unavailable();
      var box = typeof target === "string" ? document.querySelector(target) : target;
      if (!box) return Promise.reject(new Error("SH.mount: target not found"));
      mounted = box;
      box.setAttribute("data-sh-signin", "");
      function render() {
        return SH.me().then(function (me) {
          box.textContent = "";
          box.style.cssText = "";
          if (me.signed_in) {
            box.style.cssText = FONT + "font-size:15px;display:flex;flex-wrap:wrap;align-items:center;gap:12px;padding:12px 14px;color:#1a1a1a;background:#fff;border-radius:var(--sh-radius,14px)";
            var who = document.createElement("span");
            who.textContent = me.email ? "Signed in as " + me.email : "Signed in";
            var out = button("Sign out", true);
            out.onclick = function () { SH.signOut().then(render).catch(function (e) { who.textContent = e.message; }); };
            box.appendChild(who);
            box.appendChild(out);
            return;
          }
          var b = button("Sign in", true);
          b.onclick = function () { SH.signIn(); };
          box.appendChild(b);
        }).catch(function (e) { box.textContent = e.message; });
      }
      window.addEventListener("sh:signed-out", render);
      return render();
    }
  };

  // Temporary, goes with next/compat: the old helper's SH.push (browser
  // notifications) is gone; this stand-in keeps old pages from throwing.
  function pushGone() {
    console.info("[SH] SH.push is not available any more; nothing was changed.");
    return Promise.resolve({available: false, code: "not_available"});
  }
  SH.push = {subscribe: pushGone, unsubscribe: pushGone};

  SH.ready = SH.me().catch(function () { return {signed_in: false}; });
  if (!noBackend) {
    var onReady = function () {
      var flagged = false;
      try { flagged = localStorage.getItem(CHIP_KEY) === "1"; } catch (e) {}
      SH.ready.then(function (me) {
        restoreTyped(me);
        if (me.signed_in && flagged) {
          showChip(me);
          window.dispatchEvent(new CustomEvent("sh:signed-in", {detail: me}));
        }
      });
    };
    if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", function () { setTimeout(onReady, 0); });
    else setTimeout(onReady, 0);
  }
}());
