import { href } from '../nav.js'
import { adminLock, stagePicker } from './_orgA-util.js'
import { startTour } from '../tour.js'
import { cueHeading } from '../schedule.js'

const STAGE_ORDER = ['draft', 'open', 'building', 'closed', 'judging', 'results', 'archived']

// Guided walkthrough: shown once, automatically, the first time an
// organiser lands on Overview (tour.js checks the stored preference
// itself). Guarded so a refresh of this same screen (e.g. after Change
// stage) doesn't keep popping it back open within one page load.
let autoTourTried = false

export function title(ctx) { return 'Overview' }

// Teams redesign (owner, 2026-10-31): the next-step card now holds exactly
// one button that performs the next forward move itself, after one
// confirm. The server computes the step (`next_step` on the organiser
// event view -- GET /v1/hack/events/{slug}, organiser only) so the
// website and the AI apps always agree on what "next" means (parity rule
// 2026-10-27); this screen only turns that into the card's h2/sentence and
// the confirm's heads-up, using the exact copy spec.json's
// `organiser-overview.next_step_map` and prototype states give for each
// (kind, stage, deadline_passed, blocked) combination.
function nextStepContent(ev, step, resultsHref, settingsHref) {
  if (!step || step.kind === 'none') {
    return { h2: 'This event has ended', sentence: 'The public page stays up as a record.', confirmBody: '', button: null }
  }
  if (step.kind === 'publish_results') {
    return {
      h2: 'Publish results',
      sentenceHTML: `Teams see their results straight away. Your event page shows them the way you set on <a href="${resultsHref}">Results</a>.`,
      confirmBody: 'Teams see their results straight away. Your event page shows them the way you set on Results.',
      headsUp: 'To set a final order for ties, use Results first.',
      button: step.label
    }
  }
  switch (step.stage) {
    case 'open':
      return { h2: 'Open sign-up when your page is ready', sentence: 'People can join with your link and apply, and judges can use their invite link.', button: step.label }
    case 'building':
      return { h2: 'Start building when people are in', sentence: 'Entries open. Teams can edit their entry and website until you close entries.', button: step.label }
    case 'closed':
      return step.deadline_passed
        ? { h2: 'The entry deadline has passed', sentence: 'Close entries so judges can start scoring. Teams with their own extension keep it.', button: step.label, warn: true }
        : { h2: 'Close entries when building time is over', sentence: 'Entries close for every team now, except teams with their own extension. Judges can start scoring.', button: step.label }
    case 'judging':
      return { h2: 'Start judging when every entry is in', sentence: 'Teams can no longer edit their entry, team or website. Judges keep scoring.', button: step.label }
    case 'results':
      return { h2: 'Finish judging when every judge is done', sentence: 'Scoring stops and judging locks. Next you publish results.', button: step.label }
    case 'archived':
      if (step.blocked === 'archive_needs_participants') {
        return { h2: "End the event when you're done", sentenceHTML: `Nobody is in this event, so it can't be ended. Delete it in <a href="${settingsHref}">Settings</a> instead.`, confirmBody: '', button: step.label, blocked: true }
      }
      return { h2: "End the event when you're done", sentence: 'Scoring and sign-up lock for good. Team sites are removed after 30 days.', button: step.label, isEnd: true }
    default:
      return { h2: step.label || '', sentence: '', button: step.label }
  }
}

const END_CONFIRM = {
  title: 'End this event?',
  body: 'This is final. The event moves to Ended, scoring and sign-up lock for good, and team sites start their 30-day countdown before cleanup. Continue?',
  confirmLabel: 'End event',
  danger: true
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
  const archived = ev.stage === 'archived'
  const resultsHref = href('organiser', ev.slug, 'results')
  const settingsHref = href('organiser', ev.slug, 'settings')

  const step = data.next_step || { kind: 'none' }
  const content = nextStepContent(ev, step, ui.esc(resultsHref), ui.esc(settingsHref))
  // A schedule that acts (owner, 2026-11-02): when a schedule row lining up
  // with this move has come (next_step.cue, computed by the server), the
  // heading asks, e.g. "Kickoff is now. Open sign-up?". It only asks. The
  // move still waits for this button and its confirm.
  const cue = content.button && !content.blocked ? cueHeading(ui, ev, step) : ''
  if (cue) {
    content.h2 = cue
    content.cueNote = 'From your schedule. Nothing changes until you press ' + content.button + '.'
  }

  // C7 (owner decision 2026-10-30): the server's judges count includes
  // organisers who can judge (db.CountEligibleJudges, by design). The
  // snapshot tile should count judges only, so ask /people (the same
  // organiser-only endpoint organiser-people.js uses) and count
  // role === 'judge' ourselves. Falls back to the server's own count if
  // that call fails. The pending-approvals heads-up (Start building) and
  // the zero-judges heads-up (Start judging) only matter for those two
  // specific next steps, so their own fetches are conditional on the step
  // actually being offered.
  let judgesOnly = counts.judges ?? 0
  let pendingCount = 0
  // Audit item 2 (2026-11-02): in panel mode, a team with no track matches
  // no track panel and gets no judges at all (internal/db/hack_judging_options.go).
  // Fetched only before "Start judging" is offered -- the same conditional-
  // fetch pattern as the pending-approvals and zero-judges heads-ups above.
  let noTrackTeams = []
  const atStartJudging = step.kind === 'stage' && step.stage === 'judging'
  try {
    const base = '/v1/hack/events/' + encodeURIComponent(ev.slug)
    const calls = [api('GET', base + '/people')]
    if (step.kind === 'stage' && step.stage === 'building') calls.push(api('GET', base + '/applications'))
    if (atStartJudging) calls.push(api('GET', base + '/judging/settings'), api('GET', base + '/teams'))
    const results = await Promise.all(calls)
    const p = results[0]
    if (p.ok && Array.isArray(p.data)) judgesOnly = p.data.filter(x => x.role === 'judge').length
    if (step.kind === 'stage' && step.stage === 'building') {
      const a = results[1]
      if (a && a.ok && Array.isArray(a.data)) pendingCount = a.data.filter(x => x.status === 'pending').length
    }
    if (atStartJudging) {
      const settingsRes = results[1]
      const teamsRes = results[2]
      if (settingsRes && settingsRes.ok && settingsRes.data && settingsRes.data.assignment_mode === 'panel' &&
          teamsRes && teamsRes.ok && teamsRes.data && Array.isArray(teamsRes.data.teams)) {
        noTrackTeams = teamsRes.data.teams.filter(t => !t.track)
      }
    }
  } catch (e) {}
  if (!ctx.alive()) return

  // Heads-ups (spec.json organiser-overview states, "confirming the move"):
  // shown once, in the confirm dialog, under the same effect sentence the
  // card already shows. Only one ever applies, since only one next step is
  // ever offered at a time.
  let headsUp = content.headsUp || ''
  if (step.kind === 'stage' && step.stage === 'building' && pendingCount > 0) {
    headsUp = pendingCount + ' people are still waiting for approval. You can approve them later too.'
  } else if (step.kind === 'stage' && step.stage === 'closed' && (counts.on_no_team ?? 0) > 0) {
    headsUp = (counts.on_no_team) + ' people are not on a team yet.'
  } else if (step.kind === 'stage' && step.stage === 'judging' && judgesOnly === 0) {
    headsUp = 'No judges have joined yet. Share the judge link first.'
  } else if (atStartJudging && noTrackTeams.length > 0) {
    const n = noTrackTeams.length
    headsUp = n + ' team' + (n === 1 ? '' : 's') + " with no track won't be judged until " + (n === 1 ? 'it picks' : 'they pick') + ' one, or you assign one.'
  }

  // Named list, visible on the card itself (not only in the confirm) --
  // the organiser should see this before clicking "Start judging", not
  // just when confirming it.
  const teamsHref = href('organiser', ev.slug, 'teams')
  const noTrackNoteHTML = (atStartJudging && noTrackTeams.length > 0)
    ? `<div class="note warn">${noTrackTeams.length} team${noTrackTeams.length === 1 ? '' : 's'} with no track won't be judged until ${noTrackTeams.length === 1 ? 'it picks' : 'they pick'} one, or you assign one: ${noTrackTeams.map(t => ui.esc(t.name || 'A team')).join(', ')}. <a href="${ui.esc(teamsHref)}">Go to Teams</a></div>`
    : ''

  // replay-organiser-walkthrough: the shell's Help menu links here with
  // ?walkthrough=1, which forces the guided tour open even if it was
  // dismissed before. tour.js itself checks organiser_walkthrough_done.
  const openWalk = params.get('walkthrough') === '1'

  main.innerHTML = `
<div class="ph keep-row"><div class="t"><h1>Overview</h1><p><a href="${ui.esc(ev.url)}" target="_blank" rel="noopener">${ui.esc(ev.url.replace(/^https?:\/\//, ''))}</a></p></div><button class="btn" type="button" id="ov-change-stage" data-tour="change-stage"${archived ? ' disabled' : ''}>Change stage</button></div>
${content.button
    ? `<section class="next${content.warn ? ' warn' : ''}"><div class="t"><p class="k">Your next step</p><h2>${ui.esc(content.h2)}</h2><p>${content.sentenceHTML || ui.esc(content.sentence)}</p>${content.cueNote ? `<p class="small" id="ov-cue-note">${ui.esc(content.cueNote)}</p>` : ''}</div><button class="btn primary lg" type="button" id="ov-next-move" data-tour="next-move"${content.blocked ? ' disabled' : ''}>${ui.esc(content.button)}</button></section>`
    : `<section class="next"><div class="t"><p class="k">Your next step</p><h2>${ui.esc(content.h2)}</h2><p>${content.sentenceHTML || ui.esc(content.sentence)}</p></div></section>`}
${noTrackNoteHTML}
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
${archived || content.isEnd ? '' : `<section class="danger-zone"><h2>Danger zone</h2><div class="setting"><div class="t"><b>End the event</b><span>Ending locks sign-up and scoring for good. Team sites are removed after 30 days.</span></div>${(counts.participants ?? 0) > 0
    ? `<button class="btn danger" type="button" id="ov-end">End event</button>`
    : `<div class="note">Nobody joined, so there is nothing to end. You can delete the event in <a href="${ui.esc(settingsHref)}">Settings</a>.</div>`}</div></section>`}`

  adminLock(main, admin)

  if (openWalk) startTour('organiser', ctx, { force: true })
  else if (!autoTourTried) { autoTourTried = true; startTour('organiser', ctx) }

  async function callStage(target) {
    return api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/stage', { stage: target })
  }

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
    // get the shell's copy buttons onto the new link.
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

    async function runEnd(btn) {
      const ok = await ui.confirm(END_CONFIRM)
      if (!ok) return
      btn.disabled = true
      try {
        const r = await callStage('archived')
        if (!r.ok) { setLive(ui.errText(r), 'err'); btn.disabled = false; return }
        await ctx.refresh()
      } catch (e) { setLive('Cannot reach the server.', 'err'); btn.disabled = false }
    }
    const endBtn = $('ov-end')
    if (endBtn) endBtn.addEventListener('click', () => runEnd(endBtn))
  }

  const nextMoveBtn = $('ov-next-move')
  if (nextMoveBtn && !content.blocked) {
    nextMoveBtn.addEventListener('click', async () => {
      // End event, offered as the one next step (results already
      // published): the exact same danger confirm as the danger-zone
      // button, never the generic heads-up confirm.
      if (content.isEnd) { await runEndFromNext(); return }
      const bodyNodes = [ui.el('p', { text: content.confirmBody || content.sentence || '' })]
      if (headsUp) bodyNodes.push(ui.el('p', { class: 'note warn', text: headsUp }))
      const ok = await ui.confirm({ title: content.button + '?', body: bodyNodes, confirmLabel: content.button })
      if (!ok) return
      nextMoveBtn.disabled = true
      try {
        const r = step.kind === 'publish_results'
          ? await api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/results/publish', {})
          : await callStage(step.stage)
        if (!r.ok) { setLive(ui.errText(r), 'err'); nextMoveBtn.disabled = false; await ctx.refresh(); return }
        await ctx.refresh()
      } catch (e) { setLive('Cannot reach the server.', 'err'); nextMoveBtn.disabled = false }
    })
    async function runEndFromNext() {
      const ok = await ui.confirm(END_CONFIRM)
      if (!ok) return
      nextMoveBtn.disabled = true
      try {
        const r = await callStage('archived')
        if (!r.ok) { setLive(ui.errText(r), 'err'); nextMoveBtn.disabled = false; await ctx.refresh(); return }
        await ctx.refresh()
      } catch (e) { setLive('Cannot reach the server.', 'err'); nextMoveBtn.disabled = false }
    }
  }

  $('ov-change-stage').addEventListener('click', async () => {
    const target = await stagePicker(ev.stage, ev.stages_offered || STAGE_ORDER, { judgingLocked: !!ev.judging_locked_at })
    if (!target) return
    const btn = $('ov-change-stage')
    btn.disabled = true
    try {
      const r = await callStage(target)
      if (!r.ok) { setLive(ui.errText(r), 'err'); btn.disabled = false; return }
      await ctx.refresh()
    } catch (e) { setLive('Cannot reach the server.', 'err'); btn.disabled = false }
  })
}
