export function title() { return 'Results' }

// Matches hack_eventpage.go's hackScoreText (review #8): full precision
// (two decimals, same as the server stores and the public JSON API
// returns), trailing zeros trimmed ("100", "60", "42.5"), but never rounded
// to fewer decimals than it has -- toFixed(1) used to round 81.25 to "81.3"
// (or "81.2", depending on float rounding), which could make two
// differently-scored teams read the same. This keeps the participant's own
// result and the public page agreeing on how a score reads (SIM-346
// cosmetic #4), including for scores a hundredth apart.
function scoreText(n) {
  if (typeof n !== 'number' || Number.isNaN(n)) return ''
  return n.toFixed(2).replace(/0+$/, '').replace(/\.$/, '')
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
  // The count is the teams that got a place (my-results' ranked_teams), not
  // every team in the event: teams with no entry or no scores have no place,
  // so "1st of 2" in a four-team event reads "1st of 2 judged teams".
  const ranked = typeof mine.ranked_teams === 'number' ? mine.ranked_teams : rows.filter(r => r.rank != null).length
  const totalTeams = (!winnersOnly && ranked) ? ranked : null
  const mineScore = decidingScore(mine)
  const headline = mine.rank != null
    ? (mine.tied ? 'Joint ' : '') + ordinal(mine.rank) + (totalTeams ? ' of ' + totalTeams + (totalTeams === 1 ? ' judged team' : ' judged teams') : '')
    : "Not scored yet"
  const scoreLine = mineScore != null ? 'Score ' + scoreText(mineScore) : ''

  const hasRank = rows.some(r => r.rank != null)
  const hasScores = rows.some(r => r.raw_total != null || r.normalised_total != null)
  const table = rows.length ? `<h2 class="section-title">Standings</h2><div class="card flush"><table class="t"><thead><tr>${hasRank ? '<th>Rank</th>' : ''}<th>Team</th>${hasScores ? '<th class="num">Score</th>' : ''}</tr></thead><tbody>${rows.map(r => {
    const mineRow = (r.team_name || '') === (me.team && me.team.name)
    const rScore = decidingScore(r)
    return `<tr>${hasRank ? `<td>${r.rank != null ? esc(r.rank) : ''}</td>` : ''}<td>${esc(r.team_name)}${mineRow ? ' <span class="pill accent">Your team</span>' : ''}</td>${hasScores ? `<td class="num">${rScore != null ? esc(scoreText(rScore)) : ''}</td>` : ''}</tr>`
  }).join('')}</tbody></table></div>` : ''

  // "Your entry as the public sees it" (atlas participant-results decision,
  // 2026-11-02): the same snapshot card the public page would show for this
  // team, so a team never learns from a friend whether or how it is shown.
  // shown_publicly comes straight from the server (getMyResults,
  // hack_judging.go), applying the exact same winners-only/full-ranking
  // test the public page and /results already use -- this screen does not
  // re-derive it from winnersOnly/amWinner above, which only gate the
  // *standings table* a stranger would never see anyway.
  const entryCardHTML = mine.shown_publicly
    ? `<h2 class="section-title">Your entry as the public sees it</h2><div id="res-entry-card"></div>`
    : `<p class="note">${mine.rank != null ? 'Only the winners are shown publicly — your entry itself is not public.' : "Your entry isn't part of the published results yet."}</p>`

  body.innerHTML = `<div class="card"><h2>${esc(headline)}</h2>${scoreLine ? `<p class="sub">${esc(scoreLine)}</p>` : ''}${trackLine ? `<p>${trackLine}</p>` : ''}${comments.length ? `<h2>Judges' anonymous comments</h2>${comments.map(c => `<p>“${esc(c)}”</p>`).join('')}` : ''}<a class="btn" href="/e/${encodeURIComponent(ev.slug)}">See it on the event page</a></div>`
    + entryCardHTML + table

  if (mine.shown_publicly && mine.entry) {
    const slot = body.querySelector('#res-entry-card')
    if (slot) {
      const card = ui.entryCard({
        title: mine.entry.title,
        team_name: me.team && me.team.name,
        track: mine.entry.track,
        problem: mine.entry.problem,
        tagline: mine.entry.tagline,
        audience: mine.entry.audience,
        tools: mine.entry.tools,
        maturity: mine.entry.maturity
      }, { compact: true })
      slot.append(card)
      // kit.css's .entry-card .ec-links is a child-of-the-card selector,
      // so the link row nests inside the card element, not beside it.
      const links = document.createElement('div')
      links.className = 'ec-links'
      const siteHref = ui.safeHref(mine.entry.site_url)
      const videoHref = ui.safeHref(mine.entry.video_url)
      const codeHref = ui.safeHref(mine.entry.code_url)
      if (siteHref) links.append(linkEl(siteHref, 'Open website ↗', 'btn primary'))
      if (videoHref) links.append(linkEl(videoHref, 'Watch video' + (mine.entry.video_host ? ' · ' + mine.entry.video_host : '') + ' ↗', 'btn quiet'))
      if (codeHref) links.append(linkEl(codeHref, 'View code' + (mine.entry.code_host ? ' · ' + mine.entry.code_host : '') + ' ↗', 'btn quiet'))
      if (links.childNodes.length) card.append(links)
    }
  }
}

function linkEl (href, text, cls) {
  const a = document.createElement('a')
  a.className = cls
  a.href = href
  a.target = '_blank'
  a.rel = 'noopener noreferrer'
  a.textContent = text
  return a
}
