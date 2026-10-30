import { href } from '../nav.js'

export function title() { return 'Home' }

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
function justPlacedNote(slug, me) {
  const key = 'sh-seen-team-' + slug
  let seen = null
  try { seen = localStorage.getItem(key) } catch (e) {}
  const teamSlug = me.team ? me.team.slug : ''
  try { localStorage.setItem(key, teamSlug) } catch (e) {}
  return seen === '' && !!teamSlug
}

async function openTeamsNote(api, slug) {
  try {
    const r = await api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/open')
    if (!r.ok || !r.data || !r.data.enabled) return ''
    const n = (r.data.teams || []).length
    if (!n) return ''
    return n + ' team' + (n === 1 ? '' : 's') + ' ' + (n === 1 ? 'is' : 'are') + ' looking for members.'
  } catch (e) { return '' }
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

  const justPlaced = justPlacedNote(slug, me)

  let heroTitle, heroNote = '', heroLabel, heroHref, heroExternal = false
  if (!me.team && me.team_removal_notice) {
    heroTitle = esc(me.team_removal_notice)
    heroLabel = 'Find a team'
    heroHref = teamHref
  } else if (!me.team) {
    heroNote = await openTeamsNote(api, slug)
    heroTitle = 'Find a team to start building'
    heroLabel = 'Find a team'
    heroHref = teamHref
  } else if (ev.stage === 'results' || ev.results_published) {
    heroTitle = 'Results are out'
    heroLabel = 'See results'
    heroHref = resultsHref
  } else if (ev.stage === 'judging') {
    heroTitle = 'Judges are scoring now. Results come next'
    if (me.team.site && me.team.site.exists && me.team.site.url) {
      heroLabel = 'View your site'; heroHref = me.team.site.url; heroExternal = true
    } else {
      heroLabel = 'Go to your team'; heroHref = teamHref
    }
  } else if (ev.stage === 'closed') {
    heroTitle = 'Entries are in. Judging starts soon'
    if (me.team.site && me.team.site.exists && me.team.site.url) {
      heroLabel = 'View your site'; heroHref = me.team.site.url; heroExternal = true
    } else {
      heroLabel = 'Go to your team'; heroHref = teamHref
    }
  } else {
    const deadline = ev.submission_deadline ? ui.dateTime(ev.submission_deadline, ev) : ''
    heroTitle = deadline ? ('Keep building. Entries close ' + esc(deadline)) : ('You are on ' + esc(me.team.name) + '. Keep building')
    heroLabel = 'Go to your entry'
    heroHref = entryHref
  }

  const news = await announcementsCard(api, ui, announcementsHref, scheduleHref, slug, ev)

  main.innerHTML = `<div class="ph"><div class="t"><h1>${esc(ev.title)}</h1></div></div>`
    + (justPlaced ? `<div class="note ok"><b>You are on ${esc(me.team.name)} now.</b> <a href="${esc(teamHref)}">Meet your team</a></div>` : '')
    + `<div class="next"><div class="t"><p class="k">Your next step</p><h2>${heroTitle}</h2>${heroNote ? `<p>${esc(heroNote)}</p>` : ''}</div><a class="btn primary" ${heroExternal ? 'target="_blank" rel="noopener" ' : ''}href="${esc(heroHref)}">${esc(heroLabel)}</a></div>`
    + news
}
