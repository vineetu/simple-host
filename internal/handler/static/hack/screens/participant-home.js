import { href } from '../nav.js'

export function title() { return 'Home' }

// Fill teams (organiser-side) has no API signal that tells a participant
// "you were just placed" -- there is no feature id for this state (spec.json
// lists it without one) and no server field to read. This approximates it:
// remember the last team slug (or '' for no team) seen on this device for
// this event, and if the LAST recorded state was "no team" and now there is
// one, show the one-time note. A self-service join from the Team screen will
// also trigger it once; that is an accepted approximation given there is no
// way to tell the two apart without a server change.
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
    return ' ' + n + ' team' + (n === 1 ? '' : 's') + ' ' + (n === 1 ? 'is' : 'are') + ' looking for members.'
  } catch (e) { return '' }
}

async function announcementsCard(api, ui, href2, slug, ev) {
  try {
    const r = await api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/announcements')
    const items = (r.ok && r.data && r.data.announcements) || []
    if (!items.length) return ''
    const esc = ui.esc
    const rows = items.slice(0, 2).map(a =>
      `<p class="small muted">${esc(ui.dateTime(a.created_at, ev, { timeStyle: undefined }))}</p><h3>${esc(a.title)}</h3><p>${esc(a.body)}</p>`
    ).join('<hr class="rule">')
    return `<div class="card"><h2>Latest announcements</h2>${rows}<a class="btn" href="${esc(href2)}">See all</a></div>`
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

  let heroTitle, heroLabel, heroHref, heroExternal = false
  if (!me.team && me.team_removal_notice) {
    heroTitle = esc(me.team_removal_notice)
    heroLabel = 'Find a team'
    heroHref = teamHref
  } else if (!me.team) {
    const note = await openTeamsNote(api, slug)
    heroTitle = "You're not on a team yet." + note
    heroLabel = 'Find a team'
    heroHref = teamHref
  } else if (ev.stage === 'results' || ev.results_published) {
    heroTitle = 'Results are out.'
    heroLabel = 'See results'
    heroHref = resultsHref
  } else if (['closed', 'judging'].includes(ev.stage)) {
    heroTitle = "You're all set. Judging starts once entries close."
    if (me.team.site && me.team.site.exists && me.team.site.url) {
      heroLabel = 'View your site'; heroHref = me.team.site.url; heroExternal = true
    } else {
      heroLabel = 'Go to your team'; heroHref = teamHref
    }
  } else {
    heroTitle = "You're on " + esc(me.team.name) + '. Keep building before entries close.'
    heroLabel = 'Go to your entry'
    heroHref = entryHref
  }

  const next = ui.nextDeadline(ev)
  const comingUp = next
    ? `<div class="card"><h2>Coming up</h2><p>${esc(next[0])} <span class="clock">${esc(ui.dateTime(next[1], ev))}</span></p><a class="btn" href="${esc(scheduleHref)}">See schedule</a></div>`
    : ''

  const news = await announcementsCard(api, ui, announcementsHref, slug, ev)

  main.innerHTML = `<div class="ph"><div class="t"><h1>${esc(ev.title)}</h1><p>${esc(ui.stageLabel(ev.stage))}</p></div></div>`
    + (justPlaced ? `<div class="note ok"><b>The organiser placed you on ${esc(me.team.name)}</b><p><a href="${esc(teamHref)}">Say hello to your new teammates.</a></p></div>` : '')
    + `<div class="next"><div class="t"><p class="k">Your next step</p><h2>${heroTitle}</h2></div><a class="btn primary" ${heroExternal ? 'target="_blank" rel="noopener" ' : ''}href="${esc(heroHref)}">${esc(heroLabel)}</a></div>`
    + comingUp
    + news
    + `<div class="danger-zone"><h2>Leave this event</h2><div class="setting"><div class="t"><span>Leaves your team first, then removes you from the event. You can rejoin with the current join link. Planned, not available yet.</span></div><button class="btn danger" disabled>Leave this event</button></div></div>`
}
