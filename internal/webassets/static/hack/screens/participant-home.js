import { href } from '../nav.js'
import { startTour } from '../tour.js'
import { previewLeave, openLeave, participantWarning } from '../leave.js'
import { merge, nudgeHTML, keepNudgeFresh } from '../schedule.js'
import { needsCard, bindDismiss } from '../needs.js'

export function title() { return 'Overview' }

// Atlas "entries and participant journey" package (2026-11-02, slice 1): the
// "Your entry" card (deadline countdown + four-step checklist, build-plan.md
// section 4.3's `progress`) and the Submit-from-Home action.
//
// CONTRACT (coordinator decision, 2026-11-02 -- see participant-entry.js for
// the full note): the deadline sweep settles an incomplete draft as
// `progress.steps[id=submit].status === "not_submitted"` -- still judged,
// flagged, never a separate boolean. Confirmed against the landed backend
// (internal/handler/hack_entry_rules.go entryProgressJSON) while building
// this screen.
const PLAIN = {
  title: 'project name', problem: 'the problem', tagline: 'the solution', audience: "who it's for",
  track: 'a track', tools: 'tools and AI', maturity: 'what works today',
  video_url: 'a video link', site: 'a published website', code_url: 'a code link', screenshot: 'a screenshot', description: 'a description'
}
function joinAnd (items) {
  if (!items.length) return ''
  if (items.length === 1) return items[0]
  return items.slice(0, -1).join(', ') + ' and ' + items[items.length - 1]
}
function stepByID (progress, id) { return ((progress && progress.steps) || []).find(s => s.id === id) || {} }

// "Entries close Sun 8 Nov, 18:00 WIB" / "Your team closes ..., N hours
// after everyone else" (extension) / "Entries closed ..." (past).
function deadlineSmallText (ui, ev, progress) {
  if (!progress || !progress.deadline) return ''
  const dt = ui.dateTime(progress.deadline, ev)
  const now = progress.server_now ? new Date(progress.server_now) : new Date()
  if (now >= new Date(progress.deadline)) return 'Entries closed ' + dt
  if (progress.deadline_is_extension && progress.event_deadline) {
    const diffH = Math.round((new Date(progress.deadline) - new Date(progress.event_deadline)) / 3600000)
    return 'Your team closes ' + dt + (diffH > 0 ? ', ' + diffH + ' hour' + (diffH === 1 ? '' : 's') + ' after everyone else' : '')
  }
  return 'Entries close ' + dt
}

function entrySectionHTML (ctx, ev, progress) {
  if (!progress) return ''
  const { ui } = ctx
  const esc = ui.esc
  const team = stepByID(progress, 'team'), summary = stepByID(progress, 'summary'), evidence = stepByID(progress, 'evidence'), submit = stepByID(progress, 'submit')

  function row (step, num, titleText, subText, action) {
    // `step.opt` (kit.css's existing dashed-tick style) marks a step that is
    // settled but not actually a success -- the deadline-sweep "not
    // submitted, still judged, Incomplete" outcome, so it never reads as a
    // plain green checkmark.
    const cls = [step.done ? 'done' : '', step.opt ? 'opt' : '', !step.done && progress.next === step.id ? 'now' : ''].filter(Boolean).join(' ')
    const tick = step.done ? '<span class="tick">✓</span>' : '<span class="tick"><span class="num">' + num + '</span></span>'
    return '<li class="' + cls + '">' + tick + '<span class="what"><b>' + esc(titleText) + '</b><span class="sub">' + esc(subText) + '</span></span>' + (action || '') + '</li>'
  }

  const teamHref = href('participant', ev.slug, 'team')
  const entryHref = href('participant', ev.slug, 'entry')
  const websiteHref = href('participant', ev.slug, 'website')

  const r1 = row(team, 1, 'Join or start a team', team.detail || "You're not on a team yet.", team.done ? '' : '<a class="btn sm act" href="' + esc(teamHref) + '">Find a team</a>')

  const summarySub = !team.done ? "Problem, solution, who it's for, track, tools and what works." : summary.done
    ? ('All ' + (summary.of || 0) + ' lines filled.')
    : ((summary.filled || 0) + ' of ' + (summary.of || 0) + ' filled' + ((summary.missing || []).length ? ': add ' + joinAnd(summary.missing.map(f => PLAIN[f] || f)) : '') + '.')
  const r2 = row(summary, 2, 'Fill in the summary card', summarySub, (!summary.done && team.done) ? '<a class="btn quiet sm act" href="' + esc(entryHref) + '">Edit</a>' : '')

  const evidenceTitle = evidence.summary ? ('Add ' + evidence.summary) : 'Show your work'
  const evidenceSub = !team.done ? 'A video or your website, as the event asks.' : evidence.done ? 'Done.' : (evidence.summary && evidence.summary.indexOf(' or ') >= 0 && evidence.summary.indexOf(', and ') < 0 ? 'Nothing yet. One of these is enough.' : 'Nothing yet.')
  const evidenceAction = (!evidence.done && summary.done) ? ('<a class="btn sm act" href="' + esc(websiteHref) + '">Publish website</a>') : ''
  const r3 = row(evidence, 3, evidenceTitle, evidenceSub, evidenceAction)

  let submitSub, submitAction = ''
  if (submit.status === 'not_submitted') {
    // Coordinator decision (2026-11-02, replacing build-plan.md's "not
    // submitted, not judged" rule): the deadline sweep settles an
    // incomplete draft as `status: "not_submitted"` without a separate
    // flag -- still judged unless an organiser excludes it by hand, which
    // this view has no way to see. Shown verbatim, here and on Entry.
    submitSub = 'Not complete. It will still be judged, marked incomplete.'
  } else if (submit.status === 'submitted') {
    submitSub = submit.auto ? 'Submitted automatically at the deadline.' : ('Submitted' + (submit.submitted_at ? ' ' + ui.dateTime(submit.submitted_at, ev) : '') + '.')
  } else if (!team.done) {
    submitSub = 'Then the judges can see it. You can still change it after.'
  } else if (progress.next === 'submit') {
    submitSub = 'Ready. Then the judges can see it. You can still change it after.'
    submitAction = '<button type="button" class="btn primary sm act" id="ph-submit-step">Submit</button>'
  } else {
    submitSub = 'Then the judges can see it. You can still change it after.'
  }
  const submitSettled = submit.status === 'submitted' || submit.status === 'not_submitted'
  const r4 = row({ done: submit.status === 'submitted', opt: submit.status === 'not_submitted' }, 4, 'Submit before the deadline', submitSub, submitAction)

  const countdownHTML = (progress.deadline && !submitSettled) || (progress.deadline && submit.status === 'submitted')
    ? '<span class="countdown" data-deadline="' + esc(progress.deadline) + '"><span class="v"></span> <small>' + (submit.status === 'submitted' ? 'left to make changes' : 'left') + '</small></span>'
    : ''
  const smallText = deadlineSmallText(ui, ev, progress)
  const deadlineLine = progress.deadline ? ('<p class="deadline-line">' + countdownHTML + (smallText ? (countdownHTML ? ' ' : '') + '<small>' + esc(smallText) + '</small>' : '') + '</p>') : ''

  return '<section class="card" data-tour="entry-checklist"><h2>Your entry</h2>' + deadlineLine + '<ul class="steps" style="margin-top:8px">' + r1 + r2 + r3 + r4 + '</ul></section>'
}

function wireEntrySection (ctx, ev, progress) {
  const { main, ui, api: client } = ctx
  const cd = main.querySelector('.countdown[data-deadline]')
  if (cd) ui.countdown(cd, progress.deadline, progress.server_now, { quiet: stepByID(progress, 'submit').status === 'submitted' })
  const btn = main.querySelector('#ph-submit-step')
  if (btn) btn.addEventListener('click', async () => {
    const deadlineText = progress.deadline ? ui.dateTime(progress.deadline, ev) : 'the deadline'
    const ok = await ui.confirm({ title: 'Submit your entry', body: 'Judges will see it. You can still change it until ' + deadlineText + '. It stays submitted.', confirmLabel: 'Submit entry' })
    if (!ok) return
    btn.disabled = true
    try {
      const r = await client.api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/entry/submit', {})
      if (!r.ok) { ui.toast(ui.errText(r, { fallback: 'Could not submit.' }), btn, true); btn.disabled = false; return }
      ui.toast('Submitted', btn)
      await ctx.refresh()
    } catch (e) { if (!client.isSignedOut(e)) { ui.toast('Cannot reach the server.', btn, true); btn.disabled = false } }
  })
}

// Guided walkthrough: shown once, automatically, the first time an approved
// participant lands on Overview (renamed from Home 2026-10-30; app.js never
// even imports this module for a pending/rejected participant, so no extra
// approval check is needed here). Guarded so a refresh of this same screen
// doesn't keep popping it back open within one page load.
let autoTourTried = false

// Fill teams (organiser-side) has no API signal that tells a participant
// "you were just placed" -- there is no feature id for this state (spec.json
// lists it without one) and no server field to read. This approximates it:
// remember the last team slug (or '' for no team) seen on this device for
// this event, and if the LAST recorded state was "no team" and now there is
// one, show the one-time note. A self-service join from the Team screen will
// also trigger it once; that is an accepted approximation given there is no
// way to tell the two apart without a server change. Because the API can't
// tell a self-service join/accept from an organiser's Fill teams apart
// either, the note never attributes the placement to the organiser (SIM-346
// Confusing #9) -- it just confirms the result.
// Cosmetic #5 (retest 3), round 2 (Codex review): a per-(user,team) "seen"
// flag read "missing" as "not seen", which is also what a brand-new key
// reads as on everyone's very first render after this shipped -- every
// existing participant already on a team got a one-time false "You are on
// X now.", and leaving + later being placed back on that exact same team
// never showed the note again. Back to a baseline comparison (what this
// screen did before retest 3) -- the actual bug was that the old key had
// no per-user scoping, not the comparison model itself. This key is
// shared on purpose with participant-team.js's own baseline (same key
// there) -- "you are on this team now" is one fact, so whichever of
// Home/Team the person sees first updates it for both, instead of showing
// the same banner twice on two screens.
function justPlacedNote(uid, slug, me) {
  const baselineKey = 'sh-team-baseline-' + uid + '-' + slug
  const teamSlug = me.team ? me.team.slug : ''
  let baseline = null
  try { baseline = localStorage.getItem(baselineKey) } catch (e) {}
  try { localStorage.setItem(baselineKey, teamSlug) } catch (e) {}
  if (baseline === null) return false // first sight: baseline set silently
  return baseline !== teamSlug && !!teamSlug
}

// Fetched once and read for both the open-teams count (openTeamsNote) and a
// pending invite (Confusing #6) -- one call, not two, for the same data
// participant-team.js's own "Teams looking for members"/"Invites for you"
// sections already use.
async function findingData(api, slug) {
  try {
    const r = await api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/open')
    if (!r.ok || !r.data || !r.data.enabled) return null
    return r.data
  } catch (e) { return null }
}
function openTeamsNote(finding) {
  const n = finding && Array.isArray(finding.teams) ? finding.teams.length : 0
  if (!n) return ''
  return n + ' team' + (n === 1 ? '' : 's') + ' ' + (n === 1 ? 'is' : 'are') + ' looking for members.'
}
// Confusing #6: a pending invite is the single most actionable thing on
// Home for someone with no team yet -- surface it before the generic
// "find a team" message, the same priority participant-team.js already
// gives it (S13: "Invites for you" shown first there too).
function pendingInvite(finding) {
  const pending = finding && Array.isArray(finding.pending) ? finding.pending : []
  return pending.find(p => p.kind === 'invite') || null
}

async function announcementsCard(api, ui, allHref, scheduleHref, slug, ev) {
  try {
    const r = await api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/announcements')
    const items = (r.ok && r.data && r.data.announcements) || []
    if (!items.length) return ''
    const esc = ui.esc
    const rows = items.slice(0, 2).map(a =>
      `<h3>${esc(a.title)}</h3><p class="small muted">${esc(ui.dateTime(a.created_at, ev, { timeStyle: undefined }))}</p><p>${esc(a.body)}</p>`
    ).join('<hr class="rule">')
    return `<div class="card"><h2>Latest announcements</h2>${rows}<div class="row-flex"><a class="btn" href="${esc(allHref)}">All announcements</a><a class="btn quiet" href="${esc(scheduleHref)}">Schedule</a></div></div>`
  } catch (e) { return '' }
}

// A schedule that acts (owner, 2026-11-02): a row starting within 15
// minutes, or on now, shows as one line above the next step ("Demos starts
// in 12 minutes", "Build time is on now"), counting down while Home is open.
// The rows include the event's real times (the entry deadline, or this
// team's own extension). In the app only: the schedule sends no email.
async function scheduleRows(api, slug) {
  try {
    const r = await api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/content')
    if (!r.ok || !r.data) return []
    return merge(r.data.schedule, r.data.fixed_schedule)
  } catch (e) { return [] }
}

// Leave this event: danger zone last on the page. Round-5 C1: this row now
// calls leave.js's own participantWarning(), the exact text the Leave
// dialog's first line builds from the same preview, so the two can never
// disagree -- the row used to hand-roll a shorter version that, for a lone
// member before the deadline, wrongly promised "your teammates keep the
// team" when leaving actually deletes it. Returns '' (no section at all) if
// the preview couldn't be loaded -- never show a Leave button whose state
// we can't vouch for.
//
// Owner decision 2026-10-31: leaving is no longer blocked past a team's
// deadline (submissions_closed never appears in preview.blocked for a
// participant any more) -- only an ended event still refuses, so that's
// the only blocked branch left here.
function participantDangerZone(ui, preview, ev) {
  if (!preview) return ''
  const b = 'Leave ' + ev.title
  let span, disabled = false
  if (preview.blocked) {
    disabled = true
    if (preview.blocked.code === 'event_closed') {
      span = 'This event has ended, so nobody can leave it. It stays on your Home as a record.'
    } else if (preview.blocked.code === 'event_taken_down') {
      span = 'This event has been taken down.'
    } else {
      span = preview.blocked.message || "You can't leave this event right now."
    }
  } else {
    span = participantWarning(ui, preview)
  }
  return `<section class="danger-zone"><h2>Leave this event</h2><div class="setting"><div class="t"><b>${ui.esc(b)}</b><span>${ui.esc(span)}</span></div><button class="btn danger" type="button" id="leave-btn"${disabled ? ' disabled' : ''}>Leave event</button></div></section>`
}

export async function render(ctx) {
  const { main, api: client, ui, event } = ctx
  const api = client.api
  const esc = ui.esc
  const ev = event.event
  const me = event.me
  const slug = ev.slug
  const teamHref = href('participant', slug, 'team')
  const scheduleHref = href('participant', slug, 'schedule')
  const announcementsHref = href('participant', slug, 'announcements')
  const resultsHref = href('participant', slug, 'results')
  const entryHref = href('participant', slug, 'entry')
  const websiteHref = href('participant', slug, 'website')

  const uid = (ctx.me && (ctx.me.id || ctx.me.username)) || 'anon'
  const justPlaced = justPlacedNote(uid, slug, me)

  // Teams can only be started, joined or changed while sign-up/building is
  // open and the entry deadline (if any) has not already passed -- matches
  // participant-team.js's teamsOpen/canStart so Overview never promises an
  // action that screen would refuse.
  const teamsOpen = (ev.stage === 'open' || ev.stage === 'building') && !ev.taken_down
  const deadlinePassed = !!(ev.submission_deadline && new Date(ev.submission_deadline) <= new Date())
  const deadline = ev.submission_deadline ? ui.dateTime(ev.submission_deadline, ev) : ''
  // The entry checklist is for the build-and-submit phase. Once judging has
  // actually started (or results are out), it has nothing left to say.
  const showEntryCard = !ev.results_published && !['judging', 'results', 'archived'].includes(ev.stage)

  let heroTitle, heroNote = '', heroLabel = '', heroHref = '', heroExternal = false
  // The kind of waiting item the next-step card already leads with, so the
  // Needs you card above it doesn't say the same thing twice.
  let heroKind = ''
  // A team with its own extension follows its own deadline (R2-1).
  const teamDeadline = me.team && me.team.deadline ? ui.dateTime(me.team.deadline, ev) : deadline
  const teamFrozen = me.team ? !!me.team.frozen : deadlinePassed
  if (ev.results_published) {
    // Results beat every other state, with or without a team: there is
    // nothing left to build or join once they're out. S14: once the event
    // has also ended, say so up front -- "Results are out" on its own
    // reads as if the event were still running.
    heroTitle = ev.stage === 'archived' ? 'This event has ended. Results are out.' : 'Results are out'
    heroLabel = 'See results'
    heroHref = resultsHref
  } else if (ev.stage === 'archived') {
    // Ended with no results published (S14/C6): the old code fell through
    // to the "Keep building" default here. The public page still exists
    // and is the one useful link left.
    heroTitle = 'This event has ended'
    heroNote = 'The public page stays up as a record.'
    heroLabel = 'Open public page'
    heroHref = ev.url
    heroExternal = true
  } else if (!me.team && me.team_removal_notice) {
    heroTitle = me.team_removal_notice
    if (teamsOpen && !deadlinePassed) { heroLabel = 'Find a team'; heroHref = teamHref }
  } else if (!me.team) {
    const finding = teamsOpen && !deadlinePassed ? await findingData(api, slug) : null
    const invite = pendingInvite(finding)
    if (invite) {
      heroKind = 'invite'
      heroTitle = 'You have an invite from ' + invite.team_name
      // Nit: the find-a-team branch below keeps the deadline in view; an
      // invite is just as time-limited (the team's own deadline still
      // applies once accepted), so it keeps the same note instead of
      // dropping it.
      heroNote = deadline ? ('Entries close ' + deadline + '.') : ''
      heroLabel = 'Go to Team'
      heroHref = teamHref
    } else if (teamsOpen && !deadlinePassed) {
      const openNote = openTeamsNote(finding)
      heroNote = [openNote, deadline ? ('Entries close ' + deadline + '.') : ''].filter(Boolean).join(' ')
      heroTitle = 'Find a team to start building'
      heroLabel = 'Find a team'
      heroHref = teamHref
    } else {
      // Closed, judging or past the deadline while still "building": teams
      // are locked, so there is nothing to click (C5/S14) -- just the facts.
      heroTitle = "You're not on a team"
      heroNote = deadline ? ('Entries closed ' + deadline + '.') : ''
    }
  } else if (ev.stage === 'judging' || ev.stage === 'results') {
    heroTitle = ev.stage === 'results' ? 'Judging is done. Results are coming soon' : 'Judges are scoring now. Results come next'
    if (me.team.site && me.team.site.exists && me.team.site.url) {
      heroLabel = 'View your site'; heroHref = me.team.site.url; heroExternal = true
    } else {
      heroLabel = 'Go to your team'; heroHref = teamHref
    }
  } else if (teamFrozen) {
    heroTitle = 'Entries are closed. Judging starts soon'
    if (me.team.site && me.team.site.exists && me.team.site.url) {
      heroLabel = 'View your site'; heroHref = me.team.site.url; heroExternal = true
    } else {
      heroLabel = 'Go to your team'; heroHref = teamHref
    }
  } else if (me.team.admin && (me.team.pending_requests || 0) > 0) {
    // Teams redesign (owner, 2026-10-31): only admins can say yes or no now,
    // so a pending request has to reach them without anyone else nudging --
    // the next-step card says so when nothing more urgent applies (results,
    // the removal notice, an incomplete entry).
    const n = me.team.pending_requests
    heroKind = 'join_request'
    heroTitle = n + (n === 1 ? ' person' : ' people') + ' asked to join ' + me.team.name
    heroNote = 'Say yes or no on your Team page.'
    heroLabel = n === 1 ? 'See request' : 'See requests'
    heroHref = teamHref + '&tab=members'
  } else {
    // Atlas "entries and participant journey" package: this is the one
    // branch `progress` (build-plan.md 4.3) now drives -- every
    // higher-priority state above (results, archived, no team, frozen,
    // pending requests) keeps its own existing message unchanged.
    const progress = event.progress
    const next = progress && progress.next
    const submitStep = progress && stepByID(progress, 'submit')
    if (!progress) {
      // Defensive fallback while the server side of this package hasn't
      // landed yet, or for an old API response with no `progress` field.
      heroTitle = teamDeadline ? ('Keep building. Entries close ' + teamDeadline) : ('You are on ' + me.team.name + '. Keep building')
      heroLabel = 'Go to your entry'
      heroHref = entryHref
    } else if (submitStep.status === 'not_submitted') {
      // The deadline sweep has already settled this one -- checked before
      // `next` (which can still read "summary"/"evidence"/"submit" off a
      // post-settlement re-evaluation even though nothing is actionable any
      // more; see entryProgressJSON's own `next` switch, hack_entry_rules.go).
      heroTitle = me.team.name + ' is not complete'
      heroNote = 'Not complete. It will still be judged, marked incomplete.'
      heroLabel = 'Go to your entry'
      heroHref = entryHref
    } else if (next === 'summary') {
      const step = stepByID(progress, 'summary')
      const missingWords = (step.missing || []).map(f => PLAIN[f] || f)
      heroTitle = 'Finish your summary card' + (missingWords.length ? ': add ' + joinAnd(missingWords) : '')
      heroLabel = 'Finish your entry'
      heroHref = entryHref
    } else if (next === 'evidence') {
      const step = stepByID(progress, 'evidence')
      heroTitle = step.summary ? ('Add ' + step.summary) : 'Show your work'
      heroLabel = 'Publish your website'
      heroHref = websiteHref
    } else if (next === 'submit') {
      heroTitle = me.team.name + ' is ready. Submit it so judges can see it.'
      // The Your-entry card below carries the real Submit button (with its
      // own confirm); this link is a plain fallback straight to Entry.
      heroLabel = 'Go to your entry'
      heroHref = entryHref
    } else {
      // next === '' : nothing left in `progress.steps` to do, and status is
      // "submitted" (the only other value `submitStep.status` can be here).
      heroTitle = me.team.name + ' is submitted'
      heroNote = teamDeadline ? ('You can still change it until ' + teamDeadline + '. It stays submitted.') : 'It stays submitted.'
      heroLabel = 'View your entry'
      heroHref = entryHref
    }
  }

  const live = ev.stage !== 'archived' && !ev.taken_down
  const [news, preview, rows] = await Promise.all([
    announcementsCard(api, ui, announcementsHref, scheduleHref, slug, ev),
    previewLeave(client, slug).then(r => (r.ok ? r.data : null)).catch(() => null),
    live ? scheduleRows(api, slug) : Promise.resolve([])
  ])
  if (!ctx.alive()) return

  // No team yet (atlas participant-home, "no team yet"): the server sends no
  // `progress` until there is a team, but the whole path should still be
  // visible from minute one -- step 1 is current, 2 to 4 are listed quiet.
  const noTeamProgress = (!me.team && teamsOpen && ev.stage !== 'draft')
    ? { steps: [{ id: 'team', done: false }, { id: 'summary', done: false }, { id: 'evidence', done: false }, { id: 'submit', done: false, status: 'draft' }], next: 'team', deadline: ev.submission_deadline || null, server_now: null }
    : null

  // Audit item 1 (2026-11-02): a plain link from Home to the "How you're
  // judged" card that now lives on Entry -- the rubric itself is read
  // there (one fetch, not two), this is just the way in.
  const rubricCard = `<div class="card"><h2>How you're judged</h2><p>See the rubric the judges use to score your entry: criteria, descriptions and points.</p><a class="btn" href="${esc(entryHref)}">See the rubric</a></div>`

  // Needs you (atlas 2026-11-02): what waits on this person, from the
  // event view's needs_you -- requests and invites first, then news lines
  // (an application answer, a site taken down, an extension, being made
  // admin, a lock), each with Dismiss.
  const needs = (event.needs_you || []).filter(n => n.kind !== heroKind)
  main.innerHTML = `<div class="ph"><div class="t"><h1>${esc(ev.title)}</h1></div></div>`
    + needsCard(ui, 'participant', slug, needs)
    + '<div id="home-nudge"></div>'
    + (justPlaced ? `<div class="note ok"><b>You are on ${esc(me.team.name)} now.</b> <a href="${esc(teamHref)}">Meet your team</a></div>` : '')
    + `<div class="next" data-tour="next"><div class="t"><p class="k">Your next step</p><h2>${esc(heroTitle)}</h2>${heroNote ? `<p>${esc(heroNote)}</p>` : ''}</div>${heroLabel ? `<a class="btn primary" ${heroExternal ? 'target="_blank" rel="noopener" ' : ''}href="${esc(heroHref)}">${esc(heroLabel)}</a>` : ''}</div>`
    + entrySectionHTML(ctx, ev, showEntryCard ? (event.progress || noTeamProgress) : null)
    + rubricCard
    + news
    + participantDangerZone(ui, preview, ev)

  const nudgeHost = main.querySelector('#home-nudge')
  const paintNudge = () => { nudgeHost.innerHTML = nudgeHTML(ui, ev, rows, scheduleHref) }
  paintNudge()
  if (rows.length) keepNudgeFresh(ctx, nudgeHost, paintNudge)
  bindDismiss(main, client, ui, slug, () => { if (ctx.alive()) ctx.refresh() })
  if (showEntryCard && (event.progress || noTeamProgress)) wireEntrySection(ctx, ev, event.progress || noTeamProgress)

  const leaveBtn = main.querySelector('#leave-btn')
  if (leaveBtn && !leaveBtn.disabled) leaveBtn.addEventListener('click', () => openLeave({ api: client, ui, slug, title: ev.title, from: 'participant-home' }))

  // replay-participant-walkthrough: the shell's Help menu deep-links here
  // with &walkthrough=1 while Overview is the current screen; tour.js itself
  // checks the stored seen-state (localStorage -- see tours.js participant).
  const openWalk = ctx.params.get('walkthrough') === '1'
  if (openWalk) startTour('participant', ctx, { force: true })
  else if (!autoTourTried) { autoTourTried = true; startTour('participant', ctx) }
}
