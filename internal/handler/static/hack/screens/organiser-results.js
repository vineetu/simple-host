import { errText, downloadFile, localInZone } from './_orgC-util.js'

export function title() { return 'Results' }

// Features on this screen: choose-score-mode, toggle-public-scores,
// toggle-public-ranks, preview-standings, publish-results,
// override-rank-on-publish, choose-results-visibility-mode,
// download-scores-csv, download-results-csv, download-entries-csv,
// download-all-team-sites-archive, download-participants-csv,
// download-teams-csv, enable-peoples-choice-voting, set-voting-window,
// set-voting-eligibility, list-event-in-directory.
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
  const judgingLocked = !!ev.judging_locked_at

  function el(tag, attrs) {
    const node = document.createElement(tag)
    for (const [k, v] of Object.entries(attrs || {})) {
      if (k === 'text') node.textContent = v
      else node.setAttribute(k, v)
    }
    return node
  }

  let settings = { score_mode: 'raw', public_scores: false, public_ranks: false }
  let results = null // array of rows once published, else null
  let fullRanking = false
  let voting = { enabled: false, opens_at: null, closes_at: null, eligibility: 'all_signed_in', directory_listed: false, status: 'disabled', votes_to_remove: {} }
  let savedEligibility = 'all_signed_in'

  main.innerHTML = '<p class="note">Loading…</p>'

  try {
    const [settingsR, resultsR, votingR] = await Promise.all([
      api('GET', base + '/judging/settings'),
      api('GET', base + '/results'),
      api('GET', base + '/voting'),
    ])
    if (settingsR.ok) settings = settingsR.data || settings
    if (resultsR.ok) {
      results = (resultsR.data && resultsR.data.results) || (Array.isArray(resultsR.data) ? resultsR.data : null)
      fullRanking = !!(resultsR.data && resultsR.data.full_ranking)
    }
    if (votingR.ok) { voting = votingR.data || voting; savedEligibility = voting.eligibility }
  } catch (e) {
    if (!stopped(e)) setLive('Cannot reach the server.', 'err')
    return
  }
  const published = !!(results && results.length)

  main.innerHTML = `
<div class="ph"><div class="t"><h1>Results</h1><p>Preview, publish and share your standings.</p></div><button class="btn primary" type="button" id="res-publish-top">${published ? 'Publish again' : 'Publish results'}</button></div>
<div class="note" id="res-status"></div>
<section class="card"><h2>Scoring rules</h2><div class="field"><label>Score used for ranking<select class="select" id="res-score-mode"><option value="normalised">Normalised mean</option><option value="raw">Raw mean</option></select></label></div><button class="btn primary" type="button" id="res-score-mode-save">Save</button></section>
<section class="card"><h2>Publish</h2>
<details id="res-preview"><summary class="btn">Preview current standings</summary><div id="res-preview-body"></div></details>
<p class="muted">Override a tied rank by hand if one remains after the tie-break.</p>
<div id="res-override"></div>
<p class="help" id="res-lock-hint" hidden>Lock judging on the Judging section first.</p>
<button class="btn primary" type="button" id="res-publish-bottom">${published ? 'Publish again' : 'Publish results'}</button>
<p class="small muted" id="res-republish-note" hidden>The public page still shows the last snapshot until you do.</p>
</section>
<section class="card"><h2>Show on the public page</h2><div class="field"><label>Visibility<select class="select" id="res-visibility"><option value="true">Full ranking</option><option value="false">Winners only</option></select></label></div><label class="check"><input type="checkbox" id="res-public-scores"><span>Show numeric scores publicly</span></label><label class="check"><input type="checkbox" id="res-public-ranks"><span>Show ranks publicly</span></label><p class="small muted">"Full ranking with scores off" is a real combination — show everyone took part, without a public scoreboard.</p><p class="help" id="res-visibility-hint" hidden>Publish results first to control what the public page shows.</p><button class="btn primary" type="button" id="res-visibility-save">Save</button></section>
<section class="card"><h2>Voting &amp; directory</h2>
<p><span class="pill" id="res-vote-status"></span></p>
<label class="check"><input type="checkbox" id="res-vote-enabled"><span>Enable audience voting</span></label>
<div class="grid2"><div class="field"><label>Starts · <span id="res-tz-1"></span><input class="input" type="datetime-local" id="res-vote-opens"></label></div><div class="field"><label>Ends · <span id="res-tz-2"></span><input class="input" type="datetime-local" id="res-vote-closes"></label></div></div>
<button class="btn primary" type="button" id="res-vote-window-save">Save voting window</button>
<hr class="rule">
<div class="field"><label>Who can vote<select class="select" id="res-vote-eligibility"><option value="all_signed_in">Anyone signed in</option><option value="event_members">Approved event members</option><option value="participants">Approved participants only</option></select></label></div>
<button class="btn primary" type="button" id="res-vote-eligibility-save">Change who can vote</button>
<hr class="rule">
<label class="check"><input type="checkbox" id="res-directory-listed"><span>List this event in the simple-hack.app directory</span></label>
<p class="small muted">Directory listing is independent of voting.</p>
<button class="btn primary" type="button" id="res-directory-save">Save directory listing</button>
</section>
<section class="card"><h2>Export</h2><div class="stack">
<p><button class="btn" type="button" id="res-dl-results">Download results (CSV)</button></p>
<p><button class="btn" type="button" id="res-dl-scores">Download scores (CSV)</button></p>
<p><button class="btn" type="button" id="res-dl-entries">Download entries CSV</button></p>
<p><button class="btn" type="button" id="res-dl-teams">Download teams CSV</button></p>
<p><button class="btn" type="button" id="res-dl-participants">Download participants CSV</button></p>
<p><button class="btn" type="button" id="res-dl-sites">Download all team sites</button></p>
</div></section>`

  // --- status -----------------------------------------------------------
  function paintStatus() {
    const box = $('res-status')
    if (published) {
      box.className = 'note ok'
      const when = ui.dateTime(ev.results_published_at, ev, { timeZoneName: 'short' })
      box.innerHTML = `<b>Results are live${when ? ' — published ' + ui.esc(when) : ''}.</b><p>Publish again any time scores change. It's a snapshot, not a live feed.</p>`
    } else {
      box.className = 'note warn'
      box.innerHTML = `<b>Not published yet</b><p>Publish again any time scores change. It's a snapshot, not a live feed.</p>`
    }
  }
  paintStatus()

  // --- scoring rules ------------------------------------------------------
  $('res-score-mode').value = settings.score_mode || 'raw'
  $('res-score-mode-save').addEventListener('click', async () => {
    const btn = $('res-score-mode-save')
    btn.disabled = true
    let r
    try { r = await api('PATCH', base + '/judging/settings', { score_mode: $('res-score-mode').value }) }
    catch (e) { if (!stopped(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    btn.disabled = false
    if (!r.ok) { setLive(errText(r), 'err', btn); return }
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
    catch (e) { if (!stopped(e)) setLive('Cannot reach the server.', 'err'); previewLoaded = false; return }
    if (!r.ok) { body.textContent = errText(r); previewLoaded = false; return }
    const rows = (r.data && r.data.results) || []
    const table = el('table', { class: 't' })
    table.innerHTML = '<thead><tr><th>Rank</th><th>Team</th><th>Score</th></tr></thead>'
    const tbody = el('tbody')
    rows.forEach(row => {
      const tr = el('tr')
      tr.appendChild(el('td', { text: row.rank == null ? 'Not scored' : '#' + row.rank }))
      tr.appendChild(el('td', { text: row.team_name || 'A team' }))
      tr.appendChild(el('td', { text: row.total == null ? 'No score' : String(row.total) }))
      tbody.appendChild(tr)
    })
    table.appendChild(tbody)
    body.textContent = ''
    body.appendChild(table)
  })

  // --- tie override (built from the last published snapshot) ----------
  function paintOverride() {
    const box = $('res-override')
    box.textContent = ''
    if (!results) return
    const byRank = {}
    results.forEach(row => {
      if (row.tied && row.rank != null) { (byRank[row.rank] = byRank[row.rank] || []).push(row) }
    })
    const ranks = Object.keys(byRank)
    if (!ranks.length) return
    box.appendChild(el('p', { class: 'muted small', text: 'Some teams tie. Give each one a final place, then publish again to apply it:' }))
    const inputs = []
    ranks.forEach(rank => {
      box.appendChild(el('p', { class: 'small muted', text: 'Tied for #' + rank + ':' }))
      byRank[rank].forEach(row => {
        const field = el('div', { class: 'field' })
        const label = el('label', { text: row.team_name || 'A team' })
        const input = el('input', { class: 'input', type: 'number', min: '1', step: '1', value: String(row.rank) })
        label.appendChild(input)
        field.appendChild(label)
        box.appendChild(field)
        inputs.push({ team_id: row.team_id, input })
      })
    })
    const go = el('button', { class: 'btn', type: 'button', text: 'Save order and publish' })
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
    const top = $('res-publish-top'), bottom = $('res-publish-bottom')
    top.disabled = bottom.disabled = true
    const body = {}
    if (overrides && overrides.length) body.rank_overrides = overrides
    let r
    try { r = await api('POST', base + '/results/publish', body) }
    catch (e) { if (!stopped(e)) setLive('Cannot reach the server.', 'err', btn || bottom); top.disabled = bottom.disabled = false; return }
    top.disabled = bottom.disabled = false
    if (!r.ok) { setLive(errText(r), 'err', btn || bottom); return }
    results = (r.data && r.data.results) || []
    fullRanking = !!(r.data && r.data.full_ranking)
    ev.results_published = true
    ev.results_published_at = new Date().toISOString()
    paintStatus()
    paintOverride()
    paintVisibility()
    previewLoaded = false
    $('res-publish-top').textContent = 'Publish again'
    $('res-publish-bottom').textContent = 'Publish again'
    $('res-republish-note').hidden = false
    setLive('Results published', 'ok', btn || bottom)
  }
  $('res-publish-top').addEventListener('click', () => publish(null, $('res-publish-top')))
  $('res-publish-bottom').addEventListener('click', () => publish(null, $('res-publish-bottom')))
  $('res-lock-hint').hidden = judgingLocked
  $('res-publish-top').disabled = !judgingLocked
  $('res-publish-bottom').disabled = !judgingLocked
  $('res-republish-note').hidden = !published

  // --- visibility ---------------------------------------------------------
  function paintVisibility() {
    $('res-visibility').value = String(fullRanking)
    $('res-public-scores').checked = !!settings.public_scores
    $('res-public-ranks').checked = !!settings.public_ranks
    $('res-visibility').disabled = !results
    $('res-visibility-hint').hidden = !!results
  }
  paintVisibility()
  $('res-visibility-save').addEventListener('click', async () => {
    const btn = $('res-visibility-save')
    btn.disabled = true
    let ok = true
    let r1
    try { r1 = await api('PATCH', base + '/judging/settings', { public_scores: $('res-public-scores').checked, public_ranks: $('res-public-ranks').checked }) }
    catch (e) { if (!stopped(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    if (!r1.ok) { setLive(errText(r1), 'err', btn); btn.disabled = false; return }
    settings.public_scores = $('res-public-scores').checked
    settings.public_ranks = $('res-public-ranks').checked
    if (results) {
      let r2
      try { r2 = await api('PATCH', base + '/results', { full_ranking: $('res-visibility').value === 'true' }) }
      catch (e) { if (!stopped(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
      if (!r2.ok) { ok = false; setLive(errText(r2), 'err', btn) }
      else fullRanking = !!(r2.data && r2.data.full_ranking)
    }
    btn.disabled = false
    if (ok) setLive('Saved', 'ok', btn)
  })

  // --- voting & directory -------------------------------------------------
  $('res-tz-1').textContent = $('res-tz-2').textContent = ev.time_zone || 'UTC'
  function paintVoting() {
    const labels = { open: 'Audience voting: open', upcoming: 'Audience voting: upcoming', closed: 'Audience voting: closed', disabled: 'Audience voting: off' }
    const pill = $('res-vote-status')
    pill.textContent = labels[voting.status] || labels.disabled
    pill.className = 'pill' + (voting.status === 'open' ? ' ok' : voting.status === 'disabled' ? '' : ' warn')
    $('res-vote-enabled').checked = !!voting.enabled
    $('res-vote-opens').value = localInZone(voting.opens_at, ev.time_zone)
    $('res-vote-closes').value = localInZone(voting.closes_at, ev.time_zone)
    $('res-vote-eligibility').value = voting.eligibility || 'all_signed_in'
    $('res-directory-listed').checked = !!voting.directory_listed
  }
  paintVoting()

  async function putVoting(body, btn) {
    btn.disabled = true
    let r
    try { r = await api('PUT', base + '/voting', body) }
    catch (e) { if (!stopped(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return null }
    btn.disabled = false
    if (!r.ok && r.data && r.data.code !== 'vote_removal_confirmation') { setLive(errText(r), 'err', btn); return null }
    return r
  }

  $('res-vote-window-save').addEventListener('click', async () => {
    const btn = $('res-vote-window-save')
    const r = await putVoting({
      enabled: $('res-vote-enabled').checked,
      opens_at: $('res-vote-opens').value,
      closes_at: $('res-vote-closes').value,
      eligibility: voting.eligibility,
    }, btn)
    if (!r || !r.ok) return
    voting = { ...voting, ...r.data }
    paintVoting()
    setLive('Saved', 'ok', btn)
  })

  $('res-vote-eligibility-save').addEventListener('click', async () => {
    const btn = $('res-vote-eligibility-save')
    const newEligibility = $('res-vote-eligibility').value
    let confirmRemoval = false
    if (newEligibility !== savedEligibility) {
      let preview
      try { preview = await api('GET', base + '/voting') }
      catch (e) { if (!stopped(e)) setLive('Cannot reach the server.', 'err', btn); return }
      if (!preview.ok) { setLive(errText(preview), 'err', btn); return }
      const count = (preview.data.votes_to_remove || {})[newEligibility] || 0
      if (count > 0) {
        const ok = await ui.confirm({ title: 'Remove earlier votes?', body: 'Narrowing this removes ' + count + (count === 1 ? ' vote' : ' votes') + " that no longer qualify. This can't be undone. Continue?", confirmLabel: 'Save and remove votes', danger: true })
        if (!ok) return
        confirmRemoval = true
      }
    }
    const r = await putVoting({
      enabled: voting.enabled,
      opens_at: voting.opens_at,
      closes_at: voting.closes_at,
      eligibility: newEligibility,
      confirm_vote_removal: confirmRemoval,
    }, btn)
    if (!r || !r.ok) return
    voting = { ...voting, ...r.data }
    savedEligibility = newEligibility
    paintVoting()
    setLive(r.data.votes_removed ? r.data.votes_removed + (r.data.votes_removed === 1 ? ' earlier vote' : ' earlier votes') + ' removed' : 'Saved', 'ok', btn)
  })

  $('res-directory-save').addEventListener('click', async () => {
    const btn = $('res-directory-save')
    btn.disabled = true
    let r
    try { r = await api('PATCH', base + '/directory', { listed: $('res-directory-listed').checked }) }
    catch (e) { if (!stopped(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    btn.disabled = false
    if (!r.ok) { setLive(errText(r), 'err', btn); return }
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
    ['res-dl-sites', '/export/projects.tar.gz', slug + '-projects.tar.gz'],
  ]
  downloads.forEach(([id, path, filename]) => {
    $(id).addEventListener('click', () => downloadFile(base + path, filename, $(id), setLive))
  })
}
