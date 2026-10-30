import { href } from '../nav.js'
import { richConfirm, slugify } from './_orgB-util.js'

export function title() { return 'Teams' }

export async function render(ctx) {
  const { main, api: client, ui, event, role } = ctx
  const api = client.api
  const slug = event.event.slug
  const $ = id => main.querySelector('#' + id)
  const setLive = (msg, bad) => { if (msg) ui.toast(msg, main, bad) }

  let ev = event.event
  let teams = [], teamSizeMax = ev.team_size_max, onNoTeam = []
  let lookingFor = {} // slug -> looking_for text, only for open teams
  let tracks = (ev.tracks || []).map(t => ({ ...t }))

  async function load() {
    const [tr, open] = await Promise.all([
      api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams'),
      ev.team_finding ? api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/open') : Promise.resolve({ ok: true, data: { teams: [] } })
    ])
    if (!tr.ok) { setLive(tr.data.error || 'Could not load teams', true); return }
    teams = tr.data.teams || []
    teamSizeMax = tr.data.team_size_max
    onNoTeam = tr.data.on_no_team || []
    lookingFor = {}
    for (const t of (open.data && open.data.teams) || []) lookingFor[t.slug] = t.looking_for || ''
  }

  function teamSignal(t) {
    if (!ev.team_finding) return ''
    const note = lookingFor[t.slug]
    return note !== undefined && note ? 'Looking for ' + note : 'Not looking'
  }

  const fillable = ['open', 'building'].includes(ev.stage)

  function row(t) {
    const members = t.members.length
    const trackName = t.track ? t.track.name : 'No track'
    const signal = teamSignal(t)
    return `<a class="r" href="${ui.esc(href(role, slug, 'team') + '&team=' + encodeURIComponent(t.slug))}"><span class="main"><b>${ui.esc(t.name)}</b><span>${ui.esc(trackName)}</span></span><span class="meta">${members} of ${teamSizeMax}${signal ? ' · ' + ui.esc(signal) : ''}</span></a>`
  }

  function trackCard(t, i) {
    const n = teams.filter(team => team.track && team.track.slug === t.slug).length
    return `<div class="field"><label>Track<input class="input" data-track="${i}" data-f="name" type="text" value="${ui.esc(t.name)}"></label></div>
<div class="field"><label>Challenge<textarea class="input" data-track="${i}" data-f="challenge">${ui.esc(t.challenge)}</textarea></label></div>
<div class="field"><label>Prize<input class="input" data-track="${i}" data-f="prize" type="text" value="${ui.esc(t.prize)}"></label></div>
<section class="danger-zone"><div class="setting"><div class="t"><button class="btn danger" type="button" data-remove-track="${i}">Remove track</button>${n ? `<span class="small muted"> · ${n} team${n === 1 ? '' : 's'} currently chose this</span>` : ''}</div></div></section>`
  }

  function paint() {
    main.innerHTML = `<div class="ph"><div class="t"><h1>Teams</h1><p>${teams.length} team${teams.length === 1 ? '' : 's'} · ${onNoTeam.length} ${onNoTeam.length === 1 ? 'person' : 'people'} still solo</p></div><button class="btn primary" type="button" id="fill-teams"${!fillable || !onNoTeam.length ? ' disabled' : ''}>${!onNoTeam.length ? 'Everyone’s on a team' : 'Fill teams'}</button></div>
${!fillable && onNoTeam.length ? `<div class="note warn">Fill teams only works while the event is Open or Building.</div>` : ''}
${!ev.team_finding ? `<div class="note">Team finding is off. Teams form by invite code only.</div>` : ''}
<div class="list">${teams.length ? teams.map(row).join('') : '<p class="empty">No teams yet.</p>'}</div>
<p class="small muted">Manage a team by opening it above. <a href="${ui.esc(href(role, slug, 'results'))}">Download teams CSV</a> is in Results → Export.</p>
<details class="card"><summary>Team finding</summary>
<label class="check"><input type="checkbox" id="tf-on"${ev.team_finding ? ' checked' : ''}${fillable ? '' : ' disabled'}><div><b>Team finding</b><span>Let participants find teams and invite people. Team codes always work.</span></div></label>
<div class="field"><label>Most people per team<input class="input" id="tf-size" type="number" min="1" max="50" value="${teamSizeMax}"${fillable ? '' : ' disabled'}></label></div>
${fillable ? '' : '<p class="help">Team finding and team size no longer change once the event is past Building.</p>'}
<button class="btn primary" type="button" id="tf-save"${fillable ? '' : ' disabled'}>Save</button>
</details>
<details class="card"><summary>Tracks and challenges</summary>
${tracks.map(trackCard).join('')}
<button class="btn" type="button" id="track-add">Add track</button>
<button class="btn primary" type="button" id="track-save">Save tracks</button>
</details>`
    wire()
  }

  function wire() {
    const fillBtn = $('fill-teams')
    if (fillBtn) fillBtn.addEventListener('click', onFillTeams)
    const tfSave = $('tf-save')
    if (tfSave) tfSave.addEventListener('click', onSaveTeamFinding)
    const trackAdd = $('track-add')
    if (trackAdd) trackAdd.addEventListener('click', () => {
      tracks.push({ slug: '', name: '', challenge: '', prize: '' })
      paint()
    })
    const trackSave = $('track-save')
    if (trackSave) trackSave.addEventListener('click', onSaveTracks)
    main.querySelectorAll('[data-track]').forEach(el => {
      el.addEventListener('input', () => {
        const i = Number(el.getAttribute('data-track'))
        const f = el.getAttribute('data-f')
        tracks[i][f] = el.value
      })
    })
    main.querySelectorAll('[data-remove-track]').forEach(btn => {
      btn.addEventListener('click', () => onRemoveTrack(Number(btn.getAttribute('data-remove-track'))))
    })
  }

  async function onFillTeams() {
    setLive('')
    const btn = $('fill-teams')
    btn.disabled = true
    let r
    try { r = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/fill', { apply: false }) }
    catch (e) { if (e.message !== 'signed out') setLive('Cannot reach the server.', true); btn.disabled = false; return }
    if (!r.ok) { setLive(r.data.error || 'Could not plan teams', true); btn.disabled = false; return }
    const plan = r.data.plan || []
    if (!plan.length) { setLive('Nobody is left to place.'); btn.disabled = false; return }
    const list = document.createElement('ul')
    list.className = 'steps'
    for (const p of plan) {
      const li = document.createElement('li')
      li.className = 'done'
      const names = p.members.map(m => m.display_name).join(', ')
      const what = document.createElement('div')
      what.className = 'what'
      const b = document.createElement('b')
      b.textContent = p.new ? 'New team: ' + p.name : p.name
      const span = document.createElement('span')
      span.textContent = names
      what.append(b, span)
      const tick = document.createElement('span')
      tick.className = 'tick'
      tick.textContent = '✓'
      li.append(tick, what)
      list.append(li)
    }
    const note = document.createElement('p')
    note.className = 'small muted'
    note.textContent = 'Membership may have shifted since this preview was made.'
    const ok = await richConfirm({ title: 'Fill teams?', bodyNodes: [list, note], confirmLabel: 'Apply' })
    btn.disabled = false
    if (!ok) return
    let ar
    try { ar = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/fill', { apply: true }) }
    catch (e) { if (e.message !== 'signed out') setLive('Cannot reach the server.', true); return }
    if (!ar.ok) { setLive(ar.data.error || 'Could not fill teams', true); return }
    await load()
    paint()
    ui.saved(main)
  }

  async function onSaveTeamFinding() {
    setLive('')
    const btn = $('tf-save')
    const on = $('tf-on').checked
    const size = Number($('tf-size').value)
    if (!size || size < 1 || size > 50) { setLive('Most people per team must be between 1 and 50.', true); return }
    btn.disabled = true
    let r
    try { r = await api('PATCH', '/v1/hack/events/' + encodeURIComponent(slug), { team_finding: on, team_size_max: size }) }
    catch (e) { if (e.message !== 'signed out') setLive('Cannot reach the server.', true); btn.disabled = false; return }
    if (!r.ok) { setLive(r.data.error || 'Could not save', true); btn.disabled = false; return }
    ev = r.data.event
    await load()
    paint()
    ui.saved(main)
  }

  async function onRemoveTrack(i) {
    const t = tracks[i]
    const n = teams.filter(team => team.track && t.slug && team.track.slug === t.slug).length
    const body = n
      ? `${n} team${n === 1 ? '' : 's'} currently have ${t.name || 'this track'} chosen — they'll be left with no track. Remove it anyway?`
      : `Remove ${t.name || 'this track'}?`
    const ok = await ui.confirm({ title: 'Remove track?', body, confirmLabel: 'Remove track', danger: true })
    if (!ok) return
    const next = tracks.slice(0, i).concat(tracks.slice(i + 1))
    await saveTracks(next)
  }

  async function onSaveTracks() {
    await saveTracks(tracks)
  }

  async function saveTracks(list) {
    const payload = list.map(t => ({ slug: t.slug || slugify(t.name), name: t.name, challenge: t.challenge, prize: t.prize }))
    let r
    try { r = await api('PUT', '/v1/hack/events/' + encodeURIComponent(slug) + '/tracks', { tracks: payload }) }
    catch (e) { if (e.message !== 'signed out') setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(r.data.error || 'Could not save tracks', true); return }
    tracks = (r.data || []).map(t => ({ ...t }))
    await load()
    paint()
    ui.saved(main)
  }

  await load()
  paint()
}
