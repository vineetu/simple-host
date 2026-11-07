import { navigation } from './navigation-data.js'
export { navigation }
export const nav = Object.fromEntries(navigation.roles.map(r => [r.id, r.nav]))

export function href(role, slug, section) {
  return '/e/' + encodeURIComponent(slug) + '/' + ({ organiser: 'manage', participant: 'team', judge: 'judge' }[role]) + (section ? '?s=' + encodeURIComponent(section) : '')
}
export { resolve } from './navigation.js'

export function normalizeURL(input) {
  const url = new URL(input.href || input, location.origin)
  if (!/^\/e\/[^/]+\/manage\/?$/.test(url.pathname)) return url
  const section = url.searchParams.get('s') || 'overview'
  const tab = url.searchParams.get('tab') || ''
  let anchor = ''
  try { anchor = decodeURIComponent(url.hash.slice(1)) } catch (_) {}
  const validTab = navigation.targets.some(t => t.section === section && t.tab === tab && tab)
  const alias = navigation.aliases.find(a => a.section === section && a.tab === tab && a.tab)
    || navigation.aliases.find(a => a.section === section && a.anchor === anchor && a.anchor && !(a.unless_valid_tab && validTab))
  if (alias) {
    const t = navigation.targets.find(t => t.id === alias.target)
    url.searchParams.set('s', t.section)
    if (t.tab) url.searchParams.set('tab', t.tab)
    else url.searchParams.delete('tab')
    url.hash = t.anchor || ''
  }
  return url
}

export function isAppRoute(path) {
  return /^\/(events(?:\/new)?|account|signin|(?:join|judge|organiser|team)\/[^/]+|e\/[^/]+\/(?:manage|team|judge|apply))\/?$/.test(path)
}
export function resolveRoute(url) {
  const path = url.pathname.replace(/\/+$/, '')
  if (path === '/events/new') return { id: 'organiser-create-event' }
  if (path === '/account') return { id: 'account' }
  // Team link (teams redesign, 2026-10-31): /team/{code}, same stand-alone
  // shape as the join/judge/organiser invite links above -- no `slug`, so
  // app.js's route() never runs the organiser/participant/judge
  // api.getEvent(match.slug) membership check for it (public-team-link.js
  // reads the code from location.pathname itself, the same way
  // public-join.js reads its code).
  const invite = path.match(/^\/(join|judge|organiser|team)\/([^/]+)$/)
  if (invite) return { id: {join:'public-join',judge:'public-judge',organiser:'public-organiser-invite',team:'public-team-link'}[invite[1]] }
  // Apply (SIM, owner 2026-10-31): keyed by the event's public slug, not a
  // secret code, but otherwise the same stand-alone-screen shape as the
  // invite links above -- no `slug` field, so app.js's route() never runs
  // its organiser/participant/judge api.getEvent(match.slug) membership
  // check for it (that call 404s or 401s for the stranger this screen is
  // for). public-apply.js reads the slug from location.pathname itself, the
  // same way public-join.js reads its code.
  if (/^\/e\/[^/]+\/apply$/.test(path)) return { id: 'public-apply' }
  const m = path.match(/^\/e\/([^/]+)\/(manage|team|judge)$/)
  if (!m) return { id: 'landing-your-events' }
  const role = {manage:'organiser',team:'participant',judge:'judge'}[m[2]]
  const sections = nav[role].map(x => x.id).concat(role === 'organiser' ? ['team','entry','assignments'] : role === 'judge' ? ['scoring'] : [])
  const wanted = new URLSearchParams(url.search).get('s')
  const section = sections.includes(wanted) ? wanted : sections[0]
  const alias = { 'organiser-team': 'organiser-team-detail', 'organiser-entry': 'organiser-entry-detail', 'organiser-assignments': 'organiser-judging-assignments' }
  const id = role + '-' + section
  // `role` here is only what the URL segment (manage/team/judge) implies --
  // app.js compares it against the signed-in person's real event.role and
  // redirects to their own area on a mismatch (Grok retest round 6,
  // Confusing #1: an organiser opening a participant's /team link used to
  // render that participant's screen instead of bouncing to /manage).
  return { id: alias[id] || id, section, slug: decodeURIComponent(m[1]), role }
}
