/*
 * simple-host threaded comments — a drop-in Reddit-style discussion widget.
 *
 *   <section id="sh-comments"></section>
 *   <script src="https://simple-host.app/comments.js" defer></script>
 *
 * Stores comments in the site's public state KV under "_comments" and per-comment
 * scores under "_votes", using ATOMIC ops (PATCH) so concurrent replies/votes
 * never clobber, and a conditional-GET poll so threads update live and cheap.
 *
 * PUBLIC store: anyone can read comments. Don't post anything sensitive.
 */
(function () {
  "use strict";
  var host = location.hostname;
  if (location.protocol === "file:" || host === "localhost" || host === "127.0.0.1") {
    console.info("[comments] deploy this page to enable comments.");
    return;
  }
  var sub = host.split(".")[0];
  var apex = host.split(".").slice(-2).join(".");
  var API = location.protocol + "//" + apex + "/v1/sites/" + sub + "/state";
  var CK = "_comments", VK = "_votes";

  var mount = document.getElementById("sh-comments") || document.body.appendChild(document.createElement("section"));
  mount.id = "sh-comments";
  var author = localStorage.getItem("sh_comments_author") || "";
  var voted = JSON.parse(localStorage.getItem("sh_comments_voted") || "{}");
  var comments = [], votes = {}, etag = null;

  // ---- styles: blend into the HOST page ------------------------------------
  // Inherit the page's font + text color; use translucent surfaces so it sits
  // on any background; auto light/dark accent. Override via window.SH_COMMENTS
  // = { accent:"#hex" }.
  function _rgb(s) { var m = (s || "").match(/[\d.]+/g); return m ? m.map(Number) : null; }
  function _lum(c) { return c ? (0.2126 * c[0] + 0.7152 * c[1] + 0.114 * c[2]) / 255 : 1; }
  var _bg = _rgb(getComputedStyle(document.body).backgroundColor);
  if (!_bg || _bg[3] === 0) _bg = _rgb(getComputedStyle(document.documentElement).backgroundColor) || [255, 255, 255];
  var _dark = _lum(_bg) < 0.5;
  var _cfg = window.SH_COMMENTS || {};
  var A = _cfg.accent || (_dark ? "#8b8eff" : "#5b5ef4");
  var SURF = _dark ? "rgba(255,255,255,.05)" : "#ffffff";
  var FIELD = _dark ? "rgba(255,255,255,.06)" : "#f6f6f8";
  var BORD = _dark ? "rgba(255,255,255,.16)" : "#e3e3ea";
  var MUT = _dark ? "rgba(235,235,245,.55)" : "#6b6b78";

  var css = document.createElement("style");
  css.textContent =
    "#sh-comments{max-width:720px;margin:48px auto 0;padding:24px 16px 8px;border-top:1px solid " + BORD + ";font-family:inherit;color:inherit;line-height:1.5}" +
    "#sh-comments h2{font-size:1.25em;margin:0 0 14px;color:inherit}" +
    ".shc-new textarea,.shc-reply textarea{width:100%;box-sizing:border-box;background:" + FIELD + ";color:inherit;border:1px solid " + BORD + ";border-radius:8px;padding:8px;font:inherit;resize:vertical}" +
    ".shc-name{background:" + FIELD + ";color:inherit;border:1px solid " + BORD + ";border-radius:8px;padding:6px 8px;font:inherit;margin-bottom:6px;width:220px;max-width:100%}" +
    ".shc-btn{background:" + A + ";color:#fff;border:0;border-radius:8px;padding:7px 14px;font:inherit;font-weight:600;cursor:pointer;margin-top:6px}" +
    ".shc-link{background:none;border:0;color:" + A + ";cursor:pointer;font:inherit;font-weight:600;font-size:.86em;padding:0}" +
    ".shc-c{display:flex;gap:10px;margin-top:16px}" +
    ".shc-vote{display:flex;flex-direction:column;align-items:center;min-width:26px;color:" + MUT + "}" +
    ".shc-up{background:none;border:0;cursor:pointer;font-size:16px;line-height:1;color:" + MUT + ";opacity:.65;padding:0}" +
    ".shc-up.on{color:" + A + ";opacity:1}.shc-score{font-weight:600;font-size:.86em;color:inherit}" +
    ".shc-body{flex:1;min-width:0}.shc-meta{color:" + MUT + ";font-size:.86em;margin-bottom:2px}" +
    ".shc-meta b{color:inherit}.shc-text{white-space:pre-wrap;overflow-wrap:anywhere}" +
    ".shc-kids{margin-left:8px;padding-left:12px;border-left:2px solid " + BORD + "}" +
    ".shc-reply{margin-top:8px;background:" + SURF + ";border-radius:8px}";
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
  function ago(ts) {
    var s = Math.max(1, (Date.now() - ts) / 1000), m = s / 60, h = m / 60, d = h / 24;
    if (d >= 1) return Math.floor(d) + "d"; if (h >= 1) return Math.floor(h) + "h";
    if (m >= 1) return Math.floor(m) + "m"; return Math.floor(s) + "s";
  }
  function childrenOf(pid) {
    return comments.filter(function (c) { return (c.parentId || null) === pid; })
      .sort(function (a, b) { return pid === null ? (score(b.id) - score(a.id)) || (a.ts - b.ts) : a.ts - b.ts; });
  }
  function node(c) {
    var row = el("div", "shc-c");
    var vote = el("div", "shc-vote");
    var up = el("button", "shc-up" + (voted[c.id] ? " on" : "")); up.textContent = "▲"; up.title = "upvote";
    up.onclick = function () { upvote(c.id); };
    var sc = el("div", "shc-score"); sc.textContent = score(c.id);
    vote.appendChild(up); vote.appendChild(sc);
    var body = el("div", "shc-body");
    var meta = el("div", "shc-meta"); var b = document.createElement("b"); b.textContent = c.author || "anon";
    meta.appendChild(b); meta.appendChild(document.createTextNode(" · " + ago(c.ts) + " ago"));
    var text = el("div", "shc-text"); text.textContent = c.body;
    var reply = el("button", "shc-link"); reply.textContent = "Reply"; reply.style.marginTop = "4px";
    var replyBox = el("div", "shc-reply"); replyBox.style.display = "none";
    reply.onclick = function () { replyBox.style.display = replyBox.style.display === "none" ? "block" : "none"; if (replyBox.style.display === "block") replyBox.querySelector("textarea").focus(); };
    buildComposer(replyBox, "Reply…", function (val, done) { postComment(c.id, val).then(function (ok) { if (ok) { replyBox.style.display = "none"; } done(); }); });
    body.appendChild(meta); body.appendChild(text); body.appendChild(reply); body.appendChild(replyBox);
    var kids = el("div", "shc-kids");
    childrenOf(c.id).forEach(function (k) { kids.appendChild(node(k)); });
    body.appendChild(kids);
    row.appendChild(vote); row.appendChild(body);
    return row;
  }
  function render() {
    mount.textContent = "";
    var h = document.createElement("h2"); h.textContent = "Discussion (" + comments.length + ")"; mount.appendChild(h);
    var top = el("div", "shc-new");
    buildComposer(top, "Add a comment…", function (val, done) { postComment(null, val).then(done); }, true);
    mount.appendChild(top);
    childrenOf(null).forEach(function (c) { mount.appendChild(node(c)); });
  }

  function buildComposer(container, placeholder, onSubmit, withName) {
    container.classList.add("shc-reply");
    if (withName && !author) { var n = document.createElement("input"); n.className = "shc-name"; n.placeholder = "your name (optional)"; container.appendChild(n); container.appendChild(document.createElement("br")); }
    var ta = document.createElement("textarea"); ta.rows = 3; ta.placeholder = placeholder; container.appendChild(ta);
    var btn = el("button", "shc-btn"); btn.textContent = "Post"; container.appendChild(btn);
    btn.onclick = function () {
      var val = ta.value.trim(); if (!val) return;
      var nameEl = container.querySelector(".shc-name");
      if (nameEl && nameEl.value.trim()) { author = nameEl.value.trim(); localStorage.setItem("sh_comments_author", author); }
      btn.disabled = true;
      onSubmit(val, function () { btn.disabled = false; ta.value = ""; });
    };
  }
  function el(tag, cls) { var n = document.createElement(tag); if (cls) n.className = cls; return n; }

  load();
  setInterval(poll, 10000);
})();
