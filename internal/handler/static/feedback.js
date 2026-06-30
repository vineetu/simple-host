/*
 * simple-host feedback overlay — click-to-comment for UI mockups.
 *
 * Drop into any deployed page:
 *     <script src="https://simple-host.app/feedback.js"></script>
 *
 * Reviewers click anywhere to drop a pinned comment. Comments persist in the
 * site's public state KV under the reserved key "_comments", using optimistic
 * concurrency (ETag / If-Match) so simultaneous reviewers don't clobber each
 * other. A coding agent can read them back with:
 *     curl -H "Origin: https://<site>.<domain>" https://<apex>/v1/sites/<site>/state
 * and act on each comment's anchor: { sel, text, body, nx, ny, ... }.
 *
 * NOTE: the state store is PUBLIC — comments are visible to anyone. Fine for
 * shared mockup review; don't put anything sensitive in a comment.
 */
(function () {
  "use strict";
  var host = location.hostname;
  // Only meaningful on a deployed *.<domain> page (needs an Origin the server
  // authorizes). On localhost/file:// we no-op with a hint.
  if (location.protocol === "file:" || host === "localhost" || host === "127.0.0.1") {
    console.info("[feedback] deploy this page to enable click-to-comment.");
    return;
  }
  var sub = host.split(".")[0];
  var apex = host.split(".").slice(-2).join(".");
  var API = location.protocol + "//" + apex + "/v1/sites/" + sub + "/state";
  var KEY = "_comments";
  var MAX = 500;

  var author = localStorage.getItem("sh_feedback_author") || "";
  var comments = [];
  var commentMode = false;

  // ---- styles ----------------------------------------------------------------
  var css = document.createElement("style");
  css.textContent =
    ".shf-btn{position:fixed;right:16px;bottom:16px;z-index:2147483600;font:600 14px system-ui,sans-serif;background:#5b5ef4;color:#fff;border:0;border-radius:999px;padding:10px 16px;box-shadow:0 4px 14px rgba(0,0,0,.2);cursor:pointer}" +
    ".shf-btn.on{background:#16a34a}" +
    ".shf-pin{position:absolute;z-index:2147483600;width:24px;height:24px;margin:-24px 0 0 -2px;background:#5b5ef4;color:#fff;border:2px solid #fff;border-radius:50% 50% 50% 2px;font:700 12px system-ui;display:flex;align-items:center;justify-content:center;cursor:pointer;box-shadow:0 2px 6px rgba(0,0,0,.3)}" +
    ".shf-pop{position:fixed;z-index:2147483601;background:#fff;color:#1a1a1a;border:1px solid #e5e5e5;border-radius:10px;box-shadow:0 8px 30px rgba(0,0,0,.18);padding:12px;width:260px;font:14px system-ui,sans-serif}" +
    ".shf-pop textarea,.shf-pop input{width:100%;box-sizing:border-box;border:1px solid #ddd;border-radius:6px;padding:6px;font:14px system-ui;margin:4px 0}" +
    ".shf-pop button{font:600 13px system-ui;border:0;border-radius:6px;padding:6px 12px;cursor:pointer}" +
    ".shf-save{background:#5b5ef4;color:#fff}.shf-cancel{background:#eee;margin-left:6px}" +
    ".shf-cmt{margin-bottom:8px;padding-bottom:8px;border-bottom:1px solid #f0f0f0}" +
    ".shf-cmt b{color:#5b5ef4}.shf-meta{color:#888;font-size:12px}" +
    "body.shf-picking, body.shf-picking *{cursor:crosshair !important}";
  document.head.appendChild(css);

  // ---- toggle button ---------------------------------------------------------
  var btn = document.createElement("button");
  btn.className = "shf-btn";
  document.body.appendChild(btn);
  function refreshBtn() {
    btn.textContent = (commentMode ? "✓ Click to comment" : "💬 Comments") + " (" + comments.length + ")";
    btn.classList.toggle("on", commentMode);
  }
  btn.addEventListener("click", function () {
    commentMode = !commentMode;
    document.body.classList.toggle("shf-picking", commentMode);
    refreshBtn();
  });

  // ---- anchor capture --------------------------------------------------------
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

  function captureAnchor(e) {
    var el = e.target;
    var r = el.getBoundingClientRect();
    var doc = document.documentElement;
    return {
      sel: cssPath(el),                                              // which element
      text: (el.innerText || el.textContent || "").trim().slice(0, 80), // its text (context for the agent)
      nx: r.width ? (e.clientX - r.left) / r.width : 0,              // pos within element
      ny: r.height ? (e.clientY - r.top) / r.height : 0,
      px: (e.pageX) / Math.max(1, doc.scrollWidth),                 // pos within page (fallback)
      py: (e.pageY) / Math.max(1, doc.scrollHeight),
      vw: window.innerWidth
    };
  }

  // ---- networking ------------------------------------------------------------
  var etag = null;

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

  // Cheap live refresh: conditional GET returns 304 (no body) when unchanged, so
  // polling is nearly free on the server.
  async function poll() {
    try {
      var res = await fetch(API, { cache: "no-store", credentials: "include", headers: etag ? { "If-None-Match": etag } : {} });
      if (res.status === 304) return;
      if (res.ok) { etag = res.headers.get("ETag"); ingest(await res.json()); }
    } catch (e) {}
  }

  // Append a comment with one atomic server-side op — no read-modify-write, no
  // retries, conflict-free even with many simultaneous reviewers.
  async function addComment(c) {
    try {
      var res = await fetch(API, {
        method: "PATCH",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ops: [{ op: "append", path: KEY, value: c }] })
      });
      if (!res.ok) { alert("Could not save comment (" + res.status + ")"); return; }
      etag = res.headers.get("ETag");
      ingest(await res.json());
    } catch (e) { alert("Could not save comment."); }
  }

  // ---- rendering -------------------------------------------------------------
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
    Array.prototype.forEach.call(document.querySelectorAll(".shf-pin"), function (n) { n.remove(); });
    comments.forEach(function (c, i) {
      var p = pinXY(c);
      var pin = document.createElement("div");
      pin.className = "shf-pin";
      pin.style.left = p.x + "px";
      pin.style.top = p.y + "px";
      pin.textContent = i + 1;
      pin.title = (c.author ? c.author + ": " : "") + c.body;
      pin.addEventListener("click", function (ev) { ev.stopPropagation(); showComment(c, p); });
      document.body.appendChild(pin);
    });
    refreshBtn();
  }

  function showComment(c, p) {
    closePop();
    var pop = mkPop(Math.min(p.x - window.scrollX, window.innerWidth - 280), Math.min(p.y - window.scrollY, window.innerHeight - 140));
    pop.innerHTML =
      '<div class="shf-cmt"><b>' + esc(c.author || "anon") + '</b> ' +
      '<span class="shf-meta">' + (c.text ? "on “" + esc(c.text) + "”" : "") + "</span><br>" +
      esc(c.body) + "</div>";
    var x = document.createElement("button");
    x.className = "shf-cancel"; x.textContent = "close";
    x.onclick = closePop; pop.appendChild(x);
  }

  // ---- comment composer ------------------------------------------------------
  document.addEventListener("click", function (e) {
    if (!commentMode) return;
    if (e.target.closest(".shf-btn,.shf-pop,.shf-pin")) return;
    e.preventDefault(); e.stopPropagation();
    var anchor = captureAnchor(e);
    openComposer(e.clientX, e.clientY, anchor);
    commentMode = false; document.body.classList.remove("shf-picking"); refreshBtn();
  }, true);

  function openComposer(cx, cy, anchor) {
    closePop();
    var pop = mkPop(Math.min(cx, window.innerWidth - 280), Math.min(cy, window.innerHeight - 180));
    var nameRow = author ? "" : '<input class="shf-name" placeholder="your name (optional)">';
    pop.innerHTML = nameRow + '<textarea class="shf-body" rows="3" placeholder="Leave a comment…"></textarea>';
    var save = document.createElement("button"); save.className = "shf-save"; save.textContent = "Comment";
    var cancel = document.createElement("button"); cancel.className = "shf-cancel"; cancel.textContent = "Cancel";
    pop.appendChild(save); pop.appendChild(cancel);
    cancel.onclick = closePop;
    var ta = pop.querySelector(".shf-body"); ta.focus();
    save.onclick = function () {
      var body = ta.value.trim(); if (!body) return;
      var nameEl = pop.querySelector(".shf-name");
      if (nameEl && nameEl.value.trim()) { author = nameEl.value.trim(); localStorage.setItem("sh_feedback_author", author); }
      var c = Object.assign({ id: Date.now() + "-" + Math.round(performance.now()), body: body, author: author || "anon", ts: Date.now() }, anchor);
      closePop();
      addComment(c);
    };
  }

  // ---- small DOM helpers -----------------------------------------------------
  var curPop = null;
  function mkPop(x, y) {
    var pop = document.createElement("div");
    pop.className = "shf-pop"; pop.style.left = x + "px"; pop.style.top = y + "px";
    document.body.appendChild(pop); curPop = pop; return pop;
  }
  function closePop() { if (curPop) { curPop.remove(); curPop = null; } }
  function esc(s) { return String(s).replace(/[&<>"]/g, function (c) { return ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]; }); }

  var t;
  window.addEventListener("resize", function () { clearTimeout(t); t = setTimeout(render, 150); });
  load();
  setInterval(poll, 10000); // cheap conditional-GET refresh
})();
