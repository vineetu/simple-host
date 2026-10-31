package bundlegen

import (
	"fmt"
	"strings"
)

// patch is one named, documented, exact-match textual change applied to a
// source file before it is folded into a module. Each exists because the
// real browser environment the website runs in (a real pathname, a
// same-origin document, a page the browser can navigate to, a <form> that
// can submit) does not hold inside the judge frame (an isolated,
// sandboxed iframe with its own, unrelated location, that the host -- not
// this app -- owns navigation for, and whose sandbox="allow-scripts" has
// no allow-forms). Every other line of every screen is reused unmodified
// -- these four are the only exceptions, and the website itself is never
// touched: patches apply only to the copy folded into the generated
// bundle.
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
