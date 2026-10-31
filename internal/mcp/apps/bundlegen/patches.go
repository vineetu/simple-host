package bundlegen

import (
	"fmt"
	"strings"
)

// patch is one named, documented, exact-match textual change applied to a
// source file before it is folded into a module. Each exists because
// either the real browser environment the website runs in (a real
// pathname, a same-origin document, a page the browser can navigate to, a
// <form> that can submit) does not hold inside a judge or organiser frame
// (an isolated, sandboxed iframe with its own, unrelated location, that
// the host -- not this app -- owns navigation for, and whose
// sandbox="allow-scripts" has no allow-forms), or a render left running
// past a newer navigation must not touch a `main` a different screen now
// owns (ctx.alive(), Codex review finding 4, SIM-344 phase 2). Every
// other line of every screen is reused unmodified -- the patches below
// are the only exceptions, one list shared by every Hack MCP App, and the
// website itself is never touched: patches apply only to the copy folded
// into the generated bundle.
type patch struct {
	file, from, to string
}

var patches = []patch{
	{
		file: "screens/judge-scoring.js",
		// The real page's own URL gets a ?team= query param so a reload or
		// Back lands on the same team; this frame has no such URL (its
		// location is the sandboxed document, not a page the judge can
		// reload or navigate with Back/Forward), and a sandboxed iframe
		// without allow-same-origin can reject history.replaceState with a
		// SecurityError. Harmless either way here, so it is best-effort.
		from: "    history.replaceState({}, '', url)",
		to:   "    try { history.replaceState({}, '', url) } catch (e) {}",
	},
	{
		file: "screens/public-judge.js",
		// The real page is served AT /judge/<code>, so the code is read
		// back out of the browser's own address bar. The frame is not
		// navigated there; the glue script (hack-judge-view.js) hands the
		// code over directly instead.
		from: "function codeFromPath() {\n  const m = location.pathname.match(/^\\/judge\\/([^/]+)\\/?$/)\n  return m ? decodeURIComponent(m[1]) : ''\n}",
		to:   "function codeFromPath() {\n  return window.__hackJudgeCode || ''\n}",
	},
	{
		file: "screens/public-judge.js",
		// On the real website, a sandbox="allow-scripts" frame cannot
		// navigate at all (no allow-top-navigation), so after accepting a
		// judge invite the real page's own location.assign('/e/<slug>/
		// judge') would either do nothing or, worse, navigate this
		// sandboxed iframe to that path on its own serving origin (a 404,
		// not the real app) -- either way the judge is dropped out of the
		// app they just joined through. Codex review (SIM-344): hand off
		// to the glue script instead, which has the one thing
		// location.assign was standing in for -- loading this event and
		// switching the frame to its queue view -- without leaving.
		from: "    location.assign('/e/' + encodeURIComponent(slug) + '/judge')",
		to:   "    window.__hackJudgeJoined(slug)",
	},
	{
		file: "screens/public-judge.js",
		// A sandbox="allow-scripts" frame has no allow-forms, so a <form>
		// submission is blocked by the browser itself before the page's
		// own `form.addEventListener('submit', ...)` handler -- e.prevent
		// Default() inside it -- ever runs: confirmed live (not by
		// reasoning about the sandbox spec) when the browser harness's own
		// "accept the invite" case hung with hack_join_judge never called
		// at all. passcode.js already hit this and documented the fix:
		// "Sandboxed frames may not allow form submission, so no submit
		// event" -- wire the button's own click instead of the form's
		// submit, which is exactly what this patch does. The submit
		// button stays type="submit" (harmless on the real page; the
		// button's own click listener fires there too, same effect).
		from: "  form.addEventListener('submit', async e => {\n    e.preventDefault()\n",
		to:   "  form.querySelector('#pj-submit').addEventListener('click', async e => {\n    e.preventDefault()\n",
	},
	{
		file: "screens/organiser-people.js",
		// location.hash reads the SANDBOXED FRAME'S OWN address, never set
		// by a real navigation here (this app has no browser address bar;
		// see hack-organiser-view.js's in-frame router) -- so this always
		// evaluated false, and the Overview screen's own "Sign-up
		// questions" link (organiser-overview.js, `href(...) + '#signup'`)
		// could never actually open the Sign-up section the way it does on
		// the real page. hack-organiser-view.js's click handler sets
		// window.__hackOrgSignupOpen instead, bracketing exactly the one
		// render() call that link triggers, then clears it.
		from: "  let signupOpen = location.hash === '#signup'",
		to:   "  let signupOpen = !!window.__hackOrgSignupOpen",
	},
	{
		file: "screens/organiser-people.js",
		from: "  if (location.hash === '#signup') main.querySelector('#signup')?.scrollIntoView()",
		to:   "  if (window.__hackOrgSignupOpen) main.querySelector('#signup')?.scrollIntoView()",
	},
	{
		file: "screens/organiser-people.js",
		// organiser-overview.js already checks ctx.alive() after its own
		// async /people+/applications fetch, before painting -- this screen
		// had no equivalent, so a render left running past a newer
		// navigation (Judging opened while People's own fetch was still in
		// flight) would go on to replace `main` with its own, now-stale
		// screen the instant its fetch resolved (Codex review finding 4,
		// SIM-344 phase 2).
		from: "  } catch (e) { registrationError = true }\n\n  function guardRegistration(btn) {",
		to:   "  } catch (e) { registrationError = true }\n  if (!ctx.alive()) return\n\n  function guardRegistration(btn) {",
	},
	{
		file: "screens/organiser-judging.js",
		// Same allow-forms gap as public-judge.js above, for the Unlock
		// reason form: wire the submit button's own click instead of the
		// form's submit event.
		from: "      form.addEventListener('submit', e => { e.preventDefault(); doUnlock(textarea.value, go) })",
		to:   "      go.addEventListener('click', e => { e.preventDefault(); doUnlock(textarea.value, go) })",
	},
	{
		file: "screens/organiser-judging.js",
		// Same allow-forms gap, for the "add a conflict" form: wire the Add
		// button's own click instead of the form's submit event.
		from: "      form.addEventListener('submit', e => { e.preventDefault(); addConflict(judgeSelect.value, teamSelect.value, add) })",
		to:   "      add.addEventListener('click', e => { e.preventDefault(); addConflict(judgeSelect.value, teamSelect.value, add) })",
	},
	{
		file: "screens/organiser-judging.js",
		// Same ctx.alive() gap as organiser-people.js above: this screen's
		// initial six-way Promise.all has no check of its own, so a render
		// superseded while it was in flight would go on to paint a stale
		// rubric/progress/conflicts screen over whatever newer view had
		// already taken `main` (Codex review finding 4, SIM-344 phase 2).
		from: "  if (rubricR.ok) rubric = (rubricR.data && rubricR.data.criteria) || []",
		to:   "  if (!ctx.alive()) return\n  if (rubricR.ok) rubric = (rubricR.data && rubricR.data.criteria) || []",
	},
}

func applyPatches(srcPath, body string) (string, error) {
	for _, p := range patches {
		if p.file != srcPath {
			continue
		}
		if !strings.Contains(body, p.from) {
			return "", fmt.Errorf("bundlegen: patch target not found in %s (the real screen changed; update the patch): %q", srcPath, p.from)
		}
		body = strings.Replace(body, p.from, p.to, 1)
	}
	return body, nil
}
