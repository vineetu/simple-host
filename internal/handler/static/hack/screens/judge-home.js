import { href } from '../nav.js'
import { errText } from './_judge-util.js'
import { startTour } from '../tour.js'
import { previewLeave, openLeave } from '../leave.js'

// Leave this event: danger zone last on the page, text from the preview
// (copy-deck.md, judge section). Returns '' if the preview couldn't be
// loaded -- never show a Leave button whose state we can't vouch for.
function judgeDangerZone(ui, preview, ev) {
  if (!preview) return ''
  const b = 'Leave ' + ev.title
  let span, disabled = false
  if (preview.blocked) {
    disabled = true
    span = preview.blocked.code === 'event_closed' ? 'This event has ended, so nobody can leave it. It stays on your Home as a record.'
      : preview.blocked.code === 'event_taken_down' ? 'This event has been taken down.'
      : (preview.blocked.message || "You can't leave this event right now.")
  } else {
    const n = preview.scored_teams || 0
    span = n > 0
      ? `You'll lose access to judging. Your scores for ${n} team${n === 1 ? '' : 's'} stay and still count in the results.`
      : "You'll lose access to judging."
  }
  return `<section class="danger-zone"><h2>Leave this event</h2><div class="setting"><div class="t"><b>${ui.esc(b)}</b><span>${ui.esc(span)}</span></div><button class="btn danger" type="button" id="leave-btn"${disabled ? ' disabled' : ''}>Leave event</button></div></section>`
}

// Guided walkthrough: shown once, automatically, the first time a judge
// lands on Overview (renamed from Home 2026-10-30; tour.js checks the stored
// preference itself). Guarded so a refresh of this same screen doesn't keep
// popping it back open within one page load.
let autoTourTried = false

export function title() { return 'Overview' }

// judge-home: one word on whether scoring is open or locked, and the one
// next action. See docs/ux-atlas/spec.json#judge-home for the states this
// renders; the atlas has no prototype mock for "locked, fully done",
// "locked, after results" or "nothing assigned"/"everything conflicted",
// so those follow the spec's "shows" text directly.
export async function render(ctx) {
  const { main, api: client, ui, event: detail } = ctx
  const api = client.api
  const ev = detail.event
  const slug = ev.slug
  main.innerHTML = `<div class="ph"><div class="t"><h1>Overview</h1></div></div><div id="jh-body"><p class="muted">Loading…</p></div>`
  const body = main.querySelector('#jh-body')

  let queueRes, confRes, preview
  try {
    ;[queueRes, confRes, preview] = await Promise.all([
      api('GET', `/v1/hack/events/${encodeURIComponent(slug)}/judge/queue`),
      api('GET', `/v1/hack/events/${encodeURIComponent(slug)}/conflicts`),
      previewLeave(client, slug).then(r => (r.ok ? r.data : null)).catch(() => null)
    ])
  } catch (e) {
    if (client.isSignedOut(e)) return
    body.innerHTML = `<div class="note bad" role="alert">Cannot reach the server.</div>`
    return
  }
  if (!ctx.alive()) return
  if (!queueRes.ok || !confRes.ok) {
    body.innerHTML = `<div class="note bad" role="alert">${ui.esc(errText(queueRes.ok ? confRes : queueRes))}</div>`
    return
  }
  const queue = queueRes.data.queue || []
  const conflicts = confRes.data.conflicts || []

  const total = queue.length
  const scored = queue.filter(r => r.done).length
  const resultsOut = !!ev.results_published
  const locked = !!ev.judging_locked_at
  // S7/#11: one noun ("team") across the judge screens, pluralised on the
  // total being scored against (so "1 of 1 team scored", not "entries").
  const teamWord = n => n === 1 ? 'team' : 'teams'

  // bar(): the progress bar on its own, for the two states (locked) that
  // don't already say the count in a .next hero -- see the "next"/"done"
  // branches below, which fold the same count and bar into the hero instead
  // of showing it twice (A-cross-cutting "progress shows twice").
  function bar(label, max) {
    return `<div class="card"><p>${scored} of ${max} ${label}</p><div class="bar" data-tour="progress" role="progressbar" aria-label="${ui.esc(label)}" aria-valuenow="${scored}" aria-valuemin="0" aria-valuemax="${max}"><i style="max-width:${max ? Math.round((scored / max) * 100) : 0}%"></i></div></div>`
  }
  function heroBar(max) {
    return `<div class="bar" data-tour="progress" role="progressbar" aria-label="${teamWord(max)} scored" aria-valuenow="${scored}" aria-valuemin="0" aria-valuemax="${max}"><i style="max-width:${max ? Math.round((scored / max) * 100) : 0}%"></i></div>`
  }

  let top = ''
  if (resultsOut) {
    top = `<div class="note ok"><p><b>Results are out.</b></p><p><a href="${ui.esc('/e/' + encodeURIComponent(slug))}">See results on the event page</a></p></div>`
  } else if (!ev.scoring_open && !locked) {
    const when = ev.submission_deadline ? ui.dateTime(ev.submission_deadline, ev) : ''
    top = `<section class="locked"><h2>Not open yet</h2><p>Judging opens once entries close${when ? ', ' + ui.esc(when) : ''}.</p><p class="muted">Scoring will be available here when judging opens.</p></section>`
  } else if (total === 0 && conflicts.length === 0) {
    top = `<div class="note"><p>Nothing assigned to you yet. Check back once the organiser assigns judges.</p></div>`
  } else if (total === 0 && conflicts.length > 0) {
    top = `<div class="note"><p>You’ve declared a conflict with everyone assigned to you. There’s nothing left for you to score, by design.</p></div>`
  } else if (locked) {
    if (scored >= total) {
      top = `<div class="note warn"><b>Scoring is locked.</b> You scored every team. Results are not out yet.</div>` + bar(teamWord(total) + ' scored', total)
    } else {
      const left = total - scored
      top = `<div class="note warn"><b>Scoring was locked before you finished.</b> ${left} ${teamWord(left)} you were assigned ${left === 1 ? 'is' : 'are'} now closed to scoring. The organiser closed scoring, this isn’t an error.</div>` + bar(teamWord(total) + ' scored', total)
    }
  } else if (scored >= total) {
    top = `<section class="next" data-tour="next"><div class="t"><p class="k">All done</p><h2>You are done. Thank you.</h2><p>${total} of ${total} ${teamWord(total)} scored.</p>${heroBar(total)}<p class="small muted">The organiser locks scoring when judging ends. Until then you can change any score.</p></div><a class="btn" href="${ui.esc(href('judge', slug, 'queue'))}">Review my scores</a></section>`
  } else {
    const next = queue.find(r => !r.done)
    const scoreHref = `/e/${encodeURIComponent(slug)}/judge?s=scoring&team=${encodeURIComponent(next.team_id)}`
    top = `<section class="next" data-tour="next"><div class="t"><p class="k">Your next step</p><h2>Scoring is open</h2><p>${scored} of ${total} ${teamWord(total)} scored.</p>${heroBar(total)}<p class="small muted">The organiser locks scoring when judging ends.</p></div><a class="btn primary lg" href="${ui.esc(scoreHref)}">Score next</a></section>`
  }

  // replay-judging-walkthrough: the shell's Help menu links here with
  // &walkthrough=1, which forces the guided tour open even if it was
  // dismissed before. tour.js itself checks judge_walkthrough_done.
  const openWalk = ctx.params.get('walkthrough') === '1'
  body.innerHTML = top +
    `<h2 class="section-title">Read first</h2><div class="list"><a class="r" href="${ui.esc(href('judge', slug, 'rubric'))}"><span class="main"><b>Rubric &amp; brief</b><span>Read it once before you start</span></span><span class="meta">Open</span></a></div>`
    + judgeDangerZone(ui, preview, ev)

  const leaveBtn = body.querySelector('#leave-btn')
  if (leaveBtn && !leaveBtn.disabled) leaveBtn.addEventListener('click', () => openLeave({ api: client, ui, slug, title: ev.title, from: 'judge-home' }))

  if (openWalk) startTour('judge', ctx, { force: true })
  else if (!autoTourTried) { autoTourTried = true; startTour('judge', ctx) }
}
