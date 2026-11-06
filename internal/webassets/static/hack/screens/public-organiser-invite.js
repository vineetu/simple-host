import { href } from '../nav.js'

export function title(ctx) { return 'Co-organise this event' }

export async function render(ctx) {
  const { main, api: client, ui, me } = ctx
  const api = client.api
  const code = decodeURIComponent(location.pathname.replace(/^\/organiser\//, '').replace(/\/+$/, ''))
  const setLive = ui.live(main)

  let invite = null, loadErr = ''
  try {
    const r = await api('GET', '/v1/hack/organiser/' + encodeURIComponent(code))
    if (r.ok) invite = r.data
    else loadErr = r.status === 410 ? 'This invitation isn’t open any more. Ask the organiser for a new one.' : ui.errText(r, { fallback: 'This invitation isn’t open any more. Ask the organiser for a new one.' })
  } catch (e) { loadErr = 'Cannot reach the server.' }

  if (!invite || loadErr) {
    main.innerHTML = `<div class="ph"><div class="t"><h1>Co-organise this event</h1></div></div><div class="locked"><h2>${ui.esc(loadErr || "This invitation isn't open any more.")}</h2></div>`
    return
  }
  if (!invite.joinable) {
    main.innerHTML = `<div class="ph"><div class="t"><h1>${ui.esc(invite.title)}</h1></div></div><div class="locked"><h2>This invitation isn’t open any more. Ask the organiser for a new one.</h2></div>`
    return
  }

  const signedIn = !!client.apiKey()
  // One name per person, the account name (owner decision, 2026-11-06): ask
  // for it here only when the account has none yet; otherwise show who
  // they'll appear as and point to Account, and never send display_name.
  // Replaces the old behaviour of inventing a name from the email prefix
  // or falling back to the literal "Co-organiser".
  const hasName = !!(me && me.display_name && me.display_name.trim())
  const nameHTML = hasName
    ? `<p class="help">You'll appear as <b>${ui.esc(me.display_name.trim())}</b>. Change it on <a href="/account">Account</a>.</p>`
    : `<div class="field"><label for="accept-name">Your name</label><input class="input" id="accept-name" type="text" maxlength="100" required><p class="help">Saved to your account and shown in every event you join.</p></div>`
  main.innerHTML = `
<div class="ph"><div class="t"><h1>You’re invited to co-organise</h1><p>${ui.esc(invite.title)}</p></div></div>
<section class="card"><p>Accepting gives you everything ${ui.esc(invite.organiser_name || 'the organiser')} can do on this event, including editing, publishing results, and deleting it. Only ${ui.esc(invite.organiser_name || 'the creator')}, as the creator, can’t be removed.</p></section>
<section class="card"><h2>Code of conduct</h2><p>${ui.esc(invite.coc_default || '')}</p>${invite.coc_text ? `<p>${ui.esc(invite.coc_text)}</p>` : ''}</section>
${signedIn
    ? `<section class="card"><h2>Accept invitation</h2><p class="sub">Signed in as ${ui.esc((me && (me.login || me.username)) || 'you')}</p><form id="accept-form">${nameHTML}<label class="check"><input type="checkbox" id="accept-coc" required><span>I agree to the code of conduct</span></label><div class="danger-zone"><h2>Full organiser rights</h2><div class="setting"><div class="t"><span>You’ll have full control of this event, including Delete this event. Accept?</span></div><button class="btn danger" type="submit">Accept and co-organise</button></div></div></form></section>`
    : `<section class="card"><p>Read the invitation, then sign in to accept.</p><div class="form-actions"><button class="btn primary lg" id="sign-in-button" type="button">Sign in</button></div></section>`}`

  const signInBtn = main.querySelector('#sign-in-button')
  if (signInBtn) signInBtn.addEventListener('click', () => {
    ctx.go('/signin?next=' + encodeURIComponent(location.pathname))
  })

  const form = main.querySelector('#accept-form')
  if (form) form.addEventListener('submit', async e => {
    e.preventDefault()
    if (!main.querySelector('#accept-coc').checked) { setLive('You must accept the code of conduct.', 'err'); return }
    const nameInput = main.querySelector('#accept-name')
    const nm = nameInput ? nameInput.value.trim() : ''
    if (!hasName && !nm) return
    const btn = form.querySelector('button[type=submit]'); btn.disabled = true
    const body = { accept_coc: true }
    if (!hasName) body.display_name = nm
    try {
      const r = await api('POST', '/v1/hack/organiser/' + encodeURIComponent(code), body)
      if (!r.ok) {
        if (r.data && r.data.code === 'already_member') {
          main.innerHTML = `<div class="note ok">You already belong to this event.</div><p><a class="btn primary" href="/events">Home</a></p>`
          return
        }
        setLive(ui.errText(r), 'err', btn); btn.disabled = false; return
      }
      if (!hasName && me) me.display_name = nm
      const slug = r.data && r.data.event && r.data.event.slug
      if (slug) ctx.go(href('organiser', slug))
      else ctx.go('/events')
    } catch (err) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
  })
}
