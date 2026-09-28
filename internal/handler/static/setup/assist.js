// The setup helper's assistant: a button that opens a panel on /setup (a
// bottom sheet on phones, beside the form on wide screens). It answers
// questions about the settings of the product being set up, fills in the form
// from a plain request, cleans up choices, and helps with pasted error output
// (/setup#help opens it there). The page loads this script only where the
// server offers POST /v1/setup/assist.
//
// Each message sends the product, where the visitor is in the form, the
// non-secret choices (numbers, durations, switches, choices and limits, and
// the basic answers picked from lists; never hostnames, emails, IDs or
// secrets), the message and the last few turns. The answer streams as text;
// proposed changes arrive at the end, already checked by the server, and show
// as items to Apply or Ignore. Nothing changes until the visitor applies it,
// and applying goes through the form's own validation (window.shSetup, in
// setup.js). The conversation lives in this page only.
//
// Pasted error output is redacted here first (redact, below: keys, tokens,
// passwords, email addresses), shown to the person exactly as it will be
// sent, and sent only when they click "Send for help". The server redacts it
// again.
(function () {
  'use strict';
  var api = window.shSetup;
  if (!api) return;
  var MAX_TURNS = 4, MAX_ANSWER = 1500, TIMEOUT = 60000;
  var CLEAN_UP = 'Clean up my choices';
  var HELP = 'Help me fix this error.';
  // What is sent of a paste, in bytes (the server takes up to 8 KB): the
  // last part, where the error usually is.
  var PASTE_LIMIT = 8000;

  // <redact> The same rules, in the same order, as setupRedact in
  // internal/handler/setupredact.go; both are tested against
  // internal/handler/testdata/setup-redact-cases.json.
  // What a secret's name ends with (DB_PASSWORD, SMTP_PASS, GPG_PASSPHRASE,
  // SECRET_KEY_BASE, AWS_ACCESS_KEY_ID, sig).
  var NAME = '[A-Za-z0-9_.-]*(?:key|secret|token|pass|passwd|password|passphrase|pwd|dsn|salt|credentials?|cookie|sig|signature)(?:[_.-]?(?:base|b64|base64|id))?';
  var LINE_NAME = '[A-Za-z0-9_.-]*(?:key|secret|token|pass|passwd|password|passphrase|pwd|dsn|salt|hash|credentials?|cookie|sig|signature)(?:[_.-]?(?:base|b64|base64|id))?';
  var REDACT = [
    [/-----BEGIN ([A-Z0-9 ]+)-----[\s\S]*?(?:-----END [A-Z0-9 ]+-----|$)/g, '[redacted $1]'],
    [/([A-Za-z][A-Za-z0-9+.-]*:\/\/)([^\t\n\f\r :\/@]+):([^\t\n\f\r '"<>?#]+)@/g, '$1$2:[redacted]@'],
    [/\b((?:proxy-)?authorization["']?[ \t]*[:=][ \t]*["']?)(?:([A-Za-z]+)([ \t]+))?[^\t\n\f\r '",]+/gi, '$1$2$3[redacted]'],
    [/\b(set-cookie|cookie|x-[A-Za-z0-9-]*(?:key|token|secret|signature|auth|session)[A-Za-z0-9-]*)(["']?[ \t]*:[ \t]*["']?)[^\t\n\f\r '"][^\n'"]*/gi, '$1$2[redacted]'],
    [/\b(bearer|basic)([ \t]+)[A-Za-z0-9._~+\/=-]{8,}/gi, '$1$2[redacted]'],
    [/(^|[ \t])(-[A-Za-z]*u|--user)([ \t]*|=)(?:(['"])([^\n:'"]+):[^\n'"]*|([^\t\n\f\r :'"]+):[^\t\n\f\r '"]+)/gm, '$1$2$3$4$5$6:[redacted]'],
    [/(^|[ \t])(--pass(?:word|wd)?)([ \t]+|=)(["']?)[^\t\n\f\r '"]+/g, '$1$2$3$4[redacted]'],
    [/\b(mysql(?:dump|admin)?|mariadb(?:-dump|-admin)?)\b([^\n]*?[ \t])-p[^\t\n\f\r '"]+/g, '$1$2-p[redacted]'],
    [new RegExp('^([ \\t]*(?:[A-Za-z0-9_.-]+[ \\t]+\\|[ \\t]*)?(?:export[ \\t]+)?["\']?' + LINE_NAME + '["\']?[ \\t]*[:=][ \\t]*)[^\\t\\n\\f\\r ].*$', 'gim'), '$1[redacted]'],
    [new RegExp('(["\']' + NAME + '["\'][ \\t]*:[ \\t]*)(?:"(?:[^"\\\\\\n]|\\\\.)*"|\'[^\'\\n]*\'|[^\\t\\n\\f\\r ,;}]+)', 'gi'), '$1[redacted]'],
    [new RegExp('\\b(' + NAME + ')=(?:"[^"\\n]*"|\'[^\'\\n]*\'|[^\\t\\n\\f\\r &;\'",]+)', 'gi'), '$1=[redacted]'],
    [/\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{2,}/g, '[redacted token]'],
    [/\b(?:sh(?:k|at|rt|ac|c|cs|int|_admin)_|sk-|sk_|pk_|rk_|re_|ghp_|gho_|ghu_|ghs_|ghr_|github_pat_|glpat-|xai-|xox[abpors]-|ucat_)[A-Za-z0-9_-]{12,}/g, '[redacted key]'],
    [/\b(?:AKIA|ASIA)[0-9A-Z]{16}\b/g, '[redacted key]'],
    [/[A-Za-z0-9._%+\u00a1-\u1fff\u2070-\u2fff\u3001-\ud7ff\uf900-\uffef-]+[@\uff20][A-Za-z0-9\u00a1-\u1fff\u2070-\u2fff\u3001-\ud7ff\uf900-\uffef-]+(?:\.[A-Za-z0-9\u00a1-\u1fff\u2070-\u2fff\u3001-\ud7ff\uf900-\uffef-]+)+/g, '[email]'],
    [/\b[0-9a-fA-F]{32,}\b/g, '[redacted]'],
    // Long base64-like strings with digits, lower and upper case: secrets,
    // not paths or names.
    [/[A-Za-z0-9+\/_=-]{32,}/g, function (m) { return /[0-9]/.test(m) && /[a-z]/.test(m) && /[A-Z]/.test(m) ? '[redacted]' : m; }]
  ];
  function redact(s) {
    s = String(s).replace(/\r\n?/g, '\n').replace(/[\u2028\u2029]/g, '\n')
      .replace(/\u001b\[[0-9;?]*[ -\/]*[@-~]/g, '').replace(/\u001b\][^\u0007\u001b\n]*(?:\u0007|\u001b\\)?/g, '')
      .replace(/[\u0000-\u0008\u000b-\u001f\u007f-\u009f\u200b-\u200d\u2060\ufeff]/g, '');
    REDACT.forEach(function (r) { s = s.replace(r[0], r[1]); });
    return s;
  }
  // </redact>
  function hidden(s) { return (s.match(/\[redacted[^\]]*\]|\[email\]/g) || []).length; }
  function bytes(s) { return typeof TextEncoder === 'function' ? new TextEncoder().encode(s).length : s.length * 3; }
  // lastPart keeps the end of a long paste, from a line start.
  function lastPart(s) {
    if (bytes(s) <= PASTE_LIMIT) return { text: s, cut: false };
    var t = s.slice(-PASTE_LIMIT);
    while (bytes(t) > PASTE_LIMIT) t = t.slice(Math.ceil(t.length / 8));
    var nl = t.indexOf('\n');
    if (nl >= 0 && nl < 300) t = t.slice(nl + 1);
    return { text: t, cut: true };
  }
  var PRODUCTS = {
    small: { name: 'a small box', examples: ['Set this up for a weekend hackathon with 150 people signing in by emailed code', 'What does KEEP_VERSIONS do?'] },
    ent: { name: 'Simple Host Enterprise', examples: ['Set this up for a 200-person company with Microsoft sign-in and stricter security', 'How long do people stay signed in?'] }
  };
  // Answered turns per product: {q, a, changes, basics}. Only this page
  // keeps them.
  var convos = { small: [], ent: [] };
  var busy = false, ctrl = null, shownFor = '';

  function el(tag, attrs, kids) {
    var n = document.createElement(tag);
    Object.keys(attrs || {}).forEach(function (k) {
      var v = attrs[k];
      if (v == null || v === false) return;
      if (k === 'text') n.textContent = v;
      else if (k.slice(0, 2) === 'on') n.addEventListener(k.slice(2), v);
      else n.setAttribute(k, v === true ? '' : v);
    });
    (kids || []).forEach(function (c) { if (c != null && c !== false) n.appendChild(typeof c === 'string' ? document.createTextNode(c) : c); });
    return n;
  }
  var ICON = '<svg aria-hidden="true" viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a8 8 0 0 1-11.6 7.1L4 20.5l1.4-4.9A8 8 0 1 1 21 12z"/><path d="M8.5 12h.01M12 12h.01M15.5 12h.01"/></svg>';
  var CLOSE = '<svg aria-hidden="true" viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>';

  // ── The panel ──
  var fab = el('button', { class: 'sh-ask-fab', type: 'button', 'aria-label': 'Open the setup assistant', 'aria-haspopup': 'dialog', 'aria-expanded': 'false', 'aria-controls': 'sh-assist-panel' });
  fab.innerHTML = ICON;
  fab.appendChild(el('span', { text: 'Assistant' }));
  var close = el('button', { class: 'sh-ask-close', type: 'button', 'aria-label': 'Close the assistant' });
  close.innerHTML = CLOSE;
  var sub = el('p', { class: 'sh-assist-for', id: 'sh-assist-for' });
  var logEl = el('div', { class: 'sh-ask-log', 'aria-live': 'polite', 'aria-relevant': 'additions text' });
  var intro = el('div', { class: 'sh-assist-intro' });
  var cleanBtn = el('button', { class: 'sh-assist-quick', type: 'button', text: CLEAN_UP, title: 'Explain odd values, flag conflicts and offer resets to the default' });
  var pasteBtn = el('button', { class: 'sh-assist-quick', type: 'button', text: 'Paste an error', 'aria-expanded': 'false', 'aria-controls': 'sh-assist-paste' });
  // Pasting an error: paste, review exactly what will be sent, then send.
  var pasteIn = el('textarea', { id: 'sh-assist-paste-in', rows: '6', spellcheck: 'false', autocomplete: 'off', 'aria-describedby': 'sh-assist-paste-hint' });
  var pasteBox = el('div', { class: 'sh-assist-paste', id: 'sh-assist-paste', hidden: true });
  var input = el('input', { class: 'sh-ask-input', id: 'sh-assist-q', type: 'text', maxlength: '500', autocomplete: 'off', enterkeyhint: 'send', placeholder: 'Ask, or describe your setup' });
  var send = el('button', { class: 'sh-ask-send', type: 'submit', text: 'Send' });
  var form = el('form', { class: 'sh-ask-form', novalidate: true }, [
    el('label', { class: 'sh-ask-label', for: 'sh-assist-q', text: 'Your question or request' }),
    el('div', { class: 'sh-ask-row' }, [input, send])
  ]);
  var panel = el('div', { class: 'sh-ask-panel', id: 'sh-assist-panel', role: 'dialog', 'aria-modal': 'false', 'aria-labelledby': 'sh-assist-title', 'aria-describedby': 'sh-assist-for', hidden: true }, [
    el('div', { class: 'sh-ask-head' }, [el('div', null, [el('p', { class: 'sh-ask-title', id: 'sh-assist-title', text: 'Setup assistant' }), sub]), close]),
    logEl, intro, pasteBox,
    el('div', { class: 'sh-assist-tools' }, [cleanBtn, pasteBtn]),
    form,
    el('p', { class: 'sh-ask-note', text: 'Sends your message, recent turns and your choices here that are numbers, switches, limits or picked from a list, never hostnames, emails or secrets you typed; a pasted error only after you review it. Nothing is kept. Written by AI: nothing changes until you apply it.' })
  ]);
  var box = el('div', { class: 'sh-ask sh-assist' }, [fab, panel]);
  document.body.appendChild(box);

  function product() { return api.product(); }

  // Draws the conversation of the product chosen now (the visitor can switch
  // products on the first step).
  function redraw() {
    var p = product();
    sub.textContent = 'For ' + PRODUCTS[p].name;
    if (shownFor === p) return;
    shownFor = p;
    logEl.textContent = '';
    convos[p].forEach(function (t) { drawTurn(t); });
    drawIntro();
  }
  function drawIntro() {
    var p = product();
    intro.textContent = '';
    intro.hidden = convos[p].length > 0 || !pasteBox.hidden;
    if (intro.hidden) return;
    intro.appendChild(el('p', { text: 'Ask about any setting, or describe what you are setting up and I will suggest how to fill in the form. You apply each change yourself.' }));
    PRODUCTS[p].examples.forEach(function (x) {
      intro.appendChild(el('button', { class: 'sh-assist-example', type: 'button', text: x, onclick: function () { ask(x); } }));
    });
  }

  function nearBottom() { return logEl.scrollHeight - logEl.scrollTop - logEl.clientHeight < 48; }
  function toBottom() { logEl.scrollTop = logEl.scrollHeight; }

  function open() {
    redraw();
    panel.hidden = false;
    fab.hidden = true;
    fab.setAttribute('aria-expanded', 'true');
    document.body.classList.add('sh-assist-open');
    toBottom();
    input.focus();
  }
  function shut() {
    panel.hidden = true;
    fab.hidden = false;
    fab.setAttribute('aria-expanded', 'false');
    document.body.classList.remove('sh-assist-open');
    fab.focus();
  }
  fab.addEventListener('click', open);
  close.addEventListener('click', shut);
  panel.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' || e.key === 'Esc') { e.preventDefault(); shut(); }
  });
  // Switching products on the first step switches conversations.
  document.addEventListener('change', function (e) {
    if (e.target && e.target.name === 'product' && !panel.hidden) setTimeout(redraw, 0);
  });
  window.addEventListener('pagehide', function () { if (ctrl) ctrl.abort(); });

  // ── Pasted output: paste, review, send ──
  var review = null; // { text, cut } once the person asks to review
  function drawPaste() {
    pasteBox.textContent = '';
    if (!review) {
      pasteBox.appendChild(el('label', { for: 'sh-assist-paste-in', text: 'Paste the error output' }));
      pasteBox.appendChild(el('p', { class: 'sh-assist-hint', id: 'sh-assist-paste-hint', text: 'From the installer, your AI agent, kubectl, or the docker, Caddy or server log. We remove the keys, tokens, passwords and email addresses we recognise; check what will be sent before you send it.' }));
      pasteBox.appendChild(pasteIn);
      pasteBox.appendChild(el('div', { class: 'sh-assist-acts' }, [
        el('button', { class: 'sh-assist-apply', type: 'button', text: 'Review what will be sent', onclick: function () {
          if (!pasteIn.value.trim()) { pasteIn.focus(); return; }
          review = lastPart(redact(pasteIn.value).trim());
          drawPaste();
          pasteBox.querySelector('pre').focus();
        } }),
        el('button', { type: 'button', text: 'Cancel', onclick: function () { showPaste(false); input.focus(); } })
      ]));
      return;
    }
    var n = hidden(review.text);
    pasteBox.appendChild(el('p', { class: 'sh-assist-hint', id: 'sh-assist-review-hint', text: 'This is exactly what will be sent' + (n ? ', with ' + n + (n > 1 ? ' things' : ' thing') + ' hidden' : '') + '. Hostnames and addresses stay: they help find the cause.' + (review.cut ? ' Only the last part of a long paste is sent.' : '') }));
    pasteBox.appendChild(el('pre', { class: 'sh-assist-sent', tabindex: '0', 'aria-label': 'What will be sent', 'aria-describedby': 'sh-assist-review-hint', text: review.text }));
    pasteBox.appendChild(el('div', { class: 'sh-assist-acts' }, [
      el('button', { class: 'sh-assist-apply', type: 'button', text: 'Send for help', onclick: function () {
        var text = review.text;
        showPaste(false);
        ask(input.value.trim() || HELP, text);
      } }),
      el('button', { type: 'button', text: 'Edit', onclick: function () { review = null; drawPaste(); pasteIn.focus(); } })
    ]));
  }
  function showPaste(on, text) {
    review = null;
    if (text != null) pasteIn.value = text;
    if (!on) pasteIn.value = '';
    pasteBox.hidden = !on;
    pasteBtn.setAttribute('aria-expanded', on ? 'true' : 'false');
    if (on) { intro.hidden = true; drawPaste(); } else drawIntro();
  }
  pasteBtn.addEventListener('click', function () {
    if (pasteBox.hidden) { showPaste(true); pasteIn.focus(); } else { showPaste(false); input.focus(); }
  });
  // Output pasted into the one-line field goes to the paste box instead,
  // where its lines survive and it is reviewed before it is sent.
  input.addEventListener('paste', function (e) {
    var text = e.clipboardData && e.clipboardData.getData('text');
    if (!text || (text.indexOf('\n') < 0 && text.length < 200)) return;
    e.preventDefault();
    showPaste(true, text);
    pasteIn.focus();
  });

  // ── A turn: the question, the answer, the proposed changes ──
  function drawTurn(t) {
    var turn = el('div', { class: 'sh-ask-turn' }, [el('p', { class: 'sh-ask-q', text: t.q })]);
    if (t.pasted) turn.appendChild(el('details', { class: 'sh-assist-pasted' }, [el('summary', { text: 'Output sent (redacted)' }), el('pre', { text: t.pasted })]));
    t.aEl = el('p', { class: 'sh-ask-a' });
    turn.appendChild(t.aEl);
    if (t.error) { t.aEl.className = 'sh-ask-a is-error'; t.aEl.textContent = t.error; }
    else if (t.a != null) { t.aEl.textContent = t.a; drawChanges(t, turn); }
    logEl.appendChild(turn);
    return turn;
  }

  // items turns the server's changes and basics into what the panel shows.
  function items(t) {
    if (t.items) return t.items;
    t.items = [];
    (t.changes || []).forEach(function (c) { t.items.push({ kind: 'setting', name: c.setting, value: c.value, why: c.why || '', state: 'new' }); });
    Object.keys(t.basics || {}).forEach(function (k) { t.items.unshift({ kind: 'basic', name: k, value: t.basics[k], why: '', state: 'new' }); });
    return t.items;
  }

  // look says what applying an item would do now, or null when this page
  // cannot apply it (the server checks first, so this is rare).
  function look(it) {
    if (it.kind === 'basic') {
      var b = api.describeBasic(it.name, it.value);
      return b && { what: b.label + ': ' + b.valueLabel, now: 'Now: ' + b.currentLabel, same: b.same, error: '' };
    }
    var d = api.describe(it.name, it.value);
    return d && { what: 'Set ' + it.name + ' to ' + it.value,
      now: 'Now: ' + (d.current === '' ? '(empty)' : d.current) + (d.current === d.def ? ' (default)' : '') + (d.area ? ' · ' + d.area : ''),
      same: d.current === it.value, error: d.error };
  }

  function applyItem(it) {
    var err = it.kind === 'basic' ? api.applyBasic(it.name, it.value) : api.apply(it.name, it.value);
    it.state = err ? 'error' : 'applied';
    it.error = err;
    return !err;
  }

  function drawChanges(t, turn) {
    var list = items(t);
    if (!list.length) return;
    var group = el('div', { class: 'sh-assist-changes', role: 'group', 'aria-label': 'Suggested changes' });
    var ul = el('ul');
    var all = el('button', { class: 'sh-assist-all', type: 'button' });
    group.appendChild(ul);
    group.appendChild(all);
    var draw = function () {
      ul.textContent = '';
      var pending = 0;
      list.forEach(function (it) {
        var l = look(it);
        if (it.state === 'new' && (!l || l.same)) it.state = l ? 'same' : 'unavailable';
        if (it.state === 'new' && l.error) { it.state = 'error'; it.error = l.error; }
        var acts = el('div', { class: 'sh-assist-acts' });
        if (it.state === 'new') {
          pending++;
          acts.appendChild(el('button', { class: 'sh-assist-apply', type: 'button', text: 'Apply', 'aria-label': 'Apply: ' + l.what, onclick: function () {
            applyItem(it);
            api.refresh([it.name]);
            draw();
            focusNext(ul);
          } }));
          acts.appendChild(el('button', { class: 'sh-assist-ignore', type: 'button', text: 'Ignore', 'aria-label': 'Ignore: ' + l.what, onclick: function () { it.state = 'ignored'; draw(); focusNext(ul); } }));
        } else {
          var said = { applied: 'Applied', ignored: 'Ignored', same: 'Already set', unavailable: 'Not a setting this page writes', error: 'Not applied: ' + (it.error || '') }[it.state];
          acts.appendChild(el('span', { class: 'sh-assist-done is-' + it.state, text: said }));
        }
        ul.appendChild(el('li', { class: 'sh-assist-item' }, [
          el('p', { class: 'sh-assist-what' }, [el('b', { text: l ? l.what : it.name + ' = ' + it.value }), it.why ? ' — ' + it.why : null]),
          l && it.state === 'new' ? el('p', { class: 'sh-assist-now', text: l.now }) : null,
          acts
        ]));
      });
      all.hidden = pending < 2;
      all.textContent = 'Apply all (' + pending + ')';
    };
    all.addEventListener('click', function () {
      var names = [];
      list.forEach(function (it) { if (it.state === 'new') { var l = look(it); if (l && !l.same && !l.error && applyItem(it)) names.push(it.name); } });
      api.refresh(names);
      draw();
      input.focus();
    });
    draw();
    turn.appendChild(group);
  }
  // After a decision, focus moves to the next undecided Apply, or the field.
  function focusNext(ul) {
    var next = ul.querySelector('.sh-assist-apply');
    (next || input).focus();
  }

  function failText(status, j) {
    if (status === 404) return 'The assistant is not available on this server.';
    if (status === 429 && j && j.code === 'daily_limit') return "Couldn't answer right now. The assistant is paused until tomorrow.";
    if (status === 429 || (status === 503 && j && j.code === 'busy')) return "Couldn't answer right now. Give it a minute, then ask again.";
    return "Couldn't answer right now.";
  }

  // Reads the event stream: {"t"} pieces as plain text, then
  // {"done","answer","changes","basics"} or {"error"}.
  function readStream(res, t) {
    var reader = res.body.getReader(), dec = new TextDecoder();
    var buf = '', shown = '', final = null, failed = false;
    function handle(ev) {
      var data = '';
      ev.split('\n').forEach(function (l) { if (l.indexOf('data:') === 0) data += l.slice(5).trim(); });
      if (!data) return;
      var j;
      try { j = JSON.parse(data); } catch (e) { return; }
      if (j.done) { final = j; return; }
      if (j.error) { failed = true; return; }
      if (typeof j.t === 'string') {
        var stick = nearBottom();
        if (!shown) t.aEl.className = 'sh-ask-a';
        shown += j.t;
        t.aEl.textContent = shown;
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
        if (failed || !final || !final.answer) throw new Error('stream');
        return final;
      });
    }
    return pump();
  }

  // history is the product's last turns, each answer with what it proposed,
  // so "apply the stricter one" or "why?" has its context.
  function history(p) {
    return convos[p].filter(function (t) { return t.a != null; }).slice(-MAX_TURNS).map(function (t) {
      var said = items(t).map(function (it) { return it.name + '=' + it.value; });
      var a = t.a + (said.length ? '\n(Suggested: ' + said.join(', ') + ')' : '');
      return { q: t.q, a: a.slice(0, MAX_ANSWER) };
    });
  }

  function setBusy(on) {
    busy = on;
    send.disabled = on;
    cleanBtn.disabled = on;
    pasteBtn.disabled = on;
    Array.prototype.forEach.call(intro.querySelectorAll('button'), function (b) { b.disabled = on; });
    if (on) panel.setAttribute('aria-busy', 'true'); else panel.removeAttribute('aria-busy');
  }

  // ask sends a message, with pasted output already redacted and reviewed.
  // A typed message is redacted too, and shows as sent.
  function ask(q, pasted) {
    q = redact((q || '').trim()).replace(/\s+/g, ' ').slice(0, 500);
    if (!q || busy) return;
    setBusy(true);
    redraw();
    var p = product(), hist = history(p);
    var t = { q: q, pasted: pasted || '' };
    convos[p].push(t);
    if (convos[p].length > 20) convos[p].shift();
    drawIntro();
    var turn = drawTurn(t);
    t.aEl.className = 'sh-ask-a is-loading';
    t.aEl.textContent = 'Thinking…';
    input.value = '';
    toBottom();
    ctrl = typeof AbortController === 'function' ? new AbortController() : null;
    var timer = setTimeout(function () { if (ctrl) ctrl.abort(); }, TIMEOUT);
    api.ready().then(function () {
      var body = api.context();
      body.message = q;
      if (t.pasted) body.pasted = t.pasted;
      body.history = hist;
      return fetch('/v1/setup/assist', {
        method: 'POST', credentials: 'omit',
        headers: { 'Content-Type': 'application/json', 'Accept': 'text/event-stream' },
        body: JSON.stringify(body), signal: ctrl ? ctrl.signal : undefined
      });
    }).then(function (res) {
      var ct = res.headers.get('Content-Type') || '';
      if (res.ok && ct.indexOf('text/event-stream') === 0 && res.body && res.body.getReader) return readStream(res, t);
      return res.json().catch(function () { return null; }).then(function (j) {
        if (res.ok && j && j.answer) return j;
        var err = new Error('status');
        err.msg = failText(res.status, j);
        throw err;
      });
    }).then(function (j) {
      var stick = nearBottom();
      t.a = j.answer;
      t.changes = Array.isArray(j.changes) ? j.changes : [];
      t.basics = j.basics && typeof j.basics === 'object' ? j.basics : {};
      t.aEl.className = 'sh-ask-a';
      t.aEl.textContent = t.a;
      drawChanges(t, turn);
      if (stick) toBottom();
    }).catch(function (err) {
      t.error = (err && err.msg) || "Couldn't answer right now.";
      t.aEl.className = 'sh-ask-a is-error';
      t.aEl.textContent = t.error;
    }).then(function () {
      clearTimeout(timer);
      ctrl = null;
      setBusy(false);
      if (!panel.hidden && document.activeElement === document.body) input.focus();
    });
  }

  form.addEventListener('submit', function (e) { e.preventDefault(); ask(input.value); });
  cleanBtn.addEventListener('click', function () { ask(CLEAN_UP); });
  // /setup?product=…#help (the link in the handoff for your AI agent) opens
  // the assistant ready for a pasted error.
  function helpLink() {
    if (location.hash !== '#help') return;
    if (panel.hidden) open();
    showPaste(true);
    pasteIn.focus();
  }
  window.addEventListener('hashchange', helpLink);
  redraw();
  helpLink();
  // Lets the files step offer the #help line only where the assistant is.
  window.shSetupAssist = true;
})();
