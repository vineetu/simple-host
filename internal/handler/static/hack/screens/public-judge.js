import { errText, dateRange } from './_judge-util.js'

// A generic fallback; render() below sets the exact "Judge <event>" title
// once the invite has loaded.
export function title() { return 'Judge invite' }

function codeFromPath() {
  const m = location.pathname.match(/^\/judge\/([^/]+)\/?$/)
  return m ? decodeURIComponent(m[1]) : ''
}

// public-judge: preview the event and code of conduct, then accept
// (docs/ux-atlas/prototype/public-judge.html), mirroring public-join.
// Reached signed out, so app.js's route() does not gate it behind sign-in
// (see nav.js joinPathKind); this screen shows its own "Sign in" step.
//
// Known limitation outside this file's control: app.js's route() always
// wraps every screen in shell.js's signed-in app shell (sidebar, event
// switcher). The atlas prototype for this page has none — it is meant
// to be a bare, chromeless public page. Fixing that means special-casing
// invite routes inside app.js's route(), which BUILD.md does not allow this
// screen module to touch; this renders the right content inside whatever
// shell wraps it.
export async function render(ctx) {
  const { main, api: client, ui, me } = ctx
  const api = client.api
  const code = codeFromPath()
  const signedIn = !!(me && (me.username || me.email))
  main.innerHTML = `<div class="pub"><p class="muted">Loading…</p></div>`
  if (!code) {
    main.innerHTML = `<div class="pub"><div class="note bad" role="alert">This link does not work any more.</div></div>`
    return
  }

  let r
  try { r = await api('GET', `/v1/hack/judge/${encodeURIComponent(code)}`) }
  catch (e) {
    if (client.isSignedOut(e)) return
    main.innerHTML = `<div class="pub"><div class="note bad" role="alert">Cannot reach the server. Try the link again in a moment.</div></div>`
    return
  }
  if (r.status === 404 || (r.data && r.data.code === 'invalid_code')) {
    main.innerHTML = `<div class="pub"><section class="hero"><h1>This link does not work any more</h1></section></div>`
    return
  }
  if (!r.ok) {
    main.innerHTML = `<div class="pub"><div class="note bad" role="alert">${ui.esc(errText(r))}</div></div>`
    return
  }
  const info = r.data
  document.title = `Judge ${info.title || 'this event'} · Simple Hack`

  // No second <header class="pub-top">: app.js's route() already wraps every
  // public-* screen in its own pub-top bar (wordmark + "Home"), so a
  // duplicate one here stacked two top bars on the page. "See the event
  // page" below keeps that link available without rebuilding the shell's
  // own header -- app.js is Group K's file, out of reach here.
  const eventHref = ui.esc('/e/' + encodeURIComponent(info.slug))

  if (info.joinable === false) {
    const sentence = info.stage === 'draft' ? 'This event is not open yet.' : info.stage === 'archived' ? 'This event is over.' : 'This judge invite isn’t open any more.'
    main.innerHTML = `<div class="pub"><section class="hero"><h1>You’re invited to judge</h1><p>${ui.esc(info.title || '')}</p></section><div class="note">${ui.esc(sentence)} <a href="${eventHref}">See the event page</a>.</div></div>`
    return
  }

  // Already a member: skip straight to "you're already in", like public-join.
  if (signedIn) {
    let mine
    try { mine = await client.getEvent(info.slug) } catch (e) { mine = { ok: false } }
    if (mine.ok) {
      main.innerHTML = `<div class="pub"><section class="hero"><h1>You’re already in ${ui.esc(info.title || 'this event')}</h1><p>Role: ${ui.esc(mine.data.role || 'judge')}</p></section><p><a class="btn primary" href="${eventHref}/judge">Open it</a></p></div>`
      return
    }
  }

  const dates = dateRange(ui, info)
  const meta = [info.organiser_name ? (info.organiser_name + (info.organisation ? ' · ' + info.organisation : '')) : '', dates].filter(Boolean).join(' · ')
  const extraCoc = String(info.coc_text || '').trim()

  main.innerHTML = `<div class="pub"><section class="hero"><h1>You’re invited to judge</h1><p>${ui.esc(info.title || '')}</p>${meta ? `<p>${ui.esc(meta)}</p>` : ''}${info.tagline ? `<p>${ui.esc(info.tagline)}</p>` : ''}<p><a href="${eventHref}">See the event page</a></p></section>` +
    `<section class="card"><h2>Code of conduct</h2><p>${ui.esc(info.coc_default || '')}</p>${extraCoc ? `<p>${ui.esc(extraCoc)}</p>` : ''}</section>` +
    (signedIn
      ? `<section class="card" id="pj-accept"><h2>Accept invitation</h2><form id="pj-form"><div class="field"><label for="pj-name">Your name as shown to organisers and teams<input class="input" id="pj-name" type="text" maxlength="100" value="${ui.esc(me.display_name || '')}" required></label></div><label class="check"><input type="checkbox" id="pj-coc" required><span>I agree to the code of conduct</span></label><p class="help" id="pj-err"></p><button class="btn primary" type="submit" id="pj-submit">Join as judge</button></form></section>`
      : `<div class="card"><p>Read the invitation, then sign in to accept.</p><a class="btn primary lg" href="${ui.esc('/signin?next=' + encodeURIComponent(location.pathname))}">Sign in</a></div>`) +
    `</div>`

  const form = main.querySelector('#pj-form')
  if (!form) return
  form.addEventListener('submit', async e => {
    e.preventDefault()
    const errBox = main.querySelector('#pj-err')
    errBox.textContent = ''
    if (!main.querySelector('#pj-coc').checked) { errBox.textContent = 'Please agree to the code of conduct.'; return }
    const name = main.querySelector('#pj-name').value.trim()
    if (!name) return
    const submit = main.querySelector('#pj-submit')
    submit.disabled = true
    let jr
    try { jr = await api('POST', `/v1/hack/judge/${encodeURIComponent(code)}`, { accept_coc: true, display_name: name, answers: {} }) }
    catch (err) {
      if (!client.isSignedOut(err)) errBox.textContent = 'Cannot reach the server.'
      submit.disabled = false
      return
    }
    if (jr.data && jr.data.code === 'already_member') {
      main.querySelector('#pj-accept').outerHTML = `<section class="card"><h2>You’re already in ${ui.esc(info.title || 'this event')}</h2><p><a class="btn primary" href="${ui.esc('/e/' + encodeURIComponent(info.slug) + '/judge')}">Open it</a></p></section>`
      return
    }
    if (!jr.ok) { errBox.textContent = errText(jr); submit.disabled = false; return }
    const slug = (jr.data && (jr.data.slug || (jr.data.event && jr.data.event.slug))) || info.slug
    location.assign('/e/' + encodeURIComponent(slug) + '/judge')
  })
}
