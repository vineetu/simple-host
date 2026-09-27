// The "Ask" assistant: a floating button that opens a small panel. The page
// names the assistant (data-assistant) and itself (data-page); the question
// goes to POST /v1/ask, and the answer streams in as plain text, then is
// replaced by the server's cleaned final answer. The last few turns go with a
// follow-up so "tell me more" works, and are kept in this tab's sessionStorage
// so the conversation follows the reader across the assistant's pages; it ends
// when the tab closes. Rendered only when the server has it on.
(function () {
  var box = document.querySelector('.sh-ask');
  if (!box) return;
  var fab = box.querySelector('.sh-ask-fab');
  var panel = box.querySelector('.sh-ask-panel');
  var close = box.querySelector('.sh-ask-close');
  var logEl = box.querySelector('.sh-ask-log');
  var form = box.querySelector('.sh-ask-form');
  var input = box.querySelector('.sh-ask-input');
  var send = box.querySelector('.sh-ask-send');
  var assistant = box.getAttribute('data-assistant');
  var page = box.getAttribute('data-page') || '';
  var MAX_TURNS = 4, MAX_ANSWER = 1500;
  var busy = false, ctrl = null;

  // Answered {q, a}, the last MAX_TURNS, shared by this assistant's pages in
  // this tab. Storage can be missing or refuse (private windows, blocked
  // site data); the panel then keeps the conversation of this page only.
  var KEY = 'sh-ask:' + assistant;
  function load() {
    try {
      var v = JSON.parse(sessionStorage.getItem(KEY) || '[]');
      if (!Array.isArray(v)) return [];
      return v.filter(function (t) { return t && typeof t.q === 'string' && typeof t.a === 'string'; }).slice(-MAX_TURNS);
    } catch (e) { return []; }
  }
  function save() {
    try { sessionStorage.setItem(KEY, JSON.stringify(turns)); } catch (e) {}
  }
  var turns = load();

  // Markdown links become links only when they point at this site's own pages
  // (the server already drops any other); everything else stays text.
  var link = /\[([^\]\n]+)\]\((https:\/\/[^)\s]+)\)/g;
  var origin = location.origin;
  function own(u) { return u === origin || u.indexOf(origin + '/') === 0; }

  function render(el, text) {
    el.className = 'sh-ask-a';
    el.textContent = '';
    var last = 0, m;
    link.lastIndex = 0;
    while ((m = link.exec(text))) {
      el.appendChild(document.createTextNode(text.slice(last, m.index)));
      last = m.index + m[0].length;
      if (!own(m[2])) { el.appendChild(document.createTextNode(m[1])); continue; }
      var a = document.createElement('a');
      a.href = m[2];
      a.textContent = m[1];
      el.appendChild(a);
    }
    el.appendChild(document.createTextNode(text.slice(last)));
  }

  function state(el, text, kind) {
    el.className = 'sh-ask-a is-' + kind;
    el.textContent = text;
  }

  function addTurn(q) {
    var turn = document.createElement('div');
    turn.className = 'sh-ask-turn';
    var qEl = document.createElement('p');
    qEl.className = 'sh-ask-q';
    qEl.textContent = q;
    var aEl = document.createElement('p');
    turn.appendChild(qEl);
    turn.appendChild(aEl);
    logEl.appendChild(turn);
    return aEl;
  }
  // The conversation so far, from another of the assistant's pages.
  turns.forEach(function (t) { render(addTurn(t.q), t.a); });

  function nearBottom() { return logEl.scrollHeight - logEl.scrollTop - logEl.clientHeight < 48; }
  function toBottom() { logEl.scrollTop = logEl.scrollHeight; }

  function open() {
    panel.hidden = false;
    fab.hidden = true;
    fab.setAttribute('aria-expanded', 'true');
    toBottom();
    input.focus();
  }
  function shut() {
    panel.hidden = true;
    fab.hidden = false;
    fab.setAttribute('aria-expanded', 'false');
    fab.focus();
  }
  fab.addEventListener('click', open);
  close.addEventListener('click', shut);
  panel.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' || e.key === 'Esc') { e.preventDefault(); shut(); }
  });
  // Leaving the page stops an answer still being written.
  window.addEventListener('pagehide', function () { if (ctrl) ctrl.abort(); });

  function failText(status, j) {
    if (status === 429 && j && j.code === 'daily_limit') return "Couldn't answer right now. Questions are paused until tomorrow.";
    if (status === 429 || (status === 503 && j && j.code === 'busy')) return "Couldn't answer right now. Give it a minute, then ask again.";
    return "Couldn't answer right now.";
  }

  // Reads the event stream: {"t"} pieces as plain text, then {"done","answer"}
  // or {"error"}. Resolves with the final answer, or rejects.
  function readStream(res, el) {
    var reader = res.body.getReader();
    var dec = new TextDecoder();
    var buf = '', shown = '', final = null, failed = false;
    function handle(ev) {
      var data = '';
      ev.split('\n').forEach(function (l) { if (l.indexOf('data:') === 0) data += l.slice(5).trim(); });
      if (!data) return;
      var j;
      try { j = JSON.parse(data); } catch (e) { return; }
      if (j.done) { final = j.answer || ''; return; }
      if (j.error) { failed = true; return; }
      if (typeof j.t === 'string') {
        var stick = nearBottom();
        if (!shown) el.className = 'sh-ask-a';
        shown += j.t;
        el.textContent = shown;
        if (stick) toBottom();
      }
    }
    function pump() {
      return reader.read().then(function (r) {
        if (!r.done) {
          buf += dec.decode(r.value, { stream: true });
          var i;
          while ((i = buf.indexOf('\n\n')) >= 0) { handle(buf.slice(0, i)); buf = buf.slice(i + 2); }
          return pump();
        }
        if (buf.trim()) handle(buf);
        if (failed || final === null || !final) throw new Error('stream');
        return final;
      });
    }
    return pump();
  }

  form.addEventListener('submit', function (e) {
    e.preventDefault();
    var q = input.value.trim();
    if (!q || busy) return;
    busy = true;
    send.disabled = true;
    panel.setAttribute('aria-busy', 'true');

    var aEl = addTurn(q);
    state(aEl, 'Looking through the pages…', 'loading');
    input.value = '';
    toBottom();

    var history = turns.slice(-MAX_TURNS).map(function (t) { return { q: t.q, a: t.a.slice(0, MAX_ANSWER) }; });
    ctrl = typeof AbortController === 'function' ? new AbortController() : null;
    fetch('/v1/ask', {
      method: 'POST',
      credentials: 'omit',
      headers: { 'Content-Type': 'application/json', 'Accept': 'text/event-stream' },
      body: JSON.stringify({ assistant: assistant, page: page, question: q, history: history }),
      signal: ctrl ? ctrl.signal : undefined
    }).then(function (res) {
      var ct = res.headers.get('Content-Type') || '';
      if (res.ok && ct.indexOf('text/event-stream') === 0 && res.body && res.body.getReader) {
        return readStream(res, aEl);
      }
      return res.json().then(function (j) {
        if (res.ok && j && j.answer) return j.answer;
        var err = new Error('status');
        err.msg = failText(res.status, j);
        throw err;
      });
    }).then(function (answer) {
      var stick = nearBottom();
      render(aEl, answer);
      turns.push({ q: q, a: answer });
      if (turns.length > MAX_TURNS) turns.shift();
      save();
      if (stick) toBottom();
    }).catch(function (err) {
      state(aEl, (err && err.msg) || "Couldn't answer right now.", 'error');
    }).then(function () {
      busy = false;
      ctrl = null;
      send.disabled = false;
      panel.removeAttribute('aria-busy');
    });
  });
})();
