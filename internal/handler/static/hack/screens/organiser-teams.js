import { href } from '../nav.js'
import { slugify } from './_orgB-util.js'
import { rowMenuHTML, wireRowMenus, formDialog, undoToast, adminLock } from './_orgA-util.js'

// Teams redesign (owner, 2026-10-31): Teams gets its own full design, like
// the Event page -- four sub-pages instead of one long page with two
// collapsed <details> cards. spec.json organiser-teams.sub_pages.
const TABS = ['teams', 'solo', 'tracks', 'settings']
const TAB_LABEL = { teams: 'Teams', solo: 'Not on a team', tracks: 'Tracks', settings: 'Settings' }

function resolveTab(ctx) {
  const t = ctx.params.get('tab') || ''
  return TABS.includes(t) ? t : 'teams'
}

export function title(ctx) {
  return TAB_LABEL[resolveTab(ctx)] + ' · Teams'
}

function focusHeading(main) {
  const h = main.querySelector('#sp-h')
  if (!h) return
  h.setAttribute('tabindex', '-1')
  h.focus()
}

export async function render(ctx) {
  const { main, api: client, ui, event, role, params } = ctx
  const api = client.api
  const slug = event.event.slug
  const $ = id => main.querySelector('#' + id)
  const setLive = ui.live(main)
  const esc = ui.esc

  wireRowMenus()

  let tab = ctx.params.get('tab') || ''
  tab = resolveTab(ctx)
  if (ctx.params.get('tab') !== tab) {
    const fixed = `${location.pathname}?s=teams&tab=${tab}`
    history.replaceState(history.state, '', fixed)
    ui.notePath(fixed)
  }

  const admin = !!event.admin_view
  let ev = event.event
  let teams = [], teamSizeMax = ev.team_size_max, onNoTeam = []
  // looking/intro per solo person: cross-referenced from the same
  // teams/open listing the Find-people and Ask-to-join screens already
  // use (organiser is an allowed caller there too) rather than a new
  // field on on_no_team -- the "Looking for a team · <intro>" / "Not
  // looking" line the atlas puts on every Solo row.
  let lookingByUser = {}
  let tracks = (ev.tracks || []).map(t => ({ ...t }))

  async function load() {
    const calls = [api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams')]
    if (ev.team_finding) calls.push(api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/open'))
    const [tr, open] = await Promise.all(calls)
    if (!tr.ok) { setLive(tr.data.error || 'Could not load teams', true); return }
    teams = tr.data.teams || []
    teamSizeMax = tr.data.team_size_max
    onNoTeam = tr.data.on_no_team || []
    lookingByUser = {}
    const solo = (open && open.ok && open.data && open.data.solo) || []
    for (const p of solo) lookingByUser[p.user_id] = p.intro || ''
  }

  function teamRow(t) {
    const trackName = t.track ? t.track.name : 'No track'
    const admins = t.members.filter(m => m.admin).map(m => m.display_name || 'Someone')
    const adminsLine = admins.length ? (admins.length === 1 ? 'Admin: ' : 'Admins: ') + admins.join(', ') : ''
    const full = t.members.length >= teamSizeMax
    const pill = t.locked ? ' · <span class="pill warn">Locked</span>' : full ? ' · <span class="pill">Full</span>' : ''
    // nav.js resolveRoute: `s=team` (singular) is the id this app's router
    // aliases to organiser-team-detail; `s=teams` (plural, href's own
    // section name for this screen) would just reopen this same list.
    const teamHref = href(role, slug, 'team') + '&team=' + encodeURIComponent(t.slug)
    return `<a class="r" href="${esc(teamHref)}"><span class="main"><b>${esc(t.name)}</b><span>${esc(trackName)}${adminsLine ? ' · ' + esc(adminsLine) : ''}</span></span><span class="meta">${t.members.length} of ${teamSizeMax}${pill}</span></a>`
  }

  function soloRow(p) {
    const looking = Object.prototype.hasOwnProperty.call(lookingByUser, p.user_id)
    const sub = looking ? 'Looking for a team' + (lookingByUser[p.user_id] ? ' · ' + lookingByUser[p.user_id] : '') : 'Not looking'
    // Organiser hand placement works on an ordinarily locked team (the lock
    // is the TEAM's own "no more joiners", not a block on the organiser) --
    // only a kept team (kept_after_leave) refuses placement outright.
    const others = teams.filter(t => t.members.length < teamSizeMax && !t.kept)
    const menu = rowMenuHTML(p.display_name || 'this person', [{ label: 'Put on a team…', act: 'place' }])
    return `<div class="r" data-user="${esc(p.user_id)}" data-name="${esc(p.display_name || p.email || 'Someone')}" data-teams="${esc(JSON.stringify(others.map(t => ({ slug: t.slug, name: t.name }))))}"><div class="main"><b>${esc(p.display_name || p.email || 'Someone')}</b><span>${esc(sub)}</span></div><div class="row-acts">${menu}</div></div>`
  }

  function trackRow(t, i) {
    const n = teams.filter(team => team.track && team.track.slug === t.slug).length
    const menu = rowMenuHTML(t.name || 'this track', [{ label: 'Edit', act: 'edit' }, { label: 'Remove', act: 'remove', danger: true }])
    return `<div class="r" data-track="${i}"><div class="main"><b>${esc(t.name)}</b><span>${esc(t.challenge || '')}${t.prize ? ' · Prize: ' + esc(t.prize) : ''}</span></div><span class="meta">${n} team${n === 1 ? '' : 's'}</span><div class="row-acts"><button class="btn quiet sm" type="button" data-edit-track="${i}">Edit</button>${menu}</div></div>`
  }

  function subnavHTML() {
    function badge(n) { return n ? `<span class="n">${n}</span>` : '' }
    return `<nav class="subnav" aria-label="Teams sections">
<a href="?s=teams&tab=teams"${tab === 'teams' ? ' aria-current="page"' : ''}>Teams${badge(teams.length)}</a>
<a href="?s=teams&tab=solo"${tab === 'solo' ? ' aria-current="page"' : ''}>Not on a team${badge(onNoTeam.length)}</a>
<a href="?s=teams&tab=tracks"${tab === 'tracks' ? ' aria-current="page"' : ''}>Tracks${badge(tracks.length)}</a>
<a href="?s=teams&tab=settings"${tab === 'settings' ? ' aria-current="page"' : ''}>Settings</a>
</nav>`
  }

  function paintTeams(sp) {
    sp.innerHTML = `<div class="sp-head"><h2 id="sp-h" tabindex="-1">Teams</h2><p>Open a team to rename it, place people, choose its admins or lock it.</p></div>
${ev.team_finding ? '' : `<div class="note">Team finding is off. People join teams with a link or code.</div>`}
${ev.stage === 'archived' ? `<div class="note warn">This event has ended. Teams can’t change any more.</div>` : ''}
${teams.length ? `<div class="list">${teams.map(teamRow).join('')}</div><p class="list-foot">Teams hold up to ${teamSizeMax} people. Change that in <a href="?s=teams&tab=settings">Settings</a>.</p>` : `<div class="empty-state"><h3>No teams yet</h3><p>Teams show here once participants start them, or when Fill teams places people.</p></div>`}`
  }

  function paintSolo(sp) {
    const fillable = ['open', 'building'].includes(ev.stage)
    sp.innerHTML = `<div class="sp-head"><h2 id="sp-h" tabindex="-1">Not on a team</h2><p>Approved participants without a team. Fill teams places everyone, or use a person’s menu to place one.</p>${fillable ? `<button class="btn primary" type="button" id="fill-teams"${onNoTeam.length ? '' : ' disabled'}>Fill teams</button>` : ''}</div>
${onNoTeam.length ? `<div class="list editable">${onNoTeam.map(soloRow).join('')}</div>` : `<div class="empty-state"><h3>Everyone is on a team</h3><p>People who join later, or leave a team, show here.</p></div>`}`
    const fillBtn = sp.querySelector('#fill-teams')
    if (fillBtn) fillBtn.addEventListener('click', onFillTeams)
    sp.querySelectorAll('[data-rowmenu]').forEach(btn => {
      btn.closest('.row-acts').querySelectorAll('.row-menu button').forEach(item => item.addEventListener('click', () => onPlaceOnTeam(item.closest('.r'))))
    })
  }

  function paintTracks(sp) {
    sp.innerHTML = `<div class="sp-head"><h2 id="sp-h" tabindex="-1">Tracks</h2><p>Challenges teams can enter. Each team picks its own on its Team page.</p><button class="btn primary" type="button" id="track-add"><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 4v12M4 10h12"/></svg>Add track</button></div>
${ev.stage === 'archived' ? `<div class="note warn">This event has ended. Tracks can’t change any more.</div>` : ''}
${tracks.length ? `<div class="list editable">${tracks.map(trackRow).join('')}</div>` : `<div class="empty-state"><h3>No tracks yet</h3><p>Add tracks if teams should pick a challenge. Without tracks, everyone competes together.</p></div>`}`
    sp.querySelector('#track-add').addEventListener('click', () => onEditTrack(null))
    sp.querySelectorAll('[data-edit-track]').forEach(btn => btn.addEventListener('click', () => onEditTrack(Number(btn.getAttribute('data-edit-track')))))
    sp.querySelectorAll('[data-track]').forEach(row => {
      const i = Number(row.getAttribute('data-track'))
      row.querySelectorAll('.row-menu button').forEach(item => {
        item.addEventListener('click', () => {
          const act = item.getAttribute('data-act')
          if (act === 'edit') onEditTrack(i)
          else if (act === 'remove') onRemoveTrack(i)
        })
      })
    })
  }

  function paintSettings(sp) {
    const locked = ev.stage === 'archived'
    sp.innerHTML = `<div class="sp-head"><h2 id="sp-h" tabindex="-1">Settings</h2><p>How teams form in this event.</p></div>
<section class="card flat"><div class="setting"><div class="t"><b>Team finding</b><span>Participants can browse teams, ask to join and invite people looking for a team. Team links and codes work either way.</span></div><div class="ctl"><span class="saved" id="tf-saved" hidden><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 10l4 4 8-8"/></svg>Saved</span><button class="toggle${ev.team_finding ? ' on' : ''}" type="button" role="switch" aria-checked="${ev.team_finding ? 'true' : 'false'}" aria-label="Team finding" id="tf-on"${locked ? ' disabled' : ''}></button></div></div></section>
<section class="card"><h2>Team size</h2><div class="field"><label for="ts-size">Most people per team</label><input class="input" id="ts-size" type="number" min="1" max="50" value="${teamSizeMax}"${locked ? ' disabled' : ''}></div><div class="card-foot"><span class="hint" id="ts-hint">No changes</span><button class="btn quiet" type="button" id="ts-discard" disabled>Discard</button><button class="btn primary" type="button" id="ts-save" disabled>Save</button></div></section>
<p class="list-foot">Team and participant lists download as CSV from <a href="${esc(href(role, slug, 'results'))}">Results</a>.</p>`
    const tfOn = sp.querySelector('#tf-on')
    if (tfOn) tfOn.addEventListener('click', onToggleTeamFinding)
    const sizeInput = sp.querySelector('#ts-size')
    const discard = sp.querySelector('#ts-discard')
    const save = sp.querySelector('#ts-save')
    const hint = sp.querySelector('#ts-hint')
    sizeInput.addEventListener('input', () => {
      const dirty = Number(sizeInput.value) !== teamSizeMax
      discard.disabled = !dirty
      save.disabled = !dirty
      hint.textContent = dirty ? '' : 'No changes'
    })
    discard.addEventListener('click', () => { sizeInput.value = teamSizeMax; discard.disabled = true; save.disabled = true; hint.textContent = 'No changes' })
    save.addEventListener('click', () => onSaveTeamSize(sizeInput, save))
  }

  async function paint() {
    const onNoTeamCount = onNoTeam.length
    const subtitle = teams.length + ' team' + (teams.length === 1 ? '' : 's') + ' · ' + (onNoTeamCount ? onNoTeamCount + ' people not on a team' : 'Everyone is on a team')
    main.innerHTML = `<div class="ph"><div class="t"><h1>Teams</h1><p>${esc(subtitle)}</p></div></div>
<div class="subnav-split">${subnavHTML()}<section class="sp" aria-labelledby="sp-h"></section></div>`
    const sp = main.querySelector('.sp')
    if (tab === 'teams') paintTeams(sp)
    else if (tab === 'solo') paintSolo(sp)
    else if (tab === 'tracks') paintTracks(sp)
    else paintSettings(sp)
    focusHeading(main)
    // Platform-admin inspection or an archived event: every write control
    // on whichever sub-page is showing (Fill teams, the solo row menus,
    // track Add/Edit/Remove, team finding, team size) gets disabled with
    // the one shared banner, same as Overview/People/Settings.
    adminLock(main, admin, ev)
  }

  async function onFillTeams() {
    setLive('')
    const btn = $('fill-teams')
    btn.disabled = true
    let r
    // No `size` on the first preview: the server defaults to
    // min(4, team_size_max) itself, so a cap below 4 (rare, but legal --
    // "1 to 50") still opens the dialog instead of 400ing on a size the
    // event could never allow.
    try { r = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/fill', { apply: false }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); btn.disabled = false; return }
    if (!r.ok) { setLive(ui.errText(r, { fallback: 'Could not plan teams' }), true); btn.disabled = false; return }
    const ok = await fillTeamsDialog(r.data)
    btn.disabled = false
    if (!ok) return
    const size = ok.size
    let ar
    try { ar = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/fill', { apply: true, size }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); return }
    if (!ar.ok) { setLive(ui.errText(ar, { fallback: 'Could not fill teams' }), true); return }
    const shifted = planSignature(ok.plan) !== planSignature(ar.data.plan || [])
    await load()
    await paint()
    if (shifted) setLive('Saved. Team membership changed since the preview, so teams were filled a little differently than shown.')
    else ui.saved(main)
  }

  // The Fill-teams dialog previews a given "people per team" size, and
  // re-previews when the organiser changes it (spec.json organiser-teams
  // "fill-teams preview" state). Resolves { plan, size } on Apply, false on
  // cancel. `size` and `plan` are only ever updated together, from the same
  // response, and only while that response is still the latest one asked
  // for (a `token` guard) -- so Apply can never send a size whose plan the
  // organiser never actually saw, even if two previews overlap or the
  // organiser types a new size before the first one answers.
  function fillTeamsDialog(initial) {
    return new Promise(resolve => {
      let size = initial.size || Math.min(4, teamSizeMax)
      let plan = initial.plan || [], skipped = initial.skipped || []
      let requestToken = 0
      const body = document.createElement('div')
      function skipNote(s) { return `${esc(s.name)} is locked, so it was skipped.` }
      function saveLabel() {
        const n = plan.reduce((s, p) => s + p.members.length, 0)
        return n ? 'Place ' + n + ' people' : 'Nothing to place'
      }
      // The Save/Apply button lives in formDialog's own footer, outside
      // `body` -- reached through the open dialog once it exists (never on
      // the very first renderPlan(), which runs before formDialog opens).
      function applyButton() { return document.querySelector('.scrim .dialog .acts .btn.primary') }
      function syncApplyButton() {
        const btn = applyButton()
        if (!btn) return
        btn.textContent = saveLabel()
        btn.disabled = !plan.length
      }
      function renderPlan() {
        const list = document.createElement('ul')
        list.className = 'steps'
        for (const p of plan) {
          const li = document.createElement('li')
          li.className = 'done'
          const what = document.createElement('div')
          what.className = 'what'
          const b = document.createElement('b')
          b.textContent = p.new ? 'New team: ' + p.name : p.name + ' gets'
          const span = document.createElement('span')
          span.textContent = p.members.map(m => m.display_name).join(', ')
          what.append(b, span)
          const tick = document.createElement('span')
          tick.className = 'tick'
          tick.textContent = '✓'
          li.append(tick, what)
          list.append(li)
        }
        const note = skipped.length ? `<p class="small muted">${skipped.map(skipNote).join(' ')}</p>` : ''
        body.innerHTML = `<div class="field"><label for="fill-size">People per team</label><input class="input" id="fill-size" type="number" min="1" max="${teamSizeMax}" value="${size}"><p class="help">Up to ${teamSizeMax}, your team size.</p></div><div id="fill-plan"></div>${note}`
        body.querySelector('#fill-plan').append(list)
        if (!plan.length) body.querySelector('#fill-plan').innerHTML = '<p class="muted">Nobody is left to place.</p>'
        body.querySelector('#fill-size').addEventListener('change', onSizeChange)
        syncApplyButton()
      }
      async function onSizeChange(e) {
        const n = Number(e.target.value)
        if (!n || n < 1 || n > teamSizeMax) return
        const token = ++requestToken
        const applyBtn = applyButton()
        if (applyBtn) applyBtn.disabled = true
        let r
        try { r = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/fill', { apply: false, size: n }) }
        catch (err) {
          // Restores whatever the button should read for the plan already
          // on screen (syncApplyButton's own rule) -- not a bare re-enable,
          // since that plan might itself be empty.
          if (token === requestToken) syncApplyButton()
          return
        }
        // A newer preview already started (or finished) after this one --
        // this response is stale, whether or not it happens to be the one
        // that arrives last. Never let it touch size/plan/the button.
        if (token !== requestToken) return
        if (!r.ok) { syncApplyButton(); return }
        size = n
        plan = r.data.plan || []
        skipped = r.data.skipped || []
        renderPlan() // rebuilds body AND re-syncs/re-enables the Apply button
      }
      renderPlan()
      formDialog(ui, { title: 'Fill teams?', body, saveLabel: saveLabel(), focus: 0 }, async () => {
        if (!plan.length) return false
        resolve({ plan, size })
        return true
      }).then(saved => { if (!saved) resolve(false) })
    })
  }

  function planSignature(plan) {
    return JSON.stringify((plan || []).map(p => ({ team: p.team, members: p.members.map(m => m.user_id).slice().sort() })).sort((a, b) => a.team.localeCompare(b.team)))
  }

  async function onPlaceOnTeam(row) {
    const userID = row.getAttribute('data-user')
    const name = row.getAttribute('data-name')
    let choices = []
    try { choices = JSON.parse(row.getAttribute('data-teams') || '[]') } catch (e) {}
    if (!choices.length) { setLive('No team has room right now.', true); return }
    const body = document.createElement('div')
    body.innerHTML = `<div class="field"><label for="place-pick">Team</label><select class="select" id="place-pick">${choices.map(t => `<option value="${esc(t.slug)}">${esc(t.name)}</option>`).join('')}</select></div>`
    const ok = await formDialog(ui, { title: 'Put ' + name + ' on a team', body, saveLabel: 'Put on team' }, async () => true)
    if (!ok) return
    const target = body.querySelector('#place-pick').value
    let r
    try { r = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(target) + '/members', { user_id: userID }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(ui.errText(r, { fallback: 'Could not move them.' }), true); return }
    await load()
    await paint()
    ui.saved(main)
  }

  async function onToggleTeamFinding() {
    const on = !ev.team_finding
    setLive('')
    let r
    try { r = await api('PATCH', '/v1/hack/events/' + encodeURIComponent(slug), { team_finding: on }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(ui.errText(r, { fallback: 'Could not save' }), true); return }
    ev = r.data.event
    await load()
    await paint()
    const tick = $('tf-saved')
    if (tick) { tick.hidden = false; setTimeout(() => { tick.hidden = true }, 2000) }
  }

  async function onSaveTeamSize(input, btn) {
    const size = Number(input.value)
    if (!size || size < 1 || size > 50) { setLive('Most people per team must be between 1 and 50.', true); return }
    btn.disabled = true
    let r
    try { r = await api('PATCH', '/v1/hack/events/' + encodeURIComponent(slug), { team_size_max: size }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); btn.disabled = false; return }
    if (!r.ok) { setLive(ui.errText(r, { fallback: 'Could not save' }), true); btn.disabled = false; return }
    ev = r.data.event
    await load()
    await paint()
    ui.saved(main)
  }

  async function onEditTrack(i) {
    const existing = i == null ? null : tracks[i]
    const body = document.createElement('div')
    body.innerHTML = `<div class="field"><label for="trk-name">Track name</label><input class="input" id="trk-name" maxlength="80" value="${esc(existing ? existing.name : '')}"></div>
<div class="field"><label for="trk-challenge">Challenge</label><textarea class="input" id="trk-challenge">${esc(existing ? existing.challenge : '')}</textarea></div>
<div class="field"><label for="trk-prize">Prize</label><input class="input" id="trk-prize" maxlength="80" value="${esc(existing ? existing.prize : '')}"></div>`
    const saved = await formDialog(ui, { title: existing ? 'Edit track' : 'Add track', body, saveLabel: existing ? 'Save track' : 'Add track' }, async () => {
      const name = body.querySelector('#trk-name').value.trim()
      if (!name) { setLive('Enter a track name.', true, body); return false }
      const next = { slug: existing ? existing.slug : slugify(name), name, challenge: body.querySelector('#trk-challenge').value.trim(), prize: body.querySelector('#trk-prize').value.trim() }
      const list = tracks.slice()
      if (existing) list[i] = next
      else list.push(next)
      return saveTracks(list)
    })
    if (saved) { await load(); await paint() }
  }

  async function onRemoveTrack(i) {
    const t = tracks[i]
    const n = teams.filter(team => team.track && t.slug && team.track.slug === t.slug).length
    const body = n
      ? `${n} team${n === 1 ? '' : 's'} chose ${t.name || 'this track'}. They’ll have no track. Remove it?`
      : `Remove ${t.name || 'this track'}?`
    const ok = await ui.confirm({ title: 'Remove track?', body, confirmLabel: 'Remove', danger: true })
    if (!ok) return
    const next = tracks.slice(0, i).concat(tracks.slice(i + 1))
    const name = t.name || 'Track'
    const before = tracks.slice()
    if (await saveTracks(next)) {
      await load()
      await paint()
      const sp = main.querySelector('.sp')
      undoToast(sp, 'Removed ' + name, async () => { if (await saveTracks(before)) { await load(); await paint() } })
    }
  }

  async function saveTracks(list) {
    const payload = list.map(t => ({ slug: t.slug || slugify(t.name), name: t.name, challenge: t.challenge, prize: t.prize }))
    let r
    try { r = await api('PUT', '/v1/hack/events/' + encodeURIComponent(slug) + '/tracks', { tracks: payload }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); return false }
    if (!r.ok) { setLive(ui.errText(r, { fallback: 'Could not save tracks' }), true); return false }
    tracks = (r.data || []).map(t => ({ ...t }))
    return true
  }

  await load()
  await paint()
}
