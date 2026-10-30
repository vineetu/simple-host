import { href } from '../nav.js'
export function title() { return 'Assignment grid' }

// Features on this screen: manual-assignment-grid, panel-assignment-grid.
export async function render(ctx) {
  const { main, ui, params } = ctx
  const client = ctx.api
  const api = client.api
  const ev = ctx.event.event
  const slug = ev.slug
  const base = '/v1/hack/events/' + encodeURIComponent(slug)
  const $ = id => main.querySelector('#' + id)
  const setLive = ui.live(main)
  const locked = !!ev.judging_locked_at
  const gridHref = g => href('organiser', slug, 'assignments') + '&grid=' + g

  main.innerHTML = '<p class="note">Loading…</p>'

  let people = [], teams = [], conflicts = [], assignments = [], panels = [], assignmentMode = 'open'
  const tracks = ev.tracks || []
  try {
    const [peopleR, teamsR, conflictsR, assignR, panelsR, settingsR] = await Promise.all([
      api('GET', base + '/people'),
      api('GET', base + '/teams'),
      api('GET', base + '/conflicts'),
      api('GET', base + '/assignments'),
      api('GET', base + '/judging/panels'),
      api('GET', base + '/judging/settings'),
    ])
    if (peopleR.ok) people = peopleR.data || []
    if (teamsR.ok) teams = (teamsR.data && teamsR.data.teams) || []
    if (conflictsR.ok) conflicts = (conflictsR.data && conflictsR.data.conflicts) || []
    if (assignR.ok) assignments = (assignR.data && assignR.data.assignments) || []
    if (panelsR.ok) panels = (panelsR.data && panelsR.data.panels) || []
    if (settingsR.ok) assignmentMode = (settingsR.data && settingsR.data.assignment_mode) || 'open'
  } catch (e) {
    if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err')
    return
  }

  const wanted = params.get('grid')
  const grid = wanted === 'manual' || wanted === 'panel' ? wanted : (assignmentMode === 'panel' ? 'panel' : 'manual')

  main.innerHTML = `
<p class="crumbs"><a href="${ui.esc(href('organiser', slug, 'judging'))}">Judging</a> / Assignment grid</p>
<div class="ph"><div class="t"><h1>Assignment grid</h1><p>${grid === 'manual' ? 'Choose which judges score each team.' : 'Choose the judges for each track.'}</p></div><button class="btn primary" type="button" id="jg-grid-save">Save judging access</button></div>
<nav class="subtabs"><a href="${ui.esc(gridHref('manual'))}"${grid === 'manual' ? ' aria-current="page"' : ''}>Manual</a><a href="${ui.esc(gridHref('panel'))}"${grid === 'panel' ? ' aria-current="page"' : ''}>Panel</a></nav>
<p class="note" id="jg-grid-note" hidden></p>
<div class="note info" id="jg-grid-info"></div>
<div class="grid2" id="jg-grid-rows"></div>`

  $('jg-grid-info').textContent = grid === 'manual'
    ? "Judges who know a team are greyed out for that team."
    : "Conflicts are per team. In Panel mode a judge can still sit on a track's panel even if they know someone on one team in it. That team just won't be in their list."

  const judges = people.filter(p => p.role === 'judge' || p.role === 'organiser')
  const subjects = grid === 'manual'
    ? teams.map(t => ({ id: t.id, name: t.name || t.slug }))
    : tracks.map(t => ({ id: t.id, name: t.name }))

  const rowsBox = $('jg-grid-rows')
  const note = $('jg-grid-note')
  let saveBlocked = false
  if (!subjects.length || !judges.length) {
    note.hidden = false
    note.textContent = grid === 'manual' ? 'Add teams and judges first.' : 'Add tracks and judges first.'
  } else if (grid === 'manual' && assignmentMode !== 'manual') {
    note.hidden = false
    note.innerHTML = 'This grid is used only in Manual mode. Switch the mode on <a href="' + ui.esc(href('organiser', slug, 'judging')) + '">Judging</a>.'
    saveBlocked = true
  } else if (grid === 'panel' && assignmentMode !== 'panel') {
    // Confusing #3: the Manual grid already warned when the saved mode
    // wasn't Manual -- Panel could still be edited and saved with no hint
    // it has no effect until the mode is actually Panel.
    note.hidden = false
    note.innerHTML = 'This grid is used only in Panel mode. Switch the mode on <a href="' + ui.esc(href('organiser', slug, 'judging')) + '">Judging</a>.'
    saveBlocked = true
  }

  subjects.forEach(subject => {
    const card = ui.el('section', { class: 'card' })
    card.appendChild(ui.el('h2', { text: subject.name }))
    judges.forEach(judge => {
      const label = ui.el('label', { class: 'check' })
      const input = ui.el('input', { type: 'checkbox' })
      input.setAttribute('data-subject', subject.id)
      input.setAttribute('data-judge', judge.user_id)
      const conflict = grid === 'manual' && conflicts.some(c => c.team_id === subject.id && c.judge_id === judge.user_id)
      const checked = grid === 'manual'
        ? assignments.some(a => a.team_id === subject.id && a.judge_id === judge.user_id)
        : panels.some(p => p.track_id === subject.id && p.judge_id === judge.user_id)
      input.checked = !conflict && checked
      if (conflict || locked) input.disabled = true
      if (conflict) input.setAttribute('data-conflict', '')
      label.appendChild(input)
      label.appendChild(ui.el('span', { text: ' ' + (judge.display_name || judge.email || 'Someone') + (conflict ? ' · knows someone on ' + subject.name : '') }))
      card.appendChild(label)
    })
    rowsBox.appendChild(card)
  })

  async function save() {
    const checked = Array.from(rowsBox.querySelectorAll('input[type=checkbox]:checked:not([data-conflict])'))
    const rows = checked.map(input => grid === 'manual'
      ? { judge_id: input.getAttribute('data-judge'), team_id: input.getAttribute('data-subject') }
      : { judge_id: input.getAttribute('data-judge'), track_id: input.getAttribute('data-subject') })
    const path = grid === 'manual' ? '/assignments' : '/judging/panels'
    const payload = grid === 'manual' ? { assignments: rows } : { panels: rows }
    const top = $('jg-grid-save')
    top.disabled = true
    let r
    try { r = await api('PUT', base + path, payload) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', top); top.disabled = false; return }
    top.disabled = false
    if (!r.ok) { setLive(ui.errText(r), 'err', top); return }
    if (grid === 'manual') assignments = (r.data && r.data.assignments) || []
    else panels = (r.data && r.data.panels) || []
    setLive('Judging access saved', 'ok', top)
  }

  if (locked || saveBlocked) {
    $('jg-grid-save').disabled = true
  } else {
    $('jg-grid-save').addEventListener('click', save)
  }
}
