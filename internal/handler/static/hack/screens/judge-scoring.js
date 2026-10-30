import { href } from '../nav.js'
import { errText, newTabLink, siteHref } from './_judge-util.js'
import { startTour } from '../tour.js'

// A generic fallback: render() below sets the exact "Score: <entry>" title
// once the queue has loaded, since the team's name isn't known this early.
export function title() { return 'Score entry' }

// Guided walkthrough: shown once, automatically, the first time a judge
// reaches scoring while it's actually open (editable) -- not on a locked,
// read-only visit. Guarded the same way judge-home.js guards its own tour,
// so re-rendering this same screen (switching teams with Save & next)
// doesn't keep popping it back open within one page load.
let autoTourTried = false

// judge-scoring: full-focus scoring of one entry (docs/ux-atlas/prototype/
// judge-scoring*.html). No nav_id — only reached from judge-queue /
// judge-home with ?team=. Every point chip PUTs and saves immediately
// (score-criterion); there is no Save button for scores, ever, only
// Save & next, which also flushes the comment (save-and-next,
// comment-on-entry). The team's live/pinned site cannot be framed (the
// app's CSP is frame-src 'self'), so "open the entry's website" is the
// prominent button BUILD.md allows as the fallback, not an iframe.
export async function render(ctx) {
  const { main, api: client, ui, event: detail } = ctx
  const api = client.api
  const ev = detail.event
  const slug = ev.slug
  let teamID = ctx.params.get('team') || ''

  main.innerHTML = `<p class="muted" style="padding:22px 28px">Loading…</p>`

  let queueRes, rubricRes
  try {
    ;[queueRes, rubricRes] = await Promise.all([
      api('GET', `/v1/hack/events/${encodeURIComponent(slug)}/judge/queue`),
      api('GET', `/v1/hack/events/${encodeURIComponent(slug)}/rubric`)
    ])
  } catch (e) {
    if (client.isSignedOut(e)) return
    main.innerHTML = `<div class="page"><div class="note bad" role="alert">Cannot reach the server.</div></div>`
    return
  }
  if (!queueRes.ok) {
    main.innerHTML = `<div class="page"><div class="note bad" role="alert">${ui.esc(errText(queueRes))}</div><p><a href="${ui.esc(href('judge', slug, 'queue'))}">Back to My teams</a></p></div>`
    return
  }
  const queue = queueRes.data.queue || []

  // No &team= (or a stale one no longer in the queue): default to the first
  // unscored team, else the first scored one, else the spec's
  // nothing-assigned state (judge-home), with a link back to My teams.
  if (!teamID || !queue.some(r => r.team_id === teamID)) {
    const pick = queue.find(r => !r.done) || queue[0]
    if (!pick) {
      main.innerHTML = `<div class="page"><div class="note">Nothing assigned to you yet. Check back once the organiser assigns judges.</div><p><a href="${ui.esc(href('judge', slug, 'queue'))}">Back to My teams</a></p></div>`
      return
    }
    teamID = pick.team_id
    const url = new URL(location.href)
    url.searchParams.set('team', teamID)
    history.replaceState({}, '', url)
  }

  // The queue's own order is by global score coverage (least-scored team
  // first across all judges) and reshuffles the instant any judge -- this
  // one or another -- scores a team. That's fine for picking which team to
  // start on above, but using it to drive a linear "walk the queue" screen
  // means finishing team A can silently move both A and the just-arrived-at
  // team B, leaving Previous disabled on a team the judge just came from
  // and the position counter wrong (verified: scoring HealthHive moved it
  // from index 0 to index 1 on the very next fetch). Fix: keep this judge's
  // own walking order stable for the session -- stored per event, new teams
  // appended at the end, teams no longer in the queue dropped -- and derive
  // position/Prev/Next from that instead of the fresh server order.
  const orderKey = 'sh-judge-order-' + slug
  let order = []
  try { order = JSON.parse(sessionStorage.getItem(orderKey) || '[]') } catch (e) { order = [] }
  if (!Array.isArray(order)) order = []
  const presentIds = new Set(queue.map(r => r.team_id))
  order = order.filter(id => presentIds.has(id))
  queue.forEach(r => { if (!order.includes(r.team_id)) order.push(r.team_id) })
  try { sessionStorage.setItem(orderKey, JSON.stringify(order)) } catch (e) {}
  const orderedQueue = order.map(id => queue.find(r => r.team_id === id)).filter(Boolean)

  const index = orderedQueue.findIndex(r => r.team_id === teamID)
  const row = orderedQueue[index]
  const rubric = (rubricRes.ok && rubricRes.data.criteria) || []
  if (!rubric.length) {
    main.innerHTML = `<div class="page"><div class="note">The organiser has not set a rubric yet.</div><p><a href="${ui.esc(href('judge', slug, 'queue'))}">Back to My teams</a></p></div>`
    return
  }

  // A bookmarked/direct link can land here outside the window the queue
  // page itself already guards (judge-queue.js's "Not open yet" check):
  // before scoring opens, read-only eligibility must come from the same
  // signals, not just whether judging has been explicitly locked.
  const resultsOut = !!ev.results_published
  if (!ev.scoring_open && !ev.judging_locked_at && !resultsOut) {
    const when = ev.submission_deadline ? ui.dateTime(ev.submission_deadline, ev) : ''
    main.innerHTML = `<div class="page"><section class="locked"><h2>Not open yet</h2><p>Judging opens once entries close${when ? ', ' + ui.esc(when) : ''}.</p><p><a href="${ui.esc(href('judge', slug, 'queue'))}">Back to My teams</a></p></section></div>`
    return
  }
  const locked = !!ev.judging_locked_at || resultsOut || !ev.scoring_open

  let scoresRes
  try { scoresRes = await api('GET', `/v1/hack/events/${encodeURIComponent(slug)}/judge/scores/${encodeURIComponent(teamID)}`) }
  catch (e) {
    if (client.isSignedOut(e)) return
    main.innerHTML = `<div class="page"><div class="note bad" role="alert">Cannot reach the server.</div></div>`
    return
  }
  if (!scoresRes.ok) {
    main.innerHTML = `<div class="page"><div class="note bad" role="alert">${ui.esc(errText(scoresRes))}</div><p><a href="${ui.esc(href('judge', slug, 'queue'))}">Back to My teams</a></p></div>`
    return
  }

  // Mutable scoring state for this team, seeded from the server.
  const scores = {} // criterion_id -> points
  ;(scoresRes.data.scores || []).forEach(s => { scores[s.criterion_id] = s.points })
  let comment = (scoresRes.ok && scoresRes.data.comment) || ''
  let savedComment = comment
  let commentTimer = 0
  let commentSave = null

  const title = row.title || row.team.name || 'Entry'
  const tagline = String(row.tagline || '').trim()
  const desc = String(row.description || '').trim()
  const video = newTabLink(row.video_url, 'Watch video', 'btn')
  const code = newTabLink(row.code_url, 'View code', 'btn')
  const site = siteHref(row)
  const submitted = row.updated_at ? `<p>Submitted ${ui.esc(ui.dateTime(row.updated_at, ev))}</p>` : ''

  // No empty "Team site" heading over nothing (SIM-346 cosmetic #7): only
  // show the heading when there's a site to show, or when there's neither a
  // site nor a screenshot (so the judge still gets told nothing was
  // published, instead of the section just vanishing).
  const siteSection = site
    ? `<p class="section-title">Team site</p><div class="site-frame"><div class="stack"><p><b>${ui.esc(row.team.name || title)}</b></p><a class="btn" data-tour="site" href="${ui.esc(site)}" target="_blank" rel="noopener noreferrer">Open ${ui.esc(row.team.name || 'site')} →</a></div></div>`
    : (row.has_screenshot ? '' : `<p class="section-title">Team site</p><div class="site-frame"><p class="muted">Nothing published yet.</p></div>`)

  // The screenshot is often the only evidence for a team with no published
  // site (or a stale one) -- show it alongside the website, not instead of
  // telling the judge nothing was published.
  const shotSection = row.has_screenshot
    ? `<p class="section-title">Screenshot</p><div class="site-frame"><img id="jsc-shot" alt="Screenshot of ${ui.esc(row.team.name || title)}" style="max-width:100%" hidden></div>`
    : ''

  const left = `<p class="crumbs"><a href="${ui.esc(href('judge', slug, 'queue'))}">My teams</a> / ${ui.esc(title)}</p>` +
    (locked ? `<div class="note warn"><b>Scoring is locked.</b> Your scores are read-only.</div>` : '') +
    `<header class="ph"><div class="t"><h1>${ui.esc(title)}</h1>${submitted}</div></header>` +
    `<button type="button" class="btn btn-block" id="jsc-goscore">Go to scoring ↓</button>` +
    (tagline ? `<p><b>${ui.esc(tagline)}</b></p>` : '') +
    (desc ? `<p>${ui.esc(desc)}</p>` : '') +
    (!title && !tagline && !desc ? '<p class="muted">No entry yet</p>' : '') +
    `<div class="row-flex">${video}${code}</div>` +
    siteSection +
    shotSection

  function critRow(crit, isFirst) {
    const current = scores[crit.id]
    if (locked) {
      return `<div class="crit"><b>${ui.esc(crit.name)}</b>${crit.description ? `<p class="small muted">${ui.esc(crit.description)}</p>` : ''}<p>Your score: <b>${current == null ? '—' : current} / ${crit.max_points}</b></p></div>`
    }
    let chips = ''
    for (let p = 0; p <= crit.max_points; p++) {
      chips += `<span role="radio" tabindex="0" aria-checked="${current === p ? 'true' : 'false'}" class="${current === p ? 'on' : ''}" data-crit="${ui.esc(crit.id)}" data-points="${p}">${p}</span>`
    }
    // Walkthrough: only the first criterion needs a target -- "tap a score"
    // applies to all of them the same way.
    return `<div class="crit" data-crit-row="${ui.esc(crit.id)}"><b>${ui.esc(crit.name)}</b>${crit.description ? `<p class="small muted">${ui.esc(crit.description)}</p>` : ''}<div class="scale" role="radiogroup" aria-label="${ui.esc(crit.name)}"${isFirst ? ' data-tour="score"' : ''}>${chips}</div></div>`
  }

  document.title = `Score: ${title} · Simple Hack`

  const hasScore = Object.keys(scores).length > 0
  const last = index >= orderedQueue.length - 1
  const nextLabel = last ? 'Save & finish' : 'Save & next'
  const posText = `${index + 1} of ${orderedQueue.length}${row.done ? ', done' : ''}`

  const right = `<h2 id="score">Scores <span class="small muted">${ui.esc(posText)}</span></h2>` +
    (locked ? '' : `<p class="small muted">Tap a score. It saves straight away.</p>`) +
    `<p class="small"><a class="muted" href="${ui.esc(href('judge', slug, 'rubric'))}">What do the scores mean?</a></p>` +
    `<div id="jsc-criteria">${rubric.map((crit, i) => critRow(crit, i === 0)).join('')}</div>` +
    (locked
      ? `<div class="field"><label>Comment for the team</label><p>${comment ? ui.esc(comment) : '<span class="muted">No comment.</span>'}</p><p class="help">Not shown with your name.</p></div>`
      : `<div class="field"><label for="jsc-comment">Comment for the team</label><textarea class="input" id="jsc-comment" data-tour="comment" maxlength="1000" rows="3" placeholder="Share useful feedback"${!hasScore ? ' disabled' : ''}>${ui.esc(comment)}</textarea><p class="help" id="jsc-comment-help">${hasScore ? 'Optional. The team sees it without your name.' : 'Add a score before writing a comment.'}</p><p class="small muted" id="jsc-saved" aria-live="polite"></p></div>`) +
    (locked ? '' : `<p class="small"><button type="button" class="btn quiet sm" id="jsc-conflict" data-tour="conflict">I know someone on this team</button></p>`) +
    `<div class="sticky-foot"><button type="button" class="btn" id="jsc-prev" data-tour="prev"${index <= 0 ? ' disabled' : ''}>Previous</button>${locked ? '' : `<button type="button" class="btn primary" id="jsc-next" data-tour="save-next">${nextLabel}</button>`}</div>`

  // S7: the shared shell already mounts into a <main class="page">, so this
  // full-focus layout must be a div, not another <main> -- a nested <main>
  // breaks landmark navigation for screen readers.
  main.innerHTML = `<div class="page wide" style="padding:0;height:100%"><div class="focus"><section class="left">${left}</section><section class="right">${right}</section></div></div>`

  if (row.has_screenshot) {
    ui.showImage(ctx, main.querySelector('#jsc-shot'), `/v1/hack/events/${encodeURIComponent(slug)}/teams/${encodeURIComponent(row.team.slug)}/screenshot`)
  }

  const goscore = main.querySelector('#jsc-goscore')
  if (goscore) goscore.addEventListener('click', () => { main.querySelector('#score').scrollIntoView({ behavior: 'smooth', block: 'start' }) })

  function goTeam(id) {
    ctx.go(`/e/${encodeURIComponent(slug)}/judge?s=scoring&team=${encodeURIComponent(id)}`)
  }

  async function flushComment() {
    clearTimeout(commentTimer)
    if (commentSave) await commentSave
    const box = main.querySelector('#jsc-comment')
    if (!box) return true
    const val = box.value
    if (val === savedComment) return true
    const pending = (async () => {
      let r
      try { r = await api('PUT', `/v1/hack/events/${encodeURIComponent(slug)}/judge/scores/${encodeURIComponent(teamID)}`, { scores: [], comment: val }) }
      catch (e) { if (!client.isSignedOut(e)) ui.toast('Cannot reach the server.', box, true); return false }
      if (!r.ok) { ui.toast(errText(r), box, true); return false }
      comment = val
      savedComment = val
      return true
    })()
    commentSave = pending
    try { return await pending } finally { if (commentSave === pending) commentSave = null }
  }

  // Per-criterion write queue: a rapid "choose 2 then 5" fires two PUTs that
  // could otherwise land on the server out of order (the slower request for
  // 2 completing after the faster one for 5), leaving the saved score
  // wrong while the chip UI still shows 5. Chaining each criterion's writes
  // one after another makes the server apply them in click order, so the
  // last click always wins. Save & next / Previous await every chain below
  // before leaving the page, so a score never gets silently dropped.
  const scoreChain = {} // criterion_id -> promise of the latest enqueued write
  function enqueueScoreWrite(critID, run) {
    const prev = scoreChain[critID] || Promise.resolve()
    const next = prev.then(run, run)
    scoreChain[critID] = next
    return next
  }
  async function flushScores() { await Promise.allSettled(Object.values(scoreChain)) }

  if (!locked) {
    const commentBox = main.querySelector('#jsc-comment')
    commentBox.addEventListener('input', () => {
      clearTimeout(commentTimer)
      if (Object.keys(scores).length) commentTimer = setTimeout(flushComment, 900)
    })

    main.querySelector('#jsc-criteria').addEventListener('click', e => {
      const chip = e.target.closest('[role=radio]')
      if (chip && chip.getAttribute('aria-disabled') !== 'true') choose(chip)
    })
    main.querySelector('#jsc-criteria').addEventListener('keydown', e => {
      if (e.key !== 'Enter' && e.key !== ' ') return
      const chip = e.target.closest('[role=radio]')
      if (!chip || chip.getAttribute('aria-disabled') === 'true') return
      e.preventDefault()
      choose(chip)
    })

    async function choose(chip) {
      const critID = chip.getAttribute('data-crit')
      const points = Number(chip.getAttribute('data-points'))
      const rowEl = chip.closest('[data-crit-row]')
      const prev = scores[critID]
      scores[critID] = points
      rowEl.querySelectorAll('[role=radio]').forEach(s => {
        const on = s === chip
        s.classList.toggle('on', on)
        s.setAttribute('aria-checked', on ? 'true' : 'false')
      })
      chip.setAttribute('aria-disabled', 'true')
      const outcome = await enqueueScoreWrite(critID, async () => {
        try { return { r: await api('PUT', `/v1/hack/events/${encodeURIComponent(slug)}/judge/scores/${encodeURIComponent(teamID)}`, { scores: [{ criterion_id: critID, points }] }) } }
        catch (e) { return { err: e } }
      })
      chip.removeAttribute('aria-disabled')
      if (outcome.err) {
        if (!client.isSignedOut(outcome.err)) ui.toast('Cannot reach the server. Try again.', rowEl, true)
        return
      }
      const r = outcome.r
      if (!r.ok) {
        // Only roll the chips back if nothing newer has been chosen for
        // this criterion since this click -- otherwise an older request's
        // failure would stomp a selection the user already moved past.
        if (scores[critID] === points) {
          scores[critID] = prev
          rowEl.querySelectorAll('[role=radio]').forEach(s => {
            const p = Number(s.getAttribute('data-points'))
            const on = p === prev
            s.classList.toggle('on', on)
            s.setAttribute('aria-checked', on ? 'true' : 'false')
          })
        }
        ui.toast(errText(r), rowEl, true)
        return
      }
      row.done = !!(r.data && r.data.complete)
      const box = main.querySelector('#jsc-comment')
      if (box.disabled) {
        box.disabled = false
        main.querySelector('#jsc-comment-help').textContent = 'Optional. Not shown with your name. Saves automatically.'
      }
      const saved = main.querySelector('#jsc-saved')
      if (saved) saved.textContent = 'Score saved'
      if (box.value !== savedComment) flushComment()
    }

    const conflictBtn = main.querySelector('#jsc-conflict')
    conflictBtn.addEventListener('click', async () => {
      const ok = await ui.confirm({
        title: `You know someone on ${title}?`,
        body: 'This team leaves your list and your score for it stops counting. You can undo this from My teams.',
        confirmLabel: 'Yes, I know this team',
        cancelLabel: 'Cancel',
        danger: true
      })
      if (!ok) return
      conflictBtn.disabled = true
      let r
      try { r = await api('POST', `/v1/hack/events/${encodeURIComponent(slug)}/conflicts`, { team_id: teamID }) }
      catch (e) { if (!client.isSignedOut(e)) ui.toast('Cannot reach the server.', conflictBtn, true); conflictBtn.disabled = false; return }
      if (!r.ok) { ui.toast(errText(r), conflictBtn, true); conflictBtn.disabled = false; return }
      const remaining = orderedQueue.filter(q => q.team_id !== teamID)
      if (!remaining.length) { ctx.go(`/e/${encodeURIComponent(slug)}/judge?s=queue`); return }
      const nextRow = remaining[Math.min(index, remaining.length - 1)]
      goTeam(nextRow.team_id)
    })

    main.querySelector('#jsc-next').addEventListener('click', async () => {
      const ok = await flushComment()
      if (!ok) return
      await flushScores()
      if (last) { ctx.go(`/e/${encodeURIComponent(slug)}/judge?s=queue`); return }
      goTeam(orderedQueue[index + 1].team_id)
    })
  }

  main.querySelector('#jsc-prev').addEventListener('click', async () => {
    if (index <= 0) return
    if (!locked) {
      const ok = await flushComment()
      if (!ok) return
      await flushScores()
    }
    goTeam(orderedQueue[index - 1].team_id)
  })

  // replay-scoring-walkthrough: the shell's Help menu deep-links here with
  // &walkthrough=1 while this is the current screen; tour.js itself checks
  // the stored seen-state (localStorage -- see tours.js judgeScoring).
  // Auto-start only on a genuinely open, editable visit: a locked/read-only
  // one has no score buttons, comment box or Save & next to point at.
  const openWalk = ctx.params.get('walkthrough') === '1'
  if (openWalk) startTour('judgeScoring', ctx, { force: true })
  else if (!locked && !autoTourTried) { autoTourTried = true; startTour('judgeScoring', ctx) }
}
