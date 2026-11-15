import { adminLock } from './_orgA-util.js'
import { href, resolve } from '../nav.js'

export function title(ctx) { return 'People' }

function personCategory(p) {
  if (p.role === 'organiser') return p.primary_organiser ? 'Organiser' : 'Co-organiser'
  if (p.role === 'judge') return 'Judge'
  return 'Participant'
}

// Apply to a hackathon (owner, 2026-10-31): the facts line under an
// application card -- where they're based, age range (a warn pill for
// Under 18, per D8: the organiser's own safeguarding cue) and gender, all
// three optional and all left out silently when unshared, down to the one
// muted line when nothing at all was shared.
function appFacts(ui, a) {
  const esc = ui.esc
  const place = [a.city, a.country ? ui.countryName(a.country) : ''].filter(Boolean).join(', ')
  const bits = []
  if (place) bits.push(esc(place))
  if (a.age_range) bits.push(a.age_range === 'under_18' ? '<span class="pill warn">Under 18</span>' : esc(ui.AGE_RANGE_LABELS[a.age_range] || a.age_range))
  if (a.gender) bits.push(esc(ui.GENDER_LABELS[a.gender] || a.gender))
  if (!bits.length) return '<p class="small muted">Didn’t share location, age range or gender</p>'
  return '<p class="small">' + bits.join(' · ') + '</p>'
}

export async function render(ctx) {
  const { main, api: client, ui, event: data } = ctx
  const api = client.api
  const ev = data.event
  const admin = !!data.admin_view
  const path = '/v1/hack/events/' + encodeURIComponent(ev.slug)
  const setLive = ui.live(main)

  let applications = [], people = []
  try {
    const [a, p] = await Promise.all([api('GET', path + '/applications'), api('GET', path + '/people')])
    if (a.ok) applications = a.data || []
    if (p.ok) people = p.data || []
  } catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err') }
  // A render left running past a newer navigation (Judging opened while
  // People's own fetch was still in flight) must not go on to replace
  // `main` with its own, now-stale screen the instant this fetch resolves
  // -- organiser-overview.js already checks ctx.alive() after its own
  // async /people+/applications fetch, before painting; this screen had no
  // equivalent (Codex review finding 4, SIM-344 phase 2).
  if (!ctx.alive()) return

  let roleFilter = (ctx.params.get('role') === 'judge' || location.hash === '#judges') ? 'Judges' : 'Everyone'
  // Applications filter row (spec.json organiser-people "Applications"
  // section): client-side only, shown once anything is waiting. 'from' is
  // 'all'|'applied'|'link', 'age' is '' (Any) or an age_range value,
  // 'country' is '' (Any) or an ISO code, 'sort' is 'oldest'|'newest' --
  // the API already returns pending applications oldest-first, so "newest"
  // is just that list reversed, not a real re-sort.
  let appFrom = 'all', appAge = '', appCountry = '', appSort = 'oldest'

  function applicationCard(a) {
    const esc = ui.esc
    const pill = a.joined_via === 'link' ? 'Join link' : 'Applied'
    const verb = a.joined_via === 'link' ? 'Joined with your link' : 'Applied'
    return `<section class="card" data-app="${esc(a.user_id)}"><h2>${esc(a.display_name)}</h2>
<p class="sub">${esc(a.email || '')} · <span class="pill">${pill}</span></p>
<p class="small muted">${verb} ${esc(ui.dateTime(a.joined_at, ev))}</p>
${appFacts(ui, a)}
${(a.answers || []).map(q => `<p><b>${esc(q.prompt)}</b><br>${q.answer ? esc(q.answer) : '<span class="muted">No answer</span>'}</p>`).join('')}
<div class="row-flex"><button class="btn" type="button" data-app-approve>Approve</button><button class="btn danger" type="button" data-app-reject title="${esc(a.display_name)} can't join a team or take part in this event, and you can't change this decision later. They can still leave and apply again while your event takes applications, and you'd decide again.">Reject</button></div></section>`
  }
  function personRow(p) {
    const bits = [personCategory(p)]
    // Cosmetic #5 (Grok re-test, round 4): a pending applicant read
    // "Pending · Applied · Joined <date>" -- "Joined" on someone who hasn't
    // been let in yet. p.joined_at is the one timestamp the API gives for
    // both acts, so the word in front of it is what has to carry the
    // distinction: "Applied <date>" while pending, "Joined <date>" once
    // approved (same ui.dateOnly -- itself ui.dateTime with the event's own
    // zone -- the rest of this screen already uses). A rejected person was
    // never approved either, so they keep the same "Applied" word.
    const notApproved = p.approval_status === 'pending' || p.approval_status === 'rejected'
    if (p.approval_status === 'rejected') bits.push('Not approved')
    if (p.team && (p.team.name || p.team.slug)) bits.push(p.team.name || p.team.slug)
    const when = ui.dateOnly(p.joined_at, ev)
    if (when) bits.push((notApproved ? 'Applied ' : 'Joined ') + when)
    let actions = ''
    if (p.primary_organiser) actions = '<span class="meta">Event creator</span>'
    else if (p.role === 'organiser') actions = `<a href="${ui.esc(resolve('o.organisers', ev.slug).href)}">Organisers</a>`
    else {
      const keyBtn = p.has_key ? `<button class="btn danger sm" type="button" data-turn-off-key="${ui.esc(p.user_id)}" data-name="${ui.esc(p.display_name)}" title="Their key stops working at once. They'd need to create a new one themselves.">Turn off publishing key</button>` : ''
      actions = `<details><summary class="small muted">Actions</summary><button class="btn danger sm" type="button" data-remove-person="${ui.esc(p.user_id)}" data-name="${ui.esc(p.display_name)}" data-role="${ui.esc(p.role)}">Remove from event</button>${keyBtn}</details>`
    }
    return `<div class="r"><div class="main"><b>${ui.esc(p.display_name || 'Someone')}</b><span>${ui.esc(bits.filter(Boolean).join(' · '))}</span></div>${actions}</div>`
  }
  function paint() {
    const $ = id => main.querySelector('#' + id)
    const esc = ui.esc
    const pending = applications.filter(a => a.status === 'pending')
    const visiblePeople = people.filter(p => roleFilter === 'Everyone' || (roleFilter === 'Organisers' && p.role === 'organiser') || (roleFilter === 'Participants' && p.role === 'participant') || (roleFilter === 'Judges' && p.role === 'judge') || (roleFilter === 'Co-organisers' && p.role === 'organiser' && !p.primary_organiser))
    // A9: hide the Applications section once there's nothing in it and
    // sign-up is already open -- the .ph subtitle already says so, and an
    // empty card under its own heading just repeats that.
    const showApplications = pending.length > 0 || ev.stage === 'draft'

    const appliedCount = pending.filter(a => a.joined_via !== 'link').length
    const linkCount = pending.filter(a => a.joined_via === 'link').length
    const countryChoices = [...new Set(pending.filter(a => a.country).map(a => a.country))]
      .map(code => ({ code, name: ui.countryName(code) || code })).sort((a, b) => a.name.localeCompare(b.name))
    let visiblePending = pending.slice()
    if (appFrom === 'applied') visiblePending = visiblePending.filter(a => a.joined_via !== 'link')
    else if (appFrom === 'link') visiblePending = visiblePending.filter(a => a.joined_via === 'link')
    if (appAge) visiblePending = visiblePending.filter(a => a.age_range === appAge)
    if (appCountry) visiblePending = visiblePending.filter(a => a.country === appCountry)
    if (appSort === 'newest') visiblePending = visiblePending.slice().reverse()
    const filtersActive = appFrom !== 'all' || appAge !== '' || appCountry !== '' || appSort !== 'oldest'
    const filterRow = pending.length ? `<div class="field-row" id="app-filters">
<div class="field"><label>From<select class="select" id="app-filter-from"><option value="all"${appFrom === 'all' ? ' selected' : ''}>Everyone waiting (${pending.length})</option><option value="applied"${appFrom === 'applied' ? ' selected' : ''}>Applied (${appliedCount})</option><option value="link"${appFrom === 'link' ? ' selected' : ''}>Join link (${linkCount})</option></select></label></div>
<div class="field"><label>Age range<select class="select" id="app-filter-age"><option value=""${appAge === '' ? ' selected' : ''}>Any</option>${Object.entries(ui.AGE_RANGE_LABELS).map(([v, l]) => `<option value="${esc(v)}"${appAge === v ? ' selected' : ''}>${esc(l)}</option>`).join('')}</select></label></div>
<div class="field"><label>Country<select class="select" id="app-filter-country"><option value=""${appCountry === '' ? ' selected' : ''}>Any</option>${countryChoices.map(c => `<option value="${esc(c.code)}"${appCountry === c.code ? ' selected' : ''}>${esc(c.name)}</option>`).join('')}</select></label></div>
<div class="field"><label>Sort<select class="select" id="app-filter-sort"><option value="oldest"${appSort === 'oldest' ? ' selected' : ''}>Oldest first</option><option value="newest"${appSort === 'newest' ? ' selected' : ''}>Newest first</option></select></label></div>
</div>` : ''

    main.innerHTML = `
<div class="ph"><div class="t"><h1>People</h1><p>${pending.length ? pending.length + ' application' + (pending.length === 1 ? '' : 's') + ' waiting on you' : 'Nobody is waiting for approval'}</p></div></div>
${showApplications ? `<h2 class="section-title">Applications</h2>${pending.length ? `${filterRow}${visiblePending.length ? `<div id="app-list">${visiblePending.map(applicationCard).join('')}</div>` : `<div class="note">Nobody waiting matches these filters.${filtersActive ? ' <button class="btn sm" type="button" id="app-filter-clear">Clear filters</button>' : ''}</div>`}` : `<div class="note">Sign-up isn't open yet. Move the event to ${ui.esc(ui.stageLabel('open'))} to start taking applications.</div>`}` : ''}

<section class="card"><h2>Everyone</h2>
<div class="field inline" id="judges"><label for="role-filter">Show</label><select class="select" id="role-filter" data-admin-live><option${roleFilter === 'Everyone' ? ' selected' : ''}>Everyone</option><option${roleFilter === 'Organisers' ? ' selected' : ''}>Organisers</option><option${roleFilter === 'Participants' ? ' selected' : ''}>Participants</option><option${roleFilter === 'Judges' ? ' selected' : ''}>Judges</option><option${roleFilter === 'Co-organisers' ? ' selected' : ''}>Co-organisers</option></select></div>
<div class="list" id="people-list">${visiblePeople.length ? visiblePeople.map(personRow).join('') : '<div class="empty">Nobody matches that filter.</div>'}</div>
</section>

<p><a href="${ui.esc(resolve('o.signup', ev.slug).href)}">Sign-up settings</a> · <a href="${ui.esc(resolve('o.organisers', ev.slug).href)}">Organisers</a></p>`

    adminLock(main, admin, ev)


    $('role-filter').addEventListener('change', () => { roleFilter = $('role-filter').value; paint() })

    const fromSel = $('app-filter-from')
    if (fromSel) fromSel.addEventListener('change', () => { appFrom = fromSel.value; paint() })
    const ageSel = $('app-filter-age')
    if (ageSel) ageSel.addEventListener('change', () => { appAge = ageSel.value; paint() })
    const countrySel = $('app-filter-country')
    if (countrySel) countrySel.addEventListener('change', () => { appCountry = countrySel.value; paint() })
    const sortSel = $('app-filter-sort')
    if (sortSel) sortSel.addEventListener('change', () => { appSort = sortSel.value; paint() })
    const clearBtn = $('app-filter-clear')
    if (clearBtn) clearBtn.addEventListener('click', () => { appFrom = 'all'; appAge = ''; appCountry = ''; appSort = 'oldest'; paint() })

    main.querySelectorAll('[data-app-approve]').forEach(btn => btn.addEventListener('click', () => decide(btn, 'approved')))
    main.querySelectorAll('[data-app-reject]').forEach(btn => btn.addEventListener('click', () => decide(btn, 'rejected')))
    async function decide(btn, decision) {
      const card = btn.closest('[data-app]')
      const id = card.getAttribute('data-app')
      if (decision === 'rejected') {
        const ok = await ui.confirm({ title: 'Reject this application?', body: btn.getAttribute('title'), confirmLabel: 'Reject', danger: true })
        if (!ok) return
      }
      btn.disabled = true
      try {
        const r = await api('POST', path + '/applications/' + encodeURIComponent(id) + '/decision', { decision })
        if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
        const [a2, p2] = await Promise.all([api('GET', path + '/applications'), api('GET', path + '/people')])
        if (a2.ok) applications = a2.data || []
        if (p2.ok) people = p2.data || []
        setLive('Application ' + decision + '.', 'ok')
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    }

    main.querySelectorAll('[data-remove-person]').forEach(btn => btn.addEventListener('click', async () => {
      const id = btn.getAttribute('data-remove-person'); const name = btn.getAttribute('data-name') || 'this person'; const role = btn.getAttribute('data-role')
      const body = role === 'judge'
        ? name + ' is removed from the event. They can rejoin with the current join link unless you regenerate it.'
        : name + ' and their team membership are removed. They can rejoin with the current join link unless you regenerate it, or apply again while applications are on — you’d decide on that application.'
      const ok = await ui.confirm({ title: 'Remove ' + name + ' from this event?', body, confirmLabel: 'Remove', danger: true })
      if (!ok) return
      btn.disabled = true
      try {
        const r = await api('DELETE', path + '/people/' + encodeURIComponent(id))
        if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
        const p2 = await api('GET', path + '/people')
        if (p2.ok) people = p2.data || []
        setLive('Removed', 'ok')
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    }))
    main.querySelectorAll('[data-turn-off-key]').forEach(btn => btn.addEventListener('click', async () => {
      const id = btn.getAttribute('data-turn-off-key'); const name = btn.getAttribute('data-name') || 'this person'
      const ok = await ui.confirm({ title: "Turn off " + name + "'s key?", body: "Their key stops working at once. They'd need to create a new one themselves.", confirmLabel: 'Turn off key', danger: true })
      if (!ok) return
      btn.disabled = true
      try {
        const r = await api('DELETE', path + '/people/' + encodeURIComponent(id) + '/key')
        if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
        const p2 = await api('GET', path + '/people')
        if (p2.ok) people = p2.data || []
        setLive('Key turned off', 'ok')
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    }))

  }

  paint()
}
