// Frame implementation of internal/handler/static/hack/platform.js -- the
// MCP App's own answers for the same adapter calls, swapped in by the
// esbuild OnResolve plugin in internal/mcp/apps/bundlegen for exactly the
// reasons the old bundlegen/patches.go used to patch these call sites by
// hand, one exact-text replacement at a time (SIM-344): this frame has no
// real address bar, is never navigated to /judge/<code>, and
// (sandbox="allow-scripts", no allow-forms) gets no native form
// submission at all.

// judge-scoring.js: a sandboxed iframe without allow-same-origin can
// reject history.replaceState with a SecurityError; harmless either way
// here (there is no URL for the judge to reload or go Back on), so this
// is best-effort.
export function replaceUrl(url) {
  try { history.replaceState({}, '', url) } catch (e) {}
}

// public-judge.js: this frame is never navigated to /judge/<code>; the
// glue script (hack-judge-view.js) hands the code over directly instead.
export function judgeCode() {
  return window.__hackJudgeCode || ''
}

// public-judge.js: a sandbox="allow-scripts" frame cannot navigate itself
// (no allow-top-navigation) -- the real page's own location.assign would
// either do nothing or navigate this sandboxed document to a 404 on its
// own serving origin, dropping the judge out of the app they just joined
// through. The glue script has the one thing that call was standing in
// for: loading this event and switching the frame to its queue view,
// in-frame, without leaving.
export function judgeJoined(slug) {
  window.__hackJudgeJoined(slug)
}

// public-judge.js's accept form and organiser-judging.js's unlock/conflict
// forms: a sandboxed frame with no allow-forms blocks native form
// submission before it ever reaches a listener -- confirmed live, not by
// reasoning about the sandbox spec, when "accept the invite" hung with
// hack_join_judge never called at all. Wire the submit button's own click
// instead; the button stays type="submit" so the website side (which does
// get a real submit event) is unaffected.
export function onSubmit(form, button, handler) {
  button.addEventListener('click', handler)
}

// organiser-people.js: this frame has no browser address bar, so
// location.hash is never set by a real navigation here. hack-organiser-
// view.js's click handler sets window.__hackOrgSignupOpen instead,
// bracketing exactly the one render() call Overview's "Sign-up questions"
// link triggers, then clears it. 'signup' is the only section name any
// screen in this bundle ever asks for.
export function wantsSection(name) {
  return name === 'signup' && !!window.__hackOrgSignupOpen
}
