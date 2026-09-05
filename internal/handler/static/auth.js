/*
 * simple-host visitor auth and storage — a drop-in hosted helper.
 *
 *   <section id="sh-auth"></section>
 *   <script src="https://simple-host.app/auth.js" defer></script>
 *   <script>document.addEventListener('DOMContentLoaded', function () {
 *     SH.mount('#sh-auth');
 *   });</script>
 *
 * Before saving: await SH.requireSignIn(); then await SH.state.patch([{op:"inc",path:"count",by:1}])
 * or await SH.collection('entries').append(item). Never automatically re-POST.
 * Auto-derives the API from sites.<domain>/<handle>/<site>/ or the first host label.
 * Custom domain: set window.SH_CONFIG = {site:'my-site'} (same-origin API).
 * Backend anywhere (page hosted elsewhere): {site, handle, base:'https://simple-host.app'}
 * can READ only; no visitor session can be established there, so writes 401.
 * The owner must allow the page's origin. authBase optionally overrides the apex.
 * me() reports email/provider only on a custom domain; on the shared content
 * host the page learns that the visitor is signed in, not who they are.
 * Theme the status box with --sh-accent, --sh-muted and --sh-radius.
 * Requires browser Promise, fetch and CustomEvent APIs; no build or dependencies.
 */
(function () {
  "use strict";
  var _cfg = window.SH_CONFIG || {};
  var API_BASE, noBackend = false;
  if (_cfg.site) {
    // Same-origin by default: on a custom domain /v1/ is proxied to the API and
    // the visitor cookie is host-only, so the apex would never see it. A page
    // hosted elsewhere (backend-anywhere) must set base explicitly.
    var base = (_cfg.base || location.origin).replace(/\/+$/, "");
    if (_cfg.handle) {
      API_BASE = base + "/v1/u/" + _cfg.handle + "/sites/" + _cfg.site;
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
    if (host.split(".")[0] === "sites" && m) {
      API_BASE = location.origin + "/v1/u/" + m[1] + "/sites/" + m[2];
    } else {
      var sub = host.split(".")[0];
      API_BASE = location.origin + "/v1/sites/" + sub;
    }
  }
  function authApex() {
    if (_cfg.authBase) return String(_cfg.authBase).replace(/\/+$/, "");
    var hn = location.hostname;
    if (hn.indexOf("sites.") === 0) return location.protocol + "//" + hn.replace(/^sites\./, "");
    return "https://simple-host.app";
  }
  var APEX = authApex(), providers = null, providerPromise, meCache;
  var API_ORIGIN = API_BASE.replace(/^(https?:\/\/[^\/]+).*$/, "$1");
  function unavailable() {
    var e = new Error("no backend configured");
    e.code = "no_backend";
    return Promise.reject(e);
  }
  function request(url, options, withETag, anonymous) {
    if (noBackend) return unavailable();
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
          if (err.code === "visitor_auth_required") {
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
      location.href = APEX + "/v1/auth/oauth/" + encodeURIComponent(options.provider || "google") +
        "?return_to=" + encodeURIComponent(options.returnTo || location.href);
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
        return loadProviders().then(function () {
          SH.signIn();
          return new Promise(function () {});
        });
      });
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
    collection: function (name) {
      var url = API_BASE + "/collections/" + encodeURIComponent(name);
      return {
        append: function (item) { return write(url, "POST", item); },
        list: function (query) {
          var params = Object.keys(query || {}).map(function (key) {
            return encodeURIComponent(key) + "=" + encodeURIComponent(query[key]);
          });
          return request(url + (params.length ? "?" + params.join("&") : ""));
        }
      };
    },
    mount: function (target) {
      if (noBackend) return unavailable();
      var box = typeof target === "string" ? document.querySelector(target) : target;
      if (!box) return Promise.reject(new Error("SH.mount: target not found"));
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
          box.textContent = "";
          box.style.cssText = "display:flex;align-items:center;gap:12px;padding:12px;font:inherit;color:var(--sh-muted,#666);border-radius:var(--sh-radius,6px)";
          if (me.signed_in) {
            var label = document.createElement("span");
            label.textContent = me.email ? "Signed in as " + me.email : "Signed in";
            box.appendChild(label);
            button("Sign out", function () { SH.signOut().then(render).catch(showError); });
            return;
          }
          return loadProviders().then(function (names) {
            if (!names.length) {
              box.textContent = "sign-in is not configured on this host";
              return;
            }
            var name = names[0];
            button("Sign in with " + name.charAt(0).toUpperCase() + name.slice(1) + " to save", function () {
              SH.signIn({provider: name});
            });
          });
        });
      }
      window.addEventListener("sh:signin-required", function () { render().catch(showError); });
      return render();
    }
  };
  // Preload for synchronous signIn(); mount/requireSignIn await discovery explicitly.
  if (!noBackend) loadProviders().catch(function () {});
  SH.ready = SH.me().catch(function () { return {signed_in: false}; });
}());
