import { href } from '../nav.js'
import { errText, siteHref } from './_judge-util.js'
import { startTour } from '../tour.js'
import { replaceUrl } from '../platform.js'

// A generic fallback: render() below sets the exact "Score: <entry>" title
// once the queue has loaded, since the team's name isn't known this early.
export function title() { return 'Score entry' }

// Guided walkthrough: shown once, automatically, the first time a judge
// reaches scoring while it's actually open (editable) -- not on a locked,
// read-only visit. Guarded the same way judge-home.js guards its own tour,
// so re-rendering this same screen (switching teams with Save & next)
// doesn't keep popping it back open within one page load.
let autoTourTried = false
// The N/P key listener of the screen on show (see render()).
let keyAbort = null

// judge-scoring: full-focus scoring of one entry (docs/ux-atlas/prototype/
// judge-scoring*.html). No nav_id — only reached from judge-queue /
// judge-home with ?team=. Every point chip PUTs and saves immediately
// (score-criterion); there is no Save button for scores, ever, only
// Save & next, which also flushes the comment (save-and-next,
// comment-on-entry).
//
// Entries slice 2 ("the judge's one screen", build-plan.md section 11): the
// summary card first, then the video and the team's site in the event's
// judge_first order, on the same screen as the rubric.
//  - The video plays here only from the server-built video.embed_url (one of
//    four fixed player origins, rebuilt from a parsed id -- videoEmbed in
//    hack_video.go), and only after the judge presses play. Any other link
//    is a plain new-tab link naming its host.
//  - The site is framed only from the server-issued site.preview_url, and
//    only when site.frameable; otherwise the screenshot and Open website.
//  - ctx.noFrames (the MCP App judge view, whose own CSP frames nothing)
//    turns both into links.
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
    replaceUrl(url)
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
  const desc = String(row.description || '').trim()
  const teamName = row.team.name || title
  const noFrames = !!ctx.noFrames
  const judgeFirst = (queueRes.data && queueRes.data.judge_first) === 'site' ? 'site' : 'video'

  // The summary card is the page header (the 20-second read): its title
  // becomes the page's h1.
  const card = ui.entryCard({
    title: row.title || row.team.name, team_name: row.team.name, team_members: '',
    track: row.track, problem: row.problem, tagline: row.tagline, audience: row.audience,
    tools: row.tools, maturity: row.maturity
  }, { incomplete: !!row.incomplete })
  card.setAttribute('data-tour', 'card')
  const cardH = card.querySelector('h2')
  if (cardH) { const h1 = document.createElement('h1'); h1.textContent = cardH.textContent; cardH.replaceWith(h1) }
  const cardHost = document.createElement('div')
  cardHost.append(card)
  const cardHTML = cardHost.innerHTML
  const incompleteNote = row.incomplete
    ? '<p class="small muted">Not complete at the deadline. Judge it as it is.</p>'
    : ''

  const video = row.video && ui.safeHref(row.video.url) ? row.video : null
  const site = row.site || null
  const siteOpen = siteHref(row)
  const codeHref = ui.safeHref(row.code_url)
  // Only one of the four fixed player origins, whatever the server sent
  // (it never sends anything else; this is the client's own check).
  const embedURL = video && !noFrames && typeof video.embed_url === 'string' &&
    /^https:\/\/(www\.youtube-nocookie\.com\/embed\/|player\.vimeo\.com\/video\/|www\.loom\.com\/embed\/|drive\.google\.com\/file\/d\/)/.test(video.embed_url)
    ? video.embed_url : ''
  // Only the server-issued preview, and only on the team site's own host
  // (the CSP's sites wildcard is the backstop).
  const framed = site && site.frameable && !noFrames && ui.safeHref(site.preview_url) &&
    ui.hostLabel(site.preview_url) === String(site.host || '').toLowerCase() && /^https:/.test(site.preview_url)
    ? site.preview_url : ''

  function videoBlock() {
    if (!video) return ''
    const host = video.host || ui.hostLabel(video.url)
    const openLink = `<a class="btn quiet sm" href="${ui.esc(ui.safeHref(video.url))}" target="_blank" rel="noopener noreferrer">Open on ${ui.esc(host)} ↗</a>`
    if (embedURL) {
      return `<section class="media" id="jsc-video" data-tour="video"><div class="mbar"><b>Demo video</b><span class="host">${ui.esc(host)}</span><span class="grow"></span>${openLink}</div>` +
        `<button type="button" class="video-box" id="jsc-play" aria-label="Play the ${ui.esc(teamName)} demo video here"><span class="play"></span><span class="cap">Plays here · nothing loads until you press play</span></button></section>`
    }
    return `<section class="media" id="jsc-video" data-tour="video"><div class="mbar"><b>Demo video</b><span class="host">${ui.esc(host)}</span></div>` +
      `<a class="video-link" href="${ui.esc(ui.safeHref(video.url))}" target="_blank" rel="noopener noreferrer"><span class="play"></span><span><b>Watch the demo video</b><br><span class="small muted">Opens ${ui.esc(host)} in a new tab</span></span></a></section>`
  }

  const WHY = {
    passcode: 'This site asks visitors for a passcode, so it can’t show here.',
    viewers: 'Only people the team named can open this site, so it can’t show here.',
    offline: 'The team took this site offline.',
    taken_down: 'The organiser took this site down.',
    not_ready: 'This site can’t show here.'
  }
  function siteBlock() {
    const shot = row.has_screenshot
      ? `<div class="site-frame"><img id="jsc-shot" alt="Screenshot of ${ui.esc(teamName)}" hidden></div>`
      : ''
    if (!site && !siteOpen) {
      // No site at all: the screenshot (when there is one) is the only
      // evidence, shown on its own; nothing else is shown empty.
      return shot ? `<section class="media" id="jsc-site"><div class="mbar"><b>Screenshot</b></div><div class="media-body">${shot}</div></section>` : ''
    }
    const label = row.pinned_version != null ? 'Website · as submitted' : 'Website'
    const host = (site && site.host) || ui.hostLabel(siteOpen)
    const openHref = siteOpen
    const bar = `<div class="mbar"><b>${ui.esc(label)}</b><span class="host">${ui.esc(host)}</span><span class="grow"></span>${openHref ? `<a class="btn quiet sm" href="${ui.esc(openHref)}" target="_blank" rel="noopener noreferrer">Open ↗</a>` : ''}</div>`
    if (framed) {
      return `<section class="media" id="jsc-site" data-tour="site">${bar}<div class="site-preview"><iframe id="jsc-site-frame" title="${ui.esc(teamName)} website" loading="lazy" referrerpolicy="no-referrer" sandbox="allow-scripts allow-forms allow-same-origin allow-popups allow-popups-to-escape-sandbox"></iframe></div></section>`
    }
    const why = noFrames ? '' : (WHY[site && site.not_frameable_why] || '')
    const body = openHref
      ? `${shot}${why ? `<p class="small muted">${ui.esc(why)}</p>` : ''}<p><a class="btn primary" data-tour="site" href="${ui.esc(openHref)}" target="_blank" rel="noopener noreferrer">Open website ↗</a></p>`
      : `${shot}<p class="muted">${site && site.not_frameable_why === 'taken_down' ? ui.esc(WHY.taken_down) : 'Nothing published.'}</p>`
    return `<section class="media" id="jsc-site">${bar}<div class="media-body">${body}</div></section>`
  }

  function codeBlock() {
    if (!codeHref) return ''
    const host = ui.hostLabel(codeHref)
    return `<section class="media" id="jsc-code"><div class="mbar"><b>Code</b><span class="host">${ui.esc(host)}</span><span class="grow"></span><a class="btn quiet sm" href="${ui.esc(codeHref)}" target="_blank" rel="noopener noreferrer">Open ↗</a></div></section>`
  }

  // Card, then the event's first medium, then the other; code follows the
  // website in a website-first (coding) event, else comes last.
  const media = judgeFirst === 'site'
    ? siteBlock() + codeBlock() + videoBlock()
    : videoBlock() + siteBlock() + codeBlock()

  const left = `<div class="jhead"><a class="muted small" href="${ui.esc(href('judge', slug, 'queue'))}">← My teams</a><span class="grow"></span><span class="small muted">Team ${index + 1} of ${orderedQueue.length}</span></div>` +
    cardHTML + incompleteNote +
    `<p class="small jump"><a href="#score" id="jsc-goscore">Go to scoring ↓</a></p>` +
    media +
    (desc ? `<details class="card"><summary>More detail from the team</summary><p>${ui.esc(desc)}</p></details>` : '')

  // Each criterion's weight as a share of the whole rubric, the way the
  // rubric editor and results compute it (weights are whole numbers that
  // usually add up to 100; this is right either way).
  const weightSum = rubric.reduce((sum, c) => sum + (Number(c.weight) || 0), 0)
  function weightPct(crit) {
    const w = Number(crit.weight)
    if (!weightSum || crit.weight == null || Number.isNaN(w)) return ''
    return Math.round(w / weightSum * 100) + '%'
  }

  function critHead(crit) {
    const pct = weightPct(crit)
    return `<div class="t-row"><b>${ui.esc(crit.name)}</b>${pct ? `<span class="w">${ui.esc(pct)}</span>` : ''}</div>` +
      (crit.description ? `<p class="small muted">${ui.esc(crit.description)}</p>` : '')
  }

  function critRow(crit, isFirst) {
    const current = scores[crit.id]
    const maxPoints = Number(crit.max_points) || 0
    if (locked) {
      return `<div class="crit">${critHead(crit)}<p>Your score: <b>${current == null ? '—' : Number(current) || 0} / ${maxPoints}</b></p></div>`
    }
    let chips = ''
    for (let p = 0; p <= maxPoints; p++) {
      chips += `<span role="radio" tabindex="0" aria-checked="${current === p ? 'true' : 'false'}" class="${current === p ? 'on' : ''}" data-crit="${ui.esc(crit.id)}" data-points="${p}">${p}</span>`
    }
    // Walkthrough: only the first criterion needs a target -- "tap a score"
    // applies to all of them the same way.
    return `<div class="crit" data-crit-row="${ui.esc(crit.id)}">${critHead(crit)}<div class="scale" role="radiogroup" aria-label="${ui.esc(crit.name)}"${isFirst ? ' data-tour="score"' : ''}>${chips}</div></div>`
  }

  document.title = `Score: ${title} · Simple Hack`

  const hasScore = Object.keys(scores).length > 0
  const last = index >= orderedQueue.length - 1
  const nextLabel = last ? 'Save & finish' : 'Save & next'
  const doneCount = () => orderedQueue.filter(q => q.done).length
  const progressHTML = () => {
    const n = doneCount(), m = orderedQueue.length
    return `<div class="row-flex"><span class="small muted">${n} of ${m} scored</span><span class="grow"></span><a class="small muted" href="${ui.esc(href('judge', slug, 'rubric'))}">Rubric &amp; brief</a></div>` +
      `<div class="bar" style="margin:6px 0 10px" role="img" aria-label="${n} of ${m} scored"><i style="width:${m ? Math.round(n / m * 100) : 0}%"></i></div>`
  }

  const right = `<div id="jsc-progress">${progressHTML()}</div>` +
    `<h2 id="score">Score</h2>` +
    (locked ? `<div class="note warn"><b>Scoring is locked.</b> Your scores are read-only.</div>` : '') +
    `<div id="jsc-criteria">${rubric.map((crit, i) => critRow(crit, i === 0)).join('')}</div>` +
    (locked
      ? `<div class="field"><span class="lab">Comment for the team</span><p>${comment ? ui.esc(comment) : '<span class="muted">No comment.</span>'}</p><p class="help">Not shown with your name.</p></div>`
      : `<div class="field"><label for="jsc-comment">Comment for the team</label><textarea class="input" id="jsc-comment" data-tour="comment" maxlength="1000" rows="3" placeholder="Share useful feedback"${!hasScore ? ' disabled' : ''}>${ui.esc(comment)}</textarea><p class="help" id="jsc-comment-help">${hasScore ? 'Optional. Not shown with your name. Saves automatically.' : 'Optional. Not shown with your name. Add a score before writing a comment.'}</p><p class="small muted" id="jsc-saved" aria-live="polite">${hasScore ? '' : 'Tap a score. It saves straight away.'}</p></div>`) +
    (locked ? '' : `<div class="danger-zone"><h2>Know this team?</h2><div class="setting"><button type="button" class="btn danger sm" id="jsc-conflict" data-tour="conflict">I know someone on this team</button></div></div>`) +
    `<div class="note warn next-guard" id="jsc-guard" role="status" hidden></div>` +
    `<div class="sticky-foot"><button type="button" class="btn" id="jsc-prev" data-tour="prev"${index <= 0 ? ' disabled' : ''}>Previous</button>${locked ? '' : `<button type="button" class="btn primary" id="jsc-next" data-tour="save-next">${nextLabel}</button>`}<span class="small muted kbd-hint">${locked ? '' : '<span class="kbd">N</span> next · '}<span class="kbd">P</span> previous</span></div>`

  // S7: the shared shell already mounts into a <main class="page">, so this
  // full-focus layout must be a div, not another <main> -- a nested <main>
  // breaks landmark navigation for screen readers.
  main.innerHTML = `<div class="page wide" style="padding:0;height:100%"><div class="focus"><section class="left">${left}</section><section class="right">${right}</section></div></div>`

  const shotImg = main.querySelector('#jsc-shot')
  if (shotImg) {
    ui.showImage(ctx, shotImg, `/v1/hack/events/${encodeURIComponent(slug)}/teams/${encodeURIComponent(row.team.slug)}/screenshot`)
  }

  // The framed site: src set from the server-issued preview_url only.
  const siteFrame = main.querySelector('#jsc-site-frame')
  if (siteFrame && framed) siteFrame.src = framed

  // Click-to-load video: nothing from the player's origin loads until the
  // judge presses play, and it never autoplays (no autoplay in `allow`).
  const play = main.querySelector('#jsc-play')
  if (play && embedURL) {
    play.addEventListener('click', () => {
      const frame = document.createElement('iframe')
      frame.className = 'video-frame'
      frame.title = `${teamName} demo video`
      frame.setAttribute('sandbox', 'allow-scripts allow-same-origin allow-presentation allow-popups')
      frame.setAttribute('allow', 'fullscreen; picture-in-picture')
      frame.setAttribute('allowfullscreen', '')
      frame.setAttribute('referrerpolicy', 'strict-origin-when-cross-origin')
      frame.src = embedURL
      play.replaceWith(frame)
    })
  }

  const goscore = main.querySelector('#jsc-goscore')
  if (goscore) goscore.addEventListener('click', e => { e.preventDefault(); main.querySelector('#score').scrollIntoView({ behavior: 'smooth', block: 'start' }) })

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

  let guardUntil = 0
  function hideGuard() {
    const guard = main.querySelector('#jsc-guard')
    if (guard) guard.hidden = true
    guardUntil = 0
  }

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
      const prog = main.querySelector('#jsc-progress')
      if (prog) prog.innerHTML = progressHTML()
      if (rubric.every(c => scores[c.id] != null)) hideGuard()
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
      // Cosmetic #1 (Grok re-test, round 4): this used to name the entry
      // (`title`, which falls back to the team name only when there's no
      // entry title) instead of the team -- the undo dialog in judge-queue.js
      // always names the team, so this one does too, from the same
      // row.team.name the rest of this screen uses. It also claimed a score
      // "stops counting" even on a team this judge never scored. Read the
      // live `scores` map when the dialog opens (not the render-time
      // `hasScore`), so a score saved since the screen loaded counts.
      const teamName = row.team.name || title
      const scoredNow = Object.keys(scores).some(k => scores[k] != null)
      const ok = await ui.confirm({
        title: `You know someone on ${teamName}?`,
        body: scoredNow
          ? 'This team leaves your list and your score for it stops counting. You can undo this from My teams.'
          : `You won't score this team.`,
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
      // guard-unscored-on-next: one line, not a dialog. A second press
      // within 10 seconds moves on.
      const unscored = rubric.filter(c => scores[c.id] == null)
      if (unscored.length && Date.now() > guardUntil) {
        const names = unscored.map(c => c.name)
        const list = names.length === 1 ? names[0] : names.slice(0, -1).join(', ') + ' and ' + names[names.length - 1]
        const guard = main.querySelector('#jsc-guard')
        guard.textContent = `${list} ${names.length === 1 ? 'has' : 'have'} no score yet. Press again to move on.`
        guard.hidden = false
        guardUntil = Date.now() + 10000
        return
      }
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

  // N = Save & next, P = Previous, outside text fields (atlas decision
  // "Keyboard on desktop?"); digits are deliberately not bound. One
  // listener at a time: a re-render (the next team) replaces the last one,
  // and it removes itself once this screen is gone.
  if (keyAbort) keyAbort.abort()
  keyAbort = new AbortController()
  const ownRoot = main.querySelector('.focus')
  const signal = keyAbort.signal
  document.addEventListener('keydown', e => {
    if (!ownRoot.isConnected) { keyAbort.abort(); return }
    if (e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey || e.repeat) return
    const t = e.target
    if (t && (t.closest('input, textarea, select, [contenteditable=""], [contenteditable="true"]'))) return
    if (document.querySelector('dialog[open], [role="dialog"], [role="alertdialog"]')) return
    const k = e.key.toLowerCase()
    const btn = k === 'n' ? main.querySelector('#jsc-next') : k === 'p' ? main.querySelector('#jsc-prev') : null
    if (!btn || btn.disabled) return
    e.preventDefault()
    btn.click()
  }, { signal })

  // replay-scoring-walkthrough: the shell's Help menu deep-links here with
  // &walkthrough=1 while this is the current screen; tour.js itself checks
  // the stored seen-state (localStorage -- see tours.js judgeScoring).
  // Auto-start only on a genuinely open, editable visit: a locked/read-only
  // one has no score buttons, comment box or Save & next to point at.
  const openWalk = ctx.params.get('walkthrough') === '1'
  if (openWalk) startTour('judgeScoring', ctx, { force: true })
  else if (!locked && !autoTourTried) { autoTourTried = true; startTour('judgeScoring', ctx) }
}
