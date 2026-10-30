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

  function memberRow(m, locked) {
    // Full teams aren't offered as a move target (moving into one would
    // just fail), matching move-here's own full-team guard below.
    const others = teams.filter(t => t.slug !== teamSlug && t.members.length < ev.team_size_max)
    const opts = others.map(t => `<option value="${ui.esc(t.slug)}">${ui.esc(t.name)}</option>`).join('')
    return `<div class="r"><div class="main"><b>${ui.esc(m.display_name)}</b><span>${m.joined_at ? 'Joined ' + ui.esc(ui.dateTime(m.joined_at, ev)) : ''}</span></div><div class="row-flex">
<select class="select" data-move-select data-user="${ui.esc(m.user_id)}" aria-label="Move ${ui.esc(m.display_name)} to another team"${locked ? ' disabled' : ''}><option value="">Move to…</option>${opts}</select>
<button class="btn sm" type="button" data-move="${ui.esc(m.user_id)}" disabled>Move</button>
<button class="btn danger sm" type="button" data-remove="${ui.esc(m.user_id)}" data-name="${ui.esc(m.display_name)}"${locked ? ' disabled' : ''}>Remove</button>
</div></div>`
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

  function paint() {
    const trackName = team.track ? team.track.name : 'No track'
    const lockReason = ui.writeLockReason(ev, event.admin_view)
    const full = team.members.length >= ev.team_size_max
    const moveHereDisabled = !!lockReason || full || !onNoTeam.length
    const ep = entryPill(), sp = sitePill()
    main.innerHTML = `<p class="crumbs"><a href="${ui.esc(href(role, slug, 'teams'))}">Teams</a> / ${ui.esc(team.name)}</p>
<div class="ph"><div class="t"><h1>Team: ${ui.esc(team.name)}</h1><p>${ui.esc(trackName)} · ${team.members.length} of ${ev.team_size_max} people</p></div></div>
${lockReason ? `<div class="note warn">${ui.esc(lockReason)}</div>` : ''}
<section class="card"><h2>Team</h2><form id="rename-form"><div class="row-flex"><div class="field"><label>Team name<input class="input" id="rename-name" type="text" maxlength="80" value="${ui.esc(team.name)}"${lockReason ? ' disabled' : ''}></label></div><button class="btn" type="submit"${lockReason ? ' disabled' : ''}>Rename</button></div></form>
<p class="small muted">Join code <span class="mono">${ui.esc(team.code)}</span></p></section>
<h2 class="section-title">Members</h2>
<div class="list">${members.length ? members.map(m => memberRow(m, !!lockReason)).join('') : '<div class="empty">No members.</div>'}</div>
<section class="card"><h2>Add someone</h2>
${onNoTeam.length ? `<div class="field"><label>Solo participant<select class="select" id="move-here-select"${lockReason || full ? ' disabled' : ''}><option value="">Choose someone…</option>${onNoTeam.map(p => `<option value="${ui.esc(p.user_id)}">${ui.esc(p.display_name || p.email || 'Someone')}</option>`).join('')}</select></label></div>
<button class="btn" type="button" id="move-here-save"${moveHereDisabled ? ' disabled' : ''}>Move here</button>${full ? '<p class="help">This team is full.</p>' : ''}` : '<p class="muted">Everyone is on a team.</p>'}
</section>
<section class="card"><h2>Entry &amp; site</h2><dl><dt>Entry</dt><dd><span class="pill ${ep.cls}">${ui.esc(ep.text)}</span></dd><dt>Site</dt><dd><span class="pill ${sp.cls}">${ui.esc(sp.text)}</span></dd></dl><a class="btn" href="${ui.esc(href(role, slug, 'entry') + '&team=' + encodeURIComponent(teamSlug))}">View entry</a></section>
<section class="danger-zone"><h2>Danger zone</h2><div class="setting"><div class="t"><b>Delete this team</b><span>Removes ${ui.esc(team.name)}, its entry and its site for good. Members stay in the event without a team.</span></div><button class="btn danger" type="button" id="delete-team"${lockReason ? ' disabled' : ''}>Delete team</button></div></section>`
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
    if (!userID) { setLive('Choose someone.', true, sel); return }
    setLive('')
    let r
    try { r = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug) + '/members', { user_id: userID }) }
    catch (err) { if (!client.isSignedOut(err)) setLive('Cannot reach the server.', true, sel); return }
    // Shown next to the select it came from, not at the bottom of <main>,
    // and through ui.errText's codes map so a plain server refusal like
    // "this team is full" reads as the capitalised client copy instead.
    if (!r.ok) { setLive(ui.errText(r, { codes: ui.ERROR_MESSAGES, fallback: 'Could not move them.' }), true, sel); return }
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
    // S11: the server's own text here is lowercase jargon ("another team
    // already has that name") -- ui.ERROR_MESSAGES already has the
    // capitalised client copy for this code, same as onMoveHere/onMove
    // below already use it for team_full.
    if (!r.ok) { setLive(ui.errText(r, { codes: ui.ERROR_MESSAGES, fallback: 'Could not rename the team.' }), true); return }
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
    catch (err) { if (!client.isSignedOut(err)) setLive('Cannot reach the server.', true, sel); return }
    if (!r.ok) { setLive(ui.errText(r, { codes: ui.ERROR_MESSAGES, fallback: 'Could not move them.' }), true, sel); return }
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
