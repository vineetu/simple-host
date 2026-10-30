export function title() { return 'Results' }

function fmt(n) { return typeof n === 'number' ? n.toFixed(1) : '' }

export async function render(ctx) {
  const { main, api: client, ui, event } = ctx
  const api = client.api
  const esc = ui.esc
  const ev = event.event
  const me = event.me
  main.innerHTML = `<div class="ph"><div class="t"><h1>Results</h1><p>${esc(ev.title)}</p></div></div><div id="res-body"><p class="note">Loading…</p></div>`
  const body = main.querySelector('#res-body')

  let mineRes
  try { mineRes = await api('GET', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/my-results') }
  catch (e) {
    if (e.message !== 'signed out') body.innerHTML = `<div class="note bad" role="alert">Cannot reach the server.</div>`
    return
  }
  if (!mineRes.ok) {
    const code = mineRes.data && mineRes.data.code
    body.innerHTML = code === 'no_team'
      ? `<p class="note">Join or start a team to see a result once results are out.</p>`
      : `<div class="note bad" role="alert">Could not load your result.</div>`
    return
  }
  const mine = mineRes.data || {}
  if (!mine.published) { body.innerHTML = `<p class="note">Results haven't been published yet.</p>`; return }

  const amWinner = mine.rank === 1 || mine.track_winner === true
  let standings
  try { standings = await api('GET', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/results') }
  catch (e) { standings = e.message === 'signed out' ? null : { ok: false } }
  const sdata = standings && standings.ok ? (standings.data || {}) : {}
  const winnersOnly = Array.isArray(sdata.winners)
  const rows = winnersOnly ? sdata.winners : (Array.isArray(sdata.results) ? sdata.results : [])

  // "Winners only" organiser setting: a team outside the winners keeps its
  // own exact rank and score private too, not just everyone else's -- a
  // kindness call from spec.json, not something the API enforces for the
  // participant's own row (my-results always reveals it to the team itself).
  if (winnersOnly && !amWinner) {
    body.innerHTML = `<div class="card"><h2>Results</h2><p>Winners are posted. Your team isn't in the top placements this time — thanks for building with us.</p><a class="btn" href="/e/${encodeURIComponent(ev.slug)}">Open the event</a></div>`
    return
  }

  const trackLine = mine.track_winner && mine.track
    ? ' ' + esc(mine.track.name || '') + ' winner' + (mine.track.prize ? ': ' + esc(mine.track.prize) : '') + '.'
    : ''
  const comments = Array.isArray(mine.comments) ? mine.comments : []
  const rankLine = mine.rank != null ? 'Rank ' + mine.rank + (mine.tied ? ' (tied)' : '') : "Judges haven't scored your team yet."
  const scoreLine = mine.total != null ? 'Score: ' + fmt(mine.raw_total) + ' raw · ' + fmt(mine.normalised_total) + ' normalised' : ''

  const hasRank = rows.some(r => r.rank != null)
  const hasScores = rows.some(r => r.total != null)
  const table = rows.length ? `<h2 class="section-title">Standings</h2><div class="card flush"><table class="t"><thead><tr>${hasRank ? '<th>Rank</th>' : ''}<th>Team</th>${hasScores ? '<th class="num">Raw</th><th class="num">Normalised</th>' : ''}</tr></thead><tbody>${rows.map(r => {
    const mineRow = (r.team_name || '') === (me.team && me.team.name)
    return `<tr>${hasRank ? `<td>${r.rank != null ? esc(r.rank) : ''}</td>` : ''}<td>${esc(r.team_name)}${mineRow ? ' <span class="pill accent">Your team</span>' : ''}</td>${hasScores ? `<td class="num">${fmt(r.raw_total)}</td><td class="num">${fmt(r.normalised_total)}</td>` : ''}</tr>`
  }).join('')}</tbody></table></div>` : ''

  body.innerHTML = `<div class="note ok"><b>Your team · ${esc(me.team ? me.team.name : '')}</b><p>${esc(rankLine)}${scoreLine ? ' · ' + esc(scoreLine) : ''}</p></div>`
    + `<div class="card"><h2>Your result</h2><p>${esc(rankLine)}${trackLine}</p>${scoreLine ? `<p>${esc(scoreLine)}</p>` : ''}${comments.length ? `<h2>Judges' anonymous comments</h2>${comments.map(c => `<p>“${esc(c)}”</p>`).join('')}` : ''}<a class="btn" href="/e/${encodeURIComponent(ev.slug)}">View public results page</a></div>`
    + table
}
