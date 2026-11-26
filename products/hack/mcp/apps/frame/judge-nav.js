export { resolve } from '../legacy/navigation.js'
// Frame stand-in for the legacy screens' ./nav.js import (the old website's), swapped in by
// the esbuild OnResolve plugin in products/hack/mcp/apps/bundlegen for the
// judge app only: there is no sidebar or browser address bar here, just
// this frame's own four views, switched by hack-judge-view.js's own click
// handler intercepting '#v=' links (its VIEW_ALIAS maps the website's
// section names onto this frame's four views, so that alias lives there,
// not duplicated here) -- every section an href() call could ask for is
// answered the same simple way.
export function href(role, slug, section) {
  return '#v=' + section
}

// isAppRoute only gates ui.js's full-page dirty-navigation guard
// (confirming a same-document SPA link vs. a real navigation); this frame
// has neither, so it is always false.
export function isAppRoute(path) {
  return false
}
