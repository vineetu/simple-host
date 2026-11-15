// Public page reached at /team/{code}, signed in or not. Turns a team link
// into a team membership in one confirm, or says plainly why it can't --
// never on a bare GET (link previews, prefetch). Mirrors public-join.js's
// own shape: a preview before sign-in, a real navigation after a
// successful join (the cached event/team list from this load predates the
// new membership). The team link never admits anyone to the event itself
// -- a non-member is told to apply or ask the organiser (spec.json
// public-team-link decisions).
function codes(ui) { return { ...ui.ERROR_MESSAGES, rate_limited: 'Too many tries at once. Wait a moment and try again.' } }

function codeFromPath() {
  const m = location.pathname.replace(/\/+$/, '').match(/^\/team\/([^/]+)$/)
  return m ? decodeURIComponent(m[1]) : ''
}

export function title() { return 'Team link' }

export async function render(ctx) {
  const { main, api: client, ui } = ctx
  const api = client.api
  const esc = ui.esc
  const CODES = codes(ui)
  const code = codeFromPath()
  main.innerHTML = `<p class="note">Loading…</p>`
  if (!code) { main.innerHTML = `<div class="note bad" role="alert">${esc(CODES.team_link_not_found)}</div>`; return }

  let r
  try { r = await api('GET', '/v1/hack/team-links/' + encodeURIComponent(code)) }
  catch (e) {
    if (!client.isSignedOut(e)) main.innerHTML = `<div class="note bad" role="alert">Cannot reach the server. Try the link again in a moment.</div>`
    return
  }
  if (r.status === 404 || (r.data && r.data.code === 'team_link_not_found')) {
    // location.host, not a literal "simple-hack.app": this instance's own
    // address, not ours.
    main.innerHTML = `<section class="hero"><h1>Team link</h1><p>${esc(location.host)}</p></section><section class="card"><h2>This team link doesn’t work</h2><p class="sub">The team may have made a new link, or the link was copied wrong. Ask a teammate to send it again.</p><div class="row-flex"><a class="btn primary lg" href="/directory">Find a hackathon</a></div></section>`
    return
  }
  if (!r.ok) { main.innerHTML = `<div class="note bad" role="alert">${esc(ui.errText(r, { codes: CODES }))}</div>`; return }

  const info = r.data || {}
  const ev = info.event || {}
  const team = info.team || {}
  const you = info.you
  const slug = ev.slug
  const openHref = '/e/' + encodeURIComponent(slug || '')
  const teamHref = '/e/' + encodeURIComponent(slug || '') + '/team'

  const header = `<section class="hero"><h1>Join ${esc(team.name || 'this team')}</h1><p>A team in ${esc(ev.title || 'this event')}</p></section>`

  function card(h2, sub, buttons) {
    return header + `<section class="card"><h2>${esc(h2)}</h2><p class="sub">${esc(sub)}</p><div class="row-flex">${buttons}</div></section>`
  }

  if (!client.apiKey()) {
    main.innerHTML = card('Sign in to join ' + (team.name || 'this team'), 'Use the account you joined ' + (ev.title || 'this event') + ' with. You’ll come straight back here.',
      `<button class="btn primary lg" type="button" id="link-signin">Sign in</button>`)
    main.querySelector('#link-signin').addEventListener('click', () => {
      ctx.go('/signin?next=' + encodeURIComponent(location.pathname))
    })
    return
  }

  // Signed in: `you` is never null here (the server only omits it signed
  // out), but every branch below reads it defensively anyway.
  const status = you && you.status
  if (status === 'organiser' || status === 'judge') {
    main.innerHTML = card('Only participants join teams', 'Organisers and judges don’t join teams.', `<a class="btn primary lg" href="${esc(openHref)}">Open the event</a>`)
    return
  }
  if (status === 'pending') {
    main.innerHTML = card('Your place in the event is waiting', 'An organiser still has to approve you. Open this link again once you’re in.', '')
    return
  }
  if (status === 'rejected') {
    main.innerHTML = card('You’re not in this event', 'Ask the organiser if you have questions.', `<a class="btn primary lg" href="${esc(openHref)}">Open the event</a>`)
    return
  }
  if (status === 'not_in_event') {
    const buttons = ev.applications_open
      ? `<a class="btn primary lg" href="/e/${encodeURIComponent(slug)}/apply">Apply to the event</a><a class="btn quiet lg" href="${esc(openHref)}">Open the event</a>`
      : `<a class="btn quiet lg" href="${esc(openHref)}">Open the event</a>`
    main.innerHTML = card('Join the event first', 'This link adds you to a team in ' + (ev.title || 'this event') + ', and you’re not in that event yet.' + (ev.applications_open ? '' : ' Ask the organiser for the event’s join link.'), buttons)
    return
  }
  if (status === 'on_this_team') {
    main.innerHTML = card('You’re already on ' + (team.name || 'this team'), '', `<a class="btn primary lg" href="${esc(teamHref)}">Go to your team</a>`)
    return
  }
  if (status === 'on_other_team') {
    main.innerHTML = card('You’re on ' + (you.team_name || 'another team'), 'To join ' + (team.name || 'this team') + ', leave ' + (you.team_name || 'your team') + ' first, then open this link again.', `<a class="btn primary lg" href="${esc(teamHref)}">Go to your team</a>`)
    return
  }
  if (info.reason === 'locked') {
    main.innerHTML = card((team.name || 'This team') + ' isn’t taking new people', 'Its admins locked the team. Ask them, or find another team.', `<a class="btn primary lg" href="${esc(teamHref)}">Find a team</a>`)
    return
  }
  if (info.reason === 'full') {
    main.innerHTML = card((team.name || 'This team') + ' is full', 'It has ' + (team.member_count ?? '') + ' of ' + (team.team_size_max ?? '') + ' people. Find another team or start your own.', `<a class="btn primary lg" href="${esc(teamHref)}">Find a team</a>`)
    return
  }
  if (info.reason === 'teams_closed') {
    main.innerHTML = card('Teams can’t change now', 'Entries are closed for this event.', `<a class="btn primary lg" href="${esc(openHref)}">Open the event</a>`)
    return
  }

  // Ready: one confirm, join on the button press only -- never on a bare
  // GET (link previews, chat unfurls, prefetch).
  const dl = [['Team', team.name], ['People', (team.member_count ?? '') + ' of ' + (team.team_size_max ?? '')]]
  if (team.track && team.track.name) dl.push(['Track', team.track.name])
  main.innerHTML = header + `<section class="card"><h2>You’re about to join ${esc(team.name || 'this team')}</h2><p class="sub">You’ll be on the team straight away and can work on its entry and website. You can leave while teams are open.</p><dl>${dl.map(([k, v]) => `<dt>${esc(k)}</dt><dd>${esc(v)}</dd>`).join('')}</dl><div class="row-flex"><button class="btn primary lg" type="button" id="link-join">Join ${esc(team.name || 'this team')}</button><a class="btn quiet lg" href="/events">Not now</a></div></section>`
  const joinBtn = main.querySelector('#link-join')
  joinBtn.addEventListener('click', async () => {
    joinBtn.disabled = true
    let jr
    try { jr = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/join', { code }) }
    catch (e) { if (!client.isSignedOut(e)) ui.toast('Cannot reach the server.', main, true); joinBtn.disabled = false; return }
    if (!jr.ok) { ui.toast(ui.errText(jr, { codes: CODES }), main, true); joinBtn.disabled = false; return }
    // A real navigation, same reasoning as public-join.js: the cached event
    // list from this load predates the just-created team membership.
    try { sessionStorage.setItem('sh-selfjoin-' + slug, '1') } catch (e) {}
    location.assign(teamHref)
  })
}
