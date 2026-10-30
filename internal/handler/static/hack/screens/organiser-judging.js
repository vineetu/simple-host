import { href } from '../nav.js'
import { errText, writeLockReason } from './_orgC-util.js'

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
  const stopped = e => e && e.message === 'signed out'
  const setLive = (msg, kind, near) => { if (msg) ui.toast(msg, near || main, kind === 'err') }

  let locked = !!ev.judging_locked_at
  // A platform-admin inspection, a taken-down event or an ended event blocks
  // every write here too, on top of (never instead of) the judging-lock and
  // stage rules below — matching classic's writesLocked() plus its ended-
  // event lock, which this screen had been skipping.
  const adminLockReason = writeLockReason(ev, ctx.event.admin_view)
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

  const setupInner = `
<section class="card" id="jg-rubric-card"><h2>Rubric</h2><div id="jg-rubric"></div><p class="help" id="jg-weight"></p><div class="row-flex"><button class="btn" type="button" id="jg-add">Add a criterion</button><button class="btn primary" type="button" id="jg-rubric-save">Save rubric</button></div></section>
<section class="card" id="jg-tie-card"><h2>Results rules: tie-break</h2><div class="field"><label>First tie criterion<select class="select" id="jg-tie"></select></label></div><p class="help">Locks once judging begins — you can still change it any time before then, including while entries are closed.</p><button class="btn primary" type="button" id="jg-tie-save">Save</button></section>
<section class="card" id="jg-mode-card"><h2>How judges are assigned</h2>
<label class="check"><input type="radio" name="jg-mode" value="open" id="jg-mode-open"><span>Open: judges pick any entry from the full list</span></label>
<label class="check"><input type="radio" name="jg-mode" value="automatic" id="jg-mode-automatic"><span>Automatic: Simple Hack assigns judges to teams evenly</span></label>
<label class="check"><input type="radio" name="jg-mode" value="manual" id="jg-mode-manual"><span>Manual: you assign each judge to specific teams</span></label>
<label class="check"><input type="radio" name="jg-mode" value="panel" id="jg-mode-panel"><span>Panel: judges are grouped by track, one panel per track</span></label>
<div class="field" id="jg-per-wrap"><label>Judges per team<input class="input" type="number" min="1" max="20" id="jg-per"></label></div>
<div class="note info" id="jg-before-closed-note" hidden>Judges get matched once entries close</div>
<p class="note" id="jg-assign-note" hidden></p>
<div class="row-flex"><button class="btn primary" type="button" id="jg-mode-save">Save</button><label class="check" id="jg-include-organisers-wrap"><input type="checkbox" id="jg-include-organisers"><span>Include organisers who can judge</span></label><button class="btn primary" type="button" id="jg-generate">Generate assignments</button><a class="btn" id="jg-grid-link" href="${ui.esc(href('organiser', slug, 'assignments'))}">Open the assignment grid</a></div>
</section>`

  // Collapsed (no "open" attribute) once judging has started, matching
  // organiser-judging.html; not collapsible at all while still setting up,
  // matching organiser-judging~setup.html.
  const setupBlock = setupFirst ? setupInner : `<details class="card" id="jg-setup"><summary>Rubric &amp; setup</summary>${setupInner}</details>`
  const restBlock = `
<section class="card" id="jg-progress-card"><h2>Progress</h2><p class="note">Loading…</p></section>
<section class="card" id="jg-conflicts-card"><h2>Conflicts of interest</h2><p class="note">Loading…</p></section>
<section class="danger-zone" id="jg-lockzone"></section>`

  // Every organiser can also judge (the API accepts "judge" or "organiser"
  // on every /judge/* route) — only a platform-admin inspection cannot, so
  // the entry point to the organiser's own scoring queue follows that, not
  // whether assignments happen to have been generated yet.
  const canJudge = !ctx.event.admin_view
  main.innerHTML = `
<div class="ph"><div class="t"><h1>Judging</h1><p>Rubric, assignments and scoring progress.</p></div>
<span id="jg-top-action"></span>${canJudge ? `<a class="btn" href="${ui.esc('/e/' + encodeURIComponent(slug) + '/judge')}">Score entries</a>` : ''}</div>
${adminLockReason ? `<div class="note warn">${ui.esc(adminLockReason)}</div>` : ''}
${setupFirst ? setupBlock + restBlock : restBlock + setupBlock}
<p id="jg-results-link" hidden><a href="${ui.esc(href('organiser', slug, 'results'))}">Review and publish on Results</a></p>`

  function el(tag, attrs) {
    const node = document.createElement(tag)
    for (const [k, v] of Object.entries(attrs || {})) {
      if (k === 'text') node.textContent = v
      else node.setAttribute(k, v)
    }
    return node
  }

  function paintTopAction() {
    const slot = $('jg-top-action')
    slot.textContent = ''
    $('jg-results-link').hidden = !locked
    if (locked) {
      const a = el('a', { class: 'btn primary', href: href('organiser', slug, 'results'), text: 'Go to Results' })
      slot.appendChild(a)
    } else if (adminLockReason) {
      // Nothing writable to offer here while admin-viewing or on a
      // taken-down/ended event — the rubric/lock cards below explain why.
    } else if (setupFirst) {
      const b = el('button', { class: 'btn primary', type: 'button', text: 'Save rubric' })
      b.addEventListener('click', saveRubric)
      slot.appendChild(b)
    } else {
      const b = el('button', { class: 'btn primary', type: 'button', text: 'Lock judging' })
      b.addEventListener('click', doLock)
      slot.appendChild(b)
    }
  }

  function paintLockZone() {
    const zone = $('jg-lockzone')
    zone.textContent = ''
    if (!locked) {
      zone.appendChild(el('h2', { text: 'Lock' }))
      const setting = el('div', { class: 'setting' })
      const t = el('div', { class: 't' })
      t.appendChild(el('p', { text: 'Stops any further rubric, assignment or score change until you unlock.' }))
      const b = el('button', { class: 'btn danger', type: 'button', text: 'Lock judging' })
      b.disabled = !!adminLockReason
      b.addEventListener('click', doLock)
      t.appendChild(b)
      setting.appendChild(t)
      zone.appendChild(setting)
    } else {
      zone.appendChild(el('h2', { text: 'Unlock' }))
      const setting = el('div', { class: 'setting' })
      const t = el('div', { class: 't' })
      t.appendChild(el('p', { text: 'Unlocking is logged against your account. Why does scoring need to reopen?' }))
      const form = el('form')
      const field = el('div', { class: 'field' })
      const label = el('label', { text: 'Reason' })
      const textarea = el('textarea', { class: 'input', required: '', maxlength: '300' })
      textarea.disabled = !!adminLockReason
      label.appendChild(textarea)
      field.appendChild(label)
      form.appendChild(field)
      const go = el('button', { class: 'btn danger', type: 'submit', text: 'Unlock judging' })
      go.disabled = !!adminLockReason
      form.appendChild(go)
      form.addEventListener('submit', e => { e.preventDefault(); doUnlock(textarea.value, go) })
      t.appendChild(form)
      setting.appendChild(t)
      zone.appendChild(setting)
    }
  }

  async function doLock() {
    const ok = await ui.confirm({ title: 'Lock judging now?', body: "No judge can change a score, and you can't change the rubric or assignments, until you unlock.", confirmLabel: 'Lock judging', danger: true })
    if (!ok) return
    let r
    try { r = await api('POST', base + '/judging/lock', {}) }
    catch (e) { if (!stopped(e)) setLive('Cannot reach the server.', 'err'); return }
    if (!r.ok) { setLive(errText(r), 'err'); return }
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
    catch (e) { if (!stopped(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    if (!r.ok) { setLive(errText(r), 'err', btn); btn.disabled = false; return }
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
      const row = el('div', { class: 'crit' })
      const nameField = el('div', { class: 'field' })
      const nameLabel = el('label', { text: 'Name' })
      const nameIn = el('input', { class: 'input', value: c.name || '' })
      if (locked || adminLockReason) nameIn.disabled = true
      nameIn.addEventListener('input', () => { c.name = nameIn.value })
      nameLabel.appendChild(nameIn)
      nameField.appendChild(nameLabel)
      const descField = el('div', { class: 'field' })
      const descLabel = el('label', { text: 'Description (optional)' })
      const descIn = el('input', { class: 'input', value: c.description || '' })
      if (locked || adminLockReason) descIn.disabled = true
      descIn.addEventListener('input', () => { c.description = descIn.value })
      descLabel.appendChild(descIn)
      descField.appendChild(descLabel)
      const weightField = el('div', { class: 'field' })
      const weightLabel = el('label', { text: 'Weight %' })
      const weightIn = el('input', { class: 'input', type: 'number', min: '0', max: '100', value: String(c.weight == null ? 0 : c.weight) })
      if (locked || adminLockReason) weightIn.disabled = true
      weightIn.addEventListener('input', () => { c.weight = Number(weightIn.value) || 0; paintWeight() })
      weightLabel.appendChild(weightIn)
      weightField.appendChild(weightLabel)
      const ptsField = el('div', { class: 'field', style: 'max-width:140px' })
      const ptsLabel = el('label', { text: 'Max points' })
      const ptsIn = el('input', { class: 'input', type: 'number', min: '1', max: '10', value: String(c.max_points == null ? 5 : c.max_points) })
      if (locked || adminLockReason) ptsIn.disabled = true
      ptsIn.addEventListener('input', () => { c.max_points = Number(ptsIn.value) || 1 })
      ptsLabel.appendChild(ptsIn)
      ptsField.appendChild(ptsLabel)
      row.append(nameField, weightField, ptsField, descField)
      const actions = el('div', { class: 'row-flex' })
      const up = el('button', { class: 'btn', type: 'button', text: 'Move up' })
      up.disabled = locked || !!adminLockReason || i === 0
      up.addEventListener('click', () => { rubric.splice(i - 1, 0, rubric.splice(i, 1)[0]); paintRubric() })
      const down = el('button', { class: 'btn', type: 'button', text: 'Move down' })
      down.disabled = locked || !!adminLockReason || i === rubric.length - 1
      down.addEventListener('click', () => { rubric.splice(i + 1, 0, rubric.splice(i, 1)[0]); paintRubric() })
      const rm = el('button', { class: 'btn danger sm', type: 'button', text: 'Remove' })
      rm.disabled = locked || !!adminLockReason || rubric.length <= 1
      rm.addEventListener('click', () => { rubric.splice(i, 1); paintRubric() })
      actions.append(up, down, rm)
      row.appendChild(actions)
      box.appendChild(row)
    })
    if (!rubric.length) box.appendChild(el('p', { class: 'muted', text: 'No criteria yet. Add one to start.' }))
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
    rubric.forEach(c => select.appendChild(el('option', { value: c.id || '', text: c.name || '' })))
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
    catch (e) { if (!stopped(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    btn.disabled = tieClosed()
    if (!r.ok) { setLive(errText(r), 'err', btn); return }
    settings.tie_criterion_id = (r.data && r.data.tie_criterion_id) || ''
    setLive('Saved', 'ok', btn)
  }

  function paintMode() {
    $('jg-mode-open').checked = settings.assignment_mode === 'open'
    $('jg-mode-automatic').checked = settings.assignment_mode === 'automatic'
    $('jg-mode-manual').checked = settings.assignment_mode === 'manual'
    $('jg-mode-panel').checked = settings.assignment_mode === 'panel'
    $('jg-per').value = settings.judges_per_team || 2
    const disable = locked || !!adminLockReason || beforeClosed()
    ;['jg-mode-open', 'jg-mode-automatic', 'jg-mode-manual', 'jg-mode-panel', 'jg-per', 'jg-mode-save', 'jg-generate', 'jg-include-organisers'].forEach(id => { $(id).disabled = disable })
    $('jg-before-closed-note').hidden = !beforeClosed()
    const automatic = $('jg-mode-automatic').checked
    $('jg-per-wrap').hidden = !automatic
    $('jg-generate').hidden = !automatic
    $('jg-include-organisers-wrap').hidden = !automatic
  }

  async function saveMode() {
    const mode = main.querySelector('input[name="jg-mode"]:checked').value
    const perTeam = Number($('jg-per').value) || 2
    const btn = $('jg-mode-save')
    btn.disabled = true
    let r
    try { r = await api('PATCH', base + '/judging/settings', { assignment_mode: mode, judges_per_team: perTeam }) }
    catch (e) { if (!stopped(e)) setLive('Cannot reach the server.', 'err', btn); paintMode(); return }
    paintMode()
    if (!r.ok) { setLive(errText(r), 'err', btn); return }
    settings.assignment_mode = (r.data && r.data.assignment_mode) || mode
    settings.judges_per_team = (r.data && r.data.judges_per_team) || perTeam
    setLive('Saved', 'ok', btn)
    paintMode()
  }

  async function generate() {
    const ok = await ui.confirm({ title: 'Generate assignments?', body: 'This replaces any assignments already generated. Continue?', confirmLabel: 'Generate assignments', danger: true })
    if (!ok) return
    const btn = $('jg-generate')
    btn.disabled = true
    let r
    try { r = await api('POST', base + '/assignments/generate', { include_organisers: $('jg-include-organisers').checked }) }
    catch (e) { if (!stopped(e)) setLive('Cannot reach the server.', 'err', btn); paintMode(); return }
    paintMode()
    if (!r.ok) { setLive(errText(r), 'err', btn); return }
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
    card.appendChild(el('h2', { text: 'Conflicts of interest' }))
    const judges = people.filter(p => p.role === 'judge' || p.role === 'organiser')
    if (!conflicts.length) card.appendChild(el('p', { class: 'muted', text: 'No conflicts declared.' }))
    conflicts.forEach(c => {
      const setting = el('div', { class: 'setting' })
      const t = el('div', { class: 't' })
      t.appendChild(el('b', { text: c.judge_name || 'A judge' }))
      t.appendChild(el('span', { text: 'Conflicted with ' + (c.team_name || 'a team') }))
      setting.appendChild(t)
      const rm = el('button', { class: 'btn danger sm', type: 'button', text: 'Remove' })
      rm.disabled = locked || !!adminLockReason
      rm.addEventListener('click', () => removeConflict(c, rm))
      setting.appendChild(rm)
      card.appendChild(setting)
    })
    if (judges.length && teams.length) {
      const form = el('form')
      const row = el('div', { class: 'row-flex' })
      const judgeSelect = el('select', { class: 'select' })
      judges.forEach(p => judgeSelect.appendChild(el('option', { value: p.user_id, text: p.display_name || p.email || 'Someone' })))
      const teamSelect = el('select', { class: 'select' })
      teams.forEach(t => teamSelect.appendChild(el('option', { value: t.id, text: t.name || t.slug })))
      const add = el('button', { class: 'btn', type: 'submit', text: 'Add conflict' })
      judgeSelect.disabled = teamSelect.disabled = add.disabled = locked || !!adminLockReason
      row.append(judgeSelect, teamSelect, add)
      form.appendChild(row)
      form.addEventListener('submit', e => { e.preventDefault(); addConflict(judgeSelect.value, teamSelect.value, add) })
      card.appendChild(form)
    } else {
      card.appendChild(el('button', { class: 'btn', type: 'button', text: 'Add conflict', disabled: '' }))
    }
  }

  async function addConflict(judgeId, teamId, btn) {
    if (!judgeId || !teamId) { setLive('Choose a judge and a team.', 'err', btn); return }
    btn.disabled = true
    let r
    try { r = await api('POST', base + '/conflicts', { team_id: teamId, judge_user_id: judgeId }) }
    catch (e) { if (!stopped(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    btn.disabled = false
    if (!r.ok) { setLive(errText(r), 'err', btn); return }
    await reloadConflicts()
    setLive('Conflict added', 'ok')
  }

  async function removeConflict(c, btn) {
    const ok = await ui.confirm({ title: 'Remove this conflict?', body: (c.judge_name || 'This judge') + ' can be assigned to ' + (c.team_name || 'this team') + ' again.', confirmLabel: 'Remove', danger: true })
    if (!ok) return
    btn.disabled = true
    let r
    try { r = await api('DELETE', base + '/conflicts/' + encodeURIComponent(c.team_id) + '?judge_user_id=' + encodeURIComponent(c.judge_id)) }
    catch (e) { if (!stopped(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    if (!r.ok && r.status !== 204) { setLive(errText(r), 'err', btn); btn.disabled = false; return }
    await reloadConflicts()
  }

  async function reloadConflicts() {
    const r = await api('GET', base + '/conflicts')
    if (r.ok) conflicts = (r.data && r.data.conflicts) || []
    paintConflicts()
    await reloadDashboard()
  }

  function paintProgress(teamsRows, judgeRows) {
    const card = $('jg-progress-card')
    card.textContent = ''
    card.appendChild(el('h2', { text: 'Progress' }))
    if (settings.assignment_mode === 'automatic' && assignmentsCount === 0) {
      card.appendChild(el('div', { class: 'note info', text: 'Assignments not generated yet' }))
    }
    card.appendChild(el('p', { class: 'section-title', text: 'Teams' }))
    const tlist = el('div', { class: 'list' })
    if (!teamsRows.length) tlist.appendChild(el('div', { class: 'empty', text: 'No teams yet.' }))
    teamsRows.forEach(t => {
      const row = el('div', { class: 'r' })
      const mainEl = el('div', { class: 'main' })
      mainEl.appendChild(el('b', { text: t.team_name || 'A team' }))
      row.appendChild(mainEl)
      row.appendChild(el('span', { class: 'meta', text: t.judges_scored + ' judge' + (t.judges_scored === 1 ? '' : 's') + ' scored' }))
      tlist.appendChild(row)
    })
    card.appendChild(tlist)
    card.appendChild(el('p', { class: 'section-title', text: 'Judges' }))
    const jlist = el('div', { class: 'list' })
    if (!judgeRows.length) jlist.appendChild(el('div', { class: 'empty', text: 'No judges yet.' }))
    judgeRows.forEach(j => {
      const row = el('div', { class: 'r' })
      const mainEl = el('div', { class: 'main' })
      mainEl.appendChild(el('b', { text: j.judge_name || 'A judge' }))
      row.appendChild(mainEl)
      row.appendChild(el('span', { class: 'meta', text: j.done_count + ' of ' + j.assigned_count + ' done' }))
      jlist.appendChild(row)
    })
    card.appendChild(jlist)
    if (conflicts.length) {
      card.appendChild(el('p', { class: 'note', text: "A judge's conflict excludes their earlier score for that team from coverage and from the team's total and rank, even if it was scored before the conflict was declared." }))
    }
    const refresh = el('button', { class: 'btn', type: 'button', text: 'Refresh' })
    refresh.addEventListener('click', reloadDashboard)
    card.appendChild(refresh)
  }

  async function reloadDashboard() {
    let r
    try { r = await api('GET', base + '/judging/dashboard') }
    catch (e) { if (!stopped(e)) setLive('Cannot reach the server.', 'err'); return }
    if (!r.ok) { setLive(errText(r), 'err'); return }
    paintProgress((r.data && r.data.teams) || [], (r.data && r.data.judges) || [])
  }

  $('jg-add').addEventListener('click', () => { rubric.push({ id: '', name: '', description: '', weight: 0, max_points: 5 }); paintRubric() })
  $('jg-rubric-save').addEventListener('click', saveRubric)
  $('jg-tie-save').addEventListener('click', saveTie)
  $('jg-mode-save').addEventListener('click', saveMode)
  $('jg-generate').addEventListener('click', generate)
  main.querySelectorAll('input[name="jg-mode"]').forEach(r => r.addEventListener('change', paintMode))

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
    } catch (e) { if (!stopped(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    btn.disabled = locked || !!adminLockReason
    if (!r.ok) { setLive(errText(r), 'err', btn); return }
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
    if (!stopped(e)) setLive('Cannot reach the server.', 'err')
    return
  }
  if (rubricR.ok) rubric = (rubricR.data && rubricR.data.criteria) || []
  if (settingsR.ok) settings = settingsR.data || settings
  if (conflictsR.ok) conflicts = (conflictsR.data && conflictsR.data.conflicts) || []
  if (peopleR.ok) people = peopleR.data || []
  if (teamsR.ok) teams = (teamsR.data && teamsR.data.teams) || []
  if (assignR.ok) assignmentsCount = ((assignR.data && assignR.data.assignments) || []).length

  paintRubric()
  paintTie()
  paintMode()
  paintConflicts()
  await reloadDashboard()
}
