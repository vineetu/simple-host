import { resolve as resolveTarget } from '../../../webassets/static/hack/navigation.js'
// Frame stand-in for internal/handler/static/hack/nav.js, swapped in by
// the esbuild OnResolve plugin in internal/mcp/apps/bundlegen for the
// organiser app only: this frame renders three sections in-frame
// (overview, people, judging -- hack-organiser-view.js's own click
// handler intercepts '#v=<section>'); everything else nav.js's organiser
// list has (teams, entries, results, event page, settings, the
// assignment grid -- none built yet) opens the real website instead,
// through ui/open-link, same as a link to an unrelated host.
//
// __ORIGIN__ is substituted by hack_apps.go's readHackApp before this
// bundle text reaches the page -- the one placeholder this frame carries
// (bundlegen's GenerateOrganiserJS fails generation if it is not exactly
// once); everything else here runs as committed. hack-organiser-view.js's
// own click handler also reads window.__hackOrgOrigin directly, for
// markup a screen builds by hand rather than through href().
window.__hackOrgOrigin = '__ORIGIN__'

// The three sections this frame actually renders in-frame, keyed by the
// real website's own section names (nav.js's organiser list uses
// 'overview', 'people', 'judging' already -- no alias needed here, unlike
// the judge frame's home/queue/scoring renaming).
const IN_FRAME = { overview: 1, people: 1, judging: 1 }

// nav.js's href(role, slug, section) returns a real page URL (/e/<slug>/
// manage?s=<section>) for the full organiser app; every organiser screen
// call here passes role 'organiser' for the one event already open in
// this frame.
export function href(role, slug, section) {
  if (role === 'organiser' && IN_FRAME[section || 'overview']) return '#v=' + (section || 'overview')
  return window.__hackOrgOrigin + '/e/' + encodeURIComponent(slug) + '/' + ({ organiser: 'manage', judge: 'judge', participant: 'team' }[role]) + (section ? '?s=' + encodeURIComponent(section) : '')
}

// isAppRoute only gates ui.js's full-page dirty-navigation guard
// (confirming a same-document SPA link vs. a real navigation); this frame
// has neither, so it is always false, same as the judge frame.
export function isAppRoute(path) {
  return false
}

export function resolve(id, slug, params) {
 const target = resolveTarget(id, slug, params)
 return { ...target, href: target.href.startsWith('/') ? window.__hackOrgOrigin + target.href : target.href }
}
