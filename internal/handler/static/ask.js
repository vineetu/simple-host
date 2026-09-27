// "Ask about this page": sends the reader's question to POST /v1/ask and shows
// the answer as text. Rendered only when the server has the box turned on.
(function () {
  var box = document.querySelector('.sh-ask');
  if (!box) return;
  var form = box.querySelector('.sh-ask-form');
  var input = box.querySelector('.sh-ask-input');
  var send = box.querySelector('.sh-ask-send');
  var out = box.querySelector('.sh-ask-answer');
  var busy = false;
  // Links are kept only to simple-host.app pages; the server enforces the same.
  var link = /\[([^\]\n]+)\]\((https:\/\/simple-host\.app(?:\/[A-Za-z0-9\/_.#?=&-]*)?)\)/g;

  function show(text, state) {
    out.hidden = false;
    out.className = 'sh-ask-answer' + (state ? ' is-' + state : '');
    out.textContent = '';
    if (state) { out.textContent = text; return; }
    var last = 0, m;
    link.lastIndex = 0;
    while ((m = link.exec(text))) {
      out.appendChild(document.createTextNode(text.slice(last, m.index)));
      var a = document.createElement('a');
      a.href = m[2];
      a.textContent = m[1];
      out.appendChild(a);
      last = m.index + m[0].length;
    }
    out.appendChild(document.createTextNode(text.slice(last)));
  }

  function done() {
    busy = false;
    send.disabled = false;
    send.textContent = 'Ask';
    box.removeAttribute('aria-busy');
  }

  form.addEventListener('submit', function (e) {
    e.preventDefault();
    var q = input.value.trim();
    if (!q || busy) return;
    busy = true;
    send.disabled = true;
    send.textContent = 'Asking…';
    box.setAttribute('aria-busy', 'true');
    show('Looking through this page…', 'loading');
    fetch('/v1/ask', {
      method: 'POST',
      credentials: 'omit',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ question: q, page: box.getAttribute('data-page') })
    }).then(function (r) {
      return r.json().then(function (j) { return { ok: r.ok, status: r.status, j: j }; });
    }).then(function (res) {
      if (res.ok && res.j && res.j.answer) { show(res.j.answer); return; }
      if (res.status === 429 && res.j && res.j.code === 'daily_limit') {
        show("Couldn't answer right now. Questions are paused until tomorrow.", 'error');
      } else if (res.status === 429) {
        show("Couldn't answer right now. Give it a minute, then ask again.", 'error');
      } else {
        show("Couldn't answer right now.", 'error');
      }
    }).catch(function () {
      show("Couldn't answer right now.", 'error');
    }).then(done);
  });
})();
