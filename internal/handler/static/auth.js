/*
 * simple-host visitor auth and storage. On a site's own address (its
 * <site>.<handle>.simple-host.app, or the site's own domain): Google or an emailed
 * code, then saves are per-person. The shared address sites.<domain> offers no sign-in:
 * on simple-host.app it is view-only (anonymous saves refused); event and self-hosted
 * instances keep saves open there.
 * SH.email.request(email) sends a code; SH.email.verify(email, code) signs in.
 * SH.mount(target) offers Google plus an inline email/code form.
 *
 *   <section id="sh-auth"></section>
 *   <script src="https://simple-host.app/auth.js" defer></script>
 *   <script>document.addEventListener('DOMContentLoaded', function () {
 *     SH.mount('#sh-auth');
 *   });</script>
 *
 * Saved data has a name and a kind the owner's agent declares once (Page info:
 * only the owner writes it; Submissions: visitors send them). SH.data(name, kind):
 *   SH.data('menu', 'content').get()            -> the Page info document
 *   var rsvps = SH.data('rsvps', 'entries');     (kind is optional; it is checked)
 *   await SH.requireSignIn(); await rsvps.add({name: 'Ann'});
 *   rsvps.mine() / rsvps.update(id, fields) / rsvps.remove(id) / rsvps.undo(id)
 *     -> the visitor's own entries; rsvps.list() and rsvps.count() for the owner,
 *     or anyone when the list is public. Writes carry an Idempotency-Key and are
 *     retried once, with the same key, after a network error.
 *   var me = SH.data('habits', 'personal');       (Personal: the visitor's own record)
 *   await me.get(); await me.set({streak: 1}); await me.set('theme', 'dark');
 *   await me.inc('streak'); await me.patch([{op: 'append', path: 'days', value: '2026-09-27'}]);
 *   await me.clear(); me.history() / me.restore(changeId) -> their own earlier versions
 *   var todo = SH.data('todo', 'board');          (Shared board: everyone edits)
 *   await todo.add({text: 'milk'}); var r = await todo.list();  (every item; r.items[i].version)
 *   await todo.update(id, {done: true}, {version: 3}); await todo.remove(id); todo.undo(id)
 *   var stop = todo.watch(function (items) { ... }, {every: 5000});  (polls; 304 when unchanged)
 * Older pages: await SH.state.patch([{op:"inc",path:"count",by:1}]) or
 * await SH.collection('entries').append(item). Never automatically re-POST those.
 * New resource storage (the owner declares resources and their read/write
 * policies first): SH.storage.kv('settings').get('theme') / .set('theme', 'dark');
 * SH.storage.sqlite('tasks').query('SELECT * FROM tasks WHERE id = ?', [id]);
 * SH.storage.files('gallery').put('cover.webp', file) / .get('cover.webp').
 * These calls use the site's own visitor/unlock cookies. An "anyone" policy
 * works anonymously; callers do not have to sign in unless that resource's
 * policy requires it. Owner-only schema and policy changes are not page APIs.
 * Private lists (owner-only reads; set by the owner): submit the same way while
 * signed in on the site's own address; the owner's admin page there reads them with
 * SH.collection(name).list() and edits with .update(id, fields) / .remove(id).
 * Auto-derives the API from the first host label (<site>.<handle>.<domain>, a
 * claimed <name>.<domain>), <handle>.<domain>/<site>/ or sites.<domain>/<handle>/<site>/.
 * Custom domain: set window.SH_CONFIG = {site:'my-site'} (same-origin API).
 * authBase optionally overrides the apex. me() returns the server response.
 * Theme the status box with --sh-accent, --sh-muted and --sh-radius.
 * Requires browser Promise, fetch and CustomEvent APIs; no build or dependencies.
 */
(function () {
  "use strict";
  var contentHost = location.hostname.indexOf("sites.") === 0;
  var _cfg = window.SH_CONFIG || {};
  var API_BASE, noBackend = false;
  // When the API base has to be asked for (see below), URLs are built on this
  // token and request() swaps in the answer once it is known.
  var BASE_TOKEN = "\u0001sh-api", baseReady = null;
  if (_cfg.site) {
    // Same-origin by default: on a custom domain /v1/ is proxied to the API and
    // the visitor cookie is host-only, so the apex would never see it. A page
    // hosted elsewhere (backend-anywhere) must set base explicitly.
    var base = (_cfg.base || location.origin).replace(/\/+$/, "");
    // On the shared address a site name alone is ambiguous — two accounts can
    // own the same name, and /v1/sites/<site> would resolve to whichever was
    // created first, so one person's page would read and write another's
    // data. When no handle is given and the page is served from the shared
    // host itself, the owner is the first segment of the page's own path.
    var ownPath = location.pathname.match(/^\/([a-z0-9-]{1,39})\//);
    var handle = _cfg.handle ||
      (contentHost && !_cfg.base && ownPath ? ownPath[1] : "");
    if (handle) {
      API_BASE = base + "/v1/u/" + handle + "/sites/" + _cfg.site;
    } else {
      API_BASE = base + "/v1/sites/" + _cfg.site;
    }
  } else {
    var host = location.hostname, path = location.pathname;
    if (location.protocol === "file:" || host === "localhost" || host === "127.0.0.1") {
      console.info("[auth] set window.SH_CONFIG={site:'your-site'} to point at a backend, or deploy this page on simple-host.");
      noBackend = true;
    }
    var m = path.match(/^\/([a-z0-9-]{1,39})\/([a-z0-9-]{1,63})(?:\/|$)/);
    var sub = host.split(".")[0];
    var seg = path.match(/^\/([a-z0-9-]{1,63})(?:\/|$)/);
    var apexHost = authApex().replace(/^https?:\/\//, "").replace(/[:\/].*$/, "");
    var oneLabel = host.slice(-(apexHost.length + 1)) === "." + apexHost &&
      host.split(".").length === apexHost.split(".").length + 1 && sub !== "www";
    if (sub === "sites" && m) {
      API_BASE = location.origin + "/v1/u/" + m[1] + "/sites/" + m[2];
    } else if (oneLabel && seg) {
      // <handle>.<apex>/<site>/...: a person's own address, where the site is
      // the first path segment. A <name>.<apex> a site has claimed serves that
      // site at its root instead. Ask the host once which it is.
      var bySeg = location.origin + "/v1/sites/" + seg[1];
      var byLabel = location.origin + "/v1/sites/" + sub;
      API_BASE = BASE_TOKEN;
      baseReady = fetch(bySeg + "/me", {credentials: "include", cache: "no-store"}).then(
        function (r) { return r.ok ? bySeg : byLabel; },
        function () { return byLabel; });
    } else {
      // <site>.<handle>.<apex> (a site's own host) and every other host:
      // the site is the first label, and /v1/ is same-origin.
      API_BASE = location.origin + "/v1/sites/" + sub;
    }
  }
  function authApex() {
    if (_cfg.authBase) return String(_cfg.authBase).replace(/\/+$/, "");
    var hn = location.hostname;
    if (hn.indexOf("sites.") === 0) return location.protocol + "//" + hn.replace(/^sites\./, "");
    return /(?:^|\.)simple-hack\.app$/.test(hn) ? "https://simple-hack.app" : "https://simple-host.app";
  }
  var APEX = authApex(), providers = null, providerPromise, meCache, mounted = null;
  var API_ORIGIN = baseReady ? location.origin : API_BASE.replace(/^(https?:\/\/[^\/]+).*$/, "$1");
  function unavailable() {
    var e = new Error("no backend configured");
    e.code = "no_backend";
    return Promise.reject(e);
  }
  function request(url, options, withETag, anonymous) {
    if (noBackend) return unavailable();
    if (baseReady && url.indexOf(BASE_TOKEN) === 0) {
      return baseReady.then(function (base) {
        return request(base + url.slice(BASE_TOKEN.length), options, withETag, anonymous);
      });
    }
    options = options || {};
    // The apex answers with "*" CORS, which browsers reject for credentialed
    // requests; only the site's own API calls carry the visitor cookie.
    options.credentials = anonymous ? "omit" : "include";
    return fetch(url, options).then(function (r) {
      return r.text().then(function (text) {
        var body = null;
        try { body = text ? JSON.parse(text) : null; } catch (e) { body = text; }
        if (!r.ok) {
          var err = new Error((body && body.error) || "Request failed");
          err.status = r.status;
          err.code = body && body.code;
          err.body = body;
          if (err.code === "visitor_auth_required" || err.code === "sign_in_required") {
            meCache = null;
            window.dispatchEvent(new CustomEvent("sh:signin-required"));
          }
          throw err;
        }
        return withETag ? {data: body, etag: r.headers.get("ETag")} : body;
      });
    });
  }
  function loadProviders() {
    if (providers) return Promise.resolve(providers);
    if (!providerPromise) {
      providerPromise = request(APEX + "/v1/auth/oauth/providers", {}, false, true).then(function (d) {
        providers = (d && d.providers) || [];
        return providers;
      });
      providerPromise.catch(function () { providerPromise = null; });
    }
    return providerPromise;
  }
  function write(url, method, body, headers) {
    headers = headers || {};
    headers["Content-Type"] = "application/json";
    headers["X-SH-CSRF"] = "1";
    return request(url, {method: method, headers: headers, body: JSON.stringify(body)});
  }
  function storageURL(path) { return API_BASE + "/storage/" + path; }
  function storageSegment(value, label) {
    if (typeof value !== "string" || !value || value === "." || value === ".." || /[\/\x00-\x1f\x7f]/.test(value)) {
      throw new TypeError(label + " must be a nonempty path segment");
    }
    return encodeURIComponent(value);
  }
  function storagePath(value) {
    if (typeof value !== "string" || !value || value.split("/").some(function (s) { return !s || s === "." || s === ".."; })) {
      throw new TypeError("path must be a nonempty relative file path");
    }
    return value.split("/").map(function (part) { return storageSegment(part, "path"); }).join("/");
  }
  function storagePageQuery(prefix, options) {
    var parts = [];
    if (prefix != null && prefix !== "") parts.push("prefix=" + encodeURIComponent(String(prefix)));
    if (options && options.after != null && options.after !== "") parts.push("after=" + encodeURIComponent(String(options.after)));
    if (options && options.limit != null) parts.push("limit=" + encodeURIComponent(String(options.limit)));
    return parts.length ? "?" + parts.join("&") : "";
  }
  function storageBlob(url) {
    if (noBackend) return unavailable();
    if (baseReady && url.indexOf(BASE_TOKEN) === 0) {
      return baseReady.then(function (base) { return storageBlob(base + url.slice(BASE_TOKEN.length)); });
    }
    return fetch(url, {credentials: "include"}).then(function (r) {
      if (r.ok) return r.blob().then(function (blob) {
        return {blob: blob, contentType: r.headers.get("Content-Type") || blob.type || "application/octet-stream"};
      });
      return r.text().then(function (text) {
        var body;
        try { body = JSON.parse(text); } catch (e) { body = null; }
        var err = new Error((body && body.error) || "Request failed");
        err.status = r.status;
        err.code = body && body.code;
        err.body = body;
        if (err.code === "sign_in_required") {
          meCache = null;
          window.dispatchEvent(new CustomEvent("sh:signin-required"));
        }
        throw err;
      });
    });
  }
  function storageRawWrite(url, body, type) {
    if (noBackend) return unavailable();
    if (baseReady && url.indexOf(BASE_TOKEN) === 0) {
      return baseReady.then(function (base) { return storageRawWrite(base + url.slice(BASE_TOKEN.length), body, type); });
    }
    var bytes = body;
    if (!(typeof Blob !== "undefined" && body instanceof Blob) &&
        !(body instanceof ArrayBuffer) && !(ArrayBuffer.isView && ArrayBuffer.isView(body))) {
      return Promise.reject(new TypeError("put(path, file) needs a Blob, File, ArrayBuffer or typed array"));
    }
    return request(url, {
      method: "PUT",
      headers: {"Content-Type": type || (body && body.type) || "application/octet-stream", "X-SH-CSRF": "1"},
      body: bytes
    });
  }
  function storageResolvedURL(url) {
    if (noBackend) return unavailable();
    if (baseReady && url.indexOf(BASE_TOKEN) === 0) {
      return baseReady.then(function (base) { return base + url.slice(BASE_TOKEN.length); });
    }
    return Promise.resolve(url);
  }
  var SH = window.SH = {
    me: function (options) {
      if (noBackend) return unavailable();
      if (!meCache || (options && options.fresh)) {
        meCache = request(API_BASE + "/me", {cache: "no-store"});
        // A failed lookup must not poison later calls: let them retry.
        meCache.catch(function () { meCache = null; });
      }
      return meCache;
    },
    signIn: function (options) {
      if (noBackend) return unavailable();
      options = options || {};
      if (providers && !providers.length) throw new Error("sign-in is not configured on this host");
      // Start on the site's own host (the page's origin, or returnTo's): it
      // ties the sign-in to this browser with a cookie there, then goes on to
      // the provider. Only that browser can finish it.
      var back = options.returnTo || location.href, origin = location.origin;
      try { origin = new URL(back, location.href).origin; } catch (e) {}
      location.href = origin + "/v1/visitor/oauth/" + encodeURIComponent(options.provider || "google") +
        "?return_to=" + encodeURIComponent(back);
    },
    signOut: function () {
      return request(API_ORIGIN + "/v1/visitor/logout", {
        method: "POST", headers: {"Content-Type": "application/json", "X-SH-CSRF": "1"}
      }).then(function () { meCache = null; });
    },
    requireSignIn: function () {

      // Always re-check: a cached answer may be past expiry or signed out elsewhere.
      return SH.me({fresh: true}).then(function (me) {
        if (me.signed_in) return me;
        // Shared host: no sign-in exists and saves are open, so the save
        // proceeds as-is. The same page code works on a custom domain.
        if (me.sign_in_available === false) {
          if (me.domain) {
            // The site saves on its own domain; a save here would 401.
            window.dispatchEvent(new CustomEvent("sh:signin-required"));
            var e = new Error("this site saves on " + me.domain + "; sign in there");
            e.code = "use_custom_domain";
            e.domain = me.domain;
            throw e;
          }
          return me;
        }
        if (mounted) {
          // A sign-in box is on the page: bring it into view instead of leaving
          // the page for Google. Resume the save after inline sign-in.
          window.dispatchEvent(new CustomEvent("sh:signin-required"));
          if (mounted.scrollIntoView) mounted.scrollIntoView({block: "center"});
          return new Promise(function (resolve, reject) {
            function signedIn() {
              window.removeEventListener("sh:signed-in", signedIn);
              SH.me({fresh: true}).then(resolve, reject);
            }
            window.addEventListener("sh:signed-in", signedIn);
          });
        }
        return loadProviders().then(function (names) {
          if (!names.length) {
            var e = new Error("no sign-in method on this page: call SH.mount() to offer email sign-in");
            e.code = "no_sign_in";
            throw e;
          }
          SH.signIn({provider: names[0]});
          return new Promise(function (resolve, reject) {
            function signedIn() {
              window.removeEventListener("sh:signed-in", signedIn);
              SH.me({fresh: true}).then(resolve, reject);
            }
            window.addEventListener("sh:signed-in", signedIn);
          });
        });
      });
    },
    email: {
      request: function (email) {
        return request(API_BASE + "/visitor/auth", {
          method: "POST", headers: {"Content-Type": "application/json"},
          body: JSON.stringify({email: email})
        });
      },
      verify: function (email, code) {
        return write(API_BASE + "/visitor/auth/verify", "POST", {email: email, code: code}).then(function (body) {
          meCache = null;
          // Resumes any save waiting in requireSignIn(), whether the verify
          // came from the mounted form or from page code.
          window.dispatchEvent(new CustomEvent("sh:signed-in", {detail: body}));
          return body;
        });
      }
    },
    state: {
      get: function () { return request(API_BASE + "/state", {}, true); },
      patch: function (ops) {
        // Accept a bare ops array or the wire shape {ops:[...]}.
        var body = Array.isArray(ops) ? {ops: ops} : ops;
        return write(API_BASE + "/state", "PATCH", body);
      },
      put: function (obj, options) {
        var headers = {};
        if (options && options.ifMatch != null) headers["If-Match"] = options.ifMatch;
        return write(API_BASE + "/state", "PUT", obj, headers);
      }
    },
    storage: {
      kv: function (name) {
        var base = storageURL("kv/" + storageSegment(name, "resource name"));
        return {
          keys: function (prefix, options) { return request(base + "/keys" + storagePageQuery(prefix, options)); },
          get: function (key) { return request(base + "/keys/" + storageSegment(key, "key")); },
          set: function (key, value) { return write(base + "/keys/" + storageSegment(key, "key"), "PUT", {value: value}); },
          delete: function (key) { return request(base + "/keys/" + storageSegment(key, "key"), {method: "DELETE", headers: {"X-SH-CSRF": "1"}}); }
        };
      },
      sqlite: function (name) {
        var base = storageURL("sqlite/" + storageSegment(name, "resource name"));
        return {
          query: function (sql, params) { return write(base + "/query", "POST", {sql: sql, params: params || []}); },
          execute: function (sql, params) { return write(base + "/execute", "POST", {sql: sql, params: params || []}); }
        };
      },
      files: function (name) {
        var base = storageURL("files/" + storageSegment(name, "resource name"));
        function objectURL(path) { return base + "/objects/" + storagePath(path); }
        return {
          list: function (prefix, options) { return request(base + "/objects" + storagePageQuery(prefix, options)); },
          get: function (path) { return storageBlob(objectURL(path)); },
          put: function (path, file, options) {
            return storageRawWrite(objectURL(path), file, options && options.contentType);
          },
          delete: function (path) { return request(objectURL(path), {method: "DELETE", headers: {"X-SH-CSRF": "1"}}); },
          // Promise<string>: one-label person hosts may need an initial /me
          // lookup to choose between a claimed site and a path fallback.
          url: function (path) { return storageResolvedURL(objectURL(path)); }
        };
      }
    },
    collection: function (name) {
      var url = API_BASE + "/collections/" + encodeURIComponent(name);
      // A private list (owner-only reads) answers 404 to everyone but its
      // owner signed in on the site's own address; say what that means.
      function explain(e) {
        if (e && e.status === 404 && e.code === "not_found") {
          e.message = "Not found. If this is a private list, only the site owner can read or change it, signed in on the site's own address.";
        }
        throw e;
      }
      function itemURL(id) {
        if (!/^[0-9]+$/.test(String(id))) return null;
        return url + "/items/" + String(id);
      }
      return {
        append: function (item) { return write(url, "POST", item); },
        list: function (query) {
          var params = Object.keys(query || {}).map(function (key) {
            return encodeURIComponent(key) + "=" + encodeURIComponent(query[key]);
          });
          return request(url + (params.length ? "?" + params.join("&") : "")).catch(explain);
        },
        // Private lists only, site owner only: merge fields into one item
        // (null removes a field; _submitted_by/_submitted_at never change)
        // and delete one item. Public lists are append-only.
        update: function (id, fields) {
          var u = itemURL(id);
          if (!u) return Promise.reject(new Error("update(id, fields): id is the item's number from list()"));
          return write(u, "PATCH", fields || {}).catch(explain);
        },
        remove: function (id) {
          var u = itemURL(id);
          if (!u) return Promise.reject(new Error("remove(id): id is the item's number from list()"));
          return request(u, {method: "DELETE", headers: {"X-SH-CSRF": "1"}}).catch(explain);
        }
      };
    },
    data: function (name, kind) {
      var base = API_BASE + "/data/" + encodeURIComponent(name);
      var want = {"page info": "content", content: "content", submissions: "entries", entries: "entries", shared: "shared",
        personal: "mine", mine: "mine", board: "board", "shared board": "board"}[String(kind || "").toLowerCase()] || kind;
      var checked = null;
      // With a kind, the first call checks the name was declared as that kind.
      function check() {
        if (!want) return Promise.resolve();
        if (!checked) {
          checked = request(base + "/kind", {cache: "no-store"}).then(function (k) {
            var e;
            if (k.kind && k.kind !== want) {
              e = new Error('"' + name + '" is ' + k.label + " (kind " + k.kind + "), not " + want);
              e.code = "wrong_kind";
              throw e;
            }
            // A name nobody declared is Shared (public). A page that asked
            // for Page info or Submissions never saves there by mistake.
            if (!k.kind && (want !== "shared" || k.accepts_saves === false)) {
              e = new Error('"' + name + '" is not declared yet' + (k.accepts_saves === false ? "" : " (until then it is Shared: anyone can read it)") +
                ": the site owner declares it as " + want + " first");
              e.code = "declare_first";
              throw e;
            }
          });
          checked.catch(function () { checked = null; });
        }
        return checked;
      }
      function explain(e) {
        if (e && e.status === 404 && e.code === "not_found") {
          e.message = "Not found. Private entries are read only by the site owner; a visitor reads their own with mine().";
        }
        throw e;
      }
      function newKey() {
        if (window.crypto && crypto.randomUUID) return crypto.randomUUID();
        return String(Date.now()) + "-" + Math.random().toString(36).slice(2);
      }
      // One Idempotency-Key per write; a network error (no answer) is retried
      // once with the same key, so the server saves it once.
      function send(url, method, body, extra) {
        var headers = Object.assign({"Idempotency-Key": newKey()}, extra || {});
        function go() { return write(url, method, body, Object.assign({}, headers)); }
        return check().then(function () {
          return go().catch(function (e) {
            if (e && e.status === undefined) return go();
            throw e;
          });
        }).catch(explain);
      }
      function itemURL(id, rest) {
        if (!/^[0-9]+$/.test(String(id))) return null;
        return base + "/items/" + String(id) + (rest || "");
      }
      function query(q) {
        var params = Object.keys(q || {}).map(function (key) {
          return encodeURIComponent(key) + "=" + encodeURIComponent(q[key]);
        });
        return params.length ? "?" + params.join("&") : "";
      }
      function byId(id, rest, method, body) {
        var u = itemURL(id, rest);
        if (!u) return Promise.reject(new Error("id is the entry's number from add(), mine() or list()"));
        if (method === "DELETE") {
          return check().then(function () {
            return request(u, {method: "DELETE", headers: {"X-SH-CSRF": "1"}});
          }).catch(explain);
        }
        return send(u, method, body);
      }
      // A read that sends back the ETag it last got: while nothing changed the
      // server answers 304 and the last answer is used again (cheap polling).
      var cache = {};
      function cachedGet(url) {
        var c = cache[url], headers = {};
        if (c && c.etag) headers["If-None-Match"] = c.etag;
        return request(url, {cache: "no-store", headers: headers}, true).then(function (r) {
          cache[url] = {etag: r.etag, data: r.data};
          return r.data;
        }, function (e) {
          if (e && e.status === 304 && c) return c.data;
          throw e;
        });
      }
      // Every page of a list (a Shared board holds more than one page): reads
      // page after page by `next` until the end, each with its own ETag, and
      // answers {items, next: null, pages} (pages: each page's answer, the same
      // object again while it was unchanged).
      function allPages(q) {
        var items = [], pages = [];
        function page(url, n) {
          return cachedGet(url).then(function (r) {
            pages.push(r);
            items = items.concat((r && r.items) || []);
            if (r && r.next != null && n < 100) {
              return page(base + query(Object.assign({}, q, {limit: 200, before: r.next})), n + 1);
            }
            return {items: items, next: null, pages: pages};
          });
        }
        return page(base + query(Object.assign({}, q, {limit: 200})), 1);
      }
      function patch(ops) { return send(base, "PATCH", {ops: ops}).then(function (r) { return r.data; }); }
      return {
        // Page info: the document (null until the owner saves one). Personal:
        // the visitor's own record (null until they save one).
        get: function () {
          return check().then(function () { return cachedGet(base); }).then(function (r) {
            return r && (r.kind === "content" || r.kind === "mine") ? r.data : r;
          }).catch(explain);
        },
        // Page info, the owner only (signed in on the site). Personal: the
        // visitor's own record, whole (set(obj)) or one field (set(path, value)).
        set: function (obj, value) {
          if (typeof obj === "string") return patch([{op: "set", path: obj, value: value}]);
          return send(base, "PUT", obj).then(function (r) { return r.data; });
        },
        // Personal: change the visitor's record with the /state ops.
        patch: function (ops) { return patch(Array.isArray(ops) ? ops : [ops]); },
        inc: function (path, by) { return patch([{op: "inc", path: path, by: by == null ? 1 : by}]); },
        // Personal: delete the visitor's record (restorable from history()).
        clear: function () {
          return check().then(function () {
            return request(base, {method: "DELETE", headers: {"X-SH-CSRF": "1"}});
          }).catch(explain);
        },
        history: function () { return check().then(function () { return request(base + "/history"); }).catch(explain); },
        restore: function (changeId) {
          if (!/^[0-9]+$/.test(String(changeId))) return Promise.reject(new Error("restore(id): id is a change's number from history()"));
          return send(base + "/history/" + String(changeId) + "/restore", "POST", {}).then(function (r) { return r.data; });
        },
        // Shared board: calls fn(items) now and whenever the board changes
        // (every item, however many pages), checking every options.every ms
        // (at least 2000). Returns stop().
        watch: function (fn, options) {
          var every = Math.max(2000, (options && options.every) || 5000), last = null, timer = null, stopped = false;
          function same(a, b) {
            if (!a || a.length !== b.length) return false;
            for (var i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
            return true;
          }
          function tick() {
            allPages().then(function (r) {
              if (stopped) return;
              if (!same(last, r.pages)) { last = r.pages; fn(r.items, r); }
            }, function () {}).then(function () { if (!stopped) timer = setTimeout(tick, every); });
          }
          check().then(tick, function (e) { if (options && options.onError) options.onError(e); });
          return function () { stopped = true; clearTimeout(timer); };
        },
        // Submissions.
        add: function (item) { return send(base, "POST", item); },
        mine: function (q) {
          var extra = query(q);
          return check().then(function () { return request(base + "?mine=1" + (extra ? "&" + extra.slice(1) : "")); }).catch(explain);
        },
        // A Shared board with no paging asked for answers every item at once.
        list: function (q) {
          return check().then(function () {
            if (want === "board" && !(q && (q.limit || q.before))) return allPages(q);
            return cachedGet(base + query(q));
          }).catch(explain);
        },
        count: function () {
          return check().then(function () { return request(base + "?count=1"); }).then(function (r) { return r.count; }).catch(explain);
        },
        // options.version (a Shared board item's version) refuses the change
        // with 409 version_conflict (e.body.item: the item now) if it moved on.
        update: function (id, fields, options) {
          var u = itemURL(id, "");
          if (!u) return Promise.reject(new Error("id is the entry's number from add(), mine() or list()"));
          var extra = options && options.version != null ? {"If-Match": '"' + String(options.version) + '"'} : null;
          return send(u, "PATCH", fields || {}, extra);
        },
        remove: function (id) { return byId(id, "", "DELETE"); },
        undo: function (id) { return byId(id, "/undo", "POST", {}); }
      };
    },
    mount: function (target) {
      if (noBackend) return unavailable();
      var box = typeof target === "string" ? document.querySelector(target) : target;
      if (!box) return Promise.reject(new Error("SH.mount: target not found"));
      mounted = box;

      function button(label, action) {
        var b = document.createElement("button");
        b.type = "button";
        b.textContent = label;
        b.style.cssText = "font:inherit;cursor:pointer;padding:6px 12px;color:#fff;background:var(--sh-accent,#5b5ef4);border:0;border-radius:var(--sh-radius,6px)";
        b.onclick = action;
        box.appendChild(b);
      }
      function showError(e) { box.textContent = e.message; }
      function render() {
        return SH.me().then(function (me) {
          if (me.sign_in_available === false) {
            box.style.cssText = "font:inherit;color:var(--sh-muted,#666)";
            if (me.domain) {
              // This site saves on its own domain: send the visitor there.
              box.textContent = "This site saves on ";
              var a = document.createElement("a");
              var rest = location.pathname.replace(contentHost ? /^\/[a-z0-9-]+\/[a-z0-9-]+/ : /^\/[a-z0-9-]+/, "");
              a.href = "https://" + me.domain + (rest || "/") + location.search + location.hash;
              a.textContent = me.domain;
              box.appendChild(a);
              box.appendChild(document.createTextNode(". Sign in there to save."));
              return;
            }
            if (me.address) {
              // The old shared address: sign-in lives on the site's own address.
              box.textContent = "Saves at this address are public. To sign in, open ";
              var own = document.createElement("a");
              own.href = me.address;
              own.textContent = me.address.replace(/^https?:\/\//, "").replace(/\/$/, "");
              box.appendChild(own);
              box.appendChild(document.createTextNode("."));
              return;
            }
            box.textContent = "Saves at this address are public.";
            return;
          }
          box.textContent = "";
          box.style.cssText = "display:flex;flex-wrap:wrap;align-items:center;gap:12px;padding:12px;font:inherit;color:var(--sh-muted,#666);border-radius:var(--sh-radius,6px)";
          if (me.signed_in) {
            var label = document.createElement("span");
            label.textContent = me.email ? "Signed in as " + me.email : "Signed in";
            box.appendChild(label);
            button("Sign out", function () { SH.signOut().then(render).catch(showError); });
            return;
          }
          var form = document.createElement("form");
          form.style.cssText = "display:flex;flex-wrap:wrap;align-items:center;gap:8px";
          var email = document.createElement("input");
          email.type = "email";
          email.required = true;
          email.placeholder = "Email";
          email.setAttribute("aria-label", "Email");
          var code = document.createElement("input");
          code.type = "text";
          code.inputMode = "numeric";
          code.pattern = "[0-9]{6}";
          code.maxLength = 6;
          code.required = true;
          code.placeholder = "6-digit code";
          code.setAttribute("aria-label", "6-digit code");
          var style = "font:inherit;padding:6px 8px;color:var(--sh-muted,#666);border:1px solid var(--sh-muted,#666);border-radius:var(--sh-radius,6px)";
          email.style.cssText = code.style.cssText = style;
          var submit = document.createElement("button");
          submit.type = "submit";
          submit.textContent = "Send code";
          submit.style.cssText = "font:inherit;cursor:pointer;padding:6px 12px;color:#fff;background:var(--sh-accent,#5b5ef4);border:0;border-radius:var(--sh-radius,6px)";
          var error = document.createElement("span");
          error.setAttribute("role", "status");
          var address = null;
          var reset = document.createElement("button");
          reset.type = "button";
          reset.textContent = "Use a different email";
          reset.style.cssText = "font:inherit;font-size:small;cursor:pointer";
          reset.onclick = function () {
            address = null;
            email.readOnly = false;
            code.value = "";
            form.removeChild(code);
            form.removeChild(reset);
            submit.textContent = "Send code";
            error.textContent = "";
          };
          form.appendChild(email);
          form.appendChild(submit);
          form.appendChild(error);
          form.onsubmit = function (event) {
            event.preventDefault();
            submit.disabled = true;
            error.textContent = "";
            var pending = address ? SH.email.verify(address, code.value).then(render) :
              SH.email.request(email.value).then(function (body) {
                address = body.email;
                email.readOnly = true;
                form.insertBefore(code, submit);
                form.insertBefore(reset, error);
                submit.textContent = "Verify";
                error.textContent = body.message;
                code.focus();
              });
            pending.catch(function (e) { error.textContent = e.message; }).then(function () {
              submit.disabled = false;
            });
          };
          box.appendChild(form);
          return loadProviders().then(function (names) {
            if (!names.length) return;
            var name = names[0];
            button("Sign in with " + name.charAt(0).toUpperCase() + name.slice(1) + " to save", function () {
              SH.signIn({provider: name});
            });
          }).catch(function (e) { error.textContent = e.message; });
        });
      }
      window.addEventListener("sh:signin-required", function () { render().catch(showError); });
      return render();
    }
  };
  // Preload for synchronous signIn(); mount/requireSignIn await discovery explicitly.
  if (!noBackend && !contentHost) loadProviders().catch(function () {});
  SH.ready = SH.me().catch(function () { return {signed_in: false}; });
}());
