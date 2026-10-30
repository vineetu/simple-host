import { href } from '../nav.js'

export function title() { return 'Judging' }

// Features on this screen: define-rubric, confirm-score-deletion-on-rubric-change,
// reorder-rubric-criteria, choose-tie-criterion, choose-judge-assignment-mode,
// set-judges-per-team, generate-automatic-assignments, declare-conflict-organiser,
// remove-conflict-organiser, lock-judging, unlock-judging, view-judging-dashboard.
export async function render(ctx) {
  const { main, ui } = ctx
  const client = ctx.api
  const api = client.api
  const ev = ctx.event.event
  const slug = ev.slug
  const base = '/v1/hack/events/' + encodeURIComponent(slug)
  const $ = id => main.querySelector('#' + id)
  const setLive = ui.live(main)

  let locked = !!ev.judging_locked_at
  // A platform-admin inspection, a taken-down event or an ended event blocks
  // every write here too, on top of (never instead of) the judging-lock and
  // stage rules below — matching classic's writesLocked() plus its ended-
  // event lock, which this screen had been skipping.
  const adminLockReason = ui.writeLockReason(ev, ctx.event.admin_view)
  const archived = ev.stage === 'archived'
  const tieClosed = () => locked || !!adminLockReason || ['judging', 'results', 'archived'].includes(ev.stage)
  const beforeClosed = () => !['closed', 'judging', 'results', 'archived'].includes(ev.stage)
  // Layout: before judging starts, the rubric is still being written, so
  // setup comes first and the header's primary action is Save rubric
  // (organiser-judging~setup.html). Once judging opens or is locked,
  // progress/conflicts lead and setup collapses out of the way
  // (organiser-judging.html / ~locked.html). Decided once per page load —
  // the stage doesn't change on this screen, only `locked` does.
  const setupFirst = ['draft', 'open', 'building', 'closed'].includes(ev.stage) && !locked

  let rubric = []
  let settings = { assignment_mode: 'open', judges_per_team: 2, score_mode: 'raw', tie_criterion_id: '', public_scores: false, public_ranks: false }
  let conflicts = []
  let people = []
  let teams = []
  let assignmentsCount = 0
  // Draft vs. saved: the radios must keep whatever the organiser just
  // clicked (B1 -- clicking used to call paintMode(), which re-read
  // settings.assignment_mode and snapped the choice straight back to
  // whatever was last saved). draftMode/draftPerTeam hold the in-progress
  // choice; paintMode() always paints from the draft, never from settings;
  // saveMode() is the only thing that moves a draft into settings.
  let draftMode = settings.assignment_mode
  let draftPerTeam = settings.judges_per_team

  const setupInner = `
<section class="card" id="jg-rubric-card"><h2>Rubric</h2><div id="jg-rubric"></div><div class="row-flex"><button class="btn" type="button" id="jg-add">Add a criterion</button><span class="small muted" id="jg-weight"></span><button class="btn" type="button" id="jg-rubric-save">Save rubric</button></div></section>
<section class="card" id="jg-tie-card"><h2>Tie-break</h2><div class="field"><label>First tie criterion<select class="select" id="jg-tie"></select></label></div><p class="help">You can change this until judging starts.</p><button class="btn" type="button" id="jg-tie-save">Save</button></section>
<section class="card" id="jg-mode-card"><h2>How judges are assigned</h2>
<label class="check"><input type="radio" name="jg-mode" value="open" id="jg-mode-open"><div><b>Open</b><span>Judges pick any team from the full list.</span></div></label>
<label class="check"><input type="radio" name="jg-mode" value="automatic" id="jg-mode-automatic"><div><b>Automatic</b><span>Simple Hack assigns judges to teams evenly.</span></div></label>
<label class="check"><input type="radio" name="jg-mode" value="manual" id="jg-mode-manual"><div><b>Manual</b><span>You assign each judge to specific teams.</span></div></label>
<label class="check"><input type="radio" name="jg-mode" value="panel" id="jg-mode-panel"><div><b>Panel</b><span>Judges are grouped by track, one panel per track.</span></div></label>
<div class="field" id="jg-per-wrap"><label>Judges per team<input class="input" type="number" min="1" max="20" id="jg-per"></label></div>
<div class="note info" id="jg-before-closed-note" hidden>You can set this before entries close, but teams may still be forming.</div>
<p class="note" id="jg-assign-note" hidden></p>
<label class="check" id="jg-include-organisers-wrap"><input type="checkbox" id="jg-include-organisers"><span>Include organisers who can judge</span></label>
<div class="row-flex"><button class="btn" type="button" id="jg-mode-save">Save</button><button class="btn" type="button" id="jg-generate">Generate assignments</button><a class="btn" id="jg-grid-link" href="${ui.esc(href('organiser', slug, 'assignments'))}">Open the assignment grid</a></div>
<p class="small muted" id="jg-generate-hint" hidden>Save the mode first.</p>
</section>`

  // Collapsed (no "open" attribute) once judging has started, matching
  // organiser-judging.html; not collapsible at all while still setting up,
  // matching organiser-judging~setup.html.
  const setupBlock = setupFirst ? setupInner : `<details class="card" id="jg-setup"><summary>Rubric &amp; setup</summary>${setupInner}</details>`
  // The lock/unlock card has nothing to offer once the event is archived —
  // judging is long since done and writesLocked() already blocks every
  // control on it, so it's dropped rather than shown inert (A20 ended:
  // hide edit controls).
  const restBlock = `
<section class="card" id="jg-progress-card"><h2>Progress</h2><p class="note">Loading…</p></section>
<section class="card" id="jg-conflicts-card"><h2>Conflicts of interest</h2><p class="note">Loading…</p></section>
${archived ? '' : '<section class="card" id="jg-lockzone"></section>'}`

  // Every organiser can also judge (the API accepts "judge" or "organiser"
  // on every /judge/* route) — only a platform-admin inspection cannot, so
  // the entry point to the organiser's own scoring queue follows that, not
  // whether assignments happen to have been generated yet.
  const canJudge = !ctx.event.admin_view
  main.innerHTML = `
<div class="ph"><div class="t"><h1>Judging</h1><p>Rubric, assignments and scoring progress.</p></div>
<span id="jg-top-action"></span>${canJudge ? `<a class="btn" href="${ui.esc('/e/' + encodeURIComponent(slug) + '/judge')}">Score as a judge</a>` : ''}</div>
${adminLockReason ? `<div class="note warn">${ui.esc(adminLockReason)}</div>` : ''}
${setupFirst ? setupBlock + restBlock : restBlock + setupBlock}
<p id="jg-results-link" hidden><a href="${ui.esc(href('organiser', slug, 'results'))}">Review and publish on Results</a></p>`

  function paintTopAction() {
    const slot = $('jg-top-action')
    slot.textContent = ''
    $('jg-results-link').hidden = !locked
    if (locked) {
      const a = ui.el('a', { class: archived ? 'btn' : 'btn primary', href: href('organiser', slug, 'results'), text: 'Go to Results' })
      slot.appendChild(a)
    } else if (adminLockReason) {
      // Nothing writable to offer here while admin-viewing or on a
      // taken-down/ended event — the rubric/lock cards below explain why.
    } else if (setupFirst) {
      const b = ui.el('button', { class: 'btn primary', type: 'button', text: 'Save rubric' })
      b.addEventListener('click', saveRubric)
      slot.appendChild(b)
    } else {
      const b = ui.el('button', { class: 'btn primary', type: 'button', text: 'Lock judging' })
      b.addEventListener('click', doLock)
      slot.appendChild(b)
    }
  }

  function paintLockZone() {
    const zone = $('jg-lockzone')
    if (!zone) return
    zone.textContent = ''
    if (!locked) {
      zone.appendChild(ui.el('h2', { text: 'Lock judging' }))
      zone.appendChild(ui.el('p', { class: 'sub', text: 'Locking stops every rubric, assignment or score change until you unlock.' }))
    } else {
      zone.appendChild(ui.el('h2', { text: 'Judging is locked' }))
      zone.appendChild(ui.el('p', { class: 'sub', text: 'Unlocking is logged against your account. Why does scoring need to reopen?' }))
      const form = ui.el('form')
      const field = ui.el('div', { class: 'field' })
      const label = ui.el('label', { text: 'Reason' })
      const textarea = ui.el('textarea', { class: 'input', required: '', maxlength: '300' })
      textarea.disabled = !!adminLockReason
      label.appendChild(textarea)
      field.appendChild(label)
      form.appendChild(field)
      const go = ui.el('button', { class: 'btn', type: 'submit', text: 'Unlock' })
      go.disabled = !!adminLockReason
      form.appendChild(go)
      form.addEventListener('submit', e => { e.preventDefault(); doUnlock(textarea.value, go) })
      zone.appendChild(form)
    }
  }

  async function doLock() {
    const ok = await ui.confirm({ title: 'Lock judging now?', body: "No judge can change a score, and you can't change the rubric or assignments, until you unlock.", confirmLabel: 'Lock judging', danger: true })
    if (!ok) return
    let r
    try { r = await api('POST', base + '/judging/lock', {}) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err'); return }
    if (!r.ok) { setLive(ui.errText(r), 'err'); return }
    locked = true
    ev.judging_locked_at = (r.data && r.data.locked_at) || new Date().toISOString()
    paintTopAction(); paintLockZone(); paintRubric(); paintTie(); paintMode(); paintConflicts()
    setLive('Judging locked', 'ok')
  }

  async function doUnlock(reason, btn) {
    reason = String(reason || '').trim()
    if (!reason) { setLive('Enter a reason.', 'err', btn); return }
    btn.disabled = true
    let r
    try { r = await api('POST', base + '/judging/unlock', { reason }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
    locked = false
    ev.judging_locked_at = null
    paintTopAction(); paintLockZone(); paintRubric(); paintTie(); paintMode(); paintConflicts()
    setLive('Judging unlocked', 'ok')
  }

  function weightTotal() { return rubric.reduce((sum, c) => sum + (Number(c.weight) || 0), 0) }

  function paintRubric() {
    const box = $('jg-rubric')
    box.textContent = ''
    rubric.forEach((c, i) => {
      const row = ui.el('div', { class: 'crit crit-row' })
      const nameField = ui.el('div', { class: 'field' })
      const nameLabel = ui.el('label', { text: 'Name' })
      const nameIn = ui.el('input', { class: 'input', value: c.name || '' })
      if (locked || adminLockReason) nameIn.disabled = true
      nameIn.addEventListener('input', () => { c.name = nameIn.value })
      nameLabel.appendChild(nameIn)
      nameField.appendChild(nameLabel)
      const descField = ui.el('div', { class: 'field' })
      const descLabel = ui.el('label', { text: 'Description (optional)' })
      const descIn = ui.el('input', { class: 'input', value: c.description || '' })
      if (locked || adminLockReason) descIn.disabled = true
      descIn.addEventListener('input', () => { c.description = descIn.value })
      descLabel.appendChild(descIn)
      descField.appendChild(descLabel)
      const weightField = ui.el('div', { class: 'field' })
      const weightLabel = ui.el('label', { text: 'Weight %' })
      const weightIn = ui.el('input', { class: 'input', type: 'number', min: '0', max: '100', value: String(c.weight == null ? 0 : c.weight) })
      if (locked || adminLockReason) weightIn.disabled = true
      weightIn.addEventListener('input', () => { c.weight = Number(weightIn.value) || 0; paintWeight() })
      weightLabel.appendChild(weightIn)
      weightField.appendChild(weightLabel)
      const ptsField = ui.el('div', { class: 'field' })
      const ptsLabel = ui.el('label', { text: 'Max points' })
      const ptsIn = ui.el('input', { class: 'input', type: 'number', min: '1', max: '10', value: String(c.max_points == null ? 5 : c.max_points) })
      if (locked || adminLockReason) ptsIn.disabled = true
      ptsIn.addEventListener('input', () => { c.max_points = Number(ptsIn.value) || 1 })
      ptsLabel.appendChild(ptsIn)
      ptsField.appendChild(ptsLabel)
      row.append(nameField, weightField, ptsField, descField)
      const actions = ui.el('div', { class: 'row-flex' })
      const up = ui.el('button', { class: 'btn sm', type: 'button', text: 'Up', 'aria-label': 'Move ' + (c.name || 'this criterion') + ' up' })
      up.disabled = locked || !!adminLockReason || i === 0
      up.addEventListener('click', () => { rubric.splice(i - 1, 0, rubric.splice(i, 1)[0]); paintRubric() })
      const down = ui.el('button', { class: 'btn sm', type: 'button', text: 'Down', 'aria-label': 'Move ' + (c.name || 'this criterion') + ' down' })
      down.disabled = locked || !!adminLockReason || i === rubric.length - 1
      down.addEventListener('click', () => { rubric.splice(i + 1, 0, rubric.splice(i, 1)[0]); paintRubric() })
      const rm = ui.el('button', { class: 'btn danger sm', type: 'button', text: 'Remove' })
      rm.disabled = locked || !!adminLockReason || rubric.length <= 1
      rm.addEventListener('click', () => { rubric.splice(i, 1); paintRubric() })
      actions.append(up, down, rm)
      row.appendChild(actions)
      box.appendChild(row)
    })
    if (!rubric.length) box.appendChild(ui.el('p', { class: 'muted', text: 'No criteria yet. Add one to start.' }))
    paintWeight()
    $('jg-add').disabled = locked || !!adminLockReason || rubric.length >= 10
    $('jg-rubric-save').disabled = locked || !!adminLockReason
  }

  function paintWeight() {
    const p = $('jg-weight')
    if (!rubric.length) { p.textContent = ''; return }
    const total = weightTotal()
    p.textContent = 'Total weight: ' + total + '%' + (total === 100 ? '' : ' (must add up to 100%)')
  }

  function paintTie() {
    const select = $('jg-tie')
    select.textContent = ''
    select.appendChild(ui.el('option', { value: '', text: 'Pick a criterion' }))
    rubric.forEach(c => select.appendChild(ui.el('option', { value: c.id || '', text: c.name || '' })))
    select.value = settings.tie_criterion_id || ''
    select.disabled = tieClosed()
    $('jg-tie-save').disabled = tieClosed()
  }

  async function saveTie() {
    const value = $('jg-tie').value
    const btn = $('jg-tie-save')
    btn.disabled = true
    let r
    try { r = await api('PATCH', base + '/judging/settings', { tie_criterion_id: value }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    btn.disabled = tieClosed()
    if (!r.ok) { setLive(ui.errText(r), 'err', btn); return }
    settings.tie_criterion_id = (r.data && r.data.tie_criterion_id) || ''
    setLive('Saved', 'ok', btn)
  }

  // Paints from the DRAFT, never from `settings` -- see the draftMode
  // comment above (B1). Only disabled-state and the per-mode field
  // visibility depend on the saved lock/admin state; the mode itself is
  // whatever the organiser has picked but not yet saved.
  function paintMode() {
    $('jg-mode-open').checked = draftMode === 'open'
    $('jg-mode-automatic').checked = draftMode === 'automatic'
    $('jg-mode-manual').checked = draftMode === 'manual'
    $('jg-mode-panel').checked = draftMode === 'panel'
    $('jg-per').value = draftPerTeam || 2
    // C11: the API allows changing assignment_mode/judges_per_team any
    // time short of archived or locked (PATCH judging/settings has no
    // stage check for either field) -- so the radios stay enabled before
    // entries close too, instead of disabling with no API reason behind
    // it. jg-before-closed-note stays as a heads-up, not a restriction.
    const disable = locked || !!adminLockReason
    ;['jg-mode-open', 'jg-mode-automatic', 'jg-mode-manual', 'jg-mode-panel', 'jg-per', 'jg-mode-save', 'jg-include-organisers'].forEach(id => { $(id).disabled = disable })
    $('jg-before-closed-note').hidden = !beforeClosed()
    const automatic = draftMode === 'automatic'
    $('jg-per-wrap').hidden = !automatic
    $('jg-generate').hidden = !automatic
    $('jg-include-organisers-wrap').hidden = !automatic
    // Generate (hack_judging.go generateAssignments) checks the SAVED
    // assignment_mode/judges_per_team, not the radio the organiser just
    // clicked -- it 409s "Switch to automatic assignment first" even while
    // the picker already shows Automatic. Rather than let that confusing
    // server error be the first sign of it, disable Generate until the
    // draft matches what's actually saved, with a plain reason why (#6).
    const modeDirty = draftMode !== settings.assignment_mode || Number(draftPerTeam || 2) !== Number(settings.judges_per_team || 2)
    $('jg-generate').disabled = disable || modeDirty
    $('jg-generate-hint').hidden = !(automatic && modeDirty)
  }

  async function saveMode() {
    const mode = draftMode
    const perTeam = draftPerTeam || 2
    const btn = $('jg-mode-save')
    btn.disabled = true
    let r
    try { r = await api('PATCH', base + '/judging/settings', { assignment_mode: mode, judges_per_team: perTeam }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); paintMode(); return }
    if (!r.ok) { paintMode(); setLive(ui.errText(r), 'err', btn); return }
    settings.assignment_mode = (r.data && r.data.assignment_mode) || mode
    settings.judges_per_team = (r.data && r.data.judges_per_team) || perTeam
    draftMode = settings.assignment_mode
    draftPerTeam = settings.judges_per_team
    paintMode()
    setLive('Saved', 'ok', btn)
    await reloadDashboard()
  }

  async function generate() {
    const ok = await ui.confirm({ title: 'Generate assignments?', body: 'This replaces any assignments already generated. Continue?', confirmLabel: 'Generate assignments', danger: true })
    if (!ok) return
    const btn = $('jg-generate')
    btn.disabled = true
    let r
    try { r = await api('POST', base + '/assignments/generate', { include_organisers: $('jg-include-organisers').checked }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); paintMode(); return }
    paintMode()
    if (!r.ok) { setLive(ui.errText(r), 'err', btn); return }
    const data = r.data || {}
    assignmentsCount = (data.assignments || []).length
    const under = data.teams_under_target || []
    const names = under.map(id => { const t = teams.find(x => x.id === id); return t ? (t.name || t.slug) : id })
    const note = $('jg-assign-note')
    note.hidden = false
    note.textContent = assignmentsCount + ' assignment' + (assignmentsCount === 1 ? '' : 's') + ' made.' +
      (names.length ? ' ' + names.length + ' team' + (names.length === 1 ? '' : 's') + ' got fewer judges than planned: ' + names.join(', ') + '.' : '')
    await reloadDashboard()
  }

  function paintConflicts() {
    const card = $('jg-conflicts-card')
    card.textContent = ''
    card.appendChild(ui.el('h2', { text: 'Judges who know a team' }))
    const judges = people.filter(p => p.role === 'judge' || p.role === 'organiser')
    if (!conflicts.length) card.appendChild(ui.el('p', { class: 'muted', text: 'None yet.' }))
    conflicts.forEach(c => {
      const setting = ui.el('div', { class: 'setting' })
      const t = ui.el('div', { class: 't' })
      t.appendChild(ui.el('b', { text: c.judge_name || 'A judge' }))
      t.appendChild(ui.el('span', { text: 'Knows someone on ' + (c.team_name || 'a team') }))
      setting.appendChild(t)
      const rm = ui.el('button', { class: 'btn danger sm', type: 'button', text: 'Remove' })
      rm.disabled = locked || !!adminLockReason
      rm.addEventListener('click', () => removeConflict(c, rm))
      setting.appendChild(rm)
      card.appendChild(setting)
    })
    // After archive, nothing here is writable any more (A20: hide edit
    // controls rather than show them inert) -- same reasoning as the
    // dropped lock card above.
    if (archived) return
    if (judges.length && teams.length) {
      const form = ui.el('form')
      const row = ui.el('div', { class: 'row-flex' })
      const judgeSelect = ui.el('select', { class: 'select', 'aria-label': 'Judge' })
      judges.forEach(p => judgeSelect.appendChild(ui.el('option', { value: p.user_id, text: p.display_name || p.email || 'Someone' })))
      const teamSelect = ui.el('select', { class: 'select', 'aria-label': 'Team' })
      teams.forEach(t => teamSelect.appendChild(ui.el('option', { value: t.id, text: t.name || t.slug })))
      const add = ui.el('button', { class: 'btn', type: 'submit', text: 'Add' })
      judgeSelect.disabled = teamSelect.disabled = add.disabled = locked || !!adminLockReason
      row.append(judgeSelect, teamSelect, add)
      form.appendChild(row)
      form.addEventListener('submit', e => { e.preventDefault(); addConflict(judgeSelect.value, teamSelect.value, add) })
      card.appendChild(form)
    } else {
      card.appendChild(ui.el('button', { class: 'btn', type: 'button', text: 'Add', disabled: '' }))
    }
  }

  async function addConflict(judgeId, teamId, btn) {
    if (!judgeId || !teamId) { setLive('Choose a judge and a team.', 'err', btn); return }
    btn.disabled = true
    let r
    try { r = await api('POST', base + '/conflicts', { team_id: teamId, judge_user_id: judgeId }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    btn.disabled = false
    if (!r.ok) { setLive(ui.errText(r), 'err', btn); return }
    await reloadConflicts()
    setLive('Conflict added', 'ok')
  }

  async function removeConflict(c, btn) {
    const ok = await ui.confirm({ title: 'Remove this conflict?', body: (c.judge_name || 'This judge') + ' can be assigned to ' + (c.team_name || 'this team') + ' again.', confirmLabel: 'Remove', danger: true })
    if (!ok) return
    btn.disabled = true
    let r
    try { r = await api('DELETE', base + '/conflicts/' + encodeURIComponent(c.team_id) + '?judge_user_id=' + encodeURIComponent(c.judge_id)) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    if (!r.ok && r.status !== 204) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
    await reloadConflicts()
  }

  async function reloadConflicts() {
    const r = await api('GET', base + '/conflicts')
    if (r.ok) conflicts = (r.data && r.data.conflicts) || []
    paintConflicts()
    await reloadDashboard()
  }

  // C7 (owner decision, 2026-10-30): judge counts show judges only --
  // an organiser who can also judge is counted only when "Include
  // organisers" is on (automatic mode's checkbox, read live off the form),
  // they already have a real score (done_count > 0), or they have an
  // assignment (assigned_count > 0 -- Manual/Panel grid assignments, or
  // Automatic assignments generated with "Include organisers" on at the
  // time, which survive a later reload even after the checkbox resets).
  // The dashboard itself doesn't say which role a judge_id belongs to, so
  // that comes from the /people list already loaded for the conflicts form.
  function isCountedJudge(judgeId, doneCount, assignedCount) {
    const person = people.find(p => p.user_id === judgeId)
    if (!person || person.role !== 'organiser') return true
    // C7 leftover: in Open mode, judgingDashboard's assigned_count is every
    // team the judge is *eligible* for (Open has no real per-judge
    // assignment, see hack_judging.go) -- never zero even for an organiser
    // nobody has actually used as a judge. Only a real score counts them
    // while the saved mode is Open; Automatic/Manual/Panel keep the
    // assigned_count > 0 check, since there an assignment is a real one.
    if (settings.assignment_mode === 'open') return doneCount > 0
    const includeOrganisers = draftMode === 'automatic' && $('jg-include-organisers') && $('jg-include-organisers').checked
    return includeOrganisers || doneCount > 0 || assignedCount > 0
  }

  function paintProgress(teamsRows, judgeRows) {
    const card = $('jg-progress-card')
    card.textContent = ''
    const head = ui.el('div', { class: 'row-flex' })
    head.appendChild(ui.el('h2', { text: 'Progress' }))
    if (!archived) {
      const refresh = ui.el('button', { class: 'btn quiet sm', type: 'button', text: 'Refresh' })
      refresh.addEventListener('click', reloadDashboard)
      head.appendChild(refresh)
    }
    card.appendChild(head)
    if (settings.assignment_mode === 'automatic' && assignmentsCount === 0) {
      card.appendChild(ui.el('div', { class: 'note info', text: 'Assignments not generated yet' }))
    }
    card.appendChild(ui.el('p', { class: 'section-title', text: 'Teams' }))
    const tlist = ui.el('div', { class: 'list' })
    if (!teamsRows.length) tlist.appendChild(ui.el('div', { class: 'empty', text: 'No teams yet.' }))
    teamsRows.forEach(t => {
      const row = ui.el('div', { class: 'r' })
      const mainEl = ui.el('div', { class: 'main' })
      mainEl.appendChild(ui.el('b', { text: t.team_name || 'A team' }))
      row.appendChild(mainEl)
      const text = t.judges_scored === 0 ? 'No scores yet' : t.judges_scored + ' judge' + (t.judges_scored === 1 ? '' : 's') + ' scored'
      row.appendChild(ui.el('span', { class: 'meta', text }))
      tlist.appendChild(row)
    })
    card.appendChild(tlist)
    card.appendChild(ui.el('p', { class: 'section-title', text: 'Judges' }))
    const jlist = ui.el('div', { class: 'list' })
    const visibleJudges = judgeRows.filter(j => isCountedJudge(j.judge_id, j.done_count, j.assigned_count))
    if (!visibleJudges.length) jlist.appendChild(ui.el('div', { class: 'empty', text: 'No judges yet.' }))
    visibleJudges.forEach(j => {
      const row = ui.el('div', { class: 'r' })
      const mainEl = ui.el('div', { class: 'main' })
      mainEl.appendChild(ui.el('b', { text: j.judge_name || 'A judge' }))
      const pct = j.assigned_count ? Math.round(100 * j.done_count / j.assigned_count) : 0
      const bar = ui.el('div', { class: 'bar' })
      bar.appendChild(ui.el('i', { style: 'width:' + pct + '%' }))
      mainEl.appendChild(bar)
      row.appendChild(mainEl)
      row.appendChild(ui.el('span', { class: 'meta', text: j.done_count + ' of ' + j.assigned_count + ' done' }))
      jlist.appendChild(row)
    })
    card.appendChild(jlist)
    if (conflicts.length) {
      card.appendChild(ui.el('p', { class: 'note', text: "A judge's conflict excludes their earlier score for that team from coverage and from the team's total and rank, even if it was scored before the conflict was declared." }))
    }
  }

  async function reloadDashboard() {
    let r
    try { r = await api('GET', base + '/judging/dashboard') }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err'); return }
    if (!r.ok) { setLive(ui.errText(r), 'err'); return }
    paintProgress((r.data && r.data.teams) || [], (r.data && r.data.judges) || [])
  }

  $('jg-add').addEventListener('click', () => { rubric.push({ id: '', name: '', description: '', weight: 0, max_points: 5 }); paintRubric() })
  $('jg-rubric-save').addEventListener('click', saveRubric)
  $('jg-tie-save').addEventListener('click', saveTie)
  $('jg-mode-save').addEventListener('click', saveMode)
  $('jg-generate').addEventListener('click', generate)
  main.querySelectorAll('input[name="jg-mode"]').forEach(r => r.addEventListener('change', () => {
    draftMode = main.querySelector('input[name="jg-mode"]:checked').value
    paintMode()
  }))
  // Re-paint on every keystroke, not just mode changes: judges_per_team is
  // also part of modeDirty (#6), so Generate's disabled/hint state has to
  // react to this field too, not only to the radios.
  $('jg-per').addEventListener('input', () => { draftPerTeam = Number($('jg-per').value) || 2; paintMode() })

  async function saveRubric() {
    if (weightTotal() !== 100) { setLive('Weights must add up to 100%.', 'err', $('jg-weight')); return }
    const body = { criteria: rubric.map(c => ({ id: c.id || '', name: c.name || '', description: c.description || '', weight: Number(c.weight) || 0, max_points: Number(c.max_points) || 1 })) }
    const btn = $('jg-rubric-save')
    btn.disabled = true
    let r
    try {
      r = await api('PUT', base + '/rubric', body)
      if (!r.ok && r.data && r.data.code === 'score_deletion_confirmation') {
        if (await ui.confirm({ title: 'Delete scores?', body: r.data.error, confirmLabel: 'Save rubric', danger: true })) {
          body.confirm_score_deletion = true
          r = await api('PUT', base + '/rubric', body)
        }
      }
    } catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    btn.disabled = locked || !!adminLockReason
    if (!r.ok) { setLive(ui.errText(r), 'err', btn); return }
    rubric = (r.data && r.data.criteria) || []
    paintRubric()
    paintTie()
    setLive('Rubric saved', 'ok', btn)
  }

  // --- initial load ---------------------------------------------------
  paintTopAction()
  paintLockZone()

  let rubricR, settingsR, conflictsR, peopleR, teamsR, assignR
  try {
    ;[rubricR, settingsR, conflictsR, peopleR, teamsR, assignR] = await Promise.all([
      api('GET', base + '/rubric'),
      api('GET', base + '/judging/settings'),
      api('GET', base + '/conflicts'),
      api('GET', base + '/people'),
      api('GET', base + '/teams'),
      api('GET', base + '/assignments'),
    ])
  } catch (e) {
    if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err')
    return
  }
  if (rubricR.ok) rubric = (rubricR.data && rubricR.data.criteria) || []
  if (settingsR.ok) settings = settingsR.data || settings
  if (conflictsR.ok) conflicts = (conflictsR.data && conflictsR.data.conflicts) || []
  if (peopleR.ok) people = peopleR.data || []
  if (teamsR.ok) teams = (teamsR.data && teamsR.data.teams) || []
  if (assignR.ok) assignmentsCount = ((assignR.data && assignR.data.assignments) || []).length
  draftMode = settings.assignment_mode
  draftPerTeam = settings.judges_per_team

  paintRubric()
  paintTie()
  paintMode()
  paintConflicts()
  await reloadDashboard()
}
