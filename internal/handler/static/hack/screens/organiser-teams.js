import { href } from '../nav.js'
import { slugify } from './_orgB-util.js'

export function title() { return 'Teams' }

export async function render(ctx) {
  const { main, api: client, ui, event, role } = ctx
  const api = client.api
  const slug = event.event.slug
  const $ = id => main.querySelector('#' + id)
  const setLive = ui.live(main)

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

  const fillable = ['open', 'building'].includes(ev.stage)
  // Team finding and team size are a setup choice, not a Fill-teams
  // precondition: the API accepts the PATCH at any stage short of ended, so
  // a draft event can be configured before registration ever opens.
  const configurable = ev.stage !== 'archived'
  // S14: one note for the whole screen once the event has ended, not a
  // helper line sprinkled under just the Team finding card -- Tracks is
  // locked the same way, by the same `configurable` flag, below.
  const lockReason = configurable ? '' : "This event has ended. Settings can't change any more."
  // Confusing #1: the Tracks section must stay open across the repaint
  // that Add track / Save tracks / Remove track triggers, the same way
  // People's Co-organisers/Sign-up sections do -- otherwise the new row
  // (or the save) immediately folds the section shut again. Retest 3
  // Confusing #1: Team finding had the same bug -- Save folded it shut
  // right when "Saved" needed to be seen next to the button.
  let tracksOpen = false
  let teamFindingOpen = false

  function row(t) {
    const members = t.members.length
    const trackName = t.track ? t.track.name : ''
    // S16: a full team is never "looking for members" on this list, no
    // matter what the open-teams listing still says -- the participant
    // team list already guards this way.
    const looking = ev.team_finding && !!lookingFor[t.slug] && members < teamSizeMax
    const meta = members + ' of ' + teamSizeMax + ' people' + (looking ? ' · Looking for members' : '')
    return `<a class="r" href="${ui.esc(href(role, slug, 'team') + '&team=' + encodeURIComponent(t.slug))}"><span class="main"><b>${ui.esc(t.name)}</b>${trackName ? `<span>${ui.esc(trackName)}</span>` : ''}</span><span class="meta">${ui.esc(meta)}</span></a>`
  }

  function trackCard(t, i) {
    const n = teams.filter(team => team.track && team.track.slug === t.slug).length
    return `<div class="field"><label>Track<input class="input" data-track="${i}" data-f="name" type="text" value="${ui.esc(t.name)}"${configurable ? '' : ' disabled'}></label></div>
<div class="field"><label>Challenge<textarea class="input" data-track="${i}" data-f="challenge"${configurable ? '' : ' disabled'}>${ui.esc(t.challenge)}</textarea></label></div>
<div class="field"><label>Prize<input class="input" data-track="${i}" data-f="prize" type="text" value="${ui.esc(t.prize)}"${configurable ? '' : ' disabled'}></label></div>
<section class="danger-zone"><div class="setting"><div class="t"><button class="btn danger" type="button" data-remove-track="${i}"${configurable ? '' : ' disabled'}>Remove track</button>${n ? `<span class="small muted"> · ${n} team${n === 1 ? '' : 's'} currently chose this</span>` : ''}</div></div></section>`
  }

  function paint() {
    const subtitle = teams.length + ' team' + (teams.length === 1 ? '' : 's') + ' · ' + (onNoTeam.length ? onNoTeam.length + ' not on a team' : 'Everyone is on a team')
    main.innerHTML = `<div class="ph"><div class="t"><h1>Teams</h1><p>${ui.esc(subtitle)}</p></div>${fillable && onNoTeam.length ? `<button class="btn primary" type="button" id="fill-teams">Fill teams</button>` : ''}</div>
${lockReason ? `<div class="note warn" role="alert">${ui.esc(lockReason)}</div>` : ''}
${!ev.team_finding ? `<div class="note">Team finding is off. Teams form by invite code only.</div>` : ''}
<div class="list">${teams.length ? teams.map(row).join('') : '<div class="empty">No teams yet.</div>'}</div>
<p class="small muted"><a href="${ui.esc(href(role, slug, 'results'))}">Teams CSV is on Results</a></p>
<details class="card" id="team-finding"${teamFindingOpen ? ' open' : ''}><summary>Team finding</summary>
<label class="check"><input type="checkbox" id="tf-on"${ev.team_finding ? ' checked' : ''}${configurable ? '' : ' disabled'}><div><b>Team finding</b><span>Let participants find teams and invite people. Team codes always work.</span></div></label>
<div class="field"><label>Most people per team<input class="input" id="tf-size" type="number" min="1" max="50" value="${teamSizeMax}"${configurable ? '' : ' disabled'}></label></div>
<button class="btn" type="button" id="tf-save"${configurable ? '' : ' disabled'}>Save</button>
</details>
<details class="card" id="tracks"${tracksOpen ? ' open' : ''}><summary>Tracks and challenges</summary>
${tracks.map(trackCard).join('')}
<button class="btn" type="button" id="track-add"${configurable ? '' : ' disabled'}>Add track</button>
<button class="btn" type="button" id="track-save"${configurable ? '' : ' disabled'}>Save tracks</button>
</details>`
    wire()
  }

  function wire() {
    const fillBtn = $('fill-teams')
    if (fillBtn) fillBtn.addEventListener('click', onFillTeams)
    const tfSave = $('tf-save')
    if (tfSave) tfSave.addEventListener('click', onSaveTeamFinding)
    $('team-finding').addEventListener('toggle', () => { teamFindingOpen = $('team-finding').open })
    $('tracks').addEventListener('toggle', () => { tracksOpen = $('tracks').open })
    const trackAdd = $('track-add')
    if (trackAdd) trackAdd.addEventListener('click', () => {
      tracks.push({ slug: '', name: '', challenge: '', prize: '' })
      tracksOpen = true
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
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); btn.disabled = false; return }
    if (!r.ok) { setLive(r.data.error || 'Could not plan teams', true); btn.disabled = false; return }
    const plan = r.data.plan || []
    // Confusing #3 (retest 3): fill-teams deliberately never places a
    // stranger into a code-only ("only people with the code") team, even
    // with room -- its members chose not to take strangers. The server
    // reports that skip back in `skipped` (reason "code_only") so the
    // preview can say why, instead of silently starting a one-person
    // "Team N" while that team still had a seat free.
    const skipped = r.data.skipped || []
    function skipNote(s) { return `${s.name} takes people with its code only, so it was skipped.` }
    if (!plan.length) {
      setLive(skipped.length ? skipped.map(skipNote).join(' ') : 'Nobody is left to place.')
      btn.disabled = false
      return
    }
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
    const body = [list]
    if (skipped.length) {
      const note = document.createElement('p')
      note.className = 'small muted'
      note.textContent = skipped.map(skipNote).join(' ')
      body.push(note)
    }
    // Confusing #7: the stale-membership warning used to sit here
    // unconditionally, before anything could actually have changed --
    // moved below, where the apply response's own plan can be compared
    // against the one just previewed, so it only shows when membership
    // really did shift in between.
    const ok = await ui.confirm({ title: 'Fill teams?', body, confirmLabel: 'Apply' })
    btn.disabled = false
    if (!ok) return
    let ar
    try { ar = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/fill', { apply: true }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); return }
    if (!ar.ok) { setLive(ar.data.error || 'Could not fill teams', true); return }
    const shifted = planSignature(plan) !== planSignature(ar.data.plan || [])
    await load()
    paint()
    // Nit: membership shifting between preview and apply is informational,
    // not an error -- a plain note, and the apply still gets its "Saved".
    if (shifted) setLive('Saved. Team membership changed since the preview, so teams were filled a little differently than shown.')
    else ui.saved(main)
  }

  // Confusing #7: a plan is the same fill if every team has the same
  // members, regardless of array order -- used to tell a genuinely stale
  // preview from one that just happens to list teams in a different order.
  function planSignature(plan) {
    return JSON.stringify((plan || []).map(p => ({ team: p.team, members: p.members.map(m => m.user_id).slice().sort() })).sort((a, b) => a.team.localeCompare(b.team)))
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
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); btn.disabled = false; return }
    if (!r.ok) { setLive(r.data.error || 'Could not save', true); btn.disabled = false; return }
    ev = r.data.event
    teamFindingOpen = true
    await load()
    paint()
    ui.saved(main)
  }

  async function onRemoveTrack(i) {
    const t = tracks[i]
    const n = teams.filter(team => team.track && t.slug && team.track.slug === t.slug).length
    const body = n
      ? `${n} team${n === 1 ? '' : 's'} currently have ${t.name || 'this track'} chosen. They will have no track. Remove it anyway?`
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
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(r.data.error || 'Could not save tracks', true); return }
    // The server now returns tracks in the order they were saved
    // (ReplaceEventTracks gives each one a distinct created_at in submitted
    // order), so the response can be used directly -- no client-side
    // reordering workaround needed.
    tracks = (r.data || []).map(t => ({ ...t }))
    tracksOpen = true
    await load()
    paint()
    ui.saved(main)
  }

  await load()
  paint()
}
