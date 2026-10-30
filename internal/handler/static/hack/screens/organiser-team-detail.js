import { href } from '../nav.js'
import { labelList, joinAnd } from './_orgB-util.js'

export function title(ctx) {
  return 'Team'
}

export async function render(ctx) {
  const { main, api: client, ui, event, role, params, go } = ctx
  const api = client.api
  const ev = event.event
  const slug = ev.slug
  const teamSlug = params.get('team') || ''
  const $ = id => main.querySelector('#' + id)
  const setLive = ui.live(main)

  let teams = [], team = null, members = [], entryItem = null, required = [], onNoTeam = []

  async function load() {
    const [tr, pr, er] = await Promise.all([
      api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams'),
      api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/people'),
      api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/entries')
    ])
    if (!tr.ok) { setLive(tr.data.error || 'Could not load the team', true); return false }
    teams = tr.data.teams || []
    team = teams.find(t => t.slug === teamSlug)
    if (!team) { setLive('Team not found.', true); return false }
    onNoTeam = tr.data.on_no_team || []
    const people = pr.ok ? pr.data || [] : []
    members = team.members.map(m => {
      const p = people.find(x => x.user_id === m.user_id)
      return { user_id: m.user_id, display_name: m.display_name, joined_at: p ? p.joined_at : null }
    })
    if (er.ok) {
      required = er.data.required || []
      entryItem = (er.data.entries || []).find(e => e.team.slug === teamSlug) || null
    }
    return true
  }

  function entrySummary() {
    if (!entryItem || !required.length) return 'No entry fields are required.'
    const missing = entryItem.missing || []
    const filled = required.filter(f => !missing.includes(f))
    if (!missing.length) return joinAnd(labelList(filled)) + ' filled. Complete.'
    if (!filled.length) return 'No entry fields filled yet. Missing: ' + joinAnd(labelList(missing)) + '.'
    return joinAnd(labelList(filled)) + ' filled, ' + joinAnd(labelList(missing)) + ' missing.'
  }

  function siteSummary() {
    if (!team.site || !team.site.exists) return 'Site: not published yet.'
    return team.site.taken_down ? 'Site: taken down.' : 'Site: live.'
  }

  function memberRow(m, locked) {
    const others = teams.filter(t => t.slug !== teamSlug)
    const opts = others.map(t => `<option value="${ui.esc(t.slug)}">${ui.esc(t.name)}</option>`).join('')
    return `<div class="r"><div class="main"><b>${ui.esc(m.display_name)}</b><span>${m.joined_at ? 'Joined ' + ui.esc(ui.dateTime(m.joined_at, ev)) : ''}</span><div class="row-flex">
<label class="small">Move to another team<select class="select" data-move-select data-user="${ui.esc(m.user_id)}" aria-label="Move ${ui.esc(m.display_name)} to another team"${locked ? ' disabled' : ''}><option value="">Move to…</option>${opts}</select></label>
<button class="btn" type="button" data-move="${ui.esc(m.user_id)}" disabled>Move to another team</button>
<button class="btn danger sm" type="button" data-remove="${ui.esc(m.user_id)}" data-name="${ui.esc(m.display_name)}"${locked ? ' disabled' : ''}>Remove from team</button>
</div></div></div>`
  }

  function paint() {
    const trackName = team.track ? team.track.name : 'No track'
    const lockReason = ui.writeLockReason(ev, event.admin_view)
    const full = team.members.length >= ev.team_size_max
    const moveHereDisabled = !!lockReason || full || !onNoTeam.length
    main.innerHTML = `<div class="ph"><div class="t"><h1>Team: ${ui.esc(team.name)}</h1><p>${ui.esc(trackName)} · ${team.members.length} of ${ev.team_size_max} people</p></div></div>
<p class="crumbs"><a href="${ui.esc(href(role, slug, 'teams'))}">← Back to Teams</a></p>
${lockReason ? `<div class="note warn">${ui.esc(lockReason)}</div>` : ''}
<section class="card"><h2>Team</h2><form id="rename-form"><div class="field"><label>Team name<input class="input" id="rename-name" type="text" maxlength="80" value="${ui.esc(team.name)}"${lockReason ? ' disabled' : ''}></label></div><button class="btn" type="submit"${lockReason ? ' disabled' : ''}>Rename</button></form>
<p>${ui.esc(trackName)} · Code: <span class="mono">${ui.esc(team.code)}</span> · ${team.members.length} of ${ev.team_size_max} people</p></section>
<h2 class="section-title">Members</h2>
<div class="list">${members.length ? members.map(m => memberRow(m, !!lockReason)).join('') : '<p class="empty">No members.</p>'}</div>
<section class="card"><h2>Move someone here</h2>
${onNoTeam.length ? `<div class="field"><label>Solo participant<select class="select" id="move-here-select"${lockReason || full ? ' disabled' : ''}><option value="">Choose someone…</option>${onNoTeam.map(p => `<option value="${ui.esc(p.user_id)}">${ui.esc(p.display_name || p.email || 'Someone')}</option>`).join('')}</select></label></div>
<button class="btn" type="button" id="move-here-save"${moveHereDisabled ? ' disabled' : ''}>Move here</button>${full ? '<p class="help">This team is full.</p>' : ''}` : '<p class="muted">No one is unteamed.</p>'}
</section>
<section class="card"><h2>Entry &amp; site</h2><p>Entry: ${ui.esc(entrySummary())}</p><p>${ui.esc(siteSummary())}</p><a class="btn" href="${ui.esc(href(role, slug, 'entry') + '&team=' + encodeURIComponent(teamSlug))}">View entry</a></section>
<section class="danger-zone"><h2>Danger zone</h2><div class="setting"><div class="t"><p>Delete team removes ${ui.esc(team.name)}, its entry and its site for good. Members become unteamed.</p><button class="btn danger" type="button" id="delete-team"${lockReason ? ' disabled' : ''}>Delete team</button></div></div></section>`
    wire()
  }

  function wire() {
    $('rename-form').addEventListener('submit', onRename)
    main.querySelectorAll('[data-move-select]').forEach(sel => {
      sel.addEventListener('change', () => {
        const user = sel.getAttribute('data-user')
        const btn = main.querySelector('[data-move="' + user + '"]')
        if (btn) btn.disabled = !sel.value
      })
    })
    main.querySelectorAll('[data-move]').forEach(btn => {
      btn.addEventListener('click', () => onMove(btn.getAttribute('data-move')))
    })
    main.querySelectorAll('[data-remove]').forEach(btn => {
      btn.addEventListener('click', () => onRemove(btn.getAttribute('data-remove'), btn.getAttribute('data-name')))
    })
    const moveHereSave = $('move-here-save')
    if (moveHereSave) moveHereSave.addEventListener('click', onMoveHere)
    $('delete-team').addEventListener('click', onDelete)
  }

  async function onMoveHere() {
    const sel = $('move-here-select')
    const userID = sel && sel.value
    if (!userID) { setLive('Choose someone.', true); return }
    setLive('')
    let r
    try { r = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug) + '/members', { user_id: userID }) }
    catch (err) { if (!client.isSignedOut(err)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(r.data.error || 'Could not move them', true); return }
    await load()
    paint()
    ui.saved(main)
  }

  async function onRename(e) {
    e.preventDefault()
    setLive('')
    const name = $('rename-name').value.trim()
    if (!name) { setLive('Enter a team name.', true); return }
    let r
    try { r = await api('PATCH', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug), { name }) }
    catch (err) { if (!client.isSignedOut(err)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(r.data.error || 'Could not rename the team', true); return }
    await load()
    paint()
    ui.saved(main)
  }

  async function onMove(userID) {
    const sel = main.querySelector('[data-move-select][data-user="' + userID + '"]')
    const target = sel && sel.value
    if (!target) return
    setLive('')
    let r
    try { r = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(target) + '/members', { user_id: userID }) }
    catch (err) { if (!client.isSignedOut(err)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(r.data.error || 'Could not move them', true); return }
    await load()
    paint()
    ui.saved(main)
  }

  async function onRemove(userID, name) {
    const ok = await ui.confirm({
      title: 'Remove ' + name + '?',
      body: name + ' loses access to this team’s entry and site. The team gets a new join code. Remove ' + name + ' from the team?',
      confirmLabel: 'Remove from team',
      danger: true
    })
    if (!ok) return
    setLive('')
    let r
    try { r = await api('DELETE', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug) + '/members/' + encodeURIComponent(userID)) }
    catch (err) { if (!client.isSignedOut(err)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(r.data.error || 'Could not remove them', true); return }
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
