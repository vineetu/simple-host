import { href } from '../nav.js'
import { labelList, joinAnd } from './_orgB-util.js'
import { rowMenuHTML, wireRowMenus, formDialog } from './_orgA-util.js'
import { openTeamMessageDialog, messageRowsHTML } from './_team-message-dialog.js'

export function title(ctx) {
  return 'Team'
}

// pickMoveTarget: "Move to another team…" is a row-menu item here (not an
// inline select, like the old one-page version) -- it opens a small dialog
// with the one choice to make, built on the shared formDialog primitive so
// it gets the same focus-trap/Escape/Tab behaviour as every other dialog.
// Returns the chosen team's slug, or '' on cancel / nothing to choose.
async function pickMoveTarget(ui, esc, name, otherTeams, save) {
  if (!otherTeams.length) {
    await ui.dialog({ title: 'Move ' + name, body: 'No other team has room.', buttons: [{ label: 'Close', value: true, className: 'btn primary' }] })
    return ''
  }
  const body = document.createElement('div')
  body.innerHTML = `<div class="field"><label for="move-target">Team</label><select class="select" id="move-target">${otherTeams.map(t => `<option value="${esc(t.slug)}">${esc(t.name)}</option>`).join('')}</select></div>`
  const ok = await formDialog(ui, { title: 'Move ' + name + ' to another team', body, saveLabel: 'Move' }, () => save(body.querySelector('#move-target').value, body))
  return ok ? body.querySelector('#move-target').value : ''
}

export async function render(ctx) {
  const { main, api: client, ui, event, role, params, go } = ctx
  const api = client.api
  const ev = event.event
  const slug = ev.slug
  const teamSlug = params.get('team') || ''
  const $ = id => main.querySelector('#' + id)
  const setLive = ui.live(main)
  const esc = ui.esc

  wireRowMenus()

  let teams = [], team = null, members = [], entryItem = null, required = [], onNoTeam = [], messages = []

  async function load() {
    const [tr, pr, er, mr] = await Promise.all([
      api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams'),
      api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/people'),
      api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/entries'),
      api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug) + '/messages')
    ])
    if (!tr.ok) { setLive(tr.data.error || 'Could not load the team', true); return false }
    teams = tr.data.teams || []
    team = teams.find(t => t.slug === teamSlug)
    if (!team) { setLive('Team not found.', true); return false }
    onNoTeam = tr.data.on_no_team || []
    const people = pr.ok ? pr.data || [] : []
    members = team.members.map(m => {
      const p = people.find(x => x.user_id === m.user_id)
      return { user_id: m.user_id, display_name: m.display_name, admin: !!m.admin, email: p ? p.email : '' }
    })
    if (er.ok) {
      required = er.data.required || []
      entryItem = (er.data.entries || []).find(e => e.team.slug === teamSlug) || null
    }
    messages = mr.ok ? (mr.data.messages || []) : []
    return true
  }

  function memberRow(m, lockReason) {
    const name = m.display_name || 'Someone'
    // Organiser hand placement works on an ordinarily locked team (the lock
    // is the team's own "no more joiners", not a block on the organiser) --
    // only a kept team (kept_after_leave) refuses placement outright, so
    // only kept teams are excluded here.
    const otherTeams = teams.filter(t => t.slug !== teamSlug && t.members.length < ev.team_size_max && !t.kept)
    const menuHTML = lockReason ? '' : `<div class="row-acts">${rowMenuHTML(name, [
      { label: 'Move to another team…', act: 'move' },
      m.admin ? { label: 'Remove as admin', act: 'unadmin' } : { label: 'Make admin', act: 'admin' },
      { label: 'Take off team', act: 'remove', danger: true }
    ])}</div>`
    return `<div class="r" data-member="${esc(m.user_id)}" data-name="${esc(name)}" data-other-teams="${esc(JSON.stringify(otherTeams.map(t => ({ slug: t.slug, name: t.name }))))}"><div class="main"><b>${esc(name)}</b><span>${esc(m.email)}</span></div>${m.admin ? '<span class="meta"><span class="pill accent">Admin</span></span>' : ''}${menuHTML}</div>`
  }

  function entryPill() {
    if (!entryItem || !required.length) return { cls: 'ok', text: 'No fields required' }
    const missing = entryItem.missing || []
    if (!missing.length) return { cls: 'ok', text: 'Complete' }
    return { cls: 'warn', text: 'Missing: ' + joinAnd(labelList(missing)) }
  }

  function sitePill() {
    if (!team.site || !team.site.exists) return { cls: '', text: 'Not published' }
    return team.site.taken_down ? { cls: 'warn', text: 'Taken down' } : { cls: 'ok', text: 'Live' }
  }

  function paint() { return ui.forms.preserve(ctx, () => paintContent()) }
  function paintContent() {
    const trackName = team.track ? team.track.name : 'No track'
    const lockReason = ui.writeLockReason(ev, event.admin_view)
    const full = team.members.length >= ev.team_size_max
    const addDisabled = !!lockReason || full || !onNoTeam.length
    const ep = entryPill(), sp = sitePill()
    const lockedPill = team.locked && !team.kept ? ' <span class="pill warn">Locked</span>' : ''
    const link = team.link || (team.code ? location.origin + '/team/' + team.code : '')
    const addr = link.replace(/^https?:\/\//, '')

    let membersBlock
    if (team.kept) {
      membersBlock = `<div class="sp-head"><h2>Members</h2><p>Nobody is on this team now.</p></div>`
    } else {
      // Round-6 Confusing 2: same one-clear-rule fix as participant-team.js's
      // Members header -- this page's own Joining card below already says
      // "Anyone in this event who opens it joins straight away."
      membersBlock = `<div class="sp-head"><h2>Members</h2><p>Anyone with the team link joins straight away. Admins approve requests to join. Make someone admin if the team has lost theirs.</p>${lockReason ? '' : `<button class="btn" type="button" id="add-someone"${addDisabled ? ' disabled' : ''}><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 4v12M4 10h12"/></svg>Add someone</button>`}</div>${full ? '<p class="help">' + esc(team.name) + ' is full.</p>' : ''}
${team.locked ? `<div class="note warn" role="status">Locked by the team. You can still place people here.</div>` : ''}
<div class="list editable" aria-label="Members">${members.length ? members.map(m => memberRow(m, lockReason)).join('') : '<div class="empty">No members.</div>'}</div>`
    }

    main.innerHTML = `<p class="crumbs"><a href="${esc(href(role, slug, 'teams'))}">Teams</a> / ${esc(team.name)}</p>
<div class="ph"><div class="t"><h1>${esc(team.name)}${lockedPill}</h1><p>${esc(trackName)} · ${team.members.length} of ${ev.team_size_max} people</p></div>${entryItem ? `<a class="btn ph-link" href="${esc(href(role, slug, 'entry') + '&team=' + encodeURIComponent(teamSlug))}">View entry</a>` : ''}</div>
${lockReason ? `<div class="note warn">${esc(lockReason)}</div>` : ''}
${team.kept ? `<div class="note" role="status">Everyone left after the deadline. ${esc(team.name)} keeps its entry, site and scores, and can’t take new members.</div>` : ''}
${membersBlock}
<section class="card"><h2>Team</h2><form id="rename-form"><div class="field"><label for="rename-name">Team name</label><div class="control-row"><input class="input" id="rename-name" type="text" maxlength="80" value="${esc(team.name)}"${lockReason ? ' disabled' : ''}><button class="btn" type="submit"${lockReason ? ' disabled' : ''}>Save</button></div></div></form></section>
<section class="card" id="messages"><h2>Messages</h2>${messages.length ? messageRowsHTML(ui, ev, messages, { withSender: true }) : '<p class="muted">No messages sent to this team yet.</p>'}${ev.stage === 'archived' ? '' : `<div class="form-actions"><button class="btn" type="button" id="msg-team-btn">Message team</button></div>`}</section>
${team.kept ? '' : `<section class="card"><h2>Joining</h2>
<div class="setting"><div class="t"><b>Lock team</b><span>No more people can join by link, code, request or invite, and Fill teams skips it. You can still place people here.</span></div><div class="ctl"><button class="toggle${team.locked ? ' on' : ''}" type="button" role="switch" aria-checked="${team.locked ? 'true' : 'false'}" aria-label="Lock team" id="team-lock-toggle"${lockReason ? ' disabled' : ''}></button></div></div>
<div class="setting wraps"><div class="t"><b>Team link</b><span>Anyone in this event who opens it joins straight away.</span></div><div class="ctl addr"><span class="mono">${esc(addr)}</span><button class="btn" type="button" id="team-link-copy">Copy</button><button class="btn" type="button" id="team-link-new"${lockReason ? ' disabled' : ''}>New link</button></div></div>
</section>`}
<section class="card"><h2>Entry &amp; site</h2><dl><dt>Entry</dt><dd><span class="pill ${ep.cls}">${esc(ep.text)}</span></dd><dt>Site</dt><dd><span class="pill ${sp.cls}">${esc(sp.text)}</span></dd></dl><a class="btn" href="${esc(href(role, slug, 'entry') + '&team=' + encodeURIComponent(teamSlug))}">View entry</a></section>
<section class="danger-zone"><h2>Danger zone</h2><div class="setting"><div class="t"><b>Delete this team</b><span>Removes ${esc(team.name)}, its entry and its site for good. Members stay in the event without a team.</span></div><button class="btn danger" type="button" id="delete-team"${lockReason ? ' disabled' : ''}>Delete team</button></div></section>`
    wire()
  }

  function wire() {
    const addBtn = $('add-someone')
    if (addBtn) addBtn.addEventListener('click', onAddSomeone)
    ui.forms.bind(ctx, $('rename-form'), onRename)
    const msgBtn = $('msg-team-btn')
    if (msgBtn) msgBtn.addEventListener('click', onMessageTeam)
    const lockToggle = $('team-lock-toggle')
    if (lockToggle) lockToggle.addEventListener('click', onToggleLock)
    const linkCopy = $('team-link-copy')
    if (linkCopy) linkCopy.addEventListener('click', () => copyLink(linkCopy))
    const linkNew = $('team-link-new')
    if (linkNew) linkNew.addEventListener('click', onNewLink)
    main.querySelectorAll('[data-rowmenu]').forEach(btn => {
      btn.closest('.row-acts').querySelectorAll('.row-menu button').forEach(item => {
        item.addEventListener('click', () => onRowAct(item, btn.closest('[data-member]')))
      })
    })
    $('delete-team').addEventListener('click', onDelete)
  }

  async function copyLink(btn) {
    const link = team.link || (team.code ? location.origin + '/team/' + team.code : '')
    try { await navigator.clipboard.writeText(link); btn.textContent = 'Copied'; setTimeout(() => { btn.textContent = 'Copy' }, 2000) }
    catch (e) { setLive('Could not copy. Select the text and copy it.', true, btn) }
  }

  async function onAddSomeone() {
    if (!onNoTeam.length) { setLive('Everyone is on a team.', true); return }
    const body = document.createElement('div')
    body.innerHTML = `<div class="field"><label for="add-pick">Solo participant</label><select class="select" id="add-pick">${onNoTeam.map(p => `<option value="${esc(p.user_id)}">${esc(p.display_name || p.email || 'Someone')}</option>`).join('')}</select></div>`
    const ok = await formDialog(ui, { title: 'Add someone to ' + team.name, body, saveLabel: 'Add' }, async () => {
      const userID = body.querySelector('#add-pick').value
      if (!userID) return false
      try {
        const r = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug) + '/members', { user_id: userID })
        if (!r.ok) { ui.toast(ui.errText(r, { fallback: 'Could not move them.' }), body, true); return false }
        return true
      } catch (err) { if (!client.isSignedOut(err)) ui.toast('Cannot reach the server.', body, true); return false }
    })
    if (!ok) return
    await load()
    paint()
    ui.saved(main)
  }

  async function onRowAct(item, row) {
    const act = item.getAttribute('data-act')
    const userID = row.getAttribute('data-member')
    const name = row.getAttribute('data-name')
    let otherTeams = []
    try { otherTeams = JSON.parse(row.getAttribute('data-other-teams') || '[]') } catch (e) {}
    if (act === 'move') {
      const target = await pickMoveTarget(ui, esc, name, otherTeams, async (target, body) => {
        try {
          const r = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(target) + '/members', { user_id: userID })
          if (!r.ok) { ui.toast(ui.errText(r, { fallback: 'Could not move them.' }), body, true); return false }
          return true
        } catch (err) { if (!client.isSignedOut(err)) ui.toast('Cannot reach the server.', body, true); return false }
      })
      if (!target) return
      await load(); paint(); ui.saved(main)
      return
    }
    if (act === 'admin' || act === 'unadmin') {
      let r
      try { r = await api('PUT', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug) + '/members/' + encodeURIComponent(userID) + '/admin', { admin: act === 'admin' }) }
      catch (err) { if (!client.isSignedOut(err)) setLive('Cannot reach the server.', true); return }
      if (!r.ok) { setLive(ui.errText(r), true); return }
      await load(); paint(); ui.saved(main)
      return
    }
    if (act === 'remove') {
      const ok = await ui.confirm({
        title: 'Remove ' + name + '?',
        body: name + ' loses access to this team’s entry and site. The team link changes. Remove ' + name + ' from ' + team.name + '?',
        confirmLabel: 'Take off team',
        danger: true
      })
      if (!ok) return
      let r
      try { r = await api('DELETE', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug) + '/members/' + encodeURIComponent(userID)) }
      catch (err) { if (!client.isSignedOut(err)) setLive('Cannot reach the server.', true); return }
      if (!r.ok) { setLive(r.data.error || 'Could not remove them', true); return }
      await load(); paint(); ui.saved(main)
    }
  }

  async function onMessageTeam() {
    const sent = await openTeamMessageDialog(ctx, { teamIds: [team.id], title: 'Message ' + team.name })
    if (!sent) return
    const n = sent.reduce((s, m) => s + (m.recipients || 0), 0)
    await load()
    paint()
    ui.toast('Sent to ' + n + (n === 1 ? ' person' : ' people'), main)
  }

  async function onRename(e) {
    e.preventDefault()
    setLive('')
    const name = $('rename-name').value.trim()
    if (!name) { setLive('Enter a team name.', true); return }
    let r
    try { r = await api('PATCH', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug), { name }) }
    catch (err) { if (!client.isSignedOut(err)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(ui.errText(r, { codes: ui.ERROR_MESSAGES, fallback: 'Could not rename the team.' }), true); return }
    await load()
    paint()
    ui.saved(main)
  }

  // spec.json organiser-team-detail "Lock team (toggle)" has confirm: null
  // -- unlike the participant's own Settings lock toggle (which does
  // confirm), the organiser's toggle here saves on change like Team
  // finding on Teams > Settings. The response's own `cleared` counts (not
  // guessed ahead) go into the "Saved" note when there is something to say.
  async function onToggleLock() {
    const wantLocked = !team.locked
    let r
    try { r = await api('PUT', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug) + '/lock', { locked: wantLocked }) }
    catch (err) { if (!client.isSignedOut(err)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(ui.errText(r), true); return }
    await load()
    paint()
    const cleared = r.data && r.data.cleared
    const n = cleared ? (cleared.requests || 0) + (cleared.invites || 0) : 0
    ui.saved(main)
    if (n) setLive(n + ' pending request' + (n === 1 ? '' : 's') + ' and invites were cleared.')
  }

  async function onNewLink() {
    const ok = await ui.confirm({ title: 'Make a new link for ' + team.name + '?', body: 'The old link and code stop working. Everyone on the team stays.', confirmLabel: 'New link', danger: true })
    if (!ok) return
    let r
    try { r = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug) + '/link') }
    catch (err) { if (!client.isSignedOut(err)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(ui.errText(r), true); return }
    await load()
    paint()
    ui.saved(main)
  }

  async function onDelete() {
    const names = joinAnd(members.map(m => m.display_name))
    const ok = await ui.confirm({
      title: 'Delete ' + team.name + '?',
      body: 'This removes ' + team.name + ', its entry and its site.' + (names ? ' ' + names + ' become' : ' Members become') + ' unteamed and can join or start another team. This can’t be undone. Delete ' + team.name + '?',
      confirmLabel: 'Delete team',
      danger: true
    })
    if (!ok) return
    let r
    try { r = await api('DELETE', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug)) }
    catch (err) { if (!client.isSignedOut(err)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(r.data.error || 'Could not delete the team', true); return }
    go(href(role, slug, 'teams'))
  }

  if (await load()) paint()
}
