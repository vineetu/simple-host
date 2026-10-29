/*
 * simple-host visitor sign-in and storage, for pages on a site's own address
 * (its <site>.<handle>.simple-host.app, or the site's own domain).
 *
 * Site Sign in navigates through the app's apex, then receives a cookie
 * for this one site. The apex keeps its account key and SSO cookie there;
 * neither goes to the site. Never put an API key in a hosted page.
 * Instances without an apex retain the emailed-code and provider card.
 *
 *   <script>window.SH_CONFIG = { site: "my-site" };</script>  (needed on a custom domain)
 *   <script src="https://simple-host.app/auth.js"></script>
 *   <section id="sh-auth"></section>
 *   <script>document.addEventListener('DOMContentLoaded', function () {
 *     SH.mount('#sh-auth');            // the Sign in button
 *   });</script>
 *
 * Load this file with a plain script tag in the head (no defer), so SH exists
 * for every script after it.
 * SH.requireSignIn() -> resolves once the visitor is signed in (scrolls to the box
 *   and waits; with no box on the page, it asks in a dialog: the shared sign-in
 *   button); put it right before any save that needs a visitor. Typed form values
 *   survive the trip to the apex and back.
 * SH.me() -> {signed_in, email, ...}; SH.signOut(); SH.signIn() starts the hop.
 * SH.email.request(email) / .verify(email, code) remain for cached pages.
 *
 * STORAGE (the owner creates each resource and picks its preset first: public,
 * inbox, wall, comments, records, personal, board or private; SQLite per table):
 *   var orders = SH.storage.sqlite('shop').table('orders');
 *   await SH.requireSignIn(); var r = await orders.add({items: '...', total_cents: 700});
 *   var page = await orders.list({order: 'id', desc: 1, limit: 50, where: {status: 'received'}});
 *     // {columns, rows, items, next_after, mine}: items are the rows as objects,
 *     // and the result also reads like that array (page[0], page.length, page.forEach)
 *   await orders.get(12); await orders.edit(12, {status: 'shipped'}); await orders.delete(12);
 *   The server decides who may read, add, edit and delete from the preset;
 *   "own" means the visitor's own rows only. visitor_id, created_at and
 *   updated_at are stamped by the server. Visitors never send SQL.
 *   The site's owner, signed in on the site's own address, gets owner rights
 *   for saved data (an admin page); SH.me() then returns site_owner: true.
 *   SH.storage.kv('settings').get('theme') / .set('theme', 'dark') / .keys() / .delete(key)
 *   SH.storage.files('gallery').put('cover.webp', file) / .get(path) / .url(path) / .list() / .delete(path)
 *   An "anyone" action works without sign-in; a signed-in action without a session
 *   rejects with code sign_in_required and fires the 'sh:signin-required' event.
 *   On a comments resource (add "checked": anyone posts without signing in once a
 *   spam check passes), add() and set() run the check on their own: a small box
 *   shows Cloudflare Turnstile in a frame from the app's address, and the save is
 *   sent again with its one-time token. A refused check rejects with code
 *   spam_check_failed or spam_check_cancelled.
 *   query()/execute() are the owner's, or legacy full-mode databases saved before presets.
 *
 * The shared address sites.<domain> offers no sign-in: on simple-host.app it is
 * view-only; event and self-hosted instances keep saves open there.
 *
 * DEPRECATED, EXISTING SITES ONLY: SH.state (the shared document), SH.collection(name)
 * (lists; private lists submit signed in, the owner reads them) and SH.data(name, kind)
 * (declared kinds: content, entries, personal, board) keep working for sites that
 * already use them. Do not use them for anything new. Their writes carry an
 * Idempotency-Key and are retried once after a network error; never re-POST by hand.
 *
 * Auto-derives the API from the first host label (<site>.<handle>.<domain>, a
 * claimed <name>.<domain>), <handle>.<domain>/<site>/ or sites.<domain>/<handle>/<site>/.
 * Custom domain: set window.SH_CONFIG = {site:'my-site'} (same-origin API).
 * authBase optionally overrides the apex. me() returns the server response.
 * Theme the status box with --sh-accent, --sh-muted and --sh-radius.
 * Requires browser Promise, fetch and CustomEvent APIs; no build or dependencies.
 */
(function () {
  "use strict";
  /*sh:signin-card*/
  var contentHost = location.hostname.indexOf("sites.") === 0;
  var _cfg = window.SH_CONFIG || {};
  var API_BASE, noBackend = false;
  // True when this origin serves one site only (its own host or domain), so
  // what this file keeps in the browser's storage belongs to this site alone.
  var ownOrigin = false;
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
      ownOrigin = !contentHost && !_cfg.base;
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
      ownOrigin = !contentHost;
    }
  }
  function authApex() {
    if (_cfg.authBase) return String(_cfg.authBase).replace(/\/+$/, "");
    var hn = location.hostname;
    if (hn.indexOf("sites.") === 0) return location.protocol + "//" + hn.replace(/^sites\./, "");
    return /(?:^|\.)simple-hack\.app$/.test(hn) ? "https://simple-hack.app" : "https://simple-host.app";
  }
  var APEX = authApex(), providers = null, providerPromise, meCache, mounted = null;
  // emailEnabled: whether this server sends sign-in codes (from the providers
  // list; true until known). pendingSignIn: one shared wait, so a visitor who
  // clicks a save button twice before signing in triggers the save once.
  // Only a successful discovery response may select the legacy inline card.
  var hopEnabled = true;
  var emailEnabled = true, pendingSignIn = null, oidcLabel = "your company account";
  // Visitor wording. Hack's served copy says Simple Hack (hackAuthJS).
  var signInBy = "Sign-in by Simple Host";
  var signInProduct = "Simple Host";
  var ownerSees = "The site owner sees the email you sign in with.";
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
    // Only on the site's own calls: a custom header on a cross-origin call
    // would need a preflight.
    if (framedElsewhere) {
      var sameOrigin = false;
      try { sameOrigin = new URL(url, location.href).origin === location.origin; } catch (e) {}
      if (sameOrigin) { options.headers = options.headers || {}; options.headers["X-SH-Framed"] = "1"; }
    }
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
          if (body && body.reason) err.reason = body.reason;
          if (body && body.tries_left != null) err.tries_left = body.tries_left;
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
        hopEnabled = !!(d && d.sso_enabled);
        if (d && d.email_enabled === false) emailEnabled = false;
        if (d && d.oidc_label) oidcLabel = d.oidc_label;
        return providers;
      });
      providerPromise.catch(function () { providerPromise = null; });
    }
    return providerPromise;
  }
  // Framed by another origin: the server then never gives this page the
  // owner's rights over saved data (a sibling site could overlay it).
  var framedElsewhere = (function () {
    try { return window.top !== window.self && window.top.location.origin !== location.origin; } catch (e) { return true; }
  })();
  function write(url, method, body, headers) {
    headers = headers || {};
    headers["Content-Type"] = "application/json";
    headers["X-SH-CSRF"] = "1";
    return request(url, {method: method, headers: headers, body: JSON.stringify(body)});
  }
  function storageURL(path) { return API_BASE + "/storage/" + path; }
  // The spam check (comments preset). Turnstile runs in a frame served from
  // the app's own address, so it works on every site host and own domain.
  // Messages are taken only from that frame's window and origin, and the
  // start message goes only to that origin; the token comes back the same
  // way and is sent with this one save.
  var APP_ORIGIN = (function () { try { return new URL(authApex()).origin; } catch (e) { return ""; } })();
  var spamBox = null;
  function spamCheck(resource) {
    if (spamBox) return spamBox.promise;
    var box = {}, wrap = document.createElement("div"), panel = document.createElement("div");
    var note = document.createElement("p"), frame = document.createElement("iframe"), cancel = document.createElement("button");
    wrap.setAttribute("role", "dialog"); wrap.setAttribute("aria-modal", "true"); wrap.setAttribute("aria-label", "Spam check");
    wrap.style.cssText = "position:fixed;inset:0;z-index:2147483646;background:rgba(0,0,0,.35);display:flex;align-items:center;justify-content:center";
    panel.style.cssText = "background:#fff;color:#111;border-radius:var(--sh-radius,10px);padding:16px;max-width:340px;font:15px/1.4 system-ui,sans-serif;box-shadow:0 8px 30px rgba(0,0,0,.25)";
    note.textContent = "One quick check that you are not a bot, then your post is sent.";
    note.style.margin = "0 0 10px";
    frame.title = "Spam check"; frame.src = APP_ORIGIN + "/v1/turnstile/frame";
    frame.style.cssText = "border:0;width:300px;height:70px;display:block";
    frame.setAttribute("referrerpolicy", "no-referrer");
    cancel.type = "button"; cancel.textContent = "Cancel"; cancel.style.cssText = "margin-top:10px;font:inherit;padding:4px 10px;cursor:pointer";
    panel.appendChild(note); panel.appendChild(frame); panel.appendChild(cancel); wrap.appendChild(panel);
    box.promise = new Promise(function (resolve, reject) {
      var timer;
      function done(err, token) {
        window.removeEventListener("message", onMessage);
        clearTimeout(timer);
        if (wrap.parentNode) wrap.parentNode.removeChild(wrap);
        spamBox = null;
        if (err) reject(err); else resolve(token);
      }
      function fail(code, message) { var e = new Error(message); e.code = code; done(e); }
      function onMessage(e) {
        if (e.origin !== APP_ORIGIN || e.source !== frame.contentWindow) return;
        var d = e.data || {};
        if (d.type === "sh-turnstile-ready") frame.contentWindow.postMessage({type: "sh-turnstile-start", resource: resource}, APP_ORIGIN);
        else if (d.type === "sh-turnstile-token" && typeof d.token === "string" && d.token) done(null, d.token);
        else if (d.type === "sh-turnstile-error") fail("spam_check_failed", "the spam check did not pass" + (d.code ? " (" + d.code + ")" : "") + "; try again");
      }
      window.addEventListener("message", onMessage);
      cancel.onclick = function () { fail("spam_check_cancelled", "the spam check was cancelled, so nothing was sent"); };
      timer = setTimeout(function () { fail("spam_check_failed", "the spam check took too long; try again"); }, 120000);
      (document.body || document.documentElement).appendChild(wrap);
    });
    spamBox = box;
    return box.promise;
  }
  // checkedWrite sends a save; when the resource needs the spam check, it
  // runs the check and sends the save once more with the token.
  function checkedWrite(url, method, body, resource) {
    return write(url, method, body).catch(function (e) {
      if (!e || e.code !== "spam_check_required" || !APP_ORIGIN) throw e;
      return spamCheck(resource).then(function (token) {
        var headers = {};
        headers["X-SH-Turnstile"] = token;
        return write(url, method, body, headers);
      });
    });
  }
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
  function storageRowID(id) {
    var n = Number(id);
    if (!Number.isInteger(n) || n < 1) throw new TypeError("row id must be a positive whole number");
    return String(n);
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
  // What the visitor typed, kept across the trip to Google and back: only on
  // a site's own origin, only plain text-like fields (never passwords, card
  // details, codes, or fields marked data-sh-nosave), for 15 minutes, and
  // filled back in only when the visitor returns signed in.
  var TYPED_KEY = "sh:typed:" + location.pathname + location.search;
  var TYPED_TYPES = ["text", "email", "tel", "url", "number", "date", "time", "datetime-local", "month", "week", "checkbox", "radio", "textarea", "select-one"];
  var TYPED_SKIP = /card|cc_?num|cvc|cvv|cvn|csc|iban|ssn|passw|pwd|otp|secret|token|account|routing|security|birth|bank|bsb|national_?id|maiden|authenticat|exp_?(month|year|date)|(^|[_\-\s#@])(pin|code|key|pass|pw|sort|tax|dob)([_\-\s]|$)/i;
  function typedField(el) {
    var type = (el.type || "").toLowerCase(), key = el.id ? "#" + el.id : (el.name ? "@" + el.name : "");
    if (!key || TYPED_TYPES.indexOf(type) < 0 || el.readOnly || el.disabled) return null;
    if (el.hasAttribute("data-sh-nosave") || (el.closest && el.closest("[data-sh-signin], [data-sh-nosave]"))) return null;
    if (/cc-|one-time-code|password/.test(el.autocomplete || "") || TYPED_SKIP.test(el.id + " " + el.name)) return null;
    return {key: key, type: type};
  }
  function saveTyped() {
    if (!ownOrigin) return;
    try {
      var values = {};
      Array.prototype.forEach.call(document.querySelectorAll("input, textarea, select"), function (el) {
        var f = typedField(el);
        if (!f) return;
        if (f.type === "radio") { if (el.checked && el.name) values["@" + el.name] = "\u0002" + el.value; return; }
        if (f.type === "checkbox") { values[f.key + "\u0001" + el.value] = el.checked ? "\u0001on" : "\u0001off"; return; }
        if (el.value) values[f.key] = el.value;
      });
      sessionStorage.setItem(TYPED_KEY, JSON.stringify({at: Date.now(), values: values}));
    } catch (e) {}
  }
  function restoreTyped(me) {
    var saved = null;
    try { saved = JSON.parse(sessionStorage.getItem(TYPED_KEY) || "null"); sessionStorage.removeItem(TYPED_KEY); } catch (e) {}
    if (!ownOrigin || !me || !me.signed_in || !saved || !saved.values || Date.now() - saved.at > 15 * 60 * 1000) return;
    Array.prototype.forEach.call(document.querySelectorAll("input, textarea, select"), function (el) {
      try {
        var f = typedField(el);
        if (!f) return;
        var v;
        if (f.type === "radio") { v = saved.values["@" + el.name]; if (v === "\u0002" + el.value) el.checked = true; else return; }
        else if (f.type === "checkbox") {
          v = saved.values[f.key + "\u0001" + el.value];
          if (v !== "\u0001on" && v !== "\u0001off") return;
          el.checked = v === "\u0001on";
        } else {
          v = saved.values[f.key];
          if (typeof v !== "string" || (el.value && el.value !== el.defaultValue && f.type !== "select-one")) return;
          if (f.type === "select-one" && !Array.prototype.some.call(el.options, function (o) { return o.value === v; })) return;
          el.value = v;
        }
        el.dispatchEvent(new Event("input", {bubbles: true}));
        el.dispatchEvent(new Event("change", {bubbles: true}));
      } catch (e) {}
    });
  }

  // Shared look for the sign-in dialog and the signed-in chip; follows the
  // page's light or dark scheme through system colours.
  var UI = "font:15px/1.4 system-ui,sans-serif;color:CanvasText;background:Canvas;";
  function uiButton(label, primary) {
    var b = document.createElement("button");
    b.type = "button";
    b.textContent = label;
    b.style.cssText = "font:inherit;cursor:pointer;padding:8px 14px;border-radius:var(--sh-radius,6px);" +
      (primary ? "color:#fff;background:var(--sh-accent,#5b5ef4);border:0" : "color:inherit;background:transparent;border:1px solid currentColor");
    return b;
  }
  function signInLead() {
    return emailEnabled ? "No password and no account to make. " + ownerSees : ownerSees;
  }
  function signInFine() {
    return emailEnabled ? "We email you a 6-digit code. " + signInBy + "." : signInBy + ".";
  }
  function pageEyebrow() {
    var t = "";
    try { t = String(document.title || "").replace(/\s+/g, " ").trim(); } catch (e) {}
    if (!t) t = location.host || "";
    if (t.length > 60) t = t.slice(0, 60).trim();
    return t;
  }
  function cardProviders(names) {
    var out = [];
    (names || []).forEach(function (name) {
      if (name === "google" || name === "oidc") out.push(name);
    });
    return out;
  }
  function startProvider(name) {
    try { localStorage.setItem(CHIP_KEY, "1"); } catch (e) {}
    SH.signIn({provider: name});
  }
  // GitHub is a configured provider the shared card does not draw itself.
  function githubExtra(names) {
    if (!names || names.indexOf("github") < 0) return null;
    var b = document.createElement("button");
    b.type = "button";
    b.textContent = "Continue with Github";
    b.style.cssText = "display:flex;align-items:center;justify-content:center;width:100%;min-height:44px;height:44px;margin-top:10px;border-radius:22px;border:1px solid rgba(128,128,128,.55);background:transparent;color:inherit;font:500 15px system-ui,sans-serif;cursor:pointer";
    b.addEventListener("click", function () { startProvider("github"); });
    return b;
  }
  function mountSignInCard(container, opts) {
    opts = opts || {};
    container.setAttribute("data-sh-signin", "");
    if (hopEnabled) {
      var button = uiButton("Sign in", true);
      button.addEventListener("click", function () { SH.signIn(); });
      container.appendChild(button);
      return {destroy: function () { button.remove(); }};
    }
    var card = window.SHSignIn.mount(container, {
      skin: "plain",
      eyebrow: opts.eyebrow || "",
      title: opts.title,
      lead: signInLead(),
      fine: signInFine(),
      product: signInProduct,
      providers: cardProviders(opts.providers),
      oidcLabel: oidcLabel,
      emailEnabled: emailEnabled,
      autofocus: !!opts.autofocus,
      requestCode: function (email) { return SH.email.request(email); },
      verifyCode: opts.verifyCode || function (email, code) { return SH.email.verify(email, code); },
      startProvider: startProvider,
      extra: opts.extra || null
    });
    var host = container.querySelector(".sh-si-host");
    if (host) host.setAttribute("data-sh-signin", "");
    return card;
  }
  function deepActive() {
    var node = document.activeElement;
    while (node && node.shadowRoot && node.shadowRoot.activeElement) node = node.shadowRoot.activeElement;
    return node;
  }
  function focusableIn(root, into) {
    if (!root || !root.querySelectorAll) return;
    var nodes = root.querySelectorAll("button, input, a[href], select, textarea");
    var i;
    for (i = 0; i < nodes.length; i++) {
      var n = nodes[i];
      if (n.disabled || n.getAttribute("aria-hidden") === "true") continue;
      into.push(n);
    }
    var all = root.querySelectorAll("*");
    for (i = 0; i < all.length; i++) {
      if (all[i].shadowRoot) focusableIn(all[i].shadowRoot, into);
    }
  }
  var chip = null, CHIP_KEY = "sh:chip:" + location.host;
  // A small "Signed in as ... Sign out" in the corner, for pages without a
  // sign-in box that signed someone in through the dialog.
  function showChip(me) {
    if (mounted || !me || !me.signed_in || !document.body) return;
    try { localStorage.setItem(CHIP_KEY, "1"); } catch (e) {}
    if (!chip) {
      chip = document.createElement("div");
      chip.setAttribute("data-sh-signin", "");
      chip.style.cssText = UI + "position:fixed;right:12px;bottom:12px;z-index:2147483000;display:flex;gap:10px;align-items:center;padding:8px 12px;border:1px solid rgba(128,128,128,.4);border-radius:999px;box-shadow:0 2px 10px rgba(0,0,0,.15);font-size:14px";
      document.body.appendChild(chip);
    }
    chip.textContent = "";
    var who = document.createElement("span");
    who.textContent = me.email ? "Signed in as " + me.email : "Signed in";
    var out = uiButton("Sign out");
    out.style.padding = "4px 10px";
    out.onclick = function () {
      SH.signOut().then(function () {
        try { localStorage.removeItem(CHIP_KEY); } catch (e) {}
        if (chip) { chip.remove(); chip = null; }
        window.dispatchEvent(new CustomEvent("sh:signed-out"));
      }).catch(function (e) { who.textContent = e.message; });
    };
    chip.appendChild(who);
    chip.appendChild(out);
  }
  // The sign-in dialog: the shared card (plain skin). Resolves with SH.me()
  // once signed in; rejects with code "cancelled" (Escape, backdrop or X).
  var dialogOpen = null;
  function signInDialog(names) {
    if (framedElsewhere) {
      // Never collect a sign-in inside another site's frame.
      var f = new Error("open this page in its own tab to sign in");
      f.code = "framed";
      return Promise.reject(f);
    }
    if (dialogOpen) return dialogOpen;
    dialogOpen = new Promise(function (resolve, reject) {
      var previous = document.activeElement;
      var shade = document.createElement("div");
      shade.setAttribute("data-sh-signin", "");
      shade.style.cssText = "position:fixed;inset:0;z-index:2147483001;background:rgba(0,0,0,.45);display:flex;align-items:center;justify-content:center;padding:16px;box-sizing:border-box";
      var box = document.createElement("div");
      box.setAttribute("role", "dialog");
      box.setAttribute("aria-modal", "true");
      box.setAttribute("aria-label", "Sign in");
      box.style.cssText = "position:relative;width:100%;max-width:380px;box-sizing:border-box";
      var closeBtn = document.createElement("button");
      closeBtn.type = "button";
      closeBtn.setAttribute("aria-label", "Close");
      closeBtn.textContent = "\u00d7";
      closeBtn.style.cssText = "position:absolute;top:4px;right:4px;z-index:2;width:44px;height:44px;border:0;background:transparent;color:light-dark(#5d5d66,#a8a8b0);font:22px/1 system-ui,sans-serif;cursor:pointer";
      box.appendChild(closeBtn);
      var closed = false;
      function restoreFocus() {
        if (previous && previous.focus && document.contains(previous)) {
          try { previous.focus(); } catch (e) {}
        }
      }
      function close() {
        if (closed) return;
        closed = true;
        document.removeEventListener("keydown", onKey, true);
        window.removeEventListener("sh:signed-in", onSignedIn);
        shade.remove();
        dialogOpen = null;
        restoreFocus();
      }
      function onSignedIn() {
        if (closed) return;
        close();
        SH.me({fresh: true}).then(function (me) { showChip(me); resolve(me); }, reject);
      }
      function cancel() {
        if (closed) return;
        close();
        var e = new Error("sign-in cancelled");
        e.code = "cancelled";
        reject(e);
      }
      function onKey(e) {
        if (e.key === "Escape") {
          e.preventDefault();
          e.stopPropagation();
          cancel();
          return;
        }
        if (e.key !== "Tab") return;
        var items = [];
        focusableIn(box, items);
        if (!items.length) return;
        var active = deepActive();
        var first = items[0], last = items[items.length - 1];
        if (e.shiftKey && (active === first || active === box)) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && active === last) {
          e.preventDefault();
          first.focus();
        }
      }
      closeBtn.addEventListener("click", cancel);
      window.addEventListener("sh:signed-in", onSignedIn);
      document.addEventListener("keydown", onKey, true);
      shade.addEventListener("click", function (e) { if (e.target === shade) cancel(); });
      shade.appendChild(box);
      (document.body || document.documentElement).appendChild(shade);
      if (window.SHSignIn) {
        mountSignInCard(box, {
          eyebrow: pageEyebrow(),
          title: "Sign in to continue",
          providers: names,
          autofocus: true,
          extra: githubExtra(names)
        });
      } else {
        box.appendChild(document.createTextNode("Sign-in is not available on this site."));
      }
    });
    return dialogOpen;
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
      if (!providers) return loadProviders().then(function () { return SH.signIn(options); });
      if (!hopEnabled && providers && !providers.length) throw new Error("sign-in is not configured on this host");
      // Start on the site's own host (the page's origin, or returnTo's): it
      // ties the sign-in to this browser with a cookie there, then goes on to
      // the provider. Only that browser can finish it.
      var back = options.returnTo || location.href, origin = location.origin;
      try { origin = new URL(back, location.href).origin; } catch (e) {}
      // Keep what the visitor typed: the page fills it back in when they return.
      if (origin === location.origin) saveTyped();
      location.href = origin + (hopEnabled ? "/v1/sso/start" : "/v1/visitor/oauth/" + encodeURIComponent(options.provider || "google")) +
        "?return_to=" + encodeURIComponent(back);
    },
    signOut: function () {
      return request(API_ORIGIN + "/v1/visitor/logout", {
        method: "POST", headers: {"Content-Type": "application/json", "X-SH-CSRF": "1"}
      }).then(function (body) { meCache = null; if (body && body.redirect_url) location.assign(body.redirect_url); });
    },
    requireSignIn: function () {
      if (pendingSignIn) {
        // Signed in meanwhile (another tab)? Settle the shared wait.
        SH.me({fresh: true}).then(function (me) {
          if (me.signed_in) window.dispatchEvent(new CustomEvent("sh:signed-in", {detail: me}));
        }).catch(function () {});
        return pendingSignIn;
      }
      function settled(result, failed) {
        pendingSignIn = null;
        if (failed) throw result;
        return result;
      }
      // Always re-check: a cached answer may be past expiry or signed out elsewhere.
      pendingSignIn = SH.me({fresh: true}).then(function (me) {
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
          return loadProviders().catch(function () { return providers || []; }).then(function (names) {
            if (!emailEnabled && !names.length) {
              var e = new Error("sign-in is not available on this site");
              e.code = "no_sign_in";
              throw e;
            }
            window.dispatchEvent(new CustomEvent("sh:signin-required"));
            if (mounted.scrollIntoView) mounted.scrollIntoView({block: "center"});
            return new Promise(function (resolve, reject) {
              function signedIn() {
                window.removeEventListener("sh:signed-in", signedIn);
                SH.me({fresh: true}).then(resolve, reject);
              }
              window.addEventListener("sh:signed-in", signedIn);
            });
          });
        }
        // No sign-in box on the page: ask in a small dialog, then resume the save.
        return loadProviders().catch(function () { return providers || []; }).then(function (names) {
          if (!emailEnabled && !names.length) {
            var e = new Error("sign-in is not available on this site");
            e.code = "no_sign_in";
            throw e;
          }
          return signInDialog(names);
        });
      }).then(function (r) { return settled(r, false); }, function (e) { return settled(e, true); });
      return pendingSignIn;
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
          set: function (key, value) { return checkedWrite(base + "/keys/" + storageSegment(key, "key"), "PUT", {value: value}, name); },
          delete: function (key) { return request(base + "/keys/" + storageSegment(key, "key"), {method: "DELETE", headers: {"X-SH-CSRF": "1"}}); }
        };
      },
      sqlite: function (name) {
        var base = storageURL("sqlite/" + storageSegment(name, "resource name"));
        return {
          table: function (table) {
            var rows = base + "/tables/" + storageSegment(table, "table name") + "/rows";
            return {
              add: function (values) { return checkedWrite(rows, "POST", values, name); },
              // where: {column: value}, at most three, equality only.
              list: function (options) {
                // Also take order "-id" or "id desc" (a common way to ask for newest first).
                if (options && typeof options.order === "string") {
                  var o = options.order.trim(), m = o.match(/^(\w+)\s+(asc|desc)$/i);
                  if (o.charAt(0) === "-") options = Object.assign({}, options, {order: o.slice(1), desc: 1});
                  else if (m) options = Object.assign({}, options, {order: m[1], desc: m[2].toLowerCase() === "desc" ? 1 : 0});
                }
                if (options && typeof options.desc === "boolean") options = Object.assign({}, options, {desc: options.desc ? 1 : 0});
                var q = new URLSearchParams();
                ['order','desc','limit','after'].forEach(function (key) { if (options && options[key] != null) q.set(key, String(options[key])); });
                if (options && options.where) Object.keys(options.where).forEach(function (col) { q.set("where." + col, String(options.where[col])); });
                // The server's answer {columns, rows, next_after, mine} plus items
                // (the rows as objects). It also reads like that array of objects
                // (result[0], result.length, result.forEach, map, filter, ...):
                // those helpers are not enumerable, so JSON and Object.keys see
                // the server's fields only.
                return request(rows + (q.toString() ? '?' + q.toString() : '')).then(function (r) {
                  if (!r || !Array.isArray(r.rows)) return r;
                  var items = SH.storage.toObjects(r);
                  r.items = items;
                  function hidden(k, v) { if (!Object.prototype.hasOwnProperty.call(r, k)) Object.defineProperty(r, k, {value: v, configurable: true}); }
                  hidden("length", items.length);
                  items.forEach(function (o, i) { hidden(String(i), o); });
                  ["forEach", "map", "filter", "find", "findIndex", "some", "every", "reduce", "slice", "indexOf", "includes", "sort", "reverse", "concat", "join"].forEach(function (m) {
                    hidden(m, function () { return items[m].apply(items, arguments); });
                  });
                  if (typeof Symbol !== "undefined" && Symbol.iterator) hidden(Symbol.iterator, function () { return items[Symbol.iterator](); });
                  return r;
                });
              },
              get: function (id) { return request(rows + "/" + storageRowID(id)); },
              edit: function (id, values) { return write(rows + "/" + storageRowID(id), "PATCH", values); },
              delete: function (id) { return request(rows + "/" + storageRowID(id), {method: "DELETE", headers: {"X-SH-CSRF": "1"}}); }
            };
          },
          // The owner's, or a legacy full-mode database saved before presets.
          // Pages use table(), never SQL.
          query: function (sql, params) { return write(base + "/query", "POST", {sql: sql, params: params || []}); },
          execute: function (sql, params) { return write(base + "/execute", "POST", {sql: sql, params: params || []}); }
        };
      },
      // Rows as objects: {column: value, ..., mine: true|false} (mine only
      // where the answer says whose rows are whose).
      toObjects: function (result) {
        // Already objects (result.items, or a filtered copy of it).
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
      files: function (name) {
        var base = storageURL("files/" + storageSegment(name, "resource name"));
        function objectURL(path) { return base + "/objects/" + storagePath(path); }
        return {
          list: function (prefix, options) { return request(base + "/objects" + storagePageQuery(prefix, options)); },
          get: function (path) { return storageBlob(objectURL(path)); },
          put: function (path, file, options) {
            return storageRawWrite(objectURL(path), file, options && options.contentType);
          },
          // The same as put, under the name people reach for first.
          upload: function (path, file, options) {
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
          box.style.cssText = "";
          if (me.signed_in) {
            box.style.cssText = "display:flex;flex-wrap:wrap;align-items:center;gap:12px;padding:12px 14px;font:15px/1.4 system-ui,sans-serif;color:light-dark(#1a1a1a,#f2f2f2);background:light-dark(#fff,#1c1c1f);border-radius:var(--sh-radius,14px)";
            var label = document.createElement("span");
            label.textContent = me.email ? "Signed in as " + me.email : "Signed in";
            box.appendChild(label);
            button("Sign out", function () { SH.signOut().then(render).catch(showError); });
            return;
          }
          return loadProviders().catch(function () { return providers || []; }).then(function (names) {
            if (!emailEnabled && !names.length) {
              box.textContent = "Sign-in is not available on this site.";
              box.style.cssText = "font:inherit;color:var(--sh-muted,#666)";
              return;
            }
            if (!window.SHSignIn) {
              box.textContent = "Sign-in is not available on this site.";
              return;
            }
            var card = mountSignInCard(box, {
              title: "Sign in",
              providers: names,
              extra: githubExtra(names),
              // Signed in: the box shows who, with Sign out.
              verifyCode: function (email, code) {
                return SH.email.verify(email, code).then(function (body) {
                  if (card) card.destroy();
                  render().catch(showError);
                  return body;
                });
              }
            });
          });
        });
      }
      // A save that needs sign-in brings the box up to date, unless the
      // sign-in card is already showing: re-rendering would wipe what the
      // visitor has typed. The card's inputs live in a shadow root, so the
      // host element is the signal, not a form in the light DOM.
      window.addEventListener("sh:signin-required", function () {
        if (box.querySelector(".sh-si-host") || box.querySelector("form")) return;
        render().catch(showError);
      });
      return render();
    }
  };
  // Preload for synchronous signIn(); mount/requireSignIn await discovery explicitly.
  if (!noBackend && !contentHost) loadProviders().catch(function () {});
  SH.ready = SH.me().catch(function () { return {signed_in: false}; });
  if (!noBackend) {
    var onReady = function () {
      var flagged = false;
      try { flagged = localStorage.getItem(CHIP_KEY) === "1"; } catch (e) {}
      SH.ready.then(function (me) {
        restoreTyped(me);
        if (flagged && me.signed_in) showChip(me);
      });
    };
    if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", function () { setTimeout(onReady, 0); });
    else setTimeout(onReady, 0);
  }
}());
