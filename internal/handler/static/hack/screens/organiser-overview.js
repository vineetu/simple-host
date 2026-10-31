import { href } from '../nav.js'
import { adminLock, stagePicker } from './_orgA-util.js'
import { startTour } from '../tour.js'

const STAGE_ORDER = ['draft', 'open', 'building', 'closed', 'judging', 'results', 'archived']

// Guided walkthrough: shown once, automatically, the first time an
// organiser lands on Overview (tour.js checks the stored preference
// itself). Guarded so a refresh of this same screen (e.g. after Change
// stage) doesn't keep popping it back open within one page load.
let autoTourTried = false

export function title(ctx) { return 'Overview' }

// nextStep answers "what do I do now" for the whole event, from real state
// rather than a fixed per-stage string (spec.json organiser-overview.purpose).
function nextStep(ev) {
  const deadlinePassed = ev.submission_deadline && new Date(ev.submission_deadline) <= new Date()
  switch (ev.stage) {
    case 'draft':
      return { warn: false, text: 'Set up your event page, then open sign-up' }
    case 'open':
      return { warn: false, text: 'Approve people as they apply, then start building' }
    case 'building':
      if (deadlinePassed) return { warn: true, text: 'The entry deadline has passed. Check Entries and move to Judging when ready.' }
      return { warn: false, text: 'Keep teams building, then close entries when ready' }
    case 'closed':
      return { warn: false, text: 'Check the rubric and judges, then start judging' }
    case 'judging':
      return { warn: false, text: ev.judging_locked_at ? 'Judging is locked. Publish results when you are ready' : 'Lock judging when every judge has finished' }
    case 'results':
      return { warn: false, text: ev.results_published ? 'Results are published' : 'Judging is locked. Publish results when you are ready' }
    default:
      return { warn: false, text: 'This event has ended. The public page stays up as a record' }
  }
}

// heroAction answers "what's the one thing to click" for the hero's primary
// button, from the same state nextStep reads, plus the pending-applications
// count fetched below. null means no single action stands out for this
// state (the header's Change stage control still works).
function heroAction(ev, pendingCount, makeHref) {
  switch (ev.stage) {
    case 'draft': return { label: 'Set up event page', href: makeHref('organiser', ev.slug, 'event-page') }
    case 'open': return pendingCount ? { label: 'Review applications', href: makeHref('organiser', ev.slug, 'people') } : null
    // Applications (and join-link sign-ups needing approval) arrive during
    // Building too, not only Open -- the review action takes priority there
    // the same way it does for Open (owner, 2026-10-31).
    case 'building': return pendingCount ? { label: 'Review applications', href: makeHref('organiser', ev.slug, 'people') } : { label: 'See entries', href: makeHref('organiser', ev.slug, 'entries') }
    case 'closed': return { label: 'Set up judging', href: makeHref('organiser', ev.slug, 'judging') }
    case 'judging': return ev.judging_locked_at ? { label: 'Publish results', href: makeHref('organiser', ev.slug, 'results') } : { label: 'See judging progress', href: makeHref('organiser', ev.slug, 'judging') }
    case 'results': return ev.results_published ? { label: 'Open public page', href: ev.url, external: true } : { label: 'Publish results', href: makeHref('organiser', ev.slug, 'results') }
    default: return { label: 'Open public page', href: ev.url, external: true }
  }
}

export async function render(ctx) {
  const { main, api: client, ui, event: data, params } = ctx
  const api = client.api
  const ev = data.event
  const org = data.organiser || {}
  const counts = org.counts || {}
  const admin = !!data.admin_view
  const $ = id => main.querySelector('#' + id)
  const setLive = ui.live(main)

  const step = nextStep(ev)

  // C7 (owner decision 2026-10-30): the server's judges count includes
  // organisers who can judge (db.CountEligibleJudges, by design, and the
  // API does not change for this polish pass). The snapshot tile should
  // count judges only, so ask /people (the same organiser-only endpoint
  // organiser-people.js uses) and count role === 'judge' ourselves. Falls
  // back to the server's own count if that call fails, rather than showing
  // nothing. The hero action for Open and Building also needs to know
  // whether anyone is waiting on approval, from /applications (same source
  // organiser-people.js reads); only fetched at those two stages, since
  // they're the only ones that use it (owner, 2026-10-31: applications and
  // link sign-ups needing approval arrive in Building too, not only Open).
  let judgesOnly = counts.judges ?? 0
  let pendingCount = 0
  try {
    const base = '/v1/hack/events/' + encodeURIComponent(ev.slug)
    const calls = [api('GET', base + '/people')]
    if (ev.stage === 'open' || ev.stage === 'building') calls.push(api('GET', base + '/applications'))
    const [p, a] = await Promise.all(calls)
    if (p.ok && Array.isArray(p.data)) judgesOnly = p.data.filter(x => x.role === 'judge').length
    if (a && a.ok && Array.isArray(a.data)) pendingCount = a.data.filter(x => x.status === 'pending').length
  } catch (e) {}
  if (!ctx.alive()) return

  const action = heroAction(ev, pendingCount, href)

  // replay-organiser-walkthrough: the shell's Help menu links here with
  // ?walkthrough=1, which forces the guided tour open even if it was
  // dismissed before. tour.js itself checks organiser_walkthrough_done.
  const openWalk = params.get('walkthrough') === '1'
  const archived = ev.stage === 'archived'

  main.innerHTML = `
<div class="ph"><div class="t"><h1>Overview</h1><p><a href="${ui.esc(ev.url)}" target="_blank" rel="noopener">${ui.esc(ev.url.replace(/^https?:\/\//, ''))}</a></p></div><button class="btn" type="button" id="ov-change-stage" data-tour="change-stage"${archived ? ' disabled' : ''}>Change stage</button></div>
${step.warn
    ? `<div class="note warn" role="alert"><b>The entry deadline has passed.</b> Check Entries and move to Judging when ready.</div>`
    : `<section class="next"><div class="t"><p class="k">Your next step</p><h2>${ui.esc(step.text)}</h2></div>${action ? `<a class="btn primary" href="${ui.esc(action.href)}"${action.external ? ' target="_blank" rel="noopener"' : ''}>${ui.esc(action.label)}</a>` : ''}${ev.stage === 'draft' ? `<a class="btn" href="${ui.esc(href('organiser', ev.slug, 'people'))}#signup">Sign-up questions</a>` : ''}</section>`}
<h2 class="section-title">Snapshot</h2>
<div class="grid4">
  <div class="stat"><b>${counts.participants ?? 0}</b><span>Participants</span></div>
  <div class="stat"><b>${counts.teams ?? 0}</b><span>Teams</span></div>
  <div class="stat"><b>${judgesOnly}</b><span>Judges</span></div>
  <div class="stat"><b>${counts.on_no_team ?? 0}</b><span>Not on a team</span></div>
</div>
${archived ? '' : `
<h2 class="section-title">Share your event</h2>
<div data-tour="share">
<section class="card"><h2>Join link</h2><div class="copy"><input class="input mono" id="ov-join-url" aria-label="Join link" value="${ui.esc(org.join_url || '')}" readonly><button class="btn" type="button" id="ov-join-copy" data-admin-live>Copy</button><button class="btn danger sm" type="button" id="ov-join-new">New link</button></div></section>
<section class="card"><h2>Judge link</h2><div class="copy"><input class="input mono" id="ov-judge-url" aria-label="Judge link" value="${ui.esc(org.judge_url || '')}" readonly><button class="btn" type="button" id="ov-judge-copy" data-admin-live>Copy</button><button class="btn danger sm" type="button" id="ov-judge-new">New link</button></div></section>
</div>`}
${archived ? '' : `<section class="danger-zone"><h2>Danger zone</h2><div class="setting"><div class="t"><b>End the event</b><span>Ending locks sign-up and scoring for good. Team sites are removed after 30 days.</span></div>${(counts.participants ?? 0) > 0
    ? `<button class="btn danger" type="button" id="ov-end">End event</button>`
    : `<div class="note">Nobody joined, so there is nothing to end. You can delete the event in <a href="${ui.esc(href('organiser', ev.slug, 'settings'))}">Settings</a>.</div>`}</div></section>`}`

  adminLock(main, admin)

  if (openWalk) startTour('organiser', ctx, { force: true })
  else if (!autoTourTried) { autoTourTried = true; startTour('organiser', ctx) }

  if (!archived) {
    // Same clipboard logic as ui.copyButton, against the existing readonly inputs.
    async function copyInput(input, btn) {
      try { await navigator.clipboard.writeText(input.value); btn.textContent = 'Copied'; setTimeout(() => btn.textContent = 'Copy', 2000) }
      catch (e) { setLive('Could not copy. Select the text and copy it.', 'err') }
    }
    $('ov-join-copy').addEventListener('click', () => copyInput($('ov-join-url'), $('ov-join-copy')))
    $('ov-judge-copy').addEventListener('click', () => copyInput($('ov-judge-url'), $('ov-judge-copy')))

    // B4: the shell's own top-bar "Copy join/judge link" buttons are built
    // once, at mount time, from the url the shell was given -- they do not
    // listen for a change here. ctx.refresh() re-fetches the event and
    // re-mounts the shell (app.js route()), which is the only way today to
    // get the shell's copy buttons onto the new link; see requests-O1.md
    // for the cleaner fix (the shell refreshing its own buttons in place).
    async function regen(kind, btn) {
      const ok = await ui.confirm({ title: 'Make a new link?', body: kind === 'join' ? 'The old link stops working, even for people who have not used it yet. People already in the event stay.' : 'The old judge link stops working. Judges already in stay.', confirmLabel: 'New link', danger: true })
      if (!ok) return
      btn.disabled = true
      try {
        const r = await api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/codes/' + kind)
        if (!r.ok) { setLive(ui.errText(r), 'err'); btn.disabled = false; return }
        await ctx.refresh()
      } catch (e) { setLive('Cannot reach the server.', 'err'); btn.disabled = false }
    }
    $('ov-join-new').addEventListener('click', () => regen('join', $('ov-join-new')))
    $('ov-judge-new').addEventListener('click', () => regen('judge', $('ov-judge-new')))

    const endBtn = $('ov-end')
    if (endBtn) {
      endBtn.addEventListener('click', async () => {
        const ok = await ui.confirm({ title: 'End this event?', body: 'This is final. The event moves to Ended, scoring and sign-up lock for good, and team sites start their 30-day countdown before cleanup. Continue?', confirmLabel: 'End event', danger: true })
        if (!ok) return
        endBtn.disabled = true
        try {
          const r = await api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/stage', { stage: 'archived' })
          if (!r.ok) { setLive(ui.errText(r), 'err'); endBtn.disabled = false; return }
          await ctx.refresh()
        } catch (e) { setLive('Cannot reach the server.', 'err'); endBtn.disabled = false }
      })
    }
  }

  $('ov-change-stage').addEventListener('click', async () => {
    const target = await stagePicker(ev.stage, ev.stages_offered || STAGE_ORDER)
    if (!target) return
    const btn = $('ov-change-stage')
    btn.disabled = true
    try {
      const r = await api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/stage', { stage: target })
      if (!r.ok) { setLive(ui.errText(r), 'err'); btn.disabled = false; return }
      await ctx.refresh()
    } catch (e) { setLive('Cannot reach the server.', 'err'); btn.disabled = false }
  })
}
