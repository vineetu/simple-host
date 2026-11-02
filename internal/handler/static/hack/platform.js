// Platform adapter (SIM-344 phase 2 cleanup, 2026-11-02): the handful of
// browser behaviours a reused screen needs that do not hold the same way
// inside an embedded MCP App frame -- real navigation, the judge invite's
// own code, submitting a <form>, which section a link should open. This
// file is the website's own implementation: real history/location and
// native forms, unchanged from what judge-scoring.js, public-judge.js,
// organiser-people.js and organiser-judging.js did inline before this
// file existed. internal/mcp/apps/frame/platform.js is the MCP frame's
// swapped-in equivalent, selected by the same esbuild OnResolve plugin
// (internal/mcp/apps/bundlegen) that swaps nav.js and tour.js. Behaviour on
// this side is byte-for-byte what it replaced; see that file for the
// frame-side reasoning each function exists for.

// judge-scoring.js: when a reload or Back should land on the same team, a
// ?team= query param goes on the real page's own URL.
export function replaceUrl(url) {
  history.replaceState({}, '', url)
}

// public-judge.js: the real page is served AT /judge/<code>, so the code
// is read back out of the browser's own address bar.
export function judgeCode() {
  const m = location.pathname.match(/^\/judge\/([^/]+)\/?$/)
  return m ? decodeURIComponent(m[1]) : ''
}

// public-judge.js: after accepting the invite, take the judge to the real
// page for this event's queue.
export function judgeJoined(slug) {
  location.assign('/e/' + encodeURIComponent(slug) + '/judge')
}

// public-judge.js's accept form and organiser-judging.js's unlock/conflict
// forms: the website has a real <form>, so wiring the native submit event
// keeps Enter-to-submit working exactly as before. `button` is unused here
// (the frame implementation needs it instead of a submit event); `handler`
// is called with the submit event, and calls preventDefault itself, same
// as every form on this site.
export function onSubmit(form, button, handler) {
  form.addEventListener('submit', handler)
}

// organiser-people.js: the Sign-up section's open/scroll state, driven by
// the real page's own #signup URL hash.
export function wantsSection(name) {
  return location.hash === '#' + name
}
