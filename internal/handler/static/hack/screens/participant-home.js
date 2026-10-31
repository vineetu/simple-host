import { href } from '../nav.js'
import { startTour } from '../tour.js'
import { previewLeave, openLeave } from '../leave.js'

export function title() { return 'Overview' }

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

// Leave this event: danger zone last on the page. Copy per docs/ux-atlas's
// copy-deck -- the row text is deliberately generic for every team case
// (the atlas prototype shows the same "Your teammates keep the team..."
// line even on the lone-member state; only the dialog, built fresh from its
// own preview fetch in leave.js, says "you're the only member"). Returns ''
// (no section at all) if the preview couldn't be loaded -- never show a
// Leave button whose state we can't vouch for.
function participantDangerZone(ui, preview, ev) {
  if (!preview) return ''
  const b = 'Leave ' + ev.title
  let span, disabled = false
  if (preview.blocked) {
    disabled = true
    if (preview.blocked.code === 'submissions_closed') {
      const teamName = preview.team ? preview.team.name : 'your team'
      span = `Entries have closed, so your team can't change and you can't leave. Your name stays on the ${teamName} entry. If you need to leave, ask an organiser.`
    } else if (preview.blocked.code === 'event_closed') {
      span = 'This event has ended, so nobody can leave it. It stays on your Home as a record.'
    } else if (preview.blocked.code === 'event_taken_down') {
      span = 'This event has been taken down.'
    } else {
      span = preview.blocked.message || "You can't leave this event right now."
    }
  } else if (preview.team) {
    span = `You'll leave ${preview.team.name} and lose access to this event. Your teammates keep the team, its entry and its website.`
  } else if (preview.rejoin === 'join') {
    span = "You'll lose access to the event. You can join again while sign-up is open."
  } else {
    span = "You'll lose access to the event. Sign-up has closed, so you can't join again."
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

  const uid = (ctx.me && (ctx.me.id || ctx.me.username)) || 'anon'
  const justPlaced = justPlacedNote(uid, slug, me)

  // Teams can only be started, joined or changed while sign-up/building is
  // open and the entry deadline (if any) has not already passed -- matches
  // participant-team.js's teamsOpen/canStart so Overview never promises an
  // action that screen would refuse.
  const teamsOpen = (ev.stage === 'open' || ev.stage === 'building') && !ev.taken_down
  const deadlinePassed = !!(ev.submission_deadline && new Date(ev.submission_deadline) <= new Date())
  const deadline = ev.submission_deadline ? ui.dateTime(ev.submission_deadline, ev) : ''

  let heroTitle, heroNote = '', heroLabel = '', heroHref = '', heroExternal = false
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
  } else {
    heroTitle = teamDeadline ? ('Keep building. Entries close ' + teamDeadline) : ('You are on ' + me.team.name + '. Keep building')
    heroLabel = 'Go to your entry'
    heroHref = entryHref
  }

  const [news, preview] = await Promise.all([
    announcementsCard(api, ui, announcementsHref, scheduleHref, slug, ev),
    previewLeave(client, slug).then(r => (r.ok ? r.data : null)).catch(() => null)
  ])
  if (!ctx.alive()) return

  main.innerHTML = `<div class="ph"><div class="t"><h1>${esc(ev.title)}</h1></div></div>`
    + (justPlaced ? `<div class="note ok"><b>You are on ${esc(me.team.name)} now.</b> <a href="${esc(teamHref)}">Meet your team</a></div>` : '')
    + `<div class="next" data-tour="next"><div class="t"><p class="k">Your next step</p><h2>${esc(heroTitle)}</h2>${heroNote ? `<p>${esc(heroNote)}</p>` : ''}</div>${heroLabel ? `<a class="btn primary" ${heroExternal ? 'target="_blank" rel="noopener" ' : ''}href="${esc(heroHref)}">${esc(heroLabel)}</a>` : ''}</div>`
    + news
    + participantDangerZone(ui, preview, ev)

  const leaveBtn = main.querySelector('#leave-btn')
  if (leaveBtn && !leaveBtn.disabled) leaveBtn.addEventListener('click', () => openLeave({ api: client, ui, slug, title: ev.title, from: 'participant-home' }))

  // replay-participant-walkthrough: the shell's Help menu deep-links here
  // with &walkthrough=1 while Overview is the current screen; tour.js itself
  // checks the stored seen-state (localStorage -- see tours.js participant).
  const openWalk = ctx.params.get('walkthrough') === '1'
  if (openWalk) startTour('participant', ctx, { force: true })
  else if (!autoTourTried) { autoTourTried = true; startTour('participant', ctx) }
}
