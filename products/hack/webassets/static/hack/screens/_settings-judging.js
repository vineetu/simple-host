import { href } from '../nav.js'
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
  const setupLocked = locked || ['judging', 'results', 'archived'].includes(ev.stage)
  // A platform-admin inspection, a taken-down event or an ended event blocks
  // every write here too, on top of (never instead of) the judging-lock and
  // stage rules below — the same ui.writeLockReason() every other screen
  // applies, including its ended-event lock, which this screen had been
  // skipping.
  const adminLockReason = ui.writeLockReason(ev, ctx.event.admin_view)
  const archived = ev.stage === 'archived'
  const tieClosed = () => locked || !!adminLockReason || ['judging', 'results', 'archived'].includes(ev.stage)
  const beforeClosed = () => !['closed', 'judging', 'results', 'archived'].includes(ev.stage)
  // Layout: before judging starts, the rubric is still being written, so
  // setup comes first (organiser-judging~setup.html); its own Save rubric
  // button lives under the rubric card, not repeated in the header. Once
  // judging opens or is locked, progress/conflicts lead and setup collapses
  // out of the way (organiser-judging.html / ~locked.html). Decided once
  // per page load — the stage doesn't change on this screen, only `locked`
  // does.
  let rubric = []
  let organiserSelectionLoaded = false
  let settings = { assignment_mode: 'open', judges_per_team: 2, score_mode: 'raw', tie_criterion_id: '', public_scores: false, public_ranks: false }
  let conflicts = []
  let people = []
  let teams = []
  let assignmentsCount = 0
  // Former judges (SIM-340): someone who judged and then left. Their scores
  // keep counting (counted_team_ids frozen server-side) -- an organiser can
  // delete them from here, which is the only way they stop counting.
  let formerJudges = []
  // Draft vs. saved: the radios must keep whatever the organiser just
  // clicked (B1 -- clicking used to call paintMode(), which re-read
  // settings.assignment_mode and snapped the choice straight back to
  // whatever was last saved). draftMode/draftPerTeam hold the in-progress
  // choice; paintMode() always paints from the draft, never from settings;
  // saveMode() is the only thing that moves a draft into settings.
  let draftMode = settings.assignment_mode
  let draftPerTeam = settings.judges_per_team

  function closesHTML() {
    if (archived) {
      return `<section class="card" id="closes"><h2>Judging closes</h2><p>${ev.judging_ends_at ? 'Judging closed ' + ui.esc(ui.dateTime(ev.judging_ends_at, ev)) : 'No time was set.'}</p></section>`
    }
    return `<section class="card" id="closes"><h2>Judging closes</h2><div class="field"><label>Judging closes · ${ui.esc(ev.time_zone || 'UTC')}<input class="input" id="jg-closes-input" type="datetime-local" value="${ui.esc(ui.localInputValue(ev.judging_ends_at, ev))}"${adminLockReason ? ' disabled' : ''}></label><p class="help">Judges with teams left to score are reminded this long before this time (set on Entries' Reminder times). Leave it empty for no judge reminder.</p></div><div class="form-actions"><button class="btn primary" type="button" id="jg-closes-save"${adminLockReason ? ' disabled' : ''}>Save</button><button class="btn quiet" type="button" id="jg-closes-clear"${adminLockReason || !ev.judging_ends_at ? ' disabled' : ''}>Clear</button></div></section>`
  }

  const setupInner = `
${ev.stage === 'judging' ? '<div class="note">Locked while judging. Move the event back to Entries closed to change.</div>' : ''}
<section class="card" id="rubric"><h2>Rubric</h2><div id="jg-rubric"></div><div class="form-actions"><button class="btn" type="button" id="jg-add">Add a criterion</button><span class="hint" id="jg-weight"></span><button class="btn" type="button" id="jg-rubric-save">Save rubric</button></div></section>
<section class="card" id="tie-break"><h2>Tie-break</h2><div class="field"><label>First tie criterion<select class="select" id="jg-tie"></select></label>${!tieClosed() ? '<p class="help">You can change this until judging starts.</p>' : ''}</div><div class="form-actions"><button class="btn" type="button" id="jg-tie-save">Save</button></div></section>
${closesHTML()}
<section class="card" id="mode"><h2>How judges are assigned</h2>
<label class="check"><input type="radio" name="jg-mode" value="open" id="jg-mode-open"><span><b>Open</b><span>Judges pick any team from the full list.</span></span></label>
<label class="check"><input type="radio" name="jg-mode" value="automatic" id="jg-mode-automatic"><span><b>Automatic</b><span>Simple Hack assigns judges to teams evenly.</span></span></label>
<label class="check"><input type="radio" name="jg-mode" value="manual" id="jg-mode-manual"><span><b>Manual</b><span>You assign each judge to specific teams.</span></span></label>
<label class="check"><input type="radio" name="jg-mode" value="panel" id="jg-mode-panel"><span><b>Panel</b><span>Judges are grouped by track, one panel per track.</span></span></label>
<div class="field" id="jg-per-wrap"><label>Judges per team<input class="input" type="number" min="1" max="20" id="jg-per"></label></div>
<div class="note info" id="jg-before-closed-note" hidden>You can set this before entries close, but teams may still be forming.</div>
<p class="note" id="jg-assign-note" hidden></p>

<section id="organiser-judges"><h2>Organisers who also judge</h2><div id="jg-organisers"></div><div id="jg-judge-link"></div></section>
<div class="form-actions"><button class="btn" type="button" id="jg-mode-save">Save</button></div>
</section>`


 main.innerHTML = setupInner
  const closesForm = ui.trackForm(ctx, $('closes'))
  function weightTotal() { return rubric.reduce((sum, c) => sum + (Number(c.weight) || 0), 0) }

  function paintRubric() {
    const box = $('jg-rubric')
    box.textContent = ''
    rubric.forEach((c, i) => {
      const row = ui.el('div', { class: 'crit crit-row' })
      const nameField = ui.el('div', { class: 'field' })
      const nameLabel = ui.el('label', { text: 'Name' })
      const nameIn = ui.el('input', { 'data-help': 'rubric', class: 'input', value: c.name || '' })
      if (setupLocked || adminLockReason) nameIn.disabled = true
      nameIn.addEventListener('input', () => { c.name = nameIn.value })
      nameLabel.appendChild(nameIn)
      nameField.appendChild(nameLabel)
      const descField = ui.el('div', { class: 'field' })
      const descLabel = ui.el('label', { text: 'Description (optional)' })
      const descIn = ui.el('input', { 'data-help': 'rubric', class: 'input', value: c.description || '' })
      if (setupLocked || adminLockReason) descIn.disabled = true
      descIn.addEventListener('input', () => { c.description = descIn.value })
      descLabel.appendChild(descIn)
      descField.appendChild(descLabel)
      const weightField = ui.el('div', { class: 'field' })
      const weightLabel = ui.el('label', { text: 'Weight %' })
      const weightIn = ui.el('input', { 'data-help': 'rubric', class: 'input', type: 'number', min: '0', max: '100', value: String(c.weight == null ? 0 : c.weight) })
      if (setupLocked || adminLockReason) weightIn.disabled = true
      weightIn.addEventListener('input', () => { c.weight = Number(weightIn.value) || 0; paintWeight() })
      weightLabel.appendChild(weightIn)
      weightField.appendChild(weightLabel)
      const ptsField = ui.el('div', { class: 'field' })
      const ptsLabel = ui.el('label', { text: 'Max points' })
      const ptsIn = ui.el('input', { 'data-help': 'rubric', class: 'input', type: 'number', min: '1', max: '10', value: String(c.max_points == null ? 5 : c.max_points) })
      if (setupLocked || adminLockReason) ptsIn.disabled = true
      ptsIn.addEventListener('input', () => { c.max_points = Number(ptsIn.value) || 1 })
      ptsLabel.appendChild(ptsIn)
      ptsField.appendChild(ptsLabel)
      row.append(nameField, weightField, ptsField, descField)
      const actions = ui.el('div', { class: 'row-flex' })
      const up = ui.el('button', { class: 'btn sm', type: 'button', text: 'Up', 'aria-label': 'Move ' + (c.name || 'this criterion') + ' up' })
      up.disabled = setupLocked || !!adminLockReason || i === 0
      up.addEventListener('click', () => { rubric.splice(i - 1, 0, rubric.splice(i, 1)[0]); paintRubric() })
      const down = ui.el('button', { class: 'btn sm', type: 'button', text: 'Down', 'aria-label': 'Move ' + (c.name || 'this criterion') + ' down' })
      down.disabled = setupLocked || !!adminLockReason || i === rubric.length - 1
      down.addEventListener('click', () => { rubric.splice(i + 1, 0, rubric.splice(i, 1)[0]); paintRubric() })
      const rm = ui.el('button', { class: 'btn danger sm', type: 'button', text: 'Remove' })
      rm.disabled = setupLocked || !!adminLockReason || rubric.length <= 1
      rm.addEventListener('click', () => { rubric.splice(i, 1); paintRubric() })
      actions.append(up, down, rm)
      row.appendChild(actions)
      box.appendChild(row)
    })
    if (!rubric.length) box.appendChild(ui.el('p', { class: 'muted', text: 'No criteria yet. Add one to start.' }))
    paintWeight()
    $('jg-add').disabled = setupLocked || !!adminLockReason || rubric.length >= 10
    $('jg-rubric-save').disabled = setupLocked || !!adminLockReason
  }

  function paintWeight() {
    const p = $('jg-weight')
    if (!rubric.length) { p.textContent = ''; return }
    const total = weightTotal()
    p.textContent = 'Total weight: ' + total + '%' + (total === 100 ? '' : ' (must add up to 100%)')
  }

  let tieForm
  function paintTie() {
    const select = $('jg-tie')
    select.textContent = ''
    select.appendChild(ui.el('option', { value: '', text: 'Pick a criterion' }))
    rubric.forEach(c => select.appendChild(ui.el('option', { value: c.id || '', text: c.name || '' })))
    select.value = settings.tie_criterion_id || ''
    select.disabled = tieClosed()
    $('jg-tie-save').disabled = tieClosed()
    tieForm?.reset()
  }

  async function saveTie() {
    const value = $('jg-tie').value
    const btn = $('jg-tie-save')
    btn.disabled = true
    let r
    const sent = tieForm.snapshot()
    try { r = await api('PATCH', base + '/judging/settings', { tie_criterion_id: value }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    btn.disabled = tieClosed()
    if (!r.ok) { setLive(ui.errText(r), 'err', btn); return }
    const editedDuring = tieForm.snapshot() !== sent
    settings.tie_criterion_id = (r.data && r.data.tie_criterion_id) || ''
    editedDuring ? tieForm.reset(sent) : tieForm.reset()
    setLive('Saved', 'ok', btn)
  }

  async function onSaveCloses() {
    const input = $('jg-closes-input')
    const btn = $('jg-closes-save')
    const value = input.value
    btn.disabled = true
    let r
    const sent = closesForm.snapshot()
    try { r = await api('PATCH', base, { judging_ends_at: value }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = !!adminLockReason; return }
    if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = !!adminLockReason; return }
    const editedDuring = closesForm.snapshot() !== sent
    ev.judging_ends_at = (r.data && r.data.event && r.data.event.judging_ends_at) || null
    btn.disabled = !!adminLockReason
    const clearBtn = $('jg-closes-clear')
    if (clearBtn) clearBtn.disabled = !!adminLockReason || !ev.judging_ends_at
    editedDuring ? closesForm.reset(sent) : closesForm.reset()
    setLive('Saved', 'ok', btn)
  }

  async function onClearCloses() {
    const ok = await ui.confirm({ title: 'Clear judging closes?', body: 'Judges stop getting a reminder before judging closes. Clear it?', confirmLabel: 'Clear', danger: true })
    if (!ok) return
    $('jg-closes-input').value = ''
    await onSaveCloses()
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
    // Judging locks the setup until the event returns to Entries closed.
    const disable = setupLocked || !!adminLockReason
    ;['jg-mode-open', 'jg-mode-automatic', 'jg-mode-manual', 'jg-mode-panel', 'jg-per', 'jg-mode-save'].forEach(id => { $(id).disabled = disable })
    $('jg-before-closed-note').hidden = !beforeClosed()
    const automatic = draftMode === 'automatic'
    $('jg-per-wrap').hidden = !automatic

  }

  async function saveMode() {
    const mode = draftMode
    const perTeam = draftPerTeam || 2
    const btn = $('jg-mode-save')
    btn.disabled = true
    let r
    const sent = modeForm.snapshot()
    try { r = await api('PATCH', base + '/judging/settings', { assignment_mode: mode, judges_per_team: perTeam, ...(organiserSelectionLoaded ? { organiser_judges: [...main.querySelectorAll('[data-organiser-judge]:checked')].map(n => n.value) } : {}) }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); paintMode(); return }
    if (!r.ok) { paintMode(); setLive(ui.errText(r), 'err', btn); return }
    const editedDuring = modeForm.snapshot() !== sent
    settings.assignment_mode = (r.data && r.data.assignment_mode) || mode
    settings.judges_per_team = (r.data && r.data.judges_per_team) || perTeam
    draftMode = settings.assignment_mode
    draftPerTeam = settings.judges_per_team
    paintMode()
    editedDuring ? modeForm.reset(sent) : modeForm.reset()
    setLive('Saved', 'ok', btn)
  }

  $('jg-add').addEventListener('click', () => { rubric.push({ id: '', name: '', description: '', weight: 0, max_points: 5 }); paintRubric() })
  $('jg-rubric-save').addEventListener('click', saveRubric)
  $('jg-tie-save').addEventListener('click', saveTie)
  const closesSave = $('jg-closes-save')
  if (closesSave) closesSave.addEventListener('click', onSaveCloses)
  const closesClear = $('jg-closes-clear')
  if (closesClear) closesClear.addEventListener('click', onClearCloses)
  $('jg-mode-save').addEventListener('click', saveMode)
  main.querySelectorAll('input[name="jg-mode"]').forEach(r => r.addEventListener('change', () => {
    draftMode = main.querySelector('input[name="jg-mode"]:checked').value
    paintMode()
  }))
  $('jg-per').addEventListener('input', () => { draftPerTeam = Number($('jg-per').value) || 2; paintMode() })

  async function saveRubric() {
    if (weightTotal() !== 100) { setLive('Weights must add up to 100%.', 'err', $('jg-weight')); return }
    const body = { criteria: rubric.map(c => ({ id: c.id || '', name: c.name || '', description: c.description || '', weight: Number(c.weight) || 0, max_points: Number(c.max_points) || 1 })) }
    const btn = $('jg-rubric-save')
    btn.disabled = true
    let r
    const sent = rubricForm.snapshot()
    try {
      r = await api('PUT', base + '/rubric', body)
      if (!r.ok && r.data && r.data.code === 'score_deletion_confirmation') {
        if (await ui.confirm({ title: 'Delete scores?', body: r.data.error, confirmLabel: 'Save rubric', danger: true })) {
          body.confirm_score_deletion = true
          r = await api('PUT', base + '/rubric', body)
        }
      }
    } catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    btn.disabled = setupLocked || !!adminLockReason
    if (!r.ok) { setLive(ui.errText(r), 'err', btn); return }
    const editedDuring = rubricForm.snapshot() !== sent
    rubric = (r.data && r.data.criteria) || []
    paintRubric()
    paintTie()
    editedDuring ? rubricForm.reset(sent) : rubricForm.reset()
    setLive('Rubric saved', 'ok', btn)
  }


  const [rubricR, settingsR, peopleR] = await Promise.all([api('GET', base + '/rubric'), api('GET', base + '/judging/settings'), api('GET', base + '/people')])
  if (!ctx.alive()) return
  if (rubricR.ok) { rubric = rubricR.data.criteria || [] }
  organiserSelectionLoaded = settingsR.ok && peopleR.ok
  if (settingsR.ok) settings = settingsR.data
  $('jg-organisers').innerHTML = (organiserSelectionLoaded ? peopleR.data : []).filter(p => p.role === 'organiser').map(p => `<label class="check"><input data-help="judging.organiser-judges" data-organiser-judge type="checkbox" value="${ui.esc(p.user_id)}"${(settings.organiser_judges || []).includes(p.user_id) ? ' checked' : ''}${setupLocked || adminLockReason ? ' disabled' : ''}><span>${ui.esc(p.display_name || p.email)}</span></label>`).join('')
  const judgeLink = ctx.event.organiser?.judge_url || ''
  $('jg-judge-link').append(ui.copyButton(judgeLink, 'Copy judge link'))
  draftMode = settings.assignment_mode
  draftPerTeam = settings.judges_per_team
  paintRubric(); paintTie(); paintMode()
  const rubricForm = ui.trackForm(ctx, $('rubric'))
  tieForm = ui.trackForm(ctx, $('tie-break'))
  const modeForm = ui.trackForm(ctx, $('mode'))
}
