import * as api from './api.js'
import * as ui from './ui.js'
import { mount } from './shell.js'
import { resolveRoute, isAppRoute } from './nav.js'
const NEXT_KEY = 'sh-hack-next'
const apiKey = api.apiKey
const $ = id => document.getElementById(id)
let signInCard = null, joinReauth = false
const setLive = msg => ui.toast(msg)
  function safeNext(n) {
    if (typeof n !== 'string' || !n) return '';
    try {
      var u = new URL(n, location.origin);
      if (u.origin !== location.origin) return '';
      ['from', 'utm_source', 'utm_medium', 'utm_campaign', 'utm_term', 'utm_content', 'referrer'].forEach(function (key) { u.searchParams.delete(key); });
      var path = u.pathname + u.search + u.hash;
      var decoded;
      try { decoded = decodeURIComponent(path); } catch (e) { return ''; }
      if (new URL(decoded, location.origin).origin !== location.origin) return '';
      return path;
    } catch (e) { return ''; }
  }
  function saveNext(path) {
    try { sessionStorage.setItem(NEXT_KEY, path); } catch (e) {}
    try { localStorage.setItem(NEXT_KEY, JSON.stringify({ path: path, exp: Date.now() + 30 * 60 * 1000 })); } catch (e) {}
  }
  function storedNext() {
    var n = '';
    try { n = sessionStorage.getItem(NEXT_KEY) || ''; } catch (e) {}
    var p = safeNext(n);
    if (p) return p;
    try {
      var raw = localStorage.getItem(NEXT_KEY);
      if (!raw) return '';
      var o = JSON.parse(raw);
      if (!o || !(o.exp > Date.now())) { localStorage.removeItem(NEXT_KEY); return ''; }
      return safeNext(o.path || '');
    } catch (e) { return ''; }
  }
  function readNext() {
    var params = new URLSearchParams(location.search);
    var n = safeNext(params.get('next') || '');
    if (!n) n = storedNext();
    if (!n) n = '/events';
    saveNext(n);
    return n;
  }
  function consumeNext() {
    var n = readNext();
    try { sessionStorage.removeItem(NEXT_KEY); } catch (e) {}
    try { localStorage.removeItem(NEXT_KEY); } catch (e) {}
    return n;
  }
  function goSignIn() {
    // Already here: a rejected key on the sign-in page must not reload it
    // (each reload wrapped next in another /signin and the URL grew to a 414).
    if (location.pathname === '/signin') return;
    var n = safeNext(location.pathname + location.search + location.hash) || '/events';
    saveNext(n);
    location.assign(window.SHArrival.url('/signin?next=' + encodeURIComponent(n)));
  }
  function signedOut() {
    try { localStorage.removeItem('apiKey'); } catch (e) {}
    if (window.shAuthRender) window.shAuthRender();
    // A rejected key on a join link stays on that link. The key is still removed.
    var here = location.pathname.replace(/\/+$/, '') || '/';
    if (joinPathKind(here)) {
      if (joinReauth) return;
      joinReauth = true;
      initialData = null;
      Promise.resolve(route()).finally(function () { joinReauth = false; });
      return;
    }
    goSignIn();
  }
  const EMAIL_NONCE = 'sh-email-nonce', GOOGLE_NONCE = 'sh-google-nonce', OIDC_NONCE = 'sh-oidc-nonce';
  function b64url(bytes) {
    let s = '';
    bytes.forEach(b => { s += String.fromCharCode(b); });
    return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }
  function newNonce() { const r = new Uint8Array(32); crypto.getRandomValues(r); return b64url(r); }
  async function nonceHash(n) { return b64url(new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(n)))); }
  async function emailNonceHash() {
    const n = newNonce();
    try { localStorage.setItem(EMAIL_NONCE, JSON.stringify({ n, exp: Date.now() + 15 * 60 * 1000 })); } catch (e) { return ''; }
    return nonceHash(n);
  }
  // The nonce (email, Google or company account) that hashes to cn, or ''
  // — and either way the stored nonces are single-use.
  async function takeNonceFor(cn) {
    const candidates = [];
    try {
      const g = sessionStorage.getItem(GOOGLE_NONCE);
      if (g) candidates.push(g);
      const o = sessionStorage.getItem(OIDC_NONCE);
      if (o) candidates.push(o);
      const e = JSON.parse(localStorage.getItem(EMAIL_NONCE) || 'null');
      if (e && e.n && e.exp > Date.now()) candidates.push(e.n);
    } catch (err) {}
    if (!cn) return '';
    for (const n of candidates) {
      if ((await nonceHash(n)) === cn) {
        try { sessionStorage.removeItem(GOOGLE_NONCE); sessionStorage.removeItem(OIDC_NONCE); } catch (err) {}
        try { const e = JSON.parse(localStorage.getItem(EMAIL_NONCE) || 'null'); if (e && e.n === n) localStorage.removeItem(EMAIL_NONCE); } catch (err) {}
        return n;
      }
    }
    return '';
  }
  // startOAuthProvider drives both Google and company (oidc) sign-in: same
  // nonce/cn mechanics, only the path segment and the nonce's storage key
  // differ.
  async function startOAuthProvider(provider) {
    readNext();
    // return_to stays this origin with only cn. On a join link, next is that
    // path alone, saved after readNext so a query next cannot replace it.
    var here = location.pathname.replace(/\/+$/, '') || '/';
    if (joinPathKind(here)) saveNext(here);
    const n = newNonce();
    const key = provider === 'google' ? GOOGLE_NONCE : OIDC_NONCE;
    try { sessionStorage.setItem(key, n); } catch (e) { setLive('This browser is blocking storage sign-in needs. Use the emailed code.', 'err'); return; }
    const hash = await nonceHash(n);
    location.assign('/v1/auth/oauth/' + provider + '?return_to=' + encodeURIComponent(window.SHArrival.url(location.origin + '/?cn=' + hash)));
  }
  async function startGoogle() { return startOAuthProvider('google'); }
  function joinPathKind(path) {
    return /^\/(join|judge|organiser)\/[^/]+$/.test(String(path || '').replace(/\/+$/, ''));
  }
  function showAuthStep(name) {
    var slot = $('signin-slot');
    var link = $('link-elsewhere');
    var verifying = $('verifying');
    var bare = $('signin-bare');
    if (name === 'verifying') {
      if (slot) slot.hidden = true;
      if (link) link.hidden = true;
      if (bare) bare.hidden = true;
      if (verifying) verifying.hidden = false;
      return;
    }
    if (verifying) verifying.hidden = true;
    if (slot) slot.hidden = false;
  }
  function parkJoinChrome() {
    var hold = $('join-chrome-hold');
    var top = $('join-top');
    if (hold && top && top.parentNode !== hold) hold.appendChild(top);
  }
  function destroySignInCard() {
    parkJoinChrome();
    if (signInCard) {
      signInCard.destroy();
      signInCard = null;
    }
  }
  function authError(r) {
    var data = (r && r.data) || {};
    var err = new Error(data.error || 'error');
    err.status = r ? r.status : 0;
    err.code = data.code || '';
    err.reason = data.reason || '';
    err.tries_left = data.tries_left;
    err.body = data;
    return err;
  }
  function loadProviders() {
    return fetch('/v1/auth/oauth/providers').then(function (r) { return r.json(); }).then(function (d) {
      var names = d.providers || [];
      var providers = [];
      if (names.indexOf('google') >= 0) providers.push('google');
      if (names.indexOf('oidc') >= 0) providers.push('oidc');
      return { providers: providers, emailOn: d.email_enabled !== false, oidcLabel: d.oidc_label || '' };
    }).catch(function () {
      return { providers: [], emailOn: true, oidcLabel: '' };
    });
  }
  var IT_FINE = 'Email codes are turned off on this server. Ask your IT team if you cannot sign in.';
  function companyCopy(cfg) {
    if (!cfg || cfg.emailOn || !cfg.providers.length) return null;
    var oidc = cfg.providers.indexOf('oidc') >= 0;
    var google = cfg.providers.indexOf('google') >= 0;
    var copy = { fine: IT_FINE };
    if (oidc && !google) copy.lead = 'Use your ' + (cfg.oidcLabel || 'company') + ' account.';
    return copy;
  }
  function termsNote() {
    var p = document.createElement('p');
    p.className = 'small muted';
    p.appendChild(document.createTextNode('We email you a 6-digit code. No password to remember. By continuing you agree to the '));
    var terms = document.createElement('a');
    terms.href = '/terms';
    terms.textContent = 'Terms';
    var privacy = document.createElement('a');
    privacy.href = '/privacy.html';
    privacy.textContent = 'Privacy';
    p.appendChild(terms);
    p.appendChild(document.createTextNode(' and '));
    p.appendChild(privacy);
    p.appendChild(document.createTextNode('.'));
    return p;
  }
  function mountSignInCard(slot, o) {
    destroySignInCard();
    if (!window.SHSignIn || !slot) return null;
    o = o || {};
    var cfg = o.cfg || { providers: [], emailOn: true, oidcLabel: '' };
    if (!cfg.emailOn && !(cfg.providers && cfg.providers.length)) return null;
    var company = o.company ? companyCopy(cfg) : null;
    var opts = {
      skin: 'ink',
      product: 'Simple Hack',
      linkNote: true,
      title: o.title || 'Sign in',
      providers: cfg.providers || [],
      oidcLabel: cfg.oidcLabel || '',
      emailEnabled: cfg.emailOn !== false,
      autofocus: o.autofocus !== false,
      startProvider: function (name) { startOAuthProvider(name); },
      requestCode: requestSignInCode,
      verifyCode: verifySignInCode
    };
    if (o.lead) opts.lead = o.lead;
    else if (company && company.lead) opts.lead = company.lead;
    if (company && company.fine) opts.fine = company.fine;
    else if (cfg.emailOn && o.terms !== false) {
      opts.fine = '';
      opts.extra = termsNote();
    }
    if (o.top) opts.top = o.top;
    signInCard = window.SHSignIn.mount(slot, opts);
    return signInCard;
  }
  function signInCardError(msg) {
    if (signInCard) signInCard.showError(msg);
    else setLive(msg, 'err');
  }
  async function requestSignInCode(email) {
    var nonce_hash = await emailNonceHash();
    var res = await fetch('/v1/auth', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(nonce_hash ? { email: email, nonce_hash: nonce_hash } : { email: email })
    });
    var data = {};
    try { data = await res.json(); } catch (e) {}
    if (!res.ok) throw authError({ status: res.status, data: data });
    return { email: email };
  }
  function verifySignInCode(email, code) {
    return verifyBody({ email: email, code: code, name: 'dashboard sign-in' }).then(function (r) {
      if (!r.ok) throw authError(r);
      return completeSignIn(r.data);
    });
  }
  function completeSignIn(data) {
    try { localStorage.setItem('apiKey', data.api_key); } catch (e) {}
    if (window.shAuthRender) window.shAuthRender();
    var next = consumeNext();
    var path = location.pathname.replace(/\/+$/, '') || '/';
    if (joinPathKind(path)) return route();
    location.replace(next || '/events');
  }
  async function verifyBody(body) {
    var res = await fetch(window.SHArrival.url('/v1/auth/verify'), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    var data = {};
    try { data = await res.json(); } catch (e) {}
    return { ok: res.ok, status: res.status, data: data };
  }
  async function verifyMagicLink(token, nonce) {
    try {
      var r = await verifyBody({ token: token, nonce: nonce });
      if (r.ok) { await completeSignIn(r.data); return; }
      showAuthStep('email');
      await presentSignIn();
      signInCardError(window.SHSignIn && window.SHSignIn.message ? window.SHSignIn.message({
        status: r.status,
        code: r.data && r.data.code,
        reason: r.data && r.data.reason,
        tries_left: r.data && r.data.tries_left,
        body: r.data
      }) : 'Something went wrong. Try again.');
    } catch (e) {
      showAuthStep('email');
      await presentSignIn();
      signInCardError('Could not reach Simple Hack. Check your connection.');
    }
  }

  async function routeSignIn() {
    document.title = 'Sign in';
    readNext();
    var params = new URLSearchParams(location.search);
    var token = params.get('token');
    var cn = params.get('cn');
    if (token !== null || cn !== null) {
      params.delete('token');
      params.delete('cn');
      var q = params.toString();
      history.replaceState({}, '', location.pathname + (q ? '?' + q : ''));
    }
    $('link-elsewhere').hidden = true;
    if (token) {
      showAuthStep('verifying');
      var nonce = await takeNonceFor(cn);
      if (!nonce) {
        $('link-elsewhere').hidden = false;
        await presentSignIn();
        return;
      }
      await verifyMagicLink(token, nonce);
      return;
    }
    await presentSignIn();
    if (apiKey()) {
      try {
        var meRes = await fetch('/v1/me', { headers: { 'X-API-Key': apiKey(), 'Content-Type': 'application/json' } });
        if (meRes.status === 401) {
          try { localStorage.removeItem('apiKey'); } catch (e) {}
          if (window.shAuthRender) window.shAuthRender();
        } else if (meRes.ok) { location.replace(consumeNext()); return; }
      } catch (e) {}
    }
  }

  async function presentSignIn() {
    showAuthStep('email');
    var cfg = await loadProviders();
    var bare = $('signin-bare');
    if (!cfg.emailOn && !cfg.providers.length) {
      destroySignInCard();
      var empty = $('signin-slot');
      if (empty) { empty.textContent = ''; empty.hidden = true; }
      if (bare) bare.hidden = false;
      return;
    }
    if (bare) bare.hidden = true;
    var slot = $('signin-slot');
    if (slot) slot.hidden = false;
    var card = mountSignInCard(slot, { title: 'Sign in', cfg: cfg, autofocus: true, company: true });
    if (!card && bare) bare.hidden = false;
  }


let initialData, generation = 0
export function go(path) {
  const url = new URL(path, location.href)
  if (url.searchParams.has('ui') || url.pathname === '/signin') { location.assign(url); return }
  history.pushState({}, '', url)
  route()
}
async function route() {
  const turn = ++generation
  const path = location.pathname.replace(/\/+$/, '') || '/'
  if (path === '/signin') {
    document.getElementById('app').innerHTML = `<main class="pub"><div class="hero"><h1>Simple Hack</h1></div><div id="signin-slot"></div><h2 id="signin-bare" hidden>Sign in</h2><p id="link-elsewhere" class="note" hidden>Open this link on the device where you asked for it, or type the 6-digit code from the email.</p><p id="verifying" class="note" hidden>Signing in…</p></main>`
    document.body.classList.remove('booting')
    return routeSignIn()
  }
  const invite = joinPathKind(path)
  if (!apiKey() && !invite) { goSignIn(); return }
  try {
    if (apiKey() && !initialData) initialData = Promise.all([api.me(), api.listEvents()]).then(([m, e]) => {
      if (!m.ok || !e.ok) throw new Error(m.data.error || e.data.error || 'Could not load your events')
      return { me: m.data, events: Array.isArray(e.data) ? e.data : e.data.events || [] }
    }).catch(e => { initialData = null; throw e })
    const data = initialData ? await initialData : { me: {}, events: [] }
    const match = resolveRoute(location)
    let event = null, role = null
    if (match.slug) {
      const r = await api.getEvent(match.slug)
      if (!r.ok) throw new Error(r.data.error || 'Could not open this event')
      event = r.data
      role = event.role
      if (!['organiser', 'participant', 'judge'].includes(role)) throw new Error('You do not have access to this event')
    }
    if (turn !== generation) return
    const ctx = { ...data, api, ui, event, role, params: new URLSearchParams(location.search), go,
      refresh: async () => { initialData = null; return route() } }
    if (match.id.startsWith('public-')) {
      // Invite links stand alone: no sidebar or event switcher until the person has joined.
      document.getElementById('app').innerHTML = `<div class="pub"><div class="pub-top"><span class="wordmark" style="padding:0">Simple Hack</span><span class="grow"></span>${apiKey() ? '<a href="/events">Your events</a>' : ''}</div><main></main></div>`
      ctx.main = document.querySelector('#app main')
    } else ctx.main = mount(ctx, match.section)
    document.body.classList.remove('booting')
    if (event?.me?.approval_status && event.me.approval_status !== 'approved' && role === 'participant') {
      ctx.main.innerHTML = `<div class="locked"><h2>${event.me.approval_status === 'pending' ? 'Your application is waiting for approval' : 'Your application was not approved'}</h2><p>Ask an organiser if you have questions.</p></div>`
      return
    }
    let screen
    try { screen = await import('./screens/' + match.id + '.js') }
    catch (e) {
      if (turn !== generation) return
      ctx.main.innerHTML = `<section class="card"><h1>This screen is being rebuilt</h1><p><a href="${ui.esc(ui.classicURL())}">Use the old screens</a></p></section>`
      document.title = 'Simple Hack'
      return
    }
    if (turn !== generation) return
    document.title = (screen.title ? screen.title(ctx) : match.id === 'account' ? 'Account' : 'Your events') + ' · Simple Hack'
    await screen.render(ctx)
  } catch (e) {
    if (e.message === 'signed out' || turn !== generation) return
    document.body.classList.remove('booting')
    document.getElementById('app').innerHTML = `<main class="page"><div class="note bad" role="alert">${ui.esc(e.message)}</div><a href="/events">Your events</a></main>`
  }
}
api.onSignedOut(signedOut)
document.getElementById('app').addEventListener('click', e => {
  const a = e.target.closest('a[href]')
  if (a?.matches('[data-menu],[aria-disabled="true"]')) { e.preventDefault(); return }
  if (!a || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || a.target || a.hasAttribute('download')) return
  const u = new URL(a.href)
  if (u.origin !== location.origin || u.searchParams.has('ui') || !isAppRoute(u.pathname)) return
  e.preventDefault()
  go(u.pathname + u.search + u.hash)
})
window.addEventListener('popstate', route)
route()
export { saveNext, startGoogle }
