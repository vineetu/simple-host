import * as api from './api.js'
import * as ui from './ui.js'
import { mount } from './shell.js'
import { resolveRoute, isAppRoute, href, normalizeURL } from './nav.js'
import { endTour } from './tour.js'
import { openLeave } from './leave.js'
import { clearAllAssistantHistory, mountAssistant, unmountAssistant } from './assistant.js'
// Attribution carry-over (arrival.js) is only present on chromed pages.
const arrival = u => (window.SHArrival ? window.SHArrival.url(u) : u)
// Same as partials/dialog.html shStartSSO: bind this browser's apex SSO session to the key.
async function startSSO(key) {
  if (window.shStartSSO) return window.shStartSSO(key)
  const r = await fetch('/v1/sso/session', { method: 'POST', headers: { 'X-API-Key': key, 'Content-Type': 'application/json' } })
  if (!r.ok) throw new Error('Sign-in unavailable. Try again.')
}

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
    if (n) saveNext(n);
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
    location.assign(arrival('/signin?next=' + encodeURIComponent(n)));
  }
  function signedOut() {
    try { localStorage.removeItem('apiKey'); } catch (e) {}
    // SIM-394 M7: a 401 means the session itself is gone -- the next
    // sign-in on this browser must not inherit this person's chats.
    clearAllAssistantHistory();
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
    location.assign('/v1/auth/oauth/' + provider + '?return_to=' + encodeURIComponent(arrival(location.origin + '/?cn=' + hash)));
  }
  async function startGoogle() { return startOAuthProvider('google'); }
  function joinPathKind(path) {
    var p = String(path || '').replace(/\/+$/, '');
    // Apply (SIM, owner 2026-10-31): a public stand-alone screen keyed by
    // slug, same shape as the join/judge/organiser invite links -- must be
    // reachable signed out (see nav.js resolveRoute's matching regex).
    // Team link (teams redesign, 2026-10-31): /team/{code} is the same
    // shape again -- served signed in or out, one confirm, never joins on
    // a bare GET (public-team-link.js).
    return /^\/(join|judge|organiser|team)\/[^/]+$/.test(p) || /^\/e\/[^/]+\/apply$/.test(p);
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
      return { providers: providers, emailOn: d.email_enabled !== false, oidcLabel: d.oidc_label || '', password: d.password };
    }).catch(function () {
      return { providers: [], emailOn: true, oidcLabel: '' };
    });
  }
  var IT_FINE = 'Email codes are turned off on this server. Ask your IT team if you cannot sign in.';
  function companyCopy(cfg) {
    if (!cfg || cfg.emailOn || !cfg.providers.length || (cfg.password && cfg.password.enabled)) return null;
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
    if (!cfg.emailOn && !(cfg.providers && cfg.providers.length) && !(cfg.password && cfg.password.enabled)) return null;
    var company = o.company ? companyCopy(cfg) : null;
    var opts = {
      skin: 'ink',
      product: 'Simple Hack',
      linkNote: true,
      title: o.title || 'Sign in',
      providers: cfg.providers || [],
      oidcLabel: cfg.oidcLabel || '',
      emailEnabled: cfg.emailOn !== false,
      password: cfg.password,
      onPasswordSuccess: completeSignIn,
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
  async function defaultLanding() {
    const r = await api.listEvents()
    if (!r.ok) return '/events'
    const current = (Array.isArray(r.data) ? r.data : r.data.events || []).filter(e => e.stage !== 'archived' && !e.taken_down)
    return current.length === 1 ? href(current[0].role, current[0].slug, current[0].role === 'organiser' ? 'overview' : 'home') : '/events'
  }
  async function completeSignIn(data) {
    await startSSO(data.api_key);
    try { localStorage.setItem('apiKey', data.api_key); } catch (e) {}
    if (window.shAuthRender) window.shAuthRender();
    var next = consumeNext();
    var path = location.pathname.replace(/\/+$/, '') || '/';
    if (joinPathKind(path)) return route();
    location.replace(next || await defaultLanding());
  }
  async function verifyBody(body) {
    var res = await fetch(arrival('/v1/auth/verify'), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
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
        } else if (meRes.ok) { location.replace(consumeNext() || await defaultLanding()); return; }
      } catch (e) {}
    }
  }

  async function presentSignIn() {
    showAuthStep('email');
    var cfg = await loadProviders();
    var bare = $('signin-bare');
    if (!cfg.emailOn && !cfg.providers.length && !(cfg.password && cfg.password.enabled)) {
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


let initialData, activeContext, generation = 0
// Only the judge route is served with the CSP that may frame a team's site
// and the four video players (hackAppCSP, hack_ui.go). A page first loaded
// on any other route keeps the narrower one for its whole life, so arriving
// at the judge route from there loads the page afresh (route() below).
const FRAMES_PATH = /^\/e\/[^/]+\/judge\/?$/
const framesAllowed = FRAMES_PATH.test(location.pathname)
export async function go(path) {
  const url = new URL(path, location.href)
  if (!await ui.confirmFormLeave()) return
  ui.clearFormRegistrations()
  if (url.pathname === '/signin') { location.assign(url); return }
  history.pushState({}, '', url)
  ui.notePath(url.pathname + url.search + url.hash)
  route()
}
// R8: a real browser Back/Forward bypasses go() (and therefore the dirty-
// guard's own click interception) entirely -- the browser has already
// changed `location` and popped the history entry by the time this fires.
// ui.confirmDiscardForLeave() asks the same "discard your changes?"
// question the guard asks for a link click; if the person keeps editing it
// has already pushed the previous URL back onto `location` itself (we
// never re-rendered, so what's on screen is still correct and matches), so
// route() only runs once the navigation is actually accepted.
let popBusy = false
async function onPopState() {
  if (popBusy) return
  popBusy = true
  try { if (await ui.confirmDiscardForLeave()) await route() } finally { popBusy = false }
}
async function route() {
  // Review #2: a button that navigates (ctx.go(), not just an <a> click)
  // never ran the tour's own teardown, so a card could survive onto the
  // next screen with its ringed target detached. Every route change clears
  // whatever tour is open first.
  endTour()
  if (!framesAllowed && FRAMES_PATH.test(location.pathname)) { location.reload(); return }
  const turn = ++generation
  const path = location.pathname.replace(/\/+$/, '') || '/'
  if (path === '/signin') {
    unmountAssistant()
    document.getElementById('app').innerHTML = `<main class="pub"><div class="hero"><h1>Simple Hack</h1></div><div id="signin-slot"></div><h2 id="signin-bare" hidden>Sign in</h2><p id="link-elsewhere" class="note" hidden>Open this link on the device where you asked for it, or type the 6-digit code from the email.</p><p id="verifying" class="note" hidden>Signing in…</p></main>`
    document.body.classList.remove('booting')
    return routeSignIn()
  }
  const invite = joinPathKind(path)
  if (!apiKey() && !invite) { unmountAssistant(); goSignIn(); return }
  try {
    if (apiKey() && !initialData) initialData = Promise.all([api.me(), api.listEvents()]).then(([m, e]) => {
      if (!m.ok || !e.ok) throw new Error(m.data.error || e.data.error || 'Could not load your events')
      return { me: m.data, events: Array.isArray(e.data) ? e.data : e.data.events || [] }
    }).catch(e => { initialData = null; throw e })
    const data = initialData ? await initialData : { me: {}, events: [] }
    const normalized = normalizeURL(location)
    if (normalized.href !== location.href) { history.replaceState(history.state, '', normalized); ui.notePath(normalized.pathname + normalized.search + normalized.hash) }
    const match = resolveRoute(location)
    let event = null, role = null
    if (match.slug) {
      const r = await api.getEvent(match.slug)
      if (!r.ok) {
        // S15: a missing or deleted event hit at the top level (/e/<slug>/
        // team|manage|judge, before any screen module loads) used to fall
        // through to the generic catch below -- a bare error outside the
        // app frame. Give it the same friendly card the public /e/<slug>
        // page already has, inside the shell (no event, so just the Home
        // level: brand, top nav, no switcher) instead.
        if (turn !== generation) return
        if (r.status === 404 || (r.data && r.data.code === 'event_not_found')) {
          // Round-5 Cos2: the member-scoped GET 404s the same way whether
          // the event is really gone or this person just isn't (or isn't
          // any more) a member of it -- a left participant hitting an old
          // /e/<slug>/team link used to see "Event not found" about an
          // event that's still very much there. The public feed needs no
          // membership, so it tells the two cases apart.
          let pub = null
          try { const pr = await api.api('GET', '/v1/hack/events/' + encodeURIComponent(match.slug) + '/public'); if (pr.ok) pub = pr.data } catch (e) {}
          if (turn !== generation) return
          const notFoundCtx = { ...data, api, ui, event: null, role: null, params: new URLSearchParams(location.search), go,
            alive: () => turn === generation,
            refresh: async () => { initialData = null; return route() } }
          const notFoundMain = mount(notFoundCtx, null, 'event-not-found')
          if (pub) {
            notFoundMain.innerHTML = `<section class="locked"><h2>You're not in ${ui.esc(pub.title)}</h2><p>You're signed in, but not a member of this event -- you may have left it, or never joined.</p><p><a class="btn primary" href="/events">Home</a> <a class="btn" href="/directory">Find a hackathon</a></p></section>`
            document.title = `You're not in ${pub.title} · Simple Hack`
          } else {
            notFoundMain.innerHTML = `<section class="locked"><h2>Event not found</h2><p>It may have been deleted, or the link is wrong.</p><p><a class="btn primary" href="/events">Home</a> <a class="btn" href="/directory">Find a hackathon</a></p></section>`
            document.title = 'Event not found · Simple Hack'
          }
          unmountAssistant()
          document.body.classList.remove('booting')
          return
        }
        throw new Error(r.data.error || 'Could not open this event')
      }
      event = r.data
      role = event.role
      if (!['organiser', 'participant', 'judge'].includes(role)) throw new Error('You do not have access to this event')
      const required = { organiser: 'manage', judge: 'judge', participant: 'take_part' }[match.role]
      if (event.admin_view && match.role !== 'organiser') {
        history.replaceState(history.state, '', href('organiser', match.slug, 'overview'))
        return route()
      }
      if (!event.admin_view && !event.can?.[required]) {
        const home = href(role, match.slug, role === 'organiser' ? 'overview' : 'home')
        history.replaceState(history.state, '', home)
        return route()
      }
      role = match.role

    }
    if (turn !== generation) return
    const ctx = { ...data, api, ui, event, role, params: new URLSearchParams(location.search), go,
      alive: () => turn === generation,
      refresh: async () => { initialData = null; return route() } }
    ctx.screenId = match.id
    if (match.id.startsWith('public-')) {
      // Invite links stand alone: no sidebar or event switcher until the person has joined.
      document.getElementById('app').innerHTML = `<div class="pub"><div class="pub-top"><a class="wordmark" href="/">simple<b>·</b>hack</a><span class="grow"></span>${apiKey() ? '<a href="/events">Home</a>' : ''}</div><main></main></div>`
      ctx.main = document.querySelector('#app main')
      unmountAssistant()
    } else {
      ctx.main = mount(ctx, match.section, match.id)
      // Floating Ask (SIM-394 v2): mounted from the router for every
      // organiser event route, not just Home, so it exists on every
      // screen without each screen module having to ask for it itself.
      // mountAssistant decides eligibility (organiser, can.manage, not an
      // admin inspecting) itself from ctx -- not awaited here, since only
      // Home's own tour needs to wait for it (organiser-overview.js makes
      // its own call to the same effect, awaited, and the two share one
      // in-flight build rather than racing each other).
      mountAssistant(ctx, { api: api.api, ev: event?.event, checklist: event?.checklist, screenId: match.id, tab: ctx.params.get('tab') || '' })
    }
    activeContext = ctx
    document.body.classList.remove('booting')
    if (event?.me?.approval_status && event.me.approval_status !== 'approved' && role === 'participant') {
      // A20: a pending/rejected participant otherwise still sees the full
      // sidebar and tab bar, and every one of them leads to the same lock
      // card -- hide everything but the current page's own link.
      ctx.main.closest('.app')?.classList.add('locked-shell')
      const pending = event.me.approval_status === 'pending'
      // Apply to a hackathon (owner, 2026-10-31): the old pending copy
      // promised "The organisers will let you know", an email that does not
      // exist -- say where the answer actually appears instead (same copy
      // for an application and a join-link sign-up, spec.json
      // participant-home "pending / rejected approval").
      // Withdraw/leave (SIM-340, widened by owner decision 2026-10-31): a
      // pending applicant withdraws; a rejected one can now leave too --
      // same mechanics (their row is deleted like a withdraw), so they can
      // apply again later and the organiser decides again. Archived takes
      // precedence over either (Codex review, SIM-340): the server refuses
      // on an ended event (409 event_closed) same as any other leave, so
      // the control must show that reason, disabled, rather than an action
      // that can only fail.
      const ended = event.event.stage === 'archived'
      const leaveCopy = ended
        ? (pending ? 'This event has ended, so you can’t withdraw. It stays on your Home as a record.' : 'This event has ended, so nobody can leave it. It stays on your Home as a record.')
        : (pending ? 'The organisers stop seeing it, along with your answers. You can apply again while sign-up is open.' : 'Leaving removes your answers. You can then apply again while the event takes applications, and the organisers decide again.')
      const withdrawZone = `<section class="danger-zone"><h2>${pending ? 'Withdraw your application' : 'Leave this event'}</h2><div class="setting"><div class="t"><span>${leaveCopy}</span></div><button class="btn danger" type="button" id="withdraw-btn"${ended ? ' disabled' : ''}>${pending ? 'Withdraw application' : 'Leave event'}</button></div></section>`
      ctx.main.innerHTML = `<div class="locked"><h2>${pending ? 'Your application is waiting for the organisers' : "Your application wasn't accepted this time"}</h2><p>${pending ? "You'll see their answer here. Ask them if you have questions." : 'Ask an organiser if you have questions.'}</p></div>${withdrawZone}`
      const btn = ctx.main.querySelector('#withdraw-btn')
      if (btn) btn.addEventListener('click', () => openLeave({ api, ui, slug: event.event.slug, title: event.event.title, from: pending ? 'pending' : 'rejected' }))
      return
    }
    let screen
    try { screen = await import('./screens/' + match.id + '.js') }
    catch (e) {
      if (turn !== generation) return
      ctx.main.innerHTML = `<section class="card"><h1>This screen could not load</h1><p>Reload the page, or go to <a href="/events">Home</a>.</p><p><button class="btn" type="button" id="screen-reload">Reload</button></p></section>`
      document.title = 'Simple Hack'
      ctx.main.querySelector('#screen-reload')?.addEventListener('click', () => location.reload())
      return
    }
    if (turn !== generation) return
    document.title = (screen.title ? screen.title(ctx) : match.id === 'account' ? 'Account' : 'Home') + ' · Simple Hack'
    await screen.render(ctx)
  } catch (e) {
    if (api.isSignedOut(e) || turn !== generation) return
    unmountAssistant()
    document.body.classList.remove('booting')
    document.getElementById('app').innerHTML = `<main class="page"><div class="note bad" role="alert">${ui.esc(e.message)}</div><a href="/events">Home</a></main>`
  }
}
api.onSignedOut(signedOut)
if (apiKey()) api.api('POST', '/v1/sso/session').catch(() => {})
// Refresh membership chrome without replacing a form the person is editing.
async function refreshEventList() {
  if (!apiKey()) return
  const ctx = activeContext
  initialData = null
  try {
    const [m, e] = await Promise.all([api.me(), api.listEvents()])
    if (!m.ok || !e.ok || !ctx?.alive()) return
    const data = { me: m.data, events: Array.isArray(e.data) ? e.data : e.data.events || [] }
    initialData = Promise.resolve(data)
    ctx.events = data.events
    ctx.updateEvents?.(data.events)
    ctx.updateEventScreen?.()
  } catch (_) {}
}
window.addEventListener('focus', () => {
  refreshEventList()
  const match = resolveRoute(new URL(location.href))
  if (['organiser-overview', 'judge-home', 'participant-home'].includes(match?.id)) activeContext?.refresh()
})
api.onEventsChanged(() => { initialData = null; refreshEventList() })
// SIM-394 v2: attached to the whole page, not just #app, because the
// floating assistant panel (assistant.js) is appended straight to
// document.body on purpose, so it survives #app's own innerHTML
// replacement on every navigation (shell.js's mount()) -- a link inside a
// reply needs the same in-app routing interception every other app link
// already gets here. Narrowed back to '#app, .assistant-panel' (review
// fix) so nothing else that might ever be appended to document.body
// (a ui.js dialog scrim, the guided tour card) changes behaviour: those
// never needed this interception before and still don't.
document.body.addEventListener('click', e => {
  const a = e.target.closest('a[href]')
  if (a?.matches('[data-menu],[aria-disabled="true"]')) { e.preventDefault(); return }
  if (!a || !a.closest('#app, .assistant-panel') || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || a.target || a.hasAttribute('download')) return
  const u = new URL(a.href)
  if (u.origin !== location.origin || u.searchParams.has('ui') || !isAppRoute(u.pathname)) return
  // A same-document "jump to section" link (e.g. #schedule on the page
  // you're already on): ordinary browser anchor navigation, so it scrolls
  // without remounting the screen and losing unsaved edits.
  if (u.hash && u.pathname === location.pathname && u.search === location.search) return
  e.preventDefault()
  go(u.pathname + u.search + u.hash)
})
window.addEventListener('popstate', onPopState)
route()
export { saveNext, startGoogle }
