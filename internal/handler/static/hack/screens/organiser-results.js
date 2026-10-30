import { href } from '../nav.js'

export function title() { return 'Results' }

// Features on this screen: choose-score-mode, toggle-public-scores,
// toggle-public-ranks, preview-standings, publish-results,
// override-rank-on-publish, choose-results-visibility-mode,
// download-scores-csv, download-results-csv, download-entries-csv,
// download-all-team-sites-archive, download-participants-csv,
// download-teams-csv, enable-peoples-choice-voting, set-voting-window,
// set-voting-eligibility, list-event-in-directory.
export async function render(ctx) {
  const { main, ui, role } = ctx
  const client = ctx.api
  const api = client.api
  const ev = ctx.event.event
  const slug = ev.slug
  const base = '/v1/hack/events/' + encodeURIComponent(slug)
  const $ = id => main.querySelector('#' + id)
  const setLive = ui.live(main)
  const judgingLocked = !!ev.judging_locked_at
  // S14: the scoring/visibility/voting settings below don't change any
  // more once the event has ended (judging settings PATCH and voting PUT
  // both already refuse archived server-side) -- Publish, the Directory
  // toggle (its own PATCH has no archived check, so it's left live) and
  // the downloads stay as they were, since they're still useful after End.
  const archived = ev.stage === 'archived'
  const lockReason = archived ? "This event has ended. Settings can't change any more." : ''

  let settings = { score_mode: 'raw', public_scores: false, public_ranks: false }
  let results = null // array of rows once published, else null
  let preview = [] // current standings, used to build overrides before first publish
  let fullRanking = false
  let voting = { enabled: false, opens_at: null, closes_at: null, eligibility: 'all_signed_in', directory_listed: false, status: 'disabled', votes_to_remove: {} }

  main.innerHTML = '<p class="note">Loading…</p>'

  try {
    const [settingsR, resultsR, votingR, previewR] = await Promise.all([
      api('GET', base + '/judging/settings'),
      api('GET', base + '/results'),
      api('GET', base + '/voting'),
      api('GET', base + '/judging/preview'),
    ])
    if (settingsR.ok) settings = settingsR.data || settings
    if (resultsR.ok) {
      results = (resultsR.data && resultsR.data.results) || (Array.isArray(resultsR.data) ? resultsR.data : null)
      fullRanking = !!(resultsR.data && resultsR.data.full_ranking)
    }
    if (votingR.ok) voting = votingR.data || voting
    if (previewR.ok) preview = (previewR.data && previewR.data.results) || []
  } catch (e) {
    if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err')
    return
  }
  let published = !!(results && results.length)

  main.innerHTML = `
<div class="ph"><div class="t"><h1>Results</h1><p>Preview, publish and share your standings.</p></div><button class="btn primary" type="button" id="res-publish-top">${published ? 'Publish again' : 'Publish results'}</button></div>
${lockReason ? `<div class="note warn" role="alert">${ui.esc(lockReason)}</div>` : ''}
<div class="note" id="res-status"></div>
<p class="note warn" id="res-lock-hint" hidden>Lock judging first, on <a href="${ui.esc(href(role, slug, 'judging'))}">Judging</a>.</p>
<section class="card"><h2>Publish</h2>
<div id="res-override"></div>
<p class="small muted" id="res-republish-note" hidden></p>
</section>
<details class="card" id="res-preview"><summary>Preview current standings</summary><p class="muted small">Override a tied rank by hand if one remains after the tie-break.</p><div id="res-preview-body"></div></details>
<section class="card"><h2>How teams are ranked</h2><div class="field"><label>Score used for ranking<select class="select" id="res-score-mode"${archived ? ' disabled' : ''}><option value="normalised">Average, adjusted for strict and generous judges</option><option value="raw">Average score</option></select></label></div><button class="btn" type="button" id="res-score-mode-save"${archived ? ' disabled' : ''}>Save</button></section>
<section class="card"><h2>Show on the public page</h2><p class="small muted">Tip: Full ranking with scores off lists every team without a scoreboard.</p><div class="field"><label>Visibility<select class="select" id="res-visibility"${archived ? ' disabled' : ''}><option value="true">Full ranking</option><option value="false">Winners only</option></select></label></div><label class="check"><input type="checkbox" id="res-public-scores"${archived ? ' disabled' : ''}><div><b>Show scores</b><span>Teams always see their own score.</span></div></label><label class="check"><input type="checkbox" id="res-public-ranks"${archived ? ' disabled' : ''}><span>Show ranks</span></label><p class="help" id="res-visibility-hint" hidden>Publish results first to control what the public page shows.</p><button class="btn" type="button" id="res-visibility-save"${archived ? ' disabled' : ''}>Save</button></section>
<section class="card"><h2>People’s choice</h2>
<p><span class="pill" id="res-vote-status"></span></p>
<label class="check"><input type="checkbox" id="res-vote-enabled"${archived ? ' disabled' : ''}><span>Let visitors vote</span></label>
<div class="grid2"><div class="field"><label>Starts · <span id="res-tz-1"></span><input class="input" type="datetime-local" id="res-vote-opens"${archived ? ' disabled' : ''}></label></div><div class="field"><label>Ends · <span id="res-tz-2"></span><input class="input" type="datetime-local" id="res-vote-closes"${archived ? ' disabled' : ''}></label></div></div>
<button class="btn" type="button" id="res-vote-window-save"${archived ? ' disabled' : ''}>Save voting window</button>
<hr class="rule">
<div class="field"><label>Who can vote<select class="select" id="res-vote-eligibility"${archived ? ' disabled' : ''}><option value="all_signed_in">Anyone signed in</option><option value="event_members">Approved event members</option><option value="participants">Approved participants only</option></select></label></div>
<button class="btn" type="button" id="res-vote-eligibility-save"${archived ? ' disabled' : ''}>Save</button>
</section>
<section class="card"><h2>Directory</h2>
<label class="check"><input type="checkbox" id="res-directory-listed"><span>List this event on Find a hackathon</span></label>
<button class="btn" type="button" id="res-directory-save">Save</button>
</section>
<section class="card"><h2>Downloads</h2><div class="row-flex">
<button class="btn" type="button" id="res-dl-results">Results (CSV)</button>
<button class="btn" type="button" id="res-dl-scores">Scores (CSV)</button>
<button class="btn" type="button" id="res-dl-entries">Entries (CSV)</button>
<button class="btn" type="button" id="res-dl-teams">Teams (CSV)</button>
<button class="btn" type="button" id="res-dl-participants">People (CSV)</button>
<button class="btn" type="button" id="res-dl-sites">All team sites (.tar.gz)</button>
</div></section>`

  // --- status -----------------------------------------------------------
  function paintStatus() {
    const box = $('res-status')
    const secondary = 'Publishing takes a snapshot. Publish again if scores change.'
    if (published) {
      box.className = 'note ok'
      // #19: this used to roll its own timeZoneName formatter, a straight
      // duplicate of what ui.dateTime's own {zone:true} option already does.
      const when = ui.dateTime(ev.results_published_at, ev, { zone: true })
      box.innerHTML = `<b>${when ? 'Published ' + ui.esc(when) : 'Published'}</b><p>${ui.esc(secondary)}</p>`
    } else {
      box.className = 'note warn'
      box.innerHTML = `<b>Not published yet</b><p>${ui.esc(secondary)}</p>`
    }
  }
  paintStatus()

  // Confusing #4: the stale-snapshot note used to show right after every
  // publish, before anything could have changed. It only makes sense once
  // the live standings actually differ from what was last published --
  // compared here by each team's total, since a rank override can
  // legitimately make the snapshot's rank differ from a plain recompute
  // without any score having changed.
  function totalsMap(rows) {
    const m = {}
    ;(rows || []).forEach(row => { if (row && row.team_id) m[row.team_id] = row.total })
    return m
  }
  function scoresChangedSincePublish() {
    if (!results) return false
    const pub = totalsMap(results)
    const cur = totalsMap(preview)
    const ids = new Set([...Object.keys(pub), ...Object.keys(cur)])
    for (const id of ids) { if (pub[id] !== cur[id]) return true }
    return false
  }
  function paintRepublishNote(stale) {
    const note = $('res-republish-note')
    note.hidden = !stale
    note.textContent = stale ? 'Scores have changed since you published. The public page still shows the earlier snapshot.' : ''
  }
  paintRepublishNote(scoresChangedSincePublish())

  // --- scoring rules ------------------------------------------------------
  $('res-score-mode').value = settings.score_mode || 'raw'
  $('res-score-mode-save').addEventListener('click', async () => {
    const btn = $('res-score-mode-save')
    btn.disabled = true
    let r
    try { r = await api('PATCH', base + '/judging/settings', { score_mode: $('res-score-mode').value }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    btn.disabled = false
    if (!r.ok) { setLive(ui.errText(r), 'err', btn); return }
    settings.score_mode = (r.data && r.data.score_mode) || $('res-score-mode').value
    setLive('Saved', 'ok', btn)
  })

  // --- preview --------------------------------------------------------
  let previewLoaded = false
  $('res-preview').addEventListener('toggle', async () => {
    if (!$('res-preview').open || previewLoaded) return
    previewLoaded = true
    const body = $('res-preview-body')
    body.textContent = 'Loading…'
    let r
    try { r = await api('GET', base + '/judging/preview') }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err'); previewLoaded = false; return }
    if (!r.ok) { body.textContent = ui.errText(r); previewLoaded = false; return }
    const rows = (r.data && r.data.results) || []
    const table = ui.el('table', { class: 't' })
    table.innerHTML = '<thead><tr><th>Rank</th><th>Team</th><th>Score</th></tr></thead>'
    const tbody = ui.el('tbody')
    rows.forEach(row => {
      const tr = ui.el('tr')
      tr.appendChild(ui.el('td', { text: row.rank == null ? 'Not scored' : '#' + row.rank }))
      tr.appendChild(ui.el('td', { text: row.team_name || 'A team' }))
      tr.appendChild(ui.el('td', { text: row.total == null ? 'No score' : String(row.total) }))
      tbody.appendChild(tr)
    })
    table.appendChild(tbody)
    body.textContent = ''
    body.appendChild(table)
  })

  // --- tie override (from the last published snapshot once there is one,
  // otherwise from the current standings, so a tie can be resolved before
  // the very first publish too) ----------
  function paintOverride() {
    const box = $('res-override')
    box.textContent = ''
    const rows = (results && results.length) ? results : preview
    if (!rows || !rows.length) return
    const byRank = {}
    rows.forEach(row => {
      if (row.tied && row.rank != null) { (byRank[row.rank] = byRank[row.rank] || []).push(row) }
    })
    const ranks = Object.keys(byRank)
    if (!ranks.length) return
    box.appendChild(ui.el('p', { class: 'muted small', text: 'Some teams tie. Give each one a final place, then publish again to apply it:' }))
    const inputs = []
    ranks.forEach(rank => {
      box.appendChild(ui.el('p', { class: 'small muted', text: 'Tied for #' + rank + ':' }))
      byRank[rank].forEach(row => {
        const field = ui.el('div', { class: 'field' })
        const label = ui.el('label', { text: row.team_name || 'A team' })
        const input = ui.el('input', { class: 'input', type: 'number', min: '1', step: '1', value: String(row.rank) })
        label.appendChild(input)
        field.appendChild(label)
        box.appendChild(field)
        inputs.push({ team_id: row.team_id, input })
      })
    })
    const go = ui.el('button', { class: 'btn', type: 'button', text: 'Save order and publish' })
    go.addEventListener('click', () => {
      const overrides = inputs.map(x => ({ team_id: x.team_id, rank: Number(x.input.value) }))
      publish(overrides, go)
    })
    box.appendChild(go)
  }
  paintOverride()

  // --- publish ----------------------------------------------------------
  async function publish(overrides, btn) {
    const ok = await ui.confirm({ title: 'Publish results?', body: 'This replaces the public snapshot with the standings right now. Continue?', confirmLabel: published ? 'Publish again' : 'Publish results' })
    if (!ok) return
    const top = $('res-publish-top')
    const near = btn || top
    top.disabled = true
    if (btn && btn !== top) btn.disabled = true
    const body = {}
    if (overrides && overrides.length) body.rank_overrides = overrides
    let r
    try { r = await api('POST', base + '/results/publish', body) }
    catch (e) {
      if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', near)
      top.disabled = false
      if (btn && btn !== top) btn.disabled = false
      return
    }
    top.disabled = false
    if (btn && btn !== top) btn.disabled = false
    if (!r.ok) { setLive(ui.errText(r), 'err', near); return }
    results = (r.data && r.data.results) || []
    fullRanking = !!(r.data && r.data.full_ranking)
    ev.results_published = true
    ev.results_published_at = new Date().toISOString()
    published = !!(results && results.length)
    paintStatus()
    paintOverride()
    paintVisibility()
    previewLoaded = false
    $('res-publish-top').textContent = 'Publish again'
    // A publish just took a fresh snapshot of the live standings, so it can
    // never be stale the moment it lands.
    paintRepublishNote(false)
    setLive('Results published', 'ok', near)
  }
  $('res-publish-top').addEventListener('click', () => publish(null, $('res-publish-top')))
  $('res-lock-hint').hidden = judgingLocked
  $('res-publish-top').disabled = !judgingLocked

  // --- visibility ---------------------------------------------------------
  function paintVisibility() {
    $('res-visibility').value = String(fullRanking)
    $('res-public-scores').checked = !!settings.public_scores
    $('res-public-ranks').checked = !!settings.public_ranks
    $('res-visibility').disabled = archived || !results
    $('res-visibility-hint').hidden = !!results || archived
  }
  paintVisibility()
  $('res-visibility-save').addEventListener('click', async () => {
    const btn = $('res-visibility-save')
    btn.disabled = true
    let ok = true
    let r1
    try { r1 = await api('PATCH', base + '/judging/settings', { public_scores: $('res-public-scores').checked, public_ranks: $('res-public-ranks').checked }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    if (!r1.ok) { setLive(ui.errText(r1), 'err', btn); btn.disabled = false; return }
    settings.public_scores = $('res-public-scores').checked
    settings.public_ranks = $('res-public-ranks').checked
    if (results) {
      let r2
      try { r2 = await api('PATCH', base + '/results', { full_ranking: $('res-visibility').value === 'true' }) }
      catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
      if (!r2.ok) { ok = false; setLive(ui.errText(r2), 'err', btn) }
      else fullRanking = !!(r2.data && r2.data.full_ranking)
    }
    btn.disabled = false
    if (ok) setLive('Saved', 'ok', btn)
  })

  // --- voting & directory -------------------------------------------------
  $('res-tz-1').textContent = $('res-tz-2').textContent = ev.time_zone || 'UTC'
  // paintVoting only touches the voting-window fields: the directory
  // checkbox has its own save button and its own paint (paintDirectory), so
  // an unrelated voting save never overwrites a directory choice still
  // sitting unsaved in the checkbox.
  function paintVoting() {
    const labels = { open: 'Voting open', upcoming: 'Voting upcoming', closed: 'Voting closed', disabled: 'Voting off' }
    const pill = $('res-vote-status')
    pill.textContent = labels[voting.status] || labels.disabled
    pill.className = 'pill' + (voting.status === 'open' ? ' ok' : voting.status === 'disabled' ? '' : ' warn')
    $('res-vote-enabled').checked = !!voting.enabled
    $('res-vote-opens').value = ui.localInputValue(voting.opens_at, ev.time_zone)
    $('res-vote-closes').value = ui.localInputValue(voting.closes_at, ev.time_zone)
    $('res-vote-eligibility').value = voting.eligibility || 'all_signed_in'
  }
  function paintDirectory() {
    $('res-directory-listed').checked = !!voting.directory_listed
  }
  paintVoting()
  paintDirectory()

  // The gallery-closed refusal (409 gallery_closed) is common to both
  // voting-window and eligibility saves (either can re-send `enabled: true`
  // once the gallery has since been closed) -- handled once here instead of
  // per caller, with a readable note and a link to where the fix lives,
  // instead of the server's plain sentence.
  function clearGalleryNote() {
    const existing = $('res-vote-gallery-note')
    if (existing) existing.remove()
  }
  function showGalleryNote(btn) {
    clearGalleryNote()
    const p = document.createElement('p')
    p.id = 'res-vote-gallery-note'
    p.className = 'note bad'
    p.setAttribute('role', 'alert')
    // #12: match organiser-event-page.js's actual heading ("Look") and
    // toggle copy ("Show entries on the event page") -- the section itself
    // has been renamed since this note was first written, which left it
    // naming a heading ("Gallery & appearance") that no longer exists.
    p.append(document.createTextNode('Turn on Show entries on the event page first, on '))
    const a = document.createElement('a')
    a.href = href(role, slug, 'event-page') + '#appearance'
    a.textContent = 'Event page → Look'
    p.append(a, document.createTextNode('.'))
    btn.insertAdjacentElement('afterend', p)
  }

  // Handles the server's authoritative vote_removal_confirmation 409 itself
  // (a count fetched ahead of time can go stale if a vote arrives in
  // between), so every caller gets the confirm-and-retry regardless of
  // which field changed.
  async function putVoting(body, btn) {
    clearGalleryNote()
    btn.disabled = true
    let r
    try { r = await api('PUT', base + '/voting', body) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return null }
    if (!r.ok && r.data && r.data.code === 'gallery_closed') {
      btn.disabled = false
      showGalleryNote(btn)
      return null
    }
    if (!r.ok && r.data && r.data.code === 'vote_removal_confirmation') {
      const count = r.data.votes_to_remove || 0
      const ok = await ui.confirm({ title: 'Remove earlier votes?', body: 'This removes ' + count + (count === 1 ? ' vote that no longer qualifies' : ' votes that no longer qualify') + ". This can't be undone. Continue?", confirmLabel: 'Save and remove votes', danger: true })
      if (!ok) { btn.disabled = false; return null }
      try { r = await api('PUT', base + '/voting', { ...body, confirm_vote_removal: true }) }
      catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return null }
    }
    btn.disabled = false
    if (!r.ok) { setLive(ui.errText(r), 'err', btn); return null }
    return r
  }

  function applyVotingSave(data) {
    // directory_listed is excluded: the voting endpoint doesn't own that
    // setting, and merging its (possibly stale) echo back in would repaint
    // over an unsaved directory choice.
    const { directory_listed, ...rest } = data || {}
    voting = { ...voting, ...rest }
  }

  $('res-vote-window-save').addEventListener('click', async () => {
    const btn = $('res-vote-window-save')
    const r = await putVoting({
      enabled: $('res-vote-enabled').checked,
      opens_at: $('res-vote-opens').value,
      closes_at: $('res-vote-closes').value,
      eligibility: voting.eligibility,
    }, btn)
    if (!r) return
    applyVotingSave(r.data)
    paintVoting()
    setLive(r.data.votes_removed ? r.data.votes_removed + (r.data.votes_removed === 1 ? ' earlier vote' : ' earlier votes') + ' removed' : 'Saved', 'ok', btn)
  })

  $('res-vote-eligibility-save').addEventListener('click', async () => {
    const btn = $('res-vote-eligibility-save')
    const newEligibility = $('res-vote-eligibility').value
    const r = await putVoting({
      enabled: voting.enabled,
      opens_at: voting.opens_at,
      closes_at: voting.closes_at,
      eligibility: newEligibility,
    }, btn)
    if (!r) return
    applyVotingSave(r.data)
    paintVoting()
    setLive(r.data.votes_removed ? r.data.votes_removed + (r.data.votes_removed === 1 ? ' earlier vote' : ' earlier votes') + ' removed' : 'Saved', 'ok', btn)
  })

  $('res-directory-save').addEventListener('click', async () => {
    const btn = $('res-directory-save')
    btn.disabled = true
    let r
    try { r = await api('PATCH', base + '/directory', { listed: $('res-directory-listed').checked }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    btn.disabled = false
    if (!r.ok) { setLive(ui.errText(r), 'err', btn); return }
    voting.directory_listed = !!(r.data && r.data.listed)
    setLive('Saved', 'ok', btn)
  })

  // --- export -------------------------------------------------------------
  const downloads = [
    ['res-dl-results', '/export/results.csv', slug + '-results.csv'],
    ['res-dl-scores', '/export/scores.csv', slug + '-scores.csv'],
    ['res-dl-entries', '/export/entries.csv', slug + '-entries.csv'],
    ['res-dl-teams', '/export/teams.csv', slug + '-teams.csv'],
    ['res-dl-participants', '/export/participants.csv', slug + '-participants.csv'],
    ['res-dl-sites', '/export/projects.tar.gz', slug + '-sites.tar.gz'],
  ]
  downloads.forEach(([id, path, filename]) => {
    $(id).addEventListener('click', async () => {
      if (await client.downloadFile(base + path, filename, $(id))) setLive('Downloaded ' + filename, 'ok', $(id))
    })
  })
}
