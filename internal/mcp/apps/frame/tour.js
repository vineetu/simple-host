// Frame stand-in for internal/handler/static/hack/tour.js, swapped in by
// the esbuild OnResolve plugin in internal/mcp/apps/bundlegen for both the
// judge and organiser frame: the guided walkthrough (judge-scoring.js and
// organiser-overview.js's own first-visit tour) is a full-page feature
// with room for a card next to a target element in a real sidebar/nav;
// neither frame has either -- one small view at a time, no sidebar. A
// no-op here, same effect as the stubs.js files this file replaces.
export function startTour() {}
