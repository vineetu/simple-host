/*
 * simple-host threaded comments — a drop-in discussion widget.
 *
 *   <section id="sh-comments"></section>
 *   <script src="https://simple-host.app/comments.js" defer></script>
 *
 * Auto-derives the state API from the page URL:
 *   - Content host (sites.<domain>/<handle>/<site>/…): /v1/u/<handle>/sites/<site>/state
 *   - Legacy per-site subdomain or custom domain: same-origin /v1/sites/<label>/state
 * On a custom domain set window.SH_COMMENTS = { site: "…" } (and optionally handle).
 *
 * Hosted elsewhere (GitHub Pages, Netlify, …)? Point it at a Simple Host site
 * whose owner has allowed this page's origin:
 *
 *   <script>window.SH_COMMENTS = { site: "my-backend", base: "https://simple-host.app" }</script>
 *   // optional handle for the unambiguous user-scoped route:
 *   // { site:"my-backend", handle:"alice", base:"https://simple-host.app" }
 *
 * THEMING — the default is deliberately neutral; match it to the host page via
 * config and/or CSS variables:
 *   window.SH_COMMENTS = { accent:"#b4451f", theme:"auto"|"light"|"dark",
 *                          title:"Comments", placeholder:"Say something…" }
 *   #sh-comments { --shc-accent:…; --shc-surface:…; --shc-field:…;
 *                  --shc-border:…; --shc-muted:…; --shc-radius:…; }
 *
 * Stores comments in the site's public state KV under "_comments" and per-comment
 * scores under "_votes", using ATOMIC ops (PATCH) so concurrent replies/votes
 * never clobber, and a conditional-GET poll so threads update live and cheap.
 *
 * PUBLIC store: anyone can read comments. Don't post anything sensitive.
 */
(function () {
  "use strict";
  var _cfg = window.SH_COMMENTS || {};
  var API;
  if (_cfg.site) {
    // Backend-anywhere: this page is hosted elsewhere and uses a Simple Host
    // site as its comments backend. The site owner must have added this page's
    // origin via PUT /v1/sites/{site}/allowed-origins.
    var base = (_cfg.base || "https://simple-host.app").replace(/\/+$/, "");
    if (_cfg.handle) {
      API = base + "/v1/u/" + _cfg.handle + "/sites/" + _cfg.site + "/state";
    } else {
      API = base + "/v1/sites/" + _cfg.site + "/state";
    }
  } else {
    var host = location.hostname, path = location.pathname;
    if (location.protocol === "file:" || host === "localhost" || host === "127.0.0.1") {
      console.info("[comments] set window.SH_COMMENTS={site:'your-site'} to point at a backend, or deploy this page on simple-host.");
      return;
    }
    // v3 path model: /<handle>/<site>/... on the content host (host starts with "sites.")
    var m = path.match(/^\/([a-z0-9-]{1,39})\/([a-z0-9-]{1,63})(?:\/|$)/);
    if (host.split(".")[0] === "sites" && m) {
      API = location.origin + "/v1/u/" + m[1] + "/sites/" + m[2] + "/state";
    } else {
      // legacy per-site subdomain OR a custom domain: same-origin, site = first label
      // (subdomain) — for a custom domain the author should set window.SH_COMMENTS={site:"..."}.
      var sub = host.split(".")[0];
      API = location.origin + "/v1/sites/" + sub + "/state";
    }
  }
  var CK = "_comments", VK = "_votes";

  var mount = document.getElementById("sh-comments") || document.body.appendChild(document.createElement("section"));
  mount.id = "sh-comments";
  var author = localStorage.getItem("sh_comments_author") || "";
  var voted = JSON.parse(localStorage.getItem("sh_comments_voted") || "{}");
  var comments = [], votes = {}, etag = null;

  // ---- theme: blend into the HOST page --------------------------------------
  // Inherit the page's font + text color; detect light/dark from the page
  // background (or force via config.theme); everything is exposed as --shc-*
  // custom properties so an integrating agent can restyle it precisely.
  function _rgb(s) { var m = (s || "").match(/[\d.]+/g); return m ? m.map(Number) : null; }
  function _lum(c) { return c ? (0.2126 * c[0] + 0.7152 * c[1] + 0.114 * c[2]) / 255 : 1; }
  var _bg = _rgb(getComputedStyle(document.body).backgroundColor);
  if (!_bg || _bg[3] === 0) _bg = _rgb(getComputedStyle(document.documentElement).backgroundColor) || [255, 255, 255];
  var _dark = _cfg.theme === "dark" || (_cfg.theme !== "light" && _lum(_bg) < 0.5);
  var A = _cfg.accent || (_dark ? "#8b8eff" : "#5b5ef4");

  var css = document.createElement("style");
  css.textContent =
    "#sh-comments{" +
      "--shc-accent:" + A + ";--shc-on-accent:#fff;" +
      "--shc-surface:" + (_dark ? "rgba(255,255,255,.045)" : "rgba(0,0,0,.025)") + ";" +
      "--shc-field:" + (_dark ? "rgba(255,255,255,.07)" : "rgba(255,255,255,.85)") + ";" +
      "--shc-border:" + (_dark ? "rgba(255,255,255,.14)" : "rgba(0,0,0,.12)") + ";" +
      "--shc-border-soft:" + (_dark ? "rgba(255,255,255,.09)" : "rgba(0,0,0,.07)") + ";" +
      "--shc-muted:" + (_dark ? "rgba(235,235,245,.55)" : "rgba(60,60,70,.62)") + ";" +
      "--shc-radius:12px;" +
      "max-width:720px;margin:48px auto 0;font-family:inherit;color:inherit;line-height:1.55}" +
    "#sh-comments,#sh-comments *{box-sizing:border-box}" +
    "#sh-comments h2{font-size:1.15em;font-weight:600;margin:0 0 4px;color:inherit;display:flex;align-items:baseline;gap:8px}" +
    "#sh-comments h2 .shc-count{font-size:.75em;font-weight:600;color:var(--shc-muted)}" +
    ".shc-rule{height:2px;width:44px;background:var(--shc-accent);border-radius:2px;margin:0 0 18px}" +

    /* composer card */
    ".shc-new,.shc-reply{border:1px solid var(--shc-border);border-radius:var(--shc-radius);background:var(--shc-surface);padding:10px 10px 8px;transition:border-color .15s}" +
    ".shc-new:focus-within,.shc-reply:focus-within{border-color:var(--shc-accent)}" +
    ".shc-new textarea,.shc-reply textarea{display:block;width:100%;min-height:64px;background:transparent;color:inherit;border:0;outline:0;padding:2px 4px;font:inherit;resize:vertical}" +
    ".shc-new textarea::placeholder,.shc-reply textarea::placeholder,.shc-name::placeholder{color:var(--shc-muted)}" +
    ".shc-foot{display:flex;align-items:center;gap:8px;margin-top:6px;border-top:1px solid var(--shc-border-soft);padding-top:8px}" +
    ".shc-name{flex:1;min-width:0;max-width:220px;background:transparent;color:inherit;border:0;outline:0;padding:2px 4px;font:inherit;font-size:.88em}" +
    ".shc-spacer{flex:1}" +
    ".shc-btn{background:var(--shc-accent);color:var(--shc-on-accent);border:0;border-radius:calc(var(--shc-radius) - 4px);padding:6px 16px;font:inherit;font-size:.9em;font-weight:600;cursor:pointer;transition:opacity .15s}" +
    ".shc-btn:hover{opacity:.88}.shc-btn:disabled{opacity:.55;cursor:default}" +

    /* comment rows */
    ".shc-list{margin-top:22px}" +
    ".shc-c{display:flex;gap:12px;margin-top:20px}" +
    ".shc-av{flex:none;width:30px;height:30px;border-radius:50%;display:flex;align-items:center;justify-content:center;color:#fff;font-size:13px;font-weight:700;user-select:none}" +
    ".shc-body{flex:1;min-width:0}" +
    ".shc-meta{font-size:.86em;color:var(--shc-muted);margin-bottom:1px}" +
    ".shc-meta b{color:inherit;font-weight:600}" +
    ".shc-text{white-space:pre-wrap;overflow-wrap:anywhere;margin:1px 0 4px}" +
    ".shc-actions{display:flex;align-items:center;gap:12px}" +
    ".shc-up{display:inline-flex;align-items:center;gap:5px;background:transparent;border:1px solid var(--shc-border);border-radius:999px;color:var(--shc-muted);cursor:pointer;font:inherit;font-size:.8em;font-weight:600;padding:2px 10px;line-height:1.5;transition:border-color .15s,color .15s}" +
    ".shc-up:hover{border-color:var(--shc-accent);color:var(--shc-accent)}" +
    ".shc-up.on{border-color:var(--shc-accent);color:var(--shc-accent);background:color-mix(in srgb,var(--shc-accent) 10%,transparent)}" +
    ".shc-link{background:none;border:0;color:var(--shc-muted);cursor:pointer;font:inherit;font-size:.83em;font-weight:600;padding:0}" +
    ".shc-link:hover{color:var(--shc-accent)}" +
    ".shc-kids{margin-top:2px;padding-left:14px;border-left:2px solid var(--shc-border-soft)}" +
    ".shc-reply{margin-top:10px}" +
    ".shc-empty{color:var(--shc-muted);font-size:.92em;margin-top:18px;font-style:italic}";
  document.head.appendChild(css);

  // ---- networking (atomic ops + cheap live poll) ----------------------------
  function ingest(st) {
    st = st && typeof st === "object" ? st : {};
    comments = Array.isArray(st[CK]) ? st[CK] : [];
    votes = (st[VK] && typeof st[VK] === "object") ? st[VK] : {};
    render();
  }
  async function load() {
    try { var r = await fetch(API, { cache: "no-store", credentials: "include" }); etag = r.headers.get("ETag"); ingest(r.ok ? await r.json() : null); }
    catch (e) { ingest(null); }
  }
  async function poll() {
    try {
      var r = await fetch(API, { cache: "no-store", credentials: "include", headers: etag ? { "If-None-Match": etag } : {} });
      if (r.status === 304) return;
      if (r.ok) { etag = r.headers.get("ETag"); ingest(await r.json()); }
    } catch (e) {}
  }
  async function patch(ops) {
    var r = await fetch(API, { method: "PATCH", credentials: "include", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ ops: ops }) });
    if (!r.ok) { alert("Could not save (" + r.status + ")"); return false; }
    etag = r.headers.get("ETag"); ingest(await r.json()); return true;
  }
  function postComment(parentId, body) {
    var c = { id: Date.now() + "-" + Math.round(Math.random() * 1e6), parentId: parentId || null, author: author || "anon", body: body, ts: Date.now() };
    return patch([{ op: "append", path: CK, value: c }]);
  }
  function upvote(id) {
    if (voted[id]) return;
    voted[id] = 1; localStorage.setItem("sh_comments_voted", JSON.stringify(voted));
    patch([{ op: "inc", path: VK + "." + id, by: 1 }]);
  }

  // ---- rendering ------------------------------------------------------------
  function score(id) { return (votes[id] | 0) || 0; }
  function when(ts) {
    var s = (Date.now() - ts) / 1000, m = s / 60, h = m / 60, d = h / 24;
    if (s < 45) return "just now";
    if (m < 60) return Math.floor(m) + "m ago";
    if (h < 24) return Math.floor(h) + "h ago";
    if (d < 30) return Math.floor(d) + "d ago";
    var dt = new Date(ts), opts = { month: "short", day: "numeric" };
    if (dt.getFullYear() !== new Date().getFullYear()) opts.year = "numeric";
    return dt.toLocaleDateString(undefined, opts);
  }
  // Deterministic avatar color from the author name (pleasant mid-saturation hue).
  function avColor(name) {
    var h = 0; name = name || "anon";
    for (var i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) % 360;
    return "hsl(" + h + "," + (_dark ? "42%" : "48%") + "," + (_dark ? "46%" : "42%") + ")";
  }
  function childrenOf(pid) {
    return comments.filter(function (c) { return (c.parentId || null) === pid; })
      .sort(function (a, b) { return pid === null ? (score(b.id) - score(a.id)) || (a.ts - b.ts) : a.ts - b.ts; });
  }
  function node(c) {
    var row = el("div", "shc-c");
    var name = c.author || "anon";
    var av = el("div", "shc-av"); av.style.background = avColor(name); av.textContent = (name[0] || "a").toUpperCase();
    var body = el("div", "shc-body");
    var meta = el("div", "shc-meta"); var b = document.createElement("b"); b.textContent = name;
    meta.appendChild(b); meta.appendChild(document.createTextNode(" · " + when(c.ts)));
    var text = el("div", "shc-text"); text.textContent = c.body;
    var actions = el("div", "shc-actions");
    var up = el("button", "shc-up" + (voted[c.id] ? " on" : "")); up.title = "upvote";
    up.appendChild(document.createTextNode("▲")); up.appendChild(document.createTextNode(" " + score(c.id)));
    up.onclick = function () { upvote(c.id); };
    var reply = el("button", "shc-link"); reply.textContent = "Reply";
    var replyBox = el("div", "shc-reply"); replyBox.style.display = "none";
    reply.onclick = function () { replyBox.style.display = replyBox.style.display === "none" ? "block" : "none"; if (replyBox.style.display === "block") replyBox.querySelector("textarea").focus(); };
    buildComposer(replyBox, "Reply…", function (val, done) { postComment(c.id, val).then(function (ok) { if (ok) { replyBox.style.display = "none"; } done(); }); });
    actions.appendChild(up); actions.appendChild(reply);
    body.appendChild(meta); body.appendChild(text); body.appendChild(actions); body.appendChild(replyBox);
    var kids = el("div", "shc-kids");
    var ch = childrenOf(c.id);
    if (ch.length) { ch.forEach(function (k) { kids.appendChild(node(k)); }); body.appendChild(kids); }
    row.appendChild(av); row.appendChild(body);
    return row;
  }
  function render() {
    mount.textContent = "";
    var h = document.createElement("h2");
    h.appendChild(document.createTextNode(_cfg.title || "Discussion"));
    var count = el("span", "shc-count"); count.textContent = comments.length || "";
    h.appendChild(count);
    mount.appendChild(h);
    mount.appendChild(el("div", "shc-rule"));
    var top = el("div", "shc-new");
    buildComposer(top, _cfg.placeholder || "Add a comment…", function (val, done) { postComment(null, val).then(done); }, true);
    mount.appendChild(top);
    var roots = childrenOf(null);
    if (!roots.length) {
      var empty = el("div", "shc-empty"); empty.textContent = "No comments yet — be the first.";
      mount.appendChild(empty);
      return;
    }
    var list = el("div", "shc-list");
    roots.forEach(function (c) { list.appendChild(node(c)); });
    mount.appendChild(list);
  }

  function buildComposer(container, placeholder, onSubmit, isTop) {
    if (!isTop) container.classList.add("shc-reply");
    var ta = document.createElement("textarea"); ta.placeholder = placeholder; container.appendChild(ta);
    var foot = el("div", "shc-foot");
    if (!author) { var n = document.createElement("input"); n.className = "shc-name"; n.placeholder = "your name (optional)"; foot.appendChild(n); }
    foot.appendChild(el("span", "shc-spacer"));
    var btn = el("button", "shc-btn"); btn.textContent = "Post"; foot.appendChild(btn);
    container.appendChild(foot);
    btn.onclick = function () {
      var val = ta.value.trim(); if (!val) return;
      var nameEl = container.querySelector(".shc-name");
      if (nameEl && nameEl.value.trim()) { author = nameEl.value.trim(); localStorage.setItem("sh_comments_author", author); }
      btn.disabled = true; btn.textContent = "Posting…";
      onSubmit(val, function () { btn.disabled = false; btn.textContent = "Post"; ta.value = ""; });
    };
  }
  function el(tag, cls) { var n = document.createElement(tag); if (cls) n.className = cls; return n; }

  load();
  setInterval(poll, 10000);
})();
