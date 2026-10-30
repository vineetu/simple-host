// Public page reached at /join/{code}, signed in or not. Behaviour mirrors
// hack-app.html's routeJoin()/join-form: preview before sign-in, the same
// questions become the live form after, an "already applied" branch that
// reads the real approval_status, and a closed/dead-link branch. Rendered
// inside the shared app shell (mount() in shell.js runs for every route, this
// one included) -- there is no event/role for a bare join link, so the
// sidebar shows its generic, signed-out state around this card. That shell
// behaviour is unchanged here.
const ERRORS = {
  coc_required: 'Please agree to the code of conduct.',
  invalid_display_name: 'Enter your name on one line, without special formatting.',
  already_member: 'You are already in this event.',
  admin_cannot_join: 'The platform admin cannot join an event.',
  joining_closed: 'Sign-up has closed for this event.',
  event_taken_down: 'This event has been taken down.',
  invalid_code: 'This link does not work any more.',
  answer_required: 'Answer every required question.',
  invalid_answers: "Something in that request wasn't right. Reload and try again.",
  rate_limited: 'Too many tries at once. Wait a moment and try again.'
}
function errText(r) {
  const code = r && r.data && r.data.code
  if (code && ERRORS[code]) return ERRORS[code]
  if (r && r.status >= 500) return 'Something went wrong. Try again.'
  if (r && r.data && typeof r.data.error === 'string') return r.data.error
  return 'Something went wrong.'
}
function codeFromPath() {
  const m = location.pathname.replace(/\/+$/, '').match(/^\/join\/([^/]+)$/)
  return m ? decodeURIComponent(m[1]) : ''
}

export function title() { return 'Join' }

export async function render(ctx) {
  const { main, api: client, ui } = ctx
  const api = client.api
  const esc = ui.esc
  const code = codeFromPath()
  main.innerHTML = `<p class="note">Loading…</p>`
  if (!code) { main.innerHTML = `<div class="note bad" role="alert">${esc(ERRORS.invalid_code)}</div>`; return }

  let r
  try { r = await api('GET', '/v1/hack/join/' + encodeURIComponent(code)) }
  catch (e) {
    if (e.message !== 'signed out') main.innerHTML = `<div class="note bad" role="alert">Cannot reach the server. Try the link again in a moment.</div>`
    return
  }
  if (r.status === 404 || (r.data && r.data.code === 'invalid_code')) {
    main.innerHTML = `<div class="note bad" role="alert">${esc(ERRORS.invalid_code)}</div>`
    return
  }
  if (!r.ok) { main.innerHTML = `<div class="note bad" role="alert">${esc(errText(r))}</div>`; return }
  const info = r.data || {}
  const slug = info.slug
  const openHref = '/e/' + encodeURIComponent(slug)

  const header = `<div class="ph"><div class="t"><h1>Join ${esc(info.title || 'this event')}</h1>${info.organiser_name ? `<p>${esc(info.organiser_name)}${info.organisation ? ' · ' + esc(info.organisation) : ''}</p>` : ''}</div></div>`

  // Already a member? (signed in and this slug is already in their event list)
  if (client.apiKey() && slug) {
    const already = (ctx.events || []).some(e => e.slug === slug)
    if (already) {
      let mine
      try { mine = await client.getEvent(slug) } catch (e) { mine = { ok: false } }
      if (mine.ok && !(mine.data && mine.data.admin_view)) {
        const approval = mine.data.me && mine.data.me.approval_status
        const line = approval === 'rejected'
          ? "Your application wasn't accepted this time."
          : approval === 'pending'
            ? 'Your application is waiting for an organiser to approve it. This usually takes a day or two.'
            : "You're already in " + esc(info.title || 'this event') + '.'
        const go = approval === 'pending' || approval === 'rejected'
          ? `<a class="btn" href="${esc(openHref)}">Open the event</a>`
          : `<a class="btn primary" href="/e/${encodeURIComponent(slug)}/team">Go to the event</a>`
        main.innerHTML = header + `<div class="note ${approval === 'rejected' ? 'bad' : approval === 'pending' ? 'warn' : 'ok'}">${line}</div><p>${go}</p>`
        return
      }
    }
  }

  if (info.joinable === false) {
    const closedLine = info.stage === 'draft' ? 'This event is not open yet.' : info.stage === 'archived' ? 'This event is over.' : 'Sign-up has closed for this event.'
    main.innerHTML = header + `<div class="note warn">${esc(closedLine)}</div><p><a class="btn" href="${esc(openHref)}">Open the event</a></p>`
    return
  }

  const questions = Array.isArray(info.signup_questions) ? info.signup_questions : []
  const questionsHTML = questions.map((q, i) => `<div class="field"><label for="join-q-${i}">${esc(q.prompt)}${q.required ? '' : ' (optional)'}</label><textarea class="input" id="join-q-${i}" data-question="${esc(q.id)}"${q.required ? ' required' : ''}></textarea></div>`).join('')
  const cocHTML = `<h2>Code of conduct</h2><p>${esc(info.coc_default || '')}</p>${info.coc_text ? `<h3>From the organisers</h3><p>${esc(info.coc_text)}</p>` : ''}`

  if (!client.apiKey()) {
    main.innerHTML = header + `<div class="card"><h2>What you'll be asked</h2><ul class="steps">${questions.map(q => `<li><span class="what"><b>${esc(q.prompt)}${q.required ? '' : ' (optional)'}</b></span></li>`).join('') || '<li><span class="what">No questions to answer -- just the code of conduct.</span></li>'}</ul>${cocHTML}<button class="btn primary" type="button" id="join-signin">Sign in</button></div>`
    main.querySelector('#join-signin').addEventListener('click', () => {
      ctx.go('/signin?next=' + encodeURIComponent(location.pathname))
    })
    return
  }

  const name = (ctx.me && ctx.me.display_name) || ''
  main.innerHTML = header + `<form class="card" id="join-form"><h2>Apply</h2><div class="field"><label for="join-name">Your name</label><input class="input" id="join-name" type="text" maxlength="100" required value="${esc(name)}"></div>${questionsHTML}${cocHTML}<label class="check"><input type="checkbox" id="join-coc" required><span>I agree to the code of conduct.</span></label><button class="btn primary" type="submit" id="join-btn">Join ${esc(info.title || '')}</button></form>`
  main.querySelector('#join-form').addEventListener('submit', async e => {
    e.preventDefault()
    const btn = main.querySelector('#join-btn')
    if (!main.querySelector('#join-coc').checked) { ui.toast(ERRORS.coc_required, main, true); return }
    const nm = main.querySelector('#join-name').value.trim()
    if (!nm) return
    const answers = {}
    main.querySelectorAll('[data-question]').forEach(input => { answers[input.getAttribute('data-question')] = input.value })
    btn.disabled = true
    let res
    try { res = await api('POST', '/v1/hack/join/' + encodeURIComponent(code), { accept_coc: true, display_name: nm, answers }) }
    catch (err) {
      if (err.message !== 'signed out') ui.toast('Cannot reach the server.', main, true)
      btn.disabled = false
      return
    }
    if (res.data && res.data.code === 'already_member') {
      main.innerHTML = header + `<div class="note ok">You're already in ${esc(info.title || 'this event')}.</div><p><a class="btn primary" href="/e/${encodeURIComponent(slug)}/team">Go to the event</a></p>`
      return
    }
    if (!res.ok) { ui.toast(errText(res), main, true); btn.disabled = false; return }
    const joinedSlug = (res.data && res.data.event && res.data.event.slug) || slug
    // A real navigation, not an SPA one: the cached event list from this
    // load does not include the just-created membership yet, and the shell
    // reads that list for the sidebar. Reloading is what hack-app.html does
    // here too.
    location.assign('/e/' + encodeURIComponent(joinedSlug) + '/team')
  })
}
