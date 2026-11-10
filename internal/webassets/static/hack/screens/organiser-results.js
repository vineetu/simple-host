import { href } from '../nav.js'
import { scoresChangedSincePublish } from './_publish-results.js'

export function title() { return 'Results' }

// Features on this screen: choose-score-mode, toggle-public-scores,
// toggle-public-ranks, preview-standings,
//  choose-results-visibility-mode,
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
  const setLive = ui.live(main)
  let results = null // array of rows once published, else null
  let preview = [] // current standings, used to build tie overrides
  let previewOk = false // #11: only true once /judging/preview actually loaded -- a failed read must not be read as "every published team is now missing"

  main.innerHTML = '<p class="note">Loading…</p>'

  try {
    const [resultsR, previewR] = await Promise.all([
      api('GET', base + '/results'),
      api('GET', base + '/judging/preview'),
    ])
    if (resultsR.ok) {
      results = (resultsR.data && resultsR.data.results) || (Array.isArray(resultsR.data) ? resultsR.data : null)
    }
    if (previewR.ok) { preview = (previewR.data && previewR.data.results) || []; previewOk = true }
  } catch (e) {
    if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err')
    return
  }
  let published = !!(results && results.length)

  main.innerHTML = `
<div class="ph"><div class="t"><h1>Results</h1><p>Preview and share your standings.</p></div></div>
<div class="note" id="res-status"></div>
${!published ? `<p>Publish from <a href="${ui.esc(href('organiser', slug, 'home'))}">Home</a> when ready.</p>` : ''}
<p class="small muted" id="res-republish-note" hidden></p>
<details class="card" id="res-preview"><summary>Preview current standings</summary><div id="res-preview-body"></div></details>
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

  const stale = scoresChangedSincePublish(results, preview, previewOk)
  $('res-republish-note').hidden = !stale
  $('res-republish-note').textContent = stale ? 'Scores have changed since you published. Publish again from Home.' : ''

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
