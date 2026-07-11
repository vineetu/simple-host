/*
 * simple-host feedback overlay — tap-to-comment review for UI prototypes.
 *
 * Drop into any deployed page:
 *     <script src="https://simple-host.app/feedback.js"></script>
 *
 * Auto-derives the state API from the page URL:
 *   - Content host (sites.<domain>/<handle>/<site>/…): /v1/u/<handle>/sites/<site>/state
 *   - Legacy per-site subdomain or custom domain: same-origin /v1/sites/<label>/state
 * On a custom domain set window.SH_FEEDBACK = { site: "…" } (and optionally handle).
 *
 * Hosted elsewhere (GitHub Pages, Netlify, …)? Point it at a Simple Host site
 * whose owner allowed this page's origin (PUT /v1/sites/{site}/allowed-origins):
 *     <script>window.SH_FEEDBACK = { site:"my-backend", base:"https://simple-host.app" }</script>
 *     // optional handle for the unambiguous user-scoped route:
 *     // { site:"my-backend", handle:"alice", base:"https://simple-host.app" }
 *
 * HOW REVIEWERS USE IT
 *   Browse mode (default): the page behaves completely normally.
 *   Tap the floating "Feedback" pill → comment mode: a shield captures the next
 *   tap/click, drops a numbered pin, and opens a composer (bottom sheet on
 *   phones, popover on desktop). Post → back to browse mode.
 *   Shortcut on touch devices: LONG-PRESS (~½s) anywhere in browse mode.
 *   Tap any numbered pin to read that note.
 *
 * THEMING: window.SH_FEEDBACK = { accent:"#0f9d63", label:"Feedback",
 *   theme:"light"|"dark"|"auto" } — or override the --shf-* CSS variables.
 *
 * HOW AN AGENT READS THE FEEDBACK BACK
 *     curl -H "Origin: https://sites.<domain>" \
 *       https://sites.<domain>/v1/u/<handle>/sites/<site>/state
 *   → { "_comments": [ { body, author, ts, sel, text, nx, ny, px, py } ] }
 *   `sel` is a CSS selector for the element the reviewer tapped, `text` its
 *   visible text (locate it in your source), `nx`/`ny` the position within that
 *   element (0..1). Fix what each note asks, redeploy, done.
 *
 * Writes are single atomic PATCH appends (no read-modify-write), so many
 * simultaneous reviewers never clobber each other. The state store is PUBLIC —
 * fine for mockup review; don't put anything sensitive in a comment.
 */
(function () {
  "use strict";
  var _cfg = window.SH_FEEDBACK || {};
  var API;
  if (_cfg.site) {
    var base = (_cfg.base || "https://simple-host.app").replace(/\/+$/, "");
    if (_cfg.handle) {
      API = base + "/v1/u/" + _cfg.handle + "/sites/" + _cfg.site + "/state";
    } else {
      API = base + "/v1/sites/" + _cfg.site + "/state";
    }
  } else {
    var host = location.hostname, path = location.pathname;
    if (location.protocol === "file:" || host === "localhost" || host === "127.0.0.1") {
      console.info("[feedback] set window.SH_FEEDBACK={site:'your-site'} to point at a backend, or deploy this page to enable tap-to-comment.");
      return;
    }
    // v3 path model: /<handle>/<site>/... on the content host (host starts with "sites.")
    var m = path.match(/^\/([a-z0-9-]{1,39})\/([a-z0-9-]{1,63})(?:\/|$)/);
    if (host.split(".")[0] === "sites" && m) {
      API = location.origin + "/v1/u/" + m[1] + "/sites/" + m[2] + "/state";
    } else {
      // legacy per-site subdomain OR a custom domain: same-origin, site = first label
      // (subdomain) — for a custom domain the author should set window.SH_FEEDBACK={site:"..."}.
      var sub = host.split(".")[0];
      API = location.origin + "/v1/sites/" + sub + "/state";
    }
  }
  var KEY = "_comments";

  var author = localStorage.getItem("sh_feedback_author") || "";
  var comments = [], etag = null;
  var mode = false;          // comment mode on/off
  var TOUCH = matchMedia("(pointer: coarse)").matches;

  // ---- theme -----------------------------------------------------------------
  function _rgb(s) { var m = (s || "").match(/[\d.]+/g); return m ? m.map(Number) : null; }
  function _lum(c) { return c ? (0.2126 * c[0] + 0.7152 * c[1] + 0.114 * c[2]) / 255 : 1; }
  var _bg = _rgb(getComputedStyle(document.body).backgroundColor);
  if (!_bg || _bg[3] === 0) _bg = _rgb(getComputedStyle(document.documentElement).backgroundColor) || [255, 255, 255];
  var _dark = _cfg.theme === "dark" || (_cfg.theme !== "light" && _lum(_bg) < 0.5);
  var A = _cfg.accent || (_dark ? "#8b8eff" : "#5b5ef4");
  var LABEL = _cfg.label || "Feedback";

  var css = document.createElement("style");
  css.textContent =
    ":root{--shf-accent:" + A + ";--shf-on-accent:#fff;" +
      "--shf-card:" + (_dark ? "#1c2030" : "#ffffff") + ";" +
      "--shf-ink:" + (_dark ? "#f1f2f8" : "#1c2030") + ";" +
      "--shf-border:" + (_dark ? "rgba(255,255,255,.16)" : "rgba(0,0,0,.12)") + ";" +
      "--shf-muted:" + (_dark ? "rgba(235,235,245,.55)" : "rgba(60,60,70,.6)") + ";" +
      "--shf-field:" + (_dark ? "rgba(255,255,255,.07)" : "rgba(0,0,0,.045)") + "}" +
    ".shf-btn{position:fixed;right:16px;bottom:16px;z-index:2147483600;display:inline-flex;align-items:center;gap:8px;" +
      "font:600 14px/1 inherit;font-family:inherit;background:var(--shf-card);color:var(--shf-ink);border:1px solid var(--shf-border);" +
      "border-radius:999px;padding:11px 16px;box-shadow:0 6px 20px rgba(0,0,0,.18);cursor:pointer;transition:transform .12s}" +
    ".shf-btn:active{transform:scale(.96)}" +
    ".shf-btn .shf-n{background:var(--shf-accent);color:var(--shf-on-accent);border-radius:999px;font-size:11px;font-weight:700;padding:2px 7px}" +
    ".shf-btn.on{background:var(--shf-accent);color:var(--shf-on-accent);border-color:var(--shf-accent)}" +
    ".shf-shield{position:fixed;inset:0;z-index:2147483590;background:" + (_dark ? "rgba(120,130,255,.08)" : "rgba(80,90,255,.05)") + ";cursor:crosshair;touch-action:none}" +
    ".shf-hint{position:fixed;top:12px;left:50%;transform:translateX(-50%);z-index:2147483601;background:var(--shf-card);color:var(--shf-ink);" +
      "border:1px solid var(--shf-border);border-radius:999px;box-shadow:0 6px 20px rgba(0,0,0,.18);font:600 13px/1.2 inherit;font-family:inherit;" +
      "padding:9px 16px;max-width:92vw;text-align:center}" +
    ".shf-pin{position:absolute;z-index:2147483595;width:28px;height:28px;margin:-28px 0 0 -3px;background:var(--shf-accent);color:var(--shf-on-accent);" +
      "border:2px solid #fff;border-radius:50% 50% 50% 3px;font:700 12px/1 inherit;font-family:inherit;display:flex;align-items:center;justify-content:center;" +
      "cursor:pointer;box-shadow:0 3px 8px rgba(0,0,0,.28);opacity:.82;transition:opacity .15s,transform .12s}" +
    ".shf-pin:hover,.shf-pin.hot{opacity:1;transform:scale(1.08)}" +
    ".shf-pin.ghost{opacity:.55;pointer-events:none}" +
    /* composer / viewer: popover on wide screens, bottom sheet on phones */
    ".shf-pop{position:fixed;z-index:2147483602;background:var(--shf-card);color:var(--shf-ink);border:1px solid var(--shf-border);" +
      "border-radius:14px;box-shadow:0 12px 40px rgba(0,0,0,.25);padding:14px;width:300px;font:14px/1.5 inherit;font-family:inherit}" +
    "@media (max-width:640px){.shf-pop{left:0!important;right:0;top:auto!important;bottom:0;width:auto;border-radius:16px 16px 0 0;" +
      "padding:14px 16px calc(14px + env(safe-area-inset-bottom))}}" +
    ".shf-pop .shf-ctx{font-size:12px;color:var(--shf-muted);margin-bottom:8px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}" +
    ".shf-pop textarea,.shf-pop input{width:100%;box-sizing:border-box;background:var(--shf-field);color:inherit;border:1px solid var(--shf-border);" +
      "border-radius:9px;padding:9px 10px;font:inherit;font-family:inherit;margin:0 0 8px;outline:none}" +
    ".shf-pop textarea:focus,.shf-pop input:focus{border-color:var(--shf-accent)}" +
    ".shf-pop textarea{min-height:72px;resize:vertical}" +
    ".shf-row{display:flex;gap:8px;justify-content:flex-end;align-items:center}" +
    ".shf-save{background:var(--shf-accent);color:var(--shf-on-accent);border:0;border-radius:9px;padding:8px 18px;font:600 14px inherit;font-family:inherit;cursor:pointer}" +
    ".shf-save:disabled{opacity:.55}" +
    ".shf-cancel{background:transparent;color:var(--shf-muted);border:0;font:600 13px inherit;font-family:inherit;cursor:pointer;padding:8px 10px}" +
    ".shf-cmt b{color:var(--shf-accent)}.shf-meta{color:var(--shf-muted);font-size:12px;margin:2px 0 8px}" +
    ".shf-toast{position:fixed;left:50%;bottom:76px;transform:translateX(-50%);z-index:2147483602;background:var(--shf-card);color:var(--shf-ink);" +
      "border:1px solid var(--shf-border);border-radius:999px;padding:8px 16px;font:600 13px inherit;font-family:inherit;box-shadow:0 6px 20px rgba(0,0,0,.18)}";
  document.head.appendChild(css);

  // ---- FAB + shield + hint -----------------------------------------------------
  var btn = document.createElement("button");
  btn.className = "shf-btn";
  btn.type = "button";
  document.body.appendChild(btn);
  var shield = null, hint = null;

  function refreshBtn() {
    btn.innerHTML = "";
    btn.appendChild(document.createTextNode(mode ? "✕ Cancel" : "💬 " + LABEL));
    if (!mode && comments.length) { var n = document.createElement("span"); n.className = "shf-n"; n.textContent = comments.length; btn.appendChild(n); }
    btn.classList.toggle("on", mode);
  }
  function setMode(on) {
    mode = on;
    closePop();
    if (shield) { shield.remove(); shield = null; }
    if (hint) { hint.remove(); hint = null; }
    if (on) {
      // The shield swallows ALL page interaction so a tap can only mean "comment
      // here" — buttons/links underneath don't fire. Second tap on the FAB (or
      // Esc) cancels.
      shield = document.createElement("div");
      shield.className = "shf-shield";
      shield.addEventListener("pointerdown", onShieldTap);
      document.body.appendChild(shield);
      hint = document.createElement("div");
      hint.className = "shf-hint";
      hint.textContent = TOUCH ? "Tap where you want to leave a note" : "Click where you want to leave a note";
      document.body.appendChild(hint);
    }
    refreshBtn();
  }
  btn.addEventListener("click", function () { setMode(!mode); });
  document.addEventListener("keydown", function (e) { if (e.key === "Escape") setMode(false); });

  function onShieldTap(e) {
    e.preventDefault(); e.stopPropagation();
    // Find what the reviewer actually pointed at beneath the shield.
    shield.style.pointerEvents = "none";
    var el = document.elementFromPoint(e.clientX, e.clientY) || document.body;
    shield.style.pointerEvents = "";
    openComposer(e.clientX, e.clientY, anchorFor(el, e.clientX, e.clientY));
  }

  // ---- long-press shortcut (touch, browse mode) --------------------------------
  var lpTimer = null, lpStart = null, lpFired = false;
  document.addEventListener("touchstart", function (e) {
    if (mode || curPop || e.touches.length !== 1) return;
    if (e.target.closest(".shf-btn,.shf-pop,.shf-pin")) return;
    var t = e.touches[0];
    lpStart = { x: t.clientX, y: t.clientY }; lpFired = false;
    lpTimer = setTimeout(function () {
      lpFired = true;
      var el = document.elementFromPoint(lpStart.x, lpStart.y) || document.body;
      openComposer(lpStart.x, lpStart.y, anchorFor(el, lpStart.x, lpStart.y));
    }, 550);
  }, { passive: true });
  document.addEventListener("touchmove", function (e) {
    if (!lpTimer || !lpStart) return;
    var t = e.touches[0];
    if (Math.abs(t.clientX - lpStart.x) > 10 || Math.abs(t.clientY - lpStart.y) > 10) { clearTimeout(lpTimer); lpTimer = null; }
  }, { passive: true });
  document.addEventListener("touchend", function () { if (lpTimer) { clearTimeout(lpTimer); lpTimer = null; } }, { passive: true });
  document.addEventListener("contextmenu", function (e) { if (lpFired || curPop) { e.preventDefault(); lpFired = false; } });

  // ---- anchor capture ----------------------------------------------------------
  function cssPath(el) {
    if (!(el instanceof Element)) return "";
    var parts = [];
    while (el && el.nodeType === 1 && el !== document.body && parts.length < 6) {
      if (el.id) { parts.unshift("#" + CSS.escape(el.id)); break; }
      var tag = el.tagName.toLowerCase();
      var i = 1, sib = el;
      while ((sib = sib.previousElementSibling)) if (sib.tagName === el.tagName) i++;
      parts.unshift(tag + ":nth-of-type(" + i + ")");
      el = el.parentElement;
    }
    return parts.join(" > ");
  }
  function anchorFor(el, cx, cy) {
    var r = el.getBoundingClientRect();
    var doc = document.documentElement;
    return {
      sel: cssPath(el),                                                  // which element
      text: (el.innerText || el.textContent || "").trim().slice(0, 80),  // its text (context for the agent)
      nx: r.width ? (cx - r.left) / r.width : 0,                         // pos within element (0..1)
      ny: r.height ? (cy - r.top) / r.height : 0,
      px: (cx + window.scrollX) / Math.max(1, doc.scrollWidth),          // page-relative fallback
      py: (cy + window.scrollY) / Math.max(1, doc.scrollHeight),
      vw: window.innerWidth
    };
  }

  // ---- networking (atomic append + cheap live poll) -----------------------------
  function ingest(st) {
    comments = (st && Array.isArray(st[KEY])) ? st[KEY] : [];
    render();
  }
  async function load() {
    try {
      var res = await fetch(API, { cache: "no-store", credentials: "include" });
      etag = res.headers.get("ETag");
      ingest(res.ok ? await res.json() : null);
    } catch (e) { ingest(null); }
  }
  async function poll() {
    try {
      var res = await fetch(API, { cache: "no-store", credentials: "include", headers: etag ? { "If-None-Match": etag } : {} });
      if (res.status === 304) return;
      if (res.ok) { etag = res.headers.get("ETag"); ingest(await res.json()); }
    } catch (e) {}
  }
  async function addComment(c) {
    var res = await fetch(API, {
      method: "PATCH",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ops: [{ op: "append", path: KEY, value: c }] })
    });
    if (!res.ok) throw new Error("save " + res.status);
    etag = res.headers.get("ETag");
    ingest(await res.json());
  }

  // ---- pins ---------------------------------------------------------------------
  function pinXY(c) {
    var el = c.sel ? document.querySelector(c.sel) : null;
    if (el) {
      var r = el.getBoundingClientRect();
      return { x: window.scrollX + r.left + (c.nx || 0) * r.width,
               y: window.scrollY + r.top + (c.ny || 0) * r.height };
    }
    return { x: (c.px || 0) * document.documentElement.scrollWidth,
             y: (c.py || 0) * document.documentElement.scrollHeight };
  }
  function render() {
    Array.prototype.forEach.call(document.querySelectorAll(".shf-pin:not(.ghost)"), function (n) { n.remove(); });
    comments.forEach(function (c, i) {
      var p = pinXY(c);
      var pin = document.createElement("button");
      pin.type = "button";
      pin.className = "shf-pin";
      pin.style.left = p.x + "px";
      pin.style.top = p.y + "px";
      pin.textContent = i + 1;
      pin.addEventListener("click", function (ev) { ev.stopPropagation(); ev.preventDefault(); showComment(c, p); });
      document.body.appendChild(pin);
    });
    refreshBtn();
  }
  function showComment(c, p) {
    closePop();
    var pop = mkPop(p.x - window.scrollX, p.y - window.scrollY);
    var wrap = document.createElement("div"); wrap.className = "shf-cmt";
    var b = document.createElement("b"); b.textContent = c.author || "anon";
    var meta = document.createElement("div"); meta.className = "shf-meta";
    meta.textContent = (c.text ? "on “" + c.text + "” · " : "") + when(c.ts);
    var body = document.createElement("div"); body.textContent = c.body;
    wrap.appendChild(b); wrap.appendChild(meta); wrap.appendChild(body);
    pop.appendChild(wrap);
    var row = document.createElement("div"); row.className = "shf-row";
    var x = document.createElement("button"); x.className = "shf-cancel"; x.textContent = "Close"; x.onclick = closePop;
    row.appendChild(x); pop.appendChild(row);
  }
  function when(ts) {
    if (!ts) return "";
    var d = (Date.now() - ts) / 864e5;
    if (d < 1 / 24) return "just now";
    if (d < 1) return Math.floor(d * 24) + "h ago";
    if (d < 30) return Math.floor(d) + "d ago";
    return new Date(ts).toLocaleDateString();
  }

  // ---- composer -------------------------------------------------------------------
  function openComposer(cx, cy, anchor) {
    setModeSilent(false);
    closePop();
    // Ghost pin marks the exact spot while composing.
    var ghost = document.createElement("div");
    ghost.className = "shf-pin ghost";
    ghost.style.left = (cx + window.scrollX) + "px";
    ghost.style.top = (cy + window.scrollY) + "px";
    ghost.textContent = comments.length + 1;
    document.body.appendChild(ghost);

    var pop = mkPop(cx, cy);
    if (anchor.text) { var ctx = document.createElement("div"); ctx.className = "shf-ctx"; ctx.textContent = "on “" + anchor.text + "”"; pop.appendChild(ctx); }
    if (!author) { var n = document.createElement("input"); n.className = "shf-name"; n.placeholder = "your name (optional)"; pop.appendChild(n); }
    var ta = document.createElement("textarea"); ta.placeholder = "What should change here?"; pop.appendChild(ta);
    var row = document.createElement("div"); row.className = "shf-row";
    var cancel = document.createElement("button"); cancel.className = "shf-cancel"; cancel.textContent = "Cancel";
    var save = document.createElement("button"); save.className = "shf-save"; save.textContent = "Post note";
    row.appendChild(cancel); row.appendChild(save); pop.appendChild(row);
    var cleanup = function () { ghost.remove(); closePop(); };
    cancel.onclick = cleanup;
    ta.focus();
    save.onclick = function () {
      var body = ta.value.trim(); if (!body) return;
      var nameEl = pop.querySelector(".shf-name");
      if (nameEl && nameEl.value.trim()) { author = nameEl.value.trim(); localStorage.setItem("sh_feedback_author", author); }
      save.disabled = true; save.textContent = "Posting…";
      var c = Object.assign({ id: Date.now() + "-" + Math.round(Math.random() * 1e6), body: body, author: author || "anon", ts: Date.now() }, anchor);
      addComment(c).then(function () { cleanup(); toast("Note posted ✓"); })
        .catch(function () { save.disabled = false; save.textContent = "Post note"; alert("Could not save the note."); });
    };
  }
  function setModeSilent(on) { // leave comment mode without killing an open composer
    mode = on;
    if (shield) { shield.remove(); shield = null; }
    if (hint) { hint.remove(); hint = null; }
    refreshBtn();
  }
  function toast(msg) {
    var t = document.createElement("div"); t.className = "shf-toast"; t.textContent = msg;
    document.body.appendChild(t); setTimeout(function () { t.remove(); }, 1800);
  }

  // ---- small DOM helpers -------------------------------------------------------
  var curPop = null;
  function mkPop(x, y) {
    var pop = document.createElement("div");
    pop.className = "shf-pop";
    // Desktop: clamp near the point. Phones: the media query pins it as a bottom sheet.
    pop.style.left = Math.max(8, Math.min(x, window.innerWidth - 316)) + "px";
    pop.style.top = Math.max(8, Math.min(y + 12, window.innerHeight - 220)) + "px";
    document.body.appendChild(pop); curPop = pop; return pop;
  }
  function closePop() { if (curPop) { curPop.remove(); curPop = null; } var g = document.querySelector(".shf-pin.ghost"); if (g) g.remove(); }

  var rT;
  window.addEventListener("resize", function () { clearTimeout(rT); rT = setTimeout(render, 150); });
  load();
  setInterval(poll, 10000); // conditional GET → 304 when unchanged (nearly free)
})();
