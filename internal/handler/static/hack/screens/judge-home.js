import { href } from '../nav.js'
import { errText } from './_judge-util.js'
import { startTour } from '../tour.js'

// Guided walkthrough: shown once, automatically, the first time a judge
// lands on Home (tour.js checks the stored preference itself). Guarded so
// a refresh of this same screen doesn't keep popping it back open within
// one page load.
let autoTourTried = false

export function title() { return 'Home' }

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
  main.innerHTML = `<div class="ph"><div class="t"><h1>Home</h1><p>${ui.esc(ev.title)}</p></div></div><div id="jh-body"><p class="muted">Loading…</p></div>`
  const body = main.querySelector('#jh-body')

  let queueRes, confRes
  try {
    ;[queueRes, confRes] = await Promise.all([
      api('GET', `/v1/hack/events/${encodeURIComponent(slug)}/judge/queue`),
      api('GET', `/v1/hack/events/${encodeURIComponent(slug)}/conflicts`)
    ])
  } catch (e) {
    if (e.message === 'signed out') return
    body.innerHTML = `<div class="note bad" role="alert">Cannot reach the server.</div>`
    return
  }
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

  function bar(label, max) {
    return `<div class="card" data-tour="progress"><p>${scored} of ${max} ${label}</p><div class="bar" role="progressbar" aria-label="${ui.esc(label)}" aria-valuenow="${scored}" aria-valuemin="0" aria-valuemax="${max}"><i style="max-width:${max ? Math.round((scored / max) * 100) : 0}%"></i></div></div>`
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
      top = `<div class="note warn"><p><span class="pill stage">Locked</span></p><p><b>Locked</b> — you scored everyone assigned to you. Results aren’t out yet.</p></div>` + bar('entries scored', total)
    } else {
      const left = total - scored
      top = `<div class="note warn"><p><span class="pill stage">Locked</span></p><p><b>Locked before you finished</b> — ${left} ${left === 1 ? 'entry' : 'entries'} you were assigned ${left === 1 ? 'is' : 'are'} now closed to scoring. The organiser closed scoring, this isn’t an error.</p></div>` + bar('entries scored', total)
    }
  } else if (scored >= total) {
    top = `<section class="next" data-tour="next"><div class="t"><p class="k">All done</p><h2>You’re done. Thank you.</h2><p>${total} of ${total} teams scored. The organiser locks scoring when judging ends. Until then you can change any score.</p></div><a class="btn" href="${ui.esc(href('judge', slug, 'queue'))}">Review my scores</a></section>` + bar('teams scored', total)
  } else {
    const next = queue.find(r => !r.done)
    const scoreHref = `/e/${encodeURIComponent(slug)}/judge?s=scoring&team=${encodeURIComponent(next.team_id)}`
    top = `<section class="next" data-tour="next"><div class="t"><p class="k">Your next step</p><h2>Scoring is open</h2><p>${scored} of ${total} entries scored. The organiser locks scoring when judging ends.</p></div><a class="btn primary lg" href="${ui.esc(scoreHref)}">Score next</a></section>` + bar('entries scored', total)
  }

  // replay-judging-walkthrough: the shell's Help menu links here with
  // &walkthrough=1, which forces the guided tour open even if it was
  // dismissed before. tour.js itself checks judge_walkthrough_done.
  const openWalk = ctx.params.get('walkthrough') === '1'
  body.innerHTML = top +
    `<section class="card"><h2>Read first</h2><p class="sub">Rubric &amp; brief</p><a class="btn" href="${ui.esc(href('judge', slug, 'rubric'))}">Open</a></section>` +
    `<p><button class="btn quiet" type="button" id="jh-walk-replay">Replay walkthrough</button></p>` +
    `<section class="danger-zone"><h2>Leave this event</h2><div class="setting"><div class="t"><b>Planned, not available yet</b><span>Your scores are kept even after you leave.</span></div><button type="button" class="btn danger" disabled>Leave this event</button></div></section>`

  body.querySelector('#jh-walk-replay').addEventListener('click', e => { e.preventDefault(); startTour('judge', ctx, { force: true }) })
  if (openWalk) startTour('judge', ctx, { force: true })
  else if (!autoTourTried) { autoTourTried = true; startTour('judge', ctx) }
}
