import { href } from '../nav.js'
import { errText, adminLock, stagePicker } from './_orgA-util.js'

const STAGE_ORDER = ['draft', 'open', 'building', 'closed', 'judging', 'results', 'archived']
const STAGE_LABEL = { draft: 'Draft', open: 'Open', building: 'Building', closed: 'Entries closed', judging: 'Judging', results: 'Results', archived: 'Ended' }

export function title(ctx) { return 'Overview' }

// nextStep answers "what do I do now" for the whole event, from real state
// rather than a fixed per-stage string (spec.json organiser-overview.purpose).
function nextStep(ev) {
  const deadlinePassed = ev.submission_deadline && new Date(ev.submission_deadline) <= new Date()
  switch (ev.stage) {
    case 'draft':
      return { warn: false, text: 'Set up your event page and sign-up questions, then open sign-up when ready', actions: true }
    case 'open':
      return { warn: false, text: 'Sign-up is open. Approve applications as people apply, then move to Building once teams start forming.' }
    case 'building':
      if (deadlinePassed) return { warn: true, text: 'The entry deadline has passed. Check Entries and move to Judging when ready.' }
      return { warn: false, text: 'Keep teams building, then close entries when ready' }
    case 'closed':
      return { warn: false, text: 'Entries are closed. Write your rubric and assign judges, then move to Judging.' }
    case 'judging':
      return { warn: false, text: ev.judging_locked_at ? 'Scoring is locked. Publish results when you’re ready.' : 'Judging is open. Lock scoring once every judge has finished, then publish results.' }
    case 'results':
      return { warn: false, text: ev.results_published ? 'Results are published.' : 'Judging is locked. Publish results when you’re ready.' }
    default:
      return { warn: false, text: 'This event has ended.' }
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
  const setLive = (msg, kind) => { if (msg) ui.toast(msg, main, kind === 'err') }

  const order = STAGE_ORDER
  const curIdx = order.indexOf(ev.stage)
  const stageTrack = order.map((s, i) => `<span class="${s === ev.stage ? 'now' : i < curIdx ? 'done' : ''}">${ui.esc(STAGE_LABEL[s])}</span>`).join('')
  const next = ui.nextDeadline(ev)
  const step = nextStep(ev)

  let walk
  try { walk = await api('GET', '/v1/hack/preferences') } catch (e) { walk = { ok: false } }
  const walkDone = walk.ok && walk.data && walk.data.organiser_walkthrough_done

  main.innerHTML = `
<div class="ph"><div class="t"><h1>Overview</h1><p>${ui.esc(ev.title)}</p></div><button class="btn primary" type="button" id="ov-change-stage"${ev.stage === 'archived' ? ' disabled' : ''}>Change stage</button></div>
<div class="stages">${stageTrack}</div>
<p>Public address: <a href="${ui.esc(ev.url)}" target="_blank" rel="noopener">${ui.esc(ev.url.replace(/^https?:\/\//, ''))}</a></p>
${step.warn
    ? `<div class="note warn" role="alert"><b>The entry deadline has passed.</b> Check Entries and move to Judging when ready.</div>`
    : `<section class="next"><div class="t"><p class="k">Your next step</p><h2>${ui.esc(step.text)}</h2>${next ? `<p>${ui.esc(next[0])} &middot; ${ui.esc(ui.dateTime(next[1], ev))}</p>` : ''}</div></section>`}
${step.actions ? `<div class="row-flex"><a class="btn" href="${ui.esc(href('organiser', ev.slug, 'event-page'))}">Set up event page</a><a class="btn" href="${ui.esc(href('organiser', ev.slug, 'people'))}#signup">Sign-up questions</a></div>` : ''}
<h2 class="section-title">Snapshot</h2>
<div class="grid3">
  <div class="stat"><b>${counts.participants ?? 0}</b><span>Participants</span></div>
  <div class="stat"><b>${counts.teams ?? 0}</b><span>Teams</span></div>
  <div class="stat"><b>${counts.judges ?? 0}</b><span>Can judge</span></div>
  <div class="stat"><b>${counts.on_no_team ?? 0}</b><span>On no team</span></div>
</div>
<h2 class="section-title">Share your event</h2>
<section class="card"><h2>Join link for participants</h2><div class="copy"><input class="input mono" id="ov-join-url" aria-label="Join link for participants" value="${ui.esc(org.join_url || '')}" readonly><button class="btn" type="button" id="ov-join-copy" data-admin-live>Copy</button><button class="btn danger sm" type="button" id="ov-join-new">New link</button></div></section>
<section class="card"><h2>Link for judges</h2><div class="copy"><input class="input mono" id="ov-judge-url" aria-label="Link for judges" value="${ui.esc(org.judge_url || '')}" readonly><button class="btn" type="button" id="ov-judge-copy" data-admin-live>Copy</button><button class="btn danger sm" type="button" id="ov-judge-new">New link</button></div></section>
<section class="danger-zone"><h2>Danger zone</h2><div class="setting"><div class="t"><b>End the event</b><span>Ending needs at least one participant. An event nobody joined should be deleted instead, on <a href="${ui.esc(href('organiser', ev.slug, 'settings'))}">Settings</a>.</span></div>${(counts.participants ?? 0) > 0
    ? `<button class="btn danger" type="button" id="ov-end">End event</button>`
    : `<div class="note">Nobody joined this event. End isn’t available — delete it instead, on <a href="${ui.esc(href('organiser', ev.slug, 'settings'))}">Settings</a>.</div>`}</div></section>
<p><button class="btn quiet" type="button" id="ov-walk-toggle">${walkDone ? 'Replay walkthrough' : 'Hide walkthrough'}</button></p>
<section class="card" id="ov-walk" ${walkDone ? 'hidden' : ''}><h2>Running your event</h2><ol class="steps">
  <li class="${ev.stage === 'draft' ? 'now' : 'done'}"><span class="tick">${ev.stage === 'draft' ? '' : '✓'}</span><div class="what"><b>Draft</b>Set up the event page and sign-up questions.</div></li>
  <li class="${ev.stage === 'open' ? 'now' : curIdx > 1 ? 'done' : ''}"><span class="tick">${curIdx > 1 ? '✓' : ''}</span><div class="what"><b>Open</b>Share the join link; approve applications as they come in.</div></li>
  <li class="${ev.stage === 'building' ? 'now' : curIdx > 2 ? 'done' : ''}"><span class="tick">${curIdx > 2 ? '✓' : ''}</span><div class="what"><b>Building</b>Teams form and build. Close entries when the deadline passes.</div></li>
  <li class="${ev.stage === 'closed' ? 'now' : curIdx > 3 ? 'done' : ''}"><span class="tick">${curIdx > 3 ? '✓' : ''}</span><div class="what"><b>Entries closed</b>Write the rubric and assign judges.</div></li>
  <li class="${ev.stage === 'judging' ? 'now' : curIdx > 4 ? 'done' : ''}"><span class="tick">${curIdx > 4 ? '✓' : ''}</span><div class="what"><b>Judging</b>Judges score; lock scoring once everyone is done.</div></li>
  <li class="${ev.stage === 'results' ? 'now' : curIdx > 5 ? 'done' : ''}"><span class="tick">${curIdx > 5 ? '✓' : ''}</span><div class="what"><b>Results</b>Publish results when you're ready.</div></li>
  <li class="${ev.stage === 'archived' ? 'now' : ''}"><span class="tick">${ev.stage === 'archived' ? '✓' : ''}</span><div class="what"><b>Ended</b>Scoring and sign-up lock for good.</div></li>
</ol><button class="btn sm" type="button" id="ov-walk-done">Done, don't show again</button></section>`

  adminLock(main, admin)

  $('ov-walk-toggle').addEventListener('click', () => {
    const p = $('ov-walk'); const btn = $('ov-walk-toggle')
    p.hidden = !p.hidden
    btn.textContent = p.hidden ? 'Replay walkthrough' : 'Hide walkthrough'
  })
  $('ov-walk-done').addEventListener('click', async () => {
    $('ov-walk').hidden = true
    try { await api('PATCH', '/v1/hack/preferences', { organiser_walkthrough_done: true }) } catch (e) {}
  })

  // Same clipboard logic as ui.copyButton, against the existing readonly inputs.
  async function copyInput(input, btn) {
    try { await navigator.clipboard.writeText(input.value); btn.textContent = 'Copied'; setTimeout(() => btn.textContent = 'Copy', 2000) }
    catch (e) { setLive('Could not copy. Select the text and copy it.', 'err') }
  }
  $('ov-join-copy').addEventListener('click', () => copyInput($('ov-join-url'), $('ov-join-copy')))
  $('ov-judge-copy').addEventListener('click', () => copyInput($('ov-judge-url'), $('ov-judge-copy')))

  async function regen(kind, btn, urlInput) {
    const ok = await ui.confirm({ title: 'Make a new link?', body: kind === 'join' ? 'Anyone with the old link can no longer join, even people who never used it yet. People already in stay. Generate a new link?' : 'Anyone with the old judge link can no longer open it. Judges already in stay. Generate a new link?', confirmLabel: 'New link', danger: true })
    if (!ok) return
    btn.disabled = true
    try {
      const r = await api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/codes/' + kind)
      if (!r.ok) { setLive(errText(r), 'err'); return }
      urlInput.value = r.data.url || ''
      setLive('New link ready', 'ok')
    } catch (e) { setLive('Cannot reach the server.', 'err') }
    finally { btn.disabled = false }
  }
  $('ov-join-new').addEventListener('click', () => regen('join', $('ov-join-new'), $('ov-join-url')))
  $('ov-judge-new').addEventListener('click', () => regen('judge', $('ov-judge-new'), $('ov-judge-url')))

  $('ov-change-stage').addEventListener('click', async () => {
    const target = await stagePicker(ev.stage, ev.stages_offered || STAGE_ORDER)
    if (!target) return
    const btn = $('ov-change-stage')
    btn.disabled = true
    try {
      const r = await api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/stage', { stage: target })
      if (!r.ok) { setLive(errText(r), 'err'); btn.disabled = false; return }
      await ctx.refresh()
    } catch (e) { setLive('Cannot reach the server.', 'err'); btn.disabled = false }
  })

  const endBtn = $('ov-end')
  if (endBtn) {
    endBtn.addEventListener('click', async () => {
      const ok = await ui.confirm({ title: 'End this event?', body: 'This is final. The event moves to Ended, scoring and sign-up lock for good, and team sites start their 30-day countdown before cleanup. Continue?', confirmLabel: 'End event', danger: true })
      if (!ok) return
      endBtn.disabled = true
      try {
        const r = await api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/stage', { stage: 'archived' })
        if (!r.ok) { setLive(errText(r), 'err'); endBtn.disabled = false; return }
        await ctx.refresh()
      } catch (e) { setLive('Cannot reach the server.', 'err'); endBtn.disabled = false }
    })
  }
}
