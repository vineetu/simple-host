export function title() { return 'Results' }

// Matches hack_eventpage.go's hackScoreText: at most one decimal, dropping a
// trailing ".0" ("100", "60", "42.5"), so the participant's own result and
// the public page never disagree on how a score reads (SIM-346 cosmetic #4).
function scoreText(n) {
  if (typeof n !== 'number' || Number.isNaN(n)) return ''
  return n.toFixed(1).replace(/\.0$/, '')
}

function ordinal(n) {
  const v = n % 100
  if (v >= 11 && v <= 13) return n + 'th'
  switch (n % 10) {
    case 1: return n + 'st'
    case 2: return n + 'nd'
    case 3: return n + 'rd'
    default: return n + 'th'
  }
}

// The score an organiser chose for ranking (raw or normalised, B-results#2)
// -- each row and "my result" both carry their own deciding_mode, so this
// never has to guess which the event is using.
function decidingScore(row) {
  if (!row) return null
  return row.deciding_mode === 'normalised' ? row.normalised_total : row.raw_total
}

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
    if (!client.isSignedOut(e)) body.innerHTML = `<div class="note bad" role="alert">Cannot reach the server.</div>`
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
  catch (e) { standings = client.isSignedOut(e) ? null : { ok: false } }
  const sdata = standings && standings.ok ? (standings.data || {}) : {}
  const winnersOnly = Array.isArray(sdata.winners)
  const rows = winnersOnly ? sdata.winners : (Array.isArray(sdata.ranking) ? sdata.ranking : [])

  // "Winners only" organiser setting: a team outside the winners keeps its
  // own exact rank and score private too, not just everyone else's -- a
  // kindness call from spec.json, not something the API enforces for the
  // participant's own row (my-results always reveals it to the team itself).
  if (winnersOnly && !amWinner) {
    body.innerHTML = `<div class="card"><h2>Results</h2><p>Winners are posted. Your team isn't in the top placements this time — thanks for building with us.</p><a class="btn" href="/e/${encodeURIComponent(ev.slug)}">Open the event</a></div>`
    return
  }

  const trackLine = mine.track_winner && mine.track
    ? esc(mine.track.name || '') + ' winner' + (mine.track.prize ? ': ' + esc(mine.track.prize) : '') + '.'
    : ''
  const comments = Array.isArray(mine.comments) ? mine.comments : []
  // "of M" only makes sense against the full ranking; a winners-only
  // standings list isn't the whole field, so it's left off rather than
  // implying a count that isn't the real one.
  const totalTeams = (!winnersOnly && rows.length) ? rows.length : null
  const mineScore = decidingScore(mine)
  const headline = mine.rank != null
    ? ordinal(mine.rank) + (totalTeams ? ' of ' + totalTeams : '') + (mine.tied ? ' (tied)' : '')
    : "Not scored yet"
  const scoreLine = mineScore != null ? 'Score ' + scoreText(mineScore) : ''

  const hasRank = rows.some(r => r.rank != null)
  const hasScores = rows.some(r => r.raw_total != null || r.normalised_total != null)
  const table = rows.length ? `<h2 class="section-title">Standings</h2><div class="card flush"><table class="t"><thead><tr>${hasRank ? '<th>Rank</th>' : ''}<th>Team</th>${hasScores ? '<th class="num">Score</th>' : ''}</tr></thead><tbody>${rows.map(r => {
    const mineRow = (r.team_name || '') === (me.team && me.team.name)
    const rScore = decidingScore(r)
    return `<tr>${hasRank ? `<td>${r.rank != null ? esc(r.rank) : ''}</td>` : ''}<td>${esc(r.team_name)}${mineRow ? ' <span class="pill accent">Your team</span>' : ''}</td>${hasScores ? `<td class="num">${rScore != null ? esc(scoreText(rScore)) : ''}</td>` : ''}</tr>`
  }).join('')}</tbody></table></div>` : ''

  body.innerHTML = `<div class="card"><h2>${esc(headline)}</h2>${scoreLine ? `<p class="sub">${esc(scoreLine)}</p>` : ''}${trackLine ? `<p>${trackLine}</p>` : ''}${comments.length ? `<h2>Judges' anonymous comments</h2>${comments.map(c => `<p>“${esc(c)}”</p>`).join('')}` : ''}<a class="btn" href="/e/${encodeURIComponent(ev.slug)}">See it on the event page</a></div>`
    + table
}
