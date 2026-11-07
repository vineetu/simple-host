import { href } from '../nav.js'
import { onSubmit } from '../platform.js'

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
  const setupFirst = ['draft', 'open', 'building', 'closed'].includes(ev.stage) && !locked

  let rubric = []
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

  // Judging closes (notices contract 2026-11-05, CHANGE 2026-11-05): an
  // explicit, organiser-set event time -- no inference from a schedule row
  // any more. PATCH /v1/hack/events/{slug} {"judging_ends_at": "..."}
  // ("" clears), same parsing as submission_deadline/ends_at. Drives the
  // judging-reminder emails on Entries' Reminder times card, and shows as a
  // fixed, read-only "Judging closes" row on the Event page's Schedule
  // (schedule.js setInLink's 'judging' case points back here).
  const restBlock = `
<section class="card" id="jg-progress-card"><h2>Progress</h2><p class="note">Loading…</p></section>
<section class="card" id="jg-conflicts-card"><h2>Conflicts of interest</h2><p class="note">Loading…</p></section>
${archived ? '' : '<section class="card" id="jg-lockzone"></section>'}`

  // Every organiser can also judge (the API accepts "judge" or "organiser"
  // on every /judge/* route) — only a platform-admin inspection cannot, so
  // the entry point to the organiser's own scoring queue follows that, not
  // whether assignments happen to have been generated yet.
  const canJudge = !!ctx.event.can?.judge
  main.innerHTML = `
<div class="ph"><div class="t"><h1>Judging</h1><p>Rubric, assignments and scoring progress.</p></div>
<span id="jg-top-action"></span>${canJudge ? `<a class="btn" href="${ui.esc('/e/' + encodeURIComponent(slug) + '/judge?s=home')}">Score as a judge</a>` : ''}</div>
${adminLockReason ? `<div class="note warn">${ui.esc(adminLockReason)}</div>` : ''}
<p><a href="${ui.esc(href('organiser', slug, 'settings') + '&tab=judging')}">Judging settings</a></p>
<section class="card"><h2>Assignments</h2><label class="check" id="jg-include-organisers-wrap"><input type="checkbox" id="jg-include-organisers"><span>Include organisers who can judge</span></label><p class="note" id="jg-assign-note" hidden></p><div class="form-actions"><button class="btn" type="button" id="jg-generate">Generate assignments</button><a class="btn" href="${ui.esc(href('organiser', slug, 'assignments'))}">Open the assignment grid</a></div></section>
${restBlock}
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
      // Finding #9 (prod full test, round 7): this used to repeat the
      // rubric card's own "Save rubric" button up here too -- two buttons
      // with the same label on one screen, different sizes. The card's
      // button (right under the fields it saves) is the one that stays.
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
      const actions = ui.el('div', { class: 'form-actions' })
      actions.appendChild(go)
      form.appendChild(actions)
      onSubmit(form, go, e => { e.preventDefault(); doUnlock(textarea.value, go) })
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
    paintTopAction(); paintLockZone(); paintMode(); paintConflicts()
    // Repaint Progress too: a former judge's Delete scores button (and any
    // real judge's own row) needs to show as locked right away, not only
    // after the next unrelated reload.
    await reloadDashboard()
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
    paintTopAction(); paintLockZone(); paintMode(); paintConflicts()
    await reloadDashboard()
    setLive('Judging unlocked', 'ok')
  }

  function paintMode() {
    const automatic = settings.assignment_mode === 'automatic'
    $('jg-generate').hidden = !automatic
    $('jg-generate').disabled = locked || !!adminLockReason
    $('jg-include-organisers-wrap').hidden = !automatic
    $('jg-include-organisers').disabled = locked || !!adminLockReason
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
      const row = ui.el('div', { class: 'control-row' })
      const judgeSelect = ui.el('select', { class: 'select', 'aria-label': 'Judge' })
      judges.forEach(p => judgeSelect.appendChild(ui.el('option', { value: p.user_id, text: p.display_name || p.email || 'Someone' })))
      const teamSelect = ui.el('select', { class: 'select', 'aria-label': 'Team' })
      teams.forEach(t => teamSelect.appendChild(ui.el('option', { value: t.id, text: t.name || t.slug })))
      const add = ui.el('button', { class: 'btn', type: 'submit', text: 'Add' })
      judgeSelect.disabled = teamSelect.disabled = add.disabled = locked || !!adminLockReason
      row.append(judgeSelect, teamSelect, add)
      form.appendChild(row)
      onSubmit(form, add, e => { e.preventDefault(); addConflict(judgeSelect.value, teamSelect.value, add) })
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
    // Audit item 2 (2026-11-02): in panel mode, a team matches no track
    // panel and so gets no judges at all (internal/db/hack_judging_options.go,
    // the panel branch requires p.track_id=t.track_id). `teams` already
    // carries each team's track (GET .../teams, teamOrganiserFrom), so this
    // is computed client-side -- no server change needed to name them.
    if (settings.assignment_mode === 'panel') {
      const noTrack = teams.filter(t => !t.track)
      if (noTrack.length) {
        const note = ui.el('div', { class: 'note warn' })
        const n = noTrack.length
        note.appendChild(document.createTextNode(
          n + ' team' + (n === 1 ? '' : 's') + " with no track won't be judged until " +
          (n === 1 ? 'it picks' : 'they pick') + ' a track, or you assign one: ' + noTrack.map(t => t.name || 'A team').join(', ') + '. '
        ))
        note.appendChild(ui.el('a', { class: 'btn sm', href: href('organiser', slug, 'teams'), text: 'Go to Teams' }))
        card.appendChild(note)
      }
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
    if (!visibleJudges.length && !formerJudges.length) jlist.appendChild(ui.el('div', { class: 'empty', text: 'No judges yet.' }))
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
    // "Left the event" rows: their own scores stay counted (server-frozen),
    // Delete scores is the only control, disabled while locked (or by the
    // same admin/ended lock reason as everything else here).
    if (formerJudges.length) jlist.appendChild(ui.el('h3', { id: 'jg-former', text: 'Former judges' }))
    formerJudges.forEach(f => {
      const row = ui.el('div', { class: 'r' })
      const mainEl = ui.el('div', { class: 'main' })
      mainEl.appendChild(ui.el('b', { text: f.display_name || 'A judge' }))
      mainEl.appendChild(ui.el('span', { class: 'pill', text: 'Left the event' }))
      row.appendChild(mainEl)
      // assigned_count is today's live eligible-team count, which can drop
      // below (or to zero under) counted_teams once the frozen scores
      // outlive a reassignment or a mode change (Codex review, SIM-340) --
      // an "of" denominator smaller than the numerator reads as broken, so
      // drop it rather than show an impossible fraction like "4 of 0".
      const formerMeta = f.assigned_count >= f.counted_teams && f.assigned_count > 0
        ? `Scored ${f.counted_teams} of ${f.assigned_count}, still counted`
        : `${f.counted_teams} team${f.counted_teams === 1 ? '' : 's'} scored, still counted`
      row.appendChild(ui.el('span', { class: 'meta', text: formerMeta }))
      const del = ui.el('button', { class: 'btn quiet sm', type: 'button', text: 'Delete scores' })
      if (locked || adminLockReason) {
        del.disabled = true
        if (locked && !adminLockReason) del.title = 'Unlock judging to change scores.'
      }
      del.addEventListener('click', () => deleteFormerJudge(f, del))
      row.appendChild(del)
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
    formerJudges = (r.data && r.data.former_judges) || []
    paintProgress((r.data && r.data.teams) || [], (r.data && r.data.judges) || [])
  }

  // Delete a departed judge's scores (SIM-340): organiser only, refused
  // while judging is locked. Published results change only on the next
  // publish -- the confirm body says so (copy-deck.md, "Organiser Judging").
  async function deleteFormerJudge(f, btn) {
    const name = f.display_name || 'This judge'
    // Round-5 C3: a typed confirm, the event's own address name, same as
    // Settings' Delete this event -- the server already requires
    // confirm=slug (SIM-340 security review), so the dialog should ask for
    // exactly the text that route checks, not a plain OK.
    const ok = await ui.confirmTyped({
      title: `Delete ${ui.possessive(name)} scores?`,
      body: `${name} left the event. Their scores and comments for ${f.counted_teams} team${f.counted_teams === 1 ? '' : 's'} will be deleted and stop counting toward totals and ranks. Published results only change when you publish again.`,
      matchText: slug,
      fieldLabel: `Type ${slug} to confirm`,
      confirmLabel: 'Delete scores', danger: true
    })
    if (!ok) return
    btn.disabled = true
    let r
    // confirm repeats the event's address name (Grok security review,
    // SIM-340), the same way Settings' Delete this event does -- a
    // destructive route with no server-side confirm is one stray retry from
    // deleting the wrong judge's scores.
    try { r = await api('DELETE', base + '/judging/former-judges/' + encodeURIComponent(f.user_id), { confirm: slug }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    if (!r.ok && r.status !== 204) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
    setLive('Scores deleted', 'ok')
    await reloadDashboard()
  }

  $('jg-generate').addEventListener('click', generate)

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
  // This screen's initial six-way Promise.all has no check of its own; a
  // render superseded while it was in flight must not go on to paint a
  // stale rubric/progress/conflicts screen over whatever newer view has
  // already taken `main` (same ctx.alive() gap as organiser-people.js
  // above, Codex review finding 4, SIM-344 phase 2).
  if (!ctx.alive()) return
  if (rubricR.ok) rubric = (rubricR.data && rubricR.data.criteria) || []
  if (settingsR.ok) settings = settingsR.data || settings
  if (conflictsR.ok) conflicts = (conflictsR.data && conflictsR.data.conflicts) || []
  if (peopleR.ok) people = peopleR.data || []
  if (teamsR.ok) teams = (teamsR.data && teamsR.data.teams) || []
  if (assignR.ok) assignmentsCount = ((assignR.data && assignR.data.assignments) || []).length
  draftMode = settings.assignment_mode
  draftPerTeam = settings.judges_per_team

  paintMode()
  paintConflicts()
  await reloadDashboard()
}
