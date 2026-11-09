import { href } from '../nav.js'
import { offerStageAnnouncement } from './_stage-announce-dialog.js'

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
  // SIM-341: the same "Tell participants?" offer organiser-overview.js
  // makes after every stage move, here after Publish results -- same
  // emailable count source (organiser.counts) as that screen and
  // organiser-announcements.js's own "New announcement" form.
  const data = ctx.event
  const emailableCount = (data.organiser && data.organiser.counts && data.organiser.counts.emailable_participants) || 0
  // S14: the scoring/visibility/voting settings below don't change any
  // more once the event has ended (judging settings PATCH and voting PUT
  // both already refuse archived server-side) -- Publish, the Directory
  // toggle (its own PATCH has no archived check, so it's left live) and
  // the downloads stay as they were, since they're still useful after End.
  const archived = ev.stage === 'archived'
  const lockReason = archived ? "This event has ended. Settings can't change any more." : ''

  let results = null // array of rows once published, else null
  let preview = [] // current standings, used to build tie overrides
  let previewOk = false // #11: only true once /judging/preview actually loaded -- a failed read must not be read as "every published team is now missing"
  let fullRanking = false

  main.innerHTML = '<p class="note">Loading…</p>'

  try {
    const [resultsR, previewR] = await Promise.all([
      api('GET', base + '/results'),
      api('GET', base + '/judging/preview'),
    ])
    if (resultsR.ok) {
      results = (resultsR.data && resultsR.data.results) || (Array.isArray(resultsR.data) ? resultsR.data : null)
      fullRanking = !!(resultsR.data && resultsR.data.full_ranking)
    }
    if (previewR.ok) { preview = (previewR.data && previewR.data.results) || []; previewOk = true }
  } catch (e) {
    if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err')
    return
  }
  let published = !!(results && results.length)

  // S14 (retest 3): the lock note used to sit right above the still-active
  // Publish button, reading as if it covered Publish too -- Publish, the
  // Directory toggle and the downloads stay live on purpose after End (see
  // the comment above), so they're grouped before the note now, and the
  // note sits directly over the section where things actually do lock,
  // instead of over the whole page.
  main.innerHTML = `
<div class="ph" id="res-publish-target"><div class="t"><h1>Results</h1><p>Preview, publish and share your standings.</p></div><button data-help="publish" class="btn primary" type="button" id="res-publish-top">${published ? 'Publish again' : 'Publish results'}</button></div>
<div class="note" id="res-status"></div>
<p class="note warn" id="res-lock-hint" hidden>Lock judging first, on <a href="${ui.esc(href(role, slug, 'judging'))}">Judging</a>.</p>
<section class="card" id="res-publish-section"><h2>Publish</h2>
<div id="res-override"></div>
<p class="small muted" id="res-republish-note" hidden></p>
</section>
<details class="card" id="res-preview"><summary>Preview current standings</summary><p class="muted small">Override a tied rank by hand if one remains after the tie-break.</p><div id="res-preview-body"></div></details>
<section class="card" id="res-downloads"><h2>Downloads</h2><div class="row-flex">
<button class="btn" type="button" id="res-dl-results">Results (CSV)</button>
<button class="btn" type="button" id="res-dl-scores">Scores (CSV)</button>
<button class="btn" type="button" id="res-dl-entries">Entries (CSV)</button>
<button class="btn" type="button" id="res-dl-teams">Teams (CSV)</button>
<button class="btn" type="button" id="res-dl-participants">People (CSV)</button>
<button class="btn" type="button" id="res-dl-sites">All team sites (.tar.gz)</button>
</div></section>
<p><a href="?s=settings&tab=results">Results settings</a></p>`

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
    // #11: if the preview read failed, `preview` stays [] -- that must not
    // be read as every published team now missing from the live standings.
    if (!results || !previewOk) return false
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
  // Cosmetic #3 (Grok re-test, round 4): the Publish card held nothing but
  // its own heading whenever there were no ties to override and nothing
  // stale to republish -- true right after a fresh publish, and after End.
  // Hidden rather than always shown empty; paintOverride and
  // paintRepublishNote both call this after they repaint their own content,
  // so it never goes stale.
  function syncPublishSection() {
    $('res-publish-section').hidden = !$('res-override').children.length && $('res-republish-note').hidden
  }

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

  // --- tie overrides from current standings, retaining valid published choices ----------
  function savedPlace(row, group) {
    const saved = (results || []).find(r => r.team_id === row.team_id)
    if (!saved || saved.tie_decider !== 'organiser' || saved.total !== row.total) return null
    const first = Number(row.rank)
    if (saved.rank < first || saved.rank >= first + group.length) return null
    // Keep choices only for the same tied group and unchanged scoring inputs.
    if (!group.every(r => (results || []).some(old => old.team_id === r.team_id && old.total === r.total && old.rank >= first && old.rank < first + group.length))) return null
    return saved.rank
  }
  function paintOverride() {
    const box = $('res-override')
    box.textContent = ''
    const rows = previewOk ? preview : (results || [])
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
      // Finding #9 (prod full test, round 7): each row used to be its own
      // ".field.inline" -- a separate grid per row, so its label column
      // sized to that one team's own name and the inputs landed in a
      // ragged line (e.g. "Alpha Squad"'s input further right than "Team
      // 1"'s). One shared grid per tied group lines every input in this
      // group up under the one with the longest name.
      const group = ui.el('div', { class: 'tie-group' })
      byRank[rank].forEach(row => {
        const id = 'res-tie-' + row.team_id
        const label = ui.el('label', { for: id, text: row.team_name || 'A team' })
        const input = ui.el('input', { 'data-help': 'ties', class: 'input', id, type: 'number', min: '1', step: '1', value: String(savedPlace(row, byRank[rank]) ?? row.rank) })
        group.append(label, input)
        inputs.push({ team_id: row.team_id, input })
      })
      box.appendChild(group)
    })
    const go = ui.el('button', { class: 'btn', type: 'button', text: 'Save order and publish' })
    go.addEventListener('click', () => {
      const overrides = inputs.map(x => ({ team_id: x.team_id, rank: Number(x.input.value) }))
      publish(overrides, go)
    })
    const actions = ui.el('div', { class: 'form-actions' })
    actions.appendChild(go)
    box.appendChild(actions)
  }
  paintOverride()
  const tieOrderForm = ui.trackForm(ctx, $('res-override'))
  const publishForm = ui.trackForm(ctx, $('res-publish-target'))
  syncPublishSection()

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
    const form = overrides ? tieOrderForm : publishForm
    const sent = form.snapshot()
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
    const editedDuring = form.snapshot() !== sent
    results = (r.data && r.data.results) || []
    fullRanking = !!(r.data && r.data.full_ranking)
    ev.results_published = true
    ev.results_published_at = new Date().toISOString()
    published = !!(results && results.length)
    paintStatus()
    if (overrides) paintOverride()
    editedDuring ? form.reset(sent) : form.reset()
    previewLoaded = false
    $('res-publish-top').textContent = 'Publish again'
    // A publish just took a fresh snapshot of the live standings, so it can
    // never be stale the moment it lands.
    paintRepublishNote(false)
    syncPublishSection()
    setLive('Results published', 'ok', near)
    // SIM-341: offered on every successful publish, first or republish --
    // this is the only code path that publishes results on this screen --
    // before the refresh below, same ordering as every stage-changing
    // screen (the move itself always lands before the offer).
    // Not after End event: an ended event takes no announcements.
    if (ev.stage !== 'archived') await offerStageAnnouncement(ctx, { move: 'publish_results', back: false, emailableCount, setLive })
    // SIM-271: publishing before Results now moves the event there itself
    // (and locks judging), so the stage pill and track in the shell -- and
    // the Overview next step -- need the same refresh every other
    // stage-changing screen already does; without it they kept showing
    // Judging until the next navigation.
    if (ev.stage !== 'results' && ev.stage !== 'archived') await ctx.refresh()
  }
  $('res-publish-top').addEventListener('click', () => publish(null, $('res-publish-top')))
  $('res-lock-hint').hidden = judgingLocked
  $('res-publish-top').disabled = !judgingLocked

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
